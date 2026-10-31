// SPDX-License-Identifier: AGPL-3.0-or-later

package sweep

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/blob"
	"github.com/shukiv/selfcloud/internal/store"
)

// TestSweepPrunesNotifications: one sweep removes a notification past its ninety days, expires a mail still pending past
// its week, and removes a finished mail row past its week. Each count is the one the pass reports. A notification inside
// its keep and its pending mail inside a week are left alone.
func TestSweepPrunesNotifications(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "selfcloud.db")
	st, err := store.Open(ctx, store.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	blobs, err := blob.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := store.Now()
	ms := func(d time.Duration) int64 { return now.Add(-d).UnixMilli() }
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_s', 'S', 'active', 0, 0)`)
	exec(`INSERT INTO users (id, account_id, email, email_key, display_name, role, status, created_at, updated_at)
		VALUES ('usr_s', 'acc_s', 's@example.test', 's@example.test', 'Sam', 'member', 'active', 0, 0)`)
	exec(`INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at) VALUES ('fld_s', 'acc_s', 'Docs', 'docs', 0, 0)`)
	exec(`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
		VALUES ('nod_s', 'acc_s', 'fld_s', NULL, 'Report', 'report', 'file', 0, 0)`)
	// Three comments, one notification each: the old one, the pending one and the sent one.
	for _, c := range []string{"old", "pending", "sent"} {
		exec(`INSERT INTO comments (id, account_id, node_id, author_id, body, created_at) VALUES (?, 'acc_s', 'nod_s', 'usr_s', 'x', 0)`,
			"cmt_"+c)
	}
	notify := func(name string, created int64, comment string) {
		exec(`INSERT INTO notifications (id, account_id, user_id, kind, node_id, comment_id, created_at)
			VALUES (?, 'acc_s', 'usr_s', 'mention', 'nod_s', ?, ?)`, "ntf_"+name, "cmt_"+comment, created)
	}
	notify("old", ms(91*24*time.Hour), "old")
	notify("pending", ms(10*24*time.Hour), "pending")
	notify("sent", ms(10*24*time.Hour), "sent")
	// A pending row queued eight days ago: past the week, so it is expired as skipped.
	exec(`INSERT INTO notify_outbox (account_id, user_id, notification_id, state, next_attempt_at, created_at)
		VALUES ('acc_s', 'usr_s', 'ntf_pending', 'pending', ?, ?)`, ms(8*24*time.Hour), ms(8*24*time.Hour))
	// A sent row finished eight days ago: past the week, so it is removed.
	exec(`INSERT INTO notify_outbox (account_id, user_id, notification_id, state, next_attempt_at, created_at, finished_at)
		VALUES ('acc_s', 'usr_s', 'ntf_sent', 'sent', ?, ?, ?)`, ms(8*24*time.Hour), ms(8*24*time.Hour), ms(8*24*time.Hour))

	sw := New(Options{Store: st, Blobs: blobs})
	res, err := sw.Once(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if res.Notifications != 1 || res.MailExpired != 1 || res.MailRows != 1 {
		t.Fatalf("pass removed %d notifications, expired %d mails, removed %d mail rows; want 1, 1, 1",
			res.Notifications, res.MailExpired, res.MailRows)
	}
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := db.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(`SELECT COUNT(*) FROM notifications WHERE id = ?`, "ntf_old"); n != 0 {
		t.Fatal("the notification past ninety days is still there")
	}
	if n := count(`SELECT COUNT(*) FROM notifications WHERE id IN (?, ?)`, "ntf_pending", "ntf_sent"); n != 2 {
		t.Fatalf("notifications inside their keep = %d, want both kept", n)
	}
	if n := count(`SELECT COUNT(*) FROM notify_outbox WHERE notification_id = ? AND state = ? AND last_error = ?`,
		"ntf_pending", store.OutboxSkipped, "expired"); n != 1 {
		t.Fatal("the mail pending past a week is not expired as skipped")
	}
	if n := count(`SELECT COUNT(*) FROM notify_outbox WHERE notification_id = ?`, "ntf_sent"); n != 0 {
		t.Fatal("the finished mail row past a week is still there")
	}
}
