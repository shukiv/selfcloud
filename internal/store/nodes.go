// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"
)

// Node kinds, as the nodes.kind column names them.
const (
	KindDir  = "dir"
	KindFile = "file"
)

// Node is an entry of a folder's file tree: a directory, a file, or a folder's root (a directory
// with no parent and the empty name).
type Node struct {
	ID        string
	AccountID string
	FolderID  string
	ParentID  string // "" for a folder's root
	Name      string // NFC; "" for a root
	NameKey   string
	Kind      string // KindDir or KindFile
	Size      int64
	MimeType  string
	BlobID    string // files only
	SHA256    string // files only; from the blob
	ETag      string // files only; changes with every write
	// ContentTag is a random value for a file, changed by every content write (PUT, upload replace,
	// version restore) and never by a rename or move. A signed link is bound to it. Not sent to clients.
	ContentTag string // files only
	CreatedAt  time.Time
	ModifiedAt time.Time
	CreatedBy  string
	ModifiedBy string
	OwnerID    string // the user who owns the node; a new node's owner is its creator
	// NoDownload forbids viewers and commenters the contents of this node and of its subtree. It
	// deters rather than secures: inline display still shows the contents. See the access code.
	NoDownload bool
	// EditorsCanShare lets editors set permissions on this node and its subtree, up to editor.
	EditorsCanShare bool
	// TrashedAt is set on an explicit trash root only: the node that was deleted. Nodes below it are
	// trashed through the ancestor walk, so they have no mark of their own. TrashedBy is the actor that
	// trashed it, and TrashedNameKey is the name key it had before, for a restore.
	TrashedAt      *time.Time
	TrashedBy      string
	TrashedNameKey string
}

// NewContentTag returns a fresh random content tag: 16 random bytes, base64url.
func NewContentTag() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("store: no randomness: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// IsRoot reports whether n is a folder's root.
func (n *Node) IsRoot() bool { return n.ParentID == "" }

// nodeSelect reads a node with its blob's hash. Callers add a WHERE clause.
const nodeSelect = `SELECT n.id, n.account_id, n.folder_id, n.parent_id, n.name, n.name_key, n.kind,
	n.size, n.mime_type, n.blob_id, COALESCE(b.sha256, ''), n.etag, n.content_tag, n.created_at, n.modified_at,
	n.created_by, n.modified_by, n.owner_id, n.no_download, n.editors_can_share,
	n.trashed_at, n.trashed_by, n.trashed_name_key
	FROM nodes n LEFT JOIN blobs b ON b.id = n.blob_id AND b.account_id = n.account_id`

const nodeCols = `id, account_id, folder_id, parent_id, name, name_key, kind, size, mime_type, blob_id,
	etag, content_tag, created_at, modified_at, created_by, modified_by, owner_id, no_download, editors_can_share`

func scanNode(row interface{ Scan(...any) error }) (*Node, error) {
	var n Node
	var parent, blob sql.NullString
	var created, modified int64
	var noDownload, editorsCanShare int64
	var trashed sql.NullInt64
	err := row.Scan(&n.ID, &n.AccountID, &n.FolderID, &parent, &n.Name, &n.NameKey, &n.Kind, &n.Size,
		&n.MimeType, &blob, &n.SHA256, &n.ETag, &n.ContentTag, &created, &modified, &n.CreatedBy, &n.ModifiedBy, &n.OwnerID,
		&noDownload, &editorsCanShare, &trashed, &n.TrashedBy, &n.TrashedNameKey)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	n.ParentID = parent.String
	n.BlobID = blob.String
	n.CreatedAt = fromMillis(created)
	n.ModifiedAt = fromMillis(modified)
	n.NoDownload = noDownload == 1
	n.EditorsCanShare = editorsCanShare == 1
	n.TrashedAt = fromNullMillis(trashed)
	return &n, nil
}

// getNode reads one node of accountID by ID, from either a pool or a transaction.
func getNode(ctx context.Context, q querier, accountID, id string) (*Node, error) {
	return scanNode(q.QueryRowContext(ctx, nodeSelect+" WHERE n.account_id = ? AND n.id = ?", accountID, id))
}

// Node returns a node of accountID. A node of another account is ErrNotFound.
func (s *Store) Node(ctx context.Context, accountID, id string) (*Node, error) {
	return getNode(ctx, s.r, accountID, id)
}

// Node returns a node of accountID.
func (tx *Tx) Node(ctx context.Context, accountID, id string) (*Node, error) {
	return getNode(ctx, tx.q, accountID, id)
}

// RootNode returns the root of a folder of accountID.
func (s *Store) RootNode(ctx context.Context, accountID, folderID string) (*Node, error) {
	return scanNode(s.r.QueryRowContext(ctx, nodeSelect+
		" WHERE n.account_id = ? AND n.folder_id = ? AND n.parent_id IS NULL", accountID, folderID))
}

// RootNode returns the root of a folder of accountID.
func (tx *Tx) RootNode(ctx context.Context, accountID, folderID string) (*Node, error) {
	return scanNode(tx.q.QueryRowContext(ctx, nodeSelect+
		" WHERE n.account_id = ? AND n.folder_id = ? AND n.parent_id IS NULL", accountID, folderID))
}

// Child returns the child of parentID whose name key is key. ErrNotFound if there is none.
func (s *Store) Child(ctx context.Context, accountID, parentID, key string) (*Node, error) {
	return scanNode(s.r.QueryRowContext(ctx, nodeSelect+
		" WHERE n.account_id = ? AND n.parent_id = ? AND n.name_key = ?", accountID, parentID, key))
}

// Child returns the child of parentID whose name key is key, inside the transaction.
func (tx *Tx) Child(ctx context.Context, accountID, parentID, key string) (*Node, error) {
	return scanNode(tx.q.QueryRowContext(ctx, nodeSelect+
		" WHERE n.account_id = ? AND n.parent_id = ? AND n.name_key = ?", accountID, parentID, key))
}

// Children lists the children of a directory of accountID in name-key order, one page at a time.
func (s *Store) Children(ctx context.Context, accountID, parentID string, p Page) ([]*Node, string, error) {
	query := nodeSelect + " WHERE n.account_id = ? AND n.parent_id = ? AND n.trashed_at IS NULL"
	args := []any{accountID, parentID}
	if p.Cursor != "" {
		parts, err := decodeCursor(p.Cursor, 2)
		if err != nil {
			return nil, "", err
		}
		query += " AND (n.name_key > ? OR (n.name_key = ? AND n.id > ?))"
		args = append(args, parts[0], parts[0], parts[1])
	}
	args = append(args, p.Limit+1)
	rows, err := s.r.QueryContext(ctx, query+" ORDER BY n.name_key, n.id LIMIT ?", args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []*Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > p.Limit {
		out = out[:p.Limit]
		last := out[len(out)-1]
		next = encodeCursor(last.NameKey, last.ID)
	}
	return out, next, nil
}

// Subtree returns node id of accountID and its descendants, parents before children, so that
// copies can be made in this order. Trashed descendants are left out: a copy never carries what was
// deleted. The node itself is included whatever its trash state, since the caller has checked it.
func (tx *Tx) Subtree(ctx context.Context, accountID, id string) ([]*Node, error) {
	rows, err := tx.q.QueryContext(ctx, `WITH RECURSIVE sub(nid, depth) AS (
		SELECT id, 0 FROM nodes WHERE account_id = ? AND id = ?
		UNION ALL
		SELECT c.id, sub.depth + 1 FROM nodes c JOIN sub ON c.parent_id = sub.nid
		WHERE c.account_id = ? AND c.trashed_at IS NULL
	)
	`+nodeSelect+` JOIN sub ON sub.nid = n.id
		WHERE n.account_id = ? ORDER BY sub.depth, n.name_key, n.id`,
		accountID, id, accountID, accountID)
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

// SubtreeFiles counts the files under node id of accountID, the node itself included, and their
// total size, versions included: the bytes a version holds count toward usage, and they go when the
// file does. Usage is taken off by these numbers when the subtree goes.
func (tx *Tx) SubtreeFiles(ctx context.Context, accountID, id string) (files, bytes int64, err error) {
	err = tx.q.QueryRowContext(ctx, `WITH RECURSIVE sub(nid) AS (
		SELECT id FROM nodes WHERE account_id = ? AND id = ?
		UNION ALL
		SELECT c.id FROM nodes c JOIN sub ON c.parent_id = sub.nid WHERE c.account_id = ?
	)
	SELECT (SELECT COUNT(*) FROM sub JOIN nodes n ON n.id = sub.nid WHERE n.account_id = ? AND n.kind = 'file'),
		(SELECT COALESCE(SUM(n.size), 0) FROM sub JOIN nodes n ON n.id = sub.nid WHERE n.account_id = ? AND n.kind = 'file')
		+ (SELECT COALESCE(SUM(v.size), 0) FROM versions v WHERE v.account_id = ? AND v.node_id IN (SELECT nid FROM sub))`,
		accountID, id, accountID, accountID, accountID, accountID).Scan(&files, &bytes)
	return files, bytes, err
}

// SubtreeSize counts the nodes under node id of accountID, the node itself included, stopping at
// limit. The copy size check asks only whether the count passes its limit.
func (s *Store) SubtreeSize(ctx context.Context, accountID, id string, limit int) (int, error) {
	var n int
	err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM (WITH RECURSIVE sub(nid) AS (
		SELECT id FROM nodes WHERE account_id = ? AND id = ?
		UNION ALL
		SELECT c.id FROM nodes c JOIN sub ON c.parent_id = sub.nid WHERE c.account_id = ? AND c.trashed_at IS NULL
	)
	SELECT nid FROM sub LIMIT ?)`, accountID, id, accountID, limit).Scan(&n)
	return n, err
}

// IsWithin reports whether node id of accountID is ancestor itself or lies below it, read inside
// the transaction.
func (tx *Tx) IsWithin(ctx context.Context, accountID, id, ancestor string) (bool, error) {
	return isWithin(ctx, tx.q, accountID, id, ancestor)
}

// isWithin is IsWithin over either a pool or a transaction.
func isWithin(ctx context.Context, q querier, accountID, id, ancestor string) (bool, error) {
	var found bool
	err := q.QueryRowContext(ctx, `WITH RECURSIVE up(nid, parent) AS (
		SELECT id, parent_id FROM nodes WHERE account_id = ? AND id = ?
		UNION ALL
		SELECT p.id, p.parent_id FROM nodes p JOIN up ON p.id = up.parent WHERE p.account_id = ?
	)
	SELECT EXISTS(SELECT 1 FROM up WHERE nid = ?)`, accountID, id, accountID, ancestor).Scan(&found)
	return found, err
}

// InsertNode adds a node. It returns ErrConflict if a sibling has the same name key.
func (tx *Tx) InsertNode(ctx context.Context, n *Node) error {
	// A node in a personal folder belongs to the folder's user, whoever creates or copies it there.
	// Anywhere else its owner is the one given, or else its creator.
	owner := n.OwnerID
	if owner == "" {
		owner = n.CreatedBy
	}
	if n.Kind == KindFile {
		// A new file has new content, so it gets a new tag whatever the caller copied from.
		n.ContentTag = NewContentTag()
	}
	_, err := tx.q.ExecContext(ctx, `INSERT INTO nodes (`+nodeCols+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			COALESCE((SELECT owner_user_id FROM folders WHERE id = ? AND kind = 'personal'), ?), ?, ?)`,
		n.ID, n.AccountID, n.FolderID, nullString(n.ParentID), n.Name, n.NameKey, n.Kind, n.Size,
		n.MimeType, nullString(n.BlobID), n.ETag, n.ContentTag, millis(n.CreatedAt), millis(n.ModifiedAt),
		n.CreatedBy, n.ModifiedBy, n.FolderID, owner, boolInt(n.NoDownload), boolInt(n.EditorsCanShare))
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

// UpdateNode writes the mutable fields of n: parent, folder, name, content and times. It returns
// ErrNotFound if the node is not in the account, and ErrConflict on a sibling name clash.
func (tx *Tx) UpdateNode(ctx context.Context, n *Node) error {
	err := rowsAffected(tx.q.ExecContext(ctx, `UPDATE nodes SET folder_id = ?, parent_id = ?, name = ?,
		name_key = ?, size = ?, mime_type = ?, blob_id = ?, etag = ?, content_tag = ?, modified_at = ?, modified_by = ?,
		owner_id = CASE WHEN ? = '' THEN owner_id ELSE ? END, no_download = ?, editors_can_share = ?
		WHERE account_id = ? AND id = ?`,
		n.FolderID, nullString(n.ParentID), n.Name, n.NameKey, n.Size, n.MimeType, nullString(n.BlobID),
		n.ETag, n.ContentTag, millis(n.ModifiedAt), n.ModifiedBy, n.OwnerID, n.OwnerID, boolInt(n.NoDownload),
		boolInt(n.EditorsCanShare), n.AccountID, n.ID))
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

// SetSubtreeFolder moves every node under id, id included, into folderID. Folder membership is
// stored on each node, so a move across folders must rewrite the whole subtree. Moved into a personal
// folder, the subtree becomes its user's; moved into a team space, the owners stay as they were.
func (tx *Tx) SetSubtreeFolder(ctx context.Context, accountID, id, folderID string) error {
	_, err := tx.q.ExecContext(ctx, `WITH RECURSIVE sub(nid) AS (
		SELECT id FROM nodes WHERE account_id = ? AND id = ?
		UNION ALL
		SELECT c.id FROM nodes c JOIN sub ON c.parent_id = sub.nid WHERE c.account_id = ?
	)
	UPDATE nodes SET folder_id = ?,
		owner_id = COALESCE((SELECT owner_user_id FROM folders WHERE id = ? AND kind = 'personal'), owner_id)
		WHERE account_id = ? AND id IN (SELECT nid FROM sub)`,
		accountID, id, accountID, folderID, folderID, accountID)
	return err
}

// DeleteNode removes a node and, through the foreign key, everything below it. The caller takes
// the file usage off first, with SubtreeFiles. Blobs are not touched: the collector finds them.
//
// An upload whose target directory is in the subtree is deleted by the foreign key too, so its
// reservation is released here, in the same transaction, from the same rows.
func (tx *Tx) DeleteNode(ctx context.Context, accountID, id string) error {
	var reserved int64
	err := tx.q.QueryRowContext(ctx, `WITH RECURSIVE sub(nid) AS (
		SELECT id FROM nodes WHERE account_id = ? AND id = ?
		UNION ALL
		SELECT c.id FROM nodes c JOIN sub ON c.parent_id = sub.nid WHERE c.account_id = ?
	)
	SELECT COALESCE(SUM(length), 0) FROM uploads WHERE account_id = ? AND parent_id IN (SELECT nid FROM sub)`,
		accountID, id, accountID, accountID).Scan(&reserved)
	if err != nil {
		return err
	}
	if err := rowsAffected(tx.q.ExecContext(ctx, "DELETE FROM nodes WHERE account_id = ? AND id = ?", accountID, id)); err != nil {
		return err
	}
	if reserved == 0 {
		return nil
	}
	return tx.AdjustUsage(ctx, accountID, UsageChange{Reserved: -reserved})
}

// Blob is a stored file's contents.
type Blob struct {
	ID          string
	AccountID   string
	Size        int64
	SHA256      string
	State       string // BlobUploading, BlobLive or BlobOrphan
	DeleteAfter *time.Time
	CreatedAt   time.Time
}

// Blob states, as the blobs.state column names them.
const (
	BlobUploading = "uploading"
	BlobLive      = "live"
	BlobOrphan    = "orphan"
)

// InsertBlob records a committed blob. It is written in the same transaction as the node that
// first refers to it.
func (tx *Tx) InsertBlob(ctx context.Context, b *Blob) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO blobs (id, account_id, size, sha256, state, delete_after, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		b.ID, b.AccountID, b.Size, b.SHA256, b.State, nullMillis(b.DeleteAfter), millis(b.CreatedAt))
	return err
}

// OrphanUnreferenced marks every live blob that no node refers to as an orphan, to be removed
// once deleteAfter has passed. It returns how many it marked. Running it again marks nothing new.
func (tx *Tx) OrphanUnreferenced(ctx context.Context, deleteAfter time.Time) (int64, error) {
	res, err := tx.q.ExecContext(ctx, `UPDATE blobs SET state = 'orphan', delete_after = ?
		WHERE state = 'live' AND NOT EXISTS (SELECT 1 FROM nodes WHERE nodes.blob_id = blobs.id)
		AND NOT EXISTS (SELECT 1 FROM versions WHERE versions.blob_id = blobs.id)`,
		millis(deleteAfter))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DueBlobs lists up to limit orphans whose delete_after is not after now.
func (s *Store) DueBlobs(ctx context.Context, now time.Time, limit int) ([]*Blob, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT id, account_id, size, sha256, state, delete_after, created_at
		FROM blobs WHERE state = 'orphan' AND delete_after <= ? ORDER BY delete_after, id LIMIT ?`,
		millis(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Blob
	for rows.Next() {
		var b Blob
		var after sql.NullInt64
		var created int64
		if err := rows.Scan(&b.ID, &b.AccountID, &b.Size, &b.SHA256, &b.State, &after, &created); err != nil {
			return nil, err
		}
		b.DeleteAfter = fromNullMillis(after)
		b.CreatedAt = fromMillis(created)
		out = append(out, &b)
	}
	return out, rows.Err()
}

// DeleteOrphan removes an orphan's row if it is still an orphan with no node referring to it. It
// reports whether a row was removed; false means the blob was referenced again and must stay.
func (tx *Tx) DeleteOrphan(ctx context.Context, accountID, blobID string) (bool, error) {
	res, err := tx.q.ExecContext(ctx, `DELETE FROM blobs WHERE account_id = ? AND id = ? AND state = 'orphan'
		AND NOT EXISTS (SELECT 1 FROM nodes WHERE nodes.blob_id = blobs.id)
		AND NOT EXISTS (SELECT 1 FROM versions WHERE versions.blob_id = blobs.id)`, accountID, blobID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// DeleteExpiredSessions removes the sessions that have ended by now, and returns how many.
func (tx *Tx) DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error) {
	res, err := tx.q.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at <= ?", millis(now))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteSpentVerifications removes verification and reset tokens that have expired or been used,
// and returns how many. A used token is refused anyway, so keeping it gains nothing.
func (tx *Tx) DeleteSpentVerifications(ctx context.Context, now time.Time) (int64, error) {
	res, err := tx.q.ExecContext(ctx,
		"DELETE FROM verifications WHERE expires_at <= ? OR used_at IS NOT NULL", millis(now))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
