// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Version is an earlier content of a file. Its blob is kept, since the row refers to it, and the bytes
// it holds count toward the account's usage until it is pruned or deleted.
type Version struct {
	ID          string
	AccountID   string
	NodeID      string
	BlobID      string
	Size        int64
	SHA256      string
	ETag        string
	MimeType    string
	CreatedAt   time.Time // when this content was first written
	CreatedBy   string
	ReplacedAt  time.Time // when it stopped being the current content
	ReplacedBy  string
	Label       *string // at most 100 characters, or nil
	KeepForever bool
}

const versionCols = `id, account_id, node_id, blob_id, size, sha256, etag, mime_type, created_at, created_by,
	replaced_at, replaced_by, label, keep_forever`

func scanVersion(row interface{ Scan(...any) error }) (*Version, error) {
	var v Version
	var created, replaced int64
	var label sql.NullString
	var keep int64
	err := row.Scan(&v.ID, &v.AccountID, &v.NodeID, &v.BlobID, &v.Size, &v.SHA256, &v.ETag, &v.MimeType,
		&created, &v.CreatedBy, &replaced, &v.ReplacedBy, &label, &keep)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	v.CreatedAt = fromMillis(created)
	v.ReplacedAt = fromMillis(replaced)
	if label.Valid {
		l := label.String
		v.Label = &l
	}
	v.KeepForever = keep == 1
	return &v, nil
}

// InsertVersion records an earlier content of a file. It is written in the transaction that replaces the
// content, so the version and the new content commit together.
func (tx *Tx) InsertVersion(ctx context.Context, v *Version) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO versions (`+versionCols+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		v.ID, v.AccountID, v.NodeID, v.BlobID, v.Size, v.SHA256, v.ETag, v.MimeType, millis(v.CreatedAt),
		v.CreatedBy, millis(v.ReplacedAt), v.ReplacedBy, nullableLabel(v.Label), boolInt(v.KeepForever))
	return err
}

func nullableLabel(l *string) any {
	if l == nil {
		return nil
	}
	return *l
}

// Versions lists the versions of a node of accountID, newest replaced first.
func (s *Store) Versions(ctx context.Context, accountID, nodeID string) ([]*Version, error) {
	rows, err := s.r.QueryContext(ctx, "SELECT "+versionCols+` FROM versions
		WHERE account_id = ? AND node_id = ? ORDER BY replaced_at DESC, id DESC`, accountID, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Version
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Version returns one version of a node of accountID, inside the transaction. ErrNotFound if the node
// has no such version.
func (tx *Tx) Version(ctx context.Context, accountID, nodeID, id string) (*Version, error) {
	return scanVersion(tx.q.QueryRowContext(ctx, "SELECT "+versionCols+
		" FROM versions WHERE account_id = ? AND node_id = ? AND id = ?", accountID, nodeID, id))
}

// UpdateVersion writes the label and keep_forever of a version. The contents are never changed.
func (tx *Tx) UpdateVersion(ctx context.Context, v *Version) error {
	return rowsAffected(tx.q.ExecContext(ctx, `UPDATE versions SET label = ?, keep_forever = ?
		WHERE account_id = ? AND id = ?`, nullableLabel(v.Label), boolInt(v.KeepForever), v.AccountID, v.ID))
}

// KeptVersions counts the versions of a node that are kept forever.
func (tx *Tx) KeptVersions(ctx context.Context, accountID, nodeID string) (int, error) {
	var n int
	err := tx.q.QueryRowContext(ctx, "SELECT COUNT(*) FROM versions WHERE account_id = ? AND node_id = ? AND keep_forever = 1",
		accountID, nodeID).Scan(&n)
	return n, err
}

// DeleteVersion removes the row of one version. Its blob is left for the collector. The caller takes the
// version's bytes off usage, or makes the change that replaces them, in the same transaction: a restore
// moves the version's bytes into the file, so its net change is zero.
func (tx *Tx) DeleteVersion(ctx context.Context, accountID string, v *Version) error {
	return rowsAffected(tx.q.ExecContext(ctx, "DELETE FROM versions WHERE account_id = ? AND id = ?",
		accountID, v.ID))
}

// Version returns one version of a node of accountID, outside a transaction. ErrNotFound if the node has no
// such version.
func (s *Store) Version(ctx context.Context, accountID, nodeID, id string) (*Version, error) {
	return scanVersion(s.r.QueryRowContext(ctx, "SELECT "+versionCols+
		" FROM versions WHERE account_id = ? AND node_id = ? AND id = ?", accountID, nodeID, id))
}

// PruneVersions removes the versions of a node that are not kept and are past a limit, oldest first, and
// takes their bytes off the usage. A version is past the count limit when more than maxCount non-kept
// versions are newer than it; kept versions do not count. A version is past the age limit when it was
// replaced at or before ageCutoff. A limit of zero, or a nil cutoff, is no limit. It returns how many
// versions and bytes went.
func (tx *Tx) PruneVersions(ctx context.Context, accountID, nodeID string, maxCount int,
	ageCutoff *time.Time) (removed int, bytes int64, err error) {
	rows, err := tx.q.QueryContext(ctx, `SELECT id, size, replaced_at FROM versions
		WHERE account_id = ? AND node_id = ? AND keep_forever = 0 ORDER BY replaced_at, id`, accountID, nodeID)
	if err != nil {
		return 0, 0, err
	}
	type cand struct {
		id       string
		size     int64
		replaced int64
	}
	var all []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.id, &c.size, &c.replaced); err != nil {
			rows.Close()
			return 0, 0, err
		}
		all = append(all, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, 0, err
	}
	rows.Close()

	// The oldest ones go first: the excess over the count, then anything past the age cutoff.
	excess := 0
	if maxCount > 0 {
		excess = max(len(all)-maxCount, 0)
	}
	cut := int64(0)
	if ageCutoff != nil {
		cut = millis(*ageCutoff)
	}
	for i, c := range all {
		if i >= excess && (ageCutoff == nil || c.replaced > cut) {
			continue
		}
		if err := rowsAffected(tx.q.ExecContext(ctx, "DELETE FROM versions WHERE account_id = ? AND id = ?",
			accountID, c.id)); err != nil {
			return removed, bytes, err
		}
		removed++
		bytes += c.size
	}
	if bytes > 0 {
		folder, err := tx.NodeFolder(ctx, accountID, nodeID)
		if err != nil {
			return removed, bytes, err
		}
		if err := tx.AdjustUsage(ctx, accountID, folder, UsageChange{Bytes: -bytes}); err != nil {
			return removed, bytes, err
		}
	}
	return removed, bytes, nil
}

// NodeVersionTarget names a node that holds versions past a limit.
type NodeVersionTarget struct {
	AccountID string
	NodeID    string
}

// NodesOverVersionLimits lists up to limit nodes that hold non-kept versions past maxCount or older than
// ageCutoff, the nodes the retention sweep prunes. A zero maxCount or a nil cutoff leaves that limit out.
func (s *Store) NodesOverVersionLimits(ctx context.Context, maxCount int, ageCutoff *time.Time,
	limit int) ([]NodeVersionTarget, error) {
	var parts []string
	var args []any
	if maxCount > 0 {
		parts = append(parts, `SELECT account_id, node_id FROM versions WHERE keep_forever = 0
			GROUP BY account_id, node_id HAVING COUNT(*) > ?`)
		args = append(args, maxCount)
	}
	if ageCutoff != nil {
		parts = append(parts, `SELECT account_id, node_id FROM versions WHERE keep_forever = 0 AND replaced_at <= ?`)
		args = append(args, millis(*ageCutoff))
	}
	if len(parts) == 0 {
		return nil, nil
	}
	query := ""
	for i, p := range parts {
		if i > 0 {
			query += " UNION "
		}
		query += p
	}
	args = append(args, limit)
	rows, err := s.r.QueryContext(ctx, query+" LIMIT ?", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeVersionTarget
	for rows.Next() {
		var t NodeVersionTarget
		if err := rows.Scan(&t.AccountID, &t.NodeID); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
