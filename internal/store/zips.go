// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Zip is a stored zip download (migration 0029, decision 35). Its file is kept under derived/zips; this row says who
// asked for it, which nodes it holds, and when it expires.
type Zip struct {
	ID          string
	AccountID   string
	CreatedBy   string
	CreatedKind string
	CreatedCred string // the token's fingerprint, or "" for a caller with no token
	State       string // pending, ready or failed
	Size        int64
	SHA256      string
	Entries     int
	Skipped     int
	Reserved    int64
	SnapshotAt  time.Time
	ExpiresAt   time.Time
	Error       string
	ErrorCode   string // the API error code of a failure, or ""; the interface translates it
	ErrorReason string // the details' reason of that code, or ""
	CreatedAt   time.Time
	Roots       []string
}

// Zip states.
const (
	ZipPending = "pending"
	ZipReady   = "ready"
	ZipFailed  = "failed"
)

const zipCols = `id, account_id, created_by, created_kind, created_cred, state, size, sha256, entries, skipped, reserved,
	snapshot_at, expires_at, error, created_at, error_code, error_reason`

func scanZip(row interface{ Scan(...any) error }) (*Zip, error) {
	var z Zip
	var snap, exp sql.NullInt64
	var created int64
	if err := row.Scan(&z.ID, &z.AccountID, &z.CreatedBy, &z.CreatedKind, &z.CreatedCred, &z.State, &z.Size, &z.SHA256, &z.Entries,
		&z.Skipped, &z.Reserved, &snap, &exp, &z.Error, &created, &z.ErrorCode, &z.ErrorReason); err != nil {
		return nil, err
	}
	if snap.Valid {
		z.SnapshotAt = time.UnixMilli(snap.Int64).UTC()
	}
	if exp.Valid {
		z.ExpiresAt = time.UnixMilli(exp.Int64).UTC()
	}
	z.CreatedAt = time.UnixMilli(created).UTC()
	return &z, nil
}

// Zip returns a zip of accountID, with its roots, or ErrNotFound.
func (s *Store) Zip(ctx context.Context, accountID, id string) (*Zip, error) {
	z, err := scanZip(s.r.QueryRowContext(ctx, "SELECT "+zipCols+" FROM zips WHERE account_id = ? AND id = ?",
		accountID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	z.Roots, err = s.zipRoots(ctx, id)
	return z, err
}

func (s *Store) zipRoots(ctx context.Context, zipID string) ([]string, error) {
	rows, err := s.r.QueryContext(ctx, "SELECT node_id FROM zip_roots WHERE zip_id = ? ORDER BY seq", zipID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// InsertZip stores a new pending zip and its roots.
func (tx *Tx) InsertZip(ctx context.Context, z *Zip) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO zips (id, account_id, created_by, created_kind, created_cred, state, size, sha256,
		entries, skipped, reserved, snapshot_at, expires_at, error, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 0, '', 0, 0, 0, NULL, NULL, '', ?)`,
		z.ID, z.AccountID, z.CreatedBy, z.CreatedKind, z.CreatedCred, ZipPending, millis(z.CreatedAt))
	if err != nil {
		return err
	}
	for i, n := range z.Roots {
		if _, err := tx.q.ExecContext(ctx, "INSERT INTO zip_roots (zip_id, seq, node_id) VALUES (?, ?, ?)", z.ID, i, n); err != nil {
			return err
		}
	}
	return nil
}

// CountPendingZips counts the unfinished zips of an account, or of one caller in it when createdBy is not "".
func (s *Store) CountPendingZips(ctx context.Context, accountID, createdBy string) (int, error) {
	q := `SELECT COUNT(*) FROM zips WHERE account_id = ? AND state = 'pending'`
	args := []any{accountID}
	if createdBy != "" {
		q += ` AND created_by = ?`
		args = append(args, createdBy)
	}
	var n int
	err := s.r.QueryRowContext(ctx, q, args...).Scan(&n)
	return n, err
}

// CountAllPendingZips counts the unfinished zips of the whole installation.
func (s *Store) CountAllPendingZips(ctx context.Context) (int, error) {
	var n int
	err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM zips WHERE state = 'pending'`).Scan(&n)
	return n, err
}

// ZipDiskUse returns the bytes of the ready zips and the bytes reserved by the unfinished ones.
func (s *Store) ZipDiskUse(ctx context.Context) (ready, reserved int64, err error) {
	return zipDiskUse(ctx, s.r, "")
}

// ZipDiskUseOf is ZipDiskUse for the zips of one account.
func (s *Store) ZipDiskUseOf(ctx context.Context, accountID string) (ready, reserved int64, err error) {
	return zipDiskUse(ctx, s.r, accountID)
}

// ZipDiskUse is ZipDiskUse inside a write, so a check and the change it allows see the same totals.
func (tx *Tx) ZipDiskUse(ctx context.Context) (ready, reserved int64, err error) {
	return zipDiskUse(ctx, tx.q, "")
}

// ZipDiskUseOf is ZipDiskUseOf inside a write.
func (tx *Tx) ZipDiskUseOf(ctx context.Context, accountID string) (ready, reserved int64, err error) {
	return zipDiskUse(ctx, tx.q, accountID)
}

// zipDiskUse sums the ready bytes and the reserved bytes of the zips of accountID, or of every account when it is "".
func zipDiskUse(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, accountID string) (ready, reserved int64, err error) {
	err = q.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN state = 'ready' THEN size ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN state = 'pending' THEN reserved ELSE 0 END), 0) FROM zips
		WHERE ? = '' OR account_id = ?`, accountID, accountID).Scan(&ready, &reserved)
	return ready, reserved, err
}

// DeleteFailedZips removes up to limit failed zips created before cutoff, with their roots. It returns how many.
func (tx *Tx) DeleteFailedZips(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	const pick = `SELECT id FROM zips WHERE state = 'failed' AND created_at < ? ORDER BY created_at, id LIMIT ?`
	if _, err := tx.q.ExecContext(ctx, `DELETE FROM zip_roots WHERE zip_id IN (`+pick+`)`, millis(cutoff), limit); err != nil {
		return 0, err
	}
	res, err := tx.q.ExecContext(ctx, `DELETE FROM zips WHERE id IN (`+pick+`)`, millis(cutoff), limit)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ZipTree returns the untrashed subtree of rootID, the root included, parents and children alike, in no set order. It
// returns at most limit nodes, and the query stops there, so a subtree of any size costs limit rows in memory. A caller
// that gets limit nodes back has not necessarily seen the whole subtree, so it refuses the zip if the count passes its
// own budget, rather than using the rows. The root's own trash state is the caller's to check: a trashed root has no
// children here.
func (s *Store) ZipTree(ctx context.Context, accountID, rootID string, limit int) ([]*Node, error) {
	rows, err := s.r.QueryContext(ctx, `WITH RECURSIVE sub(nid) AS (
		SELECT id FROM nodes WHERE account_id = ? AND id = ?
		UNION ALL
		SELECT c.id FROM nodes c JOIN sub ON c.parent_id = sub.nid WHERE c.account_id = ? AND c.trashed_at IS NULL
	)
	`+nodeSelect+` JOIN sub ON sub.nid = n.id WHERE n.account_id = ? LIMIT ?`, accountID, rootID, accountID, accountID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// SetZipSnapshot records the snapshot of a pending zip. Its byte total is reserved until the zip is ready or failed.
func (tx *Tx) SetZipSnapshot(ctx context.Context, id string, reserved int64, entries, skipped int, at time.Time) error {
	_, err := tx.q.ExecContext(ctx, `UPDATE zips SET reserved = ?, entries = ?, skipped = ?, snapshot_at = ?
		WHERE id = ? AND state = 'pending'`, reserved, entries, skipped, millis(at), id)
	return err
}

// CompleteZip makes a pending zip ready, with its file's size and hash, and sets its expiry. It reports whether the
// row was pending.
func (tx *Tx) CompleteZip(ctx context.Context, id string, size int64, sha string, expires time.Time) (bool, error) {
	res, err := tx.q.ExecContext(ctx, `UPDATE zips SET state = 'ready', size = ?, sha256 = ?, reserved = 0,
		expires_at = ?, error = '' WHERE id = ? AND state = 'pending'`, size, sha, millis(expires), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// FailZip makes a pending zip failed, with a short reason and the code and reason of the refusal, if it has one.
// Its reservation is released.
func (tx *Tx) FailZip(ctx context.Context, id, code, errReason, reason string) error {
	if len(reason) > 200 {
		reason = reason[:200]
	}
	_, err := tx.q.ExecContext(ctx, `UPDATE zips SET state = 'failed', reserved = 0, error = ?, error_code = ?, error_reason = ?
		WHERE id = ? AND state = 'pending'`, reason, code, errReason, id)
	return err
}

// DeleteZip removes a zip row and its roots. It reports whether a row was removed.
func (tx *Tx) DeleteZip(ctx context.Context, accountID, id string) (bool, error) {
	if _, err := tx.q.ExecContext(ctx, "DELETE FROM zip_roots WHERE zip_id = ?", id); err != nil {
		return false, err
	}
	res, err := tx.q.ExecContext(ctx, "DELETE FROM zips WHERE account_id = ? AND id = ?", accountID, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// ExpiredZips returns up to limit ready zips whose expiry has passed at now.
func (s *Store) ExpiredZips(ctx context.Context, now time.Time, limit int) ([]*Zip, error) {
	return s.zipRows(ctx, `SELECT `+zipCols+` FROM zips WHERE state = 'ready' AND expires_at < ?
		ORDER BY expires_at, id LIMIT ?`, millis(now), limit)
}

// PendingZipsWithoutJob returns up to limit pending zips that no queued or running job will finish. The sweep fails
// them and removes their partial files: their job failed or was lost.
func (s *Store) PendingZipsWithoutJob(ctx context.Context, limit int) ([]*Zip, error) {
	return s.zipRows(ctx, `SELECT `+zipCols+` FROM zips z WHERE z.state = 'pending'
		AND NOT EXISTS (SELECT 1 FROM jobs j WHERE j.kind = 'node.zip' AND j.target = z.id
			AND j.status IN ('queued', 'running'))
		ORDER BY z.created_at, z.id LIMIT ?`, limit)
}

func (s *Store) zipRows(ctx context.Context, query string, args ...any) ([]*Zip, error) {
	rows, err := s.r.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Zip
	for rows.Next() {
		z, err := scanZip(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, z)
	}
	return out, rows.Err()
}
