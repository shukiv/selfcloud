// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"

	"modernc.org/sqlite"
)

// SnapshotTo writes an online snapshot of the database at src into dst, which must not exist. The source is opened
// mode=ro, so it is never written: SQLite enforces that at the file open, stronger than query_only. VACUUM INTO is
// refused on a query_only connection with SQLITE_READONLY (TestVacuumIntoRefusedUnderQueryOnly proves it), which is
// why the snapshot uses mode=ro instead of OpenCheck's connection. The copy is one file in rollback-journal mode:
// no -wal or -shm belongs to it, and it holds the database as of one moment while a server keeps writing (WAL).
// busyMS waits out a busy server as OpenCheckBusy does; zero means 10 seconds. SQLite creates dst with the process
// umask; a caller that needs owner-only permissions sets the umask around the call.
func SnapshotTo(ctx context.Context, src, dst string, busyMS int) error {
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("store: %w", err)
	}
	if busyMS <= 0 {
		busyMS = 10000
	}
	db, err := sql.Open("sqlite", fileURI(src, url.Values{
		"_pragma": {fmt.Sprintf("busy_timeout(%d)", busyMS)},
		"mode":    {"ro"},
	}))
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("store: %w", err)
	}
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", dst); err != nil {
		return fmt.Errorf("store: snapshot: %w", err)
	}
	return nil
}

// SnapshotCheck is what CheckSnapshot found. Problems is empty for a good snapshot; each entry is one line naming
// an integrity, foreign-key or schema problem. SchemaVersion is the version the snapshot's schema_migrations
// records, zero when it could not be read.
type SnapshotCheck struct {
	SchemaVersion int
	Problems      []string
}

// CheckSnapshot verifies the snapshot at path without writing anything beside it: the file is opened read-only and
// immutable, so no -wal or -shm appears even for a WAL-mode file. The selfcloud_name collation is registered (the
// package's init), without which integrity_check cannot read the name indexes. It runs PRAGMA integrity_check and
// PRAGMA foreign_key_check and compares the schema version with the binary's. Problems it returns are findings
// (the file is a database but fails a check); an error means the check could not run (not a database, unreadable).
func CheckSnapshot(ctx context.Context, path string) (*SnapshotCheck, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	db, err := sql.Open("sqlite", fileURI(path, url.Values{"mode": {"ro"}, "immutable": {"1"}}))
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	rep := &SnapshotCheck{}
	if err := db.PingContext(ctx); err != nil {
		// A damaged file is a finding; a file that is not a database, or cannot be read, is a check that could not run.
		if sqliteCode(err) == 11 {
			rep.Problems = append(rep.Problems, "the database is damaged: "+err.Error())
			return rep, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	ok, problems, err := snapshotIntegrity(ctx, db)
	switch {
	case err != nil && Damaged(err):
		rep.Problems = append(rep.Problems, "integrity check: "+err.Error())
	case err != nil:
		return nil, fmt.Errorf("store: %w", err)
	case !ok:
		rep.Problems = append(rep.Problems, problems...)
	default:
		fks, err := snapshotForeignKeys(ctx, db)
		if err != nil {
			if !Damaged(err) {
				return nil, fmt.Errorf("store: %w", err)
			}
			rep.Problems = append(rep.Problems, "foreign key check: "+err.Error())
		} else {
			rep.Problems = append(rep.Problems, fks...)
		}
	}
	want, err := LatestSchemaVersion()
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	var name string
	switch err := db.QueryRowContext(ctx, "SELECT name FROM sqlite_schema WHERE name = 'schema_migrations'").Scan(&name); {
	case errors.Is(err, sql.ErrNoRows):
		rep.Problems = append(rep.Problems, "no schema_migrations table: not a Selfcloud snapshot")
		return rep, nil
	case err != nil && Damaged(err):
		rep.Problems = append(rep.Problems, "cannot read the schema: "+err.Error())
		return rep, nil
	case err != nil:
		return nil, fmt.Errorf("store: %w", err)
	}
	var v sql.NullInt64
	if err := db.QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&v); err != nil {
		if !Damaged(err) {
			return nil, fmt.Errorf("store: %w", err)
		}
		rep.Problems = append(rep.Problems, "cannot read the schema version: "+err.Error())
		return rep, nil
	}
	rep.SchemaVersion = int(v.Int64)
	if rep.SchemaVersion != want {
		rep.Problems = append(rep.Problems, fmt.Sprintf("schema version %d is not this binary's %d", rep.SchemaVersion, want))
	}
	return rep, nil
}

// snapshotIntegrity runs integrity_check, which reports at most 100 problems. ok is true only when it reports
// exactly "ok".
func snapshotIntegrity(ctx context.Context, db *sql.DB) (ok bool, problems []string, err error) {
	rows, err := db.QueryContext(ctx, "PRAGMA integrity_check(100)")
	if err != nil {
		return false, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return false, nil, err
		}
		problems = append(problems, line)
	}
	if err := rows.Err(); err != nil {
		return false, nil, err
	}
	return len(problems) == 1 && problems[0] == "ok", problems, nil
}

// snapshotForeignKeys runs foreign_key_check and describes each row that breaks a foreign key.
func snapshotForeignKeys(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var table, parent string
		var rowid sql.NullInt64
		var fk int64
		if err := rows.Scan(&table, &rowid, &parent, &fk); err != nil {
			return nil, err
		}
		out = append(out, fmt.Sprintf("table %s row %d refers to missing %s (foreign key %d)", table, rowid.Int64, parent, fk))
	}
	return out, rows.Err()
}

// sqliteCode returns err's primary SQLite result code, or zero when err is not a SQLite error.
func sqliteCode(err error) int {
	var se *sqlite.Error
	if errors.As(err, &se) {
		return se.Code() & 0xff
	}
	return 0
}

// fileURI builds a file: URI for path with the given parameters. The path is escaped, so a name with URI characters
// (? or #) stays one file name.
func fileURI(path string, params url.Values) string {
	u := url.URL{Scheme: "file", Path: path, RawQuery: params.Encode()}
	return u.String()
}
