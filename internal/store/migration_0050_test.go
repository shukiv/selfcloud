// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"path/filepath"
	"testing"
)

// TestMigration0050BackfillsEveryRowLikeGroupOf runs the real runner up to migration 49 on an empty database, seeds nodes
// of every shape the group rule distinguishes (a name and a media type that disagree, dotted names, a trailing dot, upper
// case, parameters and spaces in the type, folders and shortcuts), then applies 50 alone. Every row's type_group must equal
// groupOf on that row, so the SQL backfill cannot drift from the Go rule.
func TestMigration0050BackfillsEveryRowLikeGroupOf(t *testing.T) {
	ctx := context.Background()
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migs) < 50 || migs[49].version != 50 {
		t.Fatalf("migration 50 is not in the embedded list (%d migrations)", len(migs))
	}

	path := filepath.Join(t.TempDir(), "at49.db")
	w, err := sql.Open("sqlite", dsn(path, []string{"busy_timeout(10000)", "foreign_keys(1)", "synchronous(NORMAL)", "journal_mode(WAL)"}, "immediate"))
	if err != nil {
		t.Fatal(err)
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	t.Cleanup(func() { w.Close() })
	s := &Store{w: w, log: slog.New(slog.DiscardHandler)}

	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := w.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}

	exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT    NOT NULL,
		applied_at INTEGER NOT NULL
	)`)
	if err := s.applyMigrations(ctx, migs[:49]); err != nil {
		t.Fatal(err)
	}

	exec(`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_m', 'M', 'active', 0, 0)`)
	exec(`INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at) VALUES ('fld_m', 'acc_m', 'Docs', 'docs', 0, 0)`)
	exec(`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
		VALUES ('nod_root', 'acc_m', 'fld_m', NULL, '', '', 'dir', 0, 0)`)

	// The fixture: name, kind and media type, each a case the rule must answer the same way as groupOf.
	fixture := []struct{ kind, name, mime string }{
		{"file", "report.DOCX", "application/zip"},
		{"file", "a.b.pdf", "text/plain"},
		{"file", "trailing.", "application/pdf"},
		{"file", "README", "application/pdf"},
		{"file", "clip.webm", "application/octet-stream"},
		{"file", "song.ogg", "video/ogg"},
		{"file", "untitled", "audio/mpeg; codecs=x"},
		{"file", "untitled", "  IMAGE/PNG  "},
		{"file", "untitled", "application/pdf;name=x"},
		{"file", "untitled", "video/x-thing"},
		{"file", "untitled", "application/x-zip-compressed"},
		{"file", "x.bin", "application/octet-stream"},
		{"file", "weird..docx", ""},
		{"file", "photo.heic", "text/plain"},
		{"file", "sheet.xlsx", "image/jpeg"},
		{"file", "untitled", "application/x-unknown"},
		{"dir", "docs.pdf", "application/pdf"},
		{"shortcut", "link.pdf", ""},
		// Unicode case and whitespace (review L1 of ff1a). SQLite's lower() and trim() fold ASCII and trim spaces only, so
		// the Kelvin sign (U+212A) in an extension is not "k" and is in no group, and a tab before a media type is not
		// trimmed. groupOf must answer the same as the backfill.
		{"file", "clip.mKv", ""},
		{"file", "untitled", "\timage/png"},
	}
	for i, f := range fixture {
		exec(`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, size, mime_type, created_at, modified_at)
			VALUES (?, 'acc_m', 'fld_m', 'nod_root', ?, ?, ?, 0, ?, 0, 0)`,
			fmt.Sprintf("nod_%02d", i), f.name, fmt.Sprintf("k%02d", i), f.kind, f.mime)
	}

	if err := s.applyMigrations(ctx, migs[49:50]); err != nil {
		t.Fatal(err)
	}

	rows, err := w.QueryContext(ctx, `SELECT kind, name, mime_type, type_group FROM nodes WHERE id LIKE 'nod_%' AND id <> 'nod_root' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var kind, name, mime, got string
		if err := rows.Scan(&kind, &name, &mime, &got); err != nil {
			t.Fatal(err)
		}
		if want := groupOf(kind, name, mime); got != want {
			t.Errorf("row %s %q %q: type_group %q, groupOf %q", kind, name, mime, got, want)
		}
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if n != len(fixture) {
		t.Fatalf("backfill checked %d rows, want %d", n, len(fixture))
	}
}
