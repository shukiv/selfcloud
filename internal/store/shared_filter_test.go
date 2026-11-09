// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// sharedFixture is filteredFixture with one member (usr_a) who holds a viewer permission on four nodes: a.pdf, b.docx,
// c.jpg and the folder. None of them is a folder root, so the root-reach rule does not hide them.
func sharedFixture(t *testing.T) *Store {
	t.Helper()
	s := filteredFixture(t)
	for _, q := range []string{
		`INSERT INTO users (id, account_id, email, email_key, role, status, created_at, updated_at)
			VALUES ('usr_a', 'acc_a', 'a@test.invalid', 'a@test.invalid', 'member', 'active', 0, 0)`,
		`INSERT INTO permissions (node_id, account_id, subject_kind, subject_id, role, created_at) VALUES
			('nod_pdf', 'acc_a', 'user', 'usr_a', 'viewer', 1000),
			('nod_docx', 'acc_a', 'user', 'usr_a', 'viewer', 1000),
			('nod_jpg', 'acc_a', 'user', 'usr_a', 'viewer', 1000),
			('nod_dir', 'acc_a', 'user', 'usr_a', 'viewer', 1000)`,
	} {
		if err := exec(s, q); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// TestSharedFilteredByType: the shared list keeps the shared nodes of the type; the zero filter keeps all four.
func TestSharedFilteredByType(t *testing.T) {
	s := sharedFixture(t)
	ctx := context.Background()
	shared, _, err := s.SharedNodesFiltered(ctx, "acc_a", "usr_a", false, false, ListFilter{Group: "pdf"}, Page{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, sn := range shared {
		ids = append(ids, sn.NodeID)
	}
	if !reflect.DeepEqual(ids, []string{"nod_pdf"}) {
		t.Errorf("pdf: %v", ids)
	}
	all, _, err := s.SharedNodesFiltered(ctx, "acc_a", "usr_a", false, false, ListFilter{}, Page{Limit: 10})
	if err != nil || len(all) != 4 {
		t.Errorf("zero filter: %d nodes, %v", len(all), err)
	}
}

// TestSharedFilteredCursorTag: a page's cursor opens only under the filter it was issued with.
func TestSharedFilteredCursorTag(t *testing.T) {
	s := sharedFixture(t)
	ctx := context.Background()
	image := ListFilter{Group: "image"}
	first, next, err := s.SharedNodesFiltered(ctx, "acc_a", "usr_a", false, false, image, Page{Limit: 1})
	if err != nil || len(first) != 1 {
		t.Fatalf("image first page: %d, %v", len(first), err)
	}
	_ = next
	// A cursor issued under the zero filter, passed with the image filter, is refused.
	_, plain, err := s.SharedNodesFiltered(ctx, "acc_a", "usr_a", false, false, ListFilter{}, Page{Limit: 1})
	if err != nil || plain == "" {
		t.Fatalf("plain first page: %v", err)
	}
	if _, _, err := s.SharedNodesFiltered(ctx, "acc_a", "usr_a", false, false, image, Page{Limit: 1, Cursor: plain}); !errors.Is(err, ErrBadCursor) {
		t.Errorf("plain cursor with image: %v, want ErrBadCursor", err)
	}
}
