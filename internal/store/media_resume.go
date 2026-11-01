// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// MediaResume is where one user stopped one media file. Positions are milliseconds of media time.
type MediaResume struct {
	PositionMS int64
	DurationMS int64
	UpdatedAt  time.Time
}

// SetMediaResume stores the place userID stopped at in nodeID, replacing the last one.
func (tx *Tx) SetMediaResume(ctx context.Context, accountID, userID, nodeID string, r MediaResume, at time.Time) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO media_resume (user_id, node_id, account_id, position_ms, duration_ms, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id, node_id) DO UPDATE SET position_ms = excluded.position_ms,
			duration_ms = excluded.duration_ms, updated_at = excluded.updated_at`,
		userID, nodeID, accountID, r.PositionMS, r.DurationMS, millis(at))
	return err
}

// MediaResumeOf reads the place userID stopped at in nodeID. found is false when none is stored.
func (s *Store) MediaResumeOf(ctx context.Context, userID, nodeID string) (r MediaResume, found bool, err error) {
	var updated int64
	err = s.r.QueryRowContext(ctx, `SELECT position_ms, duration_ms, updated_at FROM media_resume
		WHERE user_id = ? AND node_id = ?`, userID, nodeID).Scan(&r.PositionMS, &r.DurationMS, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return MediaResume{}, false, nil
	}
	if err != nil {
		return MediaResume{}, false, err
	}
	r.UpdatedAt = fromMillis(updated)
	return r, true, nil
}
