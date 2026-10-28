// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Roles, lowest first. A role includes every role before it: a commenter can do what a viewer can,
// and so on up to a manager. The API spells them in lower case.
const (
	RoleViewer      = "viewer"
	RoleCommenter   = "commenter"
	RoleContributor = "contributor"
	RoleEditor      = "editor"
	RoleManager     = "manager"
)

// Subject kinds for a permission.
const (
	SubjectUser  = "user"
	SubjectGroup = "group"
)

// roleRankSQL maps the role column to its rank, so MAX picks the highest role. The ranks match
// RoleRank; a row with an unknown role ranks 0 and grants nothing.
const roleRankSQL = `CASE %s WHEN 'viewer' THEN 1 WHEN 'commenter' THEN 2 WHEN 'contributor' THEN 3
	WHEN 'editor' THEN 4 WHEN 'manager' THEN 5 ELSE 0 END`

// RoleRank returns how far up the ladder role is: 0 for "" and any unknown role, 1 for viewer up to
// 5 for manager. Compare roles with it, never as strings.
func RoleRank(role string) int {
	switch role {
	case RoleViewer:
		return 1
	case RoleCommenter:
		return 2
	case RoleContributor:
		return 3
	case RoleEditor:
		return 4
	case RoleManager:
		return 5
	}
	return 0
}

// ValidRole reports whether role is one of the five roles.
func ValidRole(role string) bool { return RoleRank(role) > 0 }

// RoleOfRank is the role with the given rank, or "" for 0.
func RoleOfRank(rank int) string {
	for _, r := range []string{RoleViewer, RoleCommenter, RoleContributor, RoleEditor, RoleManager} {
		if RoleRank(r) == rank {
			return r
		}
	}
	return ""
}

// Permission gives a user or a group a role on one node. It reaches the node and everything below
// it. ExpiresAt, when set, ends it: an expired permission grants nothing.
type Permission struct {
	NodeID      string
	AccountID   string
	SubjectKind string // SubjectUser or SubjectGroup
	SubjectID   string
	Role        string
	CreatedAt   time.Time
	CreatedBy   string // the ID of the caller who made or last changed it
	ExpiresAt   *time.Time
}

const permissionCols = "node_id, account_id, subject_kind, subject_id, role, created_at, created_by, expires_at"

func scanPermission(row interface{ Scan(...any) error }) (*Permission, error) {
	var p Permission
	var created int64
	var expires sql.NullInt64
	err := row.Scan(&p.NodeID, &p.AccountID, &p.SubjectKind, &p.SubjectID, &p.Role, &created, &p.CreatedBy, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.CreatedAt = fromMillis(created)
	p.ExpiresAt = fromNullMillis(expires)
	return &p, nil
}

// subjectPredicate matches the permissions of a user: those naming the user, and those naming a
// group the user is in, of the same account. It takes two arguments, the user ID twice, and reads
// permission rows under the alias pm.
const subjectPredicate = `((pm.subject_kind = 'user' AND pm.subject_id = ?)
	OR (pm.subject_kind = 'group' AND pm.subject_id IN (
		SELECT gm.group_id FROM group_members gm JOIN groups g ON g.id = gm.group_id
		WHERE gm.user_id = ? AND g.account_id = pm.account_id)))`

// liveClause keeps the permissions that have not expired. It takes the current time in Unix
// milliseconds.
const liveClause = `(pm.expires_at IS NULL OR pm.expires_at > ?)`

// effectiveRank is the highest rank a user holds on a node through permissions: the max over the
// node and each of its ancestors, root included, of the permissions the user holds directly or
// through a group. Nodes have no stored path, so the ancestors come from a recursive walk up
// parent_id. Implicit rules (owner and manager of a personal folder) are not here; the access code
// adds them.
func effectiveRank(ctx context.Context, q querier, accountID, nodeID, userID string) (int, error) {
	if userID == "" {
		return 0, nil
	}
	var rank int
	err := q.QueryRowContext(ctx, `WITH RECURSIVE chain(id, parent) AS (
			SELECT id, parent_id FROM nodes WHERE account_id = ? AND id = ?
			UNION ALL
			SELECT p.id, p.parent_id FROM nodes p JOIN chain c ON p.id = c.parent WHERE p.account_id = ?
		)
		SELECT COALESCE(MAX(`+fmt.Sprintf(roleRankSQL, "pm.role")+`), 0)
		FROM chain c JOIN permissions pm ON pm.node_id = c.id AND pm.account_id = ?
		WHERE `+subjectPredicate+` AND `+liveClause,
		accountID, nodeID, accountID, accountID, userID, userID, nowMillis()).Scan(&rank)
	return rank, err
}

// nowMillis is the current time in the form the expiry column stores.
func nowMillis() int64 { return time.Now().UnixMilli() }

// EffectiveRank returns the rank of the highest permission userID holds on nodeID of accountID,
// through its own ancestry, or 0 for none. Read with the store, outside a transaction.
func (s *Store) EffectiveRank(ctx context.Context, accountID, nodeID, userID string) (int, error) {
	return effectiveRank(ctx, s.r, accountID, nodeID, userID)
}

// EffectiveRank is Store.EffectiveRank read inside the transaction, so a check made there sees the
// permissions and group memberships as they are then.
func (tx *Tx) EffectiveRank(ctx context.Context, accountID, nodeID, userID string) (int, error) {
	return effectiveRank(ctx, tx.q, accountID, nodeID, userID)
}

// IsWithin reports whether node id of accountID is ancestor itself or lies below it. It is the
// store's read, outside a transaction.
func (s *Store) IsWithin(ctx context.Context, accountID, id, ancestor string) (bool, error) {
	return isWithin(ctx, s.r, accountID, id, ancestor)
}

// ChildRanks returns, for the children of parentID that hold a permission of their own, the rank
// of the highest permission userID holds on each, direct or through a group. Children without one
// are absent. A listing takes it once for the whole directory, then takes the parent's rank against
// each child's own.
func (s *Store) ChildRanks(ctx context.Context, accountID, parentID, userID string) (map[string]int, error) {
	if userID == "" {
		return map[string]int{}, nil
	}
	rows, err := s.r.QueryContext(ctx, `SELECT pm.node_id, MAX(`+fmt.Sprintf(roleRankSQL, "pm.role")+`)
		FROM permissions pm JOIN nodes c ON c.id = pm.node_id
		WHERE c.account_id = ? AND c.parent_id = ? AND `+subjectPredicate+` AND `+liveClause+`
		GROUP BY pm.node_id`,
		accountID, parentID, userID, userID, nowMillis())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var rank int
		if err := rows.Scan(&id, &rank); err != nil {
			return nil, err
		}
		out[id] = rank
	}
	return out, rows.Err()
}

// ChainPermissions lists the live permissions on nodeID and on each of its ancestors, the node's own
// first and the root last. The caller tells direct from inherited by comparing NodeID. With userID
// set, only the permissions that user holds, directly or through a group, are listed.
func (s *Store) ChainPermissions(ctx context.Context, accountID, nodeID, userID string) ([]*Permission, error) {
	cond := ""
	args := []any{accountID, nodeID, accountID, accountID, nowMillis()}
	if userID != "" {
		cond = " AND " + subjectPredicate
		args = append(args, userID, userID)
	}
	rows, err := s.r.QueryContext(ctx, `WITH RECURSIVE chain(id, parent, depth) AS (
			SELECT id, parent_id, 0 FROM nodes WHERE account_id = ? AND id = ?
			UNION ALL
			SELECT p.id, p.parent_id, c.depth + 1 FROM nodes p JOIN chain c ON p.id = c.parent WHERE p.account_id = ?
		)
		SELECT `+prefixed("pm.", permissionCols)+` FROM chain c
		JOIN permissions pm ON pm.node_id = c.id AND pm.account_id = ?
		WHERE (pm.expires_at IS NULL OR pm.expires_at > ?)`+cond+`
		ORDER BY c.depth, pm.subject_kind, pm.subject_id`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Permission
	for rows.Next() {
		p, err := scanPermission(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// prefixed puts the table alias in front of each column of a comma-separated column list.
func prefixed(alias, cols string) string {
	parts := strings.Split(cols, ", ")
	for i, c := range parts {
		parts[i] = alias + c
	}
	return strings.Join(parts, ", ")
}

// SetPermission creates the permission or replaces its role, expiry and last changer; the time it
// was first given stays. The caller checks the subject and the role first.
func (tx *Tx) SetPermission(ctx context.Context, p *Permission) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO permissions (`+permissionCols+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (node_id, subject_kind, subject_id) DO UPDATE SET role = excluded.role,
			expires_at = excluded.expires_at, created_by = excluded.created_by`,
		p.NodeID, p.AccountID, p.SubjectKind, p.SubjectID, p.Role, millis(p.CreatedAt), p.CreatedBy,
		nullMillis(p.ExpiresAt))
	return err
}

// DeletePermission removes one permission. It returns ErrNotFound if there is none.
func (tx *Tx) DeletePermission(ctx context.Context, nodeID, subjectKind, subjectID string) error {
	return rowsAffected(tx.q.ExecContext(ctx,
		"DELETE FROM permissions WHERE node_id = ? AND subject_kind = ? AND subject_id = ?",
		nodeID, subjectKind, subjectID))
}

// DeleteUserTeamPermissions removes the permissions a user holds on nodes of team spaces, and returns
// how many. A user who stops being a member loses these, the shares the grants routes made. Its
// permissions on personal folders are kept, since a share there is the owner's own choice.
func (tx *Tx) DeleteUserTeamPermissions(ctx context.Context, accountID, userID string) (int64, error) {
	res, err := tx.q.ExecContext(ctx, `DELETE FROM permissions WHERE account_id = ? AND subject_kind = 'user'
		AND subject_id = ? AND node_id IN (SELECT n.id FROM nodes n JOIN folders f ON f.id = n.folder_id
			WHERE f.kind = 'team')`, accountID, userID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteSubjectPermissions removes every permission of one subject in accountID and returns how
// many. A user or group is deleted with its permissions, since subject_id is not a foreign key.
func (tx *Tx) DeleteSubjectPermissions(ctx context.Context, accountID, subjectKind, subjectID string) (int64, error) {
	res, err := tx.q.ExecContext(ctx,
		"DELETE FROM permissions WHERE account_id = ? AND subject_kind = ? AND subject_id = ?",
		accountID, subjectKind, subjectID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RootGrant is a user's permission on the root of a team space, seen through the grants routes.
type RootGrant struct {
	FolderID  string
	UserID    string
	Role      string
	CreatedAt time.Time
	CreatedBy string
}

// UserRootGrants lists the permissions a user holds on the roots of team spaces in accountID, oldest
// first, with the folder each one is on. It backs the grants routes, which show a root permission as
// a grant. A personal folder is never listed, since grants never applied to one.
func (s *Store) UserRootGrants(ctx context.Context, accountID, userID string) ([]RootGrant, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT n.folder_id, pm.subject_id, pm.role, pm.created_at, pm.created_by
		FROM permissions pm JOIN nodes n ON n.id = pm.node_id JOIN folders f ON f.id = n.folder_id
		WHERE pm.account_id = ? AND n.parent_id IS NULL AND f.kind = 'team'
		AND pm.subject_kind = 'user' AND pm.subject_id = ?
		AND (pm.expires_at IS NULL OR pm.expires_at > ?)
		ORDER BY pm.created_at, n.folder_id`, accountID, userID, nowMillis())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RootGrant
	for rows.Next() {
		var g RootGrant
		var created int64
		if err := rows.Scan(&g.FolderID, &g.UserID, &g.Role, &created, &g.CreatedBy); err != nil {
			return nil, err
		}
		g.CreatedAt = fromMillis(created)
		out = append(out, g)
	}
	return out, rows.Err()
}

// NodePermissions lists the live permissions held directly on one node, oldest first.
func (s *Store) NodePermissions(ctx context.Context, accountID, nodeID string) ([]*Permission, error) {
	return s.queryPermissions(ctx, "SELECT "+permissionCols+" FROM permissions WHERE account_id = ? AND node_id = ? "+
		"AND (expires_at IS NULL OR expires_at > ?) ORDER BY created_at, subject_kind, subject_id",
		accountID, nodeID, nowMillis())
}

func (s *Store) queryPermissions(ctx context.Context, query string, args ...any) ([]*Permission, error) {
	rows, err := s.r.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Permission
	for rows.Next() {
		p, err := scanPermission(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SharedNode is a node that carries a permission for a user, directly or through a group, with the
// highest rank of those permissions on it.
type SharedNode struct {
	NodeID string
	Rank   int
}

// SharedNodes lists the nodes of accountID that userID holds a permission on, one page at a time,
// in node ID order. It leaves out the user's own personal folder, the team spaces a user reaches at
// the root (every team space when owner is set), and any node whose folder root the user reaches
// through a permission of its own. Those nodes are reached already, so listing them again would
// only repeat them.
func (s *Store) SharedNodes(ctx context.Context, accountID, userID string, owner bool, p Page) ([]SharedNode, string, error) {
	where := []string{"pm.account_id = ?", subjectPredicate, liveClause,
		"NOT (f.kind = 'personal' AND f.owner_user_id = ?)",
		`NOT EXISTS (SELECT 1 FROM permissions rp JOIN nodes rn ON rn.id = rp.node_id
			WHERE rn.folder_id = n.folder_id AND rn.parent_id IS NULL AND rp.account_id = pm.account_id
			AND ((rp.subject_kind = 'user' AND rp.subject_id = ?)
				OR (rp.subject_kind = 'group' AND rp.subject_id IN (
					SELECT gm.group_id FROM group_members gm JOIN groups g ON g.id = gm.group_id
					WHERE gm.user_id = ? AND g.account_id = pm.account_id)))
			AND (rp.expires_at IS NULL OR rp.expires_at > ?))`}
	args := []any{accountID, userID, userID, nowMillis(), userID, userID, userID, nowMillis()}
	if owner {
		where = append(where, "f.kind <> 'team'")
	}
	if p.Cursor != "" {
		parts, err := decodeCursor(p.Cursor, 1)
		if err != nil {
			return nil, "", err
		}
		where = append(where, "pm.node_id > ?")
		args = append(args, parts[0])
	}
	args = append(args, p.Limit+1)
	rows, err := s.r.QueryContext(ctx, `SELECT pm.node_id, MAX(`+fmt.Sprintf(roleRankSQL, "pm.role")+`)
		FROM permissions pm
		JOIN nodes n ON n.id = pm.node_id
		JOIN folders f ON f.id = n.folder_id
		WHERE `+strings.Join(where, " AND ")+`
		GROUP BY pm.node_id ORDER BY pm.node_id LIMIT ?`, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []SharedNode
	for rows.Next() {
		var sn SharedNode
		if err := rows.Scan(&sn.NodeID, &sn.Rank); err != nil {
			return nil, "", err
		}
		out = append(out, sn)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > p.Limit {
		out = out[:p.Limit]
		next = encodeCursor(out[len(out)-1].NodeID)
	}
	return out, next, nil
}
