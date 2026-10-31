// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Office editor locks (design section 10, decision 32).
//
// A lock is one row in wopi_locks, kept while an editor holds a file. It is live until ExpiresAt. A row past that
// time is absent to every reader and writer here, and the sweep removes it. A file holds at most one live lock.
//
// The rule: a live lock refuses every change to its file that alters its name, place, content or owner, by anyone but
// the holder; a description, colour or flag passes (decision 32). A folder operation is refused
// when a live lock lies in the folder, or below the folder for a subtree operation. The check runs inside the write
// transaction (guardNode, guardFolder), at each store method that changes a node row, version or folder, so a new path
// that calls one of them is covered. The guard test in internal/store pins each caller and
// the rule it follows.

// WOPILock is one lock an office editor holds on a file.
type WOPILock struct {
	NodeID    string
	AccountID string
	FolderID  string
	LockID    string
	Holder    string
	CreatedAt time.Time
	ExpiresAt time.Time
	// VersionID is the version the lock's editing session made at its first save, or "" before that save.
	VersionID string
}

// LockedError is a write that a live lock refuses. Current is the lock ID that holds the file, or "" when no lock
// covers it, which is the case of a write made without a lock. Reason says why, for the answer and for logs. Neither
// field is shown to an API caller, which sees only the locked code.
type LockedError struct {
	Current string
	Reason  string
}

func (e *LockedError) Error() string { return "store: the file is open in an office editor" }

const wopiLockCols = `l.node_id, l.account_id, n.folder_id, l.lock_id, l.holder, l.created_at, l.expires_at,
	COALESCE(l.version_id, '')`

// scanWOPILock reads one lock. A query with no row is ErrNotFound.
func scanWOPILock(row interface{ Scan(...any) error }) (*WOPILock, error) {
	var (
		l       WOPILock
		created int64
		expires int64
	)
	err := row.Scan(&l.NodeID, &l.AccountID, &l.FolderID, &l.LockID, &l.Holder, &created, &expires, &l.VersionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	l.CreatedAt = fromMillis(created)
	l.ExpiresAt = fromMillis(expires)
	return &l, nil
}

// liveLocks returns the live locks of accountID at now, each with the folder its file is in. It is one index range
// read over the account's locks, so its cost follows the number of open editors, not the size of the tree.
func liveLocks(ctx context.Context, q querier, accountID string, now time.Time) ([]*WOPILock, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+wopiLockCols+` FROM wopi_locks l
		JOIN nodes n ON n.id = l.node_id WHERE l.account_id = ? AND l.expires_at > ?`, accountID, millis(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*WOPILock
	for rows.Next() {
		l, err := scanWOPILock(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// liveLock returns the live lock on node id of accountID at now, or ErrNotFound.
func liveLock(ctx context.Context, q querier, accountID, id string, now time.Time) (*WOPILock, error) {
	return scanWOPILock(q.QueryRowContext(ctx, `SELECT `+wopiLockCols+` FROM wopi_locks l
		JOIN nodes n ON n.id = l.node_id WHERE l.node_id = ? AND l.account_id = ? AND l.expires_at > ?`,
		id, accountID, millis(now)))
}

// LiveLock returns the live lock on node nodeID of accountID, or ErrNotFound. It reads outside a transaction.
func (s *Store) LiveLock(ctx context.Context, accountID, nodeID string) (*WOPILock, error) {
	return liveLock(ctx, s.r, accountID, nodeID, s.Clock())
}

// LiveLock is Store.LiveLock inside the transaction, so a check made there sees the writes made so far.
func (tx *Tx) LiveLock(ctx context.Context, accountID, nodeID string) (*WOPILock, error) {
	return liveLock(ctx, tx.q, accountID, nodeID, tx.clock())
}

// guardNode refuses a change to node id of accountID when a live lock holds it or, for a folder, a file below it.
// holder is the lock ID the caller holds: the holder may change its own file, and no other lock is exempt. "" holds
// none. The error is a *LockedError.
func (tx *Tx) guardNode(ctx context.Context, accountID, id, holder string) error {
	// A file has nothing below it, so only a lock on the file itself can hold it: one read by key, whatever the number of
	// editors open in the account. A folder needs the account's live locks and the walk up from each (guardQuery).
	var folder bool
	switch err := tx.q.QueryRowContext(ctx, `SELECT kind = ? FROM nodes WHERE account_id = ? AND id = ?`,
		KindDir, accountID, id).Scan(&folder); {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return err
	}
	if !folder {
		l, err := liveLock(ctx, tx.q, accountID, id, tx.clock())
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if holder == "" || l.LockID != holder {
			return &LockedError{Current: l.LockID, Reason: "the file is open in an office editor"}
		}
		return nil
	}
	locks, err := liveLocks(ctx, tx.q, accountID, tx.clock())
	if err != nil || len(locks) == 0 {
		return err
	}
	rows, err := tx.q.QueryContext(ctx, guardQuery, accountID, millis(tx.clock()), accountID, id, accountID,
		millis(tx.clock()))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var lockNode, lockID string
		if err := rows.Scan(&lockNode, &lockID); err != nil {
			return err
		}
		if lockNode != id {
			return &LockedError{Current: lockID, Reason: "a file below it is open in an office editor"}
		}
		if holder != "" && lockID == holder {
			continue
		}
		return &LockedError{Current: lockID, Reason: "the file is open in an office editor"}
	}
	return rows.Err()
}

// guardQuery finds, in one statement, the live locks that hold node id, or a file below it. Each live lock's ancestors
// are walked once, by primary key, so the cost is one statement however many editors hold files, and the walk is empty
// when no lock is live. Its two columns are the node a lock holds and the lock's ID.
const guardQuery = `WITH RECURSIVE up(lock_node, nid, parent) AS (
	SELECT l.node_id, l.node_id, n.parent_id FROM wopi_locks l JOIN nodes n ON n.id = l.node_id
		WHERE l.account_id = ? AND l.expires_at > ?
	UNION ALL
	SELECT up.lock_node, p.id, p.parent_id FROM nodes p JOIN up ON p.id = up.parent WHERE p.account_id = ?
)
SELECT DISTINCT up.lock_node, l.lock_id FROM up JOIN wopi_locks l ON l.node_id = up.lock_node
	WHERE up.nid = ? AND l.account_id = ? AND l.expires_at > ?`

// guardFolder refuses a change to every node in folderID of accountID when a live lock holds a file in it. It is
// the rule for an operation that moves or deletes a folder's contents.
func (tx *Tx) guardFolder(ctx context.Context, accountID, folderID string) error {
	locks, err := liveLocks(ctx, tx.q, accountID, tx.clock())
	if err != nil {
		return err
	}
	for _, l := range locks {
		if l.FolderID == folderID {
			return &LockedError{Current: l.LockID, Reason: "a file in this folder is open in an office editor"}
		}
	}
	return nil
}

// An account's deletion does not wait for its locks: DeleteAccount clears them in the same write, since nobody can
// hold a lock on a deleting account (decision 32). A user's deletion clears its locks with ReleaseUserLocks.

// PutLock records a new lock on a file, inside tx. The caller has checked, in the same transaction, that no live
// lock holds the file. A row for the file that is past its expiry is replaced; it is returned, so the caller can
// audit the expiry the sweep has not yet recorded. It returns nil when there was no such row.
func (tx *Tx) PutLock(ctx context.Context, l *WOPILock) (*WOPILock, error) {
	// The account's live locks are capped (MaxLiveLocks). The count reads the account's index range, once per lock taken.
	var live int
	if err := tx.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM wopi_locks WHERE account_id = ? AND expires_at > ?`,
		l.AccountID, millis(tx.clock())).Scan(&live); err != nil {
		return nil, err
	}
	if live >= MaxLiveLocks {
		return nil, &LockedError{Reason: "the account has too many open editors"}
	}
	expired, err := scanWOPILock(tx.q.QueryRowContext(ctx, `SELECT `+wopiLockCols+` FROM wopi_locks l
		JOIN nodes n ON n.id = l.node_id WHERE l.node_id = ? AND l.expires_at <= ?`, l.NodeID, millis(tx.clock())))
	switch {
	case errors.Is(err, ErrNotFound):
		expired = nil
	case err != nil:
		return nil, err
	}
	if expired != nil {
		if _, err := tx.q.ExecContext(ctx, `DELETE FROM wopi_locks WHERE node_id = ? AND expires_at <= ?`,
			l.NodeID, millis(tx.clock())); err != nil {
			return nil, err
		}
	}
	_, err = tx.q.ExecContext(ctx, `INSERT INTO wopi_locks
		(node_id, account_id, lock_id, holder, created_at, expires_at, version_id) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		l.NodeID, l.AccountID, l.LockID, l.Holder, millis(l.CreatedAt), millis(l.ExpiresAt), nullString(l.VersionID))
	if isUniqueViolation(err) {
		return nil, ErrConflict
	}
	return expired, err
}

// RefreshWOPILock moves the expiry of the live lock lockID on node nodeID to until. ErrNotFound when no live lock
// has that ID.
func (tx *Tx) RefreshWOPILock(ctx context.Context, accountID, nodeID, lockID string, until time.Time) error {
	return rowsAffected(tx.q.ExecContext(ctx, `UPDATE wopi_locks SET expires_at = ?
		WHERE node_id = ? AND account_id = ? AND lock_id = ? AND expires_at > ?`,
		millis(until), nodeID, accountID, lockID, millis(tx.clock())))
}

// UnlockNode removes the live lock lockID on node nodeID. ErrNotFound when no live lock has that ID.
func (tx *Tx) UnlockNode(ctx context.Context, accountID, nodeID, lockID string) error {
	return rowsAffected(tx.q.ExecContext(ctx, `DELETE FROM wopi_locks
		WHERE node_id = ? AND account_id = ? AND lock_id = ? AND expires_at > ?`,
		nodeID, accountID, lockID, millis(tx.clock())))
}

// SetLockVersion records on the live lock lockID of node nodeID the version its editing session made.
func (tx *Tx) SetLockVersion(ctx context.Context, accountID, nodeID, lockID, versionID string) error {
	return rowsAffected(tx.q.ExecContext(ctx, `UPDATE wopi_locks SET version_id = ?
		WHERE node_id = ? AND account_id = ? AND lock_id = ?`, versionID, nodeID, accountID, lockID))
}

// DeleteExpiredLocks removes the locks whose expiry is at or before now, and returns them, so the sweep can audit each.
func (tx *Tx) DeleteExpiredLocks(ctx context.Context, now time.Time) ([]*WOPILock, error) {
	rows, err := tx.q.QueryContext(ctx, `SELECT `+wopiLockCols+` FROM wopi_locks l
		JOIN nodes n ON n.id = l.node_id WHERE l.expires_at <= ? ORDER BY l.node_id`, millis(now))
	if err != nil {
		return nil, err
	}
	var out []*WOPILock
	for rows.Next() {
		l, err := scanWOPILock(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, l := range out {
		if _, err := tx.q.ExecContext(ctx, `DELETE FROM wopi_locks WHERE node_id = ? AND expires_at <= ?`,
			l.NodeID, millis(now)); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// MaxLiveLocks is the most live locks one account may hold at once (decision 32). A lock past it is refused.
const MaxLiveLocks = 1000

// BreakLock ends the live lock on file nodeID of accountID, whoever holds it, and returns the lock for the audit entry.
// ErrNotFound when no editor holds the file. The holder's next refresh or save then answers with no lock on the file.
func (tx *Tx) BreakLock(ctx context.Context, accountID, nodeID string) (*WOPILock, error) {
	l, err := liveLock(ctx, tx.q, accountID, nodeID, tx.clock())
	if err != nil {
		return nil, err
	}
	if err := rowsAffected(tx.q.ExecContext(ctx, `DELETE FROM wopi_locks
		WHERE node_id = ? AND account_id = ? AND expires_at > ?`, nodeID, accountID, millis(tx.clock()))); err != nil {
		return nil, err
	}
	return l, nil
}

// RelockWOPILock replaces the live lock oldID on node nodeID with newID, held by holder, until the given time. The
// row keeps the version its editing session made (version_id), so the session keeps its one version. ErrNotFound when
// no live lock has oldID.
func (tx *Tx) RelockWOPILock(ctx context.Context, accountID, nodeID, oldID, newID, holder string, until time.Time) error {
	return rowsAffected(tx.q.ExecContext(ctx, `UPDATE wopi_locks SET lock_id = ?, holder = ?, expires_at = ?
		WHERE node_id = ? AND account_id = ? AND lock_id = ? AND expires_at > ?`,
		newID, holder, millis(until), nodeID, accountID, oldID, millis(tx.clock())))
}

// ReleaseUserLocks clears the locks userID holds, and the locks on the files in its personal folder, before the user is
// deleted and those files move or go. The user's sessions and tokens go with it, so no lock it holds can be refreshed:
// clearing the rows now lets the files move at once. Other holders' locks elsewhere are left alone.
func (tx *Tx) ReleaseUserLocks(ctx context.Context, accountID, userID, personalFolderID string) error {
	_, err := tx.q.ExecContext(ctx, `DELETE FROM wopi_locks WHERE account_id = ? AND (holder = ? OR node_id IN
		(SELECT id FROM nodes WHERE account_id = ? AND folder_id = ?))`, accountID, userID, accountID, personalFolderID)
	return err
}
