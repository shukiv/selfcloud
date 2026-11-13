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

// A viewer's live session is not a reason to keep a lock. The owner signs out while a member has the
// file open to view: the member's session can never save or refresh under the lock, so the lock is freed at the sweep.
func TestWOPILockReleasedBySweepWhenOnlyAViewerRemains(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tok := fx.openToken(t, "edit")
	fx.lockFor(t, tok, "L-owner")
	fx.grant(t, fx.doc.ID, fx.bob.ID, "viewer")
	view := decode[officeJSON](t, fx.open(t, fx.bobSess, fx.doc.ID, "view")).Token
	wantStatus2xx(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/auth/logout", nil))

	sw := sweep.New(sweep.Options{Store: fx.s.store, Blobs: fx.s.blobs, DeleteGrace: time.Hour})
	res, err := sw.Once(t.Context(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.UnusedLocks != 1 {
		t.Fatalf("sweep released %d unused locks while only a viewer is live, want 1", res.UnusedLocks)
	}
	wantStatus(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), view, nil, nil), 200)
}

// A view-mode session cannot take, refresh or release a lock. Each answers as a session with no right
// to lock does (404, the specification's "user unauthorized"), and the lock held by the editor is unchanged.
func TestViewModeSessionCannotLock(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.grant(t, fx.doc.ID, fx.ann.ID, "editor")
	edit := fx.openToken(t, "edit")
	fx.lockFor(t, edit, "L-owner")
	view := decode[officeJSON](t, fx.open(t, fx.annSess, fx.doc.ID, "view")).Token
	lockHdr := func(over string) map[string]string {
		return map[string]string{"X-WOPI-Override": over, "X-WOPI-Lock": "L-owner"}
	}
	for _, over := range []string{"REFRESH_LOCK", "UNLOCK"} {
		wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), view, nil, lockHdr(over)), 404)
	}
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), view, nil,
		map[string]string{"X-WOPI-Override": "LOCK", "X-WOPI-Lock": "L-view"}), 404)
	if l, err := fx.s.store.LiveLock(t.Context(), fx.acc.ID, fx.doc.ID); err != nil || l.LockID != "L-owner" {
		t.Fatalf("the editor's lock changed after view-mode calls: %+v, %v", l, err)
	}
}

// The owner signs out, then a co-editor's fresh session refreshes the lock before the sweep runs. The
// sweep keeps the lock, since a live session on the file is the reason it was refreshed for.
func TestWOPILockKeptWhenACoEditorRefreshesItAfterTheOwnerSignsOut(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tok := fx.openToken(t, "edit")
	fx.lockFor(t, tok, "L-owner")
	wantStatus2xx(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/auth/logout", nil))
	other := sessionFrom(t, fx.s, login(t, fx.s, "ann@example.com", testPassword, ""))
	otherTok := decode[officeJSON](t, fx.open(t, other, fx.doc.ID, "edit")).Token
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), otherTok, nil,
		map[string]string{"X-WOPI-Override": "REFRESH_LOCK", "X-WOPI-Lock": "L-owner"}), 200)

	sw := sweep.New(sweep.Options{Store: fx.s.store, Blobs: fx.s.blobs, DeleteGrace: time.Hour})
	res, err := sw.Once(t.Context(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.UnusedLocks != 0 {
		t.Fatalf("sweep released %d locks under a co-editor's refreshed lock, want 0", res.UnusedLocks)
	}
	if _, err := fx.s.store.LiveLock(t.Context(), fx.acc.ID, fx.doc.ID); err != nil {
		t.Fatalf("the refreshed lock is gone: %v", err)
	}
}
