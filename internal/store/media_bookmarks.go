// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// MediaBookmark is one place a user marked in one media file: a time and an optional label. Positions are milliseconds
// of media time. It is private to its user, as the place a user stopped at is (MediaResume).
type MediaBookmark struct {
	ID         string
	PositionMS int64
	Label      string
	CreatedAt  time.Time
}

// MediaBookmarksOf reads the bookmarks userID marked in nodeID, ordered by position, then creation time, then ID.
func (s *Store) MediaBookmarksOf(ctx context.Context, userID, nodeID string) ([]MediaBookmark, error) {
	return mediaBookmarks(ctx, s.r, userID, nodeID)
}

// MediaBookmarksIn reads the same bookmarks inside a write, so the cap counts what the write itself adds to.
func (tx *Tx) MediaBookmarksIn(ctx context.Context, userID, nodeID string) ([]MediaBookmark, error) {
	return mediaBookmarks(ctx, tx.q, userID, nodeID)
}

// mediaBookmarks is the statement both reads run, bound by user and node so another user's bookmark, or one of another
// file, is never read. The user_id equality is the index's prefix, so the query never scans every user's bookmarks.
func mediaBookmarks(ctx context.Context, q Querier, userID, nodeID string) ([]MediaBookmark, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, position_ms, label, created_at FROM media_bookmarks
		WHERE user_id = ? AND node_id = ? ORDER BY position_ms, created_at, id`, userID, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MediaBookmark
	for rows.Next() {
		var b MediaBookmark
		var created int64
		if err := rows.Scan(&b.ID, &b.PositionMS, &b.Label, &created); err != nil {
			return nil, err
		}
		b.CreatedAt = fromMillis(created)
		out = append(out, b)
	}
	return out, rows.Err()
}

// AddMediaBookmark stores one bookmark of userID in nodeID. The caller has checked the cap and the file's reach.
func (tx *Tx) AddMediaBookmark(ctx context.Context, accountID, userID, nodeID string, b MediaBookmark, at time.Time) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO media_bookmarks (id, user_id, node_id, account_id, position_ms, label, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, b.ID, userID, nodeID, accountID, b.PositionMS, b.Label, millis(at))
	return err
}

// RelabelMediaBookmark sets the label of a bookmark of userID in nodeID and reads it back. found is false for a
// bookmark of another user, of another file, or one that does not exist; none of those is changed.
func (tx *Tx) RelabelMediaBookmark(ctx context.Context, userID, nodeID, bookmarkID, label string) (MediaBookmark, bool, error) {
	res, err := tx.q.ExecContext(ctx, `UPDATE media_bookmarks SET label = ? WHERE id = ? AND user_id = ? AND node_id = ?`,
		label, bookmarkID, userID, nodeID)
	if err != nil {
		return MediaBookmark{}, false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return MediaBookmark{}, false, err
	}
	return mediaBookmarkOf(ctx, tx.q, userID, nodeID, bookmarkID)
}

// DeleteMediaBookmark removes a bookmark of userID in nodeID. It reports whether one was found; a bookmark of another
// user, of another file, or one that does not exist is left alone.
func (tx *Tx) DeleteMediaBookmark(ctx context.Context, userID, nodeID, bookmarkID string) (bool, error) {
	res, err := tx.q.ExecContext(ctx, `DELETE FROM media_bookmarks WHERE id = ? AND user_id = ? AND node_id = ?`,
		bookmarkID, userID, nodeID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// mediaBookmarkOf reads one bookmark bound by user, node and ID, for the relabel's read-back.
func mediaBookmarkOf(ctx context.Context, q Querier, userID, nodeID, bookmarkID string) (MediaBookmark, bool, error) {
	var b MediaBookmark
	var created int64
	err := q.QueryRowContext(ctx, `SELECT id, position_ms, label, created_at FROM media_bookmarks
		WHERE id = ? AND user_id = ? AND node_id = ?`, bookmarkID, userID, nodeID).
		Scan(&b.ID, &b.PositionMS, &b.Label, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return MediaBookmark{}, false, nil
	}
	if err != nil {
		return MediaBookmark{}, false, err
	}
	b.CreatedAt = fromMillis(created)
	return b, true, nil
}
