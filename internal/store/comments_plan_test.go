// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"strings"
	"testing"
)

// TestCommentsSQLDoesNotScan reads the query plan of every comment statement, as TestTrashChainSQLDoesNotScanNodes does
// for the ancestor walk. A scan of the comments or mentions table makes a node's list cost the size of the whole table,
// so each statement must reach its rows through an index. The plans are read on an empty database: SQLite chooses the
// index from the schema, not from the rows, and this test is about the schema. Each statement is named so that a failure
// says which one scans.
func TestCommentsSQLDoesNotScan(t *testing.T) {
	s := openTest(t)
	cases := []struct {
		name string
		sql  string
		args []any
	}{
		{"comment by ID", commentByIDSQL, []any{"acc_a", "cmt_a"}},
		{"live count, capped", liveCountSQL, []any{"acc_a", "nod_a", 5001}},
		{"has replies", hasRepliesSQL, []any{"nod_a", "cmt_a", "acc_a"}},
		{"top-level page, first", topLevelSQL(false), []any{"acc_a", "nod_a", 50}},
		{"top-level page, after a cursor", topLevelSQL(true), []any{"acc_a", "nod_a", "t", "t", "cmt_a", 50}},
		{"thread, first", threadSQL(false), []any{"acc_a", "nod_a", "cmt_a", 50}},
		{"thread, after a cursor", threadSQL(true), []any{"acc_a", "nod_a", "cmt_a", "t", "t", "cmt_b", 50}},
		{"replies of a page", previewSQL(50), append([]any{"acc_a", "nod_a"}, append(make([]any, 50), 50)...)},
		{"mentions of a page", mentionsSQL(50), append([]any{"acc_a"}, make([]any, 50)...)},
		{"people suggestions", activeNamedSQL, []any{"acc_a", "active", "%x%", 50}},
	}
	for _, c := range cases {
		c := c
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
				for _, bad := range []string{"SCAN comments", "SCAN mentions", "SCAN c", "SCAN m"} {
					if detail == bad || strings.HasPrefix(detail, bad+" ") {
						t.Errorf("%s scans a table: %q\nplan: %v", c.name, detail, plan)
					}
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if len(plan) == 0 {
				t.Fatal("no query plan")
			}
			t.Logf("plan: %v", plan)
		})
	}
}
