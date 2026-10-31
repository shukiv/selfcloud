// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
)

// UsageChange is a change to an account's counters, made inside a transaction together with the
// change it counts. Bytes and Files follow the file tree; Reserved follows unfinished uploads.
type UsageChange struct {
	Bytes    int64
	Files    int64
	Reserved int64
}

// IsZero reports whether c changes nothing.
func (c UsageChange) IsZero() bool { return c == UsageChange{} }

// Usage is an account's or a folder's counters as stored.
type Usage struct {
	Bytes    int64 // used_bytes: the sizes of the files, and of their versions
	Files    int64 // used_files: how many files it holds
	Reserved int64 // reserved_bytes: the declared length of each unfinished upload
}

// FolderChange is a change to one folder's share of the counters.
type FolderChange struct {
	FolderID string
	Change   UsageChange
}

// Every counter write names the folder it belongs to. The account's counters are the sum of its folders'
// (slice p1): the account is adjusted by the same amounts, in the same statements.

// AdjustUsage applies c to the counters of folderID and of the account. folderID must be a folder of
// accountID.
func (tx *Tx) AdjustUsage(ctx context.Context, accountID, folderID string, c UsageChange) error {
	return tx.AdjustFolders(ctx, accountID, FolderChange{FolderID: folderID, Change: c})
}

// MoveUsage moves c from one folder to another: the folders' counters change, and the account's do not.
func (tx *Tx) MoveUsage(ctx context.Context, accountID, from, to string, c UsageChange) error {
	if from == to || c.IsZero() {
		return nil
	}
	return tx.AdjustFolders(ctx, accountID,
		FolderChange{FolderID: from, Change: UsageChange{Bytes: -c.Bytes, Files: -c.Files, Reserved: -c.Reserved}},
		FolderChange{FolderID: to, Change: c})
}

// AdjustFolders applies each change to its folder and the sum of them to the account. A counter never goes
// below zero: a change that would take one below zero leaves it at zero and logs a warning, because it means
// the counters had drifted, and a delete or shrink must never fail on that account. A folder that no longer
// exists (already deleted in this transaction) has no counter left to change; its share goes off the account.
// `selfcloud admin recount-usage` corrects the numbers.
func (tx *Tx) AdjustFolders(ctx context.Context, accountID string, parts ...FolderChange) error {
	var net UsageChange
	var owners []string
	for _, p := range parts {
		if p.FolderID == "" {
			return errors.New("store: a usage change must name its folder")
		}
		owner, err := tx.adjustFolder(ctx, accountID, p.FolderID, p.Change)
		if err != nil {
			return err
		}
		if owner != "" {
			owners = append(owners, owner)
		}
		net.Bytes += p.Change.Bytes
		net.Files += p.Change.Files
		net.Reserved += p.Change.Reserved
	}
	return tx.settleAccount(ctx, accountID, net, owners)
}

// settleAccount applies the net change to the account and, when an endpoint watches a quota event, notes the account and
// the owners of the personal folders the change touched. The quota levels are checked once, when the transaction is about
// to commit (settleQuotas), on the state it leaves, so a change that dips and returns is not announced. The account row
// just written says whether anyone watches, so a change in an account that nobody watches notes nothing.
func (tx *Tx) settleAccount(ctx context.Context, accountID string, net UsageChange, owners []string) error {
	acc, err := tx.adjustAccount(ctx, accountID, net)
	if err != nil {
		return err
	}
	if acc.Watched {
		tx.noteQuota(accountID, owners...)
	}
	return nil
}

// adjustFolder applies c to one folder's counters, clamped at zero as the account's are. It returns the folder's owner
// when it is a personal folder. The row it reads holds the owner, so a change to a team space costs no more than it did.
func (tx *Tx) adjustFolder(ctx context.Context, accountID, folderID string, c UsageChange) (owner string, err error) {
	var cur Usage
	var ownerID sql.NullString
	err = tx.q.QueryRowContext(ctx, "SELECT used_bytes, used_files, reserved_bytes, owner_user_id FROM folders WHERE account_id = ? AND id = ?",
		accountID, folderID).Scan(&cur.Bytes, &cur.Files, &cur.Reserved, &ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	next := clampUsage(tx, accountID, "folder "+folderID, cur, c)
	_, err = tx.q.ExecContext(ctx, `UPDATE folders SET used_bytes = ?, used_files = ?, reserved_bytes = ?
		WHERE account_id = ? AND id = ?`, next.Bytes, next.Files, next.Reserved, accountID, folderID)
	return ownerID.String, err
}

// accountAfter is an account as a change leaves it: its counters, and whether an active endpoint subscribes to a quota
// event (Watched). adjustAccount reads that in the statement that reads the counters, so asking costs no extra statement.
type accountAfter struct {
	Usage
	Watched bool
}

// adjustAccount applies c to the account's counters, clamped at zero, and returns the account after the change.
func (tx *Tx) adjustAccount(ctx context.Context, accountID string, c UsageChange) (accountAfter, error) {
	var cur accountAfter
	err := tx.q.QueryRowContext(ctx, `SELECT used_bytes, used_files, reserved_bytes, quota_watched = 1
		FROM accounts WHERE id = ?`, accountID).
		Scan(&cur.Bytes, &cur.Files, &cur.Reserved, &cur.Watched)
	if errors.Is(err, sql.ErrNoRows) {
		return cur, ErrNotFound
	}
	if err != nil {
		return cur, err
	}
	cur.Usage = clampUsage(tx, accountID, "account", cur.Usage, c)
	if err := rowsAffected(tx.q.ExecContext(ctx, `UPDATE accounts SET used_bytes = ?, used_files = ?,
		reserved_bytes = ? WHERE id = ?`, cur.Bytes, cur.Files, cur.Reserved, accountID)); err != nil {
		return cur, err
	}
	return cur, nil
}

// clampUsage is cur with c added, each counter kept at zero or above, with a warning when that happened.
func clampUsage(tx *Tx, accountID, what string, cur Usage, c UsageChange) Usage {
	next := Usage{Bytes: cur.Bytes + c.Bytes, Files: cur.Files + c.Files, Reserved: cur.Reserved + c.Reserved}
	if next.Bytes < 0 || next.Files < 0 || next.Reserved < 0 {
		if tx.log != nil {
			tx.log.Warn("usage counter would go negative; clamped to zero", "account", accountID, "scope", what,
				"used_bytes", cur.Bytes, "change_bytes", c.Bytes, "used_files", cur.Files, "change_files", c.Files,
				"reserved_bytes", cur.Reserved, "change_reserved", c.Reserved)
		}
		next = Usage{Bytes: max(next.Bytes, 0), Files: max(next.Files, 0), Reserved: max(next.Reserved, 0)}
	}
	return next
}

// usage reads an account's counters inside the transaction. ErrNotFound if the account is gone.
func (tx *Tx) usage(ctx context.Context, accountID string) (Usage, error) {
	var u Usage
	err := tx.q.QueryRowContext(ctx, "SELECT used_bytes, used_files, reserved_bytes FROM accounts WHERE id = ?",
		accountID).Scan(&u.Bytes, &u.Files, &u.Reserved)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

// QuotaState is what a write checks before it grows a folder: the account's quota and counters, the
// folder's kind, owner and counters, and the owner's effective limit. One joined read serves them all.
type QuotaState struct {
	AccountQuota int64 // the account's quota; 0 = none
	Account      Usage
	FolderKind   string
	OwnerUserID  string // the user of a personal folder; "" for a team space
	Folder       Usage
	UserLimit    int64 // the owner's own limit, or the account default when it follows it; 0 = none
}

// QuotaStateOf reads a QuotaState inside the transaction.
func (tx *Tx) QuotaStateOf(ctx context.Context, accountID, folderID string) (QuotaState, error) {
	return quotaState(ctx, tx.q, accountID, folderID)
}

// QuotaStateOf reads a QuotaState outside a transaction.
func (s *Store) QuotaStateOf(ctx context.Context, accountID, folderID string) (QuotaState, error) {
	return quotaState(ctx, s.r, accountID, folderID)
}

const quotaStateSQL = `SELECT a.quota_bytes, a.used_bytes, a.used_files, a.reserved_bytes,
	f.kind, COALESCE(f.owner_user_id, ''), f.used_bytes, f.used_files, f.reserved_bytes,
	COALESCE(u.quota_bytes, a.user_quota_bytes)
	FROM accounts a JOIN folders f ON f.account_id = a.id
	LEFT JOIN users u ON u.id = f.owner_user_id
	WHERE a.id = ? AND f.id = ?`

func quotaState(ctx context.Context, q querier, accountID, folderID string) (QuotaState, error) {
	var s QuotaState
	err := q.QueryRowContext(ctx, quotaStateSQL, accountID, folderID).Scan(&s.AccountQuota,
		&s.Account.Bytes, &s.Account.Files, &s.Account.Reserved,
		&s.FolderKind, &s.OwnerUserID, &s.Folder.Bytes, &s.Folder.Files, &s.Folder.Reserved, &s.UserLimit)
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	return s, err
}

// RecountAccount recomputes an account's counters and its folders' counters from the rows they count. A
// folder's used_bytes come from its file nodes and their versions, used_files from its file nodes, and
// reserved_bytes from the unfinished uploads that name it; the account's are the sums over the same rows.
// It returns the account's counters before and after. Call it in a transaction of its own, per account.
func (tx *Tx) RecountAccount(ctx context.Context, accountID string) (before, after Usage, err error) {
	before, err = tx.usage(ctx, accountID)
	if err != nil {
		return before, after, err
	}
	if _, err = tx.q.ExecContext(ctx, `UPDATE folders SET
		used_bytes = (SELECT COALESCE(SUM(n.size), 0) FROM nodes n WHERE n.folder_id = folders.id AND n.kind = 'file')
			+ (SELECT COALESCE(SUM(v.size), 0) FROM versions v JOIN nodes n ON n.id = v.node_id WHERE n.folder_id = folders.id),
		used_files = (SELECT COUNT(*) FROM nodes n WHERE n.folder_id = folders.id AND n.kind = 'file'),
		reserved_bytes = (SELECT COALESCE(SUM(CASE WHEN u.visitor_id = '' THEN u.length ELSE u.durable_offset END), 0)
			FROM uploads u WHERE u.folder_id = folders.id)
		WHERE account_id = ?`, accountID); err != nil {
		return before, after, err
	}
	err = tx.q.QueryRowContext(ctx, `SELECT COALESCE(SUM(used_bytes), 0), COALESCE(SUM(used_files), 0),
		COALESCE(SUM(reserved_bytes), 0) FROM folders WHERE account_id = ?`, accountID).
		Scan(&after.Bytes, &after.Files, &after.Reserved)
	if err != nil {
		return before, after, err
	}
	err = rowsAffected(tx.q.ExecContext(ctx, `UPDATE accounts SET used_bytes = ?, used_files = ?,
		reserved_bytes = ? WHERE id = ?`, after.Bytes, after.Files, after.Reserved, accountID))
	return before, after, err
}

// CounterDrift lists the accounts (up to limit) whose counters differ from the sum of their folders'.
// The sweep reports them; it never repairs them.
func (tx *Tx) CounterDrift(ctx context.Context, limit int) ([]string, error) {
	rows, err := tx.q.QueryContext(ctx, `SELECT a.id FROM accounts a WHERE
		a.used_bytes <> (SELECT COALESCE(SUM(f.used_bytes), 0) FROM folders f WHERE f.account_id = a.id)
		OR a.used_files <> (SELECT COALESCE(SUM(f.used_files), 0) FROM folders f WHERE f.account_id = a.id)
		OR a.reserved_bytes <> (SELECT COALESCE(SUM(f.reserved_bytes), 0) FROM folders f WHERE f.account_id = a.id)
		ORDER BY a.id LIMIT ?`, limit)
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

// UsageTotals sums the counters of every account: what the installation holds in total.
func (s *Store) UsageTotals(ctx context.Context) (used, reserved int64, err error) {
	err = s.r.QueryRowContext(ctx, "SELECT COALESCE(SUM(used_bytes), 0), COALESCE(SUM(reserved_bytes), 0) FROM accounts").
		Scan(&used, &reserved)
	return used, reserved, err
}

// ReservationTotals returns the sum of the reserved_bytes counters and the sum of the lengths of
// the unfinished uploads. They agree unless the counters have drifted; the sweep compares them.
func (tx *Tx) ReservationTotals(ctx context.Context) (counted, declared int64, err error) {
	err = tx.q.QueryRowContext(ctx, `SELECT (SELECT COALESCE(SUM(reserved_bytes), 0) FROM accounts),
		(SELECT COALESCE(SUM(CASE WHEN visitor_id = '' THEN length ELSE durable_offset END), 0) FROM uploads)`).Scan(&counted, &declared)
	return counted, declared, err
}

// CountFolders counts the top-level folders of an account.
func (s *Store) CountFolders(ctx context.Context, accountID string) (int64, error) {
	var n int64
	err := s.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM folders WHERE account_id = ?", accountID).Scan(&n)
	return n, err
}
