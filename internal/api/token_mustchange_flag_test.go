// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/store"
)

// setMustChange sets or clears a user's must-change flag in the store and moves no cut-off. The API clears the flag only
// through a password change, which also voids the user's tokens, so a test that needs the flag alone sets it here.
func setMustChange(t *testing.T, s *Server, accID, usrID string, on bool) {
	t.Helper()
	ctx := context.Background()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		u, err := tx.User(ctx, accID, usrID)
		if err != nil {
			return err
		}
		u.MustChangePassword = on
		u.UpdatedAt = store.Now()
		return tx.UpdateUser(ctx, u)
	})
	if err != nil {
		t.Fatalf("set the must-change flag to %v: %v", on, err)
	}
}

// A WOPI token of a user whose flag is set is refused on a read and on a save, with the answer a dead WOPI caller
// gets (401 unauthenticated), and the file is not written. The token is live: no cut-off moved, so only the
// must-change check refuses it.
func TestWOPIRefusesAMustChangeUser(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tok := fx.openToken(t, "edit")
	setMustChange(t, fx.s, fx.acc.ID, fx.ann.ID, true)
	t.Run("read is refused", func(t *testing.T) {
		// A WOPI refusal is a bare status, not the JSON error body, so only the status is checked.
		wantStatus(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), tok, nil, nil), 401)
	})
	t.Run("save is refused", func(t *testing.T) {
		wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID)+"/contents", tok, strings.NewReader("changed"),
			map[string]string{"X-WOPI-Override": "PUT"}), 401)
		if got := fx.fileText(t, fx.doc.ID); got != "hello" {
			t.Errorf("the file reads %q after a refused save, want %q", got, "hello")
		}
	})
}

// Clearing the flag restores the same token at once: the flag is read on every use, and no cut-off moved. No API path
// clears the flag without voiding the user's tokens (a password change does that), so the flag is cleared in the store.
func TestClearedFlagRestoresTheSameToken(t *testing.T) {
	t.Parallel()
	m := newMemberFixture(t) // ann: write on the folder f
	s, accID := m.s, m.acc.ID
	tok := mintFor(t, s, m.key, accID, map[string]any{"folders": []string{m.f.ID}, "user": m.ann.ID, "permissions": "write"})
	read := childrenPath(m.f.ID)
	wantStatus(t, getWith(t, s, tok, read), 200)
	setMustChange(t, s, accID, m.ann.ID, true)
	wantError(t, getWith(t, s, tok, read), 403, apierr.Forbidden)
	setMustChange(t, s, accID, m.ann.ID, false)
	wantStatus(t, getWith(t, s, tok, read), 200)
	wantStatus(t, tokenMkdir(t, s, tok, m.f.ID, "/again"), 201)
}

// A mint for a user whose password an admin typed is refused with 409 conflict and reason password_change_required, the
// answer sign-in-as gives (impersonation.go). The typed password ends the user's earlier tokens, so a mint is the only
// way such a token could be made. Once the flag is cleared the mint works again.
func TestMintRefusesAMustChangeUser(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	s := fx.s
	f := newFolder(t, s, fx.accKey, fx.acc.ID, "Mine")
	wantStatus(t, callSession(t, s, fx.adminSess, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.member.ID+"/password",
		map[string]any{"password": "a password the admin typed", "admin_password": testPassword}), 204)
	mint := func() *httptest.ResponseRecorder {
		return call(t, s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/tokens", fx.accKey,
			map[string]any{"folders": []string{f.ID}, "user": fx.member.ID, "permissions": "write"})
	}
	t.Run("mint is refused", func(t *testing.T) {
		e := wantError(t, mint(), 409, apierr.Conflict)
		if e.Details["reason"] != reasonPasswordChangeRequired {
			t.Errorf("reason = %v, want %s", e.Details["reason"], reasonPasswordChangeRequired)
		}
	})
	t.Run("a cleared flag mints again", func(t *testing.T) {
		setMustChange(t, s, fx.acc.ID, fx.member.ID, false)
		wantStatus(t, mint(), 201)
	})
}
