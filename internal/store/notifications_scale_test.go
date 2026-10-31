// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"
)

// TestNotificationsAtScale measures the notification statements on a large table. It is skipped unless SELFCLOUD_SCALE is
// set, since it writes a million notifications and 100,000 pending mail rows. It was run once, by hand, with TMPDIR on
// /var/tmp at nice -n 19 and a limit of ten minutes (design section 14); it is not a load test.
//
// The fixture is 10,000 users of one account, 100 notifications each, on one node. The first 100,000 notifications have a
// pending mail row that is due now, so the claim and the pending cap read a full queue.
func TestNotificationsAtScale(t *testing.T) {
	if os.Getenv("SELFCLOUD_SCALE") == "" {
		t.Skip("set SELFCLOUD_SCALE to run the scale measurement")
	}
	s := openTest(t)
	ctx := context.Background()
	exec := func(what, q string, args ...any) {
		t.Helper()
		start := time.Now()
		if _, err := s.w.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		t.Logf("%s in %v", what, time.Since(start).Round(time.Millisecond))
	}
	exec("account", `INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_s', 'S', 'active', 0, 0)`)
	exec("folder", `INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at) VALUES ('fld_s', 'acc_s', 'Docs', 'docs', 0, 0)`)
	exec("node", `INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
		VALUES ('nod_s', 'acc_s', 'fld_s', NULL, 'Report', 'report', 'file', 0, 0)`)
	exec("10,000 users", `WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM n WHERE i < 9999)
		INSERT INTO users (id, account_id, email, email_key, display_name, role, status, created_at, updated_at)
		SELECT printf('usr_%04d', i), 'acc_s', 'u' || i || '@example.test', 'u' || i || '@example.test', 'U ' || i,
			'member', 'active', 0, 0 FROM n`)
	exec("1,000,000 comments", `WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM n WHERE i < 999999)
		INSERT INTO comments (id, account_id, node_id, parent_id, author_id, body, created_at)
		SELECT printf('cmt_%07d', i), 'acc_s', 'nod_s', NULL, printf('usr_%04d', i % 10000), 'a comment', i FROM n`)
	exec("1,000,000 notifications", `WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM n WHERE i < 999999)
		INSERT INTO notifications (id, account_id, user_id, kind, node_id, comment_id, actor_id, created_at)
		SELECT printf('ntf_%07d', i), 'acc_s', printf('usr_%04d', i % 10000), 'mention', 'nod_s', printf('cmt_%07d', i),
			printf('usr_%04d', (i + 1) % 10000), i FROM n`)
	exec("100,000 pending mail rows, due now", `INSERT INTO notify_outbox (account_id, user_id, notification_id, state, next_attempt_at, created_at)
		SELECT 'acc_s', user_id, id, 'pending', 0, 0 FROM notifications WHERE created_at < 100000`)

	measure := func(label string, n int, fn func() error) {
		t.Helper()
		var ds []time.Duration
		for i := 0; i < n; i++ {
			at := time.Now()
			if err := fn(); err != nil {
				t.Fatalf("%s: %v", label, err)
			}
			ds = append(ds, time.Since(at))
		}
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		t.Logf("%-52s median %9v   worst %9v   (%d runs)", label, ds[len(ds)/2].Round(time.Microsecond),
			ds[len(ds)-1].Round(time.Microsecond), n)
	}
	now := time.Unix(2_000_000_000, 0)
	measure("claim: 50 users, 20 rows each (lease 0, repeated)", 20, func() error {
		_, err := s.ClaimMail(ctx, now, 0, notifyScaleUsers, notifyScaleRows)
		return err
	})
	measure("unread count read: newest 100 of one user", 30, func() error {
		_, err := s.UnreadNewest(ctx, "acc_s", "usr_0007", 100)
		return err
	})
	measure("list: first page of 50, one user (500 kept)", 30, func() error {
		_, err := s.NotificationsPage(ctx, "acc_s", "usr_0007", Page{Limit: 50})
		return err
	})
	measure("pending cap count, capped at 100,000", 20, func() error {
		return s.Write(ctx, func(tx *Tx) error {
			_, err := tx.countOutboxPending(ctx, NotifyPendingLimit)
			return err
		})
	})
	writes := 0
	measure("comment write: 1 comment, 1 notification, 1 mail row", 30, func() error {
		writes++
		cid := fmt.Sprintf("cmt_w%06d", writes)
		return s.Write(ctx, func(tx *Tx) error {
			if _, err := tx.q.ExecContext(ctx, `INSERT INTO comments (id, account_id, node_id, author_id, body, created_at)
				VALUES (?, 'acc_s', 'nod_s', 'usr_0001', 'a comment', 0)`, cid); err != nil {
				return err
			}
			return tx.InsertNotifications(ctx, []NewNotification{{ID: fmt.Sprintf("ntf_w%06d", writes), AccountID: "acc_s",
				UserID: "usr_0009", Kind: NotifyMention, NodeID: "nod_s", CommentID: cid, ActorID: "usr_0001", At: now}}, true)
		})
	})
	charged := 0
	measure("charge: a user not yet charged (budget write)", 50, func() error {
		charged++
		_, _, _, err := s.ChargeMail(ctx, "acc_s", fmt.Sprintf("usr_%04d", 5000+charged), now)
		return err
	})
	measure("prune by age: one batch of 500 (none old)", 20, func() error {
		_, err := s.PruneNotifications(ctx, now.Add(-90*24*time.Hour), notifyPruneScaleBatch)
		return err
	})
	measure("expire pending: one batch of 500 (none old)", 20, func() error {
		_, err := s.ExpireMail(ctx, now.Add(-7*24*time.Hour), now, notifyPruneScaleBatch)
		return err
	})
}

// Parameters of the scale measurement.
const (
	notifyScaleUsers      = 50
	notifyScaleRows       = 20
	notifyPruneScaleBatch = 500
)
