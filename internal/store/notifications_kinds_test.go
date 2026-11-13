// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestShareAndRequestNotificationsListWithoutAComment: a share and an access request name no comment, and the list and the
// bell read them beside the comment notifications. The kind strings are the values the store keeps.
func TestShareAndRequestNotificationsListWithoutAComment(t *testing.T) {
	s := openTest(t)
	seedNotify(t, s)
	ctx := context.Background()
	t0 := time.Unix(100_000, 0)
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.InsertShareNotifications(ctx, []NewShareNotification{
			{ID: "ntf_share", AccountID: "acc_n", UserID: "usr_b", Kind: "share", NodeID: "nod_n", ActorID: "usr_a", At: t0},
			{ID: "ntf_req", AccountID: "acc_n", UserID: "usr_b", Kind: "access_request", NodeID: "nod_n", ActorID: "usr_a",
				At: t0.Add(time.Second)},
		})
	}); err != nil {
		t.Fatal(err)
	}
	page, err := s.NotificationsPage(ctx, "acc_n", "usr_b", Page{Limit: 10})
	if err != nil || len(page) != 2 {
		t.Fatalf("page = %+v, %v; want the share and the request", page, err)
	}
	if page[0].Kind != "access_request" || page[1].Kind != "share" {
		t.Fatalf("page kinds = %q, %q; want the request newest, then the share", page[0].Kind, page[1].Kind)
	}
	unread, err := s.UnreadNewest(ctx, "acc_n", "usr_b", 10)
	if err != nil || len(unread) != 2 {
		t.Fatalf("unread = %+v, %v; want both", unread, err)
	}
	// No mail is queued for them: they are in-app only.
	if n := notifyCount(t, s, "SELECT COUNT(*) FROM notify_outbox"); n != 0 {
		t.Fatalf("outbox rows = %d, want 0", n)
	}
}

// TestShareNotificationsAreNotDeduplicated: two shares to one user are two rows, unlike a mention which is one per
// comment. Nothing collapses them.
func TestShareNotificationsAreNotDeduplicated(t *testing.T) {
	s := openTest(t)
	seedNotify(t, s)
	ctx := context.Background()
	t0 := time.Unix(110_000, 0)
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.InsertShareNotifications(ctx, []NewShareNotification{
			{ID: "ntf_s1", AccountID: "acc_n", UserID: "usr_b", Kind: "share", NodeID: "nod_n", ActorID: "usr_a", At: t0},
			{ID: "ntf_s2", AccountID: "acc_n", UserID: "usr_b", Kind: "share", NodeID: "nod_n", ActorID: "usr_a",
				At: t0.Add(time.Second)},
		})
	}); err != nil {
		t.Fatal(err)
	}
	page, err := s.NotificationsPage(ctx, "acc_n", "usr_b", Page{Limit: 10})
	if err != nil || len(page) != 2 {
		t.Fatalf("page = %+v, %v; want two shares", page, err)
	}
}

// TestNotificationsPageInterleavesBothTables: the page unions the two tables newest first, a tie on the time broken by
// id, and the cursor resumes exactly across the union, including across a tie.
func TestNotificationsPageInterleavesBothTables(t *testing.T) {
	s := openTest(t)
	seedNotify(t, s)
	ctx := context.Background()
	at := func(sec int64) time.Time { return time.Unix(sec, 0) }
	if err := s.Write(ctx, func(tx *Tx) error {
		if err := tx.InsertNotifications(ctx, []NewNotification{
			{ID: "ntf_m1", AccountID: "acc_n", UserID: "usr_b", Kind: "mention", NodeID: "nod_n", CommentID: "cmt_1",
				ActorID: "usr_a", At: at(300)},
			{ID: "ntf_m2", AccountID: "acc_n", UserID: "usr_b", Kind: "mention", NodeID: "nod_n", CommentID: "cmt_2",
				ActorID: "usr_a", At: at(100)},
		}, false); err != nil {
			return err
		}
		return tx.InsertShareNotifications(ctx, []NewShareNotification{
			{ID: "ntf_s1", AccountID: "acc_n", UserID: "usr_b", Kind: "share", NodeID: "nod_n", ActorID: "usr_a", At: at(300)},
			{ID: "ntf_s2", AccountID: "acc_n", UserID: "usr_b", Kind: "share", NodeID: "nod_n", ActorID: "usr_a", At: at(200)},
		})
	}); err != nil {
		t.Fatal(err)
	}
	// Newest first; at time 300 the tie is broken by id descending, so ntf_s1 precedes ntf_m1.
	want := []string{"ntf_s1", "ntf_m1", "ntf_s2", "ntf_m2"}
	var got []string
	cursor := ""
	for i := 0; i < len(want); i++ {
		page, err := s.NotificationsPage(ctx, "acc_n", "usr_b", Page{Limit: 1, Cursor: cursor})
		if err != nil || len(page) != 1 {
			t.Fatalf("page %d = %+v, %v; want one row", i, page, err)
		}
		got = append(got, page[0].ID)
		cursor = NotificationCursor(page[0])
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("paged order = %v, want %v", got, want)
		}
	}
	// One past the end is empty.
	page, err := s.NotificationsPage(ctx, "acc_n", "usr_b", Page{Limit: 1, Cursor: cursor})
	if err != nil || len(page) != 0 {
		t.Fatalf("page past the end = %+v, %v; want none", page, err)
	}
}

// TestUnreadNewestSumsBothTables: the bell's unread rows come from both tables, newest first.
func TestUnreadNewestSumsBothTables(t *testing.T) {
	s := openTest(t)
	seedNotify(t, s)
	ctx := context.Background()
	if err := s.Write(ctx, func(tx *Tx) error {
		if err := tx.InsertNotifications(ctx, []NewNotification{
			{ID: "ntf_um", AccountID: "acc_n", UserID: "usr_b", Kind: "mention", NodeID: "nod_n", CommentID: "cmt_1",
				ActorID: "usr_a", At: time.Unix(100, 0)},
		}, false); err != nil {
			return err
		}
		return tx.InsertShareNotifications(ctx, []NewShareNotification{
			{ID: "ntf_us", AccountID: "acc_n", UserID: "usr_b", Kind: "share", NodeID: "nod_n", ActorID: "usr_a",
				At: time.Unix(200, 0)},
		})
	}); err != nil {
		t.Fatal(err)
	}
	unread, err := s.UnreadNewest(ctx, "acc_n", "usr_b", 10)
	if err != nil || len(unread) != 2 {
		t.Fatalf("unread = %+v, %v; want the mention and the share", unread, err)
	}
	if unread[0].ID != "ntf_us" || unread[1].ID != "ntf_um" {
		t.Fatalf("unread order = %q, %q; want the share newest, then the mention", unread[0].ID, unread[1].ID)
	}
}

// TestMarkShareNotificationRead: a share notice is marked read by its id in the second table, leaves the unread list, and
// another user cannot mark it.
func TestMarkShareNotificationRead(t *testing.T) {
	s := openTest(t)
	seedNotify(t, s)
	ctx := context.Background()
	t0 := time.Unix(120_000, 0)
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.InsertShareNotifications(ctx, []NewShareNotification{
			{ID: "ntf_sh", AccountID: "acc_n", UserID: "usr_b", Kind: "share", NodeID: "nod_n", ActorID: "usr_a", At: t0},
		})
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.MarkNotificationRead(ctx, "acc_n", "usr_b", "ntf_sh", t0.Add(time.Second))
	}); err != nil {
		t.Fatal(err)
	}
	if n := notifyCount(t, s, "SELECT COUNT(*) FROM share_notifications WHERE id = 'ntf_sh' AND read_at IS NOT NULL"); n != 1 {
		t.Fatalf("read share rows = %d, want 1", n)
	}
	if unread, err := s.UnreadNewest(ctx, "acc_n", "usr_b", 10); err != nil || len(unread) != 0 {
		t.Fatalf("unread = %+v, %v; want none after the mark", unread, err)
	}
	err := s.Write(ctx, func(tx *Tx) error {
		return tx.MarkNotificationRead(ctx, "acc_n", "usr_a", "ntf_sh", t0)
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("mark as another user = %v, want ErrNotFound", err)
	}
}

// TestSweepRemovesOldRowsOfBothTables: the retention sweep removes the old rows of both tables and keeps the new ones.
func TestSweepRemovesOldRowsOfBothTables(t *testing.T) {
	s := openTest(t)
	seedNotify(t, s)
	ctx := context.Background()
	old, new := time.Unix(100_000, 0), time.Unix(200_000, 0)
	if err := s.Write(ctx, func(tx *Tx) error {
		if err := tx.InsertNotifications(ctx, []NewNotification{
			{ID: "ntf_oldm", AccountID: "acc_n", UserID: "usr_b", Kind: "mention", NodeID: "nod_n", CommentID: "cmt_1",
				ActorID: "usr_a", At: old},
		}, false); err != nil {
			return err
		}
		return tx.InsertShareNotifications(ctx, []NewShareNotification{
			{ID: "ntf_oldsh", AccountID: "acc_n", UserID: "usr_b", Kind: "share", NodeID: "nod_n", ActorID: "usr_a", At: old},
			{ID: "ntf_newsh", AccountID: "acc_n", UserID: "usr_b", Kind: "share", NodeID: "nod_n", ActorID: "usr_a", At: new},
		})
	}); err != nil {
		t.Fatal(err)
	}
	n, err := s.PruneNotifications(ctx, time.Unix(150_000, 0), 100)
	if err != nil || n != 2 {
		t.Fatalf("pruned = %d, %v; want the old mention and the old share", n, err)
	}
	for _, q := range []struct {
		query string
		want  int
	}{
		{"SELECT COUNT(*) FROM notifications WHERE id = 'ntf_oldm'", 0},
		{"SELECT COUNT(*) FROM share_notifications WHERE id = 'ntf_oldsh'", 0},
		{"SELECT COUNT(*) FROM share_notifications WHERE id = 'ntf_newsh'", 1},
	} {
		if got := notifyCount(t, s, q.query); got != q.want {
			t.Fatalf("%s = %d, want %d", q.query, got, q.want)
		}
	}
}

// TestShareNotificationGoesWithItsNodeUserAccount: the foreign keys remove a share notice when its node, its user or its
// account goes.
func TestShareNotificationGoesWithItsNodeUserAccount(t *testing.T) {
	for _, tc := range []struct {
		name   string
		delete string
	}{
		{"node", "DELETE FROM nodes WHERE id = 'nod_x'"},
		{"user", "DELETE FROM users WHERE id = 'usr_b'"},
		{"account", "DELETE FROM accounts WHERE id = 'acc_n'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTest(t)
			seedNotify(t, s)
			ctx := context.Background()
			if _, err := s.w.ExecContext(ctx, `INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind,
				created_at, modified_at) VALUES ('nod_x', 'acc_n', 'fld_n', 'nod_n', 'X', 'x', 'file', 0, 0)`); err != nil {
				t.Fatal(err)
			}
			if err := s.Write(ctx, func(tx *Tx) error {
				return tx.InsertShareNotifications(ctx, []NewShareNotification{
					{ID: "ntf_casc", AccountID: "acc_n", UserID: "usr_b", Kind: "share", NodeID: "nod_x", ActorID: "usr_a",
						At: time.Unix(130_000, 0)},
				})
			}); err != nil {
				t.Fatal(err)
			}
			if err := s.Write(ctx, func(tx *Tx) error {
				_, err := tx.q.ExecContext(ctx, tc.delete)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if n := notifyCount(t, s, "SELECT COUNT(*) FROM share_notifications WHERE id = 'ntf_casc'"); n != 0 {
				t.Fatalf("share notices after deleting the %s = %d, want 0", tc.name, n)
			}
		})
	}
}
