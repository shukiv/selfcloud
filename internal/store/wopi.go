// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// WOPISession is one editing session that POST .../office opened: the row a WOPI access token names. Its
// token is refused once RevokedAt is set or ExpiresAt has passed. UserID is "" when an account key opened it.
type WOPISession struct {
	ID        string
	AccountID string
	NodeID    string
	UserID    string
	Role      string // "read" or "write"
	IssuedAt  time.Time
	ExpiresAt time.Time
	RevokedAt *time.Time
	// BrowserSession is the hash of the browser session that opened the editor, or nil when a delegated token did.
	// The editor ends with that browser session: every WOPI call checks it still exists (authenticateWOPI).
	BrowserSession []byte
}

const wopiSessionCols = `id, account_id, node_id, COALESCE(user_id, ''), role, issued_at, expires_at, revoked_at,
	browser_session`

// InsertWOPISession records a new editing session, inside tx.
func (tx *Tx) InsertWOPISession(ctx context.Context, w *WOPISession) error {
	var user, browser any
	if w.UserID != "" {
		user = w.UserID
	}
	if len(w.BrowserSession) > 0 {
		browser = w.BrowserSession
	}
	_, err := tx.q.ExecContext(ctx, `INSERT INTO wopi_sessions
		(id, account_id, node_id, user_id, role, issued_at, expires_at, browser_session)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, w.ID, w.AccountID, w.NodeID, user, w.Role, millis(w.IssuedAt),
		millis(w.ExpiresAt), browser)
	return err
}

// RevokeWOPISessions ends the live sessions of accountID at at: those of one user when userID is set, else every
// session of the account. The token itself is checked against the same cut-offs as before, so this only makes the
// row say what the cut-off already does. It returns how many rows it ended.
func (tx *Tx) RevokeWOPISessions(ctx context.Context, accountID, userID string, at time.Time) (int64, error) {
	var (
		res sql.Result
		err error
	)
	if userID == "" {
		res, err = tx.q.ExecContext(ctx, `UPDATE wopi_sessions SET revoked_at = ?
			WHERE account_id = ? AND revoked_at IS NULL`, millis(at), accountID)
	} else {
		res, err = tx.q.ExecContext(ctx, `UPDATE wopi_sessions SET revoked_at = ?
			WHERE account_id = ? AND user_id = ? AND revoked_at IS NULL`, millis(at), accountID, userID)
	}
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// WOPISession returns the session with the given ID in accountID, or ErrNotFound.
func (s *Store) WOPISession(ctx context.Context, accountID, id string) (*WOPISession, error) {
	return getWOPISession(ctx, s.r, accountID, id)
}

// WOPISession is Store.WOPISession inside a write transaction, so a check made there sees the writes made so far.
func (tx *Tx) WOPISession(ctx context.Context, accountID, id string) (*WOPISession, error) {
	return getWOPISession(ctx, tx.q, accountID, id)
}

func getWOPISession(ctx context.Context, q querier, accountID, id string) (*WOPISession, error) {
	var (
		w       WOPISession
		issued  int64
		expires int64
		revoked sql.NullInt64
	)
	err := q.QueryRowContext(ctx, `SELECT `+wopiSessionCols+` FROM wopi_sessions
		WHERE account_id = ? AND id = ?`, accountID, id).Scan(&w.ID, &w.AccountID, &w.NodeID, &w.UserID,
		&w.Role, &issued, &expires, &revoked, &w.BrowserSession)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	w.IssuedAt = fromMillis(issued)
	w.ExpiresAt = fromMillis(expires)
	w.RevokedAt = fromNullMillis(revoked)
	return &w, nil
}

// DeleteEndedWOPISessions removes the sessions whose end has passed, and returns how many. A token names its session
// and is refused past its own expiry, which is the same instant, so removing the row loses no refusal.
func (tx *Tx) DeleteEndedWOPISessions(ctx context.Context, now time.Time) (int64, error) {
	res, err := tx.q.ExecContext(ctx, `DELETE FROM wopi_sessions WHERE expires_at <= ?`, millis(now))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
