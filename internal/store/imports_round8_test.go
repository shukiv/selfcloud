// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"path/filepath"
	"testing"
)

// round8Live opens a store with one live folder fld_a: a root nod_root and a file nod_x, which is indexed for search.
func round8Live(t *testing.T) (*Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "round8.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	setup := []string{
		`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_a', 'A', 'active', 0, 0)`,
		`INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at) VALUES ('fld_a', 'acc_a', 'Docs', 'docs', 0, 0)`,
		`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
			VALUES ('nod_root', 'acc_a', 'fld_a', NULL, '', '', 'dir', 0, 0)`,
		`INSERT INTO blobs (id, account_id, size, sha256, state, created_at) VALUES ('blb_x', 'acc_a', 3, '00', 'live', 0)`,
		`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, size, etag, blob_id, created_at, modified_at)
			VALUES ('nod_x', 'acc_a', 'fld_a', 'nod_root', 'report.txt', 'report.txt', 'file', 3, 'e', 'blb_x', 0, 0)`,
	}
	for _, q := range setup {
		if _, err := s.w.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	return s, ctx
}

// count returns one count of the store, read in a fresh read.
func count(t *testing.T, s *Store, q string, args ...any) int {
	t.Helper()
	var n int
	if err := s.r.QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Removing a staging folder by an ID that is a live folder changes nothing: its search rows, its nodes and its row stay
// (round 8, R7-1). Before, the search rows went first, and only then did the staged check refuse the rest.
func TestRemoveStagingLeavesALiveFolderAlone(t *testing.T) {
	s, ctx := round8Live(t)
	const searchRows = `SELECT COUNT(*) FROM search_trigram WHERE rowid IN (SELECT id FROM search_names WHERE node_id = 'nod_x')`
	err := s.Write(ctx, func(tx *Tx) error {
		return tx.indexNode(ctx, "nod_x")
	})
	if err != nil {
		t.Fatal(err)
	}
	before := count(t, s, searchRows)
	if before == 0 {
		t.Fatal("the setup indexed no search rows, so the test would prove nothing")
	}
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.removeStaging(ctx, "acc_a", "fld_a")
	}); err != nil {
		t.Fatal(err)
	}
	if got := count(t, s, searchRows); got != before {
		t.Fatalf("removing a live folder's staging removed %d of its search rows", before-got)
	}
	if got := count(t, s, `SELECT COUNT(*) FROM nodes WHERE folder_id = 'fld_a'`); got != 2 {
		t.Fatalf("the live folder has %d nodes after the call, want 2", got)
	}
	if got := count(t, s, `SELECT COUNT(*) FROM folders WHERE id = 'fld_a'`); got != 1 {
		t.Fatalf("the live folder row is gone")
	}
}

// The staging removals of a cancel, a failure and an expiry (CleanupStagedBatch and DropStaging) remove only staged nodes:
// called with a live folder's ID, they leave its nodes alone (round 8, the re-check's (b)). Before, they removed the live
// file.
func TestStagingCleanupCannotRemoveALiveNode(t *testing.T) {
	s, ctx := round8Live(t)
	err := s.Write(ctx, func(tx *Tx) error {
		_, done, err := tx.CleanupStagedBatch(ctx, "acc_a", "fld_a", 10)
		if err != nil {
			return err
		}
		if !done {
			t.Fatal("a folder with no staged nodes is not done with its cleanup")
		}
		return tx.DropStaging(ctx, "acc_a", "fld_a", "nod_root")
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := count(t, s, `SELECT COUNT(*) FROM nodes WHERE id IN ('nod_root', 'nod_x')`); got != 2 {
		t.Fatalf("the staging removals left %d of the 2 live nodes", got)
	}
	if got := count(t, s, `SELECT COUNT(*) FROM folders WHERE id = 'fld_a'`); got != 1 {
		t.Fatalf("the live folder row is gone")
	}
}
