// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

// The publish's placement of a staged top changes only a staged node: a node of a live folder, named with the import's
// staging folder's ID, is not moved or renamed by it (the node-write guard's rule for SetStagedParent).
func TestStagedPlacementTouchesOnlyStagedNodes(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "placement.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	setup := []string{
		`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_a', 'A', 'active', 0, 0)`,
		`INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at) VALUES ('fld_a', 'acc_a', 'Docs', 'docs', 0, 0)`,
		`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
			VALUES ('nod_root', 'acc_a', 'fld_a', NULL, '', '', 'dir', 0, 0)`,
		`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, size, etag, created_at, modified_at)
			VALUES ('nod_x', 'acc_a', 'fld_a', 'nod_root', 'x.txt', 'x.txt', 'file', 3, 'e', 0, 0)`,
	}
	for _, q := range setup {
		if _, err := s.w.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	err = s.Write(ctx, func(tx *Tx) error {
		if err := tx.CreateStagingFolder(ctx, "acc_a", "fld_stage", "nod_stage_root", Now()); err != nil {
			return err
		}
		// A live node of another folder, named as if it were staged: the placement must not reach it, and reports not found.
		err := tx.SetStagedParent(ctx, "acc_a", "fld_stage", "nod_x", "nod_root", "renamed.txt", "renamed.txt", Now())
		if !errors.Is(err, ErrNotFound) {
			return fmt.Errorf("the placement of a live node answered %v, want not found", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var name string
	if err := s.r.QueryRowContext(ctx, `SELECT name FROM nodes WHERE id = 'nod_x'`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "x.txt" {
		t.Fatalf("a live node was renamed to %q by the placement of a staged top", name)
	}
}
