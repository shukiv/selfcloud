// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"
)

// Group is a named set of users of one account. A permission may name a group, and then every
// member holds it.
type Group struct {
	ID         string
	AccountID  string
	ExternalID string // the client's own reference; "" when unset
	Name       string // NFC
	NameKey    string // case-folded NFC; unique within the account
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

const groupCols = "id, account_id, external_id, name, name_key, created_at, updated_at"

func scanGroup(row interface{ Scan(...any) error }) (*Group, error) {
	var g Group
	var ext sql.NullString
	var created, updated int64
	err := row.Scan(&g.ID, &g.AccountID, &ext, &g.Name, &g.NameKey, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	g.ExternalID = ext.String
	g.CreatedAt = fromMillis(created)
	g.UpdatedAt = fromMillis(updated)
	return &g, nil
}

// Group returns the group of accountID with the given ID. A group of another account is ErrNotFound.
func (s *Store) Group(ctx context.Context, accountID, id string) (*Group, error) {
	return scanGroup(s.r.QueryRowContext(ctx,
		"SELECT "+groupCols+" FROM groups WHERE account_id = ? AND id = ?", accountID, id))
}

// Group returns the group of accountID with the given ID, read inside the transaction.
func (tx *Tx) Group(ctx context.Context, accountID, id string) (*Group, error) {
	return scanGroup(tx.q.QueryRowContext(ctx,
		"SELECT "+groupCols+" FROM groups WHERE account_id = ? AND id = ?", accountID, id))
}

// GroupByNameKey returns the group of accountID with the given name key.
func (tx *Tx) GroupByNameKey(ctx context.Context, accountID, nameKey string) (*Group, error) {
	return scanGroup(tx.q.QueryRowContext(ctx,
		"SELECT "+groupCols+" FROM groups WHERE account_id = ? AND name_key = ?", accountID, nameKey))
}

// GroupByExternalID returns the group of accountID with the given external ID.
func (tx *Tx) GroupByExternalID(ctx context.Context, accountID, externalID string) (*Group, error) {
	return scanGroup(tx.q.QueryRowContext(ctx,
		"SELECT "+groupCols+" FROM groups WHERE account_id = ? AND external_id = ?", accountID, externalID))
}

// Groups lists the groups of accountID, oldest first, one page at a time.
func (s *Store) Groups(ctx context.Context, accountID string, p Page) ([]*Group, string, error) {
	where := "account_id = ?"
	args := []any{accountID}
	if p.Cursor != "" {
		parts, err := decodeCursor(p.Cursor, 2)
		if err != nil {
			return nil, "", err
		}
		ms, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, "", ErrBadCursor
		}
		where += " AND (created_at, id) > (?, ?)"
		args = append(args, ms, parts[1])
	}
	args = append(args, p.Limit+1)
	rows, err := s.r.QueryContext(ctx, "SELECT "+groupCols+" FROM groups WHERE "+where+
		" ORDER BY created_at, id LIMIT ?", args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []*Group
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
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

// InsertGroup adds g. It returns ErrConflict if the name key or the external ID is taken in the
// account.
func (tx *Tx) InsertGroup(ctx context.Context, g *Group) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO groups (`+groupCols+`) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		g.ID, g.AccountID, nullString(g.ExternalID), g.Name, g.NameKey, millis(g.CreatedAt), millis(g.UpdatedAt))
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

// UpdateGroup writes the name, external ID and update time of g. It returns ErrNotFound if the group
// is not in the account, and ErrConflict if the name or external ID is taken.
func (tx *Tx) UpdateGroup(ctx context.Context, g *Group) error {
	err := rowsAffected(tx.q.ExecContext(ctx, `UPDATE groups SET name = ?, name_key = ?, external_id = ?,
		updated_at = ? WHERE account_id = ? AND id = ?`,
		g.Name, g.NameKey, nullString(g.ExternalID), millis(g.UpdatedAt), g.AccountID, g.ID))
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

// DeleteGroup removes a group of accountID with its members. The caller removes the group's
// permissions in the same transaction, since subject_id is not a foreign key.
func (tx *Tx) DeleteGroup(ctx context.Context, accountID, id string) error {
	return rowsAffected(tx.q.ExecContext(ctx, "DELETE FROM groups WHERE account_id = ? AND id = ?", accountID, id))
}

// GroupMember is a user in a group, with when they joined.
type GroupMember struct {
	GroupID   string
	UserID    string
	CreatedAt time.Time
}

// AddGroupMember puts a user in a group. A user already in it is left as it is. The caller checks
// that both belong to the account.
func (tx *Tx) AddGroupMember(ctx context.Context, m *GroupMember) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO group_members (group_id, user_id, created_at) VALUES (?, ?, ?)
		ON CONFLICT (group_id, user_id) DO NOTHING`, m.GroupID, m.UserID, millis(m.CreatedAt))
	return err
}

// RemoveGroupMember takes a user out of a group. It returns ErrNotFound if the user is not in it.
func (tx *Tx) RemoveGroupMember(ctx context.Context, groupID, userID string) error {
	return rowsAffected(tx.q.ExecContext(ctx,
		"DELETE FROM group_members WHERE group_id = ? AND user_id = ?", groupID, userID))
}

// GroupMemberOf returns the membership of a user in a group, read inside the transaction. It
// returns ErrNotFound if the user is not in the group.
func (tx *Tx) GroupMemberOf(ctx context.Context, groupID, userID string) (*GroupMember, error) {
	var m GroupMember
	var created int64
	err := tx.q.QueryRowContext(ctx, "SELECT group_id, user_id, created_at FROM group_members WHERE group_id = ? AND user_id = ?",
		groupID, userID).Scan(&m.GroupID, &m.UserID, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	m.CreatedAt = fromMillis(created)
	return &m, nil
}

// GroupMembers lists the members of a group, earliest first, one page at a time.
func (s *Store) GroupMembers(ctx context.Context, groupID string, p Page) ([]*GroupMember, string, error) {
	where := "group_id = ?"
	args := []any{groupID}
	if p.Cursor != "" {
		parts, err := decodeCursor(p.Cursor, 2)
		if err != nil {
			return nil, "", err
		}
		ms, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, "", ErrBadCursor
		}
		where += " AND (created_at, user_id) > (?, ?)"
		args = append(args, ms, parts[1])
	}
	args = append(args, p.Limit+1)
	rows, err := s.r.QueryContext(ctx, "SELECT group_id, user_id, created_at FROM group_members WHERE "+where+
		" ORDER BY created_at, user_id LIMIT ?", args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []*GroupMember
	for rows.Next() {
		var m GroupMember
		var created int64
		if err := rows.Scan(&m.GroupID, &m.UserID, &created); err != nil {
			return nil, "", err
		}
		m.CreatedAt = fromMillis(created)
		out = append(out, &m)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > p.Limit {
		out = out[:p.Limit]
		last := out[len(out)-1]
		next = encodeCursor(strconv.FormatInt(millis(last.CreatedAt), 10), last.UserID)
	}
	return out, next, nil
}

// DeleteUserMemberships removes a user from every group and returns how many groups that was. It
// runs when a user is deleted; the foreign key would do the same, and the count is for the audit.
func (tx *Tx) DeleteUserMemberships(ctx context.Context, userID string) (int64, error) {
	res, err := tx.q.ExecContext(ctx, "DELETE FROM group_members WHERE user_id = ?", userID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
