// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Errors callers act on.
var (
	ErrNotFound  = errors.New("store: not found")
	ErrConflict  = errors.New("store: conflict")
	ErrBadCursor = errors.New("store: invalid cursor")
)

// querier is what both the read pool and a transaction offer, so read functions serve both.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Tx is a write transaction. Everything a change needs (the change itself, its audit entry,
// any job it starts) goes in one Tx so they commit or fail together.
type Tx struct {
	q   *sql.Tx
	log *slog.Logger
	// fanouts holds, per account, what the transaction emits events to (webhook_events.go).
	fanouts map[string]*webhookFanout
	// quota holds the accounts whose usage this transaction changed, for the quota check before commit (webhook_quota.go).
	quota map[string]*quotaTouch
	// now is the store's clock, read for lock expiry (wopi_locks.go). Set by Write.
	now func() time.Time
}

// clock returns the time of the transaction: the store's clock, which tests replace.
func (tx *Tx) clock() time.Time {
	if tx.now == nil {
		return Now()
	}
	return tx.now()
}

// Clock returns the time the store reads for lock expiry. Production keeps Now; a test replaces it with SetClock.
func (s *Store) Clock() time.Time {
	if s.clock == nil {
		return Now()
	}
	return s.clock()
}

// SetClock replaces the store's clock. Tests use it to run locks and their expiry on a fake clock. It is not for
// use while the store serves requests.
func (s *Store) SetClock(fn func() time.Time) { s.clock = fn }

// Write runs fn in a write transaction and commits if fn returns nil. Errors from fn are
// returned unchanged. Before the commit, the quota levels of the accounts fn changed are checked on the state it left.
func (s *Store) Write(ctx context.Context, fn func(tx *Tx) error) error {
	return s.write(ctx, func(t *sql.Tx) error {
		tx := &Tx{q: t, log: s.log, now: s.Clock}
		if err := fn(tx); err != nil {
			return err
		}
		return tx.settleQuotas(ctx)
	})
}

// Page selects one page of a list.
type Page struct {
	Limit  int    // at least 1
	Cursor string // from the previous page; empty for the first
}

func encodeCursor(parts ...string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strings.Join(parts, "\x00")))
}

func decodeCursor(c string, n int) ([]string, error) {
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return nil, ErrBadCursor
	}
	parts := strings.Split(string(b), "\x00")
	if len(parts) != n {
		return nil, ErrBadCursor
	}
	return parts, nil
}

func isUniqueViolation(err error) bool {
	var se *sqlite.Error
	if errors.As(err, &se) {
		code := se.Code()
		return code == sqlite3.SQLITE_CONSTRAINT_UNIQUE || code == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
	}
	return false
}

// Now returns the current time at the precision the store keeps (milliseconds, UTC), so a
// value written and read back compares equal.
func Now() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }

func millis(t time.Time) int64 { return t.UnixMilli() }

func fromMillis(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

func nullMillis(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixMilli()
}

func fromNullMillis(n sql.NullInt64) *time.Time {
	if !n.Valid {
		return nil
	}
	t := fromMillis(n.Int64)
	return &t
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// rowsAffected returns ErrNotFound when a statement changed nothing.
func rowsAffected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
