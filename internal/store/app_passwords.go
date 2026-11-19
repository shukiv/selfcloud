// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// AppPassword is a person's credential for the WebDAV mount (decision 236). Only its hash is stored. Revoking deletes
// the row, so a listed app password is always a live one.
type AppPassword struct {
	ID         string
	AccountID  string
	UserID     string
	Name       string
	Access     string // "read" or "write"
	Prefix     string
	SecretHash []byte
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// App password access levels.
const (
	AppAccessRead  = "read"
	AppAccessWrite = "write"
)

// MaxAppPasswords is the most app passwords one user may hold (decision 236).
const MaxAppPasswords = 20

const appPasswordCols = "id, account_id, user_id, name, access, prefix, secret_hash, created_at, last_used_at"

func scanAppPassword(row interface{ Scan(...any) error }) (*AppPassword, error) {
	var a AppPassword
	var created int64
	var used sql.NullInt64
	err := row.Scan(&a.ID, &a.AccountID, &a.UserID, &a.Name, &a.Access, &a.Prefix, &a.SecretHash,
		&created, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.CreatedAt = fromMillis(created)
	a.LastUsedAt = fromNullMillis(used)
	return &a, nil
}

// AppPasswordByID returns the app password with the given ID. The mount loads the row by the ID its secret names, then
// compares the hash in constant time, so a guess at the random tail learns nothing from the lookup.
func (s *Store) AppPasswordByID(ctx context.Context, id string) (*AppPassword, error) {
	return scanAppPassword(s.r.QueryRowContext(ctx,
		"SELECT "+appPasswordCols+" FROM app_passwords WHERE id = ?", id))
}

// AppPasswords lists a user's app passwords, oldest first.
func (s *Store) AppPasswords(ctx context.Context, accountID, userID string) ([]*AppPassword, error) {
	rows, err := s.r.QueryContext(ctx,
		"SELECT "+appPasswordCols+" FROM app_passwords WHERE account_id = ? AND user_id = ? ORDER BY created_at, id",
		accountID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AppPassword
	for rows.Next() {
		a, err := scanAppPassword(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CountAppPasswords returns how many app passwords the user holds.
func (s *Store) CountAppPasswords(ctx context.Context, accountID, userID string) (int, error) {
	return countAppPasswords(ctx, s.r, accountID, userID)
}

// CountAppPasswords is CountAppPasswords inside a write transaction.
func (tx *Tx) CountAppPasswords(ctx context.Context, accountID, userID string) (int, error) {
	return countAppPasswords(ctx, tx.q, accountID, userID)
}

func countAppPasswords(ctx context.Context, q querier, accountID, userID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM app_passwords WHERE account_id = ? AND user_id = ?",
		accountID, userID).Scan(&n)
	return n, err
}

// InsertAppPassword adds a.
func (tx *Tx) InsertAppPassword(ctx context.Context, a *AppPassword) error {
	_, err := tx.q.ExecContext(ctx, "INSERT INTO app_passwords ("+appPasswordCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		a.ID, a.AccountID, a.UserID, a.Name, a.Access, a.Prefix, a.SecretHash, millis(a.CreatedAt),
		nullMillis(a.LastUsedAt))
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

// DeleteAppPassword deletes the user's app password with the given ID. It returns ErrNotFound if no such app password
// belongs to the user. The audit, written by the caller in the same transaction, keeps the record of the revocation.
func (tx *Tx) DeleteAppPassword(ctx context.Context, accountID, userID, id string) error {
	return rowsAffected(tx.q.ExecContext(ctx,
		"DELETE FROM app_passwords WHERE id = ? AND account_id = ? AND user_id = ?", id, accountID, userID))
}

// DeleteAppPasswords deletes every app password of one user. A password change or reset, an admin's password set, and
// the revocation of the user's tokens or sessions call it in their own transaction, so a mount credential does not
// outlive the password or the sessions it was made under (decision 236).
func (tx *Tx) DeleteAppPasswords(ctx context.Context, accountID, userID string) error {
	_, err := tx.q.ExecContext(ctx,
		"DELETE FROM app_passwords WHERE account_id = ? AND user_id = ?", accountID, userID)
	return err
}

// TouchAppPassword records that the app password was used.
func (s *Store) TouchAppPassword(ctx context.Context, id string, at time.Time) error {
	return s.Write(ctx, func(tx *Tx) error {
		_, err := tx.q.ExecContext(ctx, "UPDATE app_passwords SET last_used_at = ? WHERE id = ?", millis(at), id)
		return err
	})
}
