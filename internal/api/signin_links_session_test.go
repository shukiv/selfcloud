// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// sessionOfCookie reads the stored session a test browser's cookie names.
func sessionOfCookie(t *testing.T, s *Server, sess testSession) *store.Session {
	t.Helper()
	got, err := s.store.SessionByHash(context.Background(), auth.HashToken(sess.cookie.Value))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// TestSignInLinkSessionRefValidation: session_ref is 1 to 128 letters, digits, - or _, and anything else is refused
// before a link is made. Empty or absent means none.
func TestSignInLinkSessionRefValidation(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr := newMemberUser(t, s, key, acc.ID, "ref@example.com")
	path := "/api/v1/accounts/" + acc.ID + "/users/" + usr + "/sign-in-links"

	for _, bad := range []string{strings.Repeat("a", 129), "a b", "a/b", "é", "a.b", "a\tb"} {
		e := wantError(t, call(t, s, "POST", path, key, map[string]any{"session_ref": bad}), 400, apierr.InvalidRequest)
		if e.Message != "session_ref must be 1 to 128 letters, digits, - or _" {
			t.Errorf("ref %q: message %q", bad, e.Message)
		}
	}
	for _, good := range []string{"a", strings.Repeat("a", 128), "A-z_0-9", "app-one_2"} {
		wantStatus(t, call(t, s, "POST", path, key, map[string]any{"session_ref": good}), 201)
	}
	// Empty and absent mean none: the link is made and carries no ref.
	for _, body := range []map[string]any{nil, {}, {"session_ref": ""}} {
		_, tok := signInLinkOf(t, s, key, acc.ID, usr, body)
		rec := redeem(t, s, tok)
		wantStatus(t, rec, 200)
		if got := sessionOfCookie(t, s, sessionFrom(t, s, rec)); got.SessionRef != "" || !got.EndsAt.IsZero() {
			t.Errorf("plain link session ref %q ends %v, want none", got.SessionRef, got.EndsAt)
		}
	}
}

// TestSignInLinkSessionExpiresAtValidation: session_expires_at is an RFC 3339 time after now; a past or unparsable
// time is refused.
func TestSignInLinkSessionExpiresAtValidation(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr := newMemberUser(t, s, key, acc.ID, "ends@example.com")
	path := "/api/v1/accounts/" + acc.ID + "/users/" + usr + "/sign-in-links"

	for _, bad := range []string{time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), "garbage", "2026-13-40T99:99:99Z",
		time.Now().UTC().Format(time.RFC3339)} {
		wantError(t, call(t, s, "POST", path, key, map[string]any{"session_expires_at": bad}), 400, apierr.InvalidRequest)
	}
	wantStatus(t, call(t, s, "POST", path, key,
		map[string]any{"session_expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}), 201)
	// An unknown field is still refused.
	wantError(t, call(t, s, "POST", path, key, map[string]any{"session_end": "x"}), 400, apierr.InvalidRequest)
}

// TestSignInLinkSessionCarriesRefAndEnd: the redeemed session copies the link's ref and end, its expiry is the
// minimum of the ordinary expiry and the end, and its cookie's Max-Age is capped at the seconds left.
func TestSignInLinkSessionCarriesRefAndEnd(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr := newMemberUser(t, s, key, acc.ID, "carry@example.com")
	ends := time.Now().Add(30 * time.Second).UTC().Truncate(time.Second)
	_, tok := signInLinkOf(t, s, key, acc.ID, usr, map[string]any{
		"session_ref": "app-one", "session_expires_at": ends.Format(time.RFC3339)})

	rec := redeem(t, s, tok)
	wantStatus(t, rec, 200)
	sess := sessionFrom(t, s, rec)
	stored := sessionOfCookie(t, s, sess)
	if stored.SessionRef != "app-one" {
		t.Errorf("session ref %q, want app-one", stored.SessionRef)
	}
	if !stored.EndsAt.Equal(ends) {
		t.Errorf("session ends %v, want %v", stored.EndsAt, ends)
	}
	if !stored.ExpiresAt.Equal(ends) {
		t.Errorf("expires_at %v, want the end %v (the minimum)", stored.ExpiresAt, ends)
	}
	ck := cookieNamed(t, rec, s.sessionCookie)
	if ck.MaxAge < 1 || ck.MaxAge > 30 {
		t.Errorf("cookie Max-Age %d, want 1 to 30 (the seconds left until the end)", ck.MaxAge)
	}
	// The answer is unchanged: the user, the CSRF token and next.
	if decode[signedInLink](t, rec).Next != "/files" {
		t.Errorf("next changed")
	}
}

// TestSignInLinkSessionEndAfterIdle: an end further off than the idle limit does not lengthen the session; the
// ordinary expiry stays the minimum, and the cookie keeps the ordinary Max-Age.
func TestSignInLinkSessionEndAfterIdle(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr := newMemberUser(t, s, key, acc.ID, "far@example.com")
	ends := time.Now().Add(10 * 365 * 24 * time.Hour).UTC().Truncate(time.Second)
	_, tok := signInLinkOf(t, s, key, acc.ID, usr, map[string]any{
		"session_ref": "app-far", "session_expires_at": ends.Format(time.RFC3339)})
	rec := redeem(t, s, tok)
	wantStatus(t, rec, 200)
	stored := sessionOfCookie(t, s, sessionFrom(t, s, rec))
	if !stored.ExpiresAt.Before(ends) {
		t.Errorf("expires_at %v is not before the far end %v", stored.ExpiresAt, ends)
	}
	want := int(s.settings().Sessions.MaxAge.D().Seconds())
	if ck := cookieNamed(t, rec, s.sessionCookie); ck.MaxAge != want {
		t.Errorf("cookie Max-Age %d, want the ordinary %d", ck.MaxAge, want)
	}
}

// TestSessionPastEndsAtIsRefused: a session whose end has passed answers 401 although its idle and max age have not.
func TestSessionPastEndsAtIsRefused(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr := newMemberUser(t, s, key, acc.ID, "past@example.com")
	secret, hash := auth.NewToken()
	csrf, _ := auth.NewToken()
	now := store.Now()
	err := s.store.Write(context.Background(), func(tx *store.Tx) error {
		return tx.InsertSession(context.Background(), &store.Session{IDHash: hash, UserID: usr, AccountID: acc.ID,
			CSRFToken: csrf, CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour),
			SessionRef: "app-one", EndsAt: now.Add(-time.Minute)})
	})
	if err != nil {
		t.Fatal(err)
	}
	sess := testSession{cookie: &http.Cookie{Name: s.sessionCookie, Value: secret}, csrf: csrf}
	wantError(t, callSession(t, s, sess, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)
}

// TestSessionEndsAtOnTheFakeClock: the live check ends the session at its end, and a touch never moves expires_at
// past it.
func TestSessionEndsAtOnTheFakeClock(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr := newMemberUser(t, s, key, acc.ID, "clock@example.com")
	t0 := store.Now()
	clock := t0
	s.sessionClock = func() time.Time { return clock }

	secret, hash := auth.NewToken()
	csrf, _ := auth.NewToken()
	ends := t0.Add(2 * time.Hour)
	err := s.store.Write(context.Background(), func(tx *store.Tx) error {
		return tx.InsertSession(context.Background(), &store.Session{IDHash: hash, UserID: usr, AccountID: acc.ID,
			CSRFToken: csrf, CreatedAt: t0, LastSeenAt: t0, ExpiresAt: ends, SessionRef: "app-one", EndsAt: ends})
	})
	if err != nil {
		t.Fatal(err)
	}
	sess := testSession{cookie: &http.Cookie{Name: s.sessionCookie, Value: secret}, csrf: csrf}

	// An hour in, the session is live and the touch records the use; the expiry stays at the end, not an hour later.
	clock = t0.Add(time.Hour)
	wantStatus(t, callSession(t, s, sess, "GET", "/api/v1/me", nil), 200)
	stored := sessionOfCookie(t, s, sess)
	if !stored.ExpiresAt.Equal(ends) {
		t.Errorf("after the touch expires_at %v, want the end %v", stored.ExpiresAt, ends)
	}
	if !stored.LastSeenAt.Equal(clock) {
		t.Errorf("last_seen_at %v, want the touch time %v", stored.LastSeenAt, clock)
	}
	// A minute past the end, the idle limit is still far off, but the session is refused.
	clock = ends.Add(time.Minute)
	wantError(t, callSession(t, s, sess, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)
}

// TestPlainSessionCookieUnchanged: a session with no end keeps the ordinary cookie Max-Age.
func TestPlainSessionCookieUnchanged(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr := newMemberUser(t, s, key, acc.ID, "plain@example.com")
	_, tok := signInLinkOf(t, s, key, acc.ID, usr, nil)
	rec := redeem(t, s, tok)
	wantStatus(t, rec, 200)
	want := int(s.settings().Sessions.MaxAge.D().Seconds())
	if ck := cookieNamed(t, rec, s.sessionCookie); ck.MaxAge != want {
		t.Errorf("cookie Max-Age %d, want %d", ck.MaxAge, want)
	}
}
