// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
	"time"
)

// TestShareAndRequestNotificationsListWithoutAComment: a share and an access request name no comment, so their
// comment_id is null, and the list and the bell read them beside the comment notifications. The kind strings are the
// values the store keeps.
func TestShareAndRequestNotificationsListWithoutAComment(t *testing.T) {
	s := openTest(t)
	seedNotify(t, s)
	ctx := context.Background()
	t0 := time.Unix(100_000, 0)
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.InsertNotifications(ctx, []NewNotification{
			{ID: "ntf_share", AccountID: "acc_n", UserID: "usr_b", Kind: "share", NodeID: "nod_n", ActorID: "usr_a", At: t0},
			{ID: "ntf_req", AccountID: "acc_n", UserID: "usr_b", Kind: "access_request", NodeID: "nod_n", ActorID: "usr_a",
				At: t0.Add(time.Second)},
		}, false)
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

// TestShareNotificationsAreNotDeduplicated: a share names no comment, so two shares to one user are two rows, unlike a
// mention which is one per comment. The null comment_id must not collapse them.
func TestShareNotificationsAreNotDeduplicated(t *testing.T) {
	s := openTest(t)
	seedNotify(t, s)
	ctx := context.Background()
	t0 := time.Unix(110_000, 0)
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.InsertNotifications(ctx, []NewNotification{
			{ID: "ntf_s1", AccountID: "acc_n", UserID: "usr_b", Kind: "share", NodeID: "nod_n", ActorID: "usr_a", At: t0},
			{ID: "ntf_s2", AccountID: "acc_n", UserID: "usr_b", Kind: "share", NodeID: "nod_n", ActorID: "usr_a",
				At: t0.Add(time.Second)},
		}, false)
	}); err != nil {
		t.Fatal(err)
	}
	page, err := s.NotificationsPage(ctx, "acc_n", "usr_b", Page{Limit: 10})
	if err != nil || len(page) != 2 {
		t.Fatalf("page = %+v, %v; want two shares", page, err)
	}
}