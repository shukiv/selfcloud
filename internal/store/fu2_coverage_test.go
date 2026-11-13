// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// fu2: the colour palette the code keeps and the list the migration's CHECK holds must be the same, in the same
// order (Colors says the migration repeats it).
func TestColorsMatchTheMigrationCheck(t *testing.T) {
	b, err := os.ReadFile("migrations/0019_shortcuts.sql")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	at := strings.Index(src, "color IN (")
	if at < 0 {
		t.Fatal("migration 0019 has no colour CHECK")
	}
	list := src[at : at+strings.Index(src[at:], ")")]
	var got []string
	for _, m := range regexp.MustCompile(`'([a-z]+)'`).FindAllStringSubmatch(list, -1) {
		got = append(got, m[1])
	}
	if !reflect.DeepEqual(Colors, got) {
		t.Errorf("Colors = %v, but the migration 0019 CHECK holds %v", Colors, got)
	}
}

// fu2: the global pending cap stops queueing when ten or more accounts are each under their own cap. Ten accounts
// each hold NotifyAccountPendingLimit-1 pending rows, and an eleventh holds the ten that reach NotifyPendingLimit. A
// twelfth account has none, so only the global cap can stop its mail. The per-account test fills one account and is
// stopped by its own cap first, so it does not show this.
func TestGlobalPendingCapHoldsWhenEachAccountIsUnderItsOwn(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 100,000 rows")
	}
	s := openTest(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.w.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// seed makes account k with its user, a file to comment on, and rows pending mail rows, each a mention with a comment.
	seed := func(k, rows int) {
		id := fmt.Sprintf("%02d", k)
		acc, usr, nod, fld := "acc_g"+id, "usr_g"+id, "nod_g"+id, "fld_g"+id
		exec(`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES (?, ?, 'active', 0, 0)`, acc, acc)
		exec(`INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at) VALUES (?, ?, 'Docs', 'docs', 0, 0)`,
			fld, acc)
		exec(`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
			VALUES (?, ?, ?, NULL, 'Report', 'report', 'file', 0, 0)`, nod, acc, fld)
		exec(`INSERT INTO users (id, account_id, email, email_key, display_name, role, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, 'G', 'member', 'active', 0, 0)`, usr, acc, acc+"@example.test", acc+"@example.test")
		if rows == 0 {
			return
		}
		cmt, ntf := "cmt_g"+id+"_", "ntf_g"+id+"_"
		exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?)
			INSERT INTO comments (id, account_id, node_id, author_id, body, created_at)
			SELECT ? || printf('%05d', i), ?, ?, ?, 'hello', 0 FROM n`, rows, cmt, acc, nod, usr)
		exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?)
			INSERT INTO notifications (id, account_id, user_id, kind, node_id, comment_id, created_at)
			SELECT ? || printf('%05d', i), ?, ?, 'mention', ?, ? || printf('%05d', i), 0 FROM n`, rows, ntf, acc, usr, nod, cmt)
		exec(`INSERT INTO notify_outbox (account_id, user_id, notification_id, state, next_attempt_at, created_at)
			SELECT account_id, user_id, id, 'pending', 0, 0 FROM notifications WHERE account_id = ?`, acc)
	}
	for k := 0; k < 10; k++ {
		seed(k, NotifyAccountPendingLimit-1)
	}
	seed(10, NotifyPendingLimit-10*(NotifyAccountPendingLimit-1))
	seed(11, 0)
	if got := notifyCount(t, s, "SELECT COUNT(*) FROM notify_outbox WHERE state = 'pending'"); got != NotifyPendingLimit {
		t.Fatalf("pending rows = %d, want the global limit %d", got, NotifyPendingLimit)
	}
	exec(`INSERT INTO comments (id, account_id, node_id, author_id, body, created_at)
		VALUES ('cmt_g11_new', 'acc_g11', 'nod_g11', 'usr_g11', 'hello', 0)`)
	t0 := time.Unix(60_000, 0)
	n := NewNotification{ID: "ntf_g11_new", AccountID: "acc_g11", UserID: "usr_g11", Kind: NotifyMention, NodeID: "nod_g11",
		CommentID: "cmt_g11_new", At: t0}
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.InsertNotifications(ctx, []NewNotification{n}, true)
	}); err != nil {
		t.Fatal(err)
	}
	if got := notifyCount(t, s, "SELECT COUNT(*) FROM notifications WHERE id = 'ntf_g11_new'"); got != 1 {
		t.Fatal("the notification was not written")
	}
	if got := notifyCount(t, s, "SELECT COUNT(*) FROM notify_outbox WHERE notification_id = 'ntf_g11_new'"); got != 0 {
		t.Fatal("mail was queued past the global pending cap, although this account was under its own")
	}
}
