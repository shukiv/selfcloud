// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"fmt"
	"os"
	"slices"
	"testing"
	"time"
)

// TestChildrenSortedAtScale measures a sorted listing of one large folder (decision 47): the cost of building the three
// listing indexes on rows that already exist, their disk cost, and the time of the first page and of a deep page for each
// sort and direction. It is skipped unless SELFCLOUD_SCALE is set. It is a measurement, not a load test. The run uses
// TMPDIR on /var/tmp, nice -n 19 and a ten-minute limit, on a shared machine.
//
// The fixture is 200,000 children of one directory: 2 in 100 are directories, the rest files. Names mix Latin, Hebrew and
// digits; sizes repeat every 4096 values and times every 5000, so the ties are real.
func TestChildrenSortedAtScale(t *testing.T) {
	if os.Getenv("SELFCLOUD_SCALE") == "" {
		t.Skip("set SELFCLOUD_SCALE to run the scale measurement")
	}
	const rows = 200000
	const deepAt = 150000
	s := openTest(t)
	ctx := t.Context()
	exec := func(what, q string, args ...any) {
		t.Helper()
		start := time.Now()
		if _, err := s.w.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		t.Logf("%s in %v", what, time.Since(start).Round(time.Millisecond))
	}
	pages := func() int64 {
		t.Helper()
		var n int64
		if err := s.w.QueryRowContext(ctx, "PRAGMA page_count").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// The indexes are dropped, so that the build can be timed on the rows.
	exec("drop listing indexes", `DROP INDEX nodes_list_name; DROP INDEX nodes_list_size; DROP INDEX nodes_list_modified`)
	exec("account, folder, root", `INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_s', 'S', 'active', 0, 0);
		INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at) VALUES ('fld_s', 'acc_s', 'Docs', 'docs', 0, 0);
		INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
			VALUES ('nod_root', 'acc_s', 'fld_s', NULL, '', '', 'dir', 0, 0)`)
	exec(fmt.Sprintf("%d children", rows), fmt.Sprintf(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < %d)
		INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, size, created_at, modified_at)
		SELECT printf('nod_%%06d', i), 'acc_s', 'fld_s', 'nod_root',
			CASE i %% 3 WHEN 0 THEN 'file ' || i WHEN 1 THEN 'קובץ ' || i ELSE 'Doc ' || i END,
			printf('k%%06d', i),
			CASE WHEN i %% 50 = 0 THEN 'dir' ELSE 'file' END,
			CASE WHEN i %% 50 = 0 THEN 0 ELSE (i * 7919) %% 4096 END,
			0, (i * 37) %% 5000
		FROM n`, rows))

	before := pages()
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	var migration string
	for _, m := range migs {
		if m.version == 35 {
			migration = m.sql
		}
	}
	if migration == "" {
		t.Fatal("migration 0035 not found")
	}
	start := time.Now()
	if _, err := s.w.ExecContext(ctx, migration); err != nil {
		t.Fatalf("migration 0035: %v", err)
	}
	t.Logf("building the three listing indexes on %d rows took %v", rows, time.Since(start).Round(time.Millisecond))
	after := pages()
	var pageSize int64
	if err := s.w.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		t.Fatal(err)
	}
	t.Logf("disk cost of the indexes: %d pages of %d bytes, %.1f MB (database %d pages before, %d after)",
		after-before, pageSize, float64((after-before)*pageSize)/1e6, before, after)

	// Each sort and direction: the first page, and a page at a deep position, each the median of five reads.
	for _, key := range []SortKey{SortName, SortSize, SortModified} {
		for _, desc := range []bool{false, true} {
			order := ChildSort{Key: key, Desc: desc}
			deep := deepPosition(t, s, order, deepAt)
			first := medianOf(5, func() {
				if _, _, err := s.ChildrenSorted(ctx, "acc_s", "nod_root", order, 50, nil); err != nil {
					t.Fatal(err)
				}
			})
			page := medianOf(5, func() {
				if _, _, err := s.ChildrenSorted(ctx, "acc_s", "nod_root", order, 50, &deep); err != nil {
					t.Fatal(err)
				}
			})
			t.Logf("key %d desc %v: first page %v, page after row %d %v", key, desc, first.Round(time.Microsecond), deepAt, page.Round(time.Microsecond))
		}
	}
}

// deepPosition is where the walk stands after n rows of the listing sorted by order, read with a page of 1000 at a time.
func deepPosition(t *testing.T, s *Store, order ChildSort, n int) ChildPos {
	t.Helper()
	var pos ChildPos
	read := 0
	var after *ChildPos
	for read < n {
		page, more, err := s.ChildrenSorted(t.Context(), "acc_s", "nod_root", order, 1000, after)
		if err != nil {
			t.Fatal(err)
		}
		read += len(page)
		pos = order.PosOf(page[len(page)-1])
		after = &pos
		if !more {
			break
		}
	}
	return pos
}

// medianOf runs f n times and returns the median duration.
func medianOf(n int, f func()) time.Duration {
	var ds []time.Duration
	for range n {
		start := time.Now()
		f()
		ds = append(ds, time.Since(start))
	}
	slices.Sort(ds)
	return ds[len(ds)/2]
}
