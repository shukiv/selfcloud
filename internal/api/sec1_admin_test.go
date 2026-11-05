// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
)

// sec1 item 12 (decision 84): an installation admin's session ends sooner than a user's: at admin_idle_timeout unused
// and admin_max_age in all, unless the user limits are shorter still.
func TestAdminSessionEndsSoonerThanAUsersSession(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	now := time.Now()
	if got, want := s.sessionExpiry(now, now, false, true), now.Add(12*time.Hour); !got.Equal(want) {
		t.Errorf("admin session expiry = %v, want %v (12 h idle)", got, want)
	}
	if got, want := s.sessionExpiry(now, now, false, false), now.Add(168*time.Hour); !got.Equal(want) {
		t.Errorf("user session expiry = %v, want %v (the user idle timeout)", got, want)
	}
}

// sec1 item 12 (decision 84): an admin session makes an account key only with the admin's own password. A wrong or
// missing one is refused, and the right one makes the key.
func TestAdminSessionKeyNeedsThePassword(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	path := "/api/v1/accounts/" + fx.acc.ID + "/keys"

	wantError(t, callSession(t, fx.s, fx.adminSess, "POST", path, map[string]any{"name": "ci"}), 403, apierr.Forbidden)
	wantError(t, callSession(t, fx.s, fx.adminSess, "POST", path,
		map[string]any{"name": "ci", "admin_password": "wrong password here"}), 403, apierr.Forbidden)
	wantStatus(t, callSession(t, fx.s, fx.adminSess, "POST", path,
		map[string]any{"name": "ci", "admin_password": testPassword}), 201)
}
