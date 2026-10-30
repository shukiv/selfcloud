// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"strconv"
	"time"
)

// trashKeyPrefix starts the name key of a trashed node. Names cannot contain "/", so no name key a
// name can produce ever begins with it: a trashed node holds no name, and the name is free.
const trashKeyPrefix = "/trash/"

// TrashState reports whether node id of accountID is an explicit trash root (self), or lies below one
// (above). A node that is in neither is reachable as far as the trash goes. It reads outside a
// transaction.
func (s *Store) TrashState(ctx context.Context, accountID, id string) (self, above bool, err error) {
	return trashState(ctx, s.r, accountID, id)
}

// TrashState is Store.TrashState read inside the transaction, so a check made there sees the trash as it
// is then.
func (tx *Tx) TrashState(ctx context.Context, accountID, id string) (self, above bool, err error) {
	return trashState(ctx, tx.q, accountID, id)
}

// trashState walks the node and its ancestors once. The node's own mark and any ancestor's mark are
// read from the same walk, so the access rules cannot disagree with each other.
func trashState(ctx context.Context, q querier, accountID, id string) (self, above bool, err error) {
	var s, a int64
	err = q.QueryRowContext(ctx, `WITH RECURSIVE chain(nid, parent, depth) AS (
			SELECT id, parent_id, 0 FROM nodes WHERE account_id = ? AND id = ?
			UNION ALL
			SELECT p.id, p.parent_id, c.depth + 1 FROM nodes p JOIN chain c ON p.id = c.parent WHERE p.account_id = ?
		)
		SELECT COALESCE(MAX(CASE WHEN c.depth = 0 AND n.trashed_at IS NOT NULL THEN 1 ELSE 0 END), 0),
			COALESCE(MAX(CASE WHEN c.depth > 0 AND n.trashed_at IS NOT NULL THEN 1 ELSE 0 END), 0)
		FROM chain c JOIN nodes n ON n.id = c.nid AND n.account_id = ?`,
		accountID, id, accountID, accountID).Scan(&s, &a)
	return s == 1, a == 1, err
}

// TrashNode moves node id of accountID to the trash. The node keeps its parent, its contents and its
// permissions; it gets its trash mark, and its name key becomes a reserved value, so a new item may take
// the name at once. ErrNotFound if the node is not in the account or is already trashed. The caller
// checks access and the usage side: trashed items still count toward usage.
func (tx *Tx) TrashNode(ctx context.Context, accountID, id, by string, at time.Time) error {
	return rowsAffected(tx.q.ExecContext(ctx, `UPDATE nodes SET trashed_at = ?, trashed_by = ?,
		trashed_name_key = name_key, name_key = ? || id
		WHERE account_id = ? AND id = ? AND trashed_at IS NULL`,
		millis(at), by, trashKeyPrefix, accountID, id))
}

// Untrash clears the trash mark of node id of accountID and its saved name key. The caller has already
// written the node's name key and parent with UpdateNode, in the same transaction.
func (tx *Tx) Untrash(ctx context.Context, accountID, id string) error {
	return rowsAffected(tx.q.ExecContext(ctx, `UPDATE nodes SET trashed_at = NULL, trashed_by = '',
		trashed_name_key = '' WHERE account_id = ? AND id = ? AND trashed_at IS NOT NULL`, accountID, id))
}

// TrashCursor is the position of a trash root in a listing: its trash time and its ID. A page's
// next cursor is the position of its last item.
func TrashCursor(n *Node) string {
	return encodeCursor(strconv.FormatInt(millis(*n.TrashedAt), 10), n.ID)
}

// TrashRoots lists the explicit trash roots of accountID, newest first, one page at a time. folderID
// narrows the list to one folder when it is set. The caller decides which of them it may see: a page
// may therefore hold fewer than its limit of items the caller can use.
func (s *Store) TrashRoots(ctx context.Context, accountID, folderID string, p Page) ([]*Node, error) {
	query := nodeSelect + " WHERE n.account_id = ? AND n.trashed_at IS NOT NULL"
	args := []any{accountID}
	if folderID != "" {
		query += " AND n.folder_id = ?"
		args = append(args, folderID)
	}
	if p.Cursor != "" {
		parts, err := decodeCursor(p.Cursor, 2)
		if err != nil {
			return nil, err
		}
		at, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, ErrBadCursor
		}
		query += " AND (n.trashed_at < ? OR (n.trashed_at = ? AND n.id < ?))"
		args = append(args, at, at, parts[1])
	}
	args = append(args, p.Limit)
	rows, err := s.r.QueryContext(ctx, query+" ORDER BY n.trashed_at DESC, n.id DESC LIMIT ?", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ExpiredTrash lists up to limit explicit trash roots, of any account, that were trashed at or before
// cutoff, oldest first. The retention sweep purges them.
func (s *Store) ExpiredTrash(ctx context.Context, cutoff time.Time, limit int) ([]*Node, error) {
	rows, err := s.r.QueryContext(ctx, nodeSelect+` WHERE n.trashed_at IS NOT NULL AND n.trashed_at <= ?
		ORDER BY n.trashed_at, n.id LIMIT ?`, millis(cutoff), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// DropUploadsUnder deletes the unfinished uploads whose target directory lies in the subtree of node id,
// releases their reservations in the same transaction, and returns how many it deleted. Trashing a
// directory ends them as deleting it does: the directory they were aimed at is no longer in place. The
// temporary files they left behind are collected later, as orphans. A drop upload is left alone: its
// visitor's link is only paused by the trash, and the upload resumes if the directory is restored (slice o3).
// Deleting the directory for good ends drop uploads too (PurgeNode).
func (tx *Tx) DropUploadsUnder(ctx context.Context, accountID, id string) (int64, error) {
	var reserved, count int64
	const sub = `WITH RECURSIVE sub(nid) AS (
		SELECT id FROM nodes WHERE account_id = ? AND id = ?
		UNION ALL
		SELECT c.id FROM nodes c JOIN sub ON c.parent_id = sub.nid WHERE c.account_id = ?
	)`
	err := tx.q.QueryRowContext(ctx, sub+` SELECT COUNT(*), COALESCE(SUM(CASE WHEN visitor_id = '' THEN length ELSE durable_offset END), 0) FROM uploads
		WHERE account_id = ? AND parent_id IN (SELECT nid FROM sub) AND visitor_id = ''`,
		accountID, id, accountID, accountID).Scan(&count, &reserved)
	if err != nil || count == 0 {
		return 0, err
	}
	folder, err := tx.NodeFolder(ctx, accountID, id)
	if err != nil {
		return 0, err
	}
	if _, err := tx.q.ExecContext(ctx, sub+` DELETE FROM uploads
		WHERE account_id = ? AND parent_id IN (SELECT nid FROM sub) AND visitor_id = ''`,
		accountID, id, accountID, accountID); err != nil {
		return 0, err
	}
	if err := tx.AdjustUsage(ctx, accountID, folder, UsageChange{Reserved: -reserved}); err != nil {
		return 0, err
	}
	return count, nil
}

// PurgeNode permanently deletes node id of accountID and everything below it, its versions included. The
// usage of the files and of their versions comes off in the same transaction, and so do the reservations of
// the uploads aimed into the subtree, which the foreign keys delete with it. Blobs are left for the collector.
// It returns the files and bytes that went.
func (tx *Tx) PurgeNode(ctx context.Context, accountID, id string) (files, bytes int64, err error) {
	folder, err := tx.NodeFolder(ctx, accountID, id)
	if err != nil {
		return 0, 0, err
	}
	c, err := tx.SubtreeUsage(ctx, accountID, id)
	if err != nil {
		return 0, 0, err
	}
	if err := rowsAffected(tx.q.ExecContext(ctx, "DELETE FROM nodes WHERE account_id = ? AND id = ?", accountID, id)); err != nil {
		return 0, 0, err
	}
	if err := tx.AdjustUsage(ctx, accountID, folder, UsageChange{Bytes: -c.Bytes, Files: -c.Files, Reserved: -c.Reserved}); err != nil {
		return 0, 0, err
	}
	return c.Files, c.Bytes, nil
}
