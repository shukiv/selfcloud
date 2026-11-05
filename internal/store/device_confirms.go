// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// DeviceConfirm is a single-use link that confirms a new browser after the email's sign-in cap is spent (sec2, decision
// 86). Only the hashes of its token and of the browser's nonce are stored. Short is the "Keep me signed in" choice the
// attempt made, so the session the link starts keeps that choice.
type DeviceConfirm struct {
	ID        string
	UserID    string
	TokenHash []byte
	NonceHash []byte
	Short     bool
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}

// InsertDeviceConfirm adds c.
func (tx *Tx) InsertDeviceConfirm(ctx context.Context, c *DeviceConfirm) error {
	short := 0
	if c.Short {
		short = 1
	}
	_, err := tx.q.ExecContext(ctx, `INSERT INTO device_confirms
		(id, user_id, token_hash, nonce_hash, short, expires_at, used_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.UserID, c.TokenHash, c.NonceHash, short, millis(c.ExpiresAt), nullMillis(c.UsedAt), millis(c.CreatedAt))
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

// DeviceConfirmByHash returns the device confirmation with the given token hash, or ErrNotFound.
func (tx *Tx) DeviceConfirmByHash(ctx context.Context, hash []byte) (*DeviceConfirm, error) {
	var c DeviceConfirm
	var expires, created, short int64
	var used sql.NullInt64
	err := tx.q.QueryRowContext(ctx, `SELECT id, user_id, token_hash, nonce_hash, short, expires_at, used_at, created_at
		FROM device_confirms WHERE token_hash = ?`, hash).
		Scan(&c.ID, &c.UserID, &c.TokenHash, &c.NonceHash, &short, &expires, &used, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.Short = short == 1
	c.ExpiresAt = fromMillis(expires)
	c.UsedAt = fromNullMillis(used)
	c.CreatedAt = fromMillis(created)
	return &c, nil
}

// UseDeviceConfirm marks the link used at the given time. A link that is already used is ErrNotFound, so two
// redemptions cannot both succeed.
func (tx *Tx) UseDeviceConfirm(ctx context.Context, id string, at time.Time) error {
	return rowsAffected(tx.q.ExecContext(ctx,
		"UPDATE device_confirms SET used_at = ? WHERE id = ? AND used_at IS NULL", millis(at), id))
}

// DeleteSpentDeviceConfirms removes device confirmations that expired before now or were used.
func (tx *Tx) DeleteSpentDeviceConfirms(ctx context.Context, now time.Time) (int64, error) {
	res, err := tx.q.ExecContext(ctx,
		"DELETE FROM device_confirms WHERE expires_at <= ? OR used_at IS NOT NULL", millis(now))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// TokensNotBeforeIn reads a user's cut-off inside tx: the time before which the user's delegated tokens and device
// cookies are void. The zero time means no cut-off. It returns ErrNotFound when accountID has no such user.
func (tx *Tx) TokensNotBeforeIn(ctx context.Context, accountID, id string) (time.Time, error) {
	clause, cargs := accountClause(accountID)
	var ms int64
	err := tx.q.QueryRowContext(ctx, "SELECT tokens_not_before FROM users WHERE id = ? AND "+clause,
		append([]any{id}, cargs...)...).Scan(&ms)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, ErrNotFound
	}
	if err != nil {
		return time.Time{}, err
	}
	if ms == 0 {
		return time.Time{}, nil
	}
	return fromMillis(ms), nil
}
