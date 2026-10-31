// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Derived artefact states (migration 0027).
const (
	DerivedPending = "pending"
	DerivedReady   = "ready"
	DerivedFailed  = "failed"
)

// MaxDerivedError is the most characters a derived row's error may hold. The text is the server's own short
// reason, never the source's name or content.
const MaxDerivedError = 200

// Derived is one derived file of one blob: a thumbnail for now (design section 14, slice s1).
type Derived struct {
	ID         string
	AccountID  string
	BlobID     string
	Kind       string
	Converter  string
	State      string
	Size       int64
	SHA256     string
	Error      string
	CreatedAt  time.Time
	LastUsedAt time.Time
	// FailedAt is set when the row is failed. RetryAt is set when a failure may be retried, and zero when it is
	// sticky. Attempts counts the failures of the key.
	FailedAt time.Time
	RetryAt  time.Time
	Attempts int
}

// DerivedByID returns one row of an account by its ID.
func (s *Store) DerivedByID(ctx context.Context, accountID, id string) (*Derived, error) {
	d, err := scanDerived(s.r.QueryRowContext(ctx, `SELECT `+derivedCols+` FROM derived
		WHERE account_id = ? AND id = ?`, accountID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

// DerivedCount returns how many rows of a state exist, over every account.
func (s *Store) DerivedCount(ctx context.Context, state string) (int, error) {
	var n int
	err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM derived WHERE state = ?`, state).Scan(&n)
	return n, err
}

// DerivedExists reports whether a row with this ID exists for the account, whatever its state.
func (s *Store) DerivedExists(ctx context.Context, accountID, id string) (bool, error) {
	var n int
	err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM derived WHERE account_id = ? AND id = ?`, accountID, id).Scan(&n)
	return n > 0, err
}

// DerivedFor returns the derived row of a blob for a kind and converter version, of accountID only.
func (s *Store) DerivedFor(ctx context.Context, accountID, blobID, kind, converter string) (*Derived, error) {
	d, err := scanDerived(s.r.QueryRowContext(ctx, `SELECT `+derivedCols+` FROM derived
		WHERE account_id = ? AND blob_id = ? AND kind = ? AND converter = ?`, accountID, blobID, kind, converter))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

// DerivedThumbs returns the IDs of the nodes among nodeIDs whose blob has a ready derived file of kind and converter.
// The nodes are of accountID only. It is one query per listing page: the IDs are bound, not joined into the text.
func (s *Store) DerivedThumbs(ctx context.Context, accountID string, nodeIDs []string, kind, converter string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(nodeIDs) == 0 {
		return out, nil
	}
	args := []any{accountID, kind, converter, DerivedReady}
	marks := make([]string, len(nodeIDs))
	for i, id := range nodeIDs {
		marks[i] = "?"
		args = append(args, id)
	}
	rows, err := s.r.QueryContext(ctx, `SELECT n.id FROM nodes n
		JOIN derived d ON d.account_id = n.account_id AND d.blob_id = n.blob_id
		WHERE n.account_id = ? AND d.kind = ? AND d.converter = ? AND d.state = ?
		AND n.id IN (`+strings.Join(marks, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// InsertDerivedPending queues a derived file. It reports false, without error, when the key already has a row, or when
// the blob is not one of the account's: the caller then reads the row, rather than queueing the work twice, and
// refuses a blob it cannot see. The account check is in the statement, so no caller can skip it.
func (tx *Tx) InsertDerivedPending(ctx context.Context, d *Derived) (bool, error) {
	res, err := tx.q.ExecContext(ctx, `INSERT INTO derived (id, account_id, blob_id, kind, converter, state, size, sha256,
		error, created_at, last_used_at)
		SELECT ?, b.account_id, b.id, ?, ?, 'pending', 0, '', '', ?, ?
		FROM blobs b WHERE b.id = ? AND b.account_id = ?
		ON CONFLICT (blob_id, kind, converter) DO NOTHING`,
		d.ID, d.Kind, d.Converter, millis(d.CreatedAt), millis(d.CreatedAt), d.BlobID, d.AccountID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// CompleteDerived marks a pending derived file ready. It reports false when the row is no longer pending: it was
// removed while the file was made, so the caller must delete the file.
func (tx *Tx) CompleteDerived(ctx context.Context, accountID, id string, size int64, sha256 string, at time.Time) (bool, error) {
	res, err := tx.q.ExecContext(ctx, `UPDATE derived SET state = 'ready', size = ?, sha256 = ?, error = '',
		last_used_at = ? WHERE account_id = ? AND id = ? AND state = 'pending'`,
		size, sha256, millis(at), accountID, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// FailDerived marks a pending derived file failed, with a short reason, the time, and the attempt count. retryAt
// is the earliest time a later request may queue it again; the zero time makes the failure sticky. It reports false
// when the row is not pending.
func (tx *Tx) FailDerived(ctx context.Context, accountID, id, reason string, at time.Time, attempts int, retryAt time.Time) (bool, error) {
	if len(reason) > MaxDerivedError {
		reason = reason[:MaxDerivedError]
	}
	var retry sql.NullInt64
	if !retryAt.IsZero() {
		retry = sql.NullInt64{Int64: millis(retryAt), Valid: true}
	}
	res, err := tx.q.ExecContext(ctx, `UPDATE derived SET state = 'failed', error = ?, failed_at = ?, attempts = ?, retry_at = ?
		WHERE account_id = ? AND id = ? AND state = 'pending'`, reason, millis(at), attempts, retry, accountID, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// RetryDerived moves a failed row whose retry time has come back to pending, so the caller queues it. The attempt
// count is kept. It reports false when the row is not failed, or its time has not come.
func (tx *Tx) RetryDerived(ctx context.Context, accountID, id string, now time.Time) (bool, error) {
	res, err := tx.q.ExecContext(ctx, `UPDATE derived SET state = 'pending', error = '', failed_at = NULL, retry_at = NULL
		WHERE account_id = ? AND id = ? AND state = 'failed' AND retry_at IS NOT NULL AND retry_at <= ?`,
		accountID, id, millis(now))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ClearFailedDerived removes the failed rows that failed before the given time, sticky or not. The next request
// for such a key queues it again. It returns how many rows it removed.
func (tx *Tx) ClearFailedDerived(ctx context.Context, before time.Time) (int64, error) {
	res, err := tx.q.ExecContext(ctx, `DELETE FROM derived WHERE state = 'failed' AND failed_at < ?`, millis(before))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeletePendingDerived removes a pending row, such as one whose work could not be done now: a budget, a full disk,
// or a server fault. A later request queues it again. It reports false when the row is not pending.
func (tx *Tx) DeletePendingDerived(ctx context.Context, accountID, id string) (bool, error) {
	res, err := tx.q.ExecContext(ctx, `DELETE FROM derived WHERE account_id = ? AND id = ? AND state = 'pending'`,
		accountID, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// DeleteAllPendingDerived removes every pending row. It runs at start-up, before any worker exists: a pending row is
// work the last process did not finish, and the queue it was in is gone.
func (tx *Tx) DeleteAllPendingDerived(ctx context.Context) (int64, error) {
	res, err := tx.q.ExecContext(ctx, `DELETE FROM derived WHERE state = 'pending'`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteDerivedForBlob removes every derived row of a blob and returns them, so the caller removes their files.
// It is called inside the sweep's transaction for an orphan blob, before the blob's own row goes.
func (tx *Tx) DeleteDerivedForBlob(ctx context.Context, accountID, blobID string) ([]*Derived, error) {
	rows, err := tx.q.QueryContext(ctx, `SELECT `+derivedCols+` FROM derived WHERE account_id = ? AND blob_id = ?`,
		accountID, blobID)
	if err != nil {
		return nil, err
	}
	var out []*Derived
	for rows.Next() {
		d, err := scanDerived(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := tx.q.ExecContext(ctx, `DELETE FROM derived WHERE account_id = ? AND blob_id = ?`, accountID, blobID); err != nil {
		return nil, err
	}
	return out, nil
}

// ReadyDerivedBytes is the total size of the ready derived files of every account: the figure the budget reads.
func (tx *Tx) ReadyDerivedBytes(ctx context.Context) (int64, error) {
	var n sql.NullInt64
	err := tx.q.QueryRowContext(ctx, `SELECT SUM(size) FROM derived WHERE state = 'ready'`).Scan(&n)
	return n.Int64, err
}

// ReadyDerivedBytes is ReadyDerivedBytes read outside a write transaction, for the budget's eviction.
func (s *Store) ReadyDerivedBytes(ctx context.Context) (int64, error) {
	var n sql.NullInt64
	err := s.r.QueryRowContext(ctx, `SELECT SUM(size) FROM derived WHERE state = 'ready'`).Scan(&n)
	return n.Int64, err
}

// LeastUsedDerived returns up to limit ready rows, least recently used first.
func (s *Store) LeastUsedDerived(ctx context.Context, limit int) ([]*Derived, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+derivedCols+` FROM derived WHERE state = 'ready'
		ORDER BY last_used_at, id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Derived
	for rows.Next() {
		d, err := scanDerived(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeleteDerivedRow removes one row of an account. It reports false when none matched.
func (tx *Tx) DeleteDerivedRow(ctx context.Context, accountID, id string) (bool, error) {
	res, err := tx.q.ExecContext(ctx, `DELETE FROM derived WHERE account_id = ? AND id = ?`, accountID, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// TouchDerived records a use of a ready derived file, so the budget keeps it. It is called from a recorder that
// batches the uses, never once per request.
func (tx *Tx) TouchDerived(ctx context.Context, accountID, id string, at time.Time) error {
	_, err := tx.q.ExecContext(ctx, `UPDATE derived SET last_used_at = ? WHERE account_id = ? AND id = ? AND state = 'ready'`,
		millis(at), accountID, id)
	return err
}

const derivedCols = `id, account_id, blob_id, kind, converter, state, size, sha256, error, created_at, last_used_at,
	failed_at, attempts, retry_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanDerived(r rowScanner) (*Derived, error) {
	var d Derived
	var created, used int64
	var failed, retry sql.NullInt64
	if err := r.Scan(&d.ID, &d.AccountID, &d.BlobID, &d.Kind, &d.Converter, &d.State, &d.Size, &d.SHA256, &d.Error,
		&created, &used, &failed, &d.Attempts, &retry); err != nil {
		return nil, err
	}
	d.CreatedAt = fromMillis(created)
	d.LastUsedAt = fromMillis(used)
	if failed.Valid {
		d.FailedAt = fromMillis(failed.Int64)
	}
	if retry.Valid {
		d.RetryAt = fromMillis(retry.Int64)
	}
	return &d, nil
}
