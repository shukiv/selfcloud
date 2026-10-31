// SPDX-License-Identifier: AGPL-3.0-or-later

package search

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/shukiv/selfcloud/internal/store"
)

// TestStagedRowsNeverMatch plants a search row for a node of a staged folder, which the store never writes, and checks
// that the candidate read does not return it. The candidate SQL's own staged filter (f.staged = 0) is what keeps the row
// out: reach does not exclude staged folders. Removing that filter makes this test fail.
func TestStagedRowsNeverMatch(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "staged.db")
	s, err := store.Open(ctx, store.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_a', 'A', 'active', 0, 0)`,
		`INSERT INTO folders (id, account_id, name, name_key, staged, created_at, updated_at)
			VALUES ('fld_s', 'acc_a', 'Staging', 'staging', 1, 0, 0)`,
		`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
			VALUES ('nod_root', 'acc_a', 'fld_s', NULL, '', '', 'dir', 0, 0)`,
		`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
			VALUES ('nod_x', 'acc_a', 'fld_s', 'nod_root', 'stray.txt', 'stray.txt', 'file', 0, 0)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	res, err := db.ExecContext(ctx, `INSERT INTO search_names (node_id, account_id, kind, ext) VALUES ('nod_x', 'acc_a', 'file', 'txt')`)
	if err != nil {
		t.Fatal(err)
	}
	rowid, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO search_words (rowid, text, account_id) VALUES (?, 'stray.txt', 'acc_a')`,
		`INSERT INTO search_trigram (rowid, text, account_id) VALUES (?, 'stray.txt', 'acc_a')`,
	} {
		if _, err := db.ExecContext(ctx, q, rowid); err != nil {
			t.Fatal(err)
		}
	}
	req := &Request{AccountID: "acc_a", Terms: []string{"stray"}, ModifiedFrom: -1, ModifiedTo: -1, SizeMin: -1, SizeMax: -1}
	cands, err := req.Candidates(ctx, db, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 0 {
		t.Fatalf("candidates = %v; a row of a staged folder must not be read", cands)
	}
	// The same row is read once its folder is published, so the test is not vacuous.
	if _, err := db.ExecContext(ctx, `UPDATE folders SET staged = 0 WHERE id = 'fld_s'`); err != nil {
		t.Fatal(err)
	}
	cands, err = req.Candidates(ctx, db, 0, 100)
	if err != nil || len(cands) != 1 {
		t.Fatalf("published candidates = %v, %v; want the one row", cands, err)
	}
}
