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

// Comment is a comment on a node, or a reply to a top-level comment of the same node. A deleted comment is a tombstone
// (DeletedAt set, Body empty) only while it has replies. AuthorID and ResolvedBy are "" once their user is deleted.
type Comment struct {
	ID         string
	AccountID  string
	NodeID     string
	ParentID   string // "" for a top-level comment
	AuthorID   string // "" once the author's user is deleted
	Body       string
	CreatedAt  time.Time
	EditedAt   *time.Time
	DeletedAt  *time.Time
	ResolvedAt *time.Time
	ResolvedBy string // "" when unresolved, or once the resolver's user is deleted
}

// commentCols is the column list every comment read uses, in the order scanComment reads it.
const commentCols = `id, account_id, node_id, COALESCE(parent_id, ''), COALESCE(author_id, ''), body, created_at,
	edited_at, deleted_at, resolved_at, COALESCE(resolved_by, '')`

// scanComment reads one row of commentCols. Extra destinations, such as a reply total, are read after the columns.
func scanComment(row interface{ Scan(...any) error }, extra ...any) (*Comment, error) {
	var c Comment
	var created int64
	var edited, deleted, resolved sql.NullInt64
	dest := append([]any{&c.ID, &c.AccountID, &c.NodeID, &c.ParentID, &c.AuthorID, &c.Body, &created,
		&edited, &deleted, &resolved, &c.ResolvedBy}, extra...)
	err := row.Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.CreatedAt = fromMillis(created)
	if edited.Valid {
		t := fromMillis(edited.Int64)
		c.EditedAt = &t
	}
	if deleted.Valid {
		t := fromMillis(deleted.Int64)
		c.DeletedAt = &t
	}
	if resolved.Valid {
		t := fromMillis(resolved.Int64)
		c.ResolvedAt = &t
	}
	return &c, nil
}

// InsertComment adds c. Its body must already be checked: the table holds what the rules allow.
func (tx *Tx) InsertComment(ctx context.Context, c *Comment) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO comments
		(id, account_id, node_id, parent_id, author_id, body, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.AccountID, c.NodeID, nullString(c.ParentID), nullString(c.AuthorID), c.Body, millis(c.CreatedAt))
	return err
}

// commentByIDSQL reads one comment by its ID in its account.
const commentByIDSQL = "SELECT " + commentCols + " FROM comments WHERE account_id = ? AND id = ?"

// CommentIn returns the comment with the given ID in accountID, or ErrNotFound. A comment of another account is not
// found, so a comment ID names nothing outside its account.
func (tx *Tx) CommentIn(ctx context.Context, accountID, id string) (*Comment, error) {
	return scanComment(tx.q.QueryRowContext(ctx, commentByIDSQL, accountID, id))
}

// CommentIn is Tx.CommentIn read outside a transaction.
func (s *Store) CommentIn(ctx context.Context, accountID, id string) (*Comment, error) {
	return scanComment(s.r.QueryRowContext(ctx, commentByIDSQL, accountID, id))
}

// UpdateCommentBody sets the body of a live comment and its edit time.
func (tx *Tx) UpdateCommentBody(ctx context.Context, accountID, id, body string, at time.Time) error {
	return rowsAffected(tx.q.ExecContext(ctx, `UPDATE comments SET body = ?, edited_at = ?
		WHERE account_id = ? AND id = ? AND deleted_at IS NULL`, body, millis(at), accountID, id))
}

// SetResolved sets a top-level comment resolved by user, at at, or clears it when by is "". The caller checks it is top
// level and live.
func (tx *Tx) SetResolved(ctx context.Context, accountID, id, by string, at time.Time) error {
	if by == "" {
		return rowsAffected(tx.q.ExecContext(ctx, `UPDATE comments SET resolved_at = NULL, resolved_by = NULL
			WHERE account_id = ? AND id = ? AND parent_id IS NULL AND deleted_at IS NULL`, accountID, id))
	}
	return rowsAffected(tx.q.ExecContext(ctx, `UPDATE comments SET resolved_at = ?, resolved_by = ?
		WHERE account_id = ? AND id = ? AND parent_id IS NULL AND deleted_at IS NULL`, millis(at), by, accountID, id))
}

// hasRepliesSQL is the existence test of HasReplies.
const hasRepliesSQL = `SELECT EXISTS (SELECT 1 FROM comments WHERE node_id = ? AND parent_id = ? AND account_id = ?)`

// HasReplies reports whether comment parentID of nodeID has any reply. The node is in the query so the index on
// (node_id, parent_id) answers it.
func (tx *Tx) HasReplies(ctx context.Context, accountID, nodeID, parentID string) (bool, error) {
	var n int
	err := tx.q.QueryRowContext(ctx, hasRepliesSQL, nodeID, parentID, accountID).Scan(&n)
	return n == 1, err
}

// TombstoneComment turns a top-level comment with replies into a tombstone: no body, no resolution, and the time it
// was deleted.
func (tx *Tx) TombstoneComment(ctx context.Context, accountID, id string, at time.Time) error {
	return rowsAffected(tx.q.ExecContext(ctx, `UPDATE comments SET body = '', deleted_at = ?, resolved_at = NULL,
		resolved_by = NULL WHERE account_id = ? AND id = ? AND deleted_at IS NULL`, millis(at), accountID, id))
}

// RemoveComment deletes c's row. A reply whose parent is a tombstone with no other reply takes the tombstone with it,
// so a tombstone is kept only while it has replies. Its replies and mentions go with it by cascade.
func (tx *Tx) RemoveComment(ctx context.Context, c *Comment) error {
	if err := rowsAffected(tx.q.ExecContext(ctx, "DELETE FROM comments WHERE account_id = ? AND id = ?",
		c.AccountID, c.ID)); err != nil {
		return err
	}
	if c.ParentID == "" {
		return nil
	}
	parent, err := tx.CommentIn(ctx, c.AccountID, c.ParentID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if parent.DeletedAt == nil {
		return nil
	}
	again, err := tx.HasReplies(ctx, c.AccountID, c.NodeID, parent.ID)
	if err != nil || again {
		return err
	}
	return rowsAffected(tx.q.ExecContext(ctx, "DELETE FROM comments WHERE account_id = ? AND id = ?",
		c.AccountID, parent.ID))
}

// liveCountSQL counts the live comments of a node, up to the limit it is given.
const liveCountSQL = `SELECT COUNT(*) FROM (SELECT 1 FROM comments
	WHERE account_id = ? AND node_id = ? AND deleted_at IS NULL LIMIT ?)`

// CountLiveComments counts the live comments of a node, reading at most limit rows, so a count of a huge node stops at
// limit. The caller refuses a write when the count reaches its cap.
func (tx *Tx) CountLiveComments(ctx context.Context, accountID, nodeID string, limit int) (int, error) {
	var n int
	err := tx.q.QueryRowContext(ctx, liveCountSQL, accountID, nodeID, limit).Scan(&n)
	return n, err
}

// topLevelSQL is the query of TopLevelComments, with the cursor clause when cursored. It is a function so the plan test
// reads the statement the store runs.
func topLevelSQL(cursored bool) string {
	q := "SELECT " + commentCols + " FROM comments WHERE account_id = ? AND node_id = ? AND parent_id IS NULL"
	if cursored {
		q += " AND (created_at < ? OR (created_at = ? AND id < ?))"
	}
	return q + " ORDER BY created_at DESC, id DESC LIMIT ?"
}

// TopLevelComments lists the top-level comments of a node, newest first, p.Limit at a time, after the cursor if one is
// given. A tombstone is listed only while it has replies, which the delete rule keeps true.
func (s *Store) TopLevelComments(ctx context.Context, accountID, nodeID string, p Page) ([]*Comment, error) {
	args := []any{accountID, nodeID}
	if p.Cursor != "" {
		at, id, err := parseCommentCursor(p.Cursor)
		if err != nil {
			return nil, err
		}
		args = append(args, at, at, id)
	}
	args = append(args, p.Limit)
	return s.queryComments(ctx, topLevelSQL(p.Cursor != ""), args...)
}

// threadSQL is the query of ThreadReplies, with the cursor clause when cursored.
func threadSQL(cursored bool) string {
	q := "SELECT " + commentCols + " FROM comments WHERE account_id = ? AND node_id = ? AND parent_id = ?"
	if cursored {
		q += " AND (created_at > ? OR (created_at = ? AND id > ?))"
	}
	return q + " ORDER BY created_at, id LIMIT ?"
}

// ThreadReplies lists the replies of one top-level comment, oldest first, p.Limit at a time, after the cursor if one is
// given.
func (s *Store) ThreadReplies(ctx context.Context, accountID, nodeID, parentID string, p Page) ([]*Comment, error) {
	args := []any{accountID, nodeID, parentID}
	if p.Cursor != "" {
		at, id, err := parseCommentCursor(p.Cursor)
		if err != nil {
			return nil, err
		}
		args = append(args, at, at, id)
	}
	args = append(args, p.Limit)
	return s.queryComments(ctx, threadSQL(p.Cursor != ""), args...)
}

// ReplyPreview is the first replies of one top-level comment, and how many replies it has in all.
type ReplyPreview struct {
	Replies []*Comment
	Total   int
}

// previewSQL is the query of ReplyPreviews for n parents. A window numbers the replies of each parent and counts them,
// so one query gives the first replies and the total.
func previewSQL(n int) string {
	return `SELECT ` + commentCols + `, total FROM (
		SELECT *, COUNT(*) OVER (PARTITION BY parent_id) AS total,
			ROW_NUMBER() OVER (PARTITION BY parent_id ORDER BY created_at, id) AS rn
		FROM comments WHERE account_id = ? AND node_id = ? AND parent_id IN (` + inList(n) + `)
	) WHERE rn <= ? ORDER BY parent_id, created_at, id`
}

// inList is n placeholders for an IN list.
func inList(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// ReplyPreviews returns, for each top-level comment in parentIDs, its first perParent replies and its reply count. One
// query reads them all. The rows read are the replies of the parents, which the live cap of a node bounds.
func (s *Store) ReplyPreviews(ctx context.Context, accountID, nodeID string, parentIDs []string, perParent int) (map[string]*ReplyPreview, error) {
	out := map[string]*ReplyPreview{}
	if len(parentIDs) == 0 {
		return out, nil
	}
	args := []any{accountID, nodeID}
	for _, id := range parentIDs {
		args = append(args, id)
	}
	args = append(args, perParent)
	rows, err := s.r.QueryContext(ctx, previewSQL(len(parentIDs)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var total int
		c, err := scanComment(rows, &total)
		if err != nil {
			return nil, err
		}
		pv := out[c.ParentID]
		if pv == nil {
			pv = &ReplyPreview{Total: total}
			out[c.ParentID] = pv
		}
		pv.Replies = append(pv.Replies, c)
	}
	return out, rows.Err()
}

// InsertMentions stores one mention row per user for a comment. The users are checked by the caller.
func (tx *Tx) InsertMentions(ctx context.Context, accountID, commentID string, userIDs []string) error {
	for _, u := range userIDs {
		if _, err := tx.q.ExecContext(ctx, `INSERT INTO mentions (comment_id, user_id, account_id) VALUES (?, ?, ?)`,
			commentID, u, accountID); err != nil {
			return err
		}
	}
	return nil
}

// mentionsSQL is the query of MentionedUsers for n comments.
func mentionsSQL(n int) string {
	return `SELECT comment_id, user_id FROM mentions
		WHERE account_id = ? AND comment_id IN (` + inList(n) + `) ORDER BY comment_id, user_id`
}

// MentionedUsers returns, for each comment in commentIDs, the users it names. A comment with no mention is absent.
func (s *Store) MentionedUsers(ctx context.Context, accountID string, commentIDs []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(commentIDs) == 0 {
		return out, nil
	}
	args := []any{accountID}
	for _, id := range commentIDs {
		args = append(args, id)
	}
	rows, err := s.r.QueryContext(ctx, mentionsSQL(len(commentIDs)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c, u string
		if err := rows.Scan(&c, &u); err != nil {
			return nil, err
		}
		out[c] = append(out[c], u)
	}
	return out, rows.Err()
}

// CommentsOn reports whether the account's comments are on. It is ErrNotFound for an account that does not exist.
func (s *Store) CommentsOn(ctx context.Context, accountID string) (bool, error) {
	return commentsOn(ctx, s.r, accountID)
}

// CommentsOn is Store.CommentsOn read inside the transaction, so a write sees the switch as it is then.
func (tx *Tx) CommentsOn(ctx context.Context, accountID string) (bool, error) {
	return commentsOn(ctx, tx.q, accountID)
}

// commentsOn reads the account's comment switch through q.
func commentsOn(ctx context.Context, q querier, accountID string) (bool, error) {
	var on int64
	err := q.QueryRowContext(ctx, "SELECT comments_on FROM accounts WHERE id = ?", accountID).Scan(&on)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	return on == 1, err
}

// SetCommentsOn sets the account's comment switch. Turning it off keeps every comment: a switch on again shows them.
func (tx *Tx) SetCommentsOn(ctx context.Context, accountID string, on bool, at time.Time) error {
	return rowsAffected(tx.q.ExecContext(ctx, "UPDATE accounts SET comments_on = ?, updated_at = ? WHERE id = ?",
		boolInt(on), millis(at), accountID))
}

// NamedUser is an active user of an account and its display name.
type NamedUser struct {
	ID   string
	Name string
}

// activeNamedSQL is the query of ActiveUsersNamed. The account is the users' index prefix.
const activeNamedSQL = `SELECT id, display_name FROM users
	WHERE account_id = ? AND status = ? AND display_name <> '' AND display_name LIKE ? ESCAPE '\'
	ORDER BY display_name, id LIMIT ?`

// ActiveUsersNamed returns up to limit active users of accountID whose display name contains text, by name. The text is
// matched literally: its % and _ and \ are escaped. SQLite's LIKE folds ASCII case only. The caller checks each user's
// reach to a node: this lists people of the account, not people who may see anything.
func (s *Store) ActiveUsersNamed(ctx context.Context, accountID, text string, limit int) ([]NamedUser, error) {
	pattern := "%" + likeEscaper.Replace(text) + "%"
	rows, err := s.r.QueryContext(ctx, activeNamedSQL, accountID, UserActive, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NamedUser
	for rows.Next() {
		var u NamedUser
		if err := rows.Scan(&u.ID, &u.Name); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// queryComments runs a query of commentCols and reads the rows.
func (s *Store) queryComments(ctx context.Context, query string, args ...any) ([]*Comment, error) {
	rows, err := s.r.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Comment
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CommentCursor is the cursor that resumes a TopLevelComments page after comment c.
func CommentCursor(c *Comment) string {
	return encodeCursor(strconv.FormatInt(millis(c.CreatedAt), 10), c.ID)
}

// parseCommentCursor reads a cursor made by CommentCursor.
func parseCommentCursor(c string) (int64, string, error) {
	parts, err := decodeCursor(c, 2)
	if err != nil {
		return 0, "", err
	}
	at, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, "", ErrBadCursor
	}
	return at, parts[1], nil
}
