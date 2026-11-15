// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// sessionsEndPath is the route that ends a user's web sessions.
func sessionsEndPath(accID, usr string) string {
	return "/api/v1/accounts/" + accID + "/users/" + usr + "/sessions/end"
}

// linkSession redeems a sign-in link with the given fields and returns the browser's session.
func linkSession(t *testing.T, s *Server, key, accID, usr string, fields map[string]any) testSession {
	t.Helper()
	_, tok := signInLinkOf(t, s, key, accID, usr, fields)
	rec := redeem(t, s, tok)
	wantStatus(t, rec, 200)
	return sessionFrom(t, s, rec)
}

// impersonationOf inserts an impersonation session of usr that began now, as the admin's own session parked.
func impersonationOf(t *testing.T, s *Server, accID, usr, adminID string, adminSess testSession) testSession {
	t.Helper()
	secret, hash := auth.NewToken()
	csrf, _ := auth.NewToken()
	now := store.Now()
	err := s.store.Write(context.Background(), func(tx *store.Tx) error {
		return tx.InsertSession(context.Background(), &store.Session{IDHash: hash, UserID: usr, AccountID: accID,
			CSRFToken: csrf, CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(impersonationTTL),
			ImpersonatorID: adminID, ImpersonatorSessionHash: auth.HashToken(adminSess.cookie.Value)})
	})
	if err != nil {
		t.Fatal(err)
	}
	return testSession{cookie: &http.Cookie{Name: s.sessionCookie, Value: secret}, csrf: csrf}
}

// clearSessions removes the sessions a user already holds (the one verifying the user signed in with), so a test
// starts from the sessions it makes itself. It is setup, not the route under test.
func clearSessions(t *testing.T, s *Server, usr string) {
	t.Helper()
	err := s.store.Write(context.Background(), func(tx *store.Tx) error {
		return tx.DeleteUserSessions(context.Background(), usr)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSessionsEndWhoMayCall(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	fld := folderOf(t, s, key, acc.ID)
	usr := newMemberUser(t, s, key, acc.ID, "end@example.com")
	clearSessions(t, s, usr)
	path := sessionsEndPath(acc.ID, usr)

	rec := call(t, s, "POST", path, key, nil)
	wantStatus(t, rec, 200)
	if got := decode[struct {
		Ended int64 `json:"ended"`
	}](t, rec).Ended; got != 0 {
		t.Errorf("ended %d, want 0", got)
	}

	adminUser, adminSess := newAdminUser(t, s, admin, "root@example.com")
	_ = adminUser
	makeOwner(t, s, key, acc.ID, "own@example.com")
	ownerSess := sessionFrom(t, s, login(t, s, "own@example.com", testPassword, ""))
	_, memSess := verifiedUser(t, s, key, acc.ID, "mem@example.com")
	userTok := mintToken(t, s, key, acc.ID, fld, usr)
	keyTok := mintToken(t, s, key, acc.ID, fld, "")
	_, devTok := approvedToken(t, s, memSess)

	for name, cred := range map[string]string{
		"admin key":      admin,
		"delegated user": userTok,
		"delegated key":  keyTok,
		"device":         devTok,
	} {
		e := wantError(t, call(t, s, "POST", path, cred, nil), 403, apierr.Forbidden)
		if e.Message != "only the account's own key may end a user's sessions" {
			t.Errorf("%s: message %q", name, e.Message)
		}
	}
	for name, sess := range map[string]testSession{
		"admin session":  adminSess,
		"owner session":  ownerSess,
		"member session": memSess,
	} {
		e := wantError(t, callSession(t, s, sess, "POST", path, nil), 403, apierr.Forbidden)
		if e.Message != "only the account's own key may end a user's sessions" {
			t.Errorf("%s: message %q", name, e.Message)
		}
	}
}

// TestSessionsEndByRef: with a ref, only the sessions of that ref end; the user's other sessions stay live.
func TestSessionsEndByRef(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr := newMemberUser(t, s, key, acc.ID, "byref@example.com")
	appOne := linkSession(t, s, key, acc.ID, usr, map[string]any{"session_ref": "app-one"})
	appTwo := linkSession(t, s, key, acc.ID, usr, map[string]any{"session_ref": "app-two"})
	password := sessionFrom(t, s, login(t, s, "byref@example.com", testPassword, ""))

	rec := call(t, s, "POST", sessionsEndPath(acc.ID, usr), key, map[string]any{"session_ref": "app-one"})
	wantStatus(t, rec, 200)
	if got := decode[struct {
		Ended int64 `json:"ended"`
	}](t, rec).Ended; got != 1 {
		t.Errorf("ended %d, want 1", got)
	}
	wantError(t, callSession(t, s, appOne, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)
	wantStatus(t, callSession(t, s, appTwo, "GET", "/api/v1/me", nil), 200)
	wantStatus(t, callSession(t, s, password, "GET", "/api/v1/me", nil), 200)
}

// TestSessionsEndAll: an empty body and {} end all the user's own sessions, a password one included; an
// impersonation of the user survives both, and another user's session is untouched.
func TestSessionsEndAll(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.Admin.TenantAccess = true })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr := newMemberUser(t, s, key, acc.ID, "all@example.com")
	clearSessions(t, s, usr)
	newMemberUser(t, s, key, acc.ID, "other@example.com")
	adminUser, adminSess := newAdminUser(t, s, admin, "root@example.com")
	otherSess := sessionFrom(t, s, login(t, s, "other@example.com", testPassword, ""))

	for _, body := range []map[string]any{nil, {}} {
		password := sessionFrom(t, s, login(t, s, "all@example.com", testPassword, ""))
		link := linkSession(t, s, key, acc.ID, usr, map[string]any{"session_ref": "app-one"})
		imp := impersonationOf(t, s, acc.ID, usr, adminUser.ID, adminSess)
		wantStatus(t, callSession(t, s, imp, "GET", "/api/v1/me", nil), 200)

		rec := call(t, s, "POST", sessionsEndPath(acc.ID, usr), key, body)
		wantStatus(t, rec, 200)
		if got := decode[struct {
			Ended int64 `json:"ended"`
		}](t, rec).Ended; got != 2 {
			t.Errorf("body %v: ended %d, want 2", body, got)
		}
		wantError(t, callSession(t, s, password, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)
		wantError(t, callSession(t, s, link, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)
		wantStatus(t, callSession(t, s, imp, "GET", "/api/v1/me", nil), 200)
		wantStatus(t, callSession(t, s, otherSess, "GET", "/api/v1/me", nil), 200)
	}
}

// TestSessionsEndKeepsTokens: ending the web sessions touches no token: the user's device token and an app token
// still work, and tokens_not_before does not move.
func TestSessionsEndKeepsTokens(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	fld := folderOf(t, s, key, acc.ID)
	usr := newMemberUser(t, s, key, acc.ID, "tokens@example.com")
	clearSessions(t, s, usr)
	browser := sessionFrom(t, s, login(t, s, "tokens@example.com", testPassword, ""))
	_, devTok := approvedToken(t, s, browser)
	appTok := mintToken(t, s, key, acc.ID, fld, "")
	ctx := context.Background()
	before, err := s.store.UserTokensNotBefore(ctx, acc.ID, usr)
	if err != nil {
		t.Fatal(err)
	}

	rec := call(t, s, "POST", sessionsEndPath(acc.ID, usr), key, nil)
	wantStatus(t, rec, 200)
	if got := decode[struct {
		Ended int64 `json:"ended"`
	}](t, rec).Ended; got != 1 {
		t.Errorf("ended %d, want 1", got)
	}
	wantError(t, callSession(t, s, browser, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)
	wantStatus(t, call(t, s, "GET", "/api/v1/folders", devTok, nil), 200)
	wantStatus(t, call(t, s, "GET", "/api/v1/folders/"+fld+"/children", appTok, nil), 200)
	after, err := s.store.UserTokensNotBefore(ctx, acc.ID, usr)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Equal(before) {
		t.Errorf("tokens_not_before moved from %v to %v", before, after)
	}
}

func TestSessionsEndTargets(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	other, otherKey := newAccount(t, s, admin, "Other")
	usr := newMemberUser(t, s, key, acc.ID, "target@example.com")
	otherUsr := newMemberUser(t, s, otherKey, other.ID, "x@example.com")
	adminUser, _ := newAdminUser(t, s, admin, "root@example.com")

	// Another account's key does not reach this account.
	wantError(t, call(t, s, "POST", sessionsEndPath(acc.ID, usr), otherKey, nil), 404, apierr.NotFound)
	// A user of another account is not found here.
	wantError(t, call(t, s, "POST", sessionsEndPath(acc.ID, otherUsr), key, nil), 404, apierr.NotFound)
	wantError(t, call(t, s, "POST", sessionsEndPath(acc.ID, "usr_missing"), key, nil), 404, apierr.NotFound)
	// An installation admin is not in any account.
	wantError(t, call(t, s, "POST", sessionsEndPath(acc.ID, adminUser.ID), key, nil), 404, apierr.NotFound)
	// A bad ref is refused before anything ends.
	for _, bad := range []string{strings.Repeat("a", 129), "a b", "a/b", "é"} {
		wantError(t, call(t, s, "POST", sessionsEndPath(acc.ID, usr), key, map[string]any{"session_ref": bad}),
			400, apierr.InvalidRequest)
	}
	// Unknown fields are refused.
	wantError(t, call(t, s, "POST", sessionsEndPath(acc.ID, usr), key, map[string]any{"ref": "app-one"}),
		400, apierr.InvalidRequest)
}

// TestSessionsEndSecondCallZero: a second call with nothing left to end answers {"ended": 0}.
func TestSessionsEndSecondCallZero(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr := newMemberUser(t, s, key, acc.ID, "twice@example.com")
	linkSession(t, s, key, acc.ID, usr, map[string]any{"session_ref": "app-one"})

	rec := call(t, s, "POST", sessionsEndPath(acc.ID, usr), key, map[string]any{"session_ref": "app-one"})
	wantStatus(t, rec, 200)
	if got := decode[struct {
		Ended int64 `json:"ended"`
	}](t, rec).Ended; got != 1 {
		t.Errorf("ended %d, want 1", got)
	}
	rec = call(t, s, "POST", sessionsEndPath(acc.ID, usr), key, map[string]any{"session_ref": "app-one"})
	wantStatus(t, rec, 200)
	if rec.Body.String() != "{\"ended\":0}\n" && rec.Body.String() != "{\"ended\":0}" {
		t.Errorf("second answer %q, want {\"ended\":0}", rec.Body.String())
	}
}

// TestSessionsEndAudit: the audit entry names the user and holds ref_given and ended, never the ref itself.
func TestSessionsEndAudit(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr := newMemberUser(t, s, key, acc.ID, "audit@example.com")
	clearSessions(t, s, usr)
	linkSession(t, s, key, acc.ID, usr, map[string]any{"session_ref": "app-secret-ref"})
	linkSession(t, s, key, acc.ID, usr, nil)

	wantStatus(t, call(t, s, "POST", sessionsEndPath(acc.ID, usr), key, map[string]any{"session_ref": "app-secret-ref"}), 200)
	wantStatus(t, call(t, s, "POST", sessionsEndPath(acc.ID, usr), key, nil), 200)

	rec := call(t, s, "GET", "/api/v1/accounts/"+acc.ID+"/audit?action=user.sessions_end", key, nil)
	wantStatus(t, rec, 200)
	items := decode[listResponse[auditJSON]](t, rec).Items
	if len(items) != 2 {
		t.Fatalf("audit has %d user.sessions_end entries, want 2", len(items))
	}
	byRef := map[bool]auditJSON{}
	for _, it := range items {
		if it.Target != usr {
			t.Errorf("entry targets %q, want %q", it.Target, usr)
		}
		refGiven, _ := it.Details["ref_given"].(bool)
		byRef[refGiven] = it
	}
	for _, refGiven := range []bool{true, false} {
		it, ok := byRef[refGiven]
		if !ok {
			t.Fatalf("no entry with ref_given=%v", refGiven)
		}
		if ended, _ := it.Details["ended"].(float64); ended != 1 {
			t.Errorf("ref_given=%v: ended %v, want 1", refGiven, it.Details["ended"])
		}
	}
	if strings.Contains(rec.Body.String(), "app-secret-ref") {
		t.Errorf("the audit holds the ref: %s", rec.Body.String())
	}
}

// TestSessionsEndRateLimit: the route is limited per account as the link create is, 60 a minute.
func TestSessionsEndRateLimit(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr := newMemberUser(t, s, key, acc.ID, "rl@example.com")
	path := sessionsEndPath(acc.ID, usr)
	for range 60 {
		wantStatus(t, call(t, s, "POST", path, key, nil), 200)
	}
	wantError(t, call(t, s, "POST", path, key, nil), 429, apierr.RateLimited)
}

// TestSessionsEndChunkedEmptyBody: a request with no body ends all, whether or not the client sent a length.
func TestSessionsEndChunkedEmptyBody(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr := newMemberUser(t, s, key, acc.ID, "chunked@example.com")
	browser := sessionFrom(t, s, login(t, s, "chunked@example.com", testPassword, ""))
	req := httptest.NewRequest("POST", sessionsEndPath(acc.ID, usr), nil)
	req.Body = io.NopCloser(strings.NewReader(""))
	req.ContentLength = -1
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	wantStatus(t, rec, 200)
	wantError(t, callSession(t, s, browser, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)
}

// TestSessionsEndEmptyRefEndsAll: an explicit empty ref means all, as an absent one does.
func TestSessionsEndEmptyRefEndsAll(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr := newMemberUser(t, s, key, acc.ID, "empty@example.com")
	clearSessions(t, s, usr)
	linkSession(t, s, key, acc.ID, usr, map[string]any{"session_ref": "app-one"})
	browser := sessionFrom(t, s, login(t, s, "empty@example.com", testPassword, ""))
	rec := call(t, s, "POST", sessionsEndPath(acc.ID, usr), key, map[string]any{"session_ref": ""})
	wantStatus(t, rec, 200)
	if got := decode[struct {
		Ended int64 `json:"ended"`
	}](t, rec).Ended; got != 2 {
		t.Errorf("ended %d, want 2", got)
	}
	wantError(t, callSession(t, s, browser, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)
}

// TestSessionsEndNeverEndsAnotherUser: the ref is matched within the user only.
func TestSessionsEndNeverEndsAnotherUser(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr := newMemberUser(t, s, key, acc.ID, "mine@example.com")
	other := newMemberUser(t, s, key, acc.ID, "theirs@example.com")
	mine := linkSession(t, s, key, acc.ID, usr, map[string]any{"session_ref": "app-one"})
	theirs := linkSession(t, s, key, acc.ID, other, map[string]any{"session_ref": "app-one"})

	rec := call(t, s, "POST", sessionsEndPath(acc.ID, usr), key, map[string]any{"session_ref": "app-one"})
	wantStatus(t, rec, 200)
	if got := decode[struct {
		Ended int64 `json:"ended"`
	}](t, rec).Ended; got != 1 {
		t.Errorf("ended %d, want 1", got)
	}
	wantError(t, callSession(t, s, mine, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)
	wantStatus(t, callSession(t, s, theirs, "GET", "/api/v1/me", nil), 200)
}
