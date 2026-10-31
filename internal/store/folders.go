// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
)

// Folder kinds, as the folders.kind column names them. A team space is shared by permissions. A
// personal folder belongs to one user ("My files") and is reached by that user, and by the account
// key.
const (
	KindTeam     = "team"
	KindPersonal = "personal"
)

// PersonalFolderName is the name shown for a personal folder. Its name key is not derived from the
// name: names.Clean refuses "/", so a personal folder's key is PersonalNameKey(userID), which no
// team space can have.
const PersonalFolderName = "My files"

// StartCursor is the folder cursor that sorts before every folder: a page that resumes from it begins
// with the oldest team space. The API uses it when the personal folder took the first slot of a page.
func StartCursor() string { return encodeCursor("0", "") }

// PersonalNameKey is the name key of a user's personal folder. It contains "/", which a folder name
// cannot, so it never equals a team space's key, and it is unique because user IDs are.
func PersonalNameKey(userID string) string { return "/personal/" + userID }

// Folder is a named top-level root inside an account, holding its own file tree.
type Folder struct {
	ID          string
	AccountID   string
	ExternalID  string // the client's own reference; "" when unset
	Name        string // NFC
	NameKey     string // case-folded NFC; unique within the account
	Kind        string // KindTeam or KindPersonal
	OwnerUserID string // the user of a personal folder; "" for a team space
	CreatedAt   time.Time
	UpdatedAt   time.Time
	// The settings below are for team spaces only; they stay false on a personal folder.
	// MembersOnly refuses a permission below the root to anyone who holds none on the root.
	MembersOnly bool
	// SharingManagersOnly keeps permission changes in the space to managers, whatever the nodes
	// say about editors sharing.
	SharingManagersOnly bool
	// NoDownloadForViewers applies the no-download rule to every node in the space.
	NoDownloadForViewers bool
	// AllowLinks is stored for the public-link slice, which enforces it. Nothing reads it yet.
	AllowLinks bool
}

const folderCols = "id, account_id, external_id, name, name_key, kind, owner_user_id, created_at, updated_at, " +
	"members_only, sharing_managers_only, no_download_for_viewers, allow_links"

func scanFolder(row interface{ Scan(...any) error }) (*Folder, error) {
	var f Folder
	var ext, owner sql.NullString
	var created, updated, membersOnly, managersOnly, noDownload, allowLinks int64
	err := row.Scan(&f.ID, &f.AccountID, &ext, &f.Name, &f.NameKey, &f.Kind, &owner, &created, &updated,
		&membersOnly, &managersOnly, &noDownload, &allowLinks)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	f.ExternalID = ext.String
	f.OwnerUserID = owner.String
	f.CreatedAt = fromMillis(created)
	f.UpdatedAt = fromMillis(updated)
	f.MembersOnly = membersOnly == 1
	f.SharingManagersOnly = managersOnly == 1
	f.NoDownloadForViewers = noDownload == 1
	f.AllowLinks = allowLinks == 1
	return &f, nil
}

// Folder returns the folder of accountID with the given ID. A folder of another account is
// ErrNotFound.
func (s *Store) Folder(ctx context.Context, accountID, id string) (*Folder, error) {
	return scanFolder(s.r.QueryRowContext(ctx,
		"SELECT "+folderCols+" FROM folders WHERE account_id = ? AND id = ? AND staged = 0", accountID, id))
}

// Folder returns the folder of accountID with the given ID.
func (tx *Tx) Folder(ctx context.Context, accountID, id string) (*Folder, error) {
	return scanFolder(tx.q.QueryRowContext(ctx,
		"SELECT "+folderCols+" FROM folders WHERE account_id = ? AND id = ? AND staged = 0", accountID, id))
}

// FolderByNameKey returns the folder of accountID with the given name key.
func (tx *Tx) FolderByNameKey(ctx context.Context, accountID, nameKey string) (*Folder, error) {
	return scanFolder(tx.q.QueryRowContext(ctx,
		"SELECT "+folderCols+" FROM folders WHERE account_id = ? AND name_key = ? AND staged = 0", accountID, nameKey))
}

// FolderByExternalID returns the folder of accountID with the given external ID.
func (tx *Tx) FolderByExternalID(ctx context.Context, accountID, externalID string) (*Folder, error) {
	return scanFolder(tx.q.QueryRowContext(ctx,
		"SELECT "+folderCols+" FROM folders WHERE account_id = ? AND external_id = ? AND staged = 0", accountID, externalID))
}

// PersonalFolder returns the personal folder of userID in accountID. It returns ErrNotFound for a
// user without one, which only a user made before migration 0009 can be until the backfill runs.
func (s *Store) PersonalFolder(ctx context.Context, accountID, userID string) (*Folder, error) {
	return scanFolder(s.r.QueryRowContext(ctx, "SELECT "+folderCols+" FROM folders WHERE account_id = ? AND staged = 0 AND "+
		"kind = 'personal' AND owner_user_id = ? AND staged = 0", accountID, userID))
}

// PersonalFolder returns the personal folder of userID in accountID, read inside the transaction.
func (tx *Tx) PersonalFolder(ctx context.Context, accountID, userID string) (*Folder, error) {
	return scanFolder(tx.q.QueryRowContext(ctx, "SELECT "+folderCols+" FROM folders WHERE account_id = ? AND staged = 0 AND "+
		"kind = 'personal' AND owner_user_id = ? AND staged = 0", accountID, userID))
}

// FolderFilter narrows a folder list. Empty fields do not narrow.
type FolderFilter struct {
	ExternalID string
	Kind       string // KindTeam or KindPersonal
	Hide       HideFilter
}

// Folders lists the folders of accountID, oldest first, narrowed by f.
func (s *Store) Folders(ctx context.Context, accountID string, f FolderFilter, p Page) ([]*Folder, string, error) {
	where := []string{"account_id = ?", "staged = 0"}
	args := []any{accountID}
	if f.ExternalID != "" {
		where = append(where, "external_id = ?")
		args = append(args, f.ExternalID)
	}
	if f.Kind != "" {
		where = append(where, "kind = ?")
		args = append(args, f.Kind)
	}
	hide, hargs := hideCond("", f.Hide)
	return s.pageFolders(ctx, strings.Join(where, " AND ")+hide, append(args, hargs...), p)
}

// pageFolders runs a folder query with the given condition and one page of the cursor.
func (s *Store) pageFolders(ctx context.Context, cond string, args []any, p Page) ([]*Folder, string, error) {
	if p.Cursor != "" {
		parts, err := decodeCursor(p.Cursor, 2)
		if err != nil {
			return nil, "", err
		}
		ms, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, "", ErrBadCursor
		}
		cond += " AND (created_at, id) > (?, ?)"
		args = append(args, ms, parts[1])
	}
	args = append(args, p.Limit+1)
	rows, err := s.r.QueryContext(ctx, "SELECT "+folderCols+" FROM folders WHERE staged = 0 AND "+cond+
		" ORDER BY created_at, id LIMIT ?", args...)
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

// ReachedFolder is a folder whose root the caller holds a permission on, with the highest rank of
// those permissions. The access code adds the implicit rules on top.
type ReachedFolder struct {
	Folder *Folder
	Rank   int
}

// RootReachedFolders lists the team spaces of accountID whose root userID holds a permission on,
// directly or through a group, one page at a time. A team space an owner reaches without a
// permission is not listed here; the caller adds those. Expired permissions are left out.
func (s *Store) RootReachedFolders(ctx context.Context, accountID, userID string, hide HideFilter, p Page) ([]ReachedFolder, string, error) {
	// A team space is listed when the user holds a permission on its root, or has opened an organisation link on the
	// root (its visit counts as a role there, as it does for a node below the root; see effectiveRank).
	args := []any{accountID, userID, userID, nowMillis()}
	cond := `f.account_id = ? AND f.kind = 'team' AND (EXISTS (
		SELECT 1 FROM nodes r JOIN permissions pm ON pm.node_id = r.id
		WHERE r.folder_id = f.id AND r.parent_id IS NULL AND ` + subjectPredicate + ` AND ` + liveClause + `)
		OR EXISTS (SELECT 1 FROM share_links sl JOIN share_link_visits v ON v.link_id = sl.id AND v.user_id = ?
		JOIN nodes ln ON ln.id = sl.node_id JOIN folders lf ON lf.id = ln.folder_id
		WHERE ln.folder_id = f.id AND ln.parent_id IS NULL AND sl.account_id = f.account_id AND ` + linkLiveSQL + `))`
	args = append(args, userID, nowMillis())
	hideSQL, hargs := hideCond("f.", hide)
	cond += hideSQL
	args = append(args, hargs...)
	folders, next, err := s.pageFoldersAliased(ctx, cond, args, p)
	if err != nil {
		return nil, "", err
	}
	out := make([]ReachedFolder, 0, len(folders))
	for _, f := range folders {
		rank, err := s.rootRank(ctx, f.AccountID, f.ID, userID)
		if err != nil {
			return nil, "", err
		}
		out = append(out, ReachedFolder{Folder: f, Rank: rank})
	}
	return out, next, nil
}

// pageFoldersAliased is pageFolders for a condition written against the alias f.
func (s *Store) pageFoldersAliased(ctx context.Context, cond string, args []any, p Page) ([]*Folder, string, error) {
	if p.Cursor != "" {
		parts, err := decodeCursor(p.Cursor, 2)
		if err != nil {
			return nil, "", err
		}
		ms, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, "", ErrBadCursor
		}
		cond += " AND (f.created_at, f.id) > (?, ?)"
		args = append(args, ms, parts[1])
	}
	args = append(args, p.Limit+1)
	rows, err := s.r.QueryContext(ctx, "SELECT "+prefixed("f.", folderCols)+" FROM folders f WHERE f.staged = 0 AND "+cond+
		" ORDER BY f.created_at, f.id LIMIT ?", args...)
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

// rootRank is the highest rank of the live permissions userID holds on a folder's root, direct or
// through a group.
func (s *Store) rootRank(ctx context.Context, accountID, folderID, userID string) (int, error) {
	var rank int
	err := s.r.QueryRowContext(ctx, `SELECT COALESCE(MAX(src.r), 0) FROM (
			SELECT `+fmt.Sprintf(roleRankSQL, "pm.role")+` AS r
			FROM nodes r JOIN permissions pm ON pm.node_id = r.id
			WHERE r.folder_id = ? AND r.parent_id IS NULL AND `+subjectPredicate+` AND `+liveClause+`
			UNION ALL
			SELECT `+fmt.Sprintf(roleRankSQL, "sl.role")+` AS r
			FROM share_links sl JOIN share_link_visits v ON v.link_id = sl.id AND v.user_id = ?
			JOIN nodes ln ON ln.id = sl.node_id JOIN folders lf ON lf.id = ln.folder_id
			WHERE ln.folder_id = ? AND ln.parent_id IS NULL AND sl.account_id = ? AND `+linkLiveSQL+`
		) src`,
		folderID, userID, userID, nowMillis(), userID, folderID, accountID, nowMillis()).Scan(&rank)
	return rank, err
}

// InsertFolder adds f and its root node in one transaction, so every folder has a root. An empty
// Kind is a team space. A personal folder's root is owned by its user, and the root's owner is
// otherwise the creator. It returns ErrConflict if the name key or the external ID is taken in the
// account, or if the user already has a personal folder.
func (tx *Tx) InsertFolder(ctx context.Context, f *Folder, by string) error {
	if f.Kind == "" {
		f.Kind = KindTeam
	}
	owner := f.OwnerUserID
	if f.Kind == KindTeam {
		owner = ""
	}
	// A new folder has no settings: a team space starts with every setting off, a personal folder
	// never takes one.
	_, err := tx.q.ExecContext(ctx, `INSERT INTO folders (`+folderCols+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, 0, 0)`,
		f.ID, f.AccountID, nullString(f.ExternalID), f.Name, f.NameKey, f.Kind, nullString(owner),
		millis(f.CreatedAt), millis(f.UpdatedAt))
	if isUniqueViolation(err) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	// The root has the empty name, which no child can have, and no parent. Its owner is the
	// personal folder's user, or the creator.
	rootOwner := by
	if owner != "" {
		rootOwner = owner
	}
	_, err = tx.q.ExecContext(ctx, `INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key,
		kind, created_at, modified_at, created_by, modified_by, owner_id)
		VALUES (?, ?, ?, NULL, '', '', 'dir', ?, ?, ?, ?, ?)`,
		id.New("nod"), f.AccountID, f.ID, millis(f.CreatedAt), millis(f.CreatedAt), by, by, rootOwner)
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

// DeleteFolder removes a folder of accountID, with its permissions and its file tree, and takes the
// size and count of its files off the account's usage. Usage changes in the same transaction
// as the delete, so the counters always match the files that remain.
//
// The blobs the files pointed at are not removed here. Copies share blobs, so a blob may still be
// in use elsewhere; the blob garbage collector (slice e) finds the orphaned ones by reference.
func (tx *Tx) DeleteFolder(ctx context.Context, accountID, folderID string) error {
	if err := tx.guardFolder(ctx, accountID, folderID); err != nil {
		return err
	}
	var bytes, files, reserved int64
	err := tx.q.QueryRowContext(ctx, `SELECT COALESCE(SUM(size), 0), COUNT(*) FROM nodes
		WHERE folder_id = ? AND account_id = ? AND kind = 'file'`, folderID, accountID).Scan(&bytes, &files)
	if err != nil {
		return err
	}
	// The versions of the folder's files go with them, and their bytes count toward usage.
	var versionBytes int64
	err = tx.q.QueryRowContext(ctx, `SELECT COALESCE(SUM(v.size), 0) FROM versions v
		JOIN nodes n ON n.id = v.node_id WHERE n.folder_id = ? AND v.account_id = ?`,
		folderID, accountID).Scan(&versionBytes)
	if err != nil {
		return err
	}
	bytes += versionBytes
	// An upload belongs to the folder of the directory it is aimed at (its folder_id), so the foreign keys
	// delete exactly the folder's uploads with it, and their reservations go here too.
	err = tx.q.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN visitor_id = '' THEN length ELSE durable_offset END), 0)
		FROM uploads WHERE account_id = ? AND folder_id = ?`, accountID, folderID).Scan(&reserved)
	if err != nil {
		return err
	}
	err = rowsAffected(tx.q.ExecContext(ctx, "DELETE FROM folders WHERE account_id = ? AND id = ?",
		accountID, folderID))
	if err != nil {
		return err
	}
	// The folder's counters went with its row; the account loses its share.
	return tx.settleAccount(ctx, accountID, UsageChange{Bytes: -bytes, Files: -files, Reserved: -reserved}, nil)
}

// CountFolderEntries returns how many files and directories a folder holds below its root. A
// personal folder with entries blocks its user's deletion unless the caller asks for them to go.
func (tx *Tx) CountFolderEntries(ctx context.Context, accountID, folderID string) (int64, error) {
	var n int64
	err := tx.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes
		WHERE account_id = ? AND folder_id = ? AND parent_id IS NOT NULL`, accountID, folderID).Scan(&n)
	return n, err
}

// RootNodeIDs returns the root node of each folder of accountID named in folderIDs, keyed by folder ID.
// A folder with no root is absent from the map. One query serves a whole page of folders.
func (s *Store) RootNodeIDs(ctx context.Context, accountID string, folderIDs []string) (map[string]string, error) {
	out := make(map[string]string, len(folderIDs))
	if len(folderIDs) == 0 {
		return out, nil
	}
	args := []any{accountID}
	marks := make([]string, len(folderIDs))
	for i, fid := range folderIDs {
		marks[i] = "?"
		args = append(args, fid)
	}
	rows, err := s.r.QueryContext(ctx, "SELECT folder_id, id FROM nodes WHERE account_id = ? AND parent_id IS NULL"+
		" AND folder_id IN ("+strings.Join(marks, ",")+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var fid, nid string
		if err := rows.Scan(&fid, &nid); err != nil {
			return nil, err
		}
		out[fid] = nid
	}
	return out, rows.Err()
}
