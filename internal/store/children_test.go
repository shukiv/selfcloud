// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/names"
)

// seedChildren makes account acc_s, a folder fld_s with its root nod_root, and the children given: each is
// id, name, kind, size and modified time in milliseconds. Every child has its ID as its name key, so sibling keys never clash.
type childSeed struct {
	id, name, kind string
	size, modified int64
}

func seedChildren(t *testing.T, s *Store, kids []childSeed) {
	t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_s', 'S', 'active', 0, 0)`,
		`INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at) VALUES ('fld_s', 'acc_s', 'Docs', 'docs', 0, 0)`,
		`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
			VALUES ('nod_root', 'acc_s', 'fld_s', NULL, '', '', 'dir', 0, 0)`,
	} {
		if _, err := s.w.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range kids {
		_, err := s.w.ExecContext(ctx, `INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, size, created_at, modified_at)
			VALUES (?, 'acc_s', 'fld_s', 'nod_root', ?, ?, ?, ?, 0, ?)`, k.id, k.name, k.id, k.kind, k.size, k.modified)
		if err != nil {
			t.Fatal(err)
		}
	}
}

// childPlanCases are the statements a sorted walk runs: each sort, each direction, the first page and a page after a cursor.
func childPlanCases() []struct {
	name string
	sql  string
	args []any
} {
	type c = struct {
		name string
		sql  string
		args []any
	}
	var out []c
	for _, key := range []SortKey{SortName, SortSize, SortModified} {
		for _, desc := range []bool{false, true} {
			order := ChildSort{Key: key, Desc: desc}
			tag := fmt.Sprintf("key %d desc %v", key, desc)
			from := ChildPos{Kind: KindFile, Text: "m", Num: 5, ID: "nod_x"}
			out = append(out,
				c{tag + ", first page", childSegmentSQL(order, false), []any{"acc_s", "nod_root", KindFile, 50}},
				c{tag + ", after a cursor", childSegmentSQL(order, true),
					append([]any{"acc_s", "nod_root", KindFile}, append(childSegmentArgs(order, from), 50)...)},
			)
		}
	}
	return out
}

// TestChildrenSortedSQLUsesIndexes reads the plan of every statement a sorted walk runs. A scan of nodes, or a sort
// in a temporary b-tree, makes a page cost the size of the folder. The indexes are the ones migration 0035 builds.
func TestChildrenSortedSQLUsesIndexes(t *testing.T) {
	s := openTest(t)
	for _, c := range childPlanCases() {
		t.Run(c.name, func(t *testing.T) {
			rows, err := s.r.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+c.sql, c.args...)
			if err != nil {
				t.Fatal(err)
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
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(plan, " | ")
			if len(plan) == 0 {
				t.Fatal("no query plan")
			}
			if !strings.Contains(joined, "nodes_list_") {
				t.Errorf("no sort index in plan: %s", joined)
			}
			if strings.Contains(joined, "SCAN") {
				t.Errorf("plan scans: %s", joined)
			}
			if strings.Contains(joined, "TEMP B-TREE") {
				t.Errorf("plan sorts in a temporary b-tree: %s", joined)
			}
			t.Logf("plan: %s", joined)
		})
	}
}

// childModel is the order a sorted listing must give, computed without SQL: folders, shortcuts, then files, each
// kind sorted by the key (names.Compare for a name) and then by ID, in the direction of the sort.
func childModel(kids []childSeed, order ChildSort) []string {
	out := slices.Clone(kids)
	rank := map[string]int{KindDir: 0, KindShortcut: 1, KindFile: 2}
	slices.SortFunc(out, func(a, b childSeed) int {
		if r := rank[a.kind] - rank[b.kind]; r != 0 {
			return r
		}
		var c int
		switch order.Key {
		case SortName:
			c = names.Compare(a.name, b.name)
		case SortSize:
			c = cmp64(a.size, b.size)
		case SortModified:
			c = cmp64(a.modified, b.modified)
		}
		// The ID breaks a tie, in the same direction as the sort.
		if c == 0 {
			c = strings.Compare(a.id, b.id)
		}
		if order.Desc {
			c = -c
		}
		return c
	})
	ids := make([]string, len(out))
	for i, k := range out {
		ids[i] = k.id
	}
	return ids
}

func cmp64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// walkChildren reads the whole listing a page at a time and returns the IDs in the order read.
func walkChildren(t *testing.T, s *Store, order ChildSort, limit int) []string {
	t.Helper()
	var ids []string
	var after *ChildPos
	for pages := 0; ; pages++ {
		// A walk that never ends is a broken cursor; the cap turns that into a failure instead of a hang.
		if pages > 1000 {
			t.Fatalf("walk did not end after %d pages", pages)
		}
		page, more, err := s.ChildrenSorted(t.Context(), "acc_s", "nod_root", order, limit, after)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range page {
			ids = append(ids, n.ID)
		}
		if !more {
			return ids
		}
		last := page[len(page)-1]
		pos := order.PosOf(last)
		after = &pos
	}
}

// mixedChildren is a folder with ties in size and time, Hebrew and Arabic names, numbered names, and the three kinds.
func mixedChildren() []childSeed {
	var out []childSeed
	names := []string{"file 2", "file 10", "File 1", "שלום", "תיק", "ארכיון", "مستند", "صورة", "Résumé", "resume", "a-b", "a b", "B"}
	for i := range 120 {
		kind := KindFile
		switch i % 7 {
		case 0:
			kind = KindDir
		case 1:
			kind = KindShortcut
		}
		size := int64(i % 9) // many ties
		if kind != KindFile {
			size = 0 // a directory and a shortcut hold no bytes (CHECK in migration 0019)
		}
		out = append(out, childSeed{
			id:       fmt.Sprintf("nod_%03d", i),
			name:     fmt.Sprintf("%s %03d", names[i%len(names)], i),
			kind:     kind,
			size:     size,
			modified: int64(1000 + (i%5)*10), // ties in time too
		})
	}
	return out
}

// TestChildrenSortedWalkMatchesModel walks a folder with ties in every column and checks each page sequence against the
// model, for every sort and direction, at two page sizes. Nothing repeats and nothing is skipped.
func TestChildrenSortedWalkMatchesModel(t *testing.T) {
	kids := mixedChildren()
	for _, key := range []SortKey{SortName, SortSize, SortModified} {
		for _, desc := range []bool{false, true} {
			for _, limit := range []int{1, 7, 500} {
				order := ChildSort{Key: key, Desc: desc}
				t.Run(fmt.Sprintf("key%d_desc%v_limit%d", key, desc, limit), func(t *testing.T) {
					s := openTest(t)
					seedChildren(t, s, kids)
					got := walkChildren(t, s, order, limit)
					want := childModel(kids, order)
					if !slices.Equal(got, want) {
						t.Fatalf("walk differs from model\n got: %v\nwant: %v", got, want)
					}
				})
			}
		}
	}
}

// TestChildrenSortedWalkDuringChanges changes the folder between pages: rows are added, trashed, and moved in the
// order by a size change. A row that was not touched is returned exactly once, and no row is returned twice.
func TestChildrenSortedWalkDuringChanges(t *testing.T) {
	kids := mixedChildren()
	s := openTest(t)
	seedChildren(t, s, kids)
	order := ChildSort{Key: SortSize, Desc: true}

	seen := map[string]int{}
	var after *ChildPos
	page := 0
	touched := map[string]bool{}
	for pages := 0; ; pages++ {
		if pages > 1000 {
			t.Fatal("walk did not end")
		}
		rows, more, err := s.ChildrenSorted(t.Context(), "acc_s", "nod_root", order, 9, after)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range rows {
			seen[n.ID]++
		}
		if !more {
			break
		}
		last := rows[len(rows)-1]
		pos := order.PosOf(last)
		after = &pos
		page++
		switch page {
		case 1:
			// A new file that sorts before the cursor, one after it, a trashed file after it, and a size change that
			// moves a file from after the cursor to before it.
			for _, q := range []string{
				`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, size, created_at, modified_at)
					VALUES ('nod_new_top', 'acc_s', 'fld_s', 'nod_root', 'new top', 'new top', 'file', 999999, 0, 1)`,
				`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, size, created_at, modified_at)
					VALUES ('nod_new_low', 'acc_s', 'fld_s', 'nod_root', 'new low', 'new low', 'file', 0, 0, 1)`,
				`UPDATE nodes SET trashed_at = 1 WHERE id = 'nod_005'`,
				`UPDATE nodes SET size = 100000 WHERE id = 'nod_119'`,
			} {
				if _, err := s.w.ExecContext(t.Context(), q); err != nil {
					t.Fatal(err)
				}
			}
			touched["nod_new_top"], touched["nod_new_low"], touched["nod_005"], touched["nod_119"] = true, true, true, true
		}
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("%s returned %d times", id, n)
		}
	}
	for _, k := range kids {
		if touched[k.id] {
			continue
		}
		if seen[k.id] != 1 {
			t.Errorf("untouched %s returned %d times, want 1", k.id, seen[k.id])
		}
	}
}

// TestChildrenSortedNamesNumbersAndTies pins the name order the web interface gave: digits by value, case and
// accents ignored, a Hebrew name and an Arabic name in their alphabetical place, and folders before files.
func TestChildrenSortedNamesNumbersAndTies(t *testing.T) {
	kids := []childSeed{
		{id: "a", name: "file 10", kind: KindFile},
		{id: "b", name: "file 2", kind: KindFile},
		{id: "c", name: "File 1", kind: KindFile},
		{id: "d", name: "שלום", kind: KindFile},
		{id: "e", name: "ארכיון", kind: KindFile},
		{id: "f", name: "مستند", kind: KindFile},
		{id: "g", name: "Zeta", kind: KindDir},
		{id: "h", name: "Résumé", kind: KindFile},
		{id: "i", name: "resume", kind: KindFile},
	}
	s := openTest(t)
	seedChildren(t, s, kids)
	got := walkChildren(t, s, ChildSort{Key: SortName}, 3)
	// Folders first. Then the root collation's script order: Latin, then Hebrew, then Arabic. Within a script the letters
	// follow the alphabet; "Résumé" and "resume" tie on accents and case, so the ID breaks the tie.
	want := []string{"g", "c", "b", "a", "h", "i", "e", "d", "f"}
	if !slices.Equal(got, want) {
		t.Fatalf("name order\n got: %v\nwant: %v", got, want)
	}
}

// TestChildrenSortedRejectsBadPosition: a position whose kind is not one of the segments does not resume anything.
func TestChildrenSortedRejectsBadPosition(t *testing.T) {
	s := openTest(t)
	seedChildren(t, s, mixedChildren())
	_, _, err := s.ChildrenSorted(t.Context(), "acc_s", "nod_root", ChildSort{Key: SortSize}, 5, &ChildPos{Kind: "bogus"})
	if err != ErrBadCursor {
		t.Fatalf("err = %v, want ErrBadCursor", err)
	}
}

// TestChildrenSortedTiesAcrossPages: names that compare equal (case, accents and leading zeros only) fall across page
// boundaries at every limit, in both directions. The expected order is written out by hand, not computed with
// names.Compare. Within a tie the ID decides, in the direction of the sort. The seed sets each name key to the ID, so
// names that differ only in case can be seeded together; a folder refuses that, but the order is the same either way.
func TestChildrenSortedTiesAcrossPages(t *testing.T) {
	kids := []childSeed{
		{id: "nod_a1", name: "alpha", kind: KindFile, size: 1},
		{id: "nod_f1", name: "file 2", kind: KindFile, size: 1},
		{id: "nod_f2", name: "file 02", kind: KindFile, size: 1},
		{id: "nod_f3", name: "file 2", kind: KindFile, size: 1},
		{id: "nod_r1", name: "resume", kind: KindFile, size: 1},
		{id: "nod_r2", name: "Résumé", kind: KindFile, size: 1},
		{id: "nod_r3", name: "RESUME", kind: KindFile, size: 1},
		{id: "nod_r4", name: "resume", kind: KindFile, size: 1},
		{id: "nod_z1", name: "zeta", kind: KindFile, size: 1},
	}
	asc := []string{"nod_a1", "nod_f1", "nod_f2", "nod_f3", "nod_r1", "nod_r2", "nod_r3", "nod_r4", "nod_z1"}
	desc := []string{"nod_z1", "nod_r4", "nod_r3", "nod_r2", "nod_r1", "nod_f3", "nod_f2", "nod_f1", "nod_a1"}
	for _, c := range []struct {
		name string
		desc bool
		want []string
	}{{"ascending", false, asc}, {"descending", true, desc}} {
		for _, limit := range []int{1, 2, 3, 5} {
			t.Run(fmt.Sprintf("%s_limit%d", c.name, limit), func(t *testing.T) {
				s := openTest(t)
				seedChildren(t, s, kids)
				got := walkChildren(t, s, ChildSort{Key: SortName, Desc: c.desc}, limit)
				if !slices.Equal(got, c.want) {
					t.Fatalf("order across pages\n got: %v\nwant: %v", got, c.want)
				}
			})
		}
	}
}
