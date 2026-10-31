// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// seedNotify makes account acc_n with a folder, a node nod_n, a comment cmt_n by usr_a, and two users usr_a and usr_b.
// Only the rows the notification tests read are made; the schema's NOT NULL columns are filled.
func seedNotify(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_n', 'N', 'active', 0, 0)`,
		`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_m', 'M', 'active', 0, 0)`,
		`INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at) VALUES ('fld_n', 'acc_n', 'Docs', 'docs', 0, 0)`,
		`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
			VALUES ('nod_n', 'acc_n', 'fld_n', NULL, 'Report', 'report', 'file', 0, 0)`,
		`INSERT INTO users (id, account_id, email, email_key, display_name, role, status, created_at, updated_at)
			VALUES ('usr_a', 'acc_n', 'a@example.test', 'a@example.test', 'Ann', 'member', 'active', 0, 0)`,
		`INSERT INTO users (id, account_id, email, email_key, display_name, role, status, created_at, updated_at)
			VALUES ('usr_b', 'acc_n', 'b@example.test', 'b@example.test', 'Bob', 'member', 'active', 0, 0)`,
		`INSERT INTO comments (id, account_id, node_id, author_id, body, created_at)
			VALUES ('cmt_n', 'acc_n', 'nod_n', 'usr_a', 'hello', 1000)`,
		`INSERT INTO comments (id, account_id, node_id, author_id, body, created_at)
			SELECT 'cmt_' || v, 'acc_n', 'nod_n', 'usr_a', 'hello', 1000 FROM (SELECT '1' AS v UNION ALL SELECT '2'
			UNION ALL SELECT 'old' UNION ALL SELECT 'new' UNION ALL SELECT 'over' UNION ALL SELECT 'c' UNION ALL SELECT 'r')`,
		`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 600)
			INSERT INTO comments (id, account_id, node_id, author_id, body, created_at)
			SELECT printf('cmt_%04d', i), 'acc_n', 'nod_n', 'usr_a', 'hello', 1000 FROM n`,
	} {
		if _, err := s.w.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
}

// notifyAt is one mention of usr_b by usr_a on cmt_n, with the ID n.
func notifyAt(n string, at time.Time) NewNotification {
	return NewNotification{ID: "ntf_" + n, AccountID: "acc_n", UserID: "usr_b", Kind: NotifyMention, NodeID: "nod_n",
		CommentID: "cmt_" + n, ActorID: "usr_a", At: at}
}

// countRows counts the rows of a query that takes no arguments.
func notifyCount(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.r.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestNotifyRowsCarryNoText reads the columns of the notification tables. A notification and its outbox row hold IDs, a
// kind and times; a column for a name or a text would let a later change store one.
func TestNotifyRowsCarryNoText(t *testing.T) {
	s := openTest(t)
	forbidden := map[string]bool{"body": true, "name": true, "text": true, "display_name": true,
		"item_name": true, "subject": true, "last_error_text": true}
	for _, table := range []string{"notifications", "notify_outbox", "notify_prefs"} {
		rows, err := s.r.QueryContext(context.Background(), "SELECT name FROM pragma_table_info(?)", table)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var col string
			if err := rows.Scan(&col); err != nil {
				t.Fatal(err)
			}
			if forbidden[col] {
				t.Errorf("%s has a column %q that could hold a name or text", table, col)
			}
		}
		rows.Close()
	}
}

// TestClaimLeasesEachRowOnce: a claim leases the due rows, a second claim in the lease finds none, and a claim after the
// lease gets them again. An outcome from the first claim, which no longer holds the row, changes nothing.
func TestClaimLeasesEachRowOnce(t *testing.T) {
	s := openTest(t)
	seedNotify(t, s)
	ctx := context.Background()
	t0 := time.Unix(10_000, 0)
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.InsertNotifications(ctx, []NewNotification{notifyAt("1", t0), notifyAt("2", t0)}, true)
	}); err != nil {
		t.Fatal(err)
	}
	first, err := s.ClaimMail(ctx, t0, 2*time.Minute, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 {
		t.Fatalf("first claim got %d rows, want 2", len(first))
	}
	again, err := s.ClaimMail(ctx, t0.Add(time.Minute), 2*time.Minute, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("a second claim inside the lease got %d rows", len(again))
	}
	later, err := s.ClaimMail(ctx, t0.Add(3*time.Minute), 2*time.Minute, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(later) != 2 {
		t.Fatalf("a claim after the lease got %d rows, want 2", len(later))
	}
	// The first claim's outcome is stale: the row holds the later lease, so nothing is recorded.
	if err := s.FinishMail(ctx, NotifyOutcome{OutboxID: first[0].OutboxID, Lease: first[0].Lease, State: OutboxSent,
		At: t0}); err != nil {
		t.Fatal(err)
	}
	if got := notifyCount(t, s, "SELECT COUNT(*) FROM notify_outbox WHERE state = 'sent'"); got != 0 {
		t.Fatalf("a stale outcome recorded %d sent rows", got)
	}
}

// TestHourAndDayBudget: a user is charged at most one mail an hour and NotifyDayCap in a rolling day. A refused charge
// says when the next may come.
func TestHourAndDayBudget(t *testing.T) {
	s := openTest(t)
	seedNotify(t, s)
	ctx := context.Background()
	t0 := time.Unix(20_000, 0)
	ok, reason, retry, err := s.ChargeMail(ctx, "acc_n", "usr_b", t0)
	if err != nil || !ok {
		t.Fatalf("first charge = %v %q %v", ok, reason, err)
	}
	ok, reason, retry, err = s.ChargeMail(ctx, "acc_n", "usr_b", t0.Add(30*time.Minute))
	if err != nil || ok || reason != "hour" || !retry.Equal(t0.Add(time.Hour)) {
		t.Fatalf("charge inside the hour = %v %q retry %v %v, want refused for the hour until %v", ok, reason, retry, err,
			t0.Add(time.Hour))
	}
	for i := 1; i < NotifyDayCap; i++ {
		ok, reason, _, err = s.ChargeMail(ctx, "acc_n", "usr_b", t0.Add(time.Duration(i)*time.Hour))
		if err != nil || !ok {
			t.Fatalf("charge %d = %v %q %v", i+1, ok, reason, err)
		}
	}
	ok, reason, retry, err = s.ChargeMail(ctx, "acc_n", "usr_b", t0.Add(time.Duration(NotifyDayCap)*time.Hour))
	if err != nil || ok || reason != "day" || !retry.Equal(t0.Add(24*time.Hour)) {
		t.Fatalf("charge past the day cap = %v %q retry %v %v, want refused for the day until %v", ok, reason, retry, err,
			t0.Add(24*time.Hour))
	}
	ok, _, _, err = s.ChargeMail(ctx, "acc_n", "usr_b", t0.Add(24*time.Hour+time.Minute))
	if err != nil || !ok {
		t.Fatalf("charge after the day window = %v %v", ok, err)
	}
}

// TestBudgetHoldsAcrossReopen: a charge made before a restart still holds after it. The budget is in the database.
func TestBudgetHoldsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "reopen.db")
	seedMigratedDB(t, path)
	s, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	seedNotify(t, s)
	t0 := time.Unix(30_000, 0)
	if ok, _, _, err := s.ChargeMail(ctx, "acc_n", "usr_b", t0); err != nil || !ok {
		t.Fatalf("charge = %v %v", ok, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ok, reason, _, err := s.ChargeMail(ctx, "acc_n", "usr_b", t0.Add(10*time.Minute))
	if err != nil || ok || reason != "hour" {
		t.Fatalf("after a restart, charge = %v %q %v, want refused for the hour", ok, reason, err)
	}
}

// TestAccountHourCap: an account charges at most NotifyAccountHourCap users in an hour; the next user waits for the hour.
func TestAccountHourCap(t *testing.T) {
	s := openTest(t)
	seedNotify(t, s)
	ctx := context.Background()
	if _, err := s.w.ExecContext(ctx, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?)
		INSERT INTO users (id, account_id, email, email_key, display_name, role, status, created_at, updated_at)
		SELECT printf('usr_x%04d', i), 'acc_n', 'x' || i || '@example.test', 'x' || i || '@example.test', 'X', 'member',
			'active', 0, 0 FROM n`, NotifyAccountHourCap); err != nil {
		t.Fatal(err)
	}
	t0 := time.Unix(40_000, 0)
	for i := 1; i <= NotifyAccountHourCap; i++ {
		ok, reason, _, err := s.ChargeMail(ctx, "acc_n", userN(i), t0)
		if err != nil || !ok {
			t.Fatalf("user %d = %v %q %v", i, ok, reason, err)
		}
	}
	ok, reason, _, err := s.ChargeMail(ctx, "acc_n", "usr_b", t0)
	if err != nil || ok || reason != "account" {
		t.Fatalf("user past the account cap = %v %q %v, want refused for the account", ok, reason, err)
	}
}

// userN names the seeded users of TestAccountHourCap.
func userN(i int) string {
	return "usr_x" + string([]byte{byte('0' + i/1000%10), byte('0' + i/100%10), byte('0' + i/10%10), byte('0' + i%10)})
}

// TestPerUserCapKeepsNewest: a user keeps NotifyPerUser notifications. The oldest go in the write that passes the cap, and
// their outbox rows with them.
func TestPerUserCapKeepsNewest(t *testing.T) {
	s := openTest(t)
	seedNotify(t, s)
	ctx := context.Background()
	t0 := time.Unix(50_000, 0)
	rows := make([]NewNotification, 0, NotifyPerUser+5)
	for i := 0; i < NotifyPerUser+5; i++ {
		rows = append(rows, notifyAt(userN(i + 1)[len("usr_x"):], t0.Add(time.Duration(i)*time.Second)))
	}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertNotifications(ctx, rows, true) }); err != nil {
		t.Fatal(err)
	}
	if got := notifyCount(t, s, "SELECT COUNT(*) FROM notifications WHERE user_id = 'usr_b'"); got != NotifyPerUser {
		t.Fatalf("user keeps %d notifications, want %d", got, NotifyPerUser)
	}
	if got := notifyCount(t, s, "SELECT COUNT(*) FROM notifications WHERE id = 'ntf_'||?", userN(1)[len("usr_x"):]); got != 0 {
		t.Fatal("the oldest notification was kept")
	}
	if got := notifyCount(t, s, "SELECT COUNT(*) FROM notify_outbox WHERE user_id = 'usr_b'"); got != NotifyPerUser {
		t.Fatalf("outbox rows = %d, want one for each kept notification (%d)", got, NotifyPerUser)
	}
}

// TestPendingCapStopsQueueing: past NotifyPendingLimit pending rows, a notification is written and no mail is queued.
func TestPendingCapStopsQueueing(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 100,000 rows")
	}
	s := openTest(t)
	seedNotify(t, s)
	ctx := context.Background()
	if _, err := s.w.ExecContext(ctx, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?)
		INSERT INTO comments (id, account_id, node_id, author_id, body, created_at)
		SELECT printf('cmt_p%07d', i), 'acc_n', 'nod_n', 'usr_a', 'hello', 0 FROM n`, NotifyPendingLimit); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?)
		INSERT INTO notifications (id, account_id, user_id, kind, node_id, comment_id, created_at)
		SELECT printf('ntf_p%07d', i), 'acc_n', 'usr_a', 'mention', 'nod_n', printf('cmt_p%07d', i), 0 FROM n`, NotifyPendingLimit); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, `INSERT INTO notify_outbox (account_id, user_id, notification_id, state, next_attempt_at, created_at)
		SELECT 'acc_n', 'usr_a', id, 'pending', 0, 0 FROM notifications WHERE id LIKE 'ntf_p%'`); err != nil {
		t.Fatal(err)
	}
	t0 := time.Unix(60_000, 0)
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.InsertNotifications(ctx, []NewNotification{notifyAt("over", t0)}, true)
	}); err != nil {
		t.Fatal(err)
	}
	if got := notifyCount(t, s, "SELECT COUNT(*) FROM notifications WHERE id = 'ntf_over'"); got != 1 {
		t.Fatal("a notification past the pending cap was not written")
	}
	if got := notifyCount(t, s, "SELECT COUNT(*) FROM notify_outbox WHERE notification_id = 'ntf_over'"); got != 0 {
		t.Fatal("mail was queued past the pending cap")
	}
}

// TestCascadesOnDeletes: a node, a user or an account that goes takes its notifications and mail rows with it.
func TestCascadesOnDeletes(t *testing.T) {
	cases := map[string]string{
		"node":    "DELETE FROM nodes WHERE id = 'nod_n'",
		"user":    "DELETE FROM users WHERE id = 'usr_b'",
		"account": "DELETE FROM accounts WHERE id = 'acc_n'",
		"comment": "DELETE FROM comments WHERE id = 'cmt_c'",
	}
	for name, del := range cases {
		t.Run(name, func(t *testing.T) {
			s := openTest(t)
			seedNotify(t, s)
			ctx := context.Background()
			if err := s.Write(ctx, func(tx *Tx) error {
				return tx.InsertNotifications(ctx, []NewNotification{notifyAt("c", time.Unix(70_000, 0))}, true)
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.w.ExecContext(ctx, del); err != nil {
				t.Fatal(err)
			}
			if got := notifyCount(t, s, "SELECT COUNT(*) FROM notifications"); got != 0 {
				t.Errorf("%s delete left %d notifications", name, got)
			}
			if got := notifyCount(t, s, "SELECT COUNT(*) FROM notify_outbox"); got != 0 {
				t.Errorf("%s delete left %d mail rows", name, got)
			}
		})
	}
}

// TestPruneAndExpire: notifications past their age go; a pending row past the mail keep is expired as skipped; a finished
// row past it goes.
func TestPruneAndExpire(t *testing.T) {
	s := openTest(t)
	seedNotify(t, s)
	ctx := context.Background()
	now := time.Unix(1_000_000_000, 0)
	old := now.Add(-100 * 24 * time.Hour)
	recent := now.Add(-time.Hour)
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.InsertNotifications(ctx, []NewNotification{notifyAt("old", old), notifyAt("new", recent)}, true)
	}); err != nil {
		t.Fatal(err)
	}
	n, err := s.PruneNotifications(ctx, now.Add(-90*24*time.Hour), 100)
	if err != nil || n != 1 {
		t.Fatalf("prune removed %d, %v; want the one old notification", n, err)
	}
	if got := notifyCount(t, s, "SELECT COUNT(*) FROM notify_outbox WHERE notification_id = 'ntf_old'"); got != 0 {
		t.Fatal("the mail row of a pruned notification was kept")
	}
	m, err := s.ExpireMail(ctx, now.Add(-7*24*time.Hour), now, 100)
	if err != nil || m != 0 {
		t.Fatalf("expire of a fresh row = %d, %v; want none", m, err)
	}
	if _, err := s.w.ExecContext(ctx, "UPDATE notify_outbox SET created_at = 0"); err != nil {
		t.Fatal(err)
	}
	m, err = s.ExpireMail(ctx, now.Add(-7*24*time.Hour), now, 100)
	if err != nil || m != 1 {
		t.Fatalf("expire = %d, %v; want the one pending row", m, err)
	}
	if got := notifyCount(t, s, "SELECT COUNT(*) FROM notify_outbox WHERE state = 'skipped' AND last_error = 'expired'"); got != 1 {
		t.Fatal("an expired row is not skipped with its code")
	}
	if _, err := s.w.ExecContext(ctx, "UPDATE notify_outbox SET finished_at = 0"); err != nil {
		t.Fatal(err)
	}
	r, err := s.PruneMailRows(ctx, now.Add(-7*24*time.Hour), 100)
	if err != nil || r != 1 {
		t.Fatalf("mail rows pruned = %d, %v; want the finished one", r, err)
	}
}

// TestReadIsOwnRowsOnly: a notification is marked read by its own user in its own account only. Another user's and
// another account's are not found, and the in-app reads leave out a deleted comment's notification.
func TestReadIsOwnRowsOnly(t *testing.T) {
	s := openTest(t)
	seedNotify(t, s)
	ctx := context.Background()
	t0 := time.Unix(80_000, 0)
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.InsertNotifications(ctx, []NewNotification{notifyAt("r", t0)}, false)
	}); err != nil {
		t.Fatal(err)
	}
	err := s.Write(ctx, func(tx *Tx) error {
		return tx.MarkNotificationRead(ctx, "acc_n", "usr_a", "ntf_r", t0)
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("another user marked it read: %v", err)
	}
	err = s.Write(ctx, func(tx *Tx) error {
		return tx.MarkNotificationRead(ctx, "acc_m", "usr_b", "ntf_r", t0)
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("another account marked it read: %v", err)
	}
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.MarkNotificationRead(ctx, "acc_n", "usr_b", "ntf_r", t0)
	}); err != nil {
		t.Fatal(err)
	}
	page, err := s.NotificationsPage(ctx, "acc_n", "usr_b", Page{Limit: 10})
	if err != nil || len(page) != 1 || page[0].ReadAt == nil {
		t.Fatalf("page = %+v, %v; want the one read notification", page, err)
	}
	if _, err := s.w.ExecContext(ctx, "UPDATE comments SET deleted_at = 1, body = '' WHERE id = 'cmt_r'"); err != nil {
		t.Fatal(err)
	}
	page, err = s.NotificationsPage(ctx, "acc_n", "usr_b", Page{Limit: 10})
	if err != nil || len(page) != 0 {
		t.Fatalf("a deleted comment's notification is listed: %+v, %v", page, err)
	}
}

// TestEmailSwitchDefaultsOn: a user with no preference row gets email; the switch keeps the budget it shares a row with.
func TestEmailSwitchDefaultsOn(t *testing.T) {
	s := openTest(t)
	seedNotify(t, s)
	ctx := context.Background()
	on, err := s.NotifyEmailOn(ctx, "usr_b")
	if err != nil || !on {
		t.Fatalf("default = %v, %v; want on", on, err)
	}
	t0 := time.Unix(90_000, 0)
	if ok, _, _, err := s.ChargeMail(ctx, "acc_n", "usr_b", t0); err != nil || !ok {
		t.Fatalf("charge = %v %v", ok, err)
	}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.SetNotifyEmail(ctx, "acc_n", "usr_b", false, t0) }); err != nil {
		t.Fatal(err)
	}
	on, err = s.NotifyEmailOn(ctx, "usr_b")
	if err != nil || on {
		t.Fatalf("after off = %v, %v; want off", on, err)
	}
	ok, reason, _, err := s.ChargeMail(ctx, "acc_n", "usr_b", t0.Add(2*time.Hour))
	if err != nil || !ok {
		t.Fatalf("the switch cleared the budget: %v %q %v", ok, reason, err)
	}
}

// TestNotificationSQLDoesNotScan reads the plan of each notification statement, as TestCommentsSQLDoesNotScan does for the
// comments. A scan of notifications or the outbox makes a user's bell or a worker pass cost the size of the whole table.
func TestNotificationSQLDoesNotScan(t *testing.T) {
	s := openTest(t)
	cases := []struct {
		name string
		sql  string
		args []any
	}{
		{"list, first page", notificationsSQL(false), []any{"acc_n", "usr_b", 50}},
		{"list, after a cursor", notificationsSQL(true), []any{"acc_n", "usr_b", 5, 5, "ntf_x", 50}},
		{"unread, newest", unreadSQL, []any{"acc_n", "usr_b", 100}},
		{"claim, users", claimUsersSQL, []any{5, 5, 5, 5, NotifyDayCap, 50}},
		{"claim, rows of a user", claimUserRowsSQL, []any{"usr_b", 5, 5, 20}},
		{"pending cap count", countOutboxPendingSQL, []any{NotifyPendingLimit}},
		{"per-user cap count", countUserNotifySQL, []any{"usr_b", NotifyPerUser + 1}},
		{"per-user cap, oldest", oldestOverCapSQL, []any{"usr_b", 1}},
		{"account hour count", accountHourSQL, []any{"acc_n", 5, NotifyAccountHourCap}},
		{"prefs of a user", "SELECT last_mail_at, mail_day_start, mail_day_count FROM notify_prefs WHERE user_id = ?",
			[]any{"usr_b"}},
		{"prune by age", pruneNotifySQL, []any{5, 500}},
		{"expire pending", expireMailSQL, []any{5, 5, 5, 500}},
		{"prune finished", pruneMailSQL, []any{5, 500}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			rows, err := s.r.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+c.sql, c.args...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var plan []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				plan = append(plan, detail)
				for _, bad := range []string{"SCAN notifications", "SCAN notify_outbox", "SCAN notify_prefs", "SCAN n ", "SCAN o ", "SCAN p "} {
					if detail == bad || len(detail) >= len(bad) && detail[:len(bad)] == bad {
						t.Errorf("%s scans a table: %q\nplan: %v", c.name, detail, plan)
					}
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if len(plan) == 0 {
				t.Fatal("no query plan")
			}
			t.Logf("plan: %v", plan)
		})
	}
}

// TestAccountPendingCapLeavesOtherAccountsMail: an account at NotifyAccountPendingLimit queues no more mail, and another
// account's notification is still queued (review D3). The global cap is only a backstop.
func TestAccountPendingCapLeavesOtherAccountsMail(t *testing.T) {
	s := openTest(t)
	seedNotify(t, s)
	ctx := context.Background()
	seedOtherAccount(t, s)
	if _, err := s.w.ExecContext(ctx, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?)
		INSERT INTO comments (id, account_id, node_id, author_id, body, created_at)
		SELECT printf('cmt_q%07d', i), 'acc_n', 'nod_n', 'usr_a', 'hello', 0 FROM n`, NotifyAccountPendingLimit); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?)
		INSERT INTO notifications (id, account_id, user_id, kind, node_id, comment_id, created_at)
		SELECT printf('ntf_q%07d', i), 'acc_n', 'usr_a', 'mention', 'nod_n', printf('cmt_q%07d', i), 0 FROM n`, NotifyAccountPendingLimit); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, `INSERT INTO notify_outbox (account_id, user_id, notification_id, state, next_attempt_at, created_at)
		SELECT 'acc_n', 'usr_a', id, 'pending', 0, 0 FROM notifications WHERE id LIKE 'ntf_q%'`); err != nil {
		t.Fatal(err)
	}
	t0 := time.Unix(60_000, 0)
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.InsertNotifications(ctx, []NewNotification{notifyAt("over", t0)}, true)
	}); err != nil {
		t.Fatal(err)
	}
	if got := notifyCount(t, s, "SELECT COUNT(*) FROM notify_outbox WHERE notification_id = 'ntf_over'"); got != 0 {
		t.Fatal("an account past its pending cap queued mail")
	}
	other := NewNotification{ID: "ntf_m", AccountID: "acc_m", UserID: "usr_m", Kind: NotifyMention, NodeID: "nod_m",
		CommentID: "cmt_m", At: t0}
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.InsertNotifications(ctx, []NewNotification{other}, true)
	}); err != nil {
		t.Fatal(err)
	}
	if got := notifyCount(t, s, "SELECT COUNT(*) FROM notify_outbox WHERE notification_id = 'ntf_m' AND state = 'pending'"); got != 1 {
		t.Fatal("another account's notification was not queued for mail while one account was at its cap")
	}
}

// seedOtherAccount adds account acc_m with one user (usr_m), a file (nod_m) and a comment (cmt_m) on it.
func seedOtherAccount(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at) VALUES ('fld_m', 'acc_m', 'Docs', 'docs', 0, 0)`,
		`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
			VALUES ('nod_m', 'acc_m', 'fld_m', NULL, 'Report', 'report', 'file', 0, 0)`,
		`INSERT INTO users (id, account_id, email, email_key, display_name, role, status, created_at, updated_at)
			VALUES ('usr_m', 'acc_m', 'm@example.test', 'm@example.test', 'Mia', 'member', 'active', 0, 0)`,
		`INSERT INTO comments (id, account_id, node_id, author_id, body, created_at)
			VALUES ('cmt_m', 'acc_m', 'nod_m', 'usr_m', 'hello', 1000)`,
	} {
		if _, err := s.w.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
}

// TestClaimIsFairAcrossAccounts: one account with a backlog of due users does not take every claim slot of another
// account (review D3). Two slots go to the first user of each account, not to both of the first account's users.
func TestClaimIsFairAcrossAccounts(t *testing.T) {
	s := openTest(t)
	seedNotify(t, s)
	seedOtherAccount(t, s)
	ctx := context.Background()
	t0 := time.Unix(60_000, 0)
	if err := s.Write(ctx, func(tx *Tx) error {
		if err := tx.InsertNotifications(ctx, []NewNotification{notifyAt("1", t0)}, true); err != nil {
			return err
		}
		if err := tx.InsertNotifications(ctx, []NewNotification{{ID: "ntf_a1", AccountID: "acc_n", UserID: "usr_a",
			Kind: NotifyMention, NodeID: "nod_n", CommentID: "cmt_old", ActorID: "usr_b", At: t0.Add(time.Minute)}}, true); err != nil {
			return err
		}
		return tx.InsertNotifications(ctx, []NewNotification{{ID: "ntf_m", AccountID: "acc_m", UserID: "usr_m",
			Kind: NotifyMention, NodeID: "nod_m", CommentID: "cmt_m", ActorID: "usr_m", At: t0.Add(time.Hour)}}, true)
	}); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.ClaimMail(ctx, t0.Add(2*time.Hour), time.Minute, 2, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 || jobs[0].AccountID == jobs[1].AccountID {
		t.Fatalf("the two slots went to %+v, want one user of each account", jobs)
	}
}

// TestLeasedRowsAreNotClaimedAgain: a user with more due rows than one claim takes (25, with 20 a claim) gets the other 5
// on a second claim, and never a row a claim still holds (the row-level lease).
func TestLeasedRowsAreNotClaimedAgain(t *testing.T) {
	s := openTest(t)
	seedNotify(t, s)
	ctx := context.Background()
	t0 := time.Unix(60_000, 0)
	var rows []NewNotification
	for i := 1; i <= 25; i++ {
		n := fmt.Sprintf("%04d", i)
		rows = append(rows, NewNotification{ID: "ntf_r" + n, AccountID: "acc_n", UserID: "usr_b", Kind: NotifyMention,
			NodeID: "nod_n", CommentID: "cmt_" + n, ActorID: "usr_a", At: t0})
	}
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.InsertNotifications(ctx, rows, true)
	}); err != nil {
		t.Fatal(err)
	}
	at := t0.Add(time.Second)
	first, err := s.ClaimMail(ctx, at, time.Minute, 1, 20)
	if err != nil || len(first) != 20 {
		t.Fatalf("first claim = %d rows, %v; want 20", len(first), err)
	}
	second, err := s.ClaimMail(ctx, at, time.Minute, 1, 20)
	if err != nil || len(second) != 5 {
		t.Fatalf("second claim = %d rows, %v; want the 5 unleased ones", len(second), err)
	}
	held := map[int64]bool{}
	for _, j := range first {
		held[j.OutboxID] = true
	}
	for _, j := range second {
		if held[j.OutboxID] {
			t.Fatalf("second claim took row %d, which the first still holds", j.OutboxID)
		}
	}
}

// TestLeasedUserTakesNoSlot: a user whose rows are all leased does not take a claim slot from a user with rows due (the
// user-level lease check).
func TestLeasedUserTakesNoSlot(t *testing.T) {
	s := openTest(t)
	seedNotify(t, s)
	ctx := context.Background()
	t0 := time.Unix(60_000, 0)
	if err := s.Write(ctx, func(tx *Tx) error {
		if err := tx.InsertNotifications(ctx, []NewNotification{{ID: "ntf_a1", AccountID: "acc_n", UserID: "usr_a",
			Kind: NotifyMention, NodeID: "nod_n", CommentID: "cmt_old", ActorID: "usr_b", At: t0}}, true); err != nil {
			return err
		}
		return tx.InsertNotifications(ctx, []NewNotification{notifyAt("1", t0.Add(time.Hour))}, true)
	}); err != nil {
		t.Fatal(err)
	}
	at := t0.Add(2 * time.Hour)
	first, err := s.ClaimMail(ctx, at, time.Minute, 1, 20)
	if err != nil || len(first) != 1 || first[0].UserID != "usr_a" {
		t.Fatalf("first claim = %+v, %v; want usr_a's row", first, err)
	}
	second, err := s.ClaimMail(ctx, at, time.Minute, 1, 20)
	if err != nil || len(second) != 1 || second[0].UserID != "usr_b" {
		t.Fatalf("second claim = %+v, %v; want usr_b's row, not a slot lost to leased usr_a", second, err)
	}
}
