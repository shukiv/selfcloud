// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ExternalPermission is a grant of a node to an email address of another account (decision 187). It
// names an address, not a user: the address may hold no user anywhere, and the grant is the same
// either way. The role is viewer only. AccountID is the sharer's account, the node's own.
type ExternalPermission struct {
	NodeID    string
	AccountID string
	Email     string
	EmailKey  string
	Role      string
	CreatedBy string
	CreatedAt time.Time
	ExpiresAt *time.Time
}

// scanExternalPermission reads a grant's columns, in the order externalGrantCols names them.
func scanExternalPermission(row interface{ Scan(...any) error }) (*ExternalPermission, error) {
	var p ExternalPermission
	var created int64
	var expires sql.NullInt64
	err := row.Scan(&p.NodeID, &p.AccountID, &p.Email, &p.EmailKey, &p.Role, &p.CreatedBy, &created, &expires)
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

const externalGrantCols = `node_id, account_id, email, email_key, role, created_by, created_at, expires_at`

// SetExternalPermission writes a grant, or updates the one the node already holds for the address.
// An update keeps who made the grant and when: the grant's origin does not move with its expiry.
func (tx *Tx) SetExternalPermission(ctx context.Context, p *ExternalPermission) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO external_permissions
		(node_id, account_id, email, email_key, role, created_by, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (node_id, email_key) DO UPDATE SET
			email = excluded.email, role = excluded.role, expires_at = excluded.expires_at`,
		p.NodeID, p.AccountID, p.Email, p.EmailKey, p.Role, p.CreatedBy, millis(p.CreatedAt), nullMillis(p.ExpiresAt))
	return err
}

// RemoveExternalPermission removes the grant the node holds for the address key. It returns
// ErrNotFound if there is none.
func (tx *Tx) RemoveExternalPermission(ctx context.Context, nodeID, emailKey string) error {
	return rowsAffected(tx.q.ExecContext(ctx,
		"DELETE FROM external_permissions WHERE node_id = ? AND email_key = ?", nodeID, emailKey))
}

// ExternalPermissions lists the grants of a node of accountID, oldest first. A node of another
// account lists nothing.
func (s *Store) ExternalPermissions(ctx context.Context, accountID, nodeID string) ([]*ExternalPermission, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+externalGrantCols+` FROM external_permissions
		WHERE node_id = ? AND account_id = ? ORDER BY created_at, email_key`, nodeID, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ExternalPermission{}
	for rows.Next() {
		p, err := scanExternalPermission(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CountExternalPermissions counts the grants a node holds, whatever their expiry.
func (s *Store) CountExternalPermissions(ctx context.Context, nodeID string) (int, error) {
	return countExternalPermissions(ctx, s.r, nodeID)
}

// CountExternalPermissions is Store.CountExternalPermissions read inside the transaction.
func (tx *Tx) CountExternalPermissions(ctx context.Context, nodeID string) (int, error) {
	return countExternalPermissions(ctx, tx.q, nodeID)
}

func countExternalPermissions(ctx context.Context, q querier, nodeID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM external_permissions WHERE node_id = ?", nodeID).Scan(&n)
	return n, err
}

// ExternalPermissionOn returns the grant a node holds for the address key. It returns ErrNotFound
// if there is none.
func (tx *Tx) ExternalPermissionOn(ctx context.Context, nodeID, emailKey string) (*ExternalPermission, error) {
	return scanExternalPermission(tx.q.QueryRowContext(ctx, `SELECT `+externalGrantCols+`
		FROM external_permissions WHERE node_id = ? AND email_key = ?`, nodeID, emailKey))
}

// NodeAccountID returns the account a node belongs to. It returns ErrNotFound if there is no such
// node.
func (s *Store) NodeAccountID(ctx context.Context, nodeID string) (string, error) {
	var acc string
	err := s.r.QueryRowContext(ctx, "SELECT account_id FROM nodes WHERE id = ?", nodeID).Scan(&acc)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return acc, err
}

// ExternalGrantRoot returns the topmost live grant of emailKey at or above nodeID in accountID: the
// grant the address's reach starts from (decision 187). An expired grant reaches nothing. The walk
// stays inside the account and stops at 1000 levels. It returns ErrNotFound when no live grant of
// the address reaches the node.
func (s *Store) ExternalGrantRoot(ctx context.Context, accountID, nodeID, emailKey string, now time.Time) (*ExternalPermission, error) {
	return scanExternalPermission(s.r.QueryRowContext(ctx, `WITH RECURSIVE chain(nid, parent, depth) AS (
			SELECT id, parent_id, 0 FROM nodes WHERE account_id = ? AND id = ?
			UNION ALL
			SELECT n.id, n.parent_id, chain.depth + 1 FROM nodes n JOIN chain ON n.id = chain.parent
			WHERE n.account_id = ? AND chain.depth < 1000
		)
		SELECT `+externalGrantCols+` FROM external_permissions ep JOIN chain ON ep.node_id = chain.nid
		WHERE ep.email_key = ? AND (ep.expires_at IS NULL OR ep.expires_at > ?)
		ORDER BY chain.depth DESC LIMIT 1`, accountID, nodeID, accountID, emailKey, millis(now)))
}

// ExternalSharing returns the account's switch for sharing with other accounts (decision 187).
func (s *Store) ExternalSharing(ctx context.Context, accountID string) (bool, error) {
	return externalSharing(ctx, s.r, accountID)
}

// ExternalSharing is Store.ExternalSharing read inside the transaction.
func (tx *Tx) ExternalSharing(ctx context.Context, accountID string) (bool, error) {
	return externalSharing(ctx, tx.q, accountID)
}

func externalSharing(ctx context.Context, q querier, accountID string) (bool, error) {
	var on int64
	err := q.QueryRowContext(ctx, "SELECT external_sharing FROM accounts WHERE id = ?", accountID).Scan(&on)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	return on == 1, err
}

// SetExternalSharing stores the account's switch for sharing with other accounts. Turning it off
// keeps the grants; it refuses new ones and hides the list.
func (tx *Tx) SetExternalSharing(ctx context.Context, accountID string, on bool, at time.Time) error {
	return rowsAffected(tx.q.ExecContext(ctx, "UPDATE accounts SET external_sharing = ?, updated_at = ? WHERE id = ?",
		boolInt(on), millis(at), accountID))
}

// ExternalShare is a node another account shared with an address: the node, the grant, the sharer
// account's name, and the display name of the user that made the grant ("" for a key or a user that
// is gone).
type ExternalShare struct {
	Node         *Node
	Permission   *ExternalPermission
	AccountName  string
	SharedByName string
}

// ExternalSharedWith lists the live grants made to emailKey by other accounts whose switch is on,
// newest first, up to limit. The caller's own account is left out, and so are suspended accounts and
// expired grants. Trashed nodes are returned: the reader drops them through TrashState, as the other
// shared lists do.
func (s *Store) ExternalSharedWith(ctx context.Context, accountID, emailKey string, limit int) ([]ExternalShare, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT ep.node_id, ep.account_id, ep.email, ep.email_key, ep.role,
			ep.created_by, ep.created_at, ep.expires_at, a.name, COALESCE(u.display_name, '')
		FROM external_permissions ep
		JOIN accounts a ON a.id = ep.account_id
		LEFT JOIN users u ON u.id = ep.created_by
		WHERE ep.email_key = ? AND ep.account_id != ? AND a.external_sharing = 1 AND a.status = 'active'
			AND (ep.expires_at IS NULL OR ep.expires_at > ?)
		ORDER BY ep.created_at DESC, ep.node_id LIMIT ?`,
		emailKey, accountID, millis(Now()), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExternalShare
	for rows.Next() {
		var sh ExternalShare
		var p ExternalPermission
		var created int64
		var expires sql.NullInt64
		err := rows.Scan(&p.NodeID, &p.AccountID, &p.Email, &p.EmailKey, &p.Role, &p.CreatedBy, &created, &expires,
			&sh.AccountName, &sh.SharedByName)
		if err != nil {
			return nil, err
		}
		p.CreatedAt = fromMillis(created)
		p.ExpiresAt = fromNullMillis(expires)
		sh.Permission = &p
		out = append(out, sh)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return []ExternalShare{}, nil
	}
	ids := make([]string, len(out))
	for i := range out {
		ids[i] = out[i].Permission.NodeID
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	nrows, err := s.r.QueryContext(ctx, nodeSelect+" WHERE n.id IN (SELECT value FROM json_each(?))", string(b))
	if err != nil {
		return nil, err
	}
	defer nrows.Close()
	nodes := make(map[string]*Node, len(ids))
	for nrows.Next() {
		n, err := scanNode(nrows)
		if err != nil {
			return nil, err
		}
		nodes[n.ID] = n
	}
	if err := nrows.Err(); err != nil {
		return nil, err
	}
	kept := out[:0]
	for _, sh := range out {
		if n := nodes[sh.Permission.NodeID]; n != nil {
			sh.Node = n
			kept = append(kept, sh)
		}
	}
	return kept, nil
}
