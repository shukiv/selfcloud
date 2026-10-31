// SPDX-License-Identifier: AGPL-3.0-or-later

package sweep

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/shukiv/selfcloud/internal/store"
)

// TestSweepFinishesACopyWhoseJobEnded checks that the sweep removes what a chunked copy left behind once its job has
// failed, and that it leaves alone a copy whose job is still queued.
func TestSweepFinishesACopyWhoseJobEnded(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "selfcloud.db")
	st, err := store.Open(ctx, store.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	// The account and folder rows are written straight to the database: the store has no exported writer for them
	// here, and the sweep under test does not depend on how they were made.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, q := range []string{
		`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_a', 'A', 'active', 0, 0)`,
		`INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at) VALUES ('fld_a', 'acc_a', 'Docs', 'docs', 0, 0)`,
		`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
			VALUES ('nod_root', 'acc_a', 'fld_a', NULL, '', '', 'dir', 0, 0)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	for _, jobID := range []string{"job_failed", "job_queued"} {
		if err := st.Write(ctx, func(tx *store.Tx) error {
			if err := tx.InsertJob(ctx, &store.Job{ID: jobID, AccountID: "acc_a", Kind: "node.copy", Target: "nod_" + jobID,
				Status: store.JobQueued, CreatedAt: store.Now(), UpdatedAt: store.Now()}); err != nil {
				return err
			}
			if err := tx.CreateStagingFolder(ctx, "acc_a", "fld_"+jobID, "nod_stage_"+jobID, store.Now()); err != nil {
				return err
			}
			return tx.InsertCopyJob(ctx, &store.CopyJob{JobID: jobID, AccountID: "acc_a", StagingFolderID: "fld_" + jobID,
				StagingRootID: "nod_stage_" + jobID, RootID: "nod_" + jobID, SourceID: "nod_root", DestFolderID: "fld_a",
				DestParentID: "nod_root", NameKey: "x", KeepSharing: false, Phase: store.CopyPhaseCleanup, Actor: "{}",
				CreatedAt: store.Now(), UpdatedAt: store.Now()})
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.FinishJob(ctx, "job_failed", errors.New("injected")); err != nil {
		t.Fatal(err)
	}
	w := &Sweeper{st: st}
	cleaned, err := w.cleanupCopies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cleaned != 1 {
		t.Errorf("copies cleaned = %d, want 1 (the failed job's)", cleaned)
	}
	if _, err := st.CopyJobOf(ctx, "job_failed"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("failed job's copy row: err = %v, want not found", err)
	}
	if _, err := st.CopyJobOf(ctx, "job_queued"); err != nil {
		t.Errorf("queued job's copy row: %v, want kept", err)
	}
}
