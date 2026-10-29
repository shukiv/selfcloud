// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Upload is a resumable upload in progress or complete but not yet committed.
type Upload struct {
	ID            string
	AccountID     string
	FolderID      string
	ParentID      string // target directory by ID; "" when DirPath is used
	DirPath       string // target directory by path under the folder's root
	Parents       bool   // create missing directories on completion
	Name          string
	NameKey       string
	OnConflict    string // fail, rename or replace
	MimeType      string
	SHA256        string // expected whole-file hex from the metadata; "" when none was sent
	Length        int64
	DurableOffset int64
	HashState     []byte // SHA-256 state at DurableOffset; nil at offset 0
	BlobID        string
	CreatedBy     string
	CreatedKind   string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	ExpiresAt     time.Time
	// The fields below belong to uploads through a drop link (slice o3). The name and email are what the
	// visitor typed. VisitorID is the visitor the upload belongs to; it is "" for any other upload.
	UploaderName  string
	UploaderEmail string
	VisitorID     string
}

const uploadCols = `id, account_id, folder_id, parent_id, dir_path, parents, name, name_key, on_conflict,
	mime_type, sha256, length, durable_offset, hash_state, blob_id, created_by, created_kind,
	created_at, updated_at, expires_at, uploader_name, uploader_email, visitor_id`

func scanUpload(row interface{ Scan(...any) error }) (*Upload, error) {
	var u Upload
	var parent sql.NullString
	var parents int64
	var state []byte
	var created, updated, expires int64
	err := row.Scan(&u.ID, &u.AccountID, &u.FolderID, &parent, &u.DirPath, &parents, &u.Name, &u.NameKey,
		&u.OnConflict, &u.MimeType, &u.SHA256, &u.Length, &u.DurableOffset, &state, &u.BlobID,
		&u.CreatedBy, &u.CreatedKind, &created, &updated, &expires, &u.UploaderName, &u.UploaderEmail, &u.VisitorID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.ParentID = parent.String
	u.Parents = parents != 0
	u.HashState = state
	u.CreatedAt = fromMillis(created)
	u.UpdatedAt = fromMillis(updated)
	u.ExpiresAt = fromMillis(expires)
	return &u, nil
}

// Upload returns an upload of accountID by ID, or ErrNotFound.
func (s *Store) Upload(ctx context.Context, accountID, id string) (*Upload, error) {
	return scanUpload(s.r.QueryRowContext(ctx, "SELECT "+uploadCols+" FROM uploads WHERE account_id = ? AND id = ?",
		accountID, id))
}

// Upload returns an upload of accountID by ID, inside the transaction.
func (tx *Tx) Upload(ctx context.Context, accountID, id string) (*Upload, error) {
	return scanUpload(tx.q.QueryRowContext(ctx, "SELECT "+uploadCols+" FROM uploads WHERE account_id = ? AND id = ?",
		accountID, id))
}

// InsertUpload adds an upload.
func (tx *Tx) InsertUpload(ctx context.Context, u *Upload) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO uploads (`+uploadCols+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.AccountID, u.FolderID, nullString(u.ParentID), u.DirPath, boolInt(u.Parents), u.Name, u.NameKey,
		u.OnConflict, u.MimeType, u.SHA256, u.Length, u.DurableOffset, u.HashState, u.BlobID, u.CreatedBy,
		u.CreatedKind, millis(u.CreatedAt), millis(u.UpdatedAt), millis(u.ExpiresAt), u.UploaderName,
		u.UploaderEmail, u.VisitorID)
	return err
}

// RecordUploadProgress sets a new durable offset, its hash state and expiry. It applies only while the
// offset is still fromOffset; ErrNotFound means the upload is gone or moved on.
func (tx *Tx) RecordUploadProgress(ctx context.Context, accountID, id string, fromOffset, offset int64,
	hashState []byte, at, expires time.Time) error {
	return rowsAffected(tx.q.ExecContext(ctx, `UPDATE uploads SET durable_offset = ?, hash_state = ?,
		updated_at = ?, expires_at = ? WHERE account_id = ? AND id = ? AND durable_offset = ?`,
		offset, hashState, millis(at), millis(expires), accountID, id, fromOffset))
}

// DeleteUpload removes an upload's row and releases the reservation it held, in the same
// transaction. Every path that ends an upload without completing it comes here: termination, a
// failed creation, a checksum failure, the sweep. ErrNotFound if there is no such row, in which
// case nothing is released.
//
// Completion does not come here: it moves the reservation into usage itself (see completeUpload).
func (tx *Tx) DeleteUpload(ctx context.Context, accountID, id string) error {
	var length int64
	err := tx.q.QueryRowContext(ctx, "SELECT CASE WHEN visitor_id = '' THEN length ELSE durable_offset END FROM uploads WHERE account_id = ? AND id = ?",
		accountID, id).Scan(&length)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := rowsAffected(tx.q.ExecContext(ctx, "DELETE FROM uploads WHERE account_id = ? AND id = ?",
		accountID, id)); err != nil {
		return err
	}
	return tx.AdjustUsage(ctx, accountID, UsageChange{Reserved: -length})
}

// ExpiredUploads lists up to limit uploads that have expired by now: their expiry is not after now,
// or they were created at or before createdBefore (upload.max_lifetime has passed).
func (s *Store) ExpiredUploads(ctx context.Context, now, createdBefore time.Time, limit int) ([]*Upload, error) {
	rows, err := s.r.QueryContext(ctx, "SELECT "+uploadCols+
		" FROM uploads WHERE expires_at <= ? OR created_at <= ? ORDER BY expires_at, id LIMIT ?",
		millis(now), millis(createdBefore), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Upload
	for rows.Next() {
		u, err := scanUpload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// DeleteExpiredUpload removes an upload only if it has still expired at now (or passed its
// lifetime, created at or before createdBefore), releasing its reservation, and reports whether it
// did. An upload a request refreshed in the meantime is left alone.
func (tx *Tx) DeleteExpiredUpload(ctx context.Context, accountID, id string, now, createdBefore time.Time) (bool, error) {
	const expired = "account_id = ? AND id = ? AND (expires_at <= ? OR created_at <= ?)"
	var length int64
	err := tx.q.QueryRowContext(ctx, "SELECT CASE WHEN visitor_id = '' THEN length ELSE durable_offset END FROM uploads WHERE "+expired,
		accountID, id, millis(now), millis(createdBefore)).Scan(&length)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	res, err := tx.q.ExecContext(ctx, "DELETE FROM uploads WHERE "+expired,
		accountID, id, millis(now), millis(createdBefore))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	return true, tx.AdjustUsage(ctx, accountID, UsageChange{Reserved: -length})
}

// CountOpenUploads counts the unfinished uploads a caller holds in an account. A drop link is the caller of its
// visitors' uploads, so this counts the link's uploads, whoever sent them.
func (tx *Tx) CountOpenUploads(ctx context.Context, accountID, createdBy, createdKind string) (int, error) {
	var n int
	err := tx.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM uploads
		WHERE account_id = ? AND created_by = ? AND created_kind = ?`, accountID, createdBy, createdKind).Scan(&n)
	return n, err
}

// UploadsCreatedBy lists the unfinished uploads a caller made in an account, inside the transaction.
func (tx *Tx) UploadsCreatedBy(ctx context.Context, accountID, createdBy, createdKind string) ([]*Upload, error) {
	rows, err := tx.q.QueryContext(ctx, "SELECT "+uploadCols+" FROM uploads WHERE account_id = ? AND created_by = ? AND created_kind = ?",
		accountID, createdBy, createdKind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Upload
	for rows.Next() {
		u, err := scanUpload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CountAccountUploads counts the unfinished uploads of an account, whoever made them.
func (tx *Tx) CountAccountUploads(ctx context.Context, accountID string) (int, error) {
	var n int
	err := tx.q.QueryRowContext(ctx, "SELECT COUNT(*) FROM uploads WHERE account_id = ?", accountID).Scan(&n)
	return n, err
}

// UnwrittenBytes returns the bytes that unfinished uploads still have to write: for accountID alone
// (mine), and summed over every account (total). The disk must hold them.
func (s *Store) UnwrittenBytes(ctx context.Context, accountID string) (mine, total int64, err error) {
	return unwrittenBytes(ctx, s.r, accountID)
}

// DropBytesHeld is the bytes an account's drop visitors have sent and not yet completed: the bytes on disk
// for them, as their uploads hold them. Drop uploads are the link's, not the account's, so the account's share of
// the disk counts them here, not in UnwrittenBytes.
func (s *Store) DropBytesHeld(ctx context.Context, accountID string) (int64, error) {
	var n int64
	err := s.r.QueryRowContext(ctx, `SELECT COALESCE(SUM(durable_offset), 0) FROM uploads
		WHERE account_id = ? AND visitor_id != ''`, accountID).Scan(&n)
	return n, err
}

// UnwrittenBytes is Store.UnwrittenBytes inside a transaction, so it sees uploads made in it.
func (tx *Tx) UnwrittenBytes(ctx context.Context, accountID string) (mine, total int64, err error) {
	return unwrittenBytes(ctx, tx.q, accountID)
}

func unwrittenBytes(ctx context.Context, q querier, accountID string) (mine, total int64, err error) {
	// A drop upload reserves only the bytes it has received, and its disk is charged as they arrive, so it has
	// no unwritten bytes to hold back (slice o3, reserve as received).
	err = q.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN account_id = ? THEN length - durable_offset END), 0),
		COALESCE(SUM(length - durable_offset), 0) FROM uploads WHERE visitor_id = ''`, accountID).Scan(&mine, &total)
	return mine, total, err
}

// UpdateUploadTarget sets where an upload's file will go. The caller checks access and the target.
func (tx *Tx) UpdateUploadTarget(ctx context.Context, u *Upload) error {
	return rowsAffected(tx.q.ExecContext(ctx, `UPDATE uploads SET parent_id = ?, dir_path = ?, parents = ?,
		name = ?, name_key = ?, on_conflict = ?, updated_at = ? WHERE account_id = ? AND id = ?`,
		nullString(u.ParentID), u.DirPath, boolInt(u.Parents), u.Name, u.NameKey, u.OnConflict,
		millis(u.UpdatedAt), u.AccountID, u.ID))
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
