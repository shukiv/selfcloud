// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// SignInLink is a single-use link that signs a browser in as one user. Only the hash of its token
// is stored.
type SignInLink struct {
	ID        string
	AccountID string
	UserID    string
	TokenHash []byte
	Next      string // the path to open after sign-in
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
	// Frame is true for a link made for a host application's frame (decision 197): its URL names the account in a
	// query before the fragment, and redeeming it starts a frame session with a partitioned cookie.
	Frame bool
}

// InsertSignInLink adds l.
func (tx *Tx) InsertSignInLink(ctx context.Context, l *SignInLink) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO sign_in_links
		(id, account_id, user_id, token_hash, next, expires_at, used_at, created_at, frame)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		l.ID, l.AccountID, l.UserID, l.TokenHash, l.Next, millis(l.ExpiresAt), nullMillis(l.UsedAt), millis(l.CreatedAt), boolInt(l.Frame))
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

// SignInLinkByHash returns the sign-in link with the given token hash, or ErrNotFound.
func (tx *Tx) SignInLinkByHash(ctx context.Context, hash []byte) (*SignInLink, error) {
	var l SignInLink
	var expires, created int64
	var used sql.NullInt64
	var frame int
	err := tx.q.QueryRowContext(ctx, `SELECT id, account_id, user_id, token_hash, next, expires_at, used_at, created_at, frame
		FROM sign_in_links WHERE token_hash = ?`, hash).
		Scan(&l.ID, &l.AccountID, &l.UserID, &l.TokenHash, &l.Next, &expires, &used, &created, &frame)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	l.ExpiresAt = fromMillis(expires)
	l.UsedAt = fromNullMillis(used)
	l.CreatedAt = fromMillis(created)
	l.Frame = frame == 1
	return &l, nil
}

// UseSignInLink marks the link used at the given time. A link that is already used is ErrNotFound,
// so two redemptions cannot both succeed.
func (tx *Tx) UseSignInLink(ctx context.Context, id string, at time.Time) error {
	return rowsAffected(tx.q.ExecContext(ctx,
		"UPDATE sign_in_links SET used_at = ? WHERE id = ? AND used_at IS NULL", millis(at), id))
}

// DeleteSpentSignInLinks removes sign-in links that expired before now or were used.
func (tx *Tx) DeleteSpentSignInLinks(ctx context.Context, now time.Time) (int64, error) {
	res, err := tx.q.ExecContext(ctx,
		"DELETE FROM sign_in_links WHERE expires_at <= ? OR used_at IS NOT NULL", millis(now))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
