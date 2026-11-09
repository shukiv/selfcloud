// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// viewsFixture is filteredFixture with one user (usr_a) who has viewed and starred four nodes: a.pdf at 300, the folder
// at 400, b.docx at 200 and c.jpg at 100 (last viewed, and starred at the same times).
func viewsFixture(t *testing.T) *Store {
	t.Helper()
	s := filteredFixture(t)
	for _, q := range []string{
		`INSERT INTO users (id, account_id, email, email_key, role, status, created_at, updated_at)
			VALUES ('usr_a', 'acc_a', 'a@test.invalid', 'a@test.invalid', 'member', 'active', 0, 0)`,
		`INSERT INTO recent (user_id, node_id, account_id, viewed_at, edited_at, last_at) VALUES
			('usr_a', 'nod_pdf', 'acc_a', NULL, NULL, 300),
			('usr_a', 'nod_dir', 'acc_a', NULL, NULL, 400),
			('usr_a', 'nod_docx', 'acc_a', NULL, NULL, 200),
			('usr_a', 'nod_jpg', 'acc_a', NULL, NULL, 100)`,
		`INSERT INTO stars (user_id, node_id, account_id, created_at) VALUES
			('usr_a', 'nod_pdf', 'acc_a', 300),
			('usr_a', 'nod_dir', 'acc_a', 400),
			('usr_a', 'nod_docx', 'acc_a', 200),
			('usr_a', 'nod_jpg', 'acc_a', 100)`,
	} {
		if err := exec(s, q); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// recentNodes reads the names of the recent rows of usr_a, through the nodes they name.
func recentNodes(t *testing.T, s *Store, f ListFilter, p Page) ([]string, []RecentRow) {
	t.Helper()
	rows, err := s.RecentPageFiltered(context.Background(), "usr_a", f, p)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range rows {
		n, err := s.Node(context.Background(), "acc_a", r.NodeID)
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, n.Name)
	}
	return names, rows
}

// TestRecentFilteredByType: the recent list keeps the entries whose node has the type, newest first; the zero filter
// is every entry.
func TestRecentFilteredByType(t *testing.T) {
	s := viewsFixture(t)
	got, _ := recentNodes(t, s, ListFilter{}, Page{Limit: 10})
	if !reflect.DeepEqual(got, []string{"Folder", "a.pdf", "b.docx", "c.jpg"}) {
		t.Errorf("zero filter: %v", got)
	}
	got, _ = recentNodes(t, s, ListFilter{Group: "pdf"}, Page{Limit: 10})
	if !reflect.DeepEqual(got, []string{"a.pdf"}) {
		t.Errorf("pdf: %v", got)
	}
	got, _ = recentNodes(t, s, ListFilter{Kind: KindDir}, Page{Limit: 10})
	if !reflect.DeepEqual(got, []string{"Folder"}) {
		t.Errorf("folder: %v", got)
	}
}

// TestRecentFilteredCursorTag: a page's cursor carries the filter, so only the same filter continues it.
func TestRecentFilteredCursorTag(t *testing.T) {
	s := viewsFixture(t)
	_, rows := recentNodes(t, s, ListFilter{}, Page{Limit: 1})
	tagged := RecentCursorOf(rows[0], ListFilter{Group: "image"})
	if _, err := s.RecentPageFiltered(context.Background(), "usr_a", ListFilter{Group: "image"}, Page{Limit: 1, Cursor: tagged}); err != nil {
		t.Errorf("the image cursor with image: %v", err)
	}
	if _, err := s.RecentPageFiltered(context.Background(), "usr_a", ListFilter{Group: "pdf"}, Page{Limit: 1, Cursor: tagged}); !errors.Is(err, ErrBadCursor) {
		t.Errorf("the image cursor with pdf: %v, want ErrBadCursor", err)
	}
	if _, err := s.RecentPageFiltered(context.Background(), "usr_a", ListFilter{}, Page{Limit: 1, Cursor: tagged}); !errors.Is(err, ErrBadCursor) {
		t.Errorf("the image cursor with no filter: %v, want ErrBadCursor", err)
	}
	plain := RecentCursor(rows[0])
	if _, err := s.RecentPageFiltered(context.Background(), "usr_a", ListFilter{Group: "pdf"}, Page{Limit: 1, Cursor: plain}); !errors.Is(err, ErrBadCursor) {
		t.Errorf("a plain cursor with pdf: %v, want ErrBadCursor", err)
	}
}

// TestStarredFilteredByType: the starred list keeps the stars whose node has the type, newest first.
func TestStarredFilteredByType(t *testing.T) {
	s := viewsFixture(t)
	stars, err := s.StarsPageFiltered(context.Background(), "usr_a", ListFilter{Group: "wordprocessing"}, Page{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(stars) != 1 || stars[0].NodeID != "nod_docx" {
		t.Errorf("starred wordprocessing: %+v", stars)
	}
	stars, err = s.StarsPageFiltered(context.Background(), "usr_a", ListFilter{}, Page{Limit: 10})
	if err != nil || len(stars) != 4 || stars[0].NodeID != "nod_dir" {
		t.Errorf("starred, no filter: %v %+v", err, stars)
	}
	first := stars[0]
	tagged := StarCursorOf(first, ListFilter{Group: "pdf"})
	if _, err := s.StarsPageFiltered(context.Background(), "usr_a", ListFilter{Group: "pdf"}, Page{Limit: 1, Cursor: tagged}); err != nil {
		t.Errorf("the pdf star cursor with pdf: %v", err)
	}
	if _, err := s.StarsPageFiltered(context.Background(), "usr_a", ListFilter{Group: "image"}, Page{Limit: 1, Cursor: tagged}); !errors.Is(err, ErrBadCursor) {
		t.Errorf("the pdf star cursor with image: %v, want ErrBadCursor", err)
	}
}
