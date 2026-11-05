// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// The WOPI user info of a person (PutUserInfo, decision 87): one string per user, returned to the editor in
// CheckFileInfo. It is the user's own data: it is read only for that user, and the row goes with the user.

// PutWOPIUserInfo stores info as the user's WOPI user info, replacing any it had. An empty info clears it.
func (tx *Tx) PutWOPIUserInfo(ctx context.Context, accountID, userID, info string, at time.Time) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO wopi_user_info (user_id, account_id, info, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET info = excluded.info, updated_at = excluded.updated_at`,
		userID, accountID, info, millis(at))
	return err
}

// WOPIUserInfo returns the user's WOPI user info, or ErrNotFound when none is stored.
func (s *Store) WOPIUserInfo(ctx context.Context, userID string) (string, error) {
	return getWOPIUserInfo(ctx, s.r, userID)
}

// WOPIUserInfo is Store.WOPIUserInfo inside a write transaction.
func (tx *Tx) WOPIUserInfo(ctx context.Context, userID string) (string, error) {
	return getWOPIUserInfo(ctx, tx.q, userID)
}

func getWOPIUserInfo(ctx context.Context, q querier, userID string) (string, error) {
	var info string
	err := q.QueryRowContext(ctx, `SELECT info FROM wopi_user_info WHERE user_id = ?`, userID).Scan(&info)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return info, err
}
