// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
)

// TestGoBackRefusesAdminSessionEndedOnTheClock: the admin's unticked session ends 12 hours after its last use, by the
// server's clock. Going back after that is refused, so the restore reads the same clock as every other session check
// (decision 75). The clock is fake.
func TestGoBackRefusesAdminSessionEndedOnTheClock(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	s := fx.s
	t0 := time.Now()
	clock := t0
	s.sessionClock = func() time.Time { return clock }

	adminSess := signInKeeping(t, s, "ann@example.com", false)
	rec := callSession(t, s, adminSess, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.member.ID+"/sign-in",
		map[string]any{"admin_password": testPassword})
	wantStatus(t, rec, 200)
	imp := sessionFrom(t, s, rec)
	parkedValue := parked(t, s, rec)

	clock = t0.Add(12*time.Hour + time.Minute)
	wantError(t, goBack(t, s, imp, parkedValue), 401, apierr.Unauthenticated)
}
