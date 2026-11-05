// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
)

// sec1 item 1: a guesser at one address must not lock the real user out from another address.
// Before, the per-email bucket was keyed on the email alone, so a few tries from one address made
// every later sign-in and reset for that email answer 429, from anywhere.
func TestGuessingFromOneAddressDoesNotLockOutTheUserElsewhere(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec, func(c *config.Config) { c.RateLimit.PerEmailPerMinute = 2 })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")

	const attacker = "192.0.2.10:4000"
	const victim = "192.0.2.11:4000"

	// The attacker has the per-email budget of two for its own address, so its third try is refused.
	for i := 0; i < 2; i++ {
		wantError(t, remoteCall(t, s, "POST", "/api/v1/auth/login", attacker,
			map[string]any{"email": "ann@example.com", "password": "wrong password here"}), 401, apierr.Unauthenticated)
	}
	wantError(t, remoteCall(t, s, "POST", "/api/v1/auth/login", attacker,
		map[string]any{"email": "ann@example.com", "password": "wrong password here"}), 429, apierr.RateLimited)

	// The user signs in from another address, unaffected.
	wantStatus(t, remoteCall(t, s, "POST", "/api/v1/auth/login", victim,
		map[string]any{"email": "ann@example.com", "password": testPassword}), 200)

	// Resets: the attacker's two requests use up its own budget for the email, and the user's
	// request from another address still answers 202.
	forgot := func(addr string) *httptest.ResponseRecorder {
		return remoteCall(t, s, "POST", "/api/v1/auth/password/forgot", addr,
			map[string]any{"email": "ann@example.com"})
	}
	wantStatus(t, forgot(attacker), 202)
	wantStatus(t, forgot(attacker), 202)
	wantError(t, forgot(attacker), 429, apierr.RateLimited)
	wantStatus(t, forgot(victim), 202)
	s.bg.Wait()
	// Three reset mails in all: the attacker's two, and the user's. The mail budget for the
	// address allows three an hour.
	if n := len(rec.Messages()); n != 3 {
		t.Errorf("reset mails = %d, want 3 (the user's request is served)", n)
	}
}

// sec1 item 1: online guessing of one email from many addresses is still bounded. Each address
// has its own per-email budget, so the bound is a loose cap on failed sign-ins per email, an hour
// wide, that counts every address together. An unknown email is charged the same way, so the
// answer cannot show whether the address is known.
func TestGuessingOneEmailFromManyAddressesIsBounded(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.RateLimit.PerEmailPerMinute = 2 })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")

	for _, email := range []string{"ann@example.com", "nobody@example.com"} {
		for i := 0; i < 30; i++ {
			addr := fmt.Sprintf("192.0.2.%d:4000", 20+i)
			wantError(t, remoteCall(t, s, "POST", "/api/v1/auth/login", addr,
				map[string]any{"email": email, "password": "wrong password here"}), 401, apierr.Unauthenticated)
		}
		wantError(t, remoteCall(t, s, "POST", "/api/v1/auth/login", "192.0.2.90:4000",
			map[string]any{"email": email, "password": "wrong password here"}), 429, apierr.RateLimited)
	}
}
