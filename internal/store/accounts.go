// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Account statuses.
const (
	AccountActive    = "active"
	AccountSuspended = "suspended"
	AccountDeleting  = "deleting"
)

// Account is a tenant: it owns users, folders and files, and has a storage quota.
type Account struct {
	ID         string
	ExternalID string // the client's own reference; "" when unset
	Name       string
	Status     string
	QuotaBytes int64 // 0 = unlimited
	// UserQuotaBytes is the limit each user gets when its own limit follows the account default; 0 = none.
	UserQuotaBytes  int64
	UsedBytes       int64
	UsedFiles       int64
	ReservedBytes   int64
	TokensNotBefore time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

const accountCols = `id, external_id, name, status, quota_bytes, used_bytes, used_files,
	reserved_bytes, tokens_not_before, created_at, updated_at, user_quota_bytes`

func scanAccount(row interface{ Scan(...any) error }) (*Account, error) {
	var a Account
	var ext sql.NullString
	var tnb, created, updated int64
	err := row.Scan(&a.ID, &ext, &a.Name, &a.Status, &a.QuotaBytes, &a.UsedBytes, &a.UsedFiles,
		&a.ReservedBytes, &tnb, &created, &updated, &a.UserQuotaBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.ExternalID = ext.String
	a.TokensNotBefore = fromMillis(tnb)
	a.CreatedAt = fromMillis(created)
	a.UpdatedAt = fromMillis(updated)
	return &a, nil
}

func getAccount(ctx context.Context, q querier, where string, arg any) (*Account, error) {
	return scanAccount(q.QueryRowContext(ctx, "SELECT "+accountCols+" FROM accounts WHERE "+where, arg))
}

// Account returns the account with the given ID.
func (s *Store) Account(ctx context.Context, id string) (*Account, error) {
	return getAccount(ctx, s.r, "id = ?", id)
}

// Account returns the account with the given ID.
func (tx *Tx) Account(ctx context.Context, id string) (*Account, error) {
	return getAccount(ctx, tx.q, "id = ?", id)
}

// AccountByExternalID returns the account with the given external ID.
func (tx *Tx) AccountByExternalID(ctx context.Context, externalID string) (*Account, error) {
	return getAccount(ctx, tx.q, "external_id = ?", externalID)
}

// Accounts lists accounts oldest first, optionally only the one with externalID.
func (s *Store) Accounts(ctx context.Context, externalID string, p Page) ([]*Account, string, error) {
	return s.AccountsFiltered(ctx, AccountFilter{ExternalID: externalID}, p)
}

// AccountFilter narrows an account list. Empty fields do not narrow.
type AccountFilter struct {
	ExternalID string
	// Query matches an account whose name contains it, or whose external ID is exactly it. The name match
	// folds ASCII case only, as SQLite's LIKE does, and the text is matched literally.
	Query  string
	Status string // AccountActive or AccountSuspended
}

// AccountsFiltered lists the accounts that f narrows to, oldest first, one page at a time.
func (s *Store) AccountsFiltered(ctx context.Context, f AccountFilter, p Page) ([]*Account, string, error) {
	where := []string{"1 = 1"}
	var args []any
	if f.ExternalID != "" {
		where = append(where, "external_id = ?")
		args = append(args, f.ExternalID)
	}
	if f.Query != "" {
		where = append(where, `(name LIKE ? ESCAPE '\' OR external_id = ?)`)
		args = append(args, "%"+likeEscaper.Replace(f.Query)+"%", f.Query)
	}
	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, f.Status)
	}
	if p.Cursor != "" {
		parts, err := decodeCursor(p.Cursor, 2)
		if err != nil {
			return nil, "", err
		}
		ms, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, "", ErrBadCursor
		}
		where = append(where, "(created_at, id) > (?, ?)")
		args = append(args, ms, parts[1])
	}
	args = append(args, p.Limit+1)
	rows, err := s.r.QueryContext(ctx, "SELECT "+accountCols+" FROM accounts WHERE "+
		strings.Join(where, " AND ")+" ORDER BY created_at, id LIMIT ?", args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []*Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > p.Limit {
		out = out[:p.Limit]
		last := out[len(out)-1]
		next = encodeCursor(strconv.FormatInt(millis(last.CreatedAt), 10), last.ID)
	}
	return out, next, nil
}

// InsertAccount adds a. It returns ErrConflict if the external ID is taken.
func (tx *Tx) InsertAccount(ctx context.Context, a *Account) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO accounts (`+accountCols+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, nullString(a.ExternalID), a.Name, a.Status, a.QuotaBytes, a.UsedBytes, a.UsedFiles,
		a.ReservedBytes, millis(a.TokensNotBefore), millis(a.CreatedAt), millis(a.UpdatedAt), a.UserQuotaBytes)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

// UpdateAccount writes a's name, external ID, status, quota and token cut-off. Usage counters
// are not touched; they change only alongside the files they count.
func (tx *Tx) UpdateAccount(ctx context.Context, a *Account) error {
	// A changed quota clears the account's quota marks, so the next change above a level is announced (webhook_quota.go).
	var old int64
	if err := tx.q.QueryRowContext(ctx, "SELECT quota_bytes FROM accounts WHERE id = ?", a.ID).Scan(&old); err != nil &&
		!errors.Is(err, sql.ErrNoRows) {
		return err
	}
	err := rowsAffected(tx.q.ExecContext(ctx, `UPDATE accounts SET external_id = ?, name = ?,
		status = ?, quota_bytes = ?, tokens_not_before = ?, updated_at = ? WHERE id = ?`,
		nullString(a.ExternalID), a.Name, a.Status, a.QuotaBytes, millis(a.TokensNotBefore),
		millis(a.UpdatedAt), a.ID))
	if isUniqueViolation(err) {
		return ErrConflict
	}
	if err != nil || old == a.QuotaBytes {
		return err
	}
	return tx.forgetAccountQuotaMarks(ctx, a.ID)
}

// DeleteAccount removes the account and, by cascade, everything that belongs to it in the
// database. Its office locks and editing sessions are cleared first, in this write, so an account
// with a file open in an editor is deleted at once (decision 32). Deleting an account that is
// already gone is not an error.
func (tx *Tx) DeleteAccount(ctx context.Context, id string) error {
	if _, err := tx.q.ExecContext(ctx, "DELETE FROM wopi_locks WHERE account_id = ?", id); err != nil {
		return err
	}
	if _, err := tx.q.ExecContext(ctx, "DELETE FROM wopi_sessions WHERE account_id = ?", id); err != nil {
		return err
	}
	_, err := tx.q.ExecContext(ctx, "DELETE FROM accounts WHERE id = ?", id)
	return err
}
