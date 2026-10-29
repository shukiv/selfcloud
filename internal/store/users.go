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

// User statuses and roles. The role names match the users.role column.
const (
	UserPending  = "pending"
	UserActive   = "active"
	UserDisabled = "disabled"

	RoleAdmin  = "admin"
	RoleOwner  = "owner"
	RoleMember = "member"
)

// AdminScope is the account argument that selects installation admins in FindUsersByEmail.
// It cannot clash with an account ID, which always carries the acc_ prefix.
const AdminScope = "admin"

// Verification purposes.
const (
	PurposeVerify = "verify"
	PurposeReset  = "reset"
)

// User is a person who may sign in. A member or owner belongs to one account; an installation
// admin has no account, and its AccountID is "".
type User struct {
	ID           string
	AccountID    string // "" for an installation admin
	ExternalID   string // the client's own reference; "" when unset
	Email        string // as given, trimmed
	EmailKey     string // lowercased; unique within the account
	DisplayName  string
	Locale       string
	Role         string
	Status       string
	PasswordHash string // "" until the user sets a password
	VerifiedAt   *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

const userCols = `id, account_id, external_id, email, email_key, display_name, locale, role, status,
	password_hash, verified_at, created_at, updated_at`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var acc, ext, pw sql.NullString
	var verified sql.NullInt64
	var created, updated int64
	err := row.Scan(&u.ID, &acc, &ext, &u.Email, &u.EmailKey, &u.DisplayName, &u.Locale,
		&u.Role, &u.Status, &pw, &verified, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.AccountID = acc.String
	u.ExternalID = ext.String
	u.PasswordHash = pw.String
	u.VerifiedAt = fromNullMillis(verified)
	u.CreatedAt = fromMillis(created)
	u.UpdatedAt = fromMillis(updated)
	return &u, nil
}

func getUser(ctx context.Context, q querier, where string, args ...any) (*User, error) {
	return scanUser(q.QueryRowContext(ctx, "SELECT "+userCols+" FROM users WHERE "+where, args...))
}

// accountClause is the WHERE condition that selects the rows of accountID. An empty accountID
// selects the installation admins, whose account_id is NULL. Every account-scoped query goes
// through it, because "account_id = ”" would match nothing.
func accountClause(accountID string) (string, []any) {
	if accountID == "" {
		return "account_id IS NULL", nil
	}
	return "account_id = ?", []any{accountID}
}

// userIn returns the user of accountID that matches cond. A user of another account is
// ErrNotFound, so account boundaries hold even for callers who know an ID.
func userIn(ctx context.Context, q querier, accountID, cond string, args ...any) (*User, error) {
	clause, cargs := accountClause(accountID)
	return getUser(ctx, q, clause+" AND "+cond, append(cargs, args...)...)
}

// User returns a user of accountID ("" for an admin).
func (s *Store) User(ctx context.Context, accountID, id string) (*User, error) {
	return userIn(ctx, s.r, accountID, "id = ?", id)
}

// User returns a user of accountID ("" for an admin).
func (tx *Tx) User(ctx context.Context, accountID, id string) (*User, error) {
	return userIn(ctx, tx.q, accountID, "id = ?", id)
}

// UserByID returns the user with the given ID, in any account. Only code that already holds a
// token naming the user uses it: the token is the proof, not the account.
func (tx *Tx) UserByID(ctx context.Context, id string) (*User, error) {
	return getUser(ctx, tx.q, "id = ?", id)
}

// UserByEmail returns the user of accountID with the given email key.
func (tx *Tx) UserByEmail(ctx context.Context, accountID, emailKey string) (*User, error) {
	return userIn(ctx, tx.q, accountID, "email_key = ?", emailKey)
}

// UserByExternalID returns the user of accountID with the given external ID.
func (tx *Tx) UserByExternalID(ctx context.Context, accountID, externalID string) (*User, error) {
	return userIn(ctx, tx.q, accountID, "external_id = ?", externalID)
}

// FindUsersByEmail returns up to limit users with the given email key. scope "" finds them in
// any account, AdminScope only the installation admins, and any other value only in that
// account. It is the one lookup that crosses accounts, and it exists for sign-in only.
func (s *Store) FindUsersByEmail(ctx context.Context, emailKey, scope string, limit int) ([]*User, error) {
	query := "SELECT " + userCols + " FROM users WHERE email_key = ?"
	args := []any{emailKey}
	switch scope {
	case "":
	case AdminScope:
		query += " AND account_id IS NULL"
	default:
		query += " AND account_id = ?"
		args = append(args, scope)
	}
	query += " ORDER BY created_at, id LIMIT ?"
	args = append(args, limit)
	return s.queryUsers(ctx, query, args)
}

// UserFilter narrows a user list. Empty fields do not narrow.
type UserFilter struct {
	ExternalID string
	EmailKey   string
}

// Users lists the users of accountID, oldest first, narrowed by f.
func (s *Store) Users(ctx context.Context, accountID string, f UserFilter, p Page) ([]*User, string, error) {
	clause, cargs := accountClause(accountID)
	where := []string{clause}
	args := cargs
	if f.ExternalID != "" {
		where = append(where, "external_id = ?")
		args = append(args, f.ExternalID)
	}
	if f.EmailKey != "" {
		where = append(where, "email_key = ?")
		args = append(args, f.EmailKey)
	}
	if p.Cursor != "" {
		parts, err := decodeCursor(p.Cursor, 2)
		if err != nil {
			return nil, "", err
		}
		ms, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, "", ErrBadCursor
		}
		where = append(where, "(created_at, id) > (?, ?)")
		args = append(args, ms, parts[1])
	}
	args = append(args, p.Limit+1)
	out, err := s.queryUsers(ctx, "SELECT "+userCols+" FROM users WHERE "+strings.Join(where, " AND ")+
		" ORDER BY created_at, id LIMIT ?", args)
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

func (s *Store) queryUsers(ctx context.Context, query string, args []any) ([]*User, error) {
	rows, err := s.r.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// InsertUser adds u. It returns ErrConflict if the email or the external ID is taken in the
// account.
func (tx *Tx) InsertUser(ctx context.Context, u *User) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO users (`+userCols+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, nullString(u.AccountID), nullString(u.ExternalID), u.Email, u.EmailKey, u.DisplayName, u.Locale,
		u.Role, u.Status, nullString(u.PasswordHash), nullMillis(u.VerifiedAt), millis(u.CreatedAt),
		millis(u.UpdatedAt))
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

// UpdateUser writes the mutable fields of u: name, locale, role, external ID, status, password,
// verification time and update time. The email is fixed once the user exists. It returns
// ErrNotFound if the user is not in the account, and ErrConflict if the external ID is taken.
func (tx *Tx) UpdateUser(ctx context.Context, u *User) error {
	clause, cargs := accountClause(u.AccountID)
	args := append([]any{nullString(u.ExternalID), u.DisplayName, u.Locale, u.Role, u.Status,
		nullString(u.PasswordHash), nullMillis(u.VerifiedAt), millis(u.UpdatedAt), u.ID}, cargs...)
	err := rowsAffected(tx.q.ExecContext(ctx, `UPDATE users SET external_id = ?, display_name = ?,
		locale = ?, role = ?, status = ?, password_hash = ?, verified_at = ?, updated_at = ?
		WHERE id = ? AND `+clause, args...))
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

// DeleteUser removes a user of accountID ("" for an admin), with its sessions and tokens. It returns ErrNotFound
// if there is no such user.
func (tx *Tx) DeleteUser(ctx context.Context, accountID, id string) error {
	clause, cargs := accountClause(accountID)
	return rowsAffected(tx.q.ExecContext(ctx, "DELETE FROM users WHERE id = ? AND "+clause,
		append([]any{id}, cargs...)...))
}

// Verification is a single-use token that proves control of a user's email address: a
// verification link (purpose verify) or a password-reset link (purpose reset).
type Verification struct {
	ID        string
	UserID    string
	Purpose   string
	TokenHash []byte
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}

// InsertVerification adds v.
func (tx *Tx) InsertVerification(ctx context.Context, v *Verification) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO verifications
		(id, user_id, purpose, token_hash, expires_at, used_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		v.ID, v.UserID, v.Purpose, v.TokenHash, millis(v.ExpiresAt), nullMillis(v.UsedAt), millis(v.CreatedAt))
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

// VerificationByHash returns the verification with the given token hash. It returns
// ErrNotFound if there is none.
func (tx *Tx) VerificationByHash(ctx context.Context, hash []byte) (*Verification, error) {
	var v Verification
	var expires, created int64
	var used sql.NullInt64
	err := tx.q.QueryRowContext(ctx, `SELECT id, user_id, purpose, token_hash, expires_at, used_at, created_at
		FROM verifications WHERE token_hash = ?`, hash).
		Scan(&v.ID, &v.UserID, &v.Purpose, &v.TokenHash, &expires, &used, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	v.ExpiresAt = fromMillis(expires)
	v.UsedAt = fromNullMillis(used)
	v.CreatedAt = fromMillis(created)
	return &v, nil
}

// UseVerification marks the verification with the given ID used at the given time. A token
// that is already used is ErrNotFound, so two concurrent redemptions cannot both succeed.
func (tx *Tx) UseVerification(ctx context.Context, id string, at time.Time) error {
	return rowsAffected(tx.q.ExecContext(ctx,
		"UPDATE verifications SET used_at = ? WHERE id = ? AND used_at IS NULL", millis(at), id))
}

// DeleteVerifications removes the user's verifications of the given purpose, except keepID.
// An empty keepID removes all of them.
func (tx *Tx) DeleteVerifications(ctx context.Context, userID, purpose, keepID string) error {
	_, err := tx.q.ExecContext(ctx,
		"DELETE FROM verifications WHERE user_id = ? AND purpose = ? AND id != ?", userID, purpose, keepID)
	return err
}

// CountActiveAdmins returns how many active installation admins there are, not counting the one
// with excludeID. It guards the last admin from being removed.
func (tx *Tx) CountActiveAdmins(ctx context.Context, excludeID string) (int, error) {
	var n int
	err := tx.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM users
		WHERE account_id IS NULL AND role = 'admin' AND status = 'active' AND id != ?`, excludeID).Scan(&n)
	return n, err
}

// CountActiveOwners returns how many active owners accountID has, not counting the one with excludeID.
// It guards an account from losing its last active owner.
func (tx *Tx) CountActiveOwners(ctx context.Context, accountID, excludeID string) (int, error) {
	var n int
	err := tx.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM users
		WHERE account_id = ? AND role = 'owner' AND status = 'active' AND id != ?`, accountID, excludeID).Scan(&n)
	return n, err
}

// PeopleCounts returns how many active and pending users accountID has.
func (s *Store) PeopleCounts(ctx context.Context, accountID string) (active, pending int64, err error) {
	rows, err := s.r.QueryContext(ctx, `SELECT status, COUNT(*) FROM users WHERE account_id = ?
		AND status IN ('active', 'pending') GROUP BY status`, accountID)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var n int64
		if err := rows.Scan(&status, &n); err != nil {
			return 0, 0, err
		}
		if status == UserActive {
			active = n
		} else {
			pending = n
		}
	}
	return active, pending, rows.Err()
}

// RevokeUserTokens ends every delegated token of user id issued up to now. The user's cut-off moves
// to now, or stays where it is when it is already later, so it only moves forward: a revoke that
// commits after a later one keeps the later cut-off. It reads and writes inside tx and returns the
// cut-off now in force. It returns ErrNotFound when accountID has no such user.
func (tx *Tx) RevokeUserTokens(ctx context.Context, accountID, id string, now time.Time) (time.Time, error) {
	clause, cargs := accountClause(accountID)
	var cur int64
	err := tx.q.QueryRowContext(ctx, "SELECT tokens_not_before FROM users WHERE id = ? AND "+clause,
		append([]any{id}, cargs...)...).Scan(&cur)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, ErrNotFound
	}
	if err != nil {
		return time.Time{}, err
	}
	cut := now
	if fromMillis(cur).After(now) {
		cut = fromMillis(cur)
	}
	err = rowsAffected(tx.q.ExecContext(ctx, `UPDATE users SET tokens_not_before = ? WHERE id = ? AND `+clause,
		append([]any{millis(cut), id}, cargs...)...))
	if err != nil {
		return time.Time{}, err
	}
	return cut, nil
}
