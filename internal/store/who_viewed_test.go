// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
)

// viewersFixture is a store with one account, a folder with one file in it, and four users of the account.
type viewersFixture struct {
	s     *Store
	ctx   context.Context
	acc   string
	node  string
	users []string
	now   time.Time
}

func newViewersFixture(t *testing.T) *viewersFixture {
	t.Helper()
	s := openTest(t)
	fx := &viewersFixture{s: s, ctx: context.Background(), now: time.UnixMilli(1_700_000_000_000)}
	fx.acc = newAccountRow(t, s, "Viewers")
	f := newFolderRow(t, s, fx.acc, "Docs", "docs")
	// InsertFolder makes the folder's root; the file is a child of it.
	var root string
	if err := s.r.QueryRowContext(fx.ctx, "SELECT id FROM nodes WHERE folder_id = ? AND parent_id IS NULL", f.ID).Scan(&root); err != nil {
		t.Fatal(err)
	}
	fx.node = id.New("nod")
	if err := s.Write(fx.ctx, func(tx *Tx) error {
		n := node(fx.node, f.ID, root, "file.txt", KindFile)
		n.AccountID = fx.acc
		return tx.InsertNode(fx.ctx, n)
	}); err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"a@example.com", "b@example.com", "c@example.com", "d@example.com"} {
		fx.users = append(fx.users, newUserRow(t, s, fx.acc, email, "").ID)
	}
	return fx
}

// view writes one view of the file by user at the given time, as the view recorder does.
func (fx *viewersFixture) view(t *testing.T, user string, at time.Time) {
	t.Helper()
	if err := fx.s.WriteViews(fx.ctx, []ViewRecord{{AccountID: fx.acc, UserID: user, NodeID: fx.node, At: at}}); err != nil {
		t.Fatal(err)
	}
}

// edit records an edit of the file by user at the given time, as an edit does. It keeps the recent row without a view.
func (fx *viewersFixture) edit(t *testing.T, user string, at time.Time) {
	t.Helper()
	if err := fx.s.Write(fx.ctx, func(tx *Tx) error {
		return tx.RecordEdit(fx.ctx, fx.acc, user, fx.node, at)
	}); err != nil {
		t.Fatal(err)
	}
}

// list reads one page of the file's viewers from since, as the route reads them.
func (fx *viewersFixture) list(t *testing.T, since time.Time, p Page) []ViewerRow {
	t.Helper()
	rows, err := fx.s.ViewersPage(fx.ctx, fx.acc, fx.node, since, p)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// TestWhoViewedSwitchDefaultsOff: a new account has the switch off, it can be turned on and off, and an unknown account
// is not found.
func TestWhoViewedSwitchDefaultsOff(t *testing.T) {
	fx := newViewersFixture(t)
	on, err := fx.s.WhoViewed(fx.ctx, fx.acc)
	if err != nil || on {
		t.Fatalf("a new account: switch %v, error %v; want off and no error", on, err)
	}
	for _, want := range []bool{true, false} {
		if err := fx.s.Write(fx.ctx, func(tx *Tx) error { return tx.SetWhoViewed(fx.ctx, fx.acc, want, fx.now) }); err != nil {
			t.Fatal(err)
		}
		if got, _ := fx.s.WhoViewed(fx.ctx, fx.acc); got != want {
			t.Fatalf("after setting %v the switch reads %v", want, got)
		}
	}
	if _, err := fx.s.WhoViewed(fx.ctx, "acc_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown account: error %v, want ErrNotFound", err)
	}
}

// TestViewersPlanUsesRecentNode: the list is answered from the recent_node index, not by a scan of the table, with and
// without a cursor. Without the index the plan scans recent, and this test fails.
func TestViewersPlanUsesRecentNode(t *testing.T) {
	fx := newViewersFixture(t)
	for _, cursor := range []bool{false, true} {
		args := []any{fx.node, fx.acc, int64(0)}
		if cursor {
			args = append(args, int64(0), int64(0), "usr_x")
		}
		args = append(args, 51)
		rows, err := fx.s.r.QueryContext(fx.ctx, "EXPLAIN QUERY PLAN "+viewersQuery(cursor), args...)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var sid, parent, unused int
			var detail string
			if err := rows.Scan(&sid, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		joined := strings.Join(plan, " | ")
		if !strings.Contains(joined, "recent_node") {
			t.Errorf("cursor=%v: the plan does not use recent_node: %s", cursor, joined)
		}
		if strings.Contains(joined, "SCAN recent") {
			t.Errorf("cursor=%v: the plan scans recent: %s", cursor, joined)
		}
	}
}

// TestViewersPageIsWindowedOrderedAndPaged: a view older than the window is left out, and so is a row kept by an edit
// whose view is older than the window. The rest are newest first, a tie broken by the user ID, and following the cursors
// of one-row pages lists every row once.
func TestViewersPageIsWindowedOrderedAndPaged(t *testing.T) {
	fx := newViewersFixture(t)
	since := fx.now.Add(-ViewerWindow)
	a, b, c, d := fx.users[0], fx.users[1], fx.users[2], fx.users[3]
	fx.view(t, a, fx.now.Add(-40*24*time.Hour)) // past the window, and then viewed again inside it
	fx.view(t, a, fx.now.Add(-2*time.Hour))
	fx.view(t, b, fx.now.Add(-1*time.Hour))
	fx.view(t, c, fx.now.Add(-1*time.Hour)) // the same time as b
	fx.view(t, d, fx.now.Add(-40*24*time.Hour))
	fx.edit(t, d, fx.now) // the row is kept by the edit, but its last view is past the window

	ties := []string{b, c}
	slices.Sort(ties)
	slices.Reverse(ties) // the larger user ID first
	want := []string{ties[0], ties[1], a}

	var got []string
	var cursor string
	for i := 0; i < 10; i++ {
		rows := fx.list(t, since, Page{Limit: 1, Cursor: cursor})
		if len(rows) == 0 {
			break
		}
		got = append(got, rows[0].UserID)
		cursor = ViewerCursor(rows[0])
	}
	if !slices.Equal(got, want) {
		t.Fatalf("the list is %v, want %v: newest view first, a tie by user ID, nothing past the window", got, want)
	}
	if rows := fx.list(t, fx.now.Add(time.Hour), Page{Limit: 10}); len(rows) != 0 {
		t.Fatalf("a window that starts after every view lists %d rows", len(rows))
	}
}

// TestViewersAreOfTheAccount: the list of a node of another account is empty, and a cursor that does not decode is
// ErrBadCursor.
func TestViewersAreOfTheAccount(t *testing.T) {
	fx := newViewersFixture(t)
	fx.view(t, fx.users[0], fx.now)
	other := newAccountRow(t, fx.s, "Other")
	rows, err := fx.s.ViewersPage(fx.ctx, other, fx.node, fx.now.Add(-ViewerWindow), Page{Limit: 10})
	if err != nil || len(rows) != 0 {
		t.Fatalf("another account lists %d rows, error %v; want none", len(rows), err)
	}
	if _, err := fx.s.ViewersPage(fx.ctx, fx.acc, fx.node, fx.now.Add(-ViewerWindow), Page{Limit: 10, Cursor: "%%%"}); !errors.Is(err, ErrBadCursor) {
		t.Fatalf("a cursor that does not decode: error %v, want ErrBadCursor", err)
	}
}
