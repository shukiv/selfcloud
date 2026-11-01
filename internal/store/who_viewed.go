// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"
)

// ViewerWindow is how far back the who-viewed list looks: the recent table keeps a row for 30 days after its last use
// (PruneRecent, sweep recentRetention), and a view older than that is left out even while its row is kept by an edit.
const ViewerWindow = 30 * 24 * time.Hour

// ViewerRow is one user's last view of a node.
type ViewerRow struct {
	UserID   string
	ViewedAt time.Time
}

// WhoViewed reports whether the account's who-viewed switch is on. It is ErrNotFound for an account that does not exist.
func (s *Store) WhoViewed(ctx context.Context, accountID string) (bool, error) {
	var on int64
	err := s.r.QueryRowContext(ctx, "SELECT who_viewed FROM accounts WHERE id = ?", accountID).Scan(&on)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	return on == 1, err
}

// WhoViewedSince reports the account's who-viewed switch and when it last went on. The time is zero while it is off.
func (s *Store) WhoViewedSince(ctx context.Context, accountID string) (bool, time.Time, error) {
	var on int64
	var since sql.NullInt64
	err := s.r.QueryRowContext(ctx, "SELECT who_viewed, who_viewed_since FROM accounts WHERE id = ?", accountID).Scan(&on, &since)
	if errors.Is(err, sql.ErrNoRows) {
		return false, time.Time{}, ErrNotFound
	}
	if err != nil {
		return false, time.Time{}, err
	}
	if on != 1 || !since.Valid {
		return false, time.Time{}, nil
	}
	return true, time.UnixMilli(since.Int64).UTC(), nil
}

// SetWhoViewed sets the account's who-viewed switch. Turning it off keeps the rows of the recent table, as they are
// the user's own recent list too, and forgets when it went on. Turning it on starts a new period at at; setting it on
// while it is on keeps the period that is running.
func (tx *Tx) SetWhoViewed(ctx context.Context, accountID string, on bool, at time.Time) error {
	return rowsAffected(tx.q.ExecContext(ctx, `UPDATE accounts SET
		who_viewed_since = CASE WHEN ? = 0 THEN NULL WHEN who_viewed = 1 THEN who_viewed_since ELSE ? END,
		who_viewed = ?, updated_at = ? WHERE id = ?`,
		boolInt(on), millis(at), boolInt(on), millis(at), accountID))
}

// viewersQuery is the list of viewers of one node, newest view first. The cursor, when there is one, is the range after
// the last row of the previous page.
func viewersQuery(cursor bool) string {
	q := `SELECT user_id, viewed_at FROM recent WHERE node_id = ? AND account_id = ? AND viewed_at >= ?`
	if cursor {
		q += ` AND (viewed_at < ? OR (viewed_at = ? AND user_id < ?))`
	}
	return q + ` ORDER BY viewed_at DESC, user_id DESC LIMIT ?`
}

// ViewerCursor is the cursor that resumes a ViewersPage after row r.
func ViewerCursor(r ViewerRow) string {
	return encodeCursor(strconv.FormatInt(millis(r.ViewedAt), 10), r.UserID)
}

// ViewersPage lists the users whose last view of nodeID is at or after since, newest first, p.Limit at a time, after the
// cursor if one is given. The account is part of the query, so a node of another account lists nothing here. The
// caller decides who may read the list and what each name shows.
func (s *Store) ViewersPage(ctx context.Context, accountID, nodeID string, since time.Time, p Page) ([]ViewerRow, error) {
	args := []any{nodeID, accountID, millis(since)}
	if p.Cursor != "" {
		parts, err := decodeCursor(p.Cursor, 2)
		if err != nil {
			return nil, err
		}
		at, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, ErrBadCursor
		}
		args = append(args, at, at, parts[1])
	}
	args = append(args, p.Limit)
	rows, err := s.r.QueryContext(ctx, viewersQuery(p.Cursor != ""), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ViewerRow
	for rows.Next() {
		var r ViewerRow
		var at int64
		if err := rows.Scan(&r.UserID, &at); err != nil {
			return nil, err
		}
		r.ViewedAt = fromMillis(at)
		out = append(out, r)
	}
	return out, rows.Err()
}
