// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/mail"
)

// keptMaxAge is the Max-Age of a kept session cookie: the configured sessions.max_age in seconds.
func keptMaxAge(s *Server) int {
	return int(s.cfg.Sessions.MaxAge.D().Seconds())
}

// signInKeeping signs in with the password and the "Keep me signed in" choice, the way the sign-in page sends it.
func signInKeeping(t *testing.T, s *Server, email string, remember bool) testSession {
	t.Helper()
	rec := call(t, s, "POST", "/api/v1/auth/login", "",
		map[string]any{"email": email, "password": testPassword, "remember": remember})
	wantStatus(t, rec, 200)
	return sessionFrom(t, s, rec)
}

// TestRememberChoosesTheCookieLifetime: unticked gives a browser-session cookie (no Max-Age, no Expires); ticked
// and left out give the kept cookie with the configured Max-Age, as before the choice existed.
func TestRememberChoosesTheCookieLifetime(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	for _, c := range []struct {
		name string
		body map[string]any
		kept bool
	}{
		{"unticked", map[string]any{"remember": false}, false},
		{"ticked", map[string]any{"remember": true}, true},
		{"left out", map[string]any{}, true},
	} {
		body := map[string]any{"email": "ann@example.com", "password": testPassword}
		for k, v := range c.body {
			body[k] = v
		}
		rec := call(t, s, "POST", "/api/v1/auth/login", "", body)
		wantStatus(t, rec, 200)
		ck := cookieNamed(t, rec, s.sessionCookie)
		if c.kept && ck.MaxAge != keptMaxAge(s) {
			t.Errorf("%s: Max-Age = %d, want %d", c.name, ck.MaxAge, keptMaxAge(s))
		}
		if !c.kept && (ck.MaxAge != 0 || !ck.Expires.IsZero()) {
			t.Errorf("%s: cookie %+v has a lifetime; want a browser-session cookie", c.name, ck)
		}
	}
}

// TestVerifiedAndResetSessionsAreNotKept: the person chose nothing when they followed a verification or a reset
// link, so their session is a browser-session one.
func TestVerifiedAndResetSessionsAreNotKept(t *testing.T) {
	t.Parallel()
	mailer := &mail.Recorder{}
	s := newAuthServer(t, mailer)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	created := call(t, s, "POST", "/api/v1/accounts/"+acc.ID+"/users", key,
		map[string]any{"email": "ann@example.com", "verification": "link"})
	token := decode[userResponse](t, created).Verification.Token
	rec := call(t, s, "POST", "/api/v1/auth/verify", "", map[string]any{"token": token, "password": testPassword})
	wantStatus(t, rec, 200)
	if ck := cookieNamed(t, rec, s.sessionCookie); ck.MaxAge != 0 || !ck.Expires.IsZero() {
		t.Errorf("verify cookie %+v has a lifetime; want a browser-session cookie", ck)
	}

	wantStatus(t, call(t, s, "POST", "/api/v1/auth/password/forgot", "", map[string]any{"email": "ann@example.com"}), 202)
	s.bg.Wait()
	msgs := mailer.Messages()
	if len(msgs) != 1 {
		t.Fatalf("mails = %+v", msgs)
	}
	rec = call(t, s, "POST", "/api/v1/auth/password/reset", "",
		map[string]any{"token": tokenFromMail(t, msgs[0]), "password": "a new passphrase"})
	wantStatus(t, rec, 200)
	if ck := cookieNamed(t, rec, s.sessionCookie); ck.MaxAge != 0 || !ck.Expires.IsZero() {
		t.Errorf("reset cookie %+v has a lifetime; want a browser-session cookie", ck)
	}
}

// TestUnkeptSessionEndsAfterShortIdleOnFakeClock: an unticked session ends short_idle_timeout (12 hours) after its
// last use, and a use moves that limit on; a ticked session at the same moment is still live. The clock is fake.
func TestUnkeptSessionEndsAfterShortIdleOnFakeClock(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	t0 := time.Now()
	clock := t0
	s.sessionClock = func() time.Time { return clock }

	unused := signInKeeping(t, s, "ann@example.com", false)
	used := signInKeeping(t, s, "ann@example.com", false)
	kept := signInKeeping(t, s, "ann@example.com", true)
	// The second session, used six hours in, is moved on to 18 hours; the first, never used, ends at 12.
	clock = t0.Add(6 * time.Hour)
	wantStatus(t, callSession(t, s, used, "GET", "/api/v1/me", nil), 200)

	clock = t0.Add(12*time.Hour + time.Minute)
	wantError(t, callSession(t, s, unused, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)
	wantStatus(t, callSession(t, s, kept, "GET", "/api/v1/me", nil), 200)

	// Nothing has used the second session since six hours in, so it ends at 18.
	clock = t0.Add(18*time.Hour + 2*time.Minute)
	wantError(t, callSession(t, s, used, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)
	wantStatus(t, callSession(t, s, kept, "GET", "/api/v1/me", nil), 200)
}

// TestKeptSessionIsNotShortened: a ticked session outlives the short idle limit, as it did before the choice.
func TestKeptSessionIsNotShortened(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	t0 := time.Now()
	clock := t0
	s.sessionClock = func() time.Time { return clock }
	kept := signInKeeping(t, s, "ann@example.com", true)
	clock = t0.Add(13 * time.Hour)
	wantStatus(t, callSession(t, s, kept, "GET", "/api/v1/me", nil), 200)
}

// TestUnkeptChoiceSurvivesPasswordChange: changing the password replaces the session, and the new one keeps the
// choice the old one made.
func TestUnkeptChoiceSurvivesPasswordChange(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	sess := signInKeeping(t, s, "ann@example.com", false)
	rec := callSession(t, s, sess, "POST", "/api/v1/me/password",
		map[string]any{"current_password": testPassword, "new_password": "a new passphrase"})
	wantStatus(t, rec, http.StatusNoContent)
	if ck := cookieNamed(t, rec, s.sessionCookie); ck.MaxAge != 0 || !ck.Expires.IsZero() {
		t.Errorf("changed-password cookie %+v has a lifetime; want a browser-session cookie", ck)
	}
}

// TestUnkeptChoiceFollowsSignInAsAndGoBack: a sign-in-as from an unticked admin session gives browser-session
// cookies for the sign-in-as and for the parked admin cookie, and Go back restores the admin's cookie as it was.
func TestUnkeptChoiceFollowsSignInAsAndGoBack(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	s := fx.s
	adminSess := signInKeeping(t, s, "ann@example.com", false)
	rec := callSession(t, s, adminSess, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.member.ID+"/sign-in",
		map[string]any{"admin_password": testPassword})
	wantStatus(t, rec, 200)
	if ck := cookieNamed(t, rec, s.sessionCookie); ck.MaxAge != 0 || !ck.Expires.IsZero() {
		t.Errorf("sign-in-as cookie %+v has a lifetime", ck)
	}
	var parkedCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == s.returnCookieName() {
			parkedCookie = c
		}
	}
	if parkedCookie == nil || parkedCookie.MaxAge != 0 {
		t.Fatalf("parked admin cookie = %+v; want a browser-session cookie", parkedCookie)
	}
	back := goBack(t, s, sessionFrom(t, s, rec), parkedCookie.Value)
	wantStatus(t, back, 200)
	if ck := cookieNamed(t, back, s.sessionCookie); ck.MaxAge != 0 || !ck.Expires.IsZero() {
		t.Errorf("restored admin cookie %+v has a lifetime", ck)
	}
}

// TestSignInLinkSessionKeepsItsLifetime: an application's sign-in link decides the lifetime, as it did before: the
// session is kept.
func TestSignInLinkSessionKeepsItsLifetime(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr := newMemberUser(t, s, key, acc.ID, "sess@example.com")
	_, tok := signInLinkOf(t, s, key, acc.ID, usr, nil)
	rec := redeem(t, s, tok)
	wantStatus(t, rec, 200)
	if ck := cookieNamed(t, rec, s.sessionCookie); ck.MaxAge != keptMaxAge(s) {
		t.Errorf("sign-in link cookie Max-Age = %d, want %d", ck.MaxAge, keptMaxAge(s))
	}
}
