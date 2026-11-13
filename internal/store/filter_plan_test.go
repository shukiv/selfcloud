// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"strings"
	"testing"
)

// explainPlan returns the plan detail lines of a statement with its arguments.
func explainPlan(t *testing.T, s *Store, query string, args ...any) string {
	t.Helper()
	rows, err := s.r.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("plan: %v", err)
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
	}
	return strings.Join(plan, " | ")
}

// TestFilteredLargestAndTrashPlansUseTheListIndexes: with a file group, the Largest page searches the size-and-group
// index and the trash searches the trash-and-group index (design ff1 Appendix B: B9, B11). The statements are the ones
// the builders return, so the test runs what the lists run.
func TestFilteredLargestAndTrashPlansUseTheListIndexes(t *testing.T) {
	s, _ := searchFixture(t)
	if err := exec(s, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 5000)
		INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, size, mime_type, created_at, modified_at, type_group, trashed_at)
		SELECT printf('nod_p%05d', i), 'acc_a', 'fld_a', 'nod_root', printf('file %05d.pdf', i), printf('k%05d', i), 'file',
			i % 4096, 'application/pdf', 0, i, CASE WHEN i % 10 = 0 THEN 'pdf' ELSE 'image' END,
			CASE WHEN i % 7 = 0 THEN i ELSE NULL END FROM n`); err != nil {
		t.Fatal(err)
	}
	f := ListFilter{Group: "pdf"}
	// The folder-scoped page is the measured shape (Appendix B, B9).
	query, args := largestPageSQL("acc_a", "fld_a", "", "", f, nil, 26)
	if p := explainPlan(t, s, query, args...); !strings.Contains(p, "nodes_size_group") {
		t.Errorf("largest in a folder with group pdf: plan %q does not use nodes_size_group", p)
	}
	// The account-wide page (Manage > Largest, no folder) is the same query without the folder. Without its own index the
	// planner scanned nodes_size and filtered the group on each row; it now searches the account index.
	query, args = largestPageSQL("acc_a", "", "", "", f, nil, 26)
	if p := explainPlan(t, s, query, args...); !strings.Contains(p, "nodes_size_account_group") {
		t.Errorf("largest of the account with group pdf: plan %q does not use nodes_size_account_group", p)
	}
	tq, targs := trashRootsSQL("acc_a", "", f, nil, 26)
	if p := explainPlan(t, s, tq, targs...); !strings.Contains(p, "nodes_trash_group") {
		t.Errorf("trash with group pdf: plan %q does not use nodes_trash_group", p)
	}
}

// TestFilteredChildrenPlansUseTheListIndexes: with a file group, each sorted walk of a folder's children is an index
// search on the group's index (design ff1 Appendix B: B1, B6, B8). The statement is the one ChildrenFiltered runs.
func TestFilteredChildrenPlansUseTheListIndexes(t *testing.T) {
	ctx := context.Background()
	s, _ := searchFixture(t)
	if err := exec(s, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 5000)
		INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, size, mime_type, created_at, modified_at, type_group)
		SELECT printf('nod_p%05d', i), 'acc_a', 'fld_a', 'nod_root', printf('file %05d.pdf', i), printf('k%05d', i), 'file',
			i % 4096, 'application/pdf', 0, i, CASE WHEN i % 10 = 0 THEN 'pdf' ELSE 'image' END FROM n`); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		order ChildSort
		index string
	}{
		{"name", ChildSort{Key: SortName}, "nodes_list_name_group"},
		{"modified", ChildSort{Key: SortModified}, "nodes_list_modified_group"},
		{"size", ChildSort{Key: SortSize}, "nodes_list_size_group"},
	}
	for _, c := range cases {
		f := ListFilter{Group: "pdf"}
		where, fargs := f.Where("n")
		args := append([]any{"acc_a", "nod_root", KindFile}, fargs...)
		args = append(args, 26)
		rows, err := s.r.QueryContext(ctx, "EXPLAIN QUERY PLAN "+childSegmentSQLWhere(c.order, false, where), args...)
		if err != nil {
			t.Fatalf("%s: plan: %v", c.name, err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		if p := strings.Join(plan, " | "); !strings.Contains(p, c.index) {
			t.Errorf("%s with group pdf: plan %q does not use %s", c.name, p, c.index)
		}
	}
}
