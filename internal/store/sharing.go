// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"time"
)

// HideFilter narrows a folder list to a user's visible team spaces, or to its hidden ones. A user
// hides a team space from its own list only; access does not change. An empty UserID does not narrow.
type HideFilter struct {
	UserID string
	Only   bool // list only the hidden spaces, not the visible ones
}

// hideCond is the SQL condition a HideFilter adds to a folder query, with the folder ID column named
// by alias ("" or "f."). It takes one argument, the user ID, and returns no condition and no argument
// when the filter does not narrow.
func hideCond(alias string, h HideFilter) (string, []any) {
	if h.UserID == "" {
		return "", nil
	}
	op := "NOT IN"
	if h.Only {
		op = "IN"
	}
	return " AND " + alias + "id " + op + " (SELECT folder_id FROM hidden_folders WHERE user_id = ?)",
		[]any{h.UserID}
}

// HiddenFolderIDs returns the IDs of the team spaces userID has hidden in accountID, as a set. A
// listing reads it once and marks each space it returns.
func (s *Store) HiddenFolderIDs(ctx context.Context, accountID, userID string) (map[string]bool, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT h.folder_id FROM hidden_folders h
		JOIN folders f ON f.id = h.folder_id WHERE f.account_id = ? AND h.user_id = ?`, accountID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// SetHidden records that userID hides the folder, or with hidden false shows it again. Hiding a
// folder that is already hidden changes nothing. A folder of another account matches no row, so
// the caller checks that the folder exists first.
func (tx *Tx) SetHidden(ctx context.Context, accountID, userID, folderID string, hidden bool, at time.Time) error {
	if !hidden {
		_, err := tx.q.ExecContext(ctx, `DELETE FROM hidden_folders WHERE user_id = ? AND folder_id IN
			(SELECT id FROM folders WHERE account_id = ? AND id = ?)`, userID, accountID, folderID)
		return err
	}
	_, err := tx.q.ExecContext(ctx, `INSERT INTO hidden_folders (user_id, folder_id, created_at)
		SELECT ?, id, ? FROM folders WHERE account_id = ? AND id = ?
		ON CONFLICT (user_id, folder_id) DO NOTHING`, userID, millis(at), accountID, folderID)
	return err
}

// UpdateFolderSettings writes the team-space settings of f, and its update time. The caller checks
// that f is a team space.
func (tx *Tx) UpdateFolderSettings(ctx context.Context, f *Folder) error {
	return rowsAffected(tx.q.ExecContext(ctx, `UPDATE folders SET members_only = ?, sharing_managers_only = ?,
		no_download_for_viewers = ?, allow_links = ?, updated_at = ? WHERE account_id = ? AND id = ?`,
		boolInt(f.MembersOnly), boolInt(f.SharingManagersOnly), boolInt(f.NoDownloadForViewers),
		boolInt(f.AllowLinks), millis(f.UpdatedAt), f.AccountID, f.ID))
}

// noDownloadSQL is true when a node restricts downloads: it, or an ancestor, carries no_download, or
// its team space sets no_download_for_viewers. It takes the account and node IDs three times over,
// as the chain walk needs the account twice. See noDownloadApplies.
// The query takes five arguments in this order: account, node, account, account, node.
const noDownloadSQL = `WITH RECURSIVE chain(id, parent) AS (
		SELECT id, parent_id FROM nodes WHERE account_id = ? AND id = ?
		UNION ALL
		SELECT p.id, p.parent_id FROM nodes p JOIN chain c ON p.id = c.parent WHERE p.account_id = ?
	)
	SELECT EXISTS (SELECT 1 FROM chain c JOIN nodes n ON n.id = c.id WHERE n.no_download = 1)
		OR EXISTS (SELECT 1 FROM nodes n JOIN folders f ON f.id = n.folder_id
			WHERE n.account_id = ? AND n.id = ? AND f.no_download_for_viewers = 1)`

// noDownloadApplies reports whether the no-download rule covers nodeID: the node or an ancestor has
// no_download, or the node's team space turns the rule on for viewers. The caller applies it only to
// a viewer or commenter; contributors and above are not restricted.
func noDownloadApplies(ctx context.Context, q querier, accountID, nodeID string) (bool, error) {
	var ok bool
	err := q.QueryRowContext(ctx, noDownloadSQL, accountID, nodeID, accountID, accountID, nodeID).Scan(&ok)
	return ok, err
}

// NoDownloadApplies is noDownloadApplies read outside a transaction.
func (s *Store) NoDownloadApplies(ctx context.Context, accountID, nodeID string) (bool, error) {
	return noDownloadApplies(ctx, s.r, accountID, nodeID)
}

// NoDownloadApplies is noDownloadApplies read inside the transaction.
func (tx *Tx) NoDownloadApplies(ctx context.Context, accountID, nodeID string) (bool, error) {
	return noDownloadApplies(ctx, tx.q, accountID, nodeID)
}

// subtreeHasNoDownload reports whether any node of nodeID's subtree, nodeID included, carries
// no_download itself. A copy of the subtree takes the flags along, so a copy is refused when this holds.
func subtreeHasNoDownload(ctx context.Context, q querier, accountID, nodeID string) (bool, error) {
	var ok bool
	err := q.QueryRowContext(ctx, `WITH RECURSIVE sub(id) AS (
			SELECT id FROM nodes WHERE account_id = ? AND id = ?
			UNION ALL
			SELECT c.id FROM nodes c JOIN sub ON c.parent_id = sub.id WHERE c.account_id = ?
		)
		SELECT EXISTS (SELECT 1 FROM sub JOIN nodes n ON n.id = sub.id WHERE n.no_download = 1)`,
		accountID, nodeID, accountID).Scan(&ok)
	return ok, err
}

// SubtreeHasNoDownload is subtreeHasNoDownload read outside a transaction.
func (s *Store) SubtreeHasNoDownload(ctx context.Context, accountID, nodeID string) (bool, error) {
	return subtreeHasNoDownload(ctx, s.r, accountID, nodeID)
}

// SubtreeHasNoDownload is subtreeHasNoDownload read inside the transaction.
func (tx *Tx) SubtreeHasNoDownload(ctx context.Context, accountID, nodeID string) (bool, error) {
	return subtreeHasNoDownload(ctx, tx.q, accountID, nodeID)
}

// editorsCanShare reports whether editors may set permissions on nodeID: the node or an ancestor
// carries editors_can_share. A team space's sharing_managers_only overrides it; see SharingManagersOnly.
func editorsCanShare(ctx context.Context, q querier, accountID, nodeID string) (bool, error) {
	var ok bool
	err := q.QueryRowContext(ctx, `WITH RECURSIVE chain(id, parent) AS (
			SELECT id, parent_id FROM nodes WHERE account_id = ? AND id = ?
			UNION ALL
			SELECT p.id, p.parent_id FROM nodes p JOIN chain c ON p.id = c.parent WHERE p.account_id = ?
		)
		SELECT EXISTS (SELECT 1 FROM chain c JOIN nodes n ON n.id = c.id WHERE n.editors_can_share = 1)`,
		accountID, nodeID, accountID).Scan(&ok)
	return ok, err
}

// EditorsCanShare is editorsCanShare read outside a transaction.
func (s *Store) EditorsCanShare(ctx context.Context, accountID, nodeID string) (bool, error) {
	return editorsCanShare(ctx, s.r, accountID, nodeID)
}

// EditorsCanShare is editorsCanShare read inside the transaction.
func (tx *Tx) EditorsCanShare(ctx context.Context, accountID, nodeID string) (bool, error) {
	return editorsCanShare(ctx, tx.q, accountID, nodeID)
}

// sharingManagersOnly reports whether the team space of nodeID keeps permission changes to managers.
func sharingManagersOnly(ctx context.Context, q querier, accountID, nodeID string) (bool, error) {
	var ok bool
	err := q.QueryRowContext(ctx, `SELECT f.sharing_managers_only = 1 FROM nodes n JOIN folders f ON f.id = n.folder_id
		WHERE n.account_id = ? AND n.id = ?`, accountID, nodeID).Scan(&ok)
	return ok, err
}

// SharingManagersOnly is sharingManagersOnly read inside the transaction.
func (tx *Tx) SharingManagersOnly(ctx context.Context, accountID, nodeID string) (bool, error) {
	return sharingManagersOnly(ctx, tx.q, accountID, nodeID)
}

// SetSubtreeOwner gives every node under id, id included, the owner owner. It is the ownership
// transfer inside a team space: the nodes stay where they are, only their owner changes.
func (tx *Tx) SetSubtreeOwner(ctx context.Context, accountID, id, owner string) error {
	_, err := tx.q.ExecContext(ctx, `WITH RECURSIVE sub(nid) AS (
		SELECT id FROM nodes WHERE account_id = ? AND id = ?
		UNION ALL
		SELECT c.id FROM nodes c JOIN sub ON c.parent_id = sub.nid WHERE c.account_id = ?
	)
	UPDATE nodes SET owner_id = ? WHERE account_id = ? AND id IN (SELECT nid FROM sub)`,
		accountID, id, accountID, owner, accountID)
	return err
}

// MoveFolderContents moves everything in fromFolder, but its root, into toFolder: the root's children
// go under toParent, and every node takes owner. It backs the deletion of a user whose personal folder
// keeps files, which move into the recipient's My files. The source root is left for the caller to
// delete with DeleteFolder, so usage and reservations are adjusted there. Uploads aimed at a directory
// that moved follow it, so they stay valid.
func (tx *Tx) MoveFolderContents(ctx context.Context, accountID, fromFolder, toFolder, toParent, owner string) error {
	root, err := tx.RootNode(ctx, accountID, fromFolder)
	if err != nil {
		return err
	}
	// The contents' usage moves with them: their files and versions, and the reservations of the uploads aimed
	// into them. The root's own uploads stay in the source folder, with its counters, until it is deleted.
	var c UsageChange
	if err := tx.q.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN n.kind = 'file' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN n.kind = 'file' THEN n.size ELSE 0 END), 0)
		FROM nodes n WHERE n.account_id = ? AND n.folder_id = ? AND n.parent_id IS NOT NULL`,
		accountID, fromFolder).Scan(&c.Files, &c.Bytes); err != nil {
		return err
	}
	var versionBytes int64
	if err := tx.q.QueryRowContext(ctx, `SELECT COALESCE(SUM(v.size), 0) FROM versions v
		JOIN nodes n ON n.id = v.node_id WHERE n.account_id = ? AND n.folder_id = ? AND n.parent_id IS NOT NULL`,
		accountID, fromFolder).Scan(&versionBytes); err != nil {
		return err
	}
	c.Bytes += versionBytes
	if err := tx.q.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN visitor_id = '' THEN length ELSE durable_offset END), 0)
		FROM uploads WHERE account_id = ? AND folder_id = ? AND parent_id IS NOT NULL AND parent_id <> ?`,
		accountID, fromFolder, root.ID).Scan(&c.Reserved); err != nil {
		return err
	}
	if err := tx.MoveUsage(ctx, accountID, fromFolder, toFolder, c); err != nil {
		return err
	}
	if _, err := tx.q.ExecContext(ctx, `UPDATE uploads SET folder_id = ?
		WHERE account_id = ? AND folder_id = ? AND parent_id IS NOT NULL AND parent_id <> ?`,
		toFolder, accountID, fromFolder, root.ID); err != nil {
		return err
	}
	// Every node but the root changes folder. Their parents still name nodes now in the new folder,
	// except the root's children, which are re-parented below.
	if _, err := tx.q.ExecContext(ctx, `UPDATE nodes SET folder_id = ?, owner_id = ?
		WHERE account_id = ? AND folder_id = ? AND parent_id IS NOT NULL`,
		toFolder, owner, accountID, fromFolder); err != nil {
		return err
	}
	_, err = tx.q.ExecContext(ctx, `UPDATE nodes SET parent_id = ? WHERE account_id = ? AND parent_id = ?`,
		toParent, accountID, root.ID)
	return err
}

// PermissionOn returns the live permission held on nodeID by one subject, or ErrNotFound.
func (tx *Tx) PermissionOn(ctx context.Context, nodeID, subjectKind, subjectID string, at time.Time) (*Permission, error) {
	return scanPermission(tx.q.QueryRowContext(ctx, "SELECT "+permissionCols+" FROM permissions "+
		"WHERE node_id = ? AND subject_kind = ? AND subject_id = ? AND (expires_at IS NULL OR expires_at > ?)",
		nodeID, subjectKind, subjectID, millis(at)))
}

// DeleteExpiredPermissions removes the permissions that expired before the cutoff, and returns them,
// so the caller can audit each. A permission that merely expired grants nothing already; the sweep
// only tidies the rows, after a grace period, so the audit shows when access ended.
func (tx *Tx) DeleteExpiredPermissions(ctx context.Context, before time.Time) ([]*Permission, error) {
	rows, err := tx.q.QueryContext(ctx, "SELECT "+permissionCols+" FROM permissions "+
		"WHERE expires_at IS NOT NULL AND expires_at < ? ORDER BY node_id, subject_kind, subject_id",
		millis(before))
	if err != nil {
		return nil, err
	}
	var out []*Permission
	for rows.Next() {
		p, err := scanPermission(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, p := range out {
		if _, err := tx.q.ExecContext(ctx, "DELETE FROM permissions WHERE node_id = ? AND subject_kind = ? AND subject_id = ?",
			p.NodeID, p.SubjectKind, p.SubjectID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// SharingManagersOnly is sharingManagersOnly read outside a transaction.
func (s *Store) SharingManagersOnly(ctx context.Context, accountID, nodeID string) (bool, error) {
	return sharingManagersOnly(ctx, s.r, accountID, nodeID)
}

// ClearSubtreeEditorsCanShare turns editors_can_share off on every node under id, id included. A copy or
// a move placed by a caller who is not a manager of the destination takes this flag off, so the caller
// cannot hand editors a power it does not hold.
func (tx *Tx) ClearSubtreeEditorsCanShare(ctx context.Context, accountID, id string) error {
	_, err := tx.q.ExecContext(ctx, `WITH RECURSIVE sub(nid) AS (
		SELECT id FROM nodes WHERE account_id = ? AND id = ?
		UNION ALL
		SELECT c.id FROM nodes c JOIN sub ON c.parent_id = sub.nid WHERE c.account_id = ?
	)
	UPDATE nodes SET editors_can_share = 0 WHERE account_id = ? AND id IN (SELECT nid FROM sub)`,
		accountID, id, accountID, accountID)
	return err
}
