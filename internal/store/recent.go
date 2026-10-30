// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"strconv"
	"time"
)

// RecentRow is a user's recent entry for a node: its last view and its last edit, when either happened, and the
// later of the two, which orders the list.
type RecentRow struct {
	UserID    string
	NodeID    string
	AccountID string
	ViewedAt  *time.Time
	EditedAt  *time.Time
	LastAt    time.Time
}

// ViewRecord is one view waiting to be written by the view recorder.
type ViewRecord struct {
	AccountID string
	UserID    string
	NodeID    string
	At        time.Time
}

// recentUpsert records one view or one edit. The stored times only move forward: a batch of views can reach the
// table out of order, so each column keeps the later of the stored and the new value.
const recentUpsert = `INSERT INTO recent (user_id, node_id, account_id, viewed_at, edited_at, last_at)
	VALUES (?, ?, ?, ?, ?, ?)
	ON CONFLICT (user_id, node_id) DO UPDATE SET
		viewed_at = CASE WHEN excluded.viewed_at IS NULL THEN recent.viewed_at
			WHEN recent.viewed_at IS NULL OR excluded.viewed_at > recent.viewed_at THEN excluded.viewed_at
			ELSE recent.viewed_at END,
		edited_at = CASE WHEN excluded.edited_at IS NULL THEN recent.edited_at
			WHEN recent.edited_at IS NULL OR excluded.edited_at > recent.edited_at THEN excluded.edited_at
			ELSE recent.edited_at END,
		last_at = MAX(recent.last_at, excluded.last_at)`

func upsertRecent(ctx context.Context, q querier, accountID, userID, nodeID string, viewed, edited *int64, last int64) error {
	_, err := q.ExecContext(ctx, recentUpsert, userID, nodeID, accountID, nullInt(viewed), nullInt(edited), last)
	return err
}

// RecordEdit notes in the transaction that user edited node at the given time.
func (tx *Tx) RecordEdit(ctx context.Context, accountID, userID, nodeID string, at time.Time) error {
	ms := millis(at)
	return upsertRecent(ctx, tx.q, accountID, userID, nodeID, nil, &ms, ms)
}

// viewUpsertFrom records one view only when its node and its user still exist in the account. A node purged, or a
// user deleted, between the view and the flush makes the row a no-op rather than a foreign-key failure, so one stale
// view cannot roll back the views written with it.
const viewUpsertFrom = `INSERT INTO recent (user_id, node_id, account_id, viewed_at, edited_at, last_at)
	SELECT u.id, n.id, n.account_id, ?, NULL, ? FROM nodes n JOIN users u ON u.id = ?
	WHERE n.id = ? AND n.account_id = ? AND u.account_id = ? AND true
	ON CONFLICT (user_id, node_id) DO UPDATE SET
		viewed_at = CASE WHEN excluded.viewed_at IS NULL THEN recent.viewed_at
			WHEN recent.viewed_at IS NULL OR excluded.viewed_at > recent.viewed_at THEN excluded.viewed_at
			ELSE recent.viewed_at END,
		edited_at = recent.edited_at,
		last_at = MAX(recent.last_at, excluded.last_at)`

// WriteViews writes a batch of views in one transaction, as the view recorder does. A view whose node or user is gone
// writes nothing; the rest of the batch is written.
func (s *Store) WriteViews(ctx context.Context, views []ViewRecord) error {
	if len(views) == 0 {
		return nil
	}
	return s.Write(ctx, func(tx *Tx) error {
		for _, v := range views {
			ms := millis(v.At)
			if _, err := tx.q.ExecContext(ctx, viewUpsertFrom, ms, ms, v.UserID, v.NodeID, v.AccountID,
				v.AccountID); err != nil {
				return err
			}
		}
		return nil
	})
}

// RecentCursor is the cursor that resumes a RecentPage after entry r.
func RecentCursor(r RecentRow) string {
	return encodeCursor(strconv.FormatInt(millis(r.LastAt), 10), r.NodeID)
}

// RecentPage lists user's recent entries, newest first, p.Limit at a time, after the cursor if one is given. The
// entries are not filtered here: the caller drops the ones it may not list.
func (s *Store) RecentPage(ctx context.Context, userID string, p Page) ([]RecentRow, error) {
	query := `SELECT node_id, account_id, viewed_at, edited_at, last_at FROM recent WHERE user_id = ?`
	args := []any{userID}
	if p.Cursor != "" {
		parts, err := decodeCursor(p.Cursor, 2)
		if err != nil {
			return nil, err
		}
		at, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, ErrBadCursor
		}
		query += ` AND (last_at < ? OR (last_at = ? AND node_id < ?))`
		args = append(args, at, at, parts[1])
	}
	args = append(args, p.Limit)
	rows, err := s.r.QueryContext(ctx, query+` ORDER BY last_at DESC, node_id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecentRow
	for rows.Next() {
		var r RecentRow
		var viewed, edited *int64
		var last int64
		if err := rows.Scan(&r.NodeID, &r.AccountID, &viewed, &edited, &last); err != nil {
			return nil, err
		}
		r.UserID, r.LastAt = userID, fromMillis(last)
		if viewed != nil {
			t := fromMillis(*viewed)
			r.ViewedAt = &t
		}
		if edited != nil {
			t := fromMillis(*edited)
			r.EditedAt = &t
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteRecent removes user's entry for node. An entry that is not there is not an error.
func (tx *Tx) DeleteRecent(ctx context.Context, userID, nodeID string) error {
	_, err := tx.q.ExecContext(ctx, "DELETE FROM recent WHERE user_id = ? AND node_id = ?", userID, nodeID)
	return err
}

// DeleteAllRecent clears user's recent list.
func (tx *Tx) DeleteAllRecent(ctx context.Context, userID string) error {
	_, err := tx.q.ExecContext(ctx, "DELETE FROM recent WHERE user_id = ?", userID)
	return err
}

// PruneRecent removes the entries last used before cutoff, and then, for each user, all but the perUser most recent.
// It returns how many it removed.
func (tx *Tx) PruneRecent(ctx context.Context, cutoff time.Time, perUser int) (int64, error) {
	res, err := tx.q.ExecContext(ctx, "DELETE FROM recent WHERE last_at < ?", millis(cutoff))
	if err != nil {
		return 0, err
	}
	aged, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	res, err = tx.q.ExecContext(ctx, `DELETE FROM recent WHERE rowid IN (
		SELECT rowid FROM (SELECT rowid AS rowid, ROW_NUMBER() OVER (PARTITION BY user_id
			ORDER BY last_at DESC, node_id DESC) AS rn FROM recent) WHERE rn > ?)`, perUser)
	if err != nil {
		return 0, err
	}
	extra, err := res.RowsAffected()
	return aged + extra, err
}
