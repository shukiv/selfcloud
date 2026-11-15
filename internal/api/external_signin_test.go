// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
)

// The installation's outside sign-in (decision 185): GET /login answers a redirect to the outside
// sign-in page, /info names the settings, and password sign-in off refuses every account that is
// not an installation admin's, with the wrong-password answer.

const outsideSignIn = "https://app.example.com/sign-in?next={next}"

// loginRedirect reads GET /login's answer: its status, Location and Cache-Control.
func loginRedirect(t *testing.T, s *Server, query string) (int, string, string) {
	t.Helper()
	rec := do(t, s.Handler(), "GET", "/login"+query)
	return rec.Code, rec.Header().Get("Location"), rec.Header().Get("Cache-Control")
}

func TestLoginRedirectsToOutsideSignIn(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, func(c *config.Config) { c.Users.ExternalSignInURL = outsideSignIn })

	// A good next is escaped into {next}, exactly as the sign-in links' rule accepts it.
	next := "/files/a%20b?x=1"
	code, loc, cache := loginRedirect(t, s, "?next="+url.QueryEscape(next))
	if code != http.StatusFound {
		t.Fatalf("GET /login = %d, want 302", code)
	}
	if want := "https://app.example.com/sign-in?next=" + url.QueryEscape(next); loc != want {
		t.Errorf("Location = %q, want %q", loc, want)
	}
	if cache != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cache)
	}

	// Each refusal of the links' next rule, and an absent next, go to the root.
	for _, raw := range []string{
		"//h",
		"/\\h",
		"/%2e%2e//h",
		"/%2f/h",
		"/\t/h",
		"https://h/",
		"/" + strings.Repeat("a", 2048),
		"",
	} {
		_, loc, _ := loginRedirect(t, s, "?next="+url.QueryEscape(raw))
		if want := "https://app.example.com/sign-in?next=" + url.QueryEscape("/"); loc != want {
			t.Errorf("next %q: Location = %q, want %q", raw, loc, want)
		}
	}
	_, loc, _ = loginRedirect(t, s, "")
	if want := "https://app.example.com/sign-in?next=" + url.QueryEscape("/"); loc != want {
		t.Errorf("no next: Location = %q, want %q", loc, want)
	}

	// ?password, with any value, keeps the page: the admin's way in.
	for _, query := range []string{"?password", "?password=", "?password=1", "?password&next=%2Ffiles"} {
		if code, loc, _ := loginRedirect(t, s, query); code != http.StatusOK || loc != "" {
			t.Errorf("GET /login%s = %d (Location %q), want the page", query, code, loc)
		}
	}

	// A URL with no {next} is used as it is.
	plain := newTestServer(t, func(c *config.Config) { c.Users.ExternalSignInURL = "https://app.example.com/sign-in" })
	if _, loc, _ := loginRedirect(t, plain, "?next=%2Ffiles"); loc != "https://app.example.com/sign-in" {
		t.Errorf("Location = %q, want the URL as it is", loc)
	}

	// Off, the page is served as today, whatever the query.
	off := newTestServer(t)
	for _, query := range []string{"", "?next=%2Ffiles"} {
		if code, loc, _ := loginRedirect(t, off, query); code != http.StatusOK || loc != "" {
			t.Errorf("setting off: GET /login%s = %d (Location %q), want the page", query, code, loc)
		}
	}

	// Only /login redirects: the app's other addresses keep the page.
	if code, _, _ := loginRedirect(t, s, ""); code != http.StatusFound {
		t.Fatalf("GET /login = %d, want 302", code)
	}
	rec := do(t, s.Handler(), "GET", "/files")
	if rec.Code != http.StatusOK || rec.Header().Get("Location") != "" {
		t.Errorf("GET /files = %d (Location %q), want the page", rec.Code, rec.Header().Get("Location"))
	}
}

func TestInfoSignIn(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	got := decode[infoResponse](t, do(t, s.Handler(), "GET", "/api/v1/info"))
	if got.SignIn != (infoSignIn{Password: true}) {
		t.Errorf("sign_in = %+v, want the defaults (empty URLs, password true)", got.SignIn)
	}

	set := newTestServer(t, func(c *config.Config) {
		c.Users.ExternalSignInURL = outsideSignIn
		c.Users.ExternalSignedOutURL = "https://app.example.com/signed-out"
		c.Users.PasswordSignIn = false
	})
	got = decode[infoResponse](t, do(t, set.Handler(), "GET", "/api/v1/info"))
	want := infoSignIn{ExternalURL: outsideSignIn, SignedOutURL: "https://app.example.com/signed-out", Password: false}
	if got.SignIn != want {
		t.Errorf("sign_in = %+v, want %+v", got.SignIn, want)
	}
}

// A settings write of the outside sign-in takes effect at once: a bad URL is refused naming the
// key, and a good one turns the redirect on without a restart.
func TestExternalSignInSettingsWrite(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	admin := adminKey(t, s)

	rec := call(t, s, "PATCH", "/api/v1/settings", admin, map[string]any{
		"set": map[string]any{"users.external_sign_in_url": "http://app.example.com/sign-in"},
	})
	e := wantError(t, rec, 400, apierr.InvalidRequest)
	if e.Details["reason"] != "invalid_setting" {
		t.Errorf("reason = %v, want invalid_setting", e.Details["reason"])
	}
	if keys, _ := e.Details["keys"].([]any); len(keys) != 1 || keys[0] != "users.external_sign_in_url" {
		t.Errorf("keys = %v, want users.external_sign_in_url", e.Details["keys"])
	}
	if code, _, _ := loginRedirect(t, s, ""); code != http.StatusOK {
		t.Errorf("after a refused write GET /login = %d, want the page", code)
	}

	wantStatus(t, call(t, s, "PATCH", "/api/v1/settings", admin, map[string]any{
		"set": map[string]any{
			"users.external_sign_in_url":    outsideSignIn,
			"users.external_signed_out_url": "https://app.example.com/signed-out",
			"users.password_sign_in":        false,
		},
	}), 200)
	if _, loc, _ := loginRedirect(t, s, ""); loc != "https://app.example.com/sign-in?next=%2F" {
		t.Errorf("after the write Location = %q, want the redirect", loc)
	}
	got := decode[infoResponse](t, do(t, s.Handler(), "GET", "/api/v1/info"))
	if got.SignIn.ExternalURL != outsideSignIn || got.SignIn.SignedOutURL != "https://app.example.com/signed-out" || got.SignIn.Password {
		t.Errorf("info sign_in = %+v after the write", got.SignIn)
	}

	// Reset puts the defaults back, and the page returns.
	wantStatus(t, call(t, s, "PATCH", "/api/v1/settings", admin, map[string]any{
		"reset": []string{"users.external_sign_in_url", "users.external_signed_out_url", "users.password_sign_in"},
	}), 200)
	if code, _, _ := loginRedirect(t, s, ""); code != http.StatusOK {
		t.Errorf("after the reset GET /login = %d, want the page", code)
	}
}

// Password sign-in off refuses a member's right password with the wrong-password answer, keeps an
// installation admin's sign-in, sends a member no reset mail, and leaves sign-in links alone.
func TestPasswordSignInOff(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec, func(c *config.Config) { c.Users.PasswordSignIn = false })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	memberID := newMemberUser(t, s, key, acc.ID, "mateo@example.com")
	newAdminUser(t, s, admin, "ada@example.com")

	// The member's right password is refused exactly as a wrong one: same status, code and message.
	wrong := login(t, s, "mateo@example.com", "not the password", "")
	right := login(t, s, "mateo@example.com", testPassword, "")
	if wrong.Code != right.Code {
		t.Fatalf("right password = %d, wrong = %d", right.Code, wrong.Code)
	}
	wantError(t, right, 401, apierr.Unauthenticated)
	we, re := decodeError(t, wrong), decodeError(t, right)
	if we.Code != re.Code || we.Message != re.Message {
		t.Errorf("right password answers %+v, wrong %+v; want the same", re, we)
	}
	// An unknown email keeps the same answer too.
	unknown := login(t, s, "nobody@example.com", testPassword, "")
	if ue := decodeError(t, unknown); unknown.Code != 401 || ue.Message != re.Message {
		t.Errorf("unknown email answers %d %+v, want the wrong-password answer", unknown.Code, ue)
	}

	// The installation admin keeps password sign-in.
	wantStatus(t, login(t, s, "ada@example.com", testPassword, ""), 200)

	// Forgot answers its neutral success and sends the member nothing; the admin still gets a mail.
	wantStatus(t, call(t, s, "POST", "/api/v1/auth/password/forgot", "", map[string]any{"email": "mateo@example.com"}), 202)
	s.bg.Wait()
	if msgs := rec.Messages(); len(msgs) != 0 {
		t.Fatalf("forgot sent %d mails to the member, want none", len(msgs))
	}
	wantStatus(t, call(t, s, "POST", "/api/v1/auth/password/forgot", "", map[string]any{"email": "ada@example.com"}), 202)
	s.bg.Wait()
	if msgs := rec.Messages(); len(msgs) != 1 || msgs[0].To != "ada@example.com" {
		t.Fatalf("mails = %+v, want one to the admin", msgs)
	}

	// A sign-in link still signs the member in: the setting is about passwords only.
	_, tok := signInLinkOf(t, s, key, acc.ID, memberID, nil)
	wantStatus(t, redeem(t, s, tok), 200)
}

// With password sign-in on (the default) nothing changes: the member's right password signs in.
func TestPasswordSignInOnByDefault(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	newMemberUser(t, s, key, acc.ID, "mateo@example.com")
	wantStatus(t, login(t, s, "mateo@example.com", testPassword, ""), 200)
}
