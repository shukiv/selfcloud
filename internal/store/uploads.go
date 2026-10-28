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
}

const uploadCols = `id, account_id, folder_id, parent_id, dir_path, parents, name, name_key, on_conflict,
	mime_type, sha256, length, durable_offset, hash_state, blob_id, created_by, created_kind,
	created_at, updated_at, expires_at`

func scanUpload(row interface{ Scan(...any) error }) (*Upload, error) {
	var u Upload
	var parent sql.NullString
	var parents int64
	var state []byte
	var created, updated, expires int64
	err := row.Scan(&u.ID, &u.AccountID, &u.FolderID, &parent, &u.DirPath, &parents, &u.Name, &u.NameKey,
		&u.OnConflict, &u.MimeType, &u.SHA256, &u.Length, &u.DurableOffset, &state, &u.BlobID,
		&u.CreatedBy, &u.CreatedKind, &created, &updated, &expires)
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
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.AccountID, u.FolderID, nullString(u.ParentID), u.DirPath, boolInt(u.Parents), u.Name, u.NameKey,
		u.OnConflict, u.MimeType, u.SHA256, u.Length, u.DurableOffset, u.HashState, u.BlobID, u.CreatedBy,
		u.CreatedKind, millis(u.CreatedAt), millis(u.UpdatedAt), millis(u.ExpiresAt))
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

// DeleteUpload removes an upload's row. ErrNotFound if there is none.
func (tx *Tx) DeleteUpload(ctx context.Context, accountID, id string) error {
	return rowsAffected(tx.q.ExecContext(ctx, "DELETE FROM uploads WHERE account_id = ? AND id = ?", accountID, id))
}

// ExpiredUploads lists up to limit uploads whose expiry is not after now.
func (s *Store) ExpiredUploads(ctx context.Context, now time.Time, limit int) ([]*Upload, error) {
	rows, err := s.r.QueryContext(ctx, "SELECT "+uploadCols+
		" FROM uploads WHERE expires_at <= ? ORDER BY expires_at, id LIMIT ?", millis(now), limit)
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

// DeleteExpiredUpload removes an upload only if it has still expired at now, and reports whether it
// did. An upload a request refreshed in the meantime is left alone.
func (tx *Tx) DeleteExpiredUpload(ctx context.Context, accountID, id string, now time.Time) (bool, error) {
	res, err := tx.q.ExecContext(ctx, "DELETE FROM uploads WHERE account_id = ? AND id = ? AND expires_at <= ?",
		accountID, id, millis(now))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// CountOpenUploads counts the unfinished uploads a caller holds in an account.
func (tx *Tx) CountOpenUploads(ctx context.Context, accountID, createdBy, createdKind string) (int, error) {
	var n int
	err := tx.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM uploads
		WHERE account_id = ? AND created_by = ? AND created_kind = ?`, accountID, createdBy, createdKind).Scan(&n)
	return n, err
}

// CountAccountUploads counts the unfinished uploads of an account, whoever made them.
func (tx *Tx) CountAccountUploads(ctx context.Context, accountID string) (int, error) {
	var n int
	err := tx.q.QueryRowContext(ctx, "SELECT COUNT(*) FROM uploads WHERE account_id = ?", accountID).Scan(&n)
	return n, err
}

// ReservedBytes is the sum of the bytes that unfinished uploads still have to write. The disk must
// hold them, whoever owns them.
func (s *Store) ReservedBytes(ctx context.Context) (int64, error) {
	var n int64
	err := s.r.QueryRowContext(ctx, "SELECT COALESCE(SUM(length - durable_offset), 0) FROM uploads").Scan(&n)
	return n, err
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
