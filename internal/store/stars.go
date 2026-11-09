// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// Star is a user's bookmark on a node. It is private to the user.
type Star struct {
	UserID    string
	NodeID    string
	AccountID string
	CreatedAt time.Time
}

// InsertStar stars node for user. A star that exists keeps its first time, so starring twice is a no-op.
func (tx *Tx) InsertStar(ctx context.Context, accountID, userID, nodeID string, at time.Time) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO stars (user_id, node_id, account_id, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (user_id, node_id) DO NOTHING`, userID, nodeID, accountID, millis(at))
	return err
}

// HasStar reports whether user has starred node.
func (tx *Tx) HasStar(ctx context.Context, userID, nodeID string) (bool, error) {
	var n int
	err := tx.q.QueryRowContext(ctx, "SELECT COUNT(*) FROM stars WHERE user_id = ? AND node_id = ?",
		userID, nodeID).Scan(&n)
	return n > 0, err
}

// CountStars returns how many stars user holds, listed or not.
func (tx *Tx) CountStars(ctx context.Context, userID string) (int, error) {
	var n int
	err := tx.q.QueryRowContext(ctx, "SELECT COUNT(*) FROM stars WHERE user_id = ?", userID).Scan(&n)
	return n, err
}

// DeleteStar removes user's star on node. A star that is not there is not an error.
func (tx *Tx) DeleteStar(ctx context.Context, userID, nodeID string) error {
	_, err := tx.q.ExecContext(ctx, "DELETE FROM stars WHERE user_id = ? AND node_id = ?", userID, nodeID)
	return err
}

// StarCursor is the cursor that resumes a StarsPage after star st.
func StarCursor(st Star) string {
	return encodeCursor(strconv.FormatInt(millis(st.CreatedAt), 10), st.NodeID)
}

// StarsPage lists user's stars, newest first, p.Limit at a time, after the cursor if one is given. Stars are not
// filtered here: the caller drops the ones it may not list, and pages on with StarCursor.
func (s *Store) StarsPage(ctx context.Context, userID string, p Page) ([]Star, error) {
	return s.StarsPageFiltered(ctx, userID, ListFilter{}, p)
}

// StarCursorOf is StarCursor for a list narrowed by filter f: the cursor carries the filter's tag (decision 110).
func StarCursorOf(st Star, f ListFilter) string {
	return encodeFilteredCursor(f, strconv.FormatInt(millis(st.CreatedAt), 10), st.NodeID)
}

// StarsPageFiltered is StarsPage narrowed by a list filter (decision 110). With a filter, each star is joined to its node,
// so the type and the modified range are the node's; with none, the query is the one StarsPage always ran.
func (s *Store) StarsPageFiltered(ctx context.Context, userID string, f ListFilter, p Page) ([]Star, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	query := `SELECT node_id, account_id, created_at FROM stars WHERE user_id = ?`
	args := []any{userID}
	if f.Active() {
		query = `SELECT st.node_id, st.account_id, st.created_at FROM stars st
			JOIN nodes n ON n.id = st.node_id AND n.account_id = st.account_id WHERE st.user_id = ?`
		fsql, fargs := f.Where("n")
		query += fsql
		args = append(args, fargs...)
	}
	if p.Cursor != "" {
		parts, err := decodeFilteredCursor(p.Cursor, 2, f)
		if err != nil {
			return nil, err
		}
		at, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, ErrBadCursor
		}
		if f.Active() {
			query += ` AND (st.created_at < ? OR (st.created_at = ? AND st.node_id < ?))`
		} else {
			query += ` AND (created_at < ? OR (created_at = ? AND node_id < ?))`
		}
		args = append(args, at, at, parts[1])
	}
	args = append(args, p.Limit)
	order := ` ORDER BY created_at DESC, node_id DESC LIMIT ?`
	if f.Active() {
		order = ` ORDER BY st.created_at DESC, st.node_id DESC LIMIT ?`
	}
	rows, err := s.r.QueryContext(ctx, query+order, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Star
	for rows.Next() {
		var st Star
		var created int64
		if err := rows.Scan(&st.NodeID, &st.AccountID, &created); err != nil {
			return nil, err
		}
		st.UserID, st.CreatedAt = userID, fromMillis(created)
		out = append(out, st)
	}
	return out, rows.Err()
}

// StarredAmong returns the subset of nodeIDs that user has starred, as a set. One query reads the whole page.
func (s *Store) StarredAmong(ctx context.Context, userID string, nodeIDs []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(nodeIDs) == 0 {
		return out, nil
	}
	marks := make([]string, len(nodeIDs))
	args := []any{userID}
	for i, id := range nodeIDs {
		marks[i] = "?"
		args = append(args, id)
	}
	rows, err := s.r.QueryContext(ctx, "SELECT node_id FROM stars WHERE user_id = ? AND node_id IN ("+
		strings.Join(marks, ",")+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
