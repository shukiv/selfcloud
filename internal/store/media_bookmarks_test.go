// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"slices"
	"testing"
	"time"
)

// addBookmark stores one bookmark of userID in nodeID, with the given ID, position, label and creation time.
func addBookmark(t *testing.T, s *Store, userID, nodeID, id string, pos int64, label string, at time.Time) {
	t.Helper()
	if err := s.Write(context.Background(), func(tx *Tx) error {
		return tx.AddMediaBookmark(context.Background(), "acc_n", userID, nodeID,
			MediaBookmark{ID: id, PositionMS: pos, Label: label}, at)
	}); err != nil {
		t.Fatal(err)
	}
}

// bookmarkIDs reads the IDs of one user's bookmarks in one file, in the order the list gives them.
func bookmarkIDs(t *testing.T, s *Store, userID, nodeID string) []string {
	t.Helper()
	rows, err := s.MediaBookmarksOf(context.Background(), userID, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(rows))
	for i, b := range rows {
		ids[i] = b.ID
	}
	return ids
}

// TestMediaBookmarksAddAndOrder: a file with no bookmarks reads as none; the list is ordered by position, then by
// creation time, then by ID; and another user's bookmark, or one of another file, is never in it.
func TestMediaBookmarksAddAndOrder(t *testing.T) {
	s := openTest(t)
	seedNotify(t, s)
	if err := exec(s, `INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
		VALUES ('nod_m', 'acc_n', 'fld_n', NULL, 'Other', 'other', 'file', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if rows, err := s.MediaBookmarksOf(ctx, "usr_b", "nod_n"); err != nil || len(rows) != 0 {
		t.Fatalf("no bookmarks: rows = %v, err = %v; want none", rows, err)
	}
	// Added out of order: position first, then creation time, then ID break the ties.
	addBookmark(t, s, "usr_b", "nod_n", "bmk_b", 5000, "b", time.UnixMilli(3000))
	addBookmark(t, s, "usr_b", "nod_n", "bmk_e", 9000, "e", time.UnixMilli(1000))
	addBookmark(t, s, "usr_b", "nod_n", "bmk_a", 5000, "a", time.UnixMilli(3000))
	addBookmark(t, s, "usr_b", "nod_n", "bmk_d", 1000, "d", time.UnixMilli(9000))
	addBookmark(t, s, "usr_b", "nod_n", "bmk_c", 5000, "c", time.UnixMilli(2000))
	// Another user's bookmark in the same file, and the same user's bookmark in another file: neither is listed.
	addBookmark(t, s, "usr_a", "nod_n", "bmk_other_user", 2000, "x", time.UnixMilli(1))
	addBookmark(t, s, "usr_b", "nod_m", "bmk_other_node", 2000, "y", time.UnixMilli(1))

	want := []string{"bmk_d", "bmk_c", "bmk_a", "bmk_b", "bmk_e"}
	if got := bookmarkIDs(t, s, "usr_b", "nod_n"); !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	rows, err := s.MediaBookmarksOf(ctx, "usr_b", "nod_n")
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].PositionMS != 1000 || rows[0].Label != "d" || !rows[0].CreatedAt.Equal(time.UnixMilli(9000)) {
		t.Fatalf("first bookmark = %+v, want position 1000, label d, created 9000", rows[0])
	}
	// The same list inside a write reads the same rows (the cap reads it).
	if err := s.Write(ctx, func(tx *Tx) error {
		in, err := tx.MediaBookmarksIn(ctx, "usr_b", "nod_n")
		if err != nil {
			return err
		}
		if len(in) != 5 {
			t.Errorf("MediaBookmarksIn read %d rows, want 5", len(in))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestMediaBookmarksRelabel: a relabel changes the label and reads the bookmark back; another user's bookmark, one of
// another file, and one that does not exist are not found, and nothing changes.
func TestMediaBookmarksRelabel(t *testing.T) {
	s := openTest(t)
	seedNotify(t, s)
	if err := exec(s, `INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
		VALUES ('nod_m', 'acc_n', 'fld_n', NULL, 'Other', 'other', 'file', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	at := time.UnixMilli(4000)
	addBookmark(t, s, "usr_b", "nod_n", "bmk_mine", 6000, "before", at)

	relabel := func(user, node, id, label string) (MediaBookmark, bool) {
		t.Helper()
		var got MediaBookmark
		var found bool
		if err := s.Write(ctx, func(tx *Tx) error {
			var err error
			got, found, err = tx.RelabelMediaBookmark(ctx, user, node, id, label)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return got, found
	}
	if _, found := relabel("usr_a", "nod_n", "bmk_mine", "stolen"); found {
		t.Error("another user relabelled the bookmark")
	}
	if _, found := relabel("usr_b", "nod_m", "bmk_mine", "elsewhere"); found {
		t.Error("another file's bookmark was relabelled")
	}
	if _, found := relabel("usr_b", "nod_n", "bmk_missing", "x"); found {
		t.Error("a bookmark that does not exist was relabelled")
	}
	got, found := relabel("usr_b", "nod_n", "bmk_mine", "after")
	if !found {
		t.Fatal("the owner's relabel was not found")
	}
	if got.ID != "bmk_mine" || got.PositionMS != 6000 || got.Label != "after" || !got.CreatedAt.Equal(at) {
		t.Fatalf("relabelled bookmark = %+v, want id bmk_mine, position 6000, label after, created 4000", got)
	}
	if rows, _ := s.MediaBookmarksOf(ctx, "usr_b", "nod_n"); len(rows) != 1 || rows[0].Label != "after" {
		t.Fatalf("list after relabel = %+v, want the one bookmark labelled after", rows)
	}
}

// TestMediaBookmarksDelete: a delete removes the owner's bookmark; another user's, one of another file, and one that
// does not exist are not found and leave the row.
func TestMediaBookmarksDelete(t *testing.T) {
	s := openTest(t)
	seedNotify(t, s)
	if err := exec(s, `INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
		VALUES ('nod_m', 'acc_n', 'fld_n', NULL, 'Other', 'other', 'file', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	addBookmark(t, s, "usr_b", "nod_n", "bmk_mine", 7000, "keep", time.UnixMilli(1))

	del := func(user, node, id string) bool {
		t.Helper()
		var found bool
		if err := s.Write(ctx, func(tx *Tx) error {
			var err error
			found, err = tx.DeleteMediaBookmark(ctx, user, node, id)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return found
	}
	if del("usr_a", "nod_n", "bmk_mine") {
		t.Error("another user deleted the bookmark")
	}
	if del("usr_b", "nod_m", "bmk_mine") {
		t.Error("another file's bookmark was deleted")
	}
	if del("usr_b", "nod_n", "bmk_missing") {
		t.Error("a bookmark that does not exist was deleted")
	}
	if n := len(bookmarkIDs(t, s, "usr_b", "nod_n")); n != 1 {
		t.Fatalf("after the refused deletes, %d bookmarks remain; want 1", n)
	}
	if !del("usr_b", "nod_n", "bmk_mine") {
		t.Fatal("the owner's delete was not found")
	}
	if n := len(bookmarkIDs(t, s, "usr_b", "nod_n")); n != 0 {
		t.Fatalf("after the delete, %d bookmarks remain; want 0", n)
	}
}

// TestMediaBookmarksCascades: a bookmark goes with its user, its file or its account, as the other per-user rows do.
func TestMediaBookmarksCascades(t *testing.T) {
	cases := map[string]string{
		"user":    "DELETE FROM users WHERE id = 'usr_b'",
		"node":    "DELETE FROM nodes WHERE id = 'nod_n'",
		"account": "DELETE FROM accounts WHERE id = 'acc_n'",
	}
	for name, del := range cases {
		t.Run(name, func(t *testing.T) {
			s := openTest(t)
			seedNotify(t, s)
			ctx := context.Background()
			addBookmark(t, s, "usr_b", "nod_n", "bmk_mine", 1, "x", time.UnixMilli(1))
			if _, err := s.w.ExecContext(ctx, del); err != nil {
				t.Fatal(err)
			}
			var n int
			if err := s.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM media_bookmarks").Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Errorf("%s delete left %d media_bookmarks rows; want 0", name, n)
			}
		})
	}
}

// TestMediaBookmarksIndexes: the list reads by user and file, and a file's deletion finds its bookmarks by node_id, so
// both columns are indexed.
func TestMediaBookmarksIndexes(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	rows, err := s.r.QueryContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'media_bookmarks'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"media_bookmarks_user", "media_bookmarks_node"} {
		if !slices.Contains(names, want) {
			t.Errorf("no index %s on media_bookmarks; indexes are %v", want, names)
		}
	}
}
