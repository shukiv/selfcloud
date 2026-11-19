// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// AppPassword is a person's credential for the WebDAV mount (decision 236). Only its hash is stored.
type AppPassword struct {
	ID         string
	AccountID  string
	UserID     string
	Name       string
	Access     string // "read" or "write"
	Prefix     string
	SecretHash []byte
	CreatedAt  time.Time
	RevokedAt  *time.Time
	LastUsedAt *time.Time
}

// App password access levels.
const (
	AppAccessRead  = "read"
	AppAccessWrite = "write"
)

// MaxAppPasswords is the most unrevoked app passwords one user may hold (decision 236).
const MaxAppPasswords = 20

const appPasswordCols = "id, account_id, user_id, name, access, prefix, secret_hash, created_at, revoked_at, last_used_at"

func scanAppPassword(row interface{ Scan(...any) error }) (*AppPassword, error) {
	var a AppPassword
	var created int64
	var revoked, used sql.NullInt64
	err := row.Scan(&a.ID, &a.AccountID, &a.UserID, &a.Name, &a.Access, &a.Prefix, &a.SecretHash,
		&created, &revoked, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.CreatedAt = fromMillis(created)
	a.RevokedAt = fromNullMillis(revoked)
	a.LastUsedAt = fromNullMillis(used)
	return &a, nil
}

// AppPasswordByHash returns the app password with the given secret hash, revoked or not.
func (s *Store) AppPasswordByHash(ctx context.Context, hash []byte) (*AppPassword, error) {
	return scanAppPassword(s.r.QueryRowContext(ctx,
		"SELECT "+appPasswordCols+" FROM app_passwords WHERE secret_hash = ?", hash))
}

// AppPasswords lists a user's app passwords, oldest first, revoked ones included.
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

// CountAppPasswords returns how many unrevoked app passwords the user holds.
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
		"SELECT COUNT(*) FROM app_passwords WHERE account_id = ? AND user_id = ? AND revoked_at IS NULL",
		accountID, userID).Scan(&n)
	return n, err
}

// InsertAppPassword adds a.
func (tx *Tx) InsertAppPassword(ctx context.Context, a *AppPassword) error {
	_, err := tx.q.ExecContext(ctx, "INSERT INTO app_passwords ("+appPasswordCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		a.ID, a.AccountID, a.UserID, a.Name, a.Access, a.Prefix, a.SecretHash, millis(a.CreatedAt),
		nullMillis(a.RevokedAt), nullMillis(a.LastUsedAt))
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

// RevokeAppPassword revokes the user's app password with the given ID. Revoking a revoked one keeps its first
// revocation time. It returns ErrNotFound if no such app password belongs to the user.
func (tx *Tx) RevokeAppPassword(ctx context.Context, accountID, userID, id string, at time.Time) error {
	return rowsAffected(tx.q.ExecContext(ctx,
		"UPDATE app_passwords SET revoked_at = COALESCE(revoked_at, ?) WHERE id = ? AND account_id = ? AND user_id = ?",
		millis(at), id, accountID, userID))
}

// TouchAppPassword records that the app password was used.
func (s *Store) TouchAppPassword(ctx context.Context, id string, at time.Time) error {
	return s.Write(ctx, func(tx *Tx) error {
		_, err := tx.q.ExecContext(ctx, "UPDATE app_passwords SET last_used_at = ? WHERE id = ?", millis(at), id)
		return err
	})
}
