// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/mail"
)

// TestChangePasswordGuessingEndsSession checks that guessing the current password is limited: a
// fifth wrong guess in a row ends the session that made it, and the user's attempts across
// sessions share one limit, so a second session gets rate_limited.
func TestChangePasswordGuessingEndsSession(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, sessA := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	body := map[string]any{"current_password": "wrong password here", "new_password": "a new passphrase"}

	for i := 1; i < maxPasswordFails; i++ {
		wantError(t, callSession(t, s, sessA, "POST", "/api/v1/me/password", body), 403, apierr.Forbidden)
	}
	// The fifth wrong guess ends the session and clears its cookie.
	fifth := callSession(t, s, sessA, "POST", "/api/v1/me/password", body)
	wantError(t, fifth, 401, apierr.Unauthenticated)
	if c := cookieNamed(t, fifth, s.sessionCookie); c == nil || c.MaxAge >= 0 {
		t.Fatalf("session cookie not cleared: %+v", c)
	}
	wantError(t, callSession(t, s, testSession{cookie: sessA.cookie}, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)

	// A second session of the same user shares the per-user limit, which the five guesses used up.
	sessB := sessionFrom(t, s, login(t, s, "ann@example.com", testPassword, ""))
	wantError(t, callSession(t, s, sessB, "POST", "/api/v1/me/password", body), 429, apierr.RateLimited)

	// Each refusal was audited as failed.
	entries := decode[listResponse[auditJSON]](t, call(t, s, "GET", "/api/v1/accounts/"+acc.ID+"/audit", admin, nil))
	failed := 0
	for _, en := range entries.Items {
		if en.Action == "user.password_change" && en.Outcome == "failed" {
			failed++
		}
	}
	if failed != maxPasswordFails {
		t.Errorf("failed password changes audited = %d, want %d", failed, maxPasswordFails)
	}
}
