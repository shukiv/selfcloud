// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"errors"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/sweep"
)

// lk1: an editor's lock is released by the sweep once no live editing session is on the file. Signing out of the browser
// that opened the editor ends its session, so its lock is freed at the next sweep rather than when lock_minutes runs out.
func TestWOPILockReleasedBySweepAfterTheEditorsBrowserSignsOut(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tok := fx.openToken(t, "edit")
	fx.lockFor(t, tok, "L-owner")
	wantStatus2xx(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/auth/logout", nil))

	sw := sweep.New(sweep.Options{Store: fx.s.store, Blobs: fx.s.blobs, DeleteGrace: time.Hour})
	res, err := sw.Once(t.Context(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.UnusedLocks != 1 {
		t.Fatalf("sweep released %d unused locks, want 1", res.UnusedLocks)
	}
	if _, err := fx.s.store.LiveLock(t.Context(), fx.acc.ID, fx.doc.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the file is still locked after its only editor signed out: %v", err)
	}
	entries, _, err := fx.s.store.AuditLog(t.Context(), fx.acc.ID, "", true, store.Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range entries {
		if e.Action == "node.unlock" && e.Target == fx.doc.ID && e.Details["reason"] == "no_live_session" {
			found = true
		}
	}
	if !found {
		t.Errorf("the release is not audited as node.unlock with reason no_live_session")
	}
}

// lk1: a co-editor keeps the lock. Another browser of the same person still has a live editor on the file, so the sweep
// keeps the lock when the first browser signs out, and the second editor can still save under it.
func TestWOPILockKeptWhileAnotherBrowserStillEditsTheFile(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.grant(t, fx.doc.ID, fx.ann.ID, "editor")
	tok := fx.openToken(t, "edit")
	fx.lockFor(t, tok, "L-owner")
	other := sessionFrom(t, fx.s, login(t, fx.s, "ann@example.com", testPassword, ""))
	otherTok := decode[officeJSON](t, fx.open(t, other, fx.doc.ID, "edit")).Token
	wantStatus2xx(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/auth/logout", nil))

	sw := sweep.New(sweep.Options{Store: fx.s.store, Blobs: fx.s.blobs, DeleteGrace: time.Hour})
	res, err := sw.Once(t.Context(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.UnusedLocks != 0 {
		t.Fatalf("sweep released %d locks while another editor is live, want 0", res.UnusedLocks)
	}
	if _, err := fx.s.store.LiveLock(t.Context(), fx.acc.ID, fx.doc.ID); err != nil {
		t.Fatalf("the co-editor's lock was released: %v", err)
	}
	wantStatus(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), otherTok, nil, nil), 200)
}
