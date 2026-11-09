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
	err = q.QueryRowContext(ctx, trashChainSQL, accountID, id, accountID).Scan(&s, &a)
	return s == 1, a == 1, err
}

// trashChainSQL reads the trash marks of a node and its ancestors (args: account, node, account). The final join
// reaches each chain node by its primary key. It must not filter on account_id there: that made SQLite scan the
// whole nodes table once per call, so a walk of n nodes cost O(n²) (decision 35). The chain is account-scoped already,
// because every step of the recursion is. TestTrashChainSQLDoesNotScanNodes reads its plan.
const trashChainSQL = `WITH RECURSIVE chain(nid, parent, depth) AS (
			SELECT id, parent_id, 0 FROM nodes WHERE account_id = ? AND id = ?
			UNION ALL
			SELECT p.id, p.parent_id, c.depth + 1 FROM nodes p JOIN chain c ON p.id = c.parent WHERE p.account_id = ?
		)
		SELECT COALESCE(MAX(CASE WHEN c.depth = 0 AND n.trashed_at IS NOT NULL THEN 1 ELSE 0 END), 0),
			COALESCE(MAX(CASE WHEN c.depth > 0 AND n.trashed_at IS NOT NULL THEN 1 ELSE 0 END), 0)
		FROM chain c JOIN nodes n ON n.id = c.nid`

// TrashNode moves node id of accountID to the trash. The node keeps its parent, its contents and its
// permissions; it gets its trash mark, and its name key becomes a reserved value, so a new item may take
// the name at once. ErrNotFound if the node is not in the account or is already trashed. The caller
// checks access and the usage side: trashed items still count toward usage.
func (tx *Tx) TrashNode(ctx context.Context, accountID, id, by string, at time.Time) error {
	// A folder's trash mark covers the files below it, so the guard walks the subtree (wopi_locks.go).
	if err := tx.guardNode(ctx, accountID, id, ""); err != nil {
		return err
	}
	return rowsAffected(tx.q.ExecContext(ctx, `UPDATE nodes SET trashed_at = ?, trashed_by = ?,
		trashed_name_key = name_key, name_key = ? || id
		WHERE account_id = ? AND id = ? AND trashed_at IS NULL`,
		millis(at), by, trashKeyPrefix, accountID, id))
}

// Untrash clears the trash mark of node id of accountID and its saved name key. The caller has already
// written the node's name key and parent with UpdateNode, in the same transaction.
func (tx *Tx) Untrash(ctx context.Context, accountID, id string) error {
	if err := tx.guardNode(ctx, accountID, id, ""); err != nil {
		return err
	}
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
	return s.TrashRootsFiltered(ctx, accountID, folderID, ListFilter{}, p)
}

// TrashCursorOf is TrashCursor for a list narrowed by filter f: the cursor carries the filter's tag (decision 110).
func TrashCursorOf(n *Node, f ListFilter) string {
	return encodeFilteredCursor(f, strconv.FormatInt(millis(*n.TrashedAt), 10), n.ID)
}

// TrashRootsFiltered is TrashRoots narrowed by a list filter (decision 110): a type and a modified range. A kind type
// narrows the roots to that kind; a file group narrows them to files of the group.
func (s *Store) TrashRootsFiltered(ctx context.Context, accountID, folderID string, f ListFilter, p Page) ([]*Node, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	var after *trashAfter
	if p.Cursor != "" {
		parts, err := decodeFilteredCursor(p.Cursor, 2, f)
		if err != nil {
			return nil, err
		}
		at, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, ErrBadCursor
		}
		after = &trashAfter{At: at, ID: parts[1]}
	}
	query, args := trashRootsSQL(accountID, folderID, f, after, p.Limit)
	rows, err := s.r.QueryContext(ctx, query, args...)
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

// trashAfter is the position a trash page resumes after: the root's trashed time and ID.
type trashAfter struct {
	At int64
	ID string
}

// trashRootsSQL builds the trash page query and its arguments: at most limit explicit trash roots of accountID, newest
// first, narrowed by the folder and the filter, and after the position when one is given. Every value is an argument.
func trashRootsSQL(accountID, folderID string, f ListFilter, after *trashAfter, limit int) (string, []any) {
	query := nodeSelect + " WHERE n.account_id = ? AND n.trashed_at IS NOT NULL"
	args := []any{accountID}
	if folderID != "" {
		query += " AND n.folder_id = ?"
		args = append(args, folderID)
	}
	if fsql, fargs := f.Where("n"); fsql != "" {
		query += fsql
		args = append(args, fargs...)
	}
	if after != nil {
		query += " AND (n.trashed_at < ? OR (n.trashed_at = ? AND n.id < ?))"
		args = append(args, after.At, after.At, after.ID)
	}
	args = append(args, limit)
	return query + " ORDER BY n.trashed_at DESC, n.id DESC LIMIT ?", args
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
	if err := tx.guardNode(ctx, accountID, id, ""); err != nil {
		return 0, 0, err
	}
	// The imports aimed under the node end first, so their staging and their reservations go in this write (round 4, N1).
	if err := tx.EndImportsAt(ctx, accountID, "", id); err != nil {
		return 0, 0, err
	}
	folder, err := tx.NodeFolder(ctx, accountID, id)
	if err != nil {
		return 0, 0, err
	}
	c, err := tx.SubtreeUsage(ctx, accountID, id)
	if err != nil {
		return 0, 0, err
	}
	// The search rows go with the nodes by cascade, but their full-text rows do not, so they go first (decision 40).
	if err := tx.dropSearchSubtree(ctx, accountID, id); err != nil {
		return 0, 0, err
	}
	// The text of the subtree's blobs goes with the nodes, in this write, unless another node still holds the blob (the
	// sweep would do it later, and the text would stay in the database until then).
	blobs, err := tx.textBlobsOf(ctx, accountID, `n.id IN (WITH RECURSIVE sub(nid) AS (
		SELECT id FROM nodes WHERE account_id = ? AND id = ?
		UNION ALL SELECT c.id FROM nodes c JOIN sub s ON c.parent_id = s.nid WHERE c.account_id = ?)
		SELECT nid FROM sub)`, accountID, id, accountID)
	if err != nil {
		return 0, 0, err
	}
	if err := rowsAffected(tx.q.ExecContext(ctx, "DELETE FROM nodes WHERE account_id = ? AND id = ?", accountID, id)); err != nil {
		return 0, 0, err
	}
	if err := tx.dropTextIfUnused(ctx, accountID, blobs...); err != nil {
		return 0, 0, err
	}
	if err := tx.AdjustUsage(ctx, accountID, folder, UsageChange{Bytes: -c.Bytes, Files: -c.Files, Reserved: -c.Reserved}); err != nil {
		return 0, 0, err
	}
	return c.Files, c.Bytes, nil
}
