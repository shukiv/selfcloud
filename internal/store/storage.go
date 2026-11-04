// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
)

// ErrBadKind is a file kind a largest-files list cannot be narrowed to.
var ErrBadKind = errors.New("store: unknown file kind")

// scanFunc adapts a row scanner, so a folder can be scanned together with columns after its own.
type scanFunc func(...any) error

// Scan implements the row interface scanFolder reads.
func (f scanFunc) Scan(dest ...any) error { return f(dest...) }

// SetUserQuotaDefault sets the limit each user gets when its own limit follows the account's default. 0 is
// no limit.
func (tx *Tx) SetUserQuotaDefault(ctx context.Context, accountID string, bytes int64, at time.Time) error {
	var old int64
	if err := tx.q.QueryRowContext(ctx, "SELECT user_quota_bytes FROM accounts WHERE id = ?", accountID).Scan(&old); err != nil &&
		!errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := rowsAffected(tx.q.ExecContext(ctx, "UPDATE accounts SET user_quota_bytes = ?, updated_at = ? WHERE id = ?",
		bytes, millis(at), accountID)); err != nil || old == bytes {
		return err
	}
	// A changed default clears the marks of the users whose limit follows it (webhook_quota.go).
	return tx.forgetDefaultUserQuotaMarks(ctx, accountID)
}

// StorageUser is a user of an account with the counters of its personal folder.
type StorageUser struct {
	ID          string
	DisplayName string
	Email       string
	Status      string
	FolderID    string // "" for a user without a personal folder
	Usage       Usage
	QuotaBytes  *int64 // the user's own limit; nil follows the account default
	// EffectiveQuota is the limit that applies: the user's own, or the account default; 0 = none.
	EffectiveQuota int64
}

// StorageUsers lists the users of accountID, the heaviest first (by used bytes, then ID), one page at a time.
// Each carries its personal folder's counters, read in the same query.
func (s *Store) StorageUsers(ctx context.Context, accountID string, p Page) ([]StorageUser, string, error) {
	args := []any{accountID}
	cond := ""
	if p.Cursor != "" {
		parts, err := decodeCursor(p.Cursor, 2)
		if err != nil {
			return nil, "", err
		}
		used, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, "", ErrBadCursor
		}
		cond = " AND (COALESCE(f.used_bytes, 0), u.id) < (?, ?)"
		args = append(args, used, parts[1])
	}
	args = append(args, p.Limit+1)
	rows, err := s.r.QueryContext(ctx, `SELECT u.id, u.display_name, u.email, u.status, COALESCE(f.id, ''),
		COALESCE(f.used_bytes, 0), COALESCE(f.used_files, 0), COALESCE(f.reserved_bytes, 0), u.quota_bytes,
		COALESCE(u.quota_bytes, a.user_quota_bytes)
		FROM users u JOIN accounts a ON a.id = u.account_id
		LEFT JOIN folders f ON f.owner_user_id = u.id AND f.kind = 'personal' AND f.account_id = u.account_id
		WHERE u.account_id = ?`+cond+` ORDER BY COALESCE(f.used_bytes, 0) DESC, u.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []StorageUser
	for rows.Next() {
		var su StorageUser
		var quota *int64
		if err := rows.Scan(&su.ID, &su.DisplayName, &su.Email, &su.Status, &su.FolderID,
			&su.Usage.Bytes, &su.Usage.Files, &su.Usage.Reserved, &quota, &su.EffectiveQuota); err != nil {
			return nil, "", err
		}
		su.QuotaBytes = quota
		out = append(out, su)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > p.Limit {
		out = out[:p.Limit]
		last := out[len(out)-1]
		next = encodeCursor(strconv.FormatInt(last.Usage.Bytes, 10), last.ID)
	}
	return out, next, nil
}

// FolderTotal is a folder with its counters.
type FolderTotal struct {
	Folder *Folder
	Usage  Usage
}

// FolderTotals lists the folders of accountID with their counters, oldest first. teamOnly leaves out the
// personal folders.
func (s *Store) FolderTotals(ctx context.Context, accountID string, teamOnly bool) ([]FolderTotal, error) {
	q := "SELECT " + folderCols + ", used_bytes, used_files, reserved_bytes FROM folders WHERE account_id = ? AND staged = 0"
	if teamOnly {
		q += " AND kind = 'team'"
	}
	rows, err := s.r.QueryContext(ctx, q+" ORDER BY created_at, id", accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FolderTotal
	for rows.Next() {
		var t FolderTotal
		f, err := scanFolder(scanFunc(func(dest ...any) error {
			return rows.Scan(append(dest, &t.Usage.Bytes, &t.Usage.Files, &t.Usage.Reserved)...)
		}))
		if err != nil {
			return nil, err
		}
		t.Folder = f
		out = append(out, t)
	}
	return out, rows.Err()
}

// LargeFile is a file with the bytes its versions hold, whether it is in the trash, its media type, and the name of
// the folder it sits in ("" at the top of a folder).
type LargeFile struct {
	ID           string
	FolderID     string
	FolderName   string
	Name         string
	Size         int64
	VersionBytes int64
	InTrash      bool
	ModifiedAt   time.Time
	MimeType     string
	ParentName   string
}

// fileKindCond is the SQL condition of each file kind the Storage page filters by (decision 79). The media type is
// read without its parameters and in lower case; the sets are the ones the interface uses to classify a file
// (web/src/lib/files/kind.ts). "trash" is a condition on the trash, not on the media type. A kind not in the map
// is refused before this is used, so the strings below are the only SQL a kind adds.
var fileKindCond = map[string]string{
	"audio": `{base} LIKE 'audio/%'`,
	"video": `{base} LIKE 'video/%'`,
	"image": `{base} IN ('image/jpeg', 'image/png', 'image/gif', 'image/webp', 'image/avif', 'image/svg+xml')`,
	"document": `({base} = 'application/pdf' OR {base} LIKE 'text/%' OR {base} IN ('application/json', 'application/xml',
		'application/javascript', 'application/x-yaml', 'application/yaml', 'application/toml', 'application/x-sh',
		'application/sql', 'application/x-httpd-php', 'application/msword',
		'application/vnd.openxmlformats-officedocument.wordprocessingml.document', 'application/vnd.ms-excel',
		'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet', 'application/vnd.ms-powerpoint',
		'application/vnd.openxmlformats-officedocument.presentationml.presentation',
		'application/vnd.oasis.opendocument.text', 'application/vnd.oasis.opendocument.spreadsheet',
		'application/vnd.oasis.opendocument.presentation'))`,
	"trash": `n.id IN (SELECT nid FROM trashed)`,
}

// baseMimeSQL is a file's media type without its parameters, in lower case. It replaces {base} in fileKindCond.
const baseMimeSQL = `lower(CASE WHEN instr(n.mime_type, ';') > 0 THEN substr(n.mime_type, 1, instr(n.mime_type, ';') - 1) ELSE n.mime_type END)`

// FileKindOK reports whether kind is one a largest-files list can be narrowed to ("" is every kind).
func FileKindOK(kind string) bool {
	_, ok := fileKindCond[kind]
	return kind == "" || ok
}

// LargestFiles lists the files of accountID, largest first (by size, then ID), one page at a time. folderID
// narrows it to one folder; with "" the kind narrows it: KindTeam for team spaces, "" for every folder. The
// page is picked in one query, and its versions and trash marks are read for that page only.
func (s *Store) LargestFiles(ctx context.Context, accountID, folderID, kind string, p Page) ([]LargeFile, string, error) {
	return s.LargestFilesOf(ctx, accountID, folderID, kind, "", p)
}

// LargestFilesOf is LargestFiles with a file kind as well (fileKind, one of fileKindCond's keys, or "" for any).
func (s *Store) LargestFilesOf(ctx context.Context, accountID, folderID, kind, fileKind string, p Page) ([]LargeFile, string, error) {
	if !FileKindOK(fileKind) {
		return nil, "", ErrBadKind
	}
	var args []any
	cond := ""
	if folderID != "" {
		cond += " AND n.folder_id = ?"
		args = append(args, folderID)
	}
	if kind != "" {
		cond += " AND f.kind = ?"
		args = append(args, kind)
	}
	if fileKind != "" {
		cond += " AND " + strings.ReplaceAll(fileKindCond[fileKind], "{base}", baseMimeSQL)
	}
	if p.Cursor != "" {
		parts, err := decodeCursor(p.Cursor, 2)
		if err != nil {
			return nil, "", err
		}
		size, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, "", ErrBadCursor
		}
		cond += " AND (n.size, n.id) < (?, ?)"
		args = append(args, size, parts[1])
	}
	args = append(args, p.Limit+1)
	rows, err := s.r.QueryContext(ctx, `WITH RECURSIVE trashed(nid) AS (
			SELECT id FROM nodes WHERE account_id = ? AND trashed_at IS NOT NULL
			UNION ALL
			SELECT c.id FROM nodes c JOIN trashed t ON c.parent_id = t.nid WHERE c.account_id = ?
		), page AS (
			SELECT n.id, n.folder_id, n.parent_id, n.name, n.size, n.modified_at, n.mime_type,
				n.id IN (SELECT nid FROM trashed) AS trashed
			FROM nodes n JOIN folders f ON f.id = n.folder_id AND f.staged = 0
			WHERE n.account_id = ? AND n.kind = 'file'`+cond+`
			ORDER BY n.size DESC, n.id DESC LIMIT ?
		)
		SELECT pg.id, pg.folder_id, f.name, pg.name, pg.size,
			(SELECT COALESCE(SUM(v.size), 0) FROM versions v WHERE v.node_id = pg.id),
			pg.modified_at, pg.trashed, COALESCE(pg.mime_type, ''), COALESCE(par.name, '')
		FROM page pg JOIN folders f ON f.id = pg.folder_id AND f.staged = 0
		LEFT JOIN nodes par ON par.id = pg.parent_id
		ORDER BY pg.size DESC, pg.id DESC`,
		append([]any{accountID, accountID, accountID}, args...)...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []LargeFile
	for rows.Next() {
		var lf LargeFile
		var modified int64
		var trashed int64
		if err := rows.Scan(&lf.ID, &lf.FolderID, &lf.FolderName, &lf.Name, &lf.Size, &lf.VersionBytes,
			&modified, &trashed, &lf.MimeType, &lf.ParentName); err != nil {
			return nil, "", err
		}
		lf.ModifiedAt = fromMillis(modified)
		lf.InTrash = trashed == 1
		out = append(out, lf)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > p.Limit {
		out = out[:p.Limit]
		last := out[len(out)-1]
		next = encodeCursor(strconv.FormatInt(last.Size, 10), last.ID)
	}
	return out, next, nil
}

// StorageBreakdown splits a folder's used bytes (decision 79): the old versions of its files that are not in the
// trash, and the trash, which is the files in it with their own versions. The rest of the used bytes are the files
// that are not in the trash.
type StorageBreakdown struct {
	VersionsBytes int64
	VersionsFiles int64
	TrashBytes    int64
	TrashFiles    int64
}

// PersonalBreakdown reads the breakdown of folderID in accountID, in one query. The trash is every node below a
// trashed node, as LargestFiles reads it. A folder with no trash and no versions gives zeros.
func (s *Store) PersonalBreakdown(ctx context.Context, accountID, folderID string) (StorageBreakdown, error) {
	var b StorageBreakdown
	err := s.r.QueryRowContext(ctx, `WITH RECURSIVE trashed(nid) AS (
			SELECT id FROM nodes WHERE account_id = ?1 AND folder_id = ?2 AND trashed_at IS NOT NULL
			UNION ALL
			SELECT c.id FROM nodes c JOIN trashed t ON c.parent_id = t.nid WHERE c.account_id = ?1
		)
		SELECT
			(SELECT COALESCE(SUM(v.size), 0) FROM versions v JOIN nodes n ON n.id = v.node_id
				WHERE n.account_id = ?1 AND n.folder_id = ?2 AND n.id NOT IN (SELECT nid FROM trashed)),
			(SELECT COUNT(DISTINCT v.node_id) FROM versions v JOIN nodes n ON n.id = v.node_id
				WHERE n.account_id = ?1 AND n.folder_id = ?2 AND n.id NOT IN (SELECT nid FROM trashed)),
			(SELECT COALESCE(SUM(n.size), 0) FROM nodes n
				WHERE n.account_id = ?1 AND n.folder_id = ?2 AND n.kind = 'file' AND n.id IN (SELECT nid FROM trashed))
			+ (SELECT COALESCE(SUM(v.size), 0) FROM versions v JOIN nodes n ON n.id = v.node_id
				WHERE n.account_id = ?1 AND n.folder_id = ?2 AND n.id IN (SELECT nid FROM trashed)),
			(SELECT COUNT(*) FROM nodes n
				WHERE n.account_id = ?1 AND n.folder_id = ?2 AND n.kind = 'file' AND n.id IN (SELECT nid FROM trashed))`,
		accountID, folderID).Scan(&b.VersionsBytes, &b.VersionsFiles, &b.TrashBytes, &b.TrashFiles)
	return b, err
}

// FolderUsage returns a folder's counters: the bytes and files of its tree, and the bytes its uploads reserve.
// ErrNotFound if the folder is not in the account.
func (s *Store) FolderUsage(ctx context.Context, accountID, folderID string) (Usage, error) {
	var u Usage
	err := s.r.QueryRowContext(ctx, "SELECT used_bytes, used_files, reserved_bytes FROM folders WHERE account_id = ? AND id = ?",
		accountID, folderID).Scan(&u.Bytes, &u.Files, &u.Reserved)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}
