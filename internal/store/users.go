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

	RoleOwner  = "owner"
	RoleMember = "member"
)

// Verification purposes.
const (
	PurposeVerify = "verify"
	PurposeReset  = "reset"
)

// User is a person who belongs to one account and may sign in.
type User struct {
	ID           string
	AccountID    string
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
	var ext, pw sql.NullString
	var verified sql.NullInt64
	var created, updated int64
	err := row.Scan(&u.ID, &u.AccountID, &ext, &u.Email, &u.EmailKey, &u.DisplayName, &u.Locale,
		&u.Role, &u.Status, &pw, &verified, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
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

// User returns a user of accountID. A user of another account is ErrNotFound, so account
// boundaries hold even for callers who know an ID.
func (s *Store) User(ctx context.Context, accountID, id string) (*User, error) {
	return getUser(ctx, s.r, "account_id = ? AND id = ?", accountID, id)
}

// User returns a user of accountID.
func (tx *Tx) User(ctx context.Context, accountID, id string) (*User, error) {
	return getUser(ctx, tx.q, "account_id = ? AND id = ?", accountID, id)
}

// UserByID returns the user with the given ID, in any account. Only code that already holds a
// token naming the user uses it: the token is the proof, not the account.
func (tx *Tx) UserByID(ctx context.Context, id string) (*User, error) {
	return getUser(ctx, tx.q, "id = ?", id)
}

// UserByEmail returns the user of accountID with the given email key.
func (tx *Tx) UserByEmail(ctx context.Context, accountID, emailKey string) (*User, error) {
	return getUser(ctx, tx.q, "account_id = ? AND email_key = ?", accountID, emailKey)
}

// UserByExternalID returns the user of accountID with the given external ID.
func (tx *Tx) UserByExternalID(ctx context.Context, accountID, externalID string) (*User, error) {
	return getUser(ctx, tx.q, "account_id = ? AND external_id = ?", accountID, externalID)
}

// FindUsersByEmail returns up to limit users with the given email key, in any account, or only
// in accountID when it is not "". It is the one lookup that crosses accounts, and it exists for
// sign-in only.
func (s *Store) FindUsersByEmail(ctx context.Context, emailKey, accountID string, limit int) ([]*User, error) {
	query := "SELECT " + userCols + " FROM users WHERE email_key = ?"
	args := []any{emailKey}
	if accountID != "" {
		query += " AND account_id = ?"
		args = append(args, accountID)
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
	where := []string{"account_id = ?"}
	args := []any{accountID}
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
		u.ID, u.AccountID, nullString(u.ExternalID), u.Email, u.EmailKey, u.DisplayName, u.Locale,
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
	err := rowsAffected(tx.q.ExecContext(ctx, `UPDATE users SET external_id = ?, display_name = ?,
		locale = ?, role = ?, status = ?, password_hash = ?, verified_at = ?, updated_at = ?
		WHERE account_id = ? AND id = ?`,
		nullString(u.ExternalID), u.DisplayName, u.Locale, u.Role, u.Status, nullString(u.PasswordHash),
		nullMillis(u.VerifiedAt), millis(u.UpdatedAt), u.AccountID, u.ID))
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

// DeleteUser removes a user of accountID, with its sessions and tokens. It returns ErrNotFound
// if there is no such user.
func (tx *Tx) DeleteUser(ctx context.Context, accountID, id string) error {
	return rowsAffected(tx.q.ExecContext(ctx, "DELETE FROM users WHERE account_id = ? AND id = ?",
		accountID, id))
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
