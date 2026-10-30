// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
)

// newUploadRow adds an unfinished upload of length bytes to folderID, into the directory dirID (or
// the folder's root when dirID is ""). It reserves the length, as the API does, and returns it.
func newUploadRow(t *testing.T, s *Store, accountID, folderID, dirID string, length int64) *Upload {
	t.Helper()
	ctx := context.Background()
	now := Now()
	u := &Upload{ID: id.New("upl"), AccountID: accountID, FolderID: folderID, ParentID: dirID, Name: "f.bin",
		NameKey: "f.bin", OnConflict: "fail", MimeType: "application/octet-stream", Length: length,
		BlobID: id.New("blb"), CreatedBy: "usr_test", CreatedKind: "user", CreatedAt: now, UpdatedAt: now,
		ExpiresAt: now.Add(time.Hour)}
	if err := s.Write(ctx, func(tx *Tx) error {
		if err := tx.InsertUpload(ctx, u); err != nil {
			return err
		}
		return tx.AdjustUsage(ctx, accountID, folderID, UsageChange{Reserved: length})
	}); err != nil {
		t.Fatal(err)
	}
	return u
}

// usageRow reads an account's counters straight from the database.
func usageRow(t *testing.T, s *Store, accountID string) Usage {
	t.Helper()
	var u Usage
	if err := s.r.QueryRowContext(context.Background(),
		"SELECT used_bytes, used_files, reserved_bytes FROM accounts WHERE id = ?", accountID).
		Scan(&u.Bytes, &u.Files, &u.Reserved); err != nil {
		t.Fatal(err)
	}
	return u
}

// TestAdjustUsageNeverGoesNegative: a change larger than the counter leaves zero, not a negative
// number, so a delete can never fail on an account whose counters have drifted.
func TestAdjustUsageNeverGoesNegative(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "A")
	f := newFolderRow(t, s, acc, "Docs", "docs")
	if err := s.Write(ctx, func(tx *Tx) error {
		if err := tx.AdjustUsage(ctx, acc, f.ID, UsageChange{Bytes: 10, Files: 1, Reserved: 5}); err != nil {
			return err
		}
		return tx.AdjustUsage(ctx, acc, f.ID, UsageChange{Bytes: -100, Files: -5, Reserved: -50})
	}); err != nil {
		t.Fatal(err)
	}
	if u := usageRow(t, s, acc); u != (Usage{}) {
		t.Errorf("usage after over-large release = %+v; want all zero", u)
	}
	// A single counter going negative is clamped on its own, and the others still move.
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.AdjustUsage(ctx, acc, f.ID, UsageChange{Bytes: 7, Files: 2, Reserved: -1})
	}); err != nil {
		t.Fatal(err)
	}
	if u := usageRow(t, s, acc); u != (Usage{Bytes: 7, Files: 2, Reserved: 0}) {
		t.Errorf("usage = %+v; want {7 2 0}", u)
	}
}

// TestDeleteUploadReleasesReservation: the row and its reservation go together, and a second delete
// finds nothing and releases nothing.
func TestDeleteUploadReleasesReservation(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "A")
	f := newFolderRow(t, s, acc, "Docs", "docs")
	u := newUploadRow(t, s, acc, f.ID, "", 500)
	if got := usageRow(t, s, acc).Reserved; got != 500 {
		t.Fatalf("reserved after creation = %d; want 500", got)
	}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteUpload(ctx, acc, u.ID) }); err != nil {
		t.Fatal(err)
	}
	if got := usageRow(t, s, acc).Reserved; got != 0 {
		t.Errorf("reserved after delete = %d; want 0", got)
	}
	err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteUpload(ctx, acc, u.ID) })
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete = %v; want ErrNotFound", err)
	}
}

// TestDeleteExpiredUploadReleasesOnlyWhenExpired: an upload that a request refreshed stays, and its
// reservation stays with it.
func TestDeleteExpiredUploadReleasesOnlyWhenExpired(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "A")
	f := newFolderRow(t, s, acc, "Docs", "docs")
	u := newUploadRow(t, s, acc, f.ID, "", 300)
	before := u.ExpiresAt.Add(-time.Second) // not yet expired at this time
	var gone bool
	if err := s.Write(ctx, func(tx *Tx) error {
		var err error
		gone, err = tx.DeleteExpiredUpload(ctx, acc, u.ID, before, before.Add(-time.Hour))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if gone || usageRow(t, s, acc).Reserved != 300 {
		t.Fatalf("a live upload was released: gone=%v reserved=%d", gone, usageRow(t, s, acc).Reserved)
	}
	after := u.ExpiresAt.Add(time.Second)
	if err := s.Write(ctx, func(tx *Tx) error {
		var err error
		gone, err = tx.DeleteExpiredUpload(ctx, acc, u.ID, after, after.Add(-time.Hour))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !gone || usageRow(t, s, acc).Reserved != 0 {
		t.Errorf("expired upload not released: gone=%v reserved=%d", gone, usageRow(t, s, acc).Reserved)
	}
}

// TestDeleteNodeReleasesUploadsBelowIt: the foreign key deletes uploads whose target directory is
// removed, so their reservations are released in the same delete.
func TestDeleteNodeReleasesUploadsBelowIt(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "A")
	f := newFolderRow(t, s, acc, "Docs", "docs")
	root, err := s.RootNode(ctx, acc, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	dir := &Node{ID: id.New("nod"), AccountID: acc, FolderID: f.ID, ParentID: root.ID, Name: "d", NameKey: "d",
		Kind: KindDir, CreatedAt: Now(), ModifiedAt: Now()}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertNode(ctx, dir) }); err != nil {
		t.Fatal(err)
	}
	newUploadRow(t, s, acc, f.ID, dir.ID, 400)
	kept := newUploadRow(t, s, acc, f.ID, "", 100) // targets the root, so it survives
	if got := usageRow(t, s, acc).Reserved; got != 500 {
		t.Fatalf("reserved = %d; want 500", got)
	}
	if err := s.Write(ctx, func(tx *Tx) error { _, _, err := tx.PurgeNode(ctx, acc, dir.ID); return err }); err != nil {
		t.Fatal(err)
	}
	if got := usageRow(t, s, acc).Reserved; got != 100 {
		t.Errorf("reserved after deleting the directory = %d; want 100 (the root upload)", got)
	}
	if _, err := s.Upload(ctx, acc, kept.ID); err != nil {
		t.Errorf("the upload at the root was removed: %v", err)
	}
}

// TestDeleteFolderReleasesItsUploads: deleting a folder removes its uploads and frees their reservations.
func TestDeleteFolderReleasesItsUploads(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "A")
	f := newFolderRow(t, s, acc, "Docs", "docs")
	g := newFolderRow(t, s, acc, "Other", "other")
	newUploadRow(t, s, acc, f.ID, "", 250)
	newUploadRow(t, s, acc, g.ID, "", 75)
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteFolder(ctx, acc, f.ID) }); err != nil {
		t.Fatal(err)
	}
	if got := usageRow(t, s, acc).Reserved; got != 75 {
		t.Errorf("reserved after deleting a folder = %d; want 75", got)
	}
}

// TestDeleteFolderReleasesUploadsInDirsMovedIn: an upload aimed at a directory keeps its own folder
// when the directory moves to another folder. Deleting the second folder deletes the directory and,
// through the foreign key, the upload, so its reservation must be released too.
func TestDeleteFolderReleasesUploadsInDirsMovedIn(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "A")
	f := newFolderRow(t, s, acc, "Docs", "docs")
	g := newFolderRow(t, s, acc, "Other", "other")
	froot, err := s.RootNode(ctx, acc, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	groot, err := s.RootNode(ctx, acc, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	dir := &Node{ID: id.New("nod"), AccountID: acc, FolderID: f.ID, ParentID: froot.ID, Name: "d", NameKey: "d",
		Kind: KindDir, CreatedAt: Now(), ModifiedAt: Now()}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertNode(ctx, dir) }); err != nil {
		t.Fatal(err)
	}
	moved := newUploadRow(t, s, acc, f.ID, dir.ID, 400)
	newUploadRow(t, s, acc, f.ID, "", 100) // stays in Docs, which is not deleted

	// Move the directory to Other, as a move across folders does.
	if err := s.Write(ctx, func(tx *Tx) error {
		dir.ParentID = groot.ID
		dir.FolderID = g.ID
		if err := tx.UpdateNode(ctx, dir); err != nil {
			return err
		}
		return tx.MoveSubtree(ctx, acc, dir.ID, f.ID, g.ID)
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteFolder(ctx, acc, g.ID) }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upload(ctx, acc, moved.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the upload into the moved directory survived its folder: %v", err)
	}
	if got := usageRow(t, s, acc).Reserved; got != 100 {
		t.Errorf("reserved after deleting the folder the directory moved to = %d; want 100", got)
	}
}

// TestRecountAccountRepairsDrift: drifted counters are set back to what the rows say, and the
// figures before and after are returned.
func TestRecountAccountRepairsDrift(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "A")
	f := newFolderRow(t, s, acc, "Docs", "docs")
	root, err := s.RootNode(ctx, acc, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	file := &Node{ID: id.New("nod"), AccountID: acc, FolderID: f.ID, ParentID: root.ID, Name: "a.txt", NameKey: "a.txt",
		Kind: KindFile, Size: 100, BlobID: "blb_test", ETag: `"e"`, CreatedAt: Now(), ModifiedAt: Now()}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertNode(ctx, file) }); err != nil {
		t.Fatal(err)
	}
	newUploadRow(t, s, acc, f.ID, "", 40)
	// Corrupt the counters directly, as a bug would.
	if _, err := s.w.ExecContext(ctx, "UPDATE accounts SET used_bytes = 9999, used_files = 7, reserved_bytes = 3 WHERE id = ?", acc); err != nil {
		t.Fatal(err)
	}
	var before, after Usage
	if err := s.Write(ctx, func(tx *Tx) error {
		var err error
		before, after, err = tx.RecountAccount(ctx, acc)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if before != (Usage{Bytes: 9999, Files: 7, Reserved: 3}) {
		t.Errorf("before = %+v", before)
	}
	want := Usage{Bytes: 100, Files: 1, Reserved: 40}
	if after != want || usageRow(t, s, acc) != want {
		t.Errorf("after = %+v, stored = %+v; want %+v", after, usageRow(t, s, acc), want)
	}
}

// TestUsageTotalsAndReservationTotals: the installation totals are the sums over accounts.
func TestUsageTotalsAndReservationTotals(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	b := newAccountRow(t, s, "B")
	fa := newFolderRow(t, s, a, "Docs", "docs")
	newUploadRow(t, s, a, fa.ID, "", 30)
	newUploadRow(t, s, b, newFolderRow(t, s, b, "Docs", "docs").ID, "", 70)
	used, reserved, err := s.UsageTotals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if used != 0 || reserved != 100 {
		t.Errorf("totals = %d used, %d reserved; want 0 and 100", used, reserved)
	}
	var counted, declared int64
	if err := s.Write(ctx, func(tx *Tx) error {
		var err error
		counted, declared, err = tx.ReservationTotals(ctx)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if counted != 100 || declared != 100 {
		t.Errorf("reservation totals = %d, %d; want 100, 100", counted, declared)
	}
}

// TestUnwrittenBytes: the disk counts only what uploads still have to write, per account and overall.
func TestUnwrittenBytes(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	b := newAccountRow(t, s, "B")
	fa := newFolderRow(t, s, a, "Docs", "docs")
	fb := newFolderRow(t, s, b, "Docs", "docs")
	newUploadRow(t, s, a, fa.ID, "", 50)
	newUploadRow(t, s, b, fb.ID, "", 20)
	mine, total, err := s.UnwrittenBytes(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if mine != 50 || total != 70 {
		t.Errorf("unwritten = %d mine, %d total; want 50 and 70", mine, total)
	}
}
