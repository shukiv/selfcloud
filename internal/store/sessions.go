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
}

const sessionCols = `id_hash, user_id, account_id, csrf_token, created_at, last_seen_at, expires_at,
	ip, user_agent`

// SessionByHash returns the session with the given cookie hash. Expired sessions are returned
// too; the caller checks ExpiresAt. It returns ErrNotFound if there is none.
func (s *Store) SessionByHash(ctx context.Context, hash []byte) (*Session, error) {
	var sess Session
	var created, seen, expires int64
	var acc sql.NullString
	err := s.r.QueryRowContext(ctx, "SELECT "+sessionCols+" FROM sessions WHERE id_hash = ?", hash).
		Scan(&sess.IDHash, &sess.UserID, &acc, &sess.CSRFToken, &created, &seen, &expires,
			&sess.IP, &sess.UserAgent)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sess.AccountID = acc.String
	sess.CreatedAt = fromMillis(created)
	sess.LastSeenAt = fromMillis(seen)
	sess.ExpiresAt = fromMillis(expires)
	return &sess, nil
}

// InsertSession adds sess.
func (tx *Tx) InsertSession(ctx context.Context, sess *Session) error {
	_, err := tx.q.ExecContext(ctx, "INSERT INTO sessions ("+sessionCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		sess.IDHash, sess.UserID, nullString(sess.AccountID), sess.CSRFToken, millis(sess.CreatedAt),
		millis(sess.LastSeenAt), millis(sess.ExpiresAt), sess.IP, sess.UserAgent)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
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
