// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// copyFixture is an account with a source tree (a folder "src" holding two files and a subfolder with one file), and
// a chunked copy of it queued with its job row, its staging folder and its snapshot. The job is not run.
type copyFixture struct {
	s   *Store
	job *CopyJob
}

func newCopyFixture(t *testing.T) *copyFixture {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "copy.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	setup := []string{
		`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_a', 'A', 'active', 0, 0)`,
		`INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at) VALUES ('fld_a', 'acc_a', 'Docs', 'docs', 0, 0)`,
		`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
			VALUES ('nod_root', 'acc_a', 'fld_a', NULL, '', '', 'dir', 0, 0)`,
		`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
			VALUES ('nod_src', 'acc_a', 'fld_a', 'nod_root', 'src', 'src', 'dir', 0, 0)`,
		`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, size, etag, created_at, modified_at)
			VALUES ('nod_s1', 'acc_a', 'fld_a', 'nod_src', 'a.txt', 'a.txt', 'file', 3, 'e', 0, 0),
			('nod_s2', 'acc_a', 'fld_a', 'nod_src', 'b.txt', 'b.txt', 'file', 4, 'e', 0, 0),
			('nod_sub', 'acc_a', 'fld_a', 'nod_src', 'sub', 'sub', 'dir', 0, 0, 0, 0)`,
		`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, size, etag, created_at, modified_at)
			VALUES ('nod_s3', 'acc_a', 'fld_a', 'nod_sub', 'c.txt', 'c.txt', 'file', 5, 'e', 0, 0)`,
	}
	for _, q := range setup {
		if _, err := s.w.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	job := &CopyJob{JobID: "job_1", AccountID: "acc_a", StagingFolderID: "fld_stage", StagingRootID: "nod_stage_root",
		RootID: "nod_copy", SourceID: "nod_src", DestFolderID: "fld_a", DestParentID: "nod_root", Name: "copy",
		NameKey: "copy", KeepSharing: true, Phase: CopyPhaseCopy, ReservedBytes: 12, Actor: "{}", CreatedAt: Now(), UpdatedAt: Now()}
	if err := s.Write(ctx, func(tx *Tx) error {
		if err := tx.InsertJob(ctx, &Job{ID: "job_1", AccountID: "acc_a", Kind: "node.copy", Target: "nod_copy",
			Status: JobQueued, CreatedAt: Now(), UpdatedAt: Now()}); err != nil {
			return err
		}
		if err := tx.CreateStagingFolder(ctx, "acc_a", "fld_stage", "nod_stage_root", Now()); err != nil {
			return err
		}
		if _, err := tx.q.ExecContext(ctx, `UPDATE folders SET reserved_bytes = 12 WHERE id = 'fld_stage'`); err != nil {
			return err
		}
		if _, err := tx.q.ExecContext(ctx, `UPDATE accounts SET reserved_bytes = 12 WHERE id = 'acc_a'`); err != nil {
			return err
		}
		if err := tx.InsertCopyJob(ctx, job); err != nil {
			return err
		}
		rows, err := s.SubtreeRows(ctx, "acc_a", "nod_src", 1000)
		if err != nil {
			return err
		}
		return tx.InsertCopySnapshot(ctx, "job_1", 1, rows)
	}); err != nil {
		t.Fatal(err)
	}
	return &copyFixture{s: s, job: job}
}

// staged counts the nodes in the staging folder.
func (fx *copyFixture) staged(t *testing.T) int {
	t.Helper()
	var n int
	if err := fx.s.r.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM nodes WHERE folder_id = 'fld_stage'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestCopyBatchesStageTheTreeInOrder checks the batch: every snapshot node is staged under the new root, with its
// parents, and the cursor and counts follow.
func TestCopyBatchesStageTheTreeInOrder(t *testing.T) {
	fx := newCopyFixture(t)
	ctx := context.Background()
	var copied int
	var more bool
	if err := fx.s.Write(ctx, func(tx *Tx) error {
		cur, err := tx.CopyJobOf(ctx, "job_1")
		if err != nil {
			return err
		}
		copied, more, err = tx.StagedCopyBatch(ctx, cur, 10, true, "key_1", Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if copied != 5 || more {
		t.Fatalf("batch copied %d, more %v; want 5, false", copied, more)
	}
	if n := fx.staged(t); n != 6 { // the five nodes and the staging root
		t.Errorf("staged nodes = %d, want 6", n)
	}
	cj, err := fx.s.CopyJobOf(ctx, "job_1")
	if err != nil {
		t.Fatal(err)
	}
	if cj.Nodes != 5 || cj.Files != 3 || cj.Bytes != 12 || cj.Cursor != 5 {
		t.Errorf("counts after the batch = nodes %d, files %d, bytes %d, cursor %d; want 5, 3, 12, 5", cj.Nodes, cj.Files, cj.Bytes, cj.Cursor)
	}
}

// TestLargestFilesHidesAStagedTree checks that the largest-files listing, which reads folders in a join and can name
// every folder of the account, shows the source's three files and not the copy's three while the copy is staged, for
// any kind of folder (the staging folder is a team folder, so the team filter must not show it either).
func TestLargestFilesHidesAStagedTree(t *testing.T) {
	fx := newCopyFixture(t)
	ctx := context.Background()
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
	for _, kind := range []string{"", KindTeam} {
		files, _, err := fx.s.LargestFiles(ctx, "acc_a", "", kind, Page{Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		if len(files) != 3 {
			t.Errorf("largest files (kind %q) while a copy is staged = %d, want the source's 3 only", kind, len(files))
		}
		for _, f := range files {
			if f.FolderID == "fld_stage" {
				t.Errorf("largest files (kind %q) lists a staged file %s", kind, f.ID)
			}
		}
		// The page is cut before the folder filter in the outer query, so a staged file that is larger than the source's
		// would fill a page slot. The copies have the sizes of the source's files: the first page of two must still hold two.
		page, next, err := fx.s.LargestFiles(ctx, "acc_a", "", kind, Page{Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if len(page) != 2 || next == "" {
			t.Errorf("first page of 2 (kind %q) = %d files, next %q; want 2 files and a next page", kind, len(page), next)
		}
	}
}

// TestCopyBatchSkipsASourceDeletedSinceTheSnapshot checks that a snapshot row whose node is gone is skipped, and that a
// node below a skipped node is skipped with it, so no copy is left with a missing parent.
func TestCopyBatchFailsOnASourceDeletedSinceTheSnapshot(t *testing.T) {
	fx := newCopyFixture(t)
	ctx := context.Background()
	if _, err := fx.s.w.ExecContext(ctx, `DELETE FROM nodes WHERE id = 'nod_sub'`); err != nil {
		t.Fatal(err)
	}
	before := fx.staged(t)
	err := fx.s.Write(ctx, func(tx *Tx) error {
		cur, err := tx.CopyJobOf(ctx, "job_1")
		if err != nil {
			return err
		}
		_, _, err = tx.StagedCopyBatch(ctx, cur, 10, true, "key_1", Now())
		return err
	})
	if !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("batch err = %v, want ErrSourceChanged", err)
	}
	if n := fx.staged(t); n != before {
		t.Errorf("staged after the failed batch = %d, want %d (the batch is rolled back whole)", n, before)
	}
}

// TestInsertCopySnapshotRefusesANodeTwice: a node listed by two snapshot pages means the tree changed between the reads.
func TestInsertCopySnapshotRefusesANodeTwice(t *testing.T) {
	fx := newCopyFixture(t)
	ctx := context.Background()
	err := fx.s.Write(ctx, func(tx *Tx) error {
		return tx.InsertCopySnapshot(ctx, "job_1", 99, []SubtreeRow{{ID: "nod_s1", Parent: "nod_src"}})
	})
	if !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("err = %v, want ErrSourceChanged for a node already in the snapshot", err)
	}
}

// TestCleanupStepsAreBounded removes a staged copy in steps of at most two nodes, and checks that every step leaves the
// database as a whole: nothing staged is left when the steps end, the reservation is released, and the job's row is gone.
func TestCleanupStepsAreBounded(t *testing.T) {
	fx := newCopyFixture(t)
	ctx := context.Background()
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
	if err := fx.s.Write(ctx, func(tx *Tx) error {
		return tx.SetCopyPhase(ctx, "job_1", CopyPhaseCleanup, "injected", Now())
	}); err != nil {
		t.Fatal(err)
	}
	steps := 0
	for {
		done, err := fx.s.CleanupCopyStep(ctx, "job_1", 2, Now())
		if err != nil {
			t.Fatal(err)
		}
		steps++
		if done {
			break
		}
		if n := fx.staged(t); n < 1 {
			t.Fatalf("after step %d no staged node is left, but the copy is not done", steps)
		}
		if steps > 20 {
			t.Fatal("cleanup did not finish")
		}
	}
	if steps != 3 {
		t.Errorf("cleanup took %d steps for 5 snapshot rows in steps of 2; want 3 (2, 2, 1)", steps)
	}
	if n := fx.staged(t); n != 0 {
		t.Errorf("staged nodes after cleanup = %d, want 0", n)
	}
	if _, err := fx.s.CopyJobOf(ctx, "job_1"); err == nil {
		t.Error("copy row after cleanup is still there")
	}
	var reserved int64
	if err := fx.s.r.QueryRowContext(ctx, `SELECT reserved_bytes FROM accounts WHERE id = 'acc_a'`).Scan(&reserved); err != nil {
		t.Fatal(err)
	}
	if reserved != 0 {
		t.Errorf("account reserved after cleanup = %d, want 0", reserved)
	}
	// The source is untouched: cleanup never deletes a node the copy did not stage.
	var srcs int
	if err := fx.s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes WHERE id IN ('nod_src','nod_s1','nod_s2','nod_sub','nod_s3')`).Scan(&srcs); err != nil {
		t.Fatal(err)
	}
	if srcs != 5 {
		t.Errorf("source nodes after cleanup = %d, want 5", srcs)
	}
}

// TestAbandonedCopiesAreListedOnlyWhenTheirJobFinished checks that the sweep finds a copy only once its job has ended, so
// a copy whose runner is still working is never touched.
func TestAbandonedCopiesAreListedOnlyWhenTheirJobFinished(t *testing.T) {
	fx := newCopyFixture(t)
	ctx := context.Background()
	list := func() []string {
		ids, err := fx.s.AbandonedCopyJobs(ctx, 10)
		if err != nil {
			t.Fatal(err)
		}
		return ids
	}
	if got := list(); len(got) != 0 {
		t.Fatalf("listed while the job is queued: %v", got)
	}
	if err := fx.s.FinishJob(ctx, "job_1", context.Canceled); err != nil {
		t.Fatal(err)
	}
	if got := list(); len(got) != 1 || got[0] != "job_1" {
		t.Errorf("listed after the job failed: %v, want [job_1]", got)
	}
}

// TestPublishedCopyCleanupNeverDeletesANode checks that after a publish the cleanup drops only the snapshot rows: the
// published nodes belong to the destination.
func TestPublishedCopyCleanupNeverDeletesANode(t *testing.T) {
	fx := newCopyFixture(t)
	ctx := context.Background()
	if err := fx.s.Write(ctx, func(tx *Tx) error {
		cur, err := tx.CopyJobOf(ctx, "job_1")
		if err != nil {
			return err
		}
		if _, _, err := tx.StagedCopyBatch(ctx, cur, 10, true, "key_1", Now()); err != nil {
			return err
		}
		if err := tx.PublishStaged(ctx, cur, "fld_a", "nod_root", "copy", "copy", Now()); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := fx.s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes WHERE folder_id = 'fld_a'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for {
		done, err := fx.s.CleanupCopyStep(ctx, "job_1", 2, Now())
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
	}
	var after int
	if err := fx.s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes WHERE folder_id = 'fld_a'`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	// Publishing moved the five copies into fld_a; cleanup of the published copy must remove none of them.
	if before != 11 || after != 11 {
		t.Errorf("nodes in the destination: %d before cleanup, %d after; want 11 and 11 (6 existing, 5 copies)", before, after)
	}
	if _, err := fx.s.CopyJobOf(ctx, "job_1"); err == nil {
		t.Error("published copy's row is still there after cleanup")
	}
}
