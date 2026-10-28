// SPDX-License-Identifier: AGPL-3.0-or-later

// Package store keeps Selfcloud's metadata: accounts, users, folders, the file tree, uploads,
// tokens and the audit log. File contents live on disk, managed elsewhere.
//
// SQL is written with ? placeholders and standard syntax so that a PostgreSQL back end can
// reuse it after a mechanical rebind.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/shukiv/selfcloud/internal/fsutil"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Store is the metadata database.
type Store struct {
	// w has a single connection, so writers queue in Go rather than fail with SQLITE_BUSY.
	// Its transactions start with BEGIN IMMEDIATE, taking the write lock up front instead of
	// failing when a read turns into a write.
	w *sql.DB
	// r is a pool of read-only connections; WAL mode lets them read while w writes.
	r   *sql.DB
	log *slog.Logger
}

// Options configure Open.
type Options struct {
	Path           string // SQLite file
	AllowNetworkFS bool
	Logger         *slog.Logger
}

// Open opens (creating if needed) the database at opts.Path and applies pending migrations.
// It refuses a database directory on a network or FUSE file system unless AllowNetworkFS is
// set, because SQLite needs POSIX locks and fsync that those do not reliably provide.
func Open(ctx context.Context, opts Options) (*Store, error) {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	dir := filepath.Dir(opts.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	info, err := fsutil.Type(dir)
	if err != nil {
		return nil, fmt.Errorf("store: checking file system of %s: %w", dir, err)
	}
	if info.Network {
		if !opts.AllowNetworkFS {
			return nil, fmt.Errorf("store: %s is on a %s file system; the database needs a local disk "+
				"(set store.allow_network_fs to override at your own risk)", dir, info.Name)
		}
		log.Warn("database is on a network file system", "dir", dir, "fs", info.Name)
	}
	// Create the file owner-only before SQLite does; SQLite gives its -wal and -shm files the
	// database file's permissions.
	f, err := os.OpenFile(opts.Path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	f.Close()

	// synchronous=NORMAL in WAL mode never corrupts the database; after a power cut the last
	// transactions may roll back. Callers that pair a database row with file data write and
	// sync the data first, so a rolled-back row only ever points at data that is really there.
	common := []string{"busy_timeout(10000)", "foreign_keys(1)", "synchronous(NORMAL)"}
	w, err := sql.Open("sqlite", dsn(opts.Path, append(common, "journal_mode(WAL)"), "immediate"))
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxLifetime(0)

	s := &Store{w: w, log: log}
	if err := s.migrate(ctx); err != nil {
		w.Close()
		return nil, err
	}

	r, err := sql.Open("sqlite", dsn(opts.Path, append(common, "query_only(1)"), ""))
	if err != nil {
		w.Close()
		return nil, fmt.Errorf("store: %w", err)
	}
	n := max(4, runtime.GOMAXPROCS(0))
	r.SetMaxOpenConns(n)
	r.SetMaxIdleConns(n)
	s.r = r
	if err := s.Ping(ctx); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func dsn(path string, pragmas []string, txlock string) string {
	q := url.Values{}
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}
	if txlock != "" {
		q.Set("_txlock", txlock)
	}
	return "file:" + path + "?" + q.Encode()
}

// Close closes the database.
func (s *Store) Close() error {
	var errs []error
	if s.r != nil {
		errs = append(errs, s.r.Close())
	}
	errs = append(errs, s.w.Close())
	return errors.Join(errs...)
}

// Ping checks that both connection pools answer.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.w.PingContext(ctx); err != nil {
		return fmt.Errorf("store: %w", err)
	}
	if err := s.r.PingContext(ctx); err != nil {
		return fmt.Errorf("store: %w", err)
	}
	return nil
}

// write runs fn in a write transaction, committing if fn returns nil.
func (s *Store) write(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// now returns the current time as stored: Unix milliseconds.
func now() int64 { return time.Now().UnixMilli() }

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		num, _, ok := strings.Cut(e.Name(), "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil || v <= 0 {
			return nil, fmt.Errorf("store: migration %q is not named NNNN_name.sql", e.Name())
		}
		b, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: v, name: e.Name(), sql: string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i := range out {
		if out[i].version != i+1 {
			return nil, fmt.Errorf("store: migrations are not numbered 1, 2, 3...: found %s", out[i].name)
		}
	}
	return out, nil
}

// SchemaVersion returns the highest migration applied.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v sql.NullInt64
	err := s.w.QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&v)
	return int(v.Int64), err
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.w.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT    NOT NULL,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("store: %w", err)
	}
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	current, err := s.SchemaVersion(ctx)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	if current > len(migrations) {
		return fmt.Errorf("store: database schema version %d is newer than this binary knows (%d); "+
			"run a newer selfcloud", current, len(migrations))
	}
	for _, m := range migrations[current:] {
		err := s.write(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, m.sql); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx,
				"INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)",
				m.version, m.name, now())
			return err
		})
		if err != nil {
			return fmt.Errorf("store: migration %s: %w", m.name, err)
		}
		s.log.Info("applied migration", "name", m.name)
	}
	return nil
}
