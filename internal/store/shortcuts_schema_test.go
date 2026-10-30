// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// schemaFixture is an open store with an account, a user, a folder and its root, for raw SQL tests of the
// nodes table.
func schemaFixture(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "schema.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	for _, q := range []string{
		`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_a', 'A', 'active', 0, 0)`,
		`INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at) VALUES ('fld_a', 'acc_a', 'Docs', 'docs', 0, 0)`,
		`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
			VALUES ('nod_root', 'acc_a', 'fld_a', NULL, '', '', 'dir', 0, 0)`,
	} {
		if _, err := s.w.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// insertNodeSQL adds a node with the given extra column values; the caller supplies the values in order.
func insertNodeSQL(t *testing.T, s *Store, id, kind string, size int, target, color, desc any) error {
	t.Helper()
	_, err := s.w.ExecContext(context.Background(), `INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key,
		kind, size, created_at, modified_at, target_id, color, description)
		VALUES (?, 'acc_a', 'fld_a', 'nod_root', ?, ?, ?, ?, 0, 0, ?, ?, ?)`,
		id, id, id, kind, size, target, color, desc)
	return err
}

// TestNodeRowsCheckTheirKindFields pins the rules the 0019 rebuild adds: a shortcut is a kind of its own with no
// size, only a shortcut has a target, colours come from the palette and only on a directory, and a description is
// at most 1000 characters.
func TestNodeRowsCheckTheirKindFields(t *testing.T) {
	s := schemaFixture(t)
	if err := insertNodeSQL(t, s, "nod_file", "file", 3, nil, nil, nil); err != nil {
		t.Fatalf("a file: %v", err)
	}
	if err := insertNodeSQL(t, s, "nod_sc", "shortcut", 0, "nod_file", nil, nil); err != nil {
		t.Fatalf("a shortcut to a file: %v", err)
	}
	if err := insertNodeSQL(t, s, "nod_sc_size", "shortcut", 1, "nod_file", nil, nil); err == nil {
		t.Error("a shortcut with a size was accepted")
	}
	if err := insertNodeSQL(t, s, "nod_sc_nil", "shortcut", 0, nil, nil, nil); err != nil {
		t.Errorf("a shortcut whose target was purged: %v", err)
	}
	if err := insertNodeSQL(t, s, "nod_tgt_on_file", "file", 0, "nod_file", nil, nil); err == nil {
		t.Error("a file with a target was accepted")
	}
	if err := insertNodeSQL(t, s, "nod_tgt_missing", "shortcut", 0, "nod_nope", nil, nil); err == nil {
		t.Error("a shortcut to a missing node was accepted")
	}
	if err := insertNodeSQL(t, s, "nod_kind", "link", 0, nil, nil, nil); err == nil {
		t.Error("an unknown kind was accepted")
	}
	if err := insertNodeSQL(t, s, "nod_red", "dir", 0, nil, "red", nil); err != nil {
		t.Errorf("a directory with a palette colour: %v", err)
	}
	if err := insertNodeSQL(t, s, "nod_bad", "dir", 0, nil, "magenta", nil); err == nil {
		t.Error("a colour outside the palette was accepted")
	}
	if err := insertNodeSQL(t, s, "nod_fcol", "file", 0, nil, "red", nil); err == nil {
		t.Error("a colour on a file was accepted")
	}
	if err := insertNodeSQL(t, s, "nod_scol", "shortcut", 0, "nod_file", "red", nil); err == nil {
		t.Error("a colour on a shortcut was accepted")
	}
	if err := insertNodeSQL(t, s, "nod_d1000", "dir", 0, nil, nil, strings.Repeat("é", 1000)); err != nil {
		t.Errorf("a 1000-character description: %v", err)
	}
	if err := insertNodeSQL(t, s, "nod_d1001", "dir", 0, nil, nil, strings.Repeat("é", 1001)); err == nil {
		t.Error("a 1001-character description was accepted")
	}
}

// TestPurgingATargetClearsTheShortcut: a purged target leaves its shortcut in place with no target, so the
// shortcut shows as unavailable and never names a node that is gone.
func TestPurgingATargetClearsTheShortcut(t *testing.T) {
	s := schemaFixture(t)
	if err := insertNodeSQL(t, s, "nod_file", "file", 3, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := insertNodeSQL(t, s, "nod_sc", "shortcut", 0, "nod_file", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(context.Background(), "DELETE FROM nodes WHERE id = 'nod_file'"); err != nil {
		t.Fatal(err)
	}
	var target *string
	if err := s.w.QueryRowContext(context.Background(), "SELECT target_id FROM nodes WHERE id = 'nod_sc'").Scan(&target); err != nil {
		t.Fatal(err)
	}
	if target != nil {
		t.Errorf("target_id after the target was purged = %q, want NULL", *target)
	}
}

// TestRequestRowForgetsItsShortcutWhenItIsDeleted: an access request that came through a shortcut names it while the
// shortcut exists. When the shortcut is deleted the ID is cleared, and the flag that the request came through one stays,
// so a decision mail can say "deleted" rather than fall back to the target.
func TestRequestRowForgetsItsShortcutWhenItIsDeleted(t *testing.T) {
	s := schemaFixture(t)
	ctx := context.Background()
	if err := insertNodeSQL(t, s, "nod_file", "file", 3, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := insertNodeSQL(t, s, "nod_sc", "shortcut", 0, "nod_file", nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO users (id, account_id, email, email_key, role, status, created_at, updated_at)
			VALUES ('usr_a', 'acc_a', 'a@x', 'a@x', 'member', 'active', 0, 0)`,
		`INSERT INTO access_requests (id, account_id, node_id, user_id, role, status, created_at, via_node_id, via_shortcut)
			VALUES ('arq_1', 'acc_a', 'nod_file', 'usr_a', 'viewer', 'pending', 0, 'nod_sc', 1)`,
		`INSERT INTO access_requests (id, account_id, node_id, user_id, role, status, created_at, via_shortcut)
			VALUES ('arq_2', 'acc_a', 'nod_file', 'usr_a', 'commenter', 'denied', 0, 0)`,
	} {
		if _, err := s.w.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.w.ExecContext(ctx, "DELETE FROM nodes WHERE id = 'nod_sc'"); err != nil {
		t.Fatal(err)
	}
	var via sql.NullString
	var flag int
	if err := s.w.QueryRowContext(ctx, "SELECT via_node_id, via_shortcut FROM access_requests WHERE id = 'arq_1'").Scan(&via, &flag); err != nil {
		t.Fatal(err)
	}
	if via.Valid || flag != 1 {
		t.Errorf("after the shortcut was deleted: via_node_id %v, via_shortcut %d; want NULL and 1", via, flag)
	}
}
