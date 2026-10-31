// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// migratedDB holds a database with every migration applied, made once per test binary, so that
// openTest's many fresh databases do not each run them. Tests of the migrations call Open themselves.
var (
	migratedOnce sync.Once
	migratedDB   []byte
	migratedErr  error
)

func seedMigratedDB(t testing.TB, path string) {
	t.Helper()
	migratedOnce.Do(func() {
		dir, err := os.MkdirTemp("", "selfcloud-migrated")
		if err != nil {
			migratedErr = err
			return
		}
		defer os.RemoveAll(dir)
		p := filepath.Join(dir, "selfcloud.db")
		s, err := Open(context.Background(), Options{Path: p})
		if err != nil {
			migratedErr = err
			return
		}
		if err := s.Close(); err != nil {
			migratedErr = err
			return
		}
		migratedDB, migratedErr = os.ReadFile(p)
	})
	if migratedErr != nil {
		t.Fatal(migratedErr)
	}
	if err := os.WriteFile(path, migratedDB, 0o600); err != nil {
		t.Fatal(err)
	}
}

// openTest opens a fresh database in a temporary directory.
func openTest(t testing.TB) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	seedMigratedDB(t, path)
	s, err := Open(context.Background(), Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenMigratesAndReopens(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sub", "selfcloud.db")
	s, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	ms, _ := loadMigrations()
	v, err := s.SchemaVersion(ctx)
	if err != nil || v != len(ms) {
		t.Fatalf("schema version = %d, %v; want %d", v, err, len(ms))
	}
	var mode string
	if err := s.r.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Errorf("journal_mode = %q, %v; want wal", mode, err)
	}
	if fi, err := os.Stat(path); err != nil {
		t.Error(err)
	} else if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("database file mode = %v, want 0600", perm)
	}
	s.Close()

	s, err = Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	s.Close()
}

func TestRefusesNewerSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "selfcloud.db")
	s, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx,
		"INSERT INTO schema_migrations (version, name, applied_at) VALUES (999, 'future', 0)"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	_, err = Open(ctx, Options{Path: path})
	if err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("Open on a newer schema = %v, want a refusal", err)
	}
}

func TestReadPoolIsReadOnly(t *testing.T) {
	s := openTest(t)
	_, err := s.r.ExecContext(context.Background(), "INSERT INTO settings (key, value) VALUES ('a', 'b')")
	if err == nil {
		t.Fatal("the read pool accepted a write")
	}
}
