// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// signInLinkOf makes a sign-in link for usr with key and returns its URL and the token in it.
func signInLinkOf(t *testing.T, s *Server, key, accID, usr string, body map[string]any) (string, string) {
	t.Helper()
	rec := call(t, s, "POST", "/api/v1/accounts/"+accID+"/users/"+usr+"/sign-in-links", key, body)
	wantStatus(t, rec, 201)
	out := decode[signInLinkJSON](t, rec)
	prefix := s.cfg.PublicURL + "/sign-in#token="
	if !strings.HasPrefix(out.URL, prefix) {
		t.Fatalf("url %q does not start with %q", out.URL, prefix)
	}
	tok := strings.TrimPrefix(out.URL, prefix)
	if !auth.ValidToken(tok) {
		t.Fatalf("url carries %q, not a bare token", tok)
	}
	if out.ExpiresAt.Before(time.Now()) || out.ExpiresAt.After(time.Now().Add(61*time.Second)) {
		t.Errorf("expires_at %v is not about 60s away", out.ExpiresAt)
	}
	return out.URL, tok
}

// redeem posts a sign-in link token with no credentials.
func redeem(t *testing.T, s *Server, tok string) *httptest.ResponseRecorder {
	t.Helper()
	return call(t, s, "POST", "/api/v1/auth/sign-in-link", "", map[string]any{"token": tok})
}

// mintToken mints a delegated token with key, for a user when usr is not empty, else for the key.
func mintToken(t *testing.T, s *Server, key, accID, fld, usr string) string {
	t.Helper()
	body := map[string]any{"folders": []string{fld}}
	if usr != "" {
		body["user"] = usr
	}
	return mintFor(t, s, key, accID, body)
}

// folderOf creates a team folder and returns its ID.
func folderOf(t *testing.T, s *Server, key, accID string) string {
	t.Helper()
	rec := call(t, s, "POST", "/api/v1/accounts/"+accID+"/folders", key, map[string]any{"name": "Shared"})
	wantStatus(t, rec, 201)
	return decode[struct {
		ID string `json:"id"`
	}](t, rec).ID
}

// newMemberUser makes an active member with a password and returns its ID.
func newMemberUser(t *testing.T, s *Server, key, accID, email string) string {
	t.Helper()
	u, _ := verifiedUser(t, s, key, accID, email)
	return u.ID
}

// setUserStatus sets a user's status through the API with key.
func setUserStatus(t *testing.T, s *Server, key, accID, usr, status string) {
	t.Helper()
	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+accID+"/users/"+usr, key,
		map[string]any{"status": status}), 200)
}

func TestSignInLinkWhoMayCreate(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	fld := folderOf(t, s, key, acc.ID)
	usr := newMemberUser(t, s, key, acc.ID, "mem@example.com")
	path := "/api/v1/accounts/" + acc.ID + "/users/" + usr + "/sign-in-links"

	signInLinkOf(t, s, key, acc.ID, usr, nil) // the account's own key: allowed

	wantError(t, call(t, s, "POST", path, admin, nil), 403, apierr.Forbidden)

	adminUser, adminSess := newAdminUser(t, s, admin, "root@example.com")
	_ = adminUser
	wantError(t, callSession(t, s, adminSess, "POST", path, map[string]any{}), 403, apierr.Forbidden)

	owner := makeOwner(t, s, key, acc.ID, "own@example.com")
	ownerSess := sessionFrom(t, s, login(t, s, "own@example.com", testPassword, ""))
	_ = owner
	wantError(t, callSession(t, s, ownerSess, "POST", path, map[string]any{}), 403, apierr.Forbidden)

	_, memSess := verifiedUser(t, s, key, acc.ID, "sess@example.com")
	wantError(t, callSession(t, s, memSess, "POST", path, map[string]any{}), 403, apierr.Forbidden)

	userTok := mintToken(t, s, key, acc.ID, fld, usr)
	wantError(t, call(t, s, "POST", path, userTok, nil), 403, apierr.Forbidden)

	keyTok := mintToken(t, s, key, acc.ID, fld, "")
	wantError(t, call(t, s, "POST", path, keyTok, nil), 403, apierr.Forbidden)
}

func TestSignInLinkRefusedTargets(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	other, otherKey := newAccount(t, s, admin, "Other")
	otherUsr := newMemberUser(t, s, otherKey, other.ID, "x@example.com")

	pending := call(t, s, "POST", "/api/v1/accounts/"+acc.ID+"/users", key,
		map[string]any{"email": "p@example.com", "verification": "link"})
	wantStatus(t, pending, 201)
	pendingID := decode[userResponse](t, pending).ID
	wantError(t, call(t, s, "POST", "/api/v1/accounts/"+acc.ID+"/users/"+pendingID+"/sign-in-links", key, nil),
		403, apierr.UserNotVerified)

	dis := newMemberUser(t, s, key, acc.ID, "d@example.com")
	setUserStatus(t, s, key, acc.ID, dis, "disabled")
	wantError(t, call(t, s, "POST", "/api/v1/accounts/"+acc.ID+"/users/"+dis+"/sign-in-links", key, nil),
		403, apierr.UserDisabled)

	wantError(t, call(t, s, "POST", "/api/v1/accounts/"+acc.ID+"/users/"+otherUsr+"/sign-in-links", key, nil),
		404, apierr.NotFound)
	wantError(t, call(t, s, "POST", "/api/v1/accounts/"+acc.ID+"/users/usr_missing/sign-in-links", key, nil),
		404, apierr.NotFound)

	// An installation admin is not in any account, so the account route cannot reach it: not_found.
	adminUser, _ := newAdminUser(t, s, admin, "root@example.com")
	wantError(t, call(t, s, "POST", "/api/v1/accounts/"+acc.ID+"/users/"+adminUser.ID+"/sign-in-links", key, nil),
		404, apierr.NotFound)

	// A suspended account: its key is refused at once, so the link route is not reached. Refused
	// as an account state, not as a bad key.
	okUsr := newMemberUser(t, s, key, acc.ID, "ok@example.com")
	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+acc.ID, admin, map[string]any{"status": "suspended"}), 200)
	wantError(t, call(t, s, "POST", "/api/v1/accounts/"+acc.ID+"/users/"+okUsr+"/sign-in-links", key, nil),
		403, apierr.AccountSuspended)
}

func TestSignInLinkNextValidation(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr := newMemberUser(t, s, key, acc.ID, "n@example.com")
	path := "/api/v1/accounts/" + acc.ID + "/users/" + usr + "/sign-in-links"

	for _, bad := range []string{"//evil.example", "https://evil.example", "/\\evil", "/.//evil.example",
		"files", "/a\tb", "/a\x01b", "/a b", "/a\x7fb", "/" + strings.Repeat("a", 2048), "/x?y=\n",
		// A browser reads %2e as a dot, so these resolve to //evil there although Go's resolver keeps them.
		"/%2e%2e//evil", "/%2e//evil", "/.%2e//evil", "/a/%2e%2e/%2e%2e//evil", "/%2E%2E//evil",
		"/%5cevil", "/%2f%2fevil"} {
		wantError(t, call(t, s, "POST", path, key, map[string]any{"next": bad}), 400, apierr.InvalidRequest)
	}
	// Accepted paths are kept as given and come back from the redeem answer.
	for _, good := range []string{"/files/x", "/files/x?q=1#top", "/n/nod_1"} {
		_, tok := signInLinkOf(t, s, key, acc.ID, usr, map[string]any{"next": good})
		rec := redeem(t, s, tok)
		wantStatus(t, rec, 200)
		if got := decode[signedInLink](t, rec).Next; got != good {
			t.Errorf("next = %q, want %q", got, good)
		}
	}
	// Empty or absent means /files.
	_, tok := signInLinkOf(t, s, key, acc.ID, usr, map[string]any{"next": ""})
	if got := decode[signedInLink](t, redeem(t, s, tok)).Next; got != "/files" {
		t.Errorf("empty next = %q, want /files", got)
	}
	_, tok = signInLinkOf(t, s, key, acc.ID, usr, nil)
	if got := decode[signedInLink](t, redeem(t, s, tok)).Next; got != "/files" {
		t.Errorf("absent next = %q, want /files", got)
	}
}

func TestSignInLinkSingleUse(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr := newMemberUser(t, s, key, acc.ID, "once@example.com")
	_, tok := signInLinkOf(t, s, key, acc.ID, usr, nil)
	wantStatus(t, redeem(t, s, tok), 200)
	wantError(t, redeem(t, s, tok), 400, apierr.InvalidRequest)
}

func TestSignInLinkRefusesUnknownMalformedAndExpired(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) {
		c.Users.SignInLinkTTL = config.Duration(50 * time.Millisecond)
	})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr := newMemberUser(t, s, key, acc.ID, "exp@example.com")

	wantError(t, redeem(t, s, "not-a-token"), 400, apierr.InvalidRequest)
	wantError(t, redeem(t, s, freshToken()), 400, apierr.InvalidRequest)

	_, tok := signInLinkOf(t, s, key, acc.ID, usr, nil)
	time.Sleep(120 * time.Millisecond)
	wantError(t, redeem(t, s, tok), 400, apierr.InvalidRequest)
}

func TestSignInLinkDisabledBetweenCreateAndUse(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr := newMemberUser(t, s, key, acc.ID, "dis@example.com")
	_, tok := signInLinkOf(t, s, key, acc.ID, usr, nil)

	setUserStatus(t, s, key, acc.ID, usr, "disabled")
	wantError(t, redeem(t, s, tok), 403, apierr.UserDisabled)

	// The refusal used the link up: re-enabling the user does not revive it.
	setUserStatus(t, s, key, acc.ID, usr, "active")
	wantError(t, redeem(t, s, tok), 400, apierr.InvalidRequest)
}

func TestSignInLinkStoredHashed(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr := newMemberUser(t, s, key, acc.ID, "h@example.com")
	_, tok := signInLinkOf(t, s, key, acc.ID, usr, nil)

	ctx := context.Background()
	if err := s.store.Write(ctx, func(tx *store.Tx) error {
		if _, err := tx.SignInLinkByHash(ctx, []byte(tok)); err == nil {
			t.Error("the plaintext token is stored as the hash")
		}
		l, err := tx.SignInLinkByHash(ctx, auth.HashToken(tok))
		if err != nil {
			t.Fatalf("hash lookup: %v", err)
		}
		if l.UserID != usr || l.AccountID != acc.ID {
			t.Errorf("link bound to %s/%s, want %s/%s", l.AccountID, l.UserID, acc.ID, usr)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSignInLinkStartsNormalSession(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr := newMemberUser(t, s, key, acc.ID, "sess@example.com")
	_, tok := signInLinkOf(t, s, key, acc.ID, usr, nil)

	rec := redeem(t, s, tok)
	wantStatus(t, rec, 200)
	sess := sessionFrom(t, s, rec)
	if sess.csrf == "" {
		t.Fatal("no CSRF token in the answer")
	}
	me := callSession(t, s, sess, "GET", "/api/v1/me", nil)
	wantStatus(t, me, 200)
	// A change without the CSRF token is refused.
	noCSRF := sess
	noCSRF.csrf = ""
	wantError(t, callSession(t, s, noCSRF, "PATCH", "/api/v1/me", map[string]any{"display_name": "X"}), 403, apierr.Forbidden)
	wantStatus(t, callSession(t, s, sess, "PATCH", "/api/v1/me", map[string]any{"display_name": "X"}), 200)
}

func TestSignInLinkReplacesExistingSession(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, old := verifiedUser(t, s, key, acc.ID, "old@example.com")
	usr := newMemberUser(t, s, key, acc.ID, "new@example.com")
	_, tok := signInLinkOf(t, s, key, acc.ID, usr, nil)

	rec := httptest.NewRequest("POST", "/api/v1/auth/sign-in-link", strings.NewReader(`{"token":"`+tok+`","confirm":true}`))
	rec.Header.Set("Content-Type", "application/json")
	rec.AddCookie(old.cookie)
	out := httptest.NewRecorder()
	s.Handler().ServeHTTP(out, rec)
	wantStatus(t, out, 200)
	fresh := sessionFrom(t, s, out)
	if fresh.cookie.Value == old.cookie.Value {
		t.Fatal("the session ID was not renewed")
	}
	wantError(t, callSession(t, s, old, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)
	wantStatus(t, callSession(t, s, fresh, "GET", "/api/v1/me", nil), 200)
}

func TestSignInLinkAuditHoldsNoToken(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr := newMemberUser(t, s, key, acc.ID, "aud@example.com")
	url, tok := signInLinkOf(t, s, key, acc.ID, usr, map[string]any{"next": "/files/secret-path"})
	wantStatus(t, redeem(t, s, tok), 200)
	wantError(t, redeem(t, s, tok), 400, apierr.InvalidRequest)

	rec := call(t, s, "GET", "/api/v1/accounts/"+acc.ID+"/audit", key, nil)
	wantStatus(t, rec, 200)
	body := rec.Body.String()
	for _, action := range []string{"user.sign_in_link", "auth.sign_in_link"} {
		if !strings.Contains(body, action) {
			t.Errorf("audit has no %s entry: %s", action, body)
		}
	}
	for _, leak := range []string{tok, url, "secret-path"} {
		if strings.Contains(body, leak) {
			t.Errorf("audit holds %q", leak)
		}
	}
}

func TestSignInLinkRateLimits(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.RateLimit.PerIPPerMinute = 2 })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr := newMemberUser(t, s, key, acc.ID, "rl@example.com")
	path := "/api/v1/accounts/" + acc.ID + "/users/" + usr + "/sign-in-links"

	for range 60 {
		wantStatus(t, call(t, s, "POST", path, key, nil), 201)
	}
	wantError(t, call(t, s, "POST", path, key, nil), 429, apierr.RateLimited)

	for range 2 {
		wantError(t, redeem(t, s, freshToken()), 400, apierr.InvalidRequest)
	}
	wantError(t, redeem(t, s, freshToken()), 429, apierr.RateLimited)
}

// freshToken returns a random token that no link was made for.
func freshToken() string {
	secret, _ := auth.NewToken()
	return secret
}

// TestSignInLinkSpentReplaysWriteNoAudit: replaying a link that is already used writes nothing, so a
// holder of a spent link cannot fill the audit log. The first, successful use is audited.
func TestSignInLinkSpentReplaysWriteNoAudit(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr := newMemberUser(t, s, key, acc.ID, "replay@example.com")
	_, tok := signInLinkOf(t, s, key, acc.ID, usr, nil)
	wantStatus(t, redeem(t, s, tok), 200)
	for i := 0; i < 3; i++ {
		wantError(t, redeem(t, s, tok), 400, apierr.InvalidRequest)
	}
	rec := call(t, s, "GET", "/api/v1/accounts/"+acc.ID+"/audit?action=auth.sign_in_link", key, nil)
	wantStatus(t, rec, 200)
	if n := len(decode[listResponse[auditJSON]](t, rec).Items); n != 1 {
		t.Errorf("auth.sign_in_link has %d entries after one use and three replays, want 1", n)
	}
}

// TestSignInLinkChunkedEmptyBody: a request with no body is the default path, whether or not the
// client sent a length.
func TestSignInLinkChunkedEmptyBody(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr := newMemberUser(t, s, key, acc.ID, "chunked@example.com")
	req := httptest.NewRequest("POST", "/api/v1/accounts/"+acc.ID+"/users/"+usr+"/sign-in-links", nil)
	req.Body = io.NopCloser(strings.NewReader(""))
	req.ContentLength = -1
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	wantStatus(t, rec, 201)
}

// TestSignInLinkConcurrentRedeem: one link redeemed at once from several browsers starts exactly one
// session.
func TestSignInLinkConcurrentRedeem(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr := newMemberUser(t, s, key, acc.ID, "race@example.com")
	_, tok := signInLinkOf(t, s, key, acc.ID, usr, nil)
	const n = 8
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/api/v1/auth/sign-in-link", strings.NewReader(`{"token":"`+tok+`"}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			codes[i] = rec.Code
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, c := range codes {
		if c == http.StatusOK {
			ok++
		}
	}
	if ok != 1 {
		t.Errorf("concurrent redeems answered %v, want exactly one 200", codes)
	}
}

// redeemAs redeems a sign-in link token from the browser sess (nil for no cookie), with confirm set as given.
func redeemAs(t *testing.T, s *Server, sess *testSession, tok string, confirm bool) *httptest.ResponseRecorder {
	t.Helper()
	req := jsonRequest(t, "POST", "/api/v1/auth/sign-in-link", map[string]any{"token": tok, "confirm": confirm})
	if sess != nil && sess.cookie != nil {
		req.AddCookie(sess.cookie)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// signedAsEmail returns the email of the user the session is signed in as, or "" when it is refused.
func signedAsEmail(t *testing.T, s *Server, sess testSession) string {
	t.Helper()
	rec := callSession(t, s, sess, "GET", "/api/v1/me", nil)
	if rec.Code != http.StatusOK {
		return ""
	}
	return decode[signedIn](t, rec).User.Email
}

// signInAuditCount counts the auth.sign_in_link audit entries of the account.
func signInAuditCount(t *testing.T, s *Server, key, accID string) int {
	t.Helper()
	rec := call(t, s, "GET", "/api/v1/accounts/"+accID+"/audit?action=auth.sign_in_link", key, nil)
	wantStatus(t, rec, 200)
	return len(decode[listResponse[auditJSON]](t, rec).Items)
}

// TestSignInLinkAsksBeforeSwitching: a browser signed in as A that redeems a link for B is not switched.
// The answer is 409 signed_in_as_other, naming both users. A keeps its session, and nothing is audited.
func TestSignInLinkAsksBeforeSwitching(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	aUser, a := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+acc.ID+"/users/"+aUser.ID, key,
		map[string]any{"display_name": "Ann Current"}), 200)
	bID := newMemberUser(t, s, key, acc.ID, "ben@example.com")
	_, tok := signInLinkOf(t, s, key, acc.ID, bID, nil)

	before := signInAuditCount(t, s, key, acc.ID)
	rec := redeemAs(t, s, &a, tok, false)
	e := wantError(t, rec, 409, apierr.Conflict)
	if got := e.Details["reason"]; got != "signed_in_as_other" {
		t.Errorf("reason = %v, want signed_in_as_other", got)
	}
	current, _ := e.Details["current"].(map[string]any)
	if current["name"] != "Ann Current" || current["email"] != "ann@example.com" {
		t.Errorf("current = %v, want the name and email of A", current)
	}
	target, _ := e.Details["target"].(map[string]any)
	if target["name"] != "ben@example.com" || target["email"] != "ben@example.com" {
		t.Errorf("target = %v, want B, named by its email when it has no display name", target)
	}
	if got := signedAsEmail(t, s, a); got != "ann@example.com" {
		t.Errorf("after the prompt the browser is signed in as %q, want A", got)
	}
	if n := signInAuditCount(t, s, key, acc.ID); n != before {
		t.Errorf("the 409 wrote %d auth.sign_in_link entries, want none", n-before)
	}
}

// TestSignInLinkSwitchOnConfirm: the prompt does not use the link, so confirming it signs the browser in as B,
// ends A's session, and consumes the link.
func TestSignInLinkSwitchOnConfirm(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, a := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	bID := newMemberUser(t, s, key, acc.ID, "ben@example.com")
	_, tok := signInLinkOf(t, s, key, acc.ID, bID, nil)

	wantError(t, redeemAs(t, s, &a, tok, false), 409, apierr.Conflict)
	rec := redeemAs(t, s, &a, tok, true)
	wantStatus(t, rec, 200)
	b := sessionFrom(t, s, rec)
	if got := signedAsEmail(t, s, b); got != "ben@example.com" {
		t.Errorf("confirmed switch signs in as %q, want B", got)
	}
	if got := signedAsEmail(t, s, a); got != "" {
		t.Errorf("A's old cookie still works as %q", got)
	}
	wantError(t, redeemAs(t, s, &b, tok, true), 400, apierr.InvalidRequest)
}

// TestSignInLinkSameUserNoPrompt: a browser already signed in as the link's user is signed in again at once.
func TestSignInLinkSameUserNoPrompt(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	same, old := verifiedUser(t, s, key, acc.ID, "same@example.com")
	_, tok := signInLinkOf(t, s, key, acc.ID, same.ID, nil)

	rec := redeemAs(t, s, &old, tok, false)
	wantStatus(t, rec, 200)
	fresh := sessionFrom(t, s, rec)
	if fresh.cookie.Value == old.cookie.Value {
		t.Fatal("the session ID was not renewed")
	}
	if got := signedAsEmail(t, s, old); got != "" {
		t.Errorf("the replaced session still works as %q", got)
	}
	if got := signedAsEmail(t, s, fresh); got != "same@example.com" {
		t.Errorf("fresh session is %q", got)
	}
}

// TestSignInLinkNoLiveSessionNoPrompt: a cookie that maps to no session, or to an expired one, changes nothing.
func TestSignInLinkNoLiveSessionNoPrompt(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, expired := verifiedUser(t, s, key, acc.ID, "old@example.com")
	bID := newMemberUser(t, s, key, acc.ID, "new@example.com")

	stale := testSession{cookie: &http.Cookie{Name: s.sessionCookie, Value: freshToken()}}
	_, tok := signInLinkOf(t, s, key, acc.ID, bID, nil)
	wantStatus(t, redeemAs(t, s, &stale, tok, false), 200)

	ctx := context.Background()
	if err := s.store.Write(ctx, func(tx *store.Tx) error {
		return tx.TouchSession(ctx, auth.HashToken(expired.cookie.Value), time.Now().Add(-time.Hour), time.Now().Add(-time.Minute))
	}); err != nil {
		t.Fatal(err)
	}
	_, tok = signInLinkOf(t, s, key, acc.ID, bID, nil)
	wantStatus(t, redeemAs(t, s, &expired, tok, false), 200)
}
