// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Session is a signed-in browser. Only the hash of its cookie value is stored.
type Session struct {
	IDHash     []byte
	UserID     string
	AccountID  string // "" for an installation admin
	CSRFToken  string // sent back in X-CSRF-Token on unsafe requests
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	IP         string
	UserAgent  string
	// ImpersonatorID is the installation admin who signed in as UserID, and ImpersonatorSessionHash is
	// the hash of that admin's own session. Both are empty for an ordinary session (decision 41).
	ImpersonatorID          string
	ImpersonatorSessionHash []byte
}

const sessionCols = `id_hash, user_id, account_id, csrf_token, created_at, last_seen_at, expires_at,
	ip, user_agent, impersonator_user_id, impersonator_session_hash`

// SessionByHash returns the session with the given cookie hash. Expired sessions are returned
// too; the caller checks ExpiresAt. It returns ErrNotFound if there is none.
func (s *Store) SessionByHash(ctx context.Context, hash []byte) (*Session, error) {
	return scanSession(s.r.QueryRowContext(ctx, "SELECT "+sessionCols+" FROM sessions WHERE id_hash = ?", hash))
}

// SessionByHash is Store.SessionByHash inside a write transaction, so the answer and the writes
// that depend on it see the same state.
func (tx *Tx) SessionByHash(ctx context.Context, hash []byte) (*Session, error) {
	return scanSession(tx.q.QueryRowContext(ctx, "SELECT "+sessionCols+" FROM sessions WHERE id_hash = ?", hash))
}

// scanSession reads one session row. It returns ErrNotFound when there is no row.
func scanSession(row *sql.Row) (*Session, error) {
	sess, err := scanSessionRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return sess, err
}

// scanSessionRow reads one session from a row or from a rows cursor. It returns sql.ErrNoRows as it is.
func scanSessionRow(row interface{ Scan(...any) error }) (*Session, error) {
	var sess Session
	var created, seen, expires int64
	var acc, imp sql.NullString
	var impHash []byte
	err := row.Scan(&sess.IDHash, &sess.UserID, &acc, &sess.CSRFToken, &created, &seen, &expires,
		&sess.IP, &sess.UserAgent, &imp, &impHash)
	if err != nil {
		return nil, err
	}
	sess.AccountID = acc.String
	sess.CreatedAt = fromMillis(created)
	sess.LastSeenAt = fromMillis(seen)
	sess.ExpiresAt = fromMillis(expires)
	sess.ImpersonatorID = imp.String
	sess.ImpersonatorSessionHash = impHash
	return &sess, nil
}

// InsertSession adds sess.
func (tx *Tx) InsertSession(ctx context.Context, sess *Session) error {
	_, err := tx.q.ExecContext(ctx, "INSERT INTO sessions ("+sessionCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		sess.IDHash, sess.UserID, nullString(sess.AccountID), sess.CSRFToken, millis(sess.CreatedAt),
		millis(sess.LastSeenAt), millis(sess.ExpiresAt), sess.IP, sess.UserAgent,
		nullString(sess.ImpersonatorID), nullBytes(sess.ImpersonatorSessionHash))
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

// ImpersonationsOf returns the impersonation sessions in which the user is the person signed in as. It
// reads inside tx, so the caller can end them in the same write.
func (tx *Tx) ImpersonationsOf(ctx context.Context, userID string) ([]*Session, error) {
	return tx.impersonations(ctx, "user_id = ?", userID)
}

// ImpersonationsBy returns the impersonation sessions that the installation admin started.
func (tx *Tx) ImpersonationsBy(ctx context.Context, adminID string) ([]*Session, error) {
	return tx.impersonations(ctx, "impersonator_user_id = ?", adminID)
}

// ImpersonationsIn returns the impersonation sessions whose member belongs to the account (fu2, decision 74).
func (tx *Tx) ImpersonationsIn(ctx context.Context, accountID string) ([]*Session, error) {
	return tx.impersonations(ctx, "account_id = ?", accountID)
}

func (tx *Tx) impersonations(ctx context.Context, where string, arg string) ([]*Session, error) {
	rows, err := tx.q.QueryContext(ctx, "SELECT "+sessionCols+" FROM sessions WHERE impersonator_user_id IS NOT NULL AND "+
		where, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Session
	for rows.Next() {
		sess, err := scanSessionRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

// DeleteImpersonation removes an impersonation session by its cookie hash, and reports whether one was there.
func (tx *Tx) DeleteImpersonation(ctx context.Context, hash []byte) (bool, error) {
	res, err := tx.q.ExecContext(ctx, "DELETE FROM sessions WHERE id_hash = ? AND impersonator_user_id IS NOT NULL", hash)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// nullBytes is nil for an empty value, so an ordinary session stores NULL.
func nullBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// TouchSession records a use of the session: its last-seen and expiry times.
func (tx *Tx) TouchSession(ctx context.Context, hash []byte, lastSeen, expires time.Time) error {
	return rowsAffected(tx.q.ExecContext(ctx,
		"UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id_hash = ?",
		millis(lastSeen), millis(expires), hash))
}

// DeleteSession removes the session with the given cookie hash. Removing one that is gone is
// not an error.
func (tx *Tx) DeleteSession(ctx context.Context, hash []byte) error {
	_, err := tx.q.ExecContext(ctx, "DELETE FROM sessions WHERE id_hash = ?", hash)
	return err
}

// DeleteUserSessions removes every session of the user, which signs the user out everywhere.
func (tx *Tx) DeleteUserSessions(ctx context.Context, userID string) error {
	_, err := tx.q.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ?", userID)
	return err
}
