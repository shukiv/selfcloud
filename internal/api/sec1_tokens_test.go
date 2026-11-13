// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"testing"
)

// sec1 (decision 84): a password reset, a password change, an admin's password set and a disable each end the
// user's delegated tokens. Before, only an explicit revoke did, so a token outlived a stolen or replaced password for
// up to its hour.
func TestDelegatedTokenEndsWithEachPasswordAndStatusEvent(t *testing.T) {
	t.Parallel()

	t.Run("password reset", func(t *testing.T) {
		t.Parallel()
		fx := newSharingFixture(t)
		tok := mintToken(t, fx.s, fx.key, fx.acc.ID, fx.fld.ID, fx.bob.ID)
		wantStatus(t, getWith(t, fx.s, tok, "/api/v1/folders"), 200)

		wantStatus(t, call(t, fx.s, "POST", "/api/v1/auth/password/forgot", "",
			map[string]any{"email": fx.bob.Email}), 202)
		fx.s.bg.Wait()
		msgs := fx.sent(t)
		if len(msgs) == 0 {
			t.Fatal("no reset mail")
		}
		wantStatus(t, call(t, fx.s, "POST", "/api/v1/auth/password/reset", "",
			map[string]any{"token": tokenFromMail(t, msgs[len(msgs)-1]), "password": "a new passphrase here"}), 200)
		wantStatus(t, getWith(t, fx.s, tok, "/api/v1/folders"), 401)
	})

	t.Run("password change", func(t *testing.T) {
		t.Parallel()
		fx := newSharingFixture(t)
		tok := mintToken(t, fx.s, fx.key, fx.acc.ID, fx.fld.ID, fx.bob.ID)
		wantStatus(t, getWith(t, fx.s, tok, "/api/v1/folders"), 200)

		wantStatus(t, callSession(t, fx.s, fx.bobSess, "POST", "/api/v1/me/password",
			map[string]any{"current_password": testPassword, "new_password": "a new passphrase here"}), 204)
		wantStatus(t, getWith(t, fx.s, tok, "/api/v1/folders"), 401)
	})

	t.Run("admin sets the password", func(t *testing.T) {
		t.Parallel()
		fx := newImpFix(t, true)
		tok := mintToken(t, fx.s, fx.accKey, fx.acc.ID, folderOf(t, fx.s, fx.accKey, fx.acc.ID), fx.member.ID)
		wantStatus(t, getWith(t, fx.s, tok, "/api/v1/folders"), 200)

		wantStatus(t, callSession(t, fx.s, fx.adminSess, "POST",
			"/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.member.ID+"/password",
			map[string]any{"password": "a new passphrase here", "admin_password": testPassword}), 204)
		wantStatus(t, getWith(t, fx.s, tok, "/api/v1/folders"), 401)
	})

	t.Run("disable, then enable again", func(t *testing.T) {
		t.Parallel()
		fx := newSharingFixture(t)
		tok := mintToken(t, fx.s, fx.key, fx.acc.ID, fx.fld.ID, fx.bob.ID)
		wantStatus(t, getWith(t, fx.s, tok, "/api/v1/folders"), 200)

		users := "/api/v1/accounts/" + fx.acc.ID + "/users/" + fx.bob.ID
		wantStatus(t, call(t, fx.s, "PATCH", users, fx.key, map[string]any{"status": "disabled"}), 200)
		wantStatus(t, getWith(t, fx.s, tok, "/api/v1/folders"), 401)

		// Enabling the user again does not bring the token back: its cut-off stays where the disable put it.
		wantStatus(t, call(t, fx.s, "PATCH", users, fx.key, map[string]any{"status": "active"}), 200)
		wantStatus(t, getWith(t, fx.s, tok, "/api/v1/folders"), 401)
	})
}
