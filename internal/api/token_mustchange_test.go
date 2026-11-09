// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
)

// tokenMkdir makes a directory in folder fld through a delegated token, on the write route.
func tokenMkdir(t *testing.T, s *Server, cred, fld, p string) *httptest.ResponseRecorder {
	t.Helper()
	return rawCall(t, s, "POST", "/api/v1/folders/"+fld+"/directories", cred, `{"path":"`+p+`","parents":true}`,
		map[string]string{"Content-Type": "application/json"})
}

// wantMustChangeRefusal checks the refusal a must-change user's browser session gets (decision 41): 403 forbidden
// with reason password_change_required.
func wantMustChangeRefusal(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	e := wantError(t, rec, 403, apierr.Forbidden)
	if e.Details["reason"] != reasonPasswordChangeRequired {
		t.Errorf("reason = %v, want %s", e.Details["reason"], reasonPasswordChangeRequired)
	}
}

// A delegated token acting as a user whose password an admin typed is refused on every route except the three
// must-change routes, as that user's browser session is. The three are session routes, so a token never reaches them.
// The other cases are guards: an account key, an installation admin, another user whose flag is clear, and the same
// token after the user has changed the password.
func TestDelegatedTokenOfMustChangeUser(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	s := fx.s
	f := newFolder(t, s, fx.accKey, fx.acc.ID, "Mine")
	grantWrite := func(usr string) {
		wantStatus(t, call(t, s, "PUT", "/api/v1/accounts/"+fx.acc.ID+"/folders/"+f.ID+"/grants/"+usr, fx.accKey,
			map[string]any{"level": "write"}), 200)
	}
	grantWrite(fx.member.ID)
	boss, _ := verifiedUser(t, s, fx.accKey, fx.acc.ID, "boss@example.com")
	grantWrite(boss.ID)

	// The token is minted first, and then the flag is set in the store with no cut-off moved. The token is live, so only
	// the must-change check can refuse it. The API's typed password ends the token and refuses a mint instead; see
	// TestMintRefusesAMustChangeUser.
	tok := mintFor(t, s, fx.accKey, fx.acc.ID, map[string]any{"folders": []string{f.ID}, "user": fx.member.ID, "permissions": "write"})
	setMustChange(t, s, fx.acc.ID, fx.member.ID, true)
	read := childrenPath(f.ID)

	t.Run("read is refused", func(t *testing.T) { wantMustChangeRefusal(t, getWith(t, s, tok, read)) })
	t.Run("write is refused", func(t *testing.T) { wantMustChangeRefusal(t, tokenMkdir(t, s, tok, f.ID, "/made")) })
	t.Run("GET /me is not reached by a token", func(t *testing.T) {
		wantError(t, getWith(t, s, tok, "/api/v1/me"), 401, apierr.Unauthenticated)
	})
	t.Run("POST /me/password is not reached by a token", func(t *testing.T) {
		rec := rawCall(t, s, "POST", "/api/v1/me/password", tok, `{"current_password":"x","new_password":"y"}`,
			map[string]string{"Content-Type": "application/json"})
		wantError(t, rec, 401, apierr.Unauthenticated)
	})

	// Guards.
	wantStatus(t, getWith(t, s, fx.accKey, read), 200)
	wantStatus(t, getWith(t, s, fx.admin, "/api/v1/accounts/"+fx.acc.ID), 200)
	bossTok := mintFor(t, s, fx.accKey, fx.acc.ID, map[string]any{"folders": []string{f.ID}, "user": boss.ID, "permissions": "write"})
	wantStatus(t, getWith(t, s, bossTok, read), 200)
	wantStatus(t, tokenMkdir(t, s, bossTok, f.ID, "/by-boss"), 201)

	// The member changes the password. The change voids the tokens issued before it (the existing cut-off), so the
	// token above stays void, and a token minted after the change works.
	rec := login(t, s, "member@example.com", testPassword, "")
	wantStatus(t, rec, 200)
	memSess := sessionFrom(t, s, rec)
	wantStatus(t, callSession(t, s, memSess, "POST", "/api/v1/me/password",
		map[string]any{"current_password": testPassword, "new_password": "the member's own password"}), 204)
	wantError(t, getWith(t, s, tok, read), 401, apierr.Unauthenticated)
	time.Sleep(2 * time.Millisecond)
	fresh := mintFor(t, s, fx.accKey, fx.acc.ID, map[string]any{"folders": []string{f.ID}, "user": fx.member.ID, "permissions": "write"})
	wantStatus(t, getWith(t, s, fresh, read), 200)
	wantStatus(t, tokenMkdir(t, s, fresh, f.ID, "/after-change"), 201)
}
