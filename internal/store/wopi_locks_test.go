// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
)

// fakeClock is the store's clock in these tests. It moves only when a test moves it.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// lockFixture is an account with two team folders: fld holds docs/a.docx and root/b.docx, fld2 is empty.
type lockFixture struct {
	s     *Store
	clock *fakeClock
	acc   string
	fld   *Folder
	fld2  *Folder
	docs  *Node
	a     *Node // docs/a.docx
	b     *Node // b.docx at the root of fld
}

func newLockFixture(t *testing.T) *lockFixture {
	t.Helper()
	s := openTest(t)
	clock := &fakeClock{t: Now()}
	s.SetClock(clock.Now)
	fx := &lockFixture{s: s, clock: clock, acc: newAccountRow(t, s, "A")}
	fx.fld = newFolderRow(t, s, fx.acc, "Team", "team")
	fx.fld2 = newFolderRow(t, s, fx.acc, "Other", "other")
	root := fx.root(t, fx.fld)
	fx.docs = insertDirRow(t, s, root, "docs")
	fx.a = fx.file(t, fx.fld, fx.docs, "a.docx")
	fx.b = fx.file(t, fx.fld, root, "b.docx")
	return fx
}

func (fx *lockFixture) root(t *testing.T, f *Folder) *Node {
	t.Helper()
	root, err := fx.s.RootNode(context.Background(), fx.acc, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// file inserts a file named name in folder, under parent.
func (fx *lockFixture) file(t *testing.T, f *Folder, parent *Node, name string) *Node {
	t.Helper()
	n := &Node{ID: id.New("nod"), AccountID: fx.acc, FolderID: f.ID, ParentID: parent.ID, Name: name, NameKey: name,
		Kind: KindFile, Size: 5, ETag: "e1", CreatedAt: fx.clock.Now(), ModifiedAt: fx.clock.Now(),
		CreatedBy: "usr_test", ModifiedBy: "usr_test"}
	if err := fx.s.Write(context.Background(), func(tx *Tx) error { return tx.InsertNode(context.Background(), n) }); err != nil {
		t.Fatal(err)
	}
	return n
}

// lock takes a lock on n with the given lock ID, for minutes from the fake clock's now, as the API does.
func (fx *lockFixture) lock(t *testing.T, n *Node, lockID string, minutes int) {
	t.Helper()
	now := fx.clock.Now()
	l := &WOPILock{NodeID: n.ID, AccountID: fx.acc, LockID: lockID, Holder: "usr_test", CreatedAt: now,
		ExpiresAt: now.Add(time.Duration(minutes) * time.Minute)}
	if err := fx.s.Write(context.Background(), func(tx *Tx) error {
		_, err := tx.PutLock(context.Background(), l)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// writeErr runs one write and returns its error.
func (fx *lockFixture) writeErr(t *testing.T, fn func(ctx context.Context, tx *Tx) error) error {
	t.Helper()
	return fx.s.Write(context.Background(), func(tx *Tx) error { return fn(context.Background(), tx) })
}

// wantLocked asks that err is a *LockedError whose Current is current.
func wantLocked(t *testing.T, what string, err error, current string) {
	t.Helper()
	var le *LockedError
	if !errors.As(err, &le) {
		t.Fatalf("%s: error = %v, want a *LockedError", what, err)
	}
	if le.Current != current {
		t.Fatalf("%s: Current = %q, want %q", what, le.Current, current)
	}
}

// TestLockedFileRefusesEveryWriterButItsHolder: a live lock on a file refuses each store write that changes the file, and
// lets only the holder write it.
func TestLockedFileRefusesEveryWriterButItsHolder(t *testing.T) {
	fx := newLockFixture(t)
	ctx := context.Background()
	fld2root := fx.root(t, fx.fld2)
	// One version exists before the lock, for the version writers.
	var ver *Version
	if err := fx.s.Write(ctx, func(tx *Tx) error {
		ver = &Version{ID: id.New("ver"), AccountID: fx.acc, NodeID: fx.a.ID, BlobID: "blb_v", Size: 5, SHA256: "x",
			ETag: "e0", MimeType: "text/plain", CreatedAt: fx.clock.Now(), CreatedBy: "usr_test",
			ReplacedAt: fx.clock.Now(), ReplacedBy: "usr_test"}
		return tx.InsertVersion(ctx, ver)
	}); err != nil {
		t.Fatal(err)
	}
	fx.lock(t, fx.a, "L1", 30)

	cases := []struct {
		name string
		fn   func(context.Context, *Tx) error
	}{
		{"UpdateNode (PUT, rename, sharing)", func(ctx context.Context, tx *Tx) error {
			n := *fx.a
			n.Name = "renamed.docx"
			return tx.UpdateNode(ctx, &n)
		}},
		{"TrashNode", func(ctx context.Context, tx *Tx) error {
			return tx.TrashNode(ctx, fx.acc, fx.a.ID, "usr_test", fx.clock.Now())
		}},
		{"PurgeNode", func(ctx context.Context, tx *Tx) error {
			_, _, err := tx.PurgeNode(ctx, fx.acc, fx.a.ID)
			return err
		}},
		{"MoveSubtree", func(ctx context.Context, tx *Tx) error {
			return tx.MoveSubtree(ctx, fx.acc, fx.a.ID, fx.fld.ID, fx.fld2.ID)
		}},
		{"SetSubtreeOwner", func(ctx context.Context, tx *Tx) error {
			return tx.SetSubtreeOwner(ctx, fx.acc, fx.a.ID, "usr_other")
		}},
		{"ClearSubtreeEditorsCanShare", func(ctx context.Context, tx *Tx) error {
			return tx.ClearSubtreeEditorsCanShare(ctx, fx.acc, fx.a.ID)
		}},
		{"UpdateVersion", func(ctx context.Context, tx *Tx) error {
			v := *ver
			v.KeepForever = true
			return tx.UpdateVersion(ctx, &v)
		}},
		{"DeleteVersion", func(ctx context.Context, tx *Tx) error {
			return tx.DeleteVersion(ctx, fx.acc, ver)
		}},
	}
	for _, c := range cases {
		err := fx.writeErr(t, c.fn)
		wantLocked(t, c.name, err, "L1")
	}
	// The holder's own write goes through.
	if err := fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		n := *fx.a
		n.Name = "held.docx"
		return tx.UpdateNodeHeld(ctx, &n, "L1")
	}); err != nil {
		t.Fatalf("holder write: %v", err)
	}
	// Another holder's ID is refused, with the holder's lock ID as Current.
	wantLocked(t, "UpdateNodeHeld with another ID", fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		n := *fx.a
		return tx.UpdateNodeHeld(ctx, &n, "L2")
	}), "L1")
	// The refused moves left the file where it was.
	got, err := fx.s.Node(ctx, fx.acc, fx.a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.FolderID != fx.fld.ID || got.Name != "held.docx" {
		t.Fatalf("after refused writes: folder %q name %q, want the first folder and held.docx", got.FolderID, got.Name)
	}
	_ = fld2root
}

// TestFolderOperationsRefuseASubtreeWithAFileOpen: a folder operation is refused when a live lock lies anywhere below
// the folder. A sibling outside the folder is not refused.
func TestFolderOperationsRefuseASubtreeWithAFileOpen(t *testing.T) {
	fx := newLockFixture(t)
	ctx := context.Background()
	fx.lock(t, fx.a, "L1", 30) // docs/a.docx, below docs

	wantLocked(t, "TrashNode of the folder", fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		return tx.TrashNode(ctx, fx.acc, fx.docs.ID, "usr_test", fx.clock.Now())
	}), "L1")
	wantLocked(t, "MoveSubtree of the folder", fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		return tx.MoveSubtree(ctx, fx.acc, fx.docs.ID, fx.fld.ID, fx.fld2.ID)
	}), "L1")
	wantLocked(t, "SetSubtreeOwner of the folder", fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		return tx.SetSubtreeOwner(ctx, fx.acc, fx.docs.ID, "usr_other")
	}), "L1")
	wantLocked(t, "ClearSubtreeEditorsCanShare of the folder", fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		return tx.ClearSubtreeEditorsCanShare(ctx, fx.acc, fx.docs.ID)
	}), "L1")
	wantLocked(t, "MoveFolderContents out of the folder", fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		return tx.MoveFolderContents(ctx, fx.acc, fx.fld.ID, fx.fld2.ID, fx.root(t, fx.fld2).ID, "usr_other")
	}), "L1")
	// A folder whose files are all unlocked is not refused: DeleteFolder of fld2 is empty and goes.
	if err := fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		return tx.DeleteFolder(ctx, fx.acc, fx.fld2.ID)
	}); err != nil {
		t.Fatalf("DeleteFolder of a folder with no open file: %v", err)
	}
	// Trashing the sibling file at the root of fld is outside docs and is not refused.
	if err := fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		return tx.TrashNode(ctx, fx.acc, fx.b.ID, "usr_test", fx.clock.Now())
	}); err != nil {
		t.Fatalf("trash of a sibling file: %v", err)
	}
	// DeleteFolder of the folder that holds the open file is refused. An account's deletion is not: it clears its locks
	// (TestDeleteAccountClearsItsLocksAndSessions).
	wantLocked(t, "DeleteFolder of the folder holding it", fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		return tx.DeleteFolder(ctx, fx.acc, fx.fld.ID)
	}), "L1")
	_ = ctx
}

// TestMetadataOfALockedFileAndItsFolderPasses: a description, colour, sharing flag, or a rename of a folder above a lock
// changes no content, and no path, since an editor holds the file by its ID. Those pass. A rename of the locked file, and
// a move of the file or of a folder above it, are refused.
func TestMetadataOfALockedFileAndItsFolderPasses(t *testing.T) {
	fx := newLockFixture(t)
	fx.lock(t, fx.a, "L1", 30)
	if err := fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		n := *fx.a
		n.Description = "hi"
		n.NoDownload = true
		n.EditorsCanShare = true
		return tx.UpdateNode(ctx, &n)
	}); err != nil {
		t.Fatalf("metadata of the locked file refused: %v", err)
	}
	if err := fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		n := *fx.docs
		n.Color = "red"
		n.Description = "papers"
		n.NoDownload = true
		n.EditorsCanShare = true
		n.Name, n.NameKey = "papers", "papers"
		return tx.UpdateNode(ctx, &n)
	}); err != nil {
		t.Fatalf("metadata and a rename of the folder above the lock refused: %v", err)
	}
	wantLocked(t, "rename of the locked file", fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		n := *fx.a
		n.Name, n.NameKey = "other.docx", "other.docx"
		return tx.UpdateNode(ctx, &n)
	}), "L1")
	wantLocked(t, "move of the folder above the lock", fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		n := *fx.docs
		n.FolderID = fx.fld2.ID
		n.ParentID = fx.root(t, fx.fld2).ID
		return tx.UpdateNode(ctx, &n)
	}), "L1")
	// The refused rename left the name alone, and the passed metadata is stored.
	got, err := fx.s.Node(context.Background(), fx.acc, fx.a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "a.docx" || got.Description != "hi" || !got.NoDownload {
		t.Fatalf("locked file after metadata writes: name %q description %q no_download %v", got.Name, got.Description, got.NoDownload)
	}
}

// TestDeleteAccountClearsItsLocksAndSessions: an account with a file open in an editor is deleted at once. The lock and
// the editing session go with it, in the same write, so nothing is left to wait for.
func TestDeleteAccountClearsItsLocksAndSessions(t *testing.T) {
	fx := newLockFixture(t)
	ctx := context.Background()
	fx.lock(t, fx.a, "L1", 30)
	sess := &WOPISession{ID: id.New("wps"), AccountID: fx.acc, NodeID: fx.a.ID, Role: "write",
		IssuedAt: fx.clock.Now(), ExpiresAt: fx.clock.Now().Add(time.Hour)}
	if err := fx.writeErr(t, func(ctx context.Context, tx *Tx) error { return tx.InsertWOPISession(ctx, sess) }); err != nil {
		t.Fatal(err)
	}
	if err := fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		return tx.DeleteAccount(ctx, fx.acc)
	}); err != nil {
		t.Fatalf("DeleteAccount with a live lock: %v", err)
	}
	for _, q := range []string{"SELECT COUNT(*) FROM wopi_locks WHERE account_id = ?",
		"SELECT COUNT(*) FROM wopi_sessions WHERE account_id = ?", "SELECT COUNT(*) FROM nodes WHERE account_id = ?"} {
		var n int
		if err := fx.s.r.QueryRowContext(ctx, q, fx.acc).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%s = %d after the account's deletion, want 0", q, n)
		}
	}
}

// TestUserDeletionReleasesTheLocksItLeaves: a user's deletion clears the locks the user holds, and the locks on the files in
// its personal folder, which move or go with it. Each clause is tested alone: a's lock is the user's own, in another folder;
// c's lock is another holder's, in the personal folder; b's lock is another holder's, in another folder, and stays.
func TestUserDeletionReleasesTheLocksItLeaves(t *testing.T) {
	fx := newLockFixture(t)
	ctx := context.Background()
	c := fx.file(t, fx.fld2, fx.root(t, fx.fld2), "c.docx") // in fld2, the user's personal folder below
	fx.lock(t, fx.a, "L1", 30)                              // held by usr_test, in fld
	now := fx.clock.Now()
	lockAs := func(n *Node, lockID, holder string) {
		t.Helper()
		if err := fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
			_, err := tx.PutLock(ctx, &WOPILock{NodeID: n.ID, AccountID: fx.acc, LockID: lockID, Holder: holder,
				CreatedAt: now, ExpiresAt: now.Add(30 * time.Minute)})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	lockAs(c, "L2", "usr_other")
	lockAs(fx.b, "L3", "usr_other")
	if err := fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		return tx.ReleaseUserLocks(ctx, fx.acc, "usr_test", fx.fld2.ID)
	}); err != nil {
		t.Fatalf("ReleaseUserLocks: %v", err)
	}
	if _, err := fx.s.LiveLock(ctx, fx.acc, fx.a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the user's own lock after its deletion: %v, want ErrNotFound", err)
	}
	if _, err := fx.s.LiveLock(ctx, fx.acc, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a lock on the personal folder's file after the deletion: %v, want ErrNotFound", err)
	}
	if _, err := fx.s.LiveLock(ctx, fx.acc, fx.b.ID); err != nil {
		t.Fatalf("another holder's lock on another folder was released: %v", err)
	}
}

// TestBreakLockEndsTheLockAndNothingElse: a break ends the live lock on the file and returns it, and it is an error to
// break a file that no editor holds.
func TestBreakLockEndsTheLockAndNothingElse(t *testing.T) {
	fx := newLockFixture(t)
	ctx := context.Background()
	fx.lock(t, fx.a, "L1", 30)
	fx.lock(t, fx.b, "L2", 30)
	var broken *WOPILock
	if err := fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		var err error
		broken, err = tx.BreakLock(ctx, fx.acc, fx.a.ID)
		return err
	}); err != nil {
		t.Fatalf("BreakLock: %v", err)
	}
	if broken == nil || broken.LockID != "L1" || broken.Holder != "usr_test" {
		t.Fatalf("BreakLock returned %+v, want the L1 lock held by usr_test", broken)
	}
	if _, err := fx.s.LiveLock(ctx, fx.acc, fx.a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LiveLock after a break: %v, want ErrNotFound", err)
	}
	if _, err := fx.s.LiveLock(ctx, fx.acc, fx.b.ID); err != nil {
		t.Fatalf("a break released another file's lock: %v", err)
	}
	if err := fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		_, err := tx.BreakLock(ctx, fx.acc, fx.a.ID)
		return err
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("break of an unlocked file: %v, want ErrNotFound", err)
	}
}

// TestLiveLocksAreCappedPerAccount: an account holds at most MaxLiveLocks live locks. The next lock is refused with no
// current lock, and an expired lock does not count.
func TestLiveLocksAreCappedPerAccount(t *testing.T) {
	fx := newLockFixture(t)
	ctx := context.Background()
	files := make([]*Node, MaxLiveLocks+1)
	if err := fx.s.Write(ctx, func(tx *Tx) error {
		for i := range files {
			files[i] = &Node{ID: id.New("nod"), AccountID: fx.acc, FolderID: fx.fld.ID, ParentID: fx.root(t, fx.fld).ID,
				Name: "f" + id.New("x") + ".docx", NameKey: "f" + id.New("x") + ".docx", Kind: KindFile, Size: 1, ETag: "e",
				CreatedAt: fx.clock.Now(), ModifiedAt: fx.clock.Now(), CreatedBy: "usr_test", ModifiedBy: "usr_test"}
			if err := tx.InsertNode(ctx, files[i]); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	lockOne := func(tx *Tx, n *Node, lockID string) error {
		now := fx.clock.Now()
		_, err := tx.PutLock(ctx, &WOPILock{NodeID: n.ID, AccountID: fx.acc, LockID: lockID, Holder: "usr_test",
			CreatedAt: now, ExpiresAt: now.Add(30 * time.Minute)})
		return err
	}
	if err := fx.s.Write(ctx, func(tx *Tx) error {
		for i := 0; i < MaxLiveLocks; i++ {
			if err := lockOne(tx, files[i], "L"+id.New("x")); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("%d locks: %v", MaxLiveLocks, err)
	}
	wantLocked(t, "lock over the cap", fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		return lockOne(tx, files[MaxLiveLocks], "Lover")
	}), "")
	fx.clock.Add(31 * time.Minute)
	if err := fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		return lockOne(tx, files[MaxLiveLocks], "Lafter")
	}); err != nil {
		t.Fatalf("lock after every other lock expired: %v", err)
	}
}

// TestLockOnAnotherAccountDoesNotRefuse: a file open in one account refuses nothing in another.
func TestLockOnAnotherAccountDoesNotRefuse(t *testing.T) {
	fx := newLockFixture(t)
	other := newAccountRow(t, fx.s, "B")
	otherFld := newFolderRow(t, fx.s, other, "Team", "team")
	otherRoot, err := fx.s.RootNode(context.Background(), other, otherFld.ID)
	if err != nil {
		t.Fatal(err)
	}
	f := &Node{ID: id.New("nod"), AccountID: other, FolderID: otherFld.ID, ParentID: otherRoot.ID, Name: "c.docx",
		NameKey: "c.docx", Kind: KindFile, Size: 1, ETag: "e", CreatedAt: Now(), ModifiedAt: Now(),
		CreatedBy: "usr_x", ModifiedBy: "usr_x"}
	if err := fx.s.Write(context.Background(), func(tx *Tx) error { return tx.InsertNode(context.Background(), f) }); err != nil {
		t.Fatal(err)
	}
	fx.lock(t, fx.a, "L1", 30)
	if err := fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		n := *f
		n.Name = "d.docx"
		return tx.UpdateNode(ctx, &n)
	}); err != nil {
		t.Fatalf("write in account B refused by account A's lock: %v", err)
	}
}

// TestExpiredLockIsAbsent: a lock past its expiry refuses nothing, is replaced by the next lock, and is the sweep's to
// remove, once.
func TestExpiredLockIsAbsent(t *testing.T) {
	fx := newLockFixture(t)
	ctx := context.Background()
	fx.lock(t, fx.a, "L1", 30)
	fx.clock.Add(30 * time.Minute) // expiry is exact: at ExpiresAt the lock is gone
	if err := fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		n := *fx.a
		n.Name = "after.docx"
		return tx.UpdateNode(ctx, &n)
	}); err != nil {
		t.Fatalf("write after expiry refused: %v", err)
	}
	if _, err := fx.s.LiveLock(ctx, fx.acc, fx.a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LiveLock after expiry: %v, want ErrNotFound", err)
	}
	// A new lock replaces the expired row and reports it, so the caller can audit the expiry.
	var expired *WOPILock
	now := fx.clock.Now()
	if err := fx.s.Write(ctx, func(tx *Tx) error {
		var err error
		expired, err = tx.PutLock(ctx, &WOPILock{NodeID: fx.a.ID, AccountID: fx.acc, LockID: "L2", Holder: "usr_test",
			CreatedAt: now, ExpiresAt: now.Add(time.Minute)})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if expired == nil || expired.LockID != "L1" {
		t.Fatalf("PutLock returned %+v, want the expired L1", expired)
	}
}

// TestLockRefreshAndUnlockNeedTheRightID: refresh and unlock act only on the live lock that has the given ID.
func TestLockRefreshAndUnlockNeedTheRightID(t *testing.T) {
	fx := newLockFixture(t)
	ctx := context.Background()
	fx.lock(t, fx.a, "L1", 30)
	until := fx.clock.Now().Add(30 * time.Minute)
	if err := fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		return tx.RefreshWOPILock(ctx, fx.acc, fx.a.ID, "L9", until)
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("refresh with a wrong ID: %v, want ErrNotFound", err)
	}
	if err := fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		return tx.UnlockNode(ctx, fx.acc, fx.a.ID, "L9")
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unlock with a wrong ID: %v, want ErrNotFound", err)
	}
	// A refresh made after 20 minutes moves the expiry to 30 minutes from then.
	fx.clock.Add(20 * time.Minute)
	later := fx.clock.Now().Add(30 * time.Minute)
	if err := fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		return tx.RefreshWOPILock(ctx, fx.acc, fx.a.ID, "L1", later)
	}); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	fx.clock.Add(20 * time.Minute) // 40 minutes after the lock, 20 after the refresh: still live
	if l, err := fx.s.LiveLock(ctx, fx.acc, fx.a.ID); err != nil || l.LockID != "L1" {
		t.Fatalf("after a refresh: %v %v, want the live L1", l, err)
	}
	if err := fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		return tx.UnlockNode(ctx, fx.acc, fx.a.ID, "L1")
	}); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	if err := fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		n := *fx.a
		n.Name = "unlocked.docx"
		return tx.UpdateNode(ctx, &n)
	}); err != nil {
		t.Fatalf("write after unlock: %v", err)
	}
}

// TestRefreshAfterExpiryIsNotFound: a lock that has expired cannot be refreshed back to life.
func TestRefreshAfterExpiryIsNotFound(t *testing.T) {
	fx := newLockFixture(t)
	fx.lock(t, fx.a, "L1", 30)
	fx.clock.Add(31 * time.Minute)
	if err := fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		return tx.RefreshWOPILock(ctx, fx.acc, fx.a.ID, "L1", fx.clock.Now().Add(30*time.Minute))
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("refresh of an expired lock: %v, want ErrNotFound", err)
	}
}

// TestSweepRemovesOnlyExpiredLocks: the sweep's step returns and removes the expired locks, and no live one.
func TestSweepRemovesOnlyExpiredLocks(t *testing.T) {
	fx := newLockFixture(t)
	fx.lock(t, fx.a, "L1", 5)
	fx.lock(t, fx.b, "L2", 30)
	fx.clock.Add(10 * time.Minute)
	var got []*WOPILock
	if err := fx.s.Write(context.Background(), func(tx *Tx) error {
		var err error
		got, err = tx.DeleteExpiredLocks(context.Background(), fx.clock.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].LockID != "L1" || got[0].FolderID != fx.fld.ID {
		t.Fatalf("swept %+v, want only L1 in the team folder", got)
	}
	if _, err := fx.s.LiveLock(context.Background(), fx.acc, fx.b.ID); err != nil {
		t.Fatalf("the live lock on b was swept or lost: %v", err)
	}
}

// TestSecondLockOnALiveFileIsAConflict: the row for a file is unique, so two live locks cannot both be recorded.
func TestSecondLockOnALiveFileIsAConflict(t *testing.T) {
	fx := newLockFixture(t)
	fx.lock(t, fx.a, "L1", 30)
	err := fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		_, err := tx.PutLock(ctx, &WOPILock{NodeID: fx.a.ID, AccountID: fx.acc, LockID: "L2", Holder: "usr_x",
			CreatedAt: fx.clock.Now(), ExpiresAt: fx.clock.Now().Add(time.Minute)})
		return err
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("second PutLock: %v, want ErrConflict", err)
	}
}

// TestWOPISessionTagIsRecordedAndMoved: a session records the tag it was given, and a save moves it on.
func TestWOPISessionTagIsRecordedAndMoved(t *testing.T) {
	fx := newLockFixture(t)
	ctx := context.Background()
	sess := &WOPISession{ID: id.New("wps"), AccountID: fx.acc, NodeID: fx.a.ID, Role: "write",
		IssuedAt: fx.clock.Now(), ExpiresAt: fx.clock.Now().Add(time.Hour), ServedTag: "tag1"}
	if err := fx.writeErr(t, func(ctx context.Context, tx *Tx) error { return tx.InsertWOPISession(ctx, sess) }); err != nil {
		t.Fatal(err)
	}
	got, err := fx.s.WOPISession(ctx, fx.acc, sess.ID)
	if err != nil || got.ServedTag != "tag1" {
		t.Fatalf("session tag = %q, %v; want tag1", got.ServedTag, err)
	}
	if err := fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		return tx.SetWOPISessionTag(ctx, fx.acc, sess.ID, "tag2")
	}); err != nil {
		t.Fatal(err)
	}
	if got, _ := fx.s.WOPISession(ctx, fx.acc, sess.ID); got.ServedTag != "tag2" {
		t.Fatalf("session tag after a save = %q, want tag2", got.ServedTag)
	}
	if err := fx.writeErr(t, func(ctx context.Context, tx *Tx) error {
		return tx.SetWOPISessionTag(ctx, "acc_other", sess.ID, "x")
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tag of another account's session: %v, want ErrNotFound", err)
	}
}
