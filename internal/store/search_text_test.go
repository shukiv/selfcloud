// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// Content rows (slice q3, decision 46): the rules the store keeps. The worker's extraction is tested in internal/search and
// in the api package; here the rows, their full-text table, the backlog cursor and the fairness of the claim are checked.

// addBlob makes a live blob row for account acc.
func addBlob(t *testing.T, s *Store, blobID, acc string, size int) {
	t.Helper()
	if _, err := s.w.ExecContext(context.Background(), `INSERT INTO blobs (id, account_id, size, sha256, state, created_at)
		VALUES (?, ?, ?, 'x', 'live', 0)`, blobID, acc, size); err != nil {
		t.Fatal(err)
	}
}

// addFileNode writes a file node in folder fld_a that holds blob blobID.
func addFileNode(t *testing.T, s *Store, nodeID, blobID string) {
	t.Helper()
	err := s.Write(context.Background(), func(tx *Tx) error {
		n := node(nodeID, "fld_a", "nod_root", nodeID+".txt", KindFile)
		n.BlobID = blobID
		n.Size = 10
		return tx.InsertNode(context.Background(), n)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// scalarCount reads one count, failing the test on error.
func scalarCount(t *testing.T, s *Store, q string, args ...any) int {
	t.Helper()
	var n int
	if err := s.r.QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// readyText makes blob's row ready with text, as the worker would, through FinishText.
func readyText(t *testing.T, s *Store, blobID, acc, text string) {
	t.Helper()
	var id int64
	if err := s.r.QueryRowContext(context.Background(), "SELECT id FROM search_texts WHERE blob_id = ?", blobID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishText(context.Background(), TextResult{ID: id, AccountID: acc, BlobID: blobID,
		State: TextReady, Source: "plain", Text: text, Bytes: int64(len(text))}); err != nil {
		t.Fatal(err)
	}
}

func TestFileNodeQueuesItsContentRowOnce(t *testing.T) {
	t.Parallel()
	s, _ := searchFixture(t)
	addBlob(t, s, "blb_1", "acc_a", 10)
	addFileNode(t, s, "nod_one", "blb_1")
	addFileNode(t, s, "nod_two", "blb_1") // a copy shares the blob: one row
	if n := scalarCount(t, s, "SELECT COUNT(*) FROM search_texts WHERE blob_id = 'blb_1' AND state = 'pending'"); n != 1 {
		t.Fatalf("content rows for a shared blob: %d, want 1", n)
	}
}

func TestNewBlobOnAnUpdateQueuesItsRow(t *testing.T) {
	t.Parallel()
	s, _ := searchFixture(t)
	addBlob(t, s, "blb_1", "acc_a", 10)
	addBlob(t, s, "blb_2", "acc_a", 10)
	addFileNode(t, s, "nod_one", "blb_1")
	err := s.Write(context.Background(), func(tx *Tx) error {
		n := node("nod_one", "fld_a", "nod_root", "nod_one.txt", KindFile)
		n.BlobID = "blb_2" // a new version
		n.Size = 10
		return tx.UpdateNode(context.Background(), n)
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := scalarCount(t, s, "SELECT COUNT(*) FROM search_texts WHERE blob_id = 'blb_2'"); n != 1 {
		t.Fatalf("the new version's blob has %d content rows, want 1", n)
	}
}

// TestOrphanDeleteRemovesTheFullTextRowInTheSameWrite: the cascade from blobs does not reach the full-text table, so a
// blob's text must leave the index when the blob goes. Mutation: dropText removed from DeleteOrphan leaves the text
// indexed, and this test fails.
func TestOrphanDeleteRemovesTheFullTextRowInTheSameWrite(t *testing.T) {
	t.Parallel()
	s, _ := searchFixture(t)
	addBlob(t, s, "blb_1", "acc_a", 10)
	addFileNode(t, s, "nod_one", "blb_1")
	readyText(t, s, "blb_1", "acc_a", "secret word")
	if n := scalarCount(t, s, "SELECT COUNT(*) FROM search_text_fts"); n != 1 {
		t.Fatalf("setup: %d full-text rows, want 1", n)
	}
	if _, err := s.w.ExecContext(context.Background(), "DELETE FROM nodes WHERE id = 'nod_one'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(context.Background(), "UPDATE blobs SET state = 'orphan', delete_after = 0 WHERE id = 'blb_1'"); err != nil {
		t.Fatal(err)
	}
	var removed bool
	err := s.Write(context.Background(), func(tx *Tx) error {
		var err error
		removed, err = tx.DeleteOrphan(context.Background(), "acc_a", "blb_1")
		return err
	})
	if err != nil || !removed {
		t.Fatalf("DeleteOrphan removed %v, err %v", removed, err)
	}
	if n := scalarCount(t, s, "SELECT COUNT(*) FROM search_texts"); n != 0 {
		t.Errorf("content rows after the blob went: %d", n)
	}
	if n := scalarCount(t, s, "SELECT COUNT(*) FROM search_text_fts"); n != 0 {
		t.Errorf("full-text rows after the blob went: %d (the text of a deleted file is still indexed)", n)
	}
}

// TestOrphanDeleteOfAUsedBlobKeepsItsText: a blob a node or a version still uses is not removed, so its text stays.
func TestOrphanDeleteOfAUsedBlobKeepsItsText(t *testing.T) {
	t.Parallel()
	s, _ := searchFixture(t)
	addBlob(t, s, "blb_1", "acc_a", 10)
	addFileNode(t, s, "nod_one", "blb_1")
	readyText(t, s, "blb_1", "acc_a", "kept")
	if _, err := s.w.ExecContext(context.Background(), "UPDATE blobs SET state = 'orphan', delete_after = 0 WHERE id = 'blb_1'"); err != nil {
		t.Fatal(err)
	}
	var removed bool
	err := s.Write(context.Background(), func(tx *Tx) error {
		var err error
		removed, err = tx.DeleteOrphan(context.Background(), "acc_a", "blb_1")
		return err
	})
	if err != nil || removed {
		t.Fatalf("DeleteOrphan of a used blob removed %v, err %v", removed, err)
	}
	if n := scalarCount(t, s, "SELECT COUNT(*) FROM search_text_fts"); n != 1 {
		t.Errorf("the used blob's text left the index: %d full-text rows", n)
	}
}

// TestDeletingAnAccountRemovesItsContentText: the account's full-text rows go with it (mutation: dropTextAccount removed).
func TestDeletingAnAccountRemovesItsContentText(t *testing.T) {
	t.Parallel()
	s, _ := searchFixture(t)
	ctx := context.Background()
	if _, err := s.w.ExecContext(ctx, `INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_b', 'B', 'active', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	addBlob(t, s, "blb_b", "acc_b", 10)
	if _, err := s.w.ExecContext(ctx, `INSERT INTO search_texts (blob_id, account_id, state, source) VALUES ('blb_b', 'acc_b', 'ready', 'plain')`); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := s.r.QueryRowContext(ctx, "SELECT id FROM search_texts WHERE blob_id = 'blb_b'").Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, "INSERT INTO search_text_fts (rowid, text, account_id) VALUES (?, 'bee', 'acc_b')", id); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteAccount(ctx, "acc_b") }); err != nil {
		t.Fatal(err)
	}
	if n := scalarCount(t, s, "SELECT COUNT(*) FROM search_text_fts"); n != 0 {
		t.Errorf("full-text rows of a deleted account: %d", n)
	}
}

// TestClaimIsRoundRobinAcrossAccounts: an account with 1000 pending rows does not hold the worker: the one row of
// another account is claimed on the second claim. Mutation: ordering by id alone serves the big account first, and the
// test fails.
func TestClaimIsRoundRobinAcrossAccounts(t *testing.T) {
	t.Parallel()
	s, _ := searchFixture(t)
	ctx := context.Background()
	if _, err := s.w.ExecContext(ctx, `INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_b', 'B', 'active', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 1000)
		INSERT INTO blobs (id, account_id, size, sha256, state, created_at)
		SELECT 'blb_a' || i, 'acc_a', 1, 'x', 'live', 0 FROM n`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 1000)
		INSERT INTO search_texts (blob_id, account_id, state) SELECT 'blb_a' || i, 'acc_a', 'pending' FROM n`); err != nil {
		t.Fatal(err)
	}
	addBlob(t, s, "blb_b", "acc_b", 1)
	if _, err := s.w.ExecContext(ctx, `INSERT INTO search_texts (blob_id, account_id, state) VALUES ('blb_b', 'acc_b', 'pending')`); err != nil {
		t.Fatal(err)
	}
	now := time.UnixMilli(1)
	first, ok, err := s.ClaimText(ctx, "", now)
	if err != nil || !ok || first.AccountID != "acc_a" {
		t.Fatalf("first claim %+v ok %v err %v", first, ok, err)
	}
	second, ok, err := s.ClaimText(ctx, first.AccountID, now)
	if err != nil || !ok || second.AccountID != "acc_b" {
		t.Fatalf("second claim %+v ok %v err %v: the other account waits behind 999 rows", second, ok, err)
	}
}

// TestBackfillIsBoundedAndResumes: each step queues at most its limit, the cursor is in the database, and a restart
// continues from it rather than from the first blob.
func TestBackfillIsBoundedAndResumes(t *testing.T) {
	t.Parallel()
	s, path := searchFixture(t)
	ctx := context.Background()
	for i := 1; i <= 25; i++ {
		blob := fmt.Sprintf("blb_%02d", i)
		addBlob(t, s, blob, "acc_a", 1)
		addFileNode(t, s, fmt.Sprintf("nod_%02d", i), blob)
	}
	// The migration left no rows for the backfill to find: the nodes above were queued by their own writes. Remove them, so
	// the backfill has the work it has on an upgrade.
	if _, err := s.w.ExecContext(ctx, "DELETE FROM search_texts"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, "UPDATE search_text_backfill SET after = '', done = 0 WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	done, err := s.TextBackfillStep(ctx, 10)
	if err != nil || done {
		t.Fatalf("first step done %v err %v", done, err)
	}
	if n := scalarCount(t, s, "SELECT COUNT(*) FROM search_texts"); n != 10 {
		t.Fatalf("first step queued %d rows, want 10", n)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, Options{Path: filepath.Clean(path)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if done, err := s.TextBackfillStep(ctx, 10); err != nil || done {
		t.Fatalf("second step after a restart: done %v err %v", done, err)
	}
	if n := scalarCount(t, s, "SELECT COUNT(*) FROM search_texts"); n != 20 {
		t.Fatalf("after a restart the step did not resume: %d rows, want 20", n)
	}
	if done, err := s.TextBackfillStep(ctx, 10); err != nil || !done {
		t.Fatalf("last short step: done %v err %v", done, err)
	}
	if n := scalarCount(t, s, "SELECT COUNT(*) FROM search_texts"); n != 25 {
		t.Fatalf("backfill queued %d rows, want 25", n)
	}
}

// TestSearchCheckCountsMissingContentRowsOnceTheBackfillIsDone: a file node with no content row is repaired by starting
// the backfill again, which queues only the missing rows.
func TestSearchCheckCountsMissingContentRowsOnceTheBackfillIsDone(t *testing.T) {
	t.Parallel()
	s, _ := searchFixture(t)
	ctx := context.Background()
	addBlob(t, s, "blb_1", "acc_a", 1)
	if _, err := s.w.ExecContext(ctx, `INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, size, blob_id, created_at, modified_at)
		VALUES ('nod_x', 'acc_a', 'fld_a', 'nod_root', 'x.txt', 'x.txt', 'file', 1, 'blb_1', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	// Backfill not done: the missing row is work on its way, not drift.
	d, err := s.SearchCheck(ctx)
	if err != nil || d.TextMissing != 0 {
		t.Fatalf("while the backfill runs: missing %d err %v", d.TextMissing, err)
	}
	for {
		done, err := s.TextBackfillStep(ctx, 100)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
	}
	if _, err := s.w.ExecContext(ctx, "DELETE FROM search_texts"); err != nil {
		t.Fatal(err)
	}
	d, err = s.SearchCheck(ctx)
	if err != nil || d.TextMissing != 1 {
		t.Fatalf("after the backfill: missing %d err %v, want 1", d.TextMissing, err)
	}
	if err := s.SearchRepair(ctx); err != nil {
		t.Fatal(err)
	}
	if done, err := s.TextBackfillDone(ctx); err != nil || done {
		t.Fatalf("repair did not start the backfill again: done %v err %v", done, err)
	}
}

// TestSearchRebuildEmptiesTheContentRowsAndQueuesTheBackfill: the rebuild command covers content (decision 46).
func TestSearchRebuildEmptiesTheContentRowsAndQueuesTheBackfill(t *testing.T) {
	t.Parallel()
	s, _ := searchFixture(t)
	addBlob(t, s, "blb_1", "acc_a", 10)
	addFileNode(t, s, "nod_one", "blb_1")
	readyText(t, s, "blb_1", "acc_a", "gone after rebuild")
	if err := s.SearchRebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := scalarCount(t, s, "SELECT COUNT(*) FROM search_texts"); n != 0 {
		t.Errorf("content rows after rebuild: %d", n)
	}
	if n := scalarCount(t, s, "SELECT COUNT(*) FROM search_text_fts"); n != 0 {
		t.Errorf("full-text rows after rebuild: %d", n)
	}
	if done, err := s.TextBackfillDone(context.Background()); err != nil || done {
		t.Errorf("rebuild left the backfill done: %v %v", done, err)
	}
	// The usage goes with the text in the rebuild's write (review, decision 46): a rebuild that kept it would count the
	// bytes twice once the text came back, and would fill the cap.
	if u := scalarCount(t, s, "SELECT COALESCE(SUM(bytes), 0) FROM search_text_usage"); u != 0 {
		t.Fatalf("usage after rebuild: %d bytes, want 0", u)
	}
	for {
		done, err := s.TextBackfillStep(context.Background(), 100)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
	}
	const again = "back after the rebuild"
	readyText(t, s, "blb_1", "acc_a", again)
	if u := scalarCount(t, s, "SELECT COALESCE(SUM(bytes), 0) FROM search_text_usage"); u != len(again) {
		t.Errorf("usage after re-extraction: %d bytes, want %d (the text once)", u, len(again))
	}
}

// TestPurgeDropsItsTextInTheSameWrite (review, decision 46): a purged file's text leaves the full-text table, the content
// row and the usage in the purge's own write, not at the next hourly sweep. The marker leaves the database file after a
// truncating checkpoint, and the -wal file too.
func TestPurgeDropsItsTextInTheSameWrite(t *testing.T) {
	t.Parallel()
	s, path := searchFixture(t)
	ctx := context.Background()
	addBlob(t, s, "blb_1", "acc_a", 10)
	addFileNode(t, s, "nod_one", "blb_1")
	readyText(t, s, "blb_1", "acc_a", "zqxpurgemarker words")
	if err := s.Write(ctx, func(tx *Tx) error {
		_, _, err := tx.PurgeNode(ctx, "acc_a", "nod_one")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if n := scalarCount(t, s, "SELECT COUNT(*) FROM search_text_fts"); n != 0 {
		t.Errorf("full-text rows after purge: %d, want 0", n)
	}
	if n := scalarCount(t, s, "SELECT COUNT(*) FROM search_texts"); n != 0 {
		t.Errorf("content rows after purge: %d, want 0", n)
	}
	if u := scalarCount(t, s, "SELECT COALESCE(SUM(bytes), 0) FROM search_text_usage"); u != 0 {
		t.Errorf("usage after purge: %d bytes, want 0", u)
	}
	if _, err := s.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if fileHolds(t, path, "zqxpurgemarker") {
		t.Error("the purged text is in the database file after a truncating checkpoint")
	}
	if fileHolds(t, path+"-wal", "zqxpurgemarker") {
		t.Error("the purged text is in the -wal file after a truncating checkpoint")
	}
}

// TestCopyCleanupDropsTextOfAStagedOnlyBlob (review, decision 46): the staged copy of a file is the only holder of its blob
// once the source lets go. The cleanup of the copy removes the staged node and its text in the cleanup's writes, not at
// the sweep.
func TestCopyCleanupDropsTextOfAStagedOnlyBlob(t *testing.T) {
	fx := newCopyFixture(t)
	ctx := context.Background()
	addBlob(t, fx.s, "blb_s1", "acc_a", 3)
	if _, err := fx.s.w.ExecContext(ctx, "UPDATE nodes SET blob_id = 'blb_s1' WHERE id = 'nod_s1'"); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.s.w.ExecContext(ctx, "INSERT INTO search_texts (blob_id, account_id, state) VALUES ('blb_s1', 'acc_a', 'pending')"); err != nil {
		t.Fatal(err)
	}
	readyText(t, fx.s, "blb_s1", "acc_a", "stagedonlyword")
	if err := fx.s.Write(ctx, func(tx *Tx) error {
		cur, err := tx.CopyJobOf(ctx, "job_1")
		if err != nil {
			return err
		}
		_, _, err = tx.StagedCopyBatch(ctx, cur, 10, true, "key_1", Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.s.w.ExecContext(ctx, "UPDATE nodes SET blob_id = NULL WHERE id = 'nod_s1'"); err != nil {
		t.Fatal(err)
	}
	if err := fx.s.Write(ctx, func(tx *Tx) error {
		return tx.SetCopyPhase(ctx, "job_1", CopyPhaseCleanup, "injected", Now())
	}); err != nil {
		t.Fatal(err)
	}
	for steps := 0; ; steps++ {
		done, err := fx.s.CleanupCopyStep(ctx, "job_1", 2, Now())
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
		if steps > 20 {
			t.Fatal("cleanup did not finish")
		}
	}
	if n := scalarCount(t, fx.s, "SELECT COUNT(*) FROM search_text_fts"); n != 0 {
		t.Errorf("full-text rows after the copy's cleanup: %d, want 0", n)
	}
	if u := scalarCount(t, fx.s, "SELECT COALESCE(SUM(bytes), 0) FROM search_text_usage"); u != 0 {
		t.Errorf("usage after the copy's cleanup: %d bytes, want 0", u)
	}
	if n := scalarCount(t, fx.s, "SELECT COUNT(*) FROM search_texts WHERE blob_id = 'blb_s1'"); n != 0 {
		t.Errorf("content rows after the copy's cleanup: %d, want 0", n)
	}
}

// TestPublishKeepsTheTextOfAPublishedFile (review, decision 46): a publish moves the staged files into the destination,
// and their text stays with their blobs. The publish's own deletes do not take it.
func TestPublishKeepsTheTextOfAPublishedFile(t *testing.T) {
	fx := newCopyFixture(t)
	ctx := context.Background()
	addBlob(t, fx.s, "blb_s1", "acc_a", 3)
	if _, err := fx.s.w.ExecContext(ctx, "UPDATE nodes SET blob_id = 'blb_s1' WHERE id = 'nod_s1'"); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.s.w.ExecContext(ctx, "INSERT INTO search_texts (blob_id, account_id, state) VALUES ('blb_s1', 'acc_a', 'pending')"); err != nil {
		t.Fatal(err)
	}
	readyText(t, fx.s, "blb_s1", "acc_a", "publishedword")
	if err := fx.s.Write(ctx, func(tx *Tx) error {
		cur, err := tx.CopyJobOf(ctx, "job_1")
		if err != nil {
			return err
		}
		if _, _, err := tx.StagedCopyBatch(ctx, cur, 10, true, "key_1", Now()); err != nil {
			return err
		}
		return tx.PublishStaged(ctx, cur, "fld_a", "nod_root", "copy", "copy", Now())
	}); err != nil {
		t.Fatal(err)
	}
	if n := scalarCount(t, fx.s, "SELECT COUNT(*) FROM search_text_fts"); n != 1 {
		t.Errorf("full-text rows after publish: %d, want 1 (the blob is still held)", n)
	}
	if u := scalarCount(t, fx.s, "SELECT COALESCE(SUM(bytes), 0) FROM search_text_usage"); u != len("publishedword") {
		t.Errorf("usage after publish: %d bytes, want %d", u, len("publishedword"))
	}
}

// TestPurgeKeepsTextAnotherNodeHolds: the blob of a purged node stays, with its text, while a copy elsewhere holds it.
func TestPurgeKeepsTextAnotherNodeHolds(t *testing.T) {
	t.Parallel()
	s, _ := searchFixture(t)
	ctx := context.Background()
	addBlob(t, s, "blb_1", "acc_a", 10)
	addFileNode(t, s, "nod_one", "blb_1")
	addFileNode(t, s, "nod_two", "blb_1")
	readyText(t, s, "blb_1", "acc_a", "kept copy words")
	if err := s.Write(ctx, func(tx *Tx) error {
		_, _, err := tx.PurgeNode(ctx, "acc_a", "nod_one")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if n := scalarCount(t, s, "SELECT COUNT(*) FROM search_text_fts"); n != 1 {
		t.Errorf("full-text rows after purging one copy: %d, want 1", n)
	}
}

// TestFinishTextKeepsNothingForAnUnusedBlob: a result for a blob no node or version uses is dropped with its row, so a
// later restore queues the blob again.
func TestFinishTextKeepsNothingForAnUnusedBlob(t *testing.T) {
	t.Parallel()
	s, _ := searchFixture(t)
	ctx := context.Background()
	addBlob(t, s, "blb_1", "acc_a", 10)
	if _, err := s.w.ExecContext(ctx, `INSERT INTO search_texts (blob_id, account_id, state) VALUES ('blb_1', 'acc_a', 'pending')`); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := s.r.QueryRowContext(ctx, "SELECT id FROM search_texts WHERE blob_id = 'blb_1'").Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishText(ctx, TextResult{ID: id, AccountID: "acc_a", BlobID: "blb_1", State: TextReady, Text: "late"}); err != nil {
		t.Fatal(err)
	}
	if n := scalarCount(t, s, "SELECT COUNT(*) FROM search_texts"); n != 0 {
		t.Errorf("a row for an unused blob was kept: %d", n)
	}
	if n := scalarCount(t, s, "SELECT COUNT(*) FROM search_text_fts"); n != 0 {
		t.Errorf("text of an unused blob was indexed: %d", n)
	}
}

// TestSticky refusal is retried after its wait, and a transient one is not made failed.
func TestReleaseTextRetriesMakesTheDueFailedRowsPending(t *testing.T) {
	t.Parallel()
	s, _ := searchFixture(t)
	ctx := context.Background()
	addBlob(t, s, "blb_1", "acc_a", 10)
	addFileNode(t, s, "nod_one", "blb_1")
	if _, err := s.w.ExecContext(ctx, "UPDATE search_texts SET state = 'failed', next_try = 1000 WHERE blob_id = 'blb_1'"); err != nil {
		t.Fatal(err)
	}
	n, err := s.ReleaseTextRetries(ctx, time.UnixMilli(999))
	if err != nil || n != 0 {
		t.Fatalf("before the wait: released %d err %v", n, err)
	}
	n, err = s.ReleaseTextRetries(ctx, time.UnixMilli(1000))
	if err != nil || n != 1 {
		t.Fatalf("after the wait: released %d err %v", n, err)
	}
	if got := scalarCount(t, s, "SELECT COUNT(*) FROM search_texts WHERE state = 'pending'"); got != 1 {
		t.Errorf("pending rows after the release: %d", got)
	}
}
