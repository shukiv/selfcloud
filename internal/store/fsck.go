// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"
)

// CheckStore is the database as fsck reads it. It never migrates and never writes: it opens an existing file and
// sets query_only, so a server may run beside it. It is not a Store, so none of the write methods exist on it.
type CheckStore struct {
	r *sql.DB
}

// OpenCheck opens the database at path for reading. The file must exist; it is not created.
func OpenCheck(ctx context.Context, path string) (*CheckStore, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	// mode=rw without create: the file must exist. query_only makes any write an error. busy_timeout waits out a
	// checkpoint by the server rather than fail.
	db, err := sql.Open("sqlite", dsn(path, []string{"busy_timeout(10000)", "query_only(1)"}, "")+"&mode=rw")
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	db.SetMaxOpenConns(2)
	c := &CheckStore{r: db}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: %w", err)
	}
	return c, nil
}

// Close closes the database.
func (c *CheckStore) Close() error { return c.r.Close() }

// SchemaVersion returns the highest migration applied to the database.
func (c *CheckStore) SchemaVersion(ctx context.Context) (int, error) {
	var v sql.NullInt64
	err := c.r.QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&v)
	return int(v.Int64), err
}

// LatestSchemaVersion returns the version the migrations in this binary reach.
func LatestSchemaVersion() (int, error) {
	migs, err := loadMigrations()
	if err != nil {
		return 0, err
	}
	if len(migs) == 0 {
		return 0, errors.New("store: no migrations")
	}
	return migs[len(migs)-1].version, nil
}

// Snapshot is one read transaction. Every read in it sees the database as it was when the transaction's first read
// ran, so a set of reads made through one Snapshot agree with each other.
type Snapshot struct {
	tx *sql.Tx
	// At is taken before the first read, so the snapshot is no older than At.
	At time.Time
}

// Begin starts a read transaction and runs its first read, which fixes the snapshot in WAL mode.
func (c *CheckStore) Begin(ctx context.Context) (*Snapshot, error) {
	tx, err := c.r.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	at := time.Now()
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_schema").Scan(&n); err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("store: %w", err)
	}
	return &Snapshot{tx: tx, At: at}, nil
}

// Close ends the transaction.
func (s *Snapshot) Close() error { return s.tx.Rollback() }

// IntegrityCheck runs SQLite's integrity_check, which reports at most 100 problems. ok is true only when it
// reports exactly "ok".
func (s *Snapshot) IntegrityCheck(ctx context.Context) (ok bool, problems []string, err error) {
	rows, err := s.tx.QueryContext(ctx, "PRAGMA integrity_check(100)")
	if err != nil {
		return false, nil, fmt.Errorf("store: %w", err)
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

// ForeignKeyViolations runs SQLite's foreign_key_check and describes each row that breaks a foreign key.
func (s *Snapshot) ForeignKeyViolations(ctx context.Context) ([]string, error) {
	rows, err := s.tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
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

// FileRow is a row that names a file: a blob, a derived file or a zip. State is the row's state; Size and SHA256 are
// what the row says about the file.
type FileRow struct {
	ID        string
	AccountID string
	State     string
	Size      int64
	SHA256    string
}

// BlobRow returns the blob row of blobID, if there is one.
func (s *Snapshot) BlobRow(ctx context.Context, blobID string) (FileRow, bool, error) {
	return s.one(ctx, `SELECT id, account_id, state, size, sha256 FROM blobs WHERE id = ?`, blobID)
}

// BlobNamed reports whether some row names blobID: a blob row, or a node, a version, an upload or an import archive.
// A blob named by none of them is an orphan.
func (s *Snapshot) BlobNamed(ctx context.Context, blobID string) (bool, error) {
	var named bool
	err := s.tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM blobs WHERE id = ?1)
		OR EXISTS (SELECT 1 FROM nodes WHERE blob_id = ?1)
		OR EXISTS (SELECT 1 FROM versions WHERE blob_id = ?1)
		OR EXISTS (SELECT 1 FROM uploads WHERE blob_id = ?1)
		OR EXISTS (SELECT 1 FROM import_jobs WHERE archive_blob_id = ?1)`, blobID).Scan(&named)
	if err != nil {
		return false, fmt.Errorf("store: %w", err)
	}
	return named, nil
}

// BlobReferenced reports whether a node, version, upload or import archive names blobID, whatever blob row exists.
// A recheck uses it when no row remains: a missing file that something still names is missing.
func (s *Snapshot) BlobReferenced(ctx context.Context, blobID string) (bool, error) {
	var named bool
	err := s.tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM nodes WHERE blob_id = ?1)
		OR EXISTS (SELECT 1 FROM versions WHERE blob_id = ?1)
		OR EXISTS (SELECT 1 FROM uploads WHERE blob_id = ?1)
		OR EXISTS (SELECT 1 FROM import_jobs WHERE archive_blob_id = ?1)`, blobID).Scan(&named)
	if err != nil {
		return false, fmt.Errorf("store: %w", err)
	}
	return named, nil
}

// BlobReferrers returns the first node and the first version that name blobID, or "" for none. It is for
// describing a finding.
func (s *Snapshot) BlobReferrers(ctx context.Context, blobID string) (nodeID, versionID string, err error) {
	err = s.tx.QueryRowContext(ctx, `SELECT
		COALESCE((SELECT id FROM nodes WHERE blob_id = ?1 LIMIT 1), ''),
		COALESCE((SELECT id FROM versions WHERE blob_id = ?1 LIMIT 1), '')`, blobID).Scan(&nodeID, &versionID)
	if err != nil {
		return "", "", fmt.Errorf("store: %w", err)
	}
	return nodeID, versionID, nil
}

// BlobRef is a blob that a row names with no blob row of its own.
type BlobRef struct {
	AccountID string
	BlobID    string
}

// LiveBlobsAfter returns up to limit blob rows in state live whose ID sorts after after, in ID order. A caller reads the
// list a page at a time, each page in its own snapshot, so no snapshot is held for the whole list.
func (s *Snapshot) LiveBlobsAfter(ctx context.Context, after string, limit int) ([]FileRow, error) {
	return s.page(ctx, `SELECT id, account_id, state, size, sha256 FROM blobs
		WHERE state = 'live' AND id > ?1 ORDER BY id LIMIT ?2`, after, limit)
}

// UnrecordedBlobsAfter returns up to limit blobs that a node, version or import archive names while no blob row exists,
// after the cursor (afterBlob, afterAccount), in (blob, account) order.
func (s *Snapshot) UnrecordedBlobsAfter(ctx context.Context, afterBlob, afterAccount string, limit int) ([]BlobRef, error) {
	rows, err := s.tx.QueryContext(ctx, `SELECT account_id, blob_id FROM (
			SELECT account_id, blob_id FROM nodes
				WHERE blob_id IS NOT NULL AND blob_id NOT IN (SELECT id FROM blobs)
			UNION SELECT account_id, blob_id FROM versions WHERE blob_id NOT IN (SELECT id FROM blobs)
			UNION SELECT account_id, archive_blob_id FROM import_jobs
				WHERE archive_blob_id <> '' AND archive_blob_id NOT IN (SELECT id FROM blobs))
		WHERE blob_id > ?1 OR (blob_id = ?1 AND account_id > ?2)
		ORDER BY blob_id, account_id LIMIT ?3`, afterBlob, afterAccount, limit)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	defer rows.Close()
	var out []BlobRef
	for rows.Next() {
		var r BlobRef
		if err := rows.Scan(&r.AccountID, &r.BlobID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// page reads one page of file rows. query takes the cursor and the limit as its two parameters.
func (s *Snapshot) page(ctx context.Context, query, after string, limit int) ([]FileRow, error) {
	rows, err := s.tx.QueryContext(ctx, query, after, limit)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	defer rows.Close()
	var out []FileRow
	for rows.Next() {
		var r FileRow
		if err := rows.Scan(&r.ID, &r.AccountID, &r.State, &r.Size, &r.SHA256); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DerivedRow returns the derived row with derivedID, if there is one.
func (s *Snapshot) DerivedRow(ctx context.Context, derivedID string) (FileRow, bool, error) {
	return s.one(ctx, `SELECT id, account_id, state, size, sha256 FROM derived WHERE id = ?`, derivedID)
}

// DerivedKept reports whether derivedID is a ready row whose blob is still live or still named. The sweep removes a
// blob's derived rows before their files, and only once the blob is an orphan, so a row that fails this is not a
// missing file the sweep left behind.
func (s *Snapshot) DerivedKept(ctx context.Context, derivedID string) (bool, error) {
	var kept bool
	err := s.tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM derived d JOIN blobs b ON b.id = d.blob_id
		WHERE d.id = ?1 AND d.state = 'ready' AND (b.state = 'live'
			OR EXISTS (SELECT 1 FROM nodes n WHERE n.blob_id = b.id)
			OR EXISTS (SELECT 1 FROM versions v WHERE v.blob_id = b.id)))`, derivedID).Scan(&kept)
	if err != nil {
		return false, fmt.Errorf("store: %w", err)
	}
	return kept, nil
}

// ReadyDerivedAfter returns up to limit derived rows in state ready whose ID sorts after after, in ID order.
func (s *Snapshot) ReadyDerivedAfter(ctx context.Context, after string, limit int) ([]FileRow, error) {
	return s.page(ctx, `SELECT id, account_id, state, size, sha256 FROM derived
		WHERE state = 'ready' AND id > ?1 ORDER BY id LIMIT ?2`, after, limit)
}

// ZipRow returns the zip row with zipID, if there is one.
func (s *Snapshot) ZipRow(ctx context.Context, zipID string) (FileRow, bool, error) {
	return s.one(ctx, `SELECT id, account_id, state, size, sha256 FROM zips WHERE id = ?`, zipID)
}

// ReadyZipsAfter returns up to limit zip rows in state ready whose ID sorts after after, in ID order.
func (s *Snapshot) ReadyZipsAfter(ctx context.Context, after string, limit int) ([]FileRow, error) {
	return s.page(ctx, `SELECT id, account_id, state, size, sha256 FROM zips
		WHERE state = 'ready' AND id > ?1 ORDER BY id LIMIT ?2`, after, limit)
}

func (s *Snapshot) one(ctx context.Context, query string, arg string) (FileRow, bool, error) {
	var r FileRow
	err := s.tx.QueryRowContext(ctx, query, arg).Scan(&r.ID, &r.AccountID, &r.State, &r.Size, &r.SHA256)
	if errors.Is(err, sql.ErrNoRows) {
		return FileRow{}, false, nil
	}
	if err != nil {
		return FileRow{}, false, fmt.Errorf("store: %w", err)
	}
	return r, true, nil
}
