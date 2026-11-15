// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
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

// PlayedRow is one place a user stopped at, as the played list reads it. Positions are milliseconds of media time.
type PlayedRow struct {
	NodeID     string
	AccountID  string
	PositionMS int64
	DurationMS int64
	UpdatedAt  time.Time
}

// The played list's unfinished rule, in milliseconds of media time: the web's RESUME_FROM_S and RESUME_UNTIL_END_S
// (web/src/lib/files/resume.ts). A place under playedFromMS is too near the start to resume, and a place within
// playedUntilEndMS of a known end is finished; a duration of 0 is unknown, so only the start rule applies to it.
const (
	playedFromMS     = 5000
	playedUntilEndMS = 10000
)

// PlayedCursorOf is the cursor that resumes a PlayedPage after row r, bound to the filter and the unfinished flag.
func PlayedCursorOf(r PlayedRow, f ListFilter, unfinished bool) string {
	f.Unfinished = unfinished
	return encodeFilteredCursor(f, strconv.FormatInt(millis(r.UpdatedAt), 10), r.NodeID)
}

// PlayedPage lists user's places in media files, the latest place first, p.Limit at a time, after the cursor if one is
// given. With a filter, each place is joined to its node of the same account, so the type is the node's; unfinished
// keeps only the places worth resuming. The places are not filtered for reach here: the caller drops the ones it may
// not list.
func (s *Store) PlayedPage(ctx context.Context, userID string, f ListFilter, unfinished bool, p Page) ([]PlayedRow, error) {
	query, args, err := playedPageSQL(userID, f, unfinished, p)
	if err != nil {
		return nil, err
	}
	rows, err := s.r.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlayedRow
	for rows.Next() {
		var r PlayedRow
		var updated int64
		if err := rows.Scan(&r.NodeID, &r.AccountID, &r.PositionMS, &r.DurationMS, &updated); err != nil {
			return nil, err
		}
		r.UpdatedAt = fromMillis(updated)
		out = append(out, r)
	}
	return out, rows.Err()
}

// playedPageSQL is the statement PlayedPage runs, with its arguments. The plan test reads it, so the test runs what the
// list runs. The user_id equality is the primary key's prefix, so the query never scans every user's places.
func playedPageSQL(userID string, f ListFilter, unfinished bool, p Page) (string, []any, error) {
	if err := f.Validate(); err != nil {
		return "", nil, err
	}
	f.Unfinished = unfinished
	query := `SELECT node_id, account_id, position_ms, duration_ms, updated_at FROM media_resume WHERE user_id = ?`
	args := []any{userID}
	alias := ""
	if f.Active() {
		query = `SELECT m.node_id, m.account_id, m.position_ms, m.duration_ms, m.updated_at FROM media_resume m
			JOIN nodes n ON n.id = m.node_id AND n.account_id = m.account_id WHERE m.user_id = ?`
		fsql, fargs := f.Where("n")
		query += fsql
		args = append(args, fargs...)
		alias = "m."
	}
	if unfinished {
		query += ` AND ` + alias + `position_ms >= ? AND (` + alias + `duration_ms = 0 OR ` + alias + `position_ms < ` + alias + `duration_ms - ?)`
		args = append(args, playedFromMS, playedUntilEndMS)
	}
	if p.Cursor != "" {
		parts, err := decodeFilteredCursor(p.Cursor, 2, f)
		if err != nil {
			return "", nil, err
		}
		at, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return "", nil, ErrBadCursor
		}
		query += ` AND (` + alias + `updated_at < ? OR (` + alias + `updated_at = ? AND ` + alias + `node_id < ?))`
		args = append(args, at, at, parts[1])
	}
	args = append(args, p.Limit)
	query += ` ORDER BY ` + alias + `updated_at DESC, ` + alias + `node_id DESC LIMIT ?`
	return query, args, nil
}
