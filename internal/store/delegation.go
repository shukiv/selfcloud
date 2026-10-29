// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Delegation is the state a credential is checked against when it is used: whether its account is
// active, whether the account key that minted it still works, and whether the user it acts for is
// active. A delegated token carries only a signature, so revoking it means changing one of these
// rows, and every use reads them.
type Delegation struct {
	AccountStatus   string
	TokensNotBefore time.Time // tokens issued at or before this moment are void
	KeyFound        bool      // the key exists in the account
	KeyRevokedAt    *time.Time
	KeyExpiresAt    *time.Time
	UserFound       bool // the user exists in the account
	UserRole        string
	UserStatus      string
	// UserTokensNotBefore is the user's own cut-off: the user's tokens issued at or before it are
	// void. Zero when the user was never revoked, and when there is no user.
	UserTokensNotBefore time.Time
}

// Delegation reads the state of the account, of the key keyID and of the user userID, in one
// query. An empty keyID or userID is simply not found, so a caller that has neither reads only
// the account. It returns ErrNotFound when the account does not exist.
func (s *Store) Delegation(ctx context.Context, accountID, keyID, userID string) (*Delegation, error) {
	return delegation(ctx, s.r, accountID, keyID, userID)
}

// Delegation is Store.Delegation read inside a write transaction, so a check made there sees
// the state the write will see.
func (tx *Tx) Delegation(ctx context.Context, accountID, keyID, userID string) (*Delegation, error) {
	return delegation(ctx, tx.q, accountID, keyID, userID)
}

// delegation joins each row to the account it must belong to. A key or user from another account
// does not match, so it is reported as not found rather than trusted.
func delegation(ctx context.Context, q querier, accountID, keyID, userID string) (*Delegation, error) {
	var (
		d                            Delegation
		tnb                          int64
		keyAccount, role, userStatus sql.NullString
		keyRevoked, keyExpires       sql.NullInt64
		userTnb                      sql.NullInt64
	)
	err := q.QueryRowContext(ctx, `SELECT a.status, a.tokens_not_before,
			k.account_id, k.revoked_at, k.expires_at,
			u.role, u.status, u.tokens_not_before
		FROM accounts a
		LEFT JOIN api_keys k ON k.id = ? AND k.account_id = a.id
		LEFT JOIN users u ON u.id = ? AND u.account_id = a.id
		WHERE a.id = ?`, keyID, userID, accountID).Scan(&d.AccountStatus, &tnb,
		&keyAccount, &keyRevoked, &keyExpires, &role, &userStatus, &userTnb)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d.TokensNotBefore = fromMillis(tnb)
	d.KeyFound = keyAccount.Valid
	d.KeyRevokedAt = fromNullMillis(keyRevoked)
	d.KeyExpiresAt = fromNullMillis(keyExpires)
	d.UserFound = role.Valid
	d.UserRole = role.String
	d.UserStatus = userStatus.String
	if userTnb.Valid {
		d.UserTokensNotBefore = fromMillis(userTnb.Int64)
	}
	return &d, nil
}
