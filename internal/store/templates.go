// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
)

// TemplatesNode returns the node the account names as its templates folder, or "" for none. A trashed or unreadable folder
// is still named here; the API decides whether it counts (decision 105).
func (s *Store) TemplatesNode(ctx context.Context, accountID string) (string, error) {
	return templatesNode(ctx, s.r, accountID)
}

// TemplatesNode is TemplatesNode read inside the transaction.
func (tx *Tx) TemplatesNode(ctx context.Context, accountID string) (string, error) {
	return templatesNode(ctx, tx.q, accountID)
}

func templatesNode(ctx context.Context, q querier, accountID string) (string, error) {
	var id string
	err := q.QueryRowContext(ctx, `SELECT node_id FROM account_templates WHERE account_id = ?`, accountID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// SetTemplatesNode names nodeID as the account's templates folder, or clears the setting when nodeID is "".
func (tx *Tx) SetTemplatesNode(ctx context.Context, accountID, nodeID string) error {
	if nodeID == "" {
		_, err := tx.q.ExecContext(ctx, `DELETE FROM account_templates WHERE account_id = ?`, accountID)
		return err
	}
	_, err := tx.q.ExecContext(ctx, `INSERT INTO account_templates (account_id, node_id) VALUES (?, ?)
		ON CONFLICT (account_id) DO UPDATE SET node_id = excluded.node_id`, accountID, nodeID)
	return err
}

// ChildCursor is the cursor that starts a Children page after child n, in Children's order.
func ChildCursor(n *Node) string {
	return encodeCursor(n.NameKey, n.ID)
}

// ValidChildCursor reports whether c is a cursor that Children accepts.
func ValidChildCursor(c string) bool {
	_, err := decodeCursor(c, 2)
	return err == nil
}
