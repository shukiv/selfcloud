// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"maps"
	"slices"
	"time"

	"github.com/shukiv/selfcloud/internal/webhook"
)

// Quota warnings (design section 10, slice j4, decision 30). quota.warning is sent when what the quota counts reaches the
// account's warning percent of the account's quota, or of a user's limit on its personal folder. quota.exceeded is sent
// when it reaches 100 percent. What a level measures is used bytes: a reservation is not usage, so taking or cancelling one
// says nothing (refusals still count reservations, in internal/api). A level is crossed once: a mark row says the scope is
// past that level, and it is written with the event. When the scope falls below the level, the row is deleted, so a later
// crossing sends again.
//
// The levels are checked once per transaction, when it is about to commit (Store.Write, settleQuotas), on the state it
// leaves. A transaction that dips below a level and returns before it commits sends nothing, and no call site can forget
// the check. The usage changes note what they touched (noteQuota); the check reads the counters afresh.
//
// The marks are kept only while the account is watched: it has an active endpoint that subscribes to a quota event. The
// flag accounts.quota_watched says so, and the endpoint writes keep it (syncQuotaWatch). A mark for a level exists only
// while an active endpoint subscribes to that level's event, so a subscriber added later hears the next change above the
// level. Editing a limit or the percent clears the marks it changes, in the same transaction; the edit itself sends nothing.

const (
	quotaScopeAccount  = "account"
	quotaScopeUser     = "user"
	quotaLevelWarning  = "warning"
	quotaLevelExceeded = "exceeded"
	// WebhookQuotaWarningDefault is the warning percent of an account that has not set one.
	WebhookQuotaWarningDefault = 80
)

// quotaScope is a scope as the transaction leaves it: its kind and ID, what the quota counts, the limit, and the percent that
// the warning level is taken at.
type quotaScope struct {
	kind, id         string
	held, limit, pct int64
}

// quotaTouch is what a transaction changed in one watched account: the owners of the personal folders whose usage moved.
// The account itself is always checked.
type quotaTouch struct {
	owners map[string]bool
}

// noteQuota records that the transaction changed usage of accountID, and of the personal folders of owners. The check runs
// when the transaction commits.
func (tx *Tx) noteQuota(accountID string, owners ...string) {
	if tx.quota == nil {
		tx.quota = map[string]*quotaTouch{}
	}
	t := tx.quota[accountID]
	if t == nil {
		t = &quotaTouch{owners: map[string]bool{}}
		tx.quota[accountID] = t
	}
	for _, o := range owners {
		if o != "" {
			t.owners[o] = true
		}
	}
}

// settleQuotas checks, for each account the transaction touched, its account level and the levels of the owners it touched,
// against the state the transaction leaves. Store.Write calls it before the commit, so its events and marks commit with the
// change, or are rolled back with it.
func (tx *Tx) settleQuotas(ctx context.Context) error {
	touched := tx.quota
	tx.quota = nil
	for _, accountID := range slices.Sorted(maps.Keys(touched)) {
		if err := tx.settleAccountQuota(ctx, accountID, touched[accountID]); err != nil {
			return err
		}
	}
	return nil
}

// settleAccountQuota checks one account's scopes. An account nobody watches, or one that is gone, has nothing to check.
func (tx *Tx) settleAccountQuota(ctx context.Context, accountID string, t *quotaTouch) error {
	var used, quota, pct int64
	var watched bool
	err := tx.q.QueryRowContext(ctx, `SELECT used_bytes, quota_bytes, quota_warning_percent, quota_watched = 1
		FROM accounts WHERE id = ?`, accountID).Scan(&used, &quota, &pct, &watched)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !watched {
		return nil
	}
	at := Now()
	if err := tx.crossLevels(ctx, accountID, quotaScope{kind: quotaScopeAccount, id: accountID,
		held: used, limit: quota, pct: pct}, at); err != nil {
		return err
	}
	for _, owner := range slices.Sorted(maps.Keys(t.owners)) {
		held, limit, err := tx.userQuotaOf(ctx, accountID, owner)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if err := tx.crossLevels(ctx, accountID, quotaScope{kind: quotaScopeUser, id: owner,
			held: held, limit: limit, pct: pct}, at); err != nil {
			return err
		}
	}
	return nil
}

// userQuotaOf is what a user's personal folder uses, and its limit: the user's own, or the account's default when the
// user's follows it. A limit of 0 is none. ErrNotFound when the user has no personal folder in the account.
func (tx *Tx) userQuotaOf(ctx context.Context, accountID, userID string) (used, limit int64, err error) {
	err = tx.q.QueryRowContext(ctx, `SELECT f.used_bytes, COALESCE(u.quota_bytes, a.user_quota_bytes)
		FROM folders f JOIN users u ON u.id = f.owner_user_id JOIN accounts a ON a.id = f.account_id
		WHERE f.account_id = ? AND f.owner_user_id = ? AND f.kind = ?`, accountID, userID, KindPersonal).Scan(&used, &limit)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, ErrNotFound
	}
	return used, limit, err
}

// crossLevels makes the marks of one scope agree with where it stands: a level it is at or above and has no mark for is
// sent and marked, if an active endpoint subscribes to its event, and is otherwise left unmarked for a later subscriber; a
// level it is below and has a mark for is unmarked. A limit of 0 is no limit, so no level is reached. The warning is sent
// before exceeded when one change passes both.
func (tx *Tx) crossLevels(ctx context.Context, accountID string, sc quotaScope, at time.Time) error {
	marked, err := tx.quotaMarks(ctx, accountID, sc.kind, sc.id)
	if err != nil {
		return err
	}
	levels := [...]struct {
		level, event string
		percent      int64
	}{
		{quotaLevelWarning, "quota.warning", sc.pct},
		{quotaLevelExceeded, "quota.exceeded", 100},
	}
	for _, lv := range levels {
		above := sc.limit > 0 && sc.held*100 >= sc.limit*lv.percent
		switch {
		case above && !marked[lv.level]:
			subscribed, err := tx.subscribed(ctx, accountID, lv.event)
			if err != nil {
				return err
			}
			if !subscribed {
				break
			}
			data := webhook.Quota{Scope: sc.kind, ID: sc.id, Percent: lv.percent, UsedBytes: sc.held, LimitBytes: sc.limit}
			if err := tx.EmitEvent(ctx, accountID, lv.event, at, data); err != nil {
				return err
			}
			// The mark is written only while someone still watches: EmitEvent may have disabled the last watcher
			// (an overflow), and syncQuotaWatch has then cleared the account's marks and its flag.
			if _, err := tx.q.ExecContext(ctx, `INSERT INTO webhook_quota_marks (account_id, scope, scope_id, level, set_at)
				SELECT ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM accounts WHERE id = ? AND quota_watched = 1)`,
				accountID, sc.kind, sc.id, lv.level, millis(at), accountID); err != nil {
				return err
			}
		case !above && marked[lv.level]:
			if _, err := tx.q.ExecContext(ctx, `DELETE FROM webhook_quota_marks
				WHERE account_id = ? AND scope = ? AND scope_id = ? AND level = ?`,
				accountID, sc.kind, sc.id, lv.level); err != nil {
				return err
			}
		}
	}
	return nil
}

// subscribed reports whether an active endpoint of accountID subscribes to event. It is the fan-out's answer, read once
// per transaction.
func (tx *Tx) subscribed(ctx context.Context, accountID, event string) (bool, error) {
	f, err := tx.fanout(ctx, accountID)
	if err != nil {
		return false, err
	}
	return len(f.subs[event]) > 0, nil
}

// quotaMarks returns the levels that a scope is marked at.
func (tx *Tx) quotaMarks(ctx context.Context, accountID, kind, scopeID string) (map[string]bool, error) {
	rows, err := tx.q.QueryContext(ctx, `SELECT level FROM webhook_quota_marks
		WHERE account_id = ? AND scope = ? AND scope_id = ?`, accountID, kind, scopeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var level string
		if err := rows.Scan(&level); err != nil {
			return nil, err
		}
		out[level] = true
	}
	return out, rows.Err()
}

// syncQuotaWatch sets accounts.quota_watched from the endpoints of accountID, and removes each mark whose level no active
// endpoint subscribes to. Every write that adds, changes or removes an endpoint calls it, so the flag, and the marks, never
// outlive the subscriptions that keep them right. It runs in the transaction of the change.
func (tx *Tx) syncQuotaWatch(ctx context.Context, accountID string) error {
	if _, err := tx.q.ExecContext(ctx, `UPDATE accounts SET quota_watched = EXISTS (
		SELECT 1 FROM webhook_endpoint_events s JOIN webhook_endpoints e ON e.id = s.endpoint_id AND e.account_id = s.account_id
		WHERE s.account_id = accounts.id AND e.state = ? AND s.event IN ('quota.warning', 'quota.exceeded'))
		WHERE id = ?`, WebhookActive, accountID); err != nil {
		return err
	}
	_, err := tx.q.ExecContext(ctx, `DELETE FROM webhook_quota_marks WHERE account_id = ? AND NOT EXISTS (
		SELECT 1 FROM webhook_endpoint_events s JOIN webhook_endpoints e ON e.id = s.endpoint_id AND e.account_id = s.account_id
		WHERE s.account_id = webhook_quota_marks.account_id AND e.state = ?
		AND s.event = CASE webhook_quota_marks.level WHEN 'exceeded' THEN 'quota.exceeded' ELSE 'quota.warning' END)`,
		accountID, WebhookActive)
	return err
}

// SetQuotaWarningPercent sets the account's warning level. The caller checks that percent is 1 to 99. A changed level clears
// the account's warning marks, so the next change above the new level is announced; the change itself sends nothing.
func (tx *Tx) SetQuotaWarningPercent(ctx context.Context, accountID string, percent int64, at time.Time) error {
	var old int64
	if err := tx.q.QueryRowContext(ctx, "SELECT quota_warning_percent FROM accounts WHERE id = ?", accountID).Scan(&old); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if err := rowsAffected(tx.q.ExecContext(ctx, "UPDATE accounts SET quota_warning_percent = ?, updated_at = ? WHERE id = ?",
		percent, millis(at), accountID)); err != nil {
		return err
	}
	if old == percent {
		return nil
	}
	_, err := tx.q.ExecContext(ctx, "DELETE FROM webhook_quota_marks WHERE account_id = ? AND level = ?", accountID, quotaLevelWarning)
	return err
}

// forgetAccountQuotaMarks clears the marks of an account's own quota, after its quota changed. The edit sends nothing.
func (tx *Tx) forgetAccountQuotaMarks(ctx context.Context, accountID string) error {
	_, err := tx.q.ExecContext(ctx, "DELETE FROM webhook_quota_marks WHERE account_id = ? AND scope = ?", accountID, quotaScopeAccount)
	return err
}

// forgetDefaultUserQuotaMarks clears the marks of the users whose limit follows the account's default, after the default
// changed. A user with its own limit is not affected, so its marks stay.
func (tx *Tx) forgetDefaultUserQuotaMarks(ctx context.Context, accountID string) error {
	_, err := tx.q.ExecContext(ctx, `DELETE FROM webhook_quota_marks WHERE account_id = ? AND scope = ? AND scope_id IN (
		SELECT id FROM users WHERE account_id = ? AND quota_bytes IS NULL)`, accountID, quotaScopeUser, accountID)
	return err
}

// QuotaWarningPercent returns the account's warning level. Another account's ID is ErrNotFound.
func (s *Store) QuotaWarningPercent(ctx context.Context, accountID string) (int64, error) {
	var p int64
	err := s.r.QueryRowContext(ctx, "SELECT quota_warning_percent FROM accounts WHERE id = ?", accountID).Scan(&p)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return p, err
}

// deleteUserQuotaMarks removes the marks of one user: when the user is deleted, or its own limit changes. A user's ID is
// never reused, so a deleted user's marks match no scope.
func (tx *Tx) deleteUserQuotaMarks(ctx context.Context, userID string) error {
	_, err := tx.q.ExecContext(ctx, "DELETE FROM webhook_quota_marks WHERE scope = ? AND scope_id = ?", quotaScopeUser, userID)
	return err
}

// PruneQuotaMarks removes the marks that no longer hold: a level the scope is back under, a limit that is gone, a user
// that is deleted, and every mark of an account nothing watches. The writes above keep the marks right
// already, so this removes only what a path did not see, such as a recount that moved the counters. It returns how many
// marks it removed. The sweep runs it.
func (s *Store) PruneQuotaMarks(ctx context.Context) (int, error) {
	var n int
	err := s.Write(ctx, func(tx *Tx) error {
		var err error
		n, err = rowsOf(tx.q.ExecContext(ctx, `DELETE FROM webhook_quota_marks WHERE rowid IN (
			SELECT m.rowid FROM webhook_quota_marks m
			JOIN accounts a ON a.id = m.account_id
			LEFT JOIN users u ON m.scope = 'user' AND u.id = m.scope_id
			LEFT JOIN folders f ON m.scope = 'user' AND f.kind = 'personal' AND f.owner_user_id = m.scope_id AND f.account_id = m.account_id
			WHERE (m.scope = 'user' AND u.id IS NULL)
				OR a.quota_watched = 0
				OR (CASE WHEN m.scope = 'user' THEN COALESCE(u.quota_bytes, a.user_quota_bytes) ELSE a.quota_bytes END) <= 0
				OR (CASE WHEN m.scope = 'user' THEN COALESCE(f.used_bytes, 0) ELSE a.used_bytes END) * 100
					< (CASE WHEN m.scope = 'user' THEN COALESCE(u.quota_bytes, a.user_quota_bytes) ELSE a.quota_bytes END)
						* (CASE m.level WHEN 'exceeded' THEN 100 ELSE a.quota_warning_percent END)
		)`))
		return err
	})
	return n, err
}
