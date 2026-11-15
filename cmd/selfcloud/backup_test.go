// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/store"
)

// backupDataDir makes a data directory whose database holds one account, a user, a folder with a file and its search
// text, and a configuration file that names it.
func backupDataDir(t *testing.T) (dir, cfg, dbPath string) {
	t.Helper()
	dir = t.TempDir()
	dbPath = filepath.Join(dir, "selfcloud.db")
	st, err := store.Open(context.Background(), store.Options{Path: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
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
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	cfg = filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfg, []byte("data_dir: "+dir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, cfg, dbPath
}

// backupSnapshot runs one backup and returns the path it printed.
func backupSnapshot(t *testing.T, cfg, out string) string {
	t.Helper()
	code, stdout, stderr := runCmd(t, "backup", "-config", cfg, "-to", out)
	if code != 0 {
		t.Fatalf("backup --to = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, line := range strings.Split(stdout, "\n") {
		if p, ok := strings.CutPrefix(line, "snapshot: "); ok {
			return p
		}
	}
	t.Fatalf("no snapshot path in output:\n%s", stdout)
	return ""
}

func backupFileHash(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func backupRowCount(t *testing.T, path, table string) int {
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

func TestBackupSnapshotPassesCheckAndHoldsTheData(t *testing.T) {
	_, cfg, dbPath := backupDataDir(t)
	out := t.TempDir()
	snap := backupSnapshot(t, cfg, out)
	code, stdout, stderr := runCmd(t, "backup", "-config", cfg, "-check", snap)
	if code != 0 {
		t.Fatalf("backup --check = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, table := range []string{"accounts", "users", "nodes", "blobs", "search_texts", "search_text_fts"} {
		src, dst := backupRowCount(t, dbPath, table), backupRowCount(t, snap, table)
		if src != dst || src == 0 {
			t.Errorf("%s: source %d rows, snapshot %d rows", table, src, dst)
		}
	}
}

func TestBackupJSON(t *testing.T) {
	_, cfg, _ := backupDataDir(t)
	out := t.TempDir()
	code, stdout, stderr := runCmd(t, "backup", "-config", cfg, "-to", out, "-json")
	if code != 0 {
		t.Fatalf("backup --to -json = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	var doc struct {
		Path          string   `json:"path"`
		Bytes         int64    `json:"bytes"`
		SHA256        string   `json:"sha256"`
		SchemaVersion int      `json:"schema_version"`
		DurationMS    int64    `json:"duration_ms"`
		Integrity     string   `json:"integrity"`
		Problems      []string `json:"problems"`
	}
	if err := json.Unmarshal(bytes.TrimSpace([]byte(stdout)), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	fi, err := os.Stat(doc.Path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := store.LatestSchemaVersion()
	if err != nil {
		t.Fatal(err)
	}
	if doc.Bytes != fi.Size() || doc.SHA256 != backupFileHash(t, doc.Path) || doc.SchemaVersion != want ||
		doc.Integrity != "ok" || len(doc.Problems) != 0 || doc.DurationMS < 0 {
		t.Fatalf("JSON = %+v", doc)
	}
	code, stdout, stderr = runCmd(t, "backup", "-config", cfg, "-check", doc.Path, "-json")
	if code != 0 {
		t.Fatalf("backup --check -json = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	doc = struct {
		Path          string   `json:"path"`
		Bytes         int64    `json:"bytes"`
		SHA256        string   `json:"sha256"`
		SchemaVersion int      `json:"schema_version"`
		DurationMS    int64    `json:"duration_ms"`
		Integrity     string   `json:"integrity"`
		Problems      []string `json:"problems"`
	}{}
	if err := json.Unmarshal(bytes.TrimSpace([]byte(stdout)), &doc); err != nil {
		t.Fatalf("check stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if doc.Integrity != "ok" || len(doc.Problems) != 0 || doc.SchemaVersion != want {
		t.Fatalf("check JSON = %+v", doc)
	}
}

func TestBackupNameModeAndTmpGone(t *testing.T) {
	_, cfg, _ := backupDataDir(t)
	old := backupClock
	backupClock = func() time.Time { return time.Date(2026, 11, 14, 9, 5, 3, 0, time.UTC) }
	defer func() { backupClock = old }()
	out := t.TempDir()
	code, stdout, stderr := runCmd(t, "backup", "-config", cfg, "-to", out)
	if code != 0 {
		t.Fatalf("backup --to = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	want := filepath.Join(out, "selfcloud-20261114T090503Z.db")
	fi, err := os.Stat(want)
	if err != nil {
		t.Fatalf("snapshot not at the stamped name: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("snapshot mode = %o, want 600", fi.Mode().Perm())
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(want) {
		t.Errorf("directory holds %v, want only the snapshot (no temporary file)", entries)
	}
	if !strings.Contains(stdout, want) || !strings.Contains(stdout, "sha256:") {
		t.Errorf("text output lacks the path or the hash:\n%s", stdout)
	}
}

func TestBackupExistingTargetExitsTwo(t *testing.T) {
	_, cfg, _ := backupDataDir(t)
	old := backupClock
	backupClock = func() time.Time { return time.Date(2026, 11, 14, 9, 5, 3, 0, time.UTC) }
	defer func() { backupClock = old }()
	out := t.TempDir()
	target := filepath.Join(out, "selfcloud-20261114T090503Z.db")
	if err := os.WriteFile(target, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runCmd(t, "backup", "-config", cfg, "-to", out)
	if code != 2 {
		t.Fatalf("backup onto an existing name = %d, want 2 (stderr: %s)", code, stderr)
	}
	b, err := os.ReadFile(target)
	if err != nil || string(b) != "keep me" {
		t.Fatalf("existing file changed: %q, %v", b, err)
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("directory holds %v, want only the untouched file", entries)
	}
}

func TestBackupDirMissingOrAFileExitsTwo(t *testing.T) {
	dir, cfg, _ := backupDataDir(t)
	if code, _, _ := runCmd(t, "backup", "-config", cfg, "-to", filepath.Join(dir, "missing")); code != 2 {
		t.Errorf("backup --to a missing directory = %d, want 2", code)
	}
	file := filepath.Join(dir, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := runCmd(t, "backup", "-config", cfg, "-to", file); code != 2 {
		t.Errorf("backup --to a file = %d, want 2", code)
	}
}

func TestBackupToInsideBlobsExitsTwo(t *testing.T) {
	dir, cfg, _ := backupDataDir(t)
	blobs := filepath.Join(dir, "blobs")
	deep := filepath.Join(blobs, "acc_b", "ab")
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{blobs, deep} {
		if code, _, _ := runCmd(t, "backup", "-config", cfg, "-to", target); code != 2 {
			t.Errorf("backup --to %s = %d, want 2", target, code)
		}
	}
}

func TestBackupCheckFlippedByteExitsOne(t *testing.T) {
	_, cfg, _ := backupDataDir(t)
	snap := backupSnapshot(t, cfg, t.TempDir())
	b, err := os.ReadFile(snap)
	if err != nil {
		t.Fatal(err)
	}
	pageSize := int(b[16])<<8 | int(b[17])
	b[pageSize] ^= 0xFF // the page type of page 2
	if err := os.WriteFile(snap, b, 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCmd(t, "backup", "-config", cfg, "-check", snap)
	if code != 1 {
		t.Fatalf("check of a flipped page = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "problem:") {
		t.Errorf("the problem is not listed:\n%s", stdout)
	}
}

func TestBackupCheckTruncatedExitsOne(t *testing.T) {
	_, cfg, _ := backupDataDir(t)
	snap := backupSnapshot(t, cfg, t.TempDir())
	b, err := os.ReadFile(snap)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(snap, b[:len(b)/2], 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCmd(t, "backup", "-config", cfg, "-check", snap)
	if code != 1 {
		t.Fatalf("check of a truncated file = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "problem:") {
		t.Errorf("the problem is not listed:\n%s", stdout)
	}
}

func TestBackupCheckTextFileExitsTwo(t *testing.T) {
	_, cfg, _ := backupDataDir(t)
	file := filepath.Join(t.TempDir(), "text.db")
	if err := os.WriteFile(file, []byte("hello, not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runCmd(t, "backup", "-config", cfg, "-check", file)
	if code != 2 {
		t.Fatalf("check of a text file = %d, want 2 (stderr: %s)", code, stderr)
	}
}

func TestBackupCheckHigherSchemaExitsOne(t *testing.T) {
	_, cfg, _ := backupDataDir(t)
	snap := backupSnapshot(t, cfg, t.TempDir())
	want, err := store.LatestSchemaVersion()
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+snap)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, '9999_future.sql', 0)`, want+1); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCmd(t, "backup", "-config", cfg, "-check", snap)
	if code != 1 {
		t.Fatalf("check of a newer schema = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, fmt.Sprint(want+1)) || !strings.Contains(stdout, fmt.Sprint(want)) {
		t.Errorf("the problem does not name both versions (%d and %d):\n%s", want+1, want, stdout)
	}
}

func TestBackupCheckLeavesNoWalOrShm(t *testing.T) {
	_, cfg, _ := backupDataDir(t)
	out := t.TempDir()
	snap := backupSnapshot(t, cfg, out)
	if code, _, _ := runCmd(t, "backup", "-config", cfg, "-check", snap); code != 0 {
		t.Fatal("check of a good snapshot did not exit 0")
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(snap) {
		t.Fatalf("check left files beside the snapshot: %v", entries)
	}
	// A WAL-mode database checked beside its -wal: the immutable open reads it without creating a -shm.
	walDir := t.TempDir()
	walPath := filepath.Join(walDir, "wal.db")
	db, err := sql.Open("sqlite", "file:"+walPath+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE x (a INTEGER); INSERT INTO x VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	for _, ext := range []string{"", "-wal"} {
		b, err := os.ReadFile(walPath + ext)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(walDir, "copy.db"+ext), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ := runCmd(t, "backup", "-config", cfg, "-check", filepath.Join(walDir, "copy.db"))
	if code != 1 { // a database, but not a Selfcloud snapshot: no schema_migrations
		t.Fatalf("check of a WAL copy without schema_migrations = %d, want 1\n%s", code, stdout)
	}
	entries, err = os.ReadDir(walDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "-shm") {
			t.Errorf("check created %s beside the file", e.Name())
		}
	}
}

func TestBackupLeavesTheSourceUnchanged(t *testing.T) {
	_, cfg, dbPath := backupDataDir(t)
	before := backupFileHash(t, dbPath)
	out := t.TempDir()
	if code, _, stderr := runCmd(t, "backup", "-config", cfg, "-to", out); code != 0 {
		t.Fatalf("backup --to = %d (stderr: %s)", code, stderr)
	}
	if after := backupFileHash(t, dbPath); after != before {
		t.Fatal("backup changed the source database's bytes")
	}
}

func TestBackupUsage(t *testing.T) {
	_, cfg, _ := backupDataDir(t)
	if code, _, _ := runCmd(t, "backup", "-config", cfg, "-to", "x", "-check", "y"); code != 2 {
		t.Errorf("--to with --check = %d, want 2", code)
	}
	if code, _, _ := runCmd(t, "backup", "-config", cfg); code != 2 {
		t.Errorf("backup with neither --to nor --check = %d, want 2", code)
	}
	if code, out, _ := runCmd(t, "help"); code != 0 || !strings.Contains(out, "backup") {
		t.Errorf("help does not list backup: %d\n%s", code, out)
	}
}

// backupNoLinks stands in for a filesystem without hard links: every link is refused with EPERM.
func backupNoLinks(oldname, newname string) error {
	return &os.LinkError{Op: "link", Old: oldname, New: newname, Err: syscall.EPERM}
}

func TestBackupPublishesByRenameWithoutHardLinks(t *testing.T) {
	_, cfg, _ := backupDataDir(t)
	old := backupLink
	backupLink = backupNoLinks
	defer func() { backupLink = old }()
	out := t.TempDir()
	code, stdout, stderr := runCmd(t, "backup", "-config", cfg, "-to", out)
	if code != 0 {
		t.Fatalf("backup without hard links = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "selfcloud-") || !strings.HasSuffix(entries[0].Name(), ".db") {
		t.Fatalf("directory holds %v, want only the stamped snapshot (no temporary file)", entries)
	}
	snap := filepath.Join(out, entries[0].Name())
	fi, err := os.Stat(snap)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("snapshot mode = %o, want 600", fi.Mode().Perm())
	}
	if code, _, _ := runCmd(t, "backup", "-config", cfg, "-check", snap); code != 0 {
		t.Errorf("check of the renamed snapshot = %d, want 0", code)
	}
}

func TestBackupNoHardLinksKeepsExistingFile(t *testing.T) {
	_, cfg, _ := backupDataDir(t)
	oldClock := backupClock
	backupClock = func() time.Time { return time.Date(2026, 11, 14, 9, 5, 3, 0, time.UTC) }
	defer func() { backupClock = oldClock }()
	oldLink := backupLink
	backupLink = backupNoLinks
	defer func() { backupLink = oldLink }()
	out := t.TempDir()
	target := filepath.Join(out, "selfcloud-20261114T090503Z.db")
	if err := os.WriteFile(target, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runCmd(t, "backup", "-config", cfg, "-to", out)
	if code != 2 {
		t.Fatalf("backup onto an existing name without hard links = %d, want 2 (stderr: %s)", code, stderr)
	}
	b, err := os.ReadFile(target)
	if err != nil || string(b) != "keep me" {
		t.Fatalf("existing file changed: %q, %v", b, err)
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("directory holds %v, want only the untouched file", entries)
	}
}

// TestBackupNoHardLinksFileAppearsDuringPublish exercises the fallback's Lstat guard: the link is refused and the
// final name appears meanwhile, so the rename must not replace it.
func TestBackupNoHardLinksFileAppearsDuringPublish(t *testing.T) {
	_, cfg, _ := backupDataDir(t)
	old := backupLink
	backupLink = func(oldname, newname string) error {
		if err := os.WriteFile(newname, []byte("appeared"), 0o600); err != nil {
			return err
		}
		return &os.LinkError{Op: "link", Old: oldname, New: newname, Err: syscall.EPERM}
	}
	defer func() { backupLink = old }()
	out := t.TempDir()
	code, _, stderr := runCmd(t, "backup", "-config", cfg, "-to", out)
	if code != 2 {
		t.Fatalf("backup with a name appearing during publish = %d, want 2 (stderr: %s)", code, stderr)
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("directory holds %v, want only the file that appeared", entries)
	}
	b, err := os.ReadFile(filepath.Join(out, entries[0].Name()))
	if err != nil || string(b) != "appeared" {
		t.Fatalf("the file that appeared changed: %q, %v", b, err)
	}
}
