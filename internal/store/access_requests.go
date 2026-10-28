// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"
)

// Access request states, as the access_requests.status column names them.
const (
	RequestPending  = "pending"
	RequestApproved = "approved"
	RequestDenied   = "denied"
)

// AccessRequest asks for a role on a node. A user who lacks a role asks for it; a manager approves
// or denies. At most one request is pending per node and requester.
type AccessRequest struct {
	ID        string
	AccountID string
	NodeID    string
	UserID    string // the requester
	Role      string // viewer, commenter or editor
	Message   string
	Status    string // RequestPending, RequestApproved or RequestDenied
	CreatedAt time.Time
	DecidedAt *time.Time
	DecidedBy string
}

const accessRequestCols = "id, account_id, node_id, user_id, role, message, status, created_at, decided_at, decided_by"

func scanAccessRequest(row interface{ Scan(...any) error }) (*AccessRequest, error) {
	var r AccessRequest
	var created int64
	var decided sql.NullInt64
	err := row.Scan(&r.ID, &r.AccountID, &r.NodeID, &r.UserID, &r.Role, &r.Message, &r.Status, &created,
		&decided, &r.DecidedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.CreatedAt = fromMillis(created)
	r.DecidedAt = fromNullMillis(decided)
	return &r, nil
}

// InsertAccessRequest records a new request. A request that is already pending for the same node and
// requester is returned unchanged: the repeat collapses into it, and keeps the role and message it
// was made with. To ask for more, the requester waits for a decision. The caller compares the returned
// ID with its own to tell a new request from a collapsed one.
func (tx *Tx) InsertAccessRequest(ctx context.Context, r *AccessRequest) (*AccessRequest, error) {
	if cur, err := tx.PendingAccessRequest(ctx, r.NodeID, r.UserID); err == nil {
		return cur, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	_, err := tx.q.ExecContext(ctx, `INSERT INTO access_requests (`+accessRequestCols+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL, '')`,
		r.ID, r.AccountID, r.NodeID, r.UserID, r.Role, r.Message, RequestPending, millis(r.CreatedAt))
	if isUniqueViolation(err) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

// PendingAccessRequest returns the pending request of userID on nodeID, or ErrNotFound.
func (tx *Tx) PendingAccessRequest(ctx context.Context, nodeID, userID string) (*AccessRequest, error) {
	return scanAccessRequest(tx.q.QueryRowContext(ctx, "SELECT "+accessRequestCols+" FROM access_requests "+
		"WHERE node_id = ? AND user_id = ? AND status = 'pending'", nodeID, userID))
}

// AccessRequest returns the request with the given ID in accountID, read inside the transaction.
func (tx *Tx) AccessRequest(ctx context.Context, accountID, id string) (*AccessRequest, error) {
	return scanAccessRequest(tx.q.QueryRowContext(ctx, "SELECT "+accessRequestCols+" FROM access_requests "+
		"WHERE account_id = ? AND id = ?", accountID, id))
}

// AccessRequest returns the request with the given ID in accountID.
func (s *Store) AccessRequest(ctx context.Context, accountID, id string) (*AccessRequest, error) {
	return scanAccessRequest(s.r.QueryRowContext(ctx, "SELECT "+accessRequestCols+" FROM access_requests "+
		"WHERE account_id = ? AND id = ?", accountID, id))
}

// DecideAccessRequest closes a pending request as approved or denied, and stores the role decided: the
// role granted on approval, which the caller sets on r first. It returns ErrNotFound when the request is
// not pending any more, so a decision made twice is refused the second time.
func (tx *Tx) DecideAccessRequest(ctx context.Context, r *AccessRequest, by string, at time.Time) error {
	err := rowsAffected(tx.q.ExecContext(ctx, `UPDATE access_requests SET status = ?, role = ?, decided_at = ?, decided_by = ?
		WHERE account_id = ? AND id = ? AND status = 'pending'`, r.Status, r.Role, millis(at), by, r.AccountID, r.ID))
	if err != nil {
		return err
	}
	r.DecidedAt = &at
	r.DecidedBy = by
	return nil
}

// NodeAccessRequests lists the requests on one node, oldest first, one page at a time. A cursor is
// the ID of the last request on the page.
func (s *Store) NodeAccessRequests(ctx context.Context, accountID, nodeID string, p Page) ([]*AccessRequest, string, error) {
	return s.pageAccessRequests(ctx, "account_id = ? AND node_id = ?", []any{accountID, nodeID}, p)
}

// PendingAccessRequests lists the pending requests of accountID, oldest first, one page at a time.
// The caller keeps only those on nodes it may manage.
func (s *Store) PendingAccessRequests(ctx context.Context, accountID string, p Page) ([]*AccessRequest, string, error) {
	return s.pageAccessRequests(ctx, "account_id = ? AND status = 'pending'", []any{accountID}, p)
}

func (s *Store) pageAccessRequests(ctx context.Context, cond string, args []any, p Page) ([]*AccessRequest, string, error) {
	if p.Cursor != "" {
		parts, err := decodeCursor(p.Cursor, 2)
		if err != nil {
			return nil, "", err
		}
		cond += " AND (created_at, id) > (?, ?)"
		ms, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, "", ErrBadCursor
		}
		args = append(args, ms, parts[1])
	}
	args = append(args, p.Limit+1)
	rows, err := s.r.QueryContext(ctx, "SELECT "+accessRequestCols+" FROM access_requests WHERE "+cond+
		" ORDER BY created_at, id LIMIT ?", args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []*AccessRequest
	for rows.Next() {
		r, err := scanAccessRequest(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, r)
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

// DeleteUserAccessRequests removes the requests a user made, when the user is deleted. Requests on a
// node go with the node, through the foreign key.
func (tx *Tx) DeleteUserAccessRequests(ctx context.Context, accountID, userID string) error {
	_, err := tx.q.ExecContext(ctx, "DELETE FROM access_requests WHERE account_id = ? AND user_id = ?",
		accountID, userID)
	return err
}

// TeamManagerIDs returns the active users who manage a team space: every owner of the account, and the
// users who hold manager on the space's root, directly or through a group. They are the people an
// access request on one of its nodes is mailed to.
func (s *Store) TeamManagerIDs(ctx context.Context, accountID, folderID string) ([]string, error) {
	now := nowMillis()
	rows, err := s.r.QueryContext(ctx, `SELECT u.id FROM users u WHERE u.account_id = ? AND u.status = 'active'
		AND (u.role = 'owner'
			OR u.id IN (SELECT pm.subject_id FROM permissions pm JOIN nodes r ON r.id = pm.node_id
				WHERE r.folder_id = ? AND r.parent_id IS NULL AND pm.subject_kind = 'user' AND pm.role = 'manager'
				AND (pm.expires_at IS NULL OR pm.expires_at > ?))
			OR u.id IN (SELECT gm.user_id FROM group_members gm JOIN permissions pm ON pm.subject_id = gm.group_id
				JOIN nodes r ON r.id = pm.node_id
				WHERE r.folder_id = ? AND r.parent_id IS NULL AND pm.subject_kind = 'group' AND pm.role = 'manager'
				AND (pm.expires_at IS NULL OR pm.expires_at > ?)))
		ORDER BY u.id`, accountID, folderID, now, folderID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// RecentAccessRequest reports whether userID made a request on nodeID at or after since, whatever its
// state. A request that repeats one this recent is recorded, but it is not mailed again.
func (tx *Tx) RecentAccessRequest(ctx context.Context, nodeID, userID string, since time.Time) (bool, error) {
	var n int
	err := tx.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM access_requests
		WHERE node_id = ? AND user_id = ? AND created_at >= ?`, nodeID, userID, millis(since)).Scan(&n)
	return n > 0, err
}

// DeleteDecidedAccessRequests removes the requests decided before the cutoff, and returns how many. A
// pending request is kept however old it is, since someone still has to decide it.
func (tx *Tx) DeleteDecidedAccessRequests(ctx context.Context, before time.Time) (int64, error) {
	res, err := tx.q.ExecContext(ctx, `DELETE FROM access_requests WHERE status <> 'pending' AND decided_at < ?`,
		millis(before))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
