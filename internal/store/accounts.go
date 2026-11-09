// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Account statuses.
const (
	AccountActive    = "active"
	AccountSuspended = "suspended"
	AccountDeleting  = "deleting"
)

// Account is a tenant: it owns users, folders and files, and has a storage quota.
type Account struct {
	ID         string
	ExternalID string // the client's own reference; "" when unset
	Name       string
	Status     string
	QuotaBytes int64 // 0 = unlimited
	// UserQuotaBytes is the limit each user gets when its own limit follows the account default; 0 = none.
	UserQuotaBytes  int64
	UsedBytes       int64
	UsedFiles       int64
	ReservedBytes   int64
	TokensNotBefore time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
	// UserCount is the number of the account's users. The account lists set it, counted in their own query (decision
	// 114); a single account's read leaves it nil.
	UserCount *int64
}

const accountCols = `id, external_id, name, status, quota_bytes, used_bytes, used_files,
	reserved_bytes, tokens_not_before, created_at, updated_at, user_quota_bytes`

// accountListSQL is the query of an account list: the accounts that where narrows to, in the order order, up to the
// LIMIT the last argument gives, each with its number of users (decision 114). The accounts are read, sorted and limited
// in a MATERIALIZED page first, and the users are counted in the query over that page, so the count runs for the page's
// rows only, not for every account the sort reads (the tl1 re-check: in name order it ran for all of them before the
// sort). The users_list index answers each count. The outer query keeps the page's order, and the page is one query, not
// one per account.
func accountListSQL(where, order string) string {
	return "WITH page AS MATERIALIZED (SELECT " + accountCols + " FROM accounts WHERE " + where +
		" ORDER BY " + order + " LIMIT ?) SELECT " + accountCols +
		", (SELECT COUNT(*) FROM users u WHERE u.account_id = page.id) AS user_count FROM page ORDER BY " + order
}

// scanListedAccount reads a row of accountListSQL.
func scanListedAccount(row interface{ Scan(...any) error }) (*Account, error) {
	var n int64
	a, err := scanAccount(row, &n)
	if err != nil {
		return nil, err
	}
	a.UserCount = &n
	return a, nil
}

// scanAccount reads a row of accountCols. extra are the destinations of the columns the query adds after them.
func scanAccount(row interface{ Scan(...any) error }, extra ...any) (*Account, error) {
	var a Account
	var ext sql.NullString
	var tnb, created, updated int64
	err := row.Scan(append([]any{&a.ID, &ext, &a.Name, &a.Status, &a.QuotaBytes, &a.UsedBytes, &a.UsedFiles,
		&a.ReservedBytes, &tnb, &created, &updated, &a.UserQuotaBytes}, extra...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.ExternalID = ext.String
	a.TokensNotBefore = fromMillis(tnb)
	a.CreatedAt = fromMillis(created)
	a.UpdatedAt = fromMillis(updated)
	return &a, nil
}

func getAccount(ctx context.Context, q querier, where string, arg any) (*Account, error) {
	return scanAccount(q.QueryRowContext(ctx, "SELECT "+accountCols+" FROM accounts WHERE "+where, arg))
}

// Account returns the account with the given ID.
func (s *Store) Account(ctx context.Context, id string) (*Account, error) {
	return getAccount(ctx, s.r, "id = ?", id)
}

// Account returns the account with the given ID.
func (tx *Tx) Account(ctx context.Context, id string) (*Account, error) {
	return getAccount(ctx, tx.q, "id = ?", id)
}

// AccountByExternalID returns the account with the given external ID.
func (tx *Tx) AccountByExternalID(ctx context.Context, externalID string) (*Account, error) {
	return getAccount(ctx, tx.q, "external_id = ?", externalID)
}

// Accounts lists accounts oldest first, optionally only the one with externalID.
func (s *Store) Accounts(ctx context.Context, externalID string, p Page) ([]*Account, string, error) {
	return s.AccountsFiltered(ctx, AccountFilter{ExternalID: externalID}, p)
}

// AccountFilter narrows an account list. Empty fields do not narrow.
type AccountFilter struct {
	ExternalID string
	// Query matches an account whose name contains it, or whose external ID is exactly it. The name match
	// folds ASCII case only, as SQLite's LIKE does, and the text is matched literally.
	Query  string
	Status string // AccountActive or AccountSuspended
}

// accountFilterWhere is the WHERE clause of f: its conditions joined by AND, and their arguments.
func accountFilterWhere(f AccountFilter) (string, []any) {
	where := []string{"1 = 1"}
	var args []any
	if f.ExternalID != "" {
		where = append(where, "external_id = ?")
		args = append(args, f.ExternalID)
	}
	if f.Query != "" {
		where = append(where, `(name LIKE ? ESCAPE '\' OR external_id = ?)`)
		args = append(args, "%"+likeEscaper.Replace(f.Query)+"%", f.Query)
	}
	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, f.Status)
	}
	return strings.Join(where, " AND "), args
}

// AccountSort orders an accounts list: by name, or by creation time (the order the list has always had). Desc reverses
// either. The zero value is creation time, oldest first.
type AccountSort struct {
	ByName bool
	Desc   bool
}

// AccountPos is the position of an account in a list sorted by an AccountSort: its name for a name sort, its creation
// time for a creation sort, and its ID, which breaks a tie.
type AccountPos struct {
	Name    string
	Created int64
	ID      string
}

// PosOf returns the position of a in the list sorted by s, for the next page to resume after.
func (s AccountSort) PosOf(a *Account) AccountPos {
	pos := AccountPos{ID: a.ID}
	if s.ByName {
		pos.Name = a.Name
	} else {
		pos.Created = millis(a.CreatedAt)
	}
	return pos
}

// AccountsSorted lists up to limit accounts that f narrows to, in the order order, after the position after (nil for the
// first page). more says whether an account follows the last one returned. The sort key is a fixed column, never caller
// text, so no value of a sort reaches the SQL as text.
func (s *Store) AccountsSorted(ctx context.Context, f AccountFilter, order AccountSort, limit int, after *AccountPos) (out []*Account, more bool, err error) {
	query, args := accountsSortedSQL(f, order, limit, after)
	out, err = s.listedAccounts(ctx, query, args)
	if err != nil {
		return nil, false, err
	}
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

// accountsSortedSQL is the query of AccountsSorted and its arguments. It reads one account more than limit, which says
// whether another page follows.
func accountsSortedSQL(f AccountFilter, order AccountSort, limit int, after *AccountPos) (string, []any) {
	where, args := accountFilterWhere(f)
	key, dir, op := "created_at", "ASC", ">"
	if order.ByName {
		key = "name COLLATE " + NameCollation
	}
	if order.Desc {
		dir, op = "DESC", "<"
	}
	if after != nil {
		if order.ByName {
			where += " AND (" + key + ", id) " + op + " (?, ?)"
			args = append(args, after.Name, after.ID)
		} else {
			where += " AND (created_at, id) " + op + " (?, ?)"
			args = append(args, after.Created, after.ID)
		}
	}
	args = append(args, limit+1)
	return accountListSQL(where, key+" "+dir+", id "+dir), args
}

// listedAccounts runs a query of an account list and reads its rows.
func (s *Store) listedAccounts(ctx context.Context, query string, args []any) ([]*Account, error) {
	rows, err := s.r.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Account
	for rows.Next() {
		a, err := scanListedAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// AccountsFiltered lists the accounts that f narrows to, oldest first, one page at a time.
func (s *Store) AccountsFiltered(ctx context.Context, f AccountFilter, p Page) ([]*Account, string, error) {
	query, args, err := accountsFilteredSQL(f, p)
	if err != nil {
		return nil, "", err
	}
	out, err := s.listedAccounts(ctx, query, args)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > p.Limit {
		out = out[:p.Limit]
		last := out[len(out)-1]
		next = encodeCursor(strconv.FormatInt(millis(last.CreatedAt), 10), last.ID)
	}
	return out, next, nil
}

// accountsFilteredSQL is the query of AccountsFiltered and its arguments, oldest first, resuming after p's cursor. It reads
// one account more than the page, which says whether another page follows.
func accountsFilteredSQL(f AccountFilter, p Page) (string, []any, error) {
	where, args := accountFilterWhere(f)
	if p.Cursor != "" {
		parts, err := decodeCursor(p.Cursor, 2)
		if err != nil {
			return "", nil, err
		}
		ms, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return "", nil, ErrBadCursor
		}
		where += " AND (created_at, id) > (?, ?)"
		args = append(args, ms, parts[1])
	}
	args = append(args, p.Limit+1)
	return accountListSQL(where, "created_at, id"), args, nil
}

// InsertAccount adds a. It returns ErrConflict if the external ID is taken.
func (tx *Tx) InsertAccount(ctx context.Context, a *Account) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO accounts (`+accountCols+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, nullString(a.ExternalID), a.Name, a.Status, a.QuotaBytes, a.UsedBytes, a.UsedFiles,
		a.ReservedBytes, millis(a.TokensNotBefore), millis(a.CreatedAt), millis(a.UpdatedAt), a.UserQuotaBytes)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

// UpdateAccount writes a's name, external ID, status, quota and token cut-off. Usage counters
// are not touched; they change only alongside the files they count.
func (tx *Tx) UpdateAccount(ctx context.Context, a *Account) error {
	// A changed quota clears the account's quota marks, so the next change above a level is announced (webhook_quota.go).
	var old int64
	if err := tx.q.QueryRowContext(ctx, "SELECT quota_bytes FROM accounts WHERE id = ?", a.ID).Scan(&old); err != nil &&
		!errors.Is(err, sql.ErrNoRows) {
		return err
	}
	err := rowsAffected(tx.q.ExecContext(ctx, `UPDATE accounts SET external_id = ?, name = ?,
		status = ?, quota_bytes = ?, tokens_not_before = ?, updated_at = ? WHERE id = ?`,
		nullString(a.ExternalID), a.Name, a.Status, a.QuotaBytes, millis(a.TokensNotBefore),
		millis(a.UpdatedAt), a.ID))
	if isUniqueViolation(err) {
		return ErrConflict
	}
	if err != nil || old == a.QuotaBytes {
		return err
	}
	return tx.forgetAccountQuotaMarks(ctx, a.ID)
}

// DeleteAccount removes the account and, by cascade, everything that belongs to it in the
// database. Its office locks and editing sessions are cleared first, in this write, so an account
// with a file open in an editor is deleted at once (decision 32). Deleting an account that is
// already gone is not an error.
func (tx *Tx) DeleteAccount(ctx context.Context, id string) error {
	if _, err := tx.q.ExecContext(ctx, "DELETE FROM wopi_locks WHERE account_id = ?", id); err != nil {
		return err
	}
	if _, err := tx.q.ExecContext(ctx, "DELETE FROM wopi_sessions WHERE account_id = ?", id); err != nil {
		return err
	}
	// The account's search rows go by cascade, but their full-text rows do not, so they go first (decision 40).
	if err := tx.dropSearchAccount(ctx, id); err != nil {
		return err
	}
	_, err := tx.q.ExecContext(ctx, "DELETE FROM accounts WHERE id = ?", id)
	return err
}
