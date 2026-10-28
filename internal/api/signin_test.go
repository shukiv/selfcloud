// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// testPassword is a valid password for test users.
const testPassword = "correct horse battery"

// newAuthServer is a test server with mail recorded in rec and rate limits high enough that the
// flows below do not trip them. Rate-limit tests lower the limits themselves.
func newAuthServer(t *testing.T, mailer mail.Sender, mutate ...func(*config.Config)) *Server {
	t.Helper()
	return newTestServerWith(t, mailer, append([]func(*config.Config){func(c *config.Config) {
		c.RateLimit.PerIPPerMinute = 1000
		c.RateLimit.PerEmailPerMinute = 1000
	}}, mutate...)...)
}

// testSession is a signed-in browser: its cookie and CSRF token.
type testSession struct {
	cookie *http.Cookie
	csrf   string
}

// sessionFrom reads the session cookie and CSRF token from a sign-in answer.
func sessionFrom(t *testing.T, s *Server, rec *httptest.ResponseRecorder) testSession {
	t.Helper()
	body := decode[signedIn](t, rec)
	for _, c := range rec.Result().Cookies() {
		if c.Name == s.sessionCookie {
			return testSession{cookie: &http.Cookie{Name: c.Name, Value: c.Value}, csrf: body.CSRFToken}
		}
	}
	t.Fatalf("no session cookie %q in %v", s.sessionCookie, rec.Header())
	return testSession{}
}

// callSession sends a request with the session. Requests that change state also get the site's
// Origin and the session's CSRF token, as a browser page would send them.
func callSession(t *testing.T, s *Server, sess testSession, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	req := jsonRequest(t, method, path, body)
	if sess.cookie != nil {
		req.AddCookie(sess.cookie)
	}
	if isUnsafe(method) {
		req.Header.Set("Origin", s.origin)
		req.Header.Set("X-CSRF-Token", sess.csrf)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// jsonRequest builds a request with an optional JSON body and no credentials.
func jsonRequest(t *testing.T, method, path string, body any) *http.Request {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

// tokenFromMail returns the token in the link of a mail.
func tokenFromMail(t *testing.T, m mail.Message) string {
	t.Helper()
	_, after, ok := strings.Cut(m.Body, "#token=")
	if !ok {
		t.Fatalf("no link in mail body %q", m.Body)
	}
	end := strings.IndexAny(after, " \r\n")
	if end < 0 {
		return after
	}
	return after[:end]
}

// verifiedUser creates a user with a verification link, verifies it with testPassword, and
// returns the user and the session the verification started.
func verifiedUser(t *testing.T, s *Server, key, accID, email string) (userResponse, testSession) {
	t.Helper()
	rec := call(t, s, "POST", "/api/v1/accounts/"+accID+"/users", key,
		map[string]any{"email": email, "verification": "link"})
	wantStatus(t, rec, 201)
	created := decode[userResponse](t, rec)
	rec = call(t, s, "POST", "/api/v1/auth/verify", "",
		map[string]any{"token": created.Verification.Token, "password": testPassword})
	wantStatus(t, rec, 200)
	return created, sessionFrom(t, s, rec)
}

// login posts a sign-in request with no credentials.
func login(t *testing.T, s *Server, email, password, account string) *httptest.ResponseRecorder {
	t.Helper()
	body := map[string]any{"email": email, "password": password}
	if account != "" {
		body["account"] = account
	}
	return call(t, s, "POST", "/api/v1/auth/login", "", body)
}

func TestVerifyLinkThenSignIn(t *testing.T) {
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")

	created := call(t, s, "POST", "/api/v1/accounts/"+acc.ID+"/users", key,
		map[string]any{"email": "ann@example.com", "display_name": "Ann", "verification": "link"})
	wantStatus(t, created, 201)
	cu := decode[userResponse](t, created)
	if cu.Status != store.UserPending || cu.Verification == nil || cu.Verification.Method != "link" {
		t.Fatalf("created %+v", cu)
	}
	if !strings.HasPrefix(cu.Verification.URL, "http://localhost:8080/verify#token=") ||
		!strings.HasSuffix(cu.Verification.URL, "#token="+cu.Verification.Token) {
		t.Fatalf("link %q does not carry token %q in its fragment", cu.Verification.URL, cu.Verification.Token)
	}
	if n := len(rec.Messages()); n != 0 {
		t.Fatalf("link method sent %d mails", n)
	}

	// A pending user cannot sign in: they have no password yet.
	wantError(t, login(t, s, "ann@example.com", testPassword, ""), 401, apierr.Unauthenticated)

	// Verifying without a password is refused while the user has none.
	wantError(t, call(t, s, "POST", "/api/v1/auth/verify", "", map[string]any{"token": cu.Verification.Token}),
		400, apierr.InvalidRequest)
	wantError(t, call(t, s, "POST", "/api/v1/auth/verify", "", map[string]any{"token": cu.Verification.Token,
		"password": "short"}), 400, apierr.InvalidRequest)

	rec2 := call(t, s, "POST", "/api/v1/auth/verify", "",
		map[string]any{"token": cu.Verification.Token, "password": testPassword})
	wantStatus(t, rec2, 200)
	sess := sessionFrom(t, s, rec2)
	if got := decode[signedIn](t, rec2).User; got.Status != store.UserActive || got.VerifiedAt == nil {
		t.Fatalf("verified user = %+v", got)
	}
	// The link works once.
	wantError(t, call(t, s, "POST", "/api/v1/auth/verify", "", map[string]any{"token": cu.Verification.Token,
		"password": testPassword}), 400, apierr.InvalidRequest)

	wantStatus(t, login(t, s, "ann@example.com", testPassword, ""), 200)
	wantError(t, login(t, s, "ann@example.com", "wrong password here", ""), 401, apierr.Unauthenticated)

	me := callSession(t, s, sess, "GET", "/api/v1/me", nil)
	wantStatus(t, me, 200)
	if got := decode[meResponse](t, me); got.User.Email != "ann@example.com" || got.Account.ID != acc.ID ||
		got.CSRFToken != sess.csrf {
		t.Fatalf("me = %+v", got)
	}

	// The audit log names the sign-in and the failed attempt.
	entries := decode[listResponse[auditJSON]](t, call(t, s, "GET", "/api/v1/accounts/"+acc.ID+"/audit", admin, nil))
	seen := map[string]bool{}
	for _, e := range entries.Items {
		seen[e.Action] = true
	}
	for _, want := range []string{"user.verify", "user.login", "user.login_failed", "user.create"} {
		if !seen[want] {
			t.Errorf("audit log has no %s", want)
		}
	}
}

func TestSessionCSRFAndPrecedence(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, _ := newAccount(t, s, admin, "Acme")
	_, sess := verifiedUser(t, s, admin, acc.ID, "ann@example.com")

	// An unsafe request needs the site's Origin and the CSRF token.
	noOrigin := jsonRequest(t, "POST", "/api/v1/auth/logout", nil)
	noOrigin.AddCookie(sess.cookie)
	noOrigin.Header.Set("X-CSRF-Token", sess.csrf)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, noOrigin)
	wantError(t, rec, 403, apierr.Forbidden)

	wrongToken := jsonRequest(t, "POST", "/api/v1/auth/logout", nil)
	wrongToken.AddCookie(sess.cookie)
	wrongToken.Header.Set("Origin", s.origin)
	wrongToken.Header.Set("X-CSRF-Token", "not-the-token")
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, wrongToken)
	wantError(t, rec, 403, apierr.Forbidden)

	foreign := jsonRequest(t, "POST", "/api/v1/auth/logout", nil)
	foreign.AddCookie(sess.cookie)
	foreign.Header.Set("Origin", "https://evil.example.net")
	foreign.Header.Set("X-CSRF-Token", sess.csrf)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, foreign)
	wantError(t, rec, 401, apierr.Unauthenticated)

	// Reads need only the cookie.
	wantStatus(t, callSession(t, s, testSession{cookie: sess.cookie}, "GET", "/api/v1/me", nil), 200)

	// A session cannot reach another account's users.
	other, _ := newAccount(t, s, admin, "Other")
	wantError(t, callSession(t, s, sess, "GET", "/api/v1/accounts/"+other.ID+"/users", nil), 404, apierr.NotFound)

	// A Bearer credential wins over the cookie: the admin key is used, not the session.
	req := jsonRequest(t, "GET", "/api/v1/accounts", nil)
	req.Header.Set("Authorization", "Bearer "+admin)
	req.AddCookie(sess.cookie)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	wantStatus(t, rec, 200)

	// Session-only routes refuse a Bearer credential.
	req = jsonRequest(t, "GET", "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+admin)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	wantError(t, rec, 401, apierr.Unauthenticated)

	// A malformed cookie is refused before any lookup.
	req = jsonRequest(t, "GET", "/api/v1/me", nil)
	req.AddCookie(&http.Cookie{Name: s.sessionCookie, Value: "short"})
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	wantError(t, rec, 401, apierr.Unauthenticated)

	// A session past its expiry is refused.
	err := s.store.Write(context.Background(), func(tx *store.Tx) error {
		return tx.TouchSession(context.Background(), auth.HashToken(sess.cookie.Value),
			time.Now().Add(-2*time.Hour), time.Now().Add(-time.Second))
	})
	if err != nil {
		t.Fatal(err)
	}
	wantError(t, callSession(t, s, testSession{cookie: sess.cookie}, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)
}

func TestSuspendedAccountEndsSession(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, _ := newAccount(t, s, admin, "Acme")
	_, sess := verifiedUser(t, s, admin, acc.ID, "ann@example.com")

	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+acc.ID, admin, map[string]any{"status": "suspended"}), 200)
	wantError(t, callSession(t, s, testSession{cookie: sess.cookie}, "GET", "/api/v1/me", nil), 403, apierr.AccountSuspended)
	wantError(t, login(t, s, "ann@example.com", testPassword, ""), 403, apierr.AccountSuspended)
}

func TestLoginOutcomes(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	ann, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")

	wantError(t, login(t, s, "nobody@example.com", testPassword, ""), 401, apierr.Unauthenticated)
	wantError(t, login(t, s, "ann@example.com", "wrong password here", ""), 401, apierr.Unauthenticated)
	wantStatus(t, login(t, s, "ANN@example.com", testPassword, ""), 200)

	// Another site cannot sign the browser in, even with a correct password.
	req := httptest.NewRequest("POST", "/api/v1/auth/login",
		strings.NewReader(`{"email":"ann@example.com","password":"`+testPassword+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://attacker.example.net")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	wantError(t, rec, 403, apierr.Forbidden)

	// A disabled user with the right password gets user_disabled, and their sessions end.
	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+acc.ID+"/users/"+ann.ID, key,
		map[string]any{"status": "disabled"}), 200)
	wantError(t, login(t, s, "ann@example.com", testPassword, ""), 403, apierr.UserDisabled)
	wantError(t, callSession(t, s, testSession{cookie: sess.cookie}, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)
}

func TestLoginAcrossAccounts(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	a, keyA := newAccount(t, s, admin, "Alpha")
	b, keyB := newAccount(t, s, admin, "Beta")
	verifiedUser(t, s, keyA, a.ID, "shared@example.com")
	verifiedUser(t, s, keyB, b.ID, "shared@example.com")

	// The password matched in both accounts, so the answer lists them.
	rec := login(t, s, "shared@example.com", testPassword, "")
	e := wantError(t, rec, 409, apierr.Conflict)
	choices, ok := e.Details["accounts"].([]any)
	if !ok || len(choices) != 2 {
		t.Fatalf("details = %v", e.Details)
	}
	// A wrong password gets the same refusal as any other, and no list.
	wantError(t, login(t, s, "shared@example.com", "wrong password here", ""), 401, apierr.Unauthenticated)

	wantStatus(t, login(t, s, "shared@example.com", testPassword, b.ID), 200)
	wantError(t, login(t, s, "shared@example.com", testPassword, "acc_unknown"), 401, apierr.Unauthenticated)
}

func TestForgotAndReset(t *testing.T) {
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, old := verifiedUser(t, s, key, acc.ID, "ann@example.com")

	// Unknown emails get the same 202, and no mail.
	forgot := func(email string) *httptest.ResponseRecorder {
		return call(t, s, "POST", "/api/v1/auth/password/forgot", "", map[string]any{"email": email})
	}
	wantStatus(t, forgot("nobody@example.com"), 202)
	wantStatus(t, forgot("ann@example.com"), 202)
	s.bg.Wait()
	msgs := rec.Messages()
	if len(msgs) != 1 || msgs[0].To != "ann@example.com" {
		t.Fatalf("mails = %+v", msgs)
	}
	token := tokenFromMail(t, msgs[0])

	wantStatus(t, call(t, s, "POST", "/api/v1/auth/password/reset", "", map[string]any{"token": token,
		"password": "a new passphrase"}), 200)
	// The old session is gone; the reset answered with a new one.
	wantError(t, callSession(t, s, testSession{cookie: old.cookie}, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)
	// The link works once.
	wantError(t, call(t, s, "POST", "/api/v1/auth/password/reset", "", map[string]any{"token": token,
		"password": "another passphrase"}), 400, apierr.InvalidRequest)

	wantError(t, login(t, s, "ann@example.com", testPassword, ""), 401, apierr.Unauthenticated)
	wantStatus(t, login(t, s, "ann@example.com", "a new passphrase", ""), 200)
}

func TestForgotWithoutMail(t *testing.T) {
	s := newAuthServer(t, nil)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	wantStatus(t, call(t, s, "POST", "/api/v1/auth/password/forgot", "", map[string]any{"email": "ann@example.com"}), 202)
	s.bg.Wait()
}

func TestChangePassword(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")

	wantError(t, callSession(t, s, sess, "POST", "/api/v1/me/password", map[string]any{
		"current_password": "wrong password here", "new_password": "a new passphrase"}), 403, apierr.Forbidden)

	rec := callSession(t, s, sess, "POST", "/api/v1/me/password", map[string]any{
		"current_password": testPassword, "new_password": "a new passphrase"})
	wantStatus(t, rec, 204)
	fresh := sessionFrom204(t, s, rec)
	if fresh.cookie.Value == sess.cookie.Value {
		t.Fatal("the session was not rotated")
	}
	wantError(t, callSession(t, s, testSession{cookie: sess.cookie}, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)
	// The answer has no body, so the new CSRF token comes from GET /me.
	me := decode[meResponse](t, callSession(t, s, testSession{cookie: fresh.cookie}, "GET", "/api/v1/me", nil))
	fresh.csrf = me.CSRFToken
	wantStatus(t, callSession(t, s, fresh, "POST", "/api/v1/auth/logout", nil), 204)
	wantError(t, callSession(t, s, fresh, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)
	wantStatus(t, login(t, s, "ann@example.com", "a new passphrase", ""), 200)
}

// sessionFrom204 reads the rotated cookie from an answer that has no body.
func sessionFrom204(t *testing.T, s *Server, rec *httptest.ResponseRecorder) testSession {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == s.sessionCookie {
			return testSession{cookie: &http.Cookie{Name: c.Name, Value: c.Value}}
		}
	}
	t.Fatal("no rotated session cookie")
	return testSession{}
}

func TestCookieAttributes(t *testing.T) {
	// Plain http (development): no __Host- prefix and no Secure.
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	created := call(t, s, "POST", "/api/v1/accounts/"+acc.ID+"/users", key,
		map[string]any{"email": "ann@example.com", "verification": "link"})
	token := decode[userResponse](t, created).Verification.Token
	rec := call(t, s, "POST", "/api/v1/auth/verify", "", map[string]any{"token": token, "password": testPassword})
	wantStatus(t, rec, 200)
	c := cookieNamed(t, rec, "selfcloud_session")
	if c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.MaxAge <= 0 {
		t.Errorf("http cookie = %+v", c)
	}

	// Under https the cookie is __Host-prefixed and Secure.
	secure := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.PublicURL = "https://drive.example.com" })
	admin = adminKey(t, secure)
	acc, key = newAccount(t, secure, admin, "Acme")
	created = call(t, secure, "POST", "/api/v1/accounts/"+acc.ID+"/users", key,
		map[string]any{"email": "ann@example.com", "verification": "link"})
	token = decode[userResponse](t, created).Verification.Token
	rec = call(t, secure, "POST", "/api/v1/auth/verify", "", map[string]any{"token": token, "password": testPassword})
	wantStatus(t, rec, 200)
	c = cookieNamed(t, rec, "__Host-selfcloud_session")
	if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
		t.Errorf("https cookie = %+v", c)
	}
	if !strings.HasPrefix(created.Header().Get("Location"), "/api/v1/accounts/") {
		t.Errorf("Location = %q", created.Header().Get("Location"))
	}
}

func cookieNamed(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no cookie %s", name)
	return nil
}

func TestLoginRateLimit(t *testing.T) {
	s := newTestServerWith(t, &mail.Recorder{}, func(c *config.Config) {
		c.RateLimit.PerIPPerMinute = 100
		c.RateLimit.PerEmailPerMinute = 2
	})
	for i := 0; i < 2; i++ {
		wantError(t, login(t, s, "ann@example.com", "wrong password here", ""), 401, apierr.Unauthenticated)
	}
	rec := login(t, s, "ann@example.com", "wrong password here", "")
	wantError(t, rec, 429, apierr.RateLimited)
	if rec.Header().Get("Retry-After") == "" {
		t.Error("no Retry-After on rate_limited")
	}
	// Another email has its own bucket.
	wantError(t, login(t, s, "bob@example.com", "wrong password here", ""), 401, apierr.Unauthenticated)
}

func TestVerificationRateLimitIsPerAddress(t *testing.T) {
	s := newTestServerWith(t, nil, func(c *config.Config) { c.RateLimit.PerIPPerMinute = 2 })
	for i := 0; i < 2; i++ {
		wantError(t, call(t, s, "POST", "/api/v1/auth/verify", "", map[string]any{"token": "x"}), 400, apierr.InvalidRequest)
	}
	wantError(t, call(t, s, "POST", "/api/v1/auth/verify", "", map[string]any{"token": "x"}), 429, apierr.RateLimited)
}
