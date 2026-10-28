package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"selfcloud/internal/id"
)

// Grant levels, as the grants.level column names them.
const (
	LevelRead  = "read"
	LevelWrite = "write"
)

// Folder is a named top-level root inside an account, holding its own file tree.
type Folder struct {
	ID         string
	AccountID  string
	ExternalID string // the client's own reference; "" when unset
	Name       string // NFC
	NameKey    string // case-folded NFC; unique within the account
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

const folderCols = "id, account_id, external_id, name, name_key, created_at, updated_at"

func scanFolder(row interface{ Scan(...any) error }) (*Folder, error) {
	var f Folder
	var ext sql.NullString
	var created, updated int64
	err := row.Scan(&f.ID, &f.AccountID, &ext, &f.Name, &f.NameKey, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	f.ExternalID = ext.String
	f.CreatedAt = fromMillis(created)
	f.UpdatedAt = fromMillis(updated)
	return &f, nil
}

// Folder returns the folder of accountID with the given ID. A folder of another account is
// ErrNotFound.
func (s *Store) Folder(ctx context.Context, accountID, id string) (*Folder, error) {
	return scanFolder(s.r.QueryRowContext(ctx,
		"SELECT "+folderCols+" FROM folders WHERE account_id = ? AND id = ?", accountID, id))
}

// Folder returns the folder of accountID with the given ID.
func (tx *Tx) Folder(ctx context.Context, accountID, id string) (*Folder, error) {
	return scanFolder(tx.q.QueryRowContext(ctx,
		"SELECT "+folderCols+" FROM folders WHERE account_id = ? AND id = ?", accountID, id))
}

// FolderByNameKey returns the folder of accountID with the given name key.
func (tx *Tx) FolderByNameKey(ctx context.Context, accountID, nameKey string) (*Folder, error) {
	return scanFolder(tx.q.QueryRowContext(ctx,
		"SELECT "+folderCols+" FROM folders WHERE account_id = ? AND name_key = ?", accountID, nameKey))
}

// FolderByExternalID returns the folder of accountID with the given external ID.
func (tx *Tx) FolderByExternalID(ctx context.Context, accountID, externalID string) (*Folder, error) {
	return scanFolder(tx.q.QueryRowContext(ctx,
		"SELECT "+folderCols+" FROM folders WHERE account_id = ? AND external_id = ?", accountID, externalID))
}

// FolderFilter narrows a folder list. Empty fields do not narrow.
type FolderFilter struct {
	ExternalID string
}

// Folders lists the folders of accountID, oldest first, narrowed by f.
func (s *Store) Folders(ctx context.Context, accountID string, f FolderFilter, p Page) ([]*Folder, string, error) {
	where := []string{"account_id = ?"}
	args := []any{accountID}
	if f.ExternalID != "" {
		where = append(where, "external_id = ?")
		args = append(args, f.ExternalID)
	}
	if p.Cursor != "" {
		parts, err := decodeCursor(p.Cursor, 2)
		if err != nil {
			return nil, "", err
		}
		ms, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, "", ErrBadCursor
		}
		where = append(where, "(created_at, id) > (?, ?)")
		args = append(args, ms, parts[1])
	}
	args = append(args, p.Limit+1)
	rows, err := s.r.QueryContext(ctx, "SELECT "+folderCols+" FROM folders WHERE "+
		strings.Join(where, " AND ")+" ORDER BY created_at, id LIMIT ?", args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []*Folder
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > p.Limit {
		out = out[:p.Limit]
		last := out[len(out)-1]
		next = encodeCursor(strconv.FormatInt(millis(last.CreatedAt), 10), last.ID)
	}
	return out, next, nil
}

// InsertFolder adds f and its root node in one transaction, so every folder has a root. It
// returns ErrConflict if the name key or the external ID is taken in the account.
func (tx *Tx) InsertFolder(ctx context.Context, f *Folder, by string) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO folders (`+folderCols+`)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		f.ID, f.AccountID, nullString(f.ExternalID), f.Name, f.NameKey, millis(f.CreatedAt), millis(f.UpdatedAt))
	if isUniqueViolation(err) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	// The root has the empty name, which no child can have, and no parent.
	_, err = tx.q.ExecContext(ctx, `INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key,
		kind, created_at, modified_at, created_by, modified_by)
		VALUES (?, ?, ?, NULL, '', '', 'dir', ?, ?, ?, ?)`,
		id.New("nod"), f.AccountID, f.ID, millis(f.CreatedAt), millis(f.CreatedAt), by, by)
	return err
}

// UpdateFolder writes the name, external ID and update time of f. It returns ErrNotFound if the
// folder is not in the account, and ErrConflict if the name or external ID is taken.
func (tx *Tx) UpdateFolder(ctx context.Context, f *Folder) error {
	err := rowsAffected(tx.q.ExecContext(ctx, `UPDATE folders SET name = ?, name_key = ?, external_id = ?,
		updated_at = ? WHERE account_id = ? AND id = ?`,
		f.Name, f.NameKey, nullString(f.ExternalID), millis(f.UpdatedAt), f.AccountID, f.ID))
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

// DeleteFolder removes a folder of accountID, with its grants and its file tree, and takes the
// size and count of its files off the account's usage. Usage changes in the same transaction
// as the delete, so the counters always match the files that remain.
//
// The blobs the files pointed at are not removed here. Copies share blobs, so a blob may still be
// in use elsewhere; the blob garbage collector (slice e) finds the orphaned ones by reference.
func (tx *Tx) DeleteFolder(ctx context.Context, accountID, folderID string) error {
	var bytes, files int64
	err := tx.q.QueryRowContext(ctx, `SELECT COALESCE(SUM(size), 0), COUNT(*) FROM nodes
		WHERE folder_id = ? AND account_id = ? AND kind = 'file'`, folderID, accountID).Scan(&bytes, &files)
	if err != nil {
		return err
	}
	err = rowsAffected(tx.q.ExecContext(ctx, "DELETE FROM folders WHERE account_id = ? AND id = ?",
		accountID, folderID))
	if err != nil {
		return err
	}
	return tx.AdjustUsage(ctx, accountID, -bytes, -files)
}

// AdjustUsage adds deltaBytes and deltaFiles to the account's usage counters. Callers change
// usage only alongside the files they count, in the same transaction.
func (tx *Tx) AdjustUsage(ctx context.Context, accountID string, deltaBytes, deltaFiles int64) error {
	return rowsAffected(tx.q.ExecContext(ctx,
		"UPDATE accounts SET used_bytes = used_bytes + ?, used_files = used_files + ? WHERE id = ?",
		deltaBytes, deltaFiles, accountID))
}

// Grant shares a folder with a user at a level.
type Grant struct {
	FolderID  string
	UserID    string
	Level     string // LevelRead or LevelWrite
	CreatedAt time.Time
	CreatedBy string // the ID of the caller who made or last changed the grant
}

// SetGrant creates the grant or changes its level. The caller checks that the user is a member
// of the folder's account first.
func (tx *Tx) SetGrant(ctx context.Context, g *Grant) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO grants (folder_id, user_id, level, created_at, created_by)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (folder_id, user_id) DO UPDATE SET level = excluded.level`,
		g.FolderID, g.UserID, g.Level, millis(g.CreatedAt), g.CreatedBy)
	return err
}

// DeleteGrant removes the grant of a user on a folder. It returns ErrNotFound if there is none.
func (tx *Tx) DeleteGrant(ctx context.Context, folderID, userID string) error {
	return rowsAffected(tx.q.ExecContext(ctx,
		"DELETE FROM grants WHERE folder_id = ? AND user_id = ?", folderID, userID))
}

const grantCols = "folder_id, user_id, level, created_at, created_by"

func scanGrant(row interface{ Scan(...any) error }) (*Grant, error) {
	var g Grant
	var created int64
	err := row.Scan(&g.FolderID, &g.UserID, &g.Level, &created, &g.CreatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	g.CreatedAt = fromMillis(created)
	return &g, nil
}

// Grant returns the grant of a user on a folder. It returns ErrNotFound if there is none.
func (s *Store) Grant(ctx context.Context, folderID, userID string) (*Grant, error) {
	return scanGrant(s.r.QueryRowContext(ctx,
		"SELECT "+grantCols+" FROM grants WHERE folder_id = ? AND user_id = ?", folderID, userID))
}

// GrantsOfFolder lists the grants of a folder, oldest first.
func (s *Store) GrantsOfFolder(ctx context.Context, folderID string) ([]*Grant, error) {
	return s.queryGrants(ctx, "SELECT "+grantCols+" FROM grants WHERE folder_id = ? ORDER BY created_at, user_id",
		folderID)
}

// GrantsOfUser lists the grants a user holds in accountID, oldest first. The account join keeps
// a grant from another account out, even when a user ID is known.
func (s *Store) GrantsOfUser(ctx context.Context, accountID, userID string) ([]*Grant, error) {
	return s.queryGrants(ctx, `SELECT g.folder_id, g.user_id, g.level, g.created_at, g.created_by
		FROM grants g JOIN folders f ON f.id = g.folder_id
		WHERE f.account_id = ? AND g.user_id = ? ORDER BY g.created_at, g.folder_id`, accountID, userID)
}

func (s *Store) queryGrants(ctx context.Context, query string, args ...any) ([]*Grant, error) {
	rows, err := s.r.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Grant
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// FolderAccess is a folder a user may reach through a grant, with the level granted.
type FolderAccess struct {
	Folder *Folder
	Level  string
}

// GrantedFolders lists the folders of accountID that userID holds a grant on, with their levels.
func (s *Store) GrantedFolders(ctx context.Context, accountID, userID string) ([]FolderAccess, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT f.id, f.account_id, f.external_id, f.name, f.name_key,
		f.created_at, f.updated_at, g.level
		FROM grants g JOIN folders f ON f.id = g.folder_id
		WHERE f.account_id = ? AND g.user_id = ? ORDER BY f.created_at, f.id`, accountID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FolderAccess
	for rows.Next() {
		var f Folder
		var ext sql.NullString
		var created, updated int64
		var level string
		if err := rows.Scan(&f.ID, &f.AccountID, &ext, &f.Name, &f.NameKey, &created, &updated, &level); err != nil {
			return nil, err
		}
		f.ExternalID = ext.String
		f.CreatedAt = fromMillis(created)
		f.UpdatedAt = fromMillis(updated)
		out = append(out, FolderAccess{Folder: &f, Level: level})
	}
	return out, rows.Err()
}

// DeleteUserGrants removes every grant the user holds and returns how many. It runs when a user
// stops being a member, so the grants do not come back if the user becomes a member again.
func (tx *Tx) DeleteUserGrants(ctx context.Context, userID string) (int64, error) {
	res, err := tx.q.ExecContext(ctx, "DELETE FROM grants WHERE user_id = ?", userID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
