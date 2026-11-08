// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
)

// wopiSessionOn opens an editing session on n that ends at until. A non-nil browser binds it to the browser session with
// that cookie hash, and userID is the person the session belongs to.
func (fx *lockFixture) wopiSessionOn(t *testing.T, n *Node, userID string, until time.Time, browser []byte) *WOPISession {
	t.Helper()
	w := &WOPISession{ID: id.New("wop"), AccountID: fx.acc, NodeID: n.ID, UserID: userID, Role: "write",
		IssuedAt: fx.clock.Now(), ExpiresAt: until, BrowserSession: browser}
	if err := fx.s.Write(context.Background(), func(tx *Tx) error { return tx.InsertWOPISession(context.Background(), w) }); err != nil {
		t.Fatal(err)
	}
	return w
}

// browserSessionFor signs userID in: a browser session with the cookie hash hash, ending at until.
func (fx *lockFixture) browserSessionFor(t *testing.T, userID string, hash []byte, until time.Time) {
	t.Helper()
	now := fx.clock.Now()
	sess := &Session{IDHash: hash, UserID: userID, AccountID: fx.acc, CSRFToken: "c", CreatedAt: now,
		LastSeenAt: now, ExpiresAt: until}
	if err := fx.s.Write(context.Background(), func(tx *Tx) error { return tx.InsertSession(context.Background(), sess) }); err != nil {
		t.Fatal(err)
	}
}

// releaseNow runs the early release at the fake clock's now and returns the locks it released.
func (fx *lockFixture) releaseNow(t *testing.T) []*WOPILock {
	t.Helper()
	var got []*WOPILock
	if err := fx.s.Write(context.Background(), func(tx *Tx) error {
		var err error
		got, err = tx.ReleaseUnusedLocks(context.Background(), fx.clock.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return got
}

// wantLockKept asks that the file n still holds a live lock.
func (fx *lockFixture) wantLockKept(t *testing.T, n *Node, what string) {
	t.Helper()
	if _, err := fx.s.LiveLock(context.Background(), fx.acc, n.ID); err != nil {
		t.Fatalf("%s: the lock is gone: %v", what, err)
	}
}

// wantLockReleased asks that the release returned one lock on n, with lockID, and that the file is free.
func (fx *lockFixture) wantLockReleased(t *testing.T, got []*WOPILock, n *Node, lockID, what string) {
	t.Helper()
	if len(got) != 1 || got[0].NodeID != n.ID || got[0].LockID != lockID || got[0].FolderID != fx.fld.ID {
		t.Fatalf("%s: released %+v, want the lock %s on the file in the team folder", what, got, lockID)
	}
	if _, err := fx.s.LiveLock(context.Background(), fx.acc, n.ID); err == nil {
		t.Fatalf("%s: the file is still locked", what)
	}
}

// TestLockReleasedWhenItsOnlySessionHasEnded: a lock whose only WOPI session has run out is released before the lock's
// own expiry, and the file is free at once.
func TestLockReleasedWhenItsOnlySessionHasEnded(t *testing.T) {
	t.Parallel()
	fx := newLockFixture(t)
	fx.lock(t, fx.a, "L1", 30)
	fx.wopiSessionOn(t, fx.a, "", fx.clock.Now().Add(time.Minute), nil)
	fx.clock.Add(2 * time.Minute)
	fx.wantLockReleased(t, fx.releaseNow(t), fx.a, "L1", "only session ended")
}

// TestLockKeptWhileItsOnlySessionIsLive: a live session holds the lock, so the release leaves it alone.
func TestLockKeptWhileItsOnlySessionIsLive(t *testing.T) {
	t.Parallel()
	fx := newLockFixture(t)
	fx.lock(t, fx.a, "L1", 30)
	fx.wopiSessionOn(t, fx.a, "", fx.clock.Now().Add(time.Hour), nil)
	fx.clock.Add(2 * time.Minute)
	if got := fx.releaseNow(t); len(got) != 0 {
		t.Fatalf("released %+v while a session is live", got)
	}
	fx.wantLockKept(t, fx.a, "live session")
}

// TestLockReleasedWhenItsBrowserSessionEnded: an editor bound to a browser session ends with that session. A sign-out
// (the row is gone) releases the lock even though the editor's own expiry is still ahead.
func TestLockReleasedWhenItsBrowserSessionEnded(t *testing.T) {
	t.Parallel()
	fx := newLockFixture(t)
	u := newUserRow(t, fx.s, fx.acc, "ann@example.com", "ext-ann")
	hash := []byte("browser-session-hash-0123456789")
	fx.browserSessionFor(t, u.ID, hash, fx.clock.Now().Add(time.Hour))
	fx.lock(t, fx.a, "L1", 30)
	fx.wopiSessionOn(t, fx.a, u.ID, fx.clock.Now().Add(time.Hour), hash)
	if got := fx.releaseNow(t); len(got) != 0 {
		t.Fatalf("released %+v while the browser session is live", got)
	}
	if err := fx.writeErr(t, func(ctx context.Context, tx *Tx) error { return tx.DeleteSession(ctx, hash) }); err != nil {
		t.Fatal(err)
	}
	fx.wantLockReleased(t, fx.releaseNow(t), fx.a, "L1", "browser session signed out")
}

// TestLockReleasedWhenItsBrowserSessionExpired: the same rule when the browser session runs out rather than being removed.
func TestLockReleasedWhenItsBrowserSessionExpired(t *testing.T) {
	t.Parallel()
	fx := newLockFixture(t)
	u := newUserRow(t, fx.s, fx.acc, "ann@example.com", "ext-ann")
	hash := []byte("browser-session-hash-0123456789")
	fx.browserSessionFor(t, u.ID, hash, fx.clock.Now().Add(time.Minute))
	fx.lock(t, fx.a, "L1", 30)
	fx.wopiSessionOn(t, fx.a, u.ID, fx.clock.Now().Add(time.Hour), hash)
	fx.clock.Add(2 * time.Minute)
	fx.wantLockReleased(t, fx.releaseNow(t), fx.a, "L1", "browser session expired")
}

// TestLockKeptForACoEditorWhileAnotherSessionIsLive: one editor of the file has ended and another is live under the same
// lock. Releasing then would make the live editor's next PUT get 409, so the lock is kept (the co-editor rule).
func TestLockKeptForACoEditorWhileAnotherSessionIsLive(t *testing.T) {
	t.Parallel()
	fx := newLockFixture(t)
	fx.lock(t, fx.a, "L1", 30)
	fx.wopiSessionOn(t, fx.a, "", fx.clock.Now().Add(time.Minute), nil)
	fx.wopiSessionOn(t, fx.a, "", fx.clock.Now().Add(time.Hour), nil)
	fx.clock.Add(2 * time.Minute)
	if got := fx.releaseNow(t); len(got) != 0 {
		t.Fatalf("released %+v while a co-editor's session is live", got)
	}
	fx.wantLockKept(t, fx.a, "co-editor")
}

// TestLockReleasedWhenItsOnlySessionIsRevoked: a revoked session is refused, so it cannot hold the file.
func TestLockReleasedWhenItsOnlySessionIsRevoked(t *testing.T) {
	t.Parallel()
	fx := newLockFixture(t)
	fx.lock(t, fx.a, "L1", 30)
	fx.wopiSessionOn(t, fx.a, "", fx.clock.Now().Add(time.Hour), nil)
	if err := fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		_, err := tx.RevokeWOPISessions(ctx, fx.acc, "", fx.clock.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	fx.wantLockReleased(t, fx.releaseNow(t), fx.a, "L1", "session revoked")
}

// TestReleaseRechecksTheLockAndTheSessions pins the release's own re-check, which the race rule depends on. The release
// deletes a lock only when the lock ID is still the one read and no live session has come to the file since. Here each
// case changes the file after the lock was read, so the delete must affect nothing.
func TestReleaseRechecksTheLockAndTheSessions(t *testing.T) {
	t.Parallel()
	t.Run("a session opened after the read keeps the lock", func(t *testing.T) {
		t.Parallel()
		fx := newLockFixture(t)
		fx.lock(t, fx.a, "L1", 30)
		fx.clock.Add(time.Minute)
		read := fx.liveLockRead(t, fx.a)
		fx.wopiSessionOn(t, fx.a, "", fx.clock.Now().Add(time.Hour), nil)
		if ok := fx.releaseGuarded(t, read); ok {
			t.Fatal("the lock was released although a live session now holds the file")
		}
		fx.wantLockKept(t, fx.a, "session opened after the read")
	})
	t.Run("a lock replaced after the read is not released", func(t *testing.T) {
		t.Parallel()
		fx := newLockFixture(t)
		fx.lock(t, fx.a, "L1", 30)
		read := fx.liveLockRead(t, fx.a)
		if err := fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
			return tx.RelockWOPILock(ctx, fx.acc, fx.a.ID, "L1", "L2", "usr_test", fx.clock.Now().Add(30*time.Minute))
		}); err != nil {
			t.Fatal(err)
		}
		if ok := fx.releaseGuarded(t, read); ok {
			t.Fatal("the old lock ID released the file after it was replaced")
		}
		fx.wantLockKept(t, fx.a, "replaced lock")
	})
}

// liveLockRead reads the live lock on n, as the sweep's candidate list does, before a change made after the read.
func (fx *lockFixture) liveLockRead(t *testing.T, n *Node) *WOPILock {
	t.Helper()
	l, err := fx.s.LiveLock(context.Background(), fx.acc, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// releaseGuarded runs the release of one lock as read, and reports whether it deleted the row.
func (fx *lockFixture) releaseGuarded(t *testing.T, read *WOPILock) bool {
	t.Helper()
	var ok bool
	if err := fx.s.Write(context.Background(), func(tx *Tx) error {
		var err error
		ok, err = tx.releaseIfUnused(context.Background(), read, fx.clock.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return ok
}
