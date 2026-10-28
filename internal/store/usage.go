package store

import (
	"context"
	"database/sql"
	"errors"
)

// UsageChange is a change to an account's counters, made inside a transaction together with the
// change it counts. Bytes and Files follow the file tree; Reserved follows unfinished uploads.
type UsageChange struct {
	Bytes    int64
	Files    int64
	Reserved int64
}

// Usage is an account's counters as stored.
type Usage struct {
	Bytes    int64 // used_bytes: the sizes of the account's files
	Files    int64 // used_files: how many files it holds
	Reserved int64 // reserved_bytes: the declared length of each unfinished upload
}

// AdjustUsage applies c to the account's counters. A counter never goes below zero: a change that
// would take one below zero leaves it at zero and logs a warning, because it means the counters had
// drifted, and a delete or shrink must never fail on that account. `selfcloud admin recount-usage`
// corrects the numbers.
func (tx *Tx) AdjustUsage(ctx context.Context, accountID string, c UsageChange) error {
	cur, err := tx.usage(ctx, accountID)
	if err != nil {
		return err
	}
	next := Usage{Bytes: cur.Bytes + c.Bytes, Files: cur.Files + c.Files, Reserved: cur.Reserved + c.Reserved}
	if next.Bytes < 0 || next.Files < 0 || next.Reserved < 0 {
		if tx.log != nil {
			tx.log.Warn("usage counter would go negative; clamped to zero", "account", accountID,
				"used_bytes", cur.Bytes, "change_bytes", c.Bytes, "used_files", cur.Files, "change_files", c.Files,
				"reserved_bytes", cur.Reserved, "change_reserved", c.Reserved)
		}
		next = Usage{Bytes: max(next.Bytes, 0), Files: max(next.Files, 0), Reserved: max(next.Reserved, 0)}
	}
	return rowsAffected(tx.q.ExecContext(ctx, `UPDATE accounts SET used_bytes = ?, used_files = ?,
		reserved_bytes = ? WHERE id = ?`, next.Bytes, next.Files, next.Reserved, accountID))
}

// usage reads an account's counters inside the transaction. ErrNotFound if the account is gone.
func (tx *Tx) usage(ctx context.Context, accountID string) (Usage, error) {
	var u Usage
	err := tx.q.QueryRowContext(ctx, "SELECT used_bytes, used_files, reserved_bytes FROM accounts WHERE id = ?",
		accountID).Scan(&u.Bytes, &u.Files, &u.Reserved)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

// RecountAccount recomputes an account's counters from the rows they count: used_bytes and used_files
// from its file nodes, reserved_bytes from its unfinished uploads. It returns the counters before and
// after. Call it in a transaction of its own, per account.
func (tx *Tx) RecountAccount(ctx context.Context, accountID string) (before, after Usage, err error) {
	before, err = tx.usage(ctx, accountID)
	if err != nil {
		return before, after, err
	}
	err = tx.q.QueryRowContext(ctx, `SELECT COALESCE(SUM(size), 0), COUNT(*) FROM nodes
		WHERE account_id = ? AND kind = 'file'`, accountID).Scan(&after.Bytes, &after.Files)
	if err != nil {
		return before, after, err
	}
	err = tx.q.QueryRowContext(ctx, "SELECT COALESCE(SUM(length), 0) FROM uploads WHERE account_id = ?",
		accountID).Scan(&after.Reserved)
	if err != nil {
		return before, after, err
	}
	err = rowsAffected(tx.q.ExecContext(ctx, `UPDATE accounts SET used_bytes = ?, used_files = ?,
		reserved_bytes = ? WHERE id = ?`, after.Bytes, after.Files, after.Reserved, accountID))
	return before, after, err
}

// UsageTotals sums the counters of every account: what the installation holds in total.
func (s *Store) UsageTotals(ctx context.Context) (used, reserved int64, err error) {
	err = s.r.QueryRowContext(ctx, "SELECT COALESCE(SUM(used_bytes), 0), COALESCE(SUM(reserved_bytes), 0) FROM accounts").
		Scan(&used, &reserved)
	return used, reserved, err
}

// ReservationTotals returns the sum of the reserved_bytes counters and the sum of the lengths of
// the unfinished uploads. They agree unless the counters have drifted; the sweep compares them.
func (tx *Tx) ReservationTotals(ctx context.Context) (counted, declared int64, err error) {
	err = tx.q.QueryRowContext(ctx, `SELECT (SELECT COALESCE(SUM(reserved_bytes), 0) FROM accounts),
		(SELECT COALESCE(SUM(length), 0) FROM uploads)`).Scan(&counted, &declared)
	return counted, declared, err
}

// CountFolders counts the top-level folders of an account.
func (s *Store) CountFolders(ctx context.Context, accountID string) (int64, error) {
	var n int64
	err := s.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM folders WHERE account_id = ?", accountID).Scan(&n)
	return n, err
}
