// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"modernc.org/sqlite"
)

// The bare driver carries no collation: it stands in for a system sqlite3 that does not know selfcloud_name.
func init() { sql.Register("sqlite_bare_backup_test", &sqlite.Driver{}) }

// seedBackupRows fills a store with one account, a user, a folder with a root and a file, and the file's search text.
func seedBackupRows(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_b', 'Backup', 'active', 0, 0)`,
		`INSERT INTO users (id, account_id, email, email_key, role, status, created_at, updated_at)
			VALUES ('usr_b', 'acc_b', 'owner@example.test', 'owner@example.test', 'owner', 'active', 0, 0)`,
		`INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at) VALUES ('fld_b', 'acc_b', 'Docs', 'docs', 0, 0)`,
		`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
			VALUES ('nod_broot', 'acc_b', 'fld_b', NULL, '', '', 'dir', 0, 0)`,
		`INSERT INTO blobs (id, account_id, size, sha256, state, created_at)
			VALUES ('blb_b', 'acc_b', 11, '` + strings.Repeat("ab", 32) + `', 'live', 0)`,
		`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, size, blob_id, created_at, modified_at)
			VALUES ('nod_bfile', 'acc_b', 'fld_b', 'nod_broot', 'notes.txt', 'notes.txt', 'file', 11, 'blb_b', 0, 0)`,
		`INSERT INTO search_texts (id, blob_id, account_id, state, source, bytes) VALUES (1, 'blb_b', 'acc_b', 'ready', 'plain', 11)`,
		`INSERT INTO search_text_fts (rowid, text, account_id) VALUES (1, 'hello backup', 'acc_b')`,
	} {
		if _, err := s.w.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
}

func openBackupSource(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "selfcloud.db")
	s, err := Open(context.Background(), Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

// TestVacuumIntoRefusedUnderQueryOnly proves why SnapshotTo opens the source mode=ro: VACUUM INTO on OpenCheck's
// query_only connection is refused with SQLITE_READONLY, so the snapshot cannot run on that connection.
func TestVacuumIntoRefusedUnderQueryOnly(t *testing.T) {
	s, path := openBackupSource(t)
	seedBackupRows(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cs, err := OpenCheck(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	_, err = cs.r.ExecContext(ctx, "VACUUM INTO ?", filepath.Join(t.TempDir(), "refused.db"))
	if err == nil {
		t.Fatal("VACUUM INTO under query_only succeeded; SnapshotTo would not need mode=ro")
	}
	var se *sqlite.Error
	if !errors.As(err, &se) || se.Code()&0xff != 8 {
		t.Fatalf("VACUUM INTO under query_only = %v, want SQLITE_READONLY (8)", err)
	}
	// The mode=ro fallback snapshots the same file, and the copy passes the check.
	snap := filepath.Join(t.TempDir(), "snap.db")
	if err := SnapshotTo(ctx, path, snap, 0); err != nil {
		t.Fatalf("SnapshotTo: %v", err)
	}
	rep, err := CheckSnapshot(ctx, snap)
	if err != nil {
		t.Fatalf("CheckSnapshot: %v", err)
	}
	if len(rep.Problems) != 0 {
		t.Fatalf("snapshot problems: %v", rep.Problems)
	}
}

func TestSnapshotToHoldsTheData(t *testing.T) {
	s, path := openBackupSource(t)
	seedBackupRows(t, s)
	ctx := context.Background()
	snap := filepath.Join(t.TempDir(), "snap.db")
	if err := SnapshotTo(ctx, path, snap, 0); err != nil {
		t.Fatalf("SnapshotTo: %v", err)
	}
	rep, err := CheckSnapshot(ctx, snap)
	if err != nil {
		t.Fatalf("CheckSnapshot: %v", err)
	}
	if len(rep.Problems) != 0 {
		t.Fatalf("snapshot problems: %v", rep.Problems)
	}
	want, err := LatestSchemaVersion()
	if err != nil {
		t.Fatal(err)
	}
	if rep.SchemaVersion != want {
		t.Fatalf("snapshot schema version = %d, want %d", rep.SchemaVersion, want)
	}
	for _, table := range []string{"accounts", "users", "nodes", "blobs", "search_texts", "search_text_fts"} {
		src, dst := countTable(t, path, table), countTable(t, snap, table)
		if src != dst || src == 0 {
			t.Errorf("%s: source %d rows, snapshot %d rows", table, src, dst)
		}
	}
}

// TestSnapshotCheckNeedsTheCollation proves the check registers selfcloud_name: the same copy fails integrity_check
// on a connection without the collation (a system sqlite3) and passes on the store's own.
func TestSnapshotCheckNeedsTheCollation(t *testing.T) {
	s, path := openBackupSource(t)
	seedBackupRows(t, s)
	ctx := context.Background()
	snap := filepath.Join(t.TempDir(), "snap.db")
	if err := SnapshotTo(ctx, path, snap, 0); err != nil {
		t.Fatal(err)
	}
	bare, err := sql.Open("sqlite_bare_backup_test", "file:"+snap+"?mode=ro&immutable=1")
	if err != nil {
		t.Fatal(err)
	}
	defer bare.Close()
	var line string
	err = bare.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&line)
	if err == nil || !strings.Contains(err.Error(), "no such collation sequence") {
		t.Fatalf("integrity_check without the collation = %q, %v; want no such collation sequence", line, err)
	}
	rep, err := CheckSnapshot(ctx, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Problems) != 0 {
		t.Fatalf("CheckSnapshot with the collation registered: %v", rep.Problems)
	}
}

// TestSnapshotToWhileWriting keeps a writer committing rows while the snapshot runs. The copy must pass the integrity
// check and hold the writer's rows as of one moment: a prefix of the sequence, with no gap.
func TestSnapshotToWhileWriting(t *testing.T) {
	s, path := openBackupSource(t)
	ctx := context.Background()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			_, err := s.w.ExecContext(ctx,
				`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES (?, 'Writer', 'active', 0, 0)`,
				fmt.Sprintf("acc_w%06d", i))
			if err != nil {
				t.Errorf("writer: %v", err)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	time.Sleep(50 * time.Millisecond)
	snap := filepath.Join(t.TempDir(), "snap.db")
	err := SnapshotTo(ctx, path, snap, 0)
	close(stop)
	<-done
	if err != nil {
		t.Fatalf("SnapshotTo while writing: %v", err)
	}
	rep, err := CheckSnapshot(ctx, snap)
	if err != nil {
		t.Fatalf("CheckSnapshot: %v", err)
	}
	if len(rep.Problems) != 0 {
		t.Fatalf("snapshot taken while writing has problems: %v", rep.Problems)
	}
	db, err := sql.Open("sqlite", "file:"+snap+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	var maxID string
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MAX(id), '') FROM accounts`).Scan(&n, &maxID); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("the snapshot holds none of the writer's rows")
	}
	if want := fmt.Sprintf("acc_w%06d", n-1); maxID != want {
		t.Fatalf("snapshot holds %d rows but the last is %s, want %s: not one moment", n, maxID, want)
	}
}

func countTable(t *testing.T, path, table string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
