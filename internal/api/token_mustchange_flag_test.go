// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
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
