// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"strings"
	"testing"
)

// planOf returns the query plan of sql with args, one line a step, and fails on a step that scans the nodes table.
// The restriction checks run on every GET and Range of a zip for a viewer or commenter (zr1, decision 117): a scan of
// the nodes table inside them makes each check cost the size of the whole table, not the chain or the subtree.
func planOf(t *testing.T, sql string, args ...any) []string {
	t.Helper()
	s := openTest(t)
	rows, err := s.r.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+sql, args...)
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
	if len(plan) == 0 {
		t.Fatal("no query plan")
	}
	return plan
}

// assertNoNodesScan fails on any step of plan that scans the nodes table, under the names the queries give it. A scan of
// a recursive common table expression is allowed: it is the walk itself (trash_plan_test.go says the same).
func assertNoNodesScan(t *testing.T, plan []string) {
	t.Helper()
	t.Logf("plan:\n  %s", strings.Join(plan, "\n  "))
	for _, detail := range plan {
		for _, bad := range []string{"SCAN nodes", "SCAN n", "SCAN p"} {
			if detail == bad || strings.HasPrefix(detail, bad+" ") {
				t.Errorf("the restriction check scans the nodes table: %q", detail)
			}
		}
	}
}

// TestNoDownloadSQLDoesNotScanNodes: the check that a node or an ancestor is no-download, or its team space turns the
// rule on, reaches each node by its key.
func TestNoDownloadSQLDoesNotScanNodes(t *testing.T) {
	assertNoNodesScan(t, planOf(t, noDownloadSQL, "acc_a", "nod_a", "acc_a", "acc_a", "nod_a"))
}

// TestSubtreeNoDownloadSQLDoesNotScanNodes: the check that a subtree holds a no-download node walks the children by
// their parent and reads each node's flag in the walk, not by a scan of the nodes table.
func TestSubtreeNoDownloadSQLDoesNotScanNodes(t *testing.T) {
	assertNoNodesScan(t, planOf(t, subtreeNoDownloadSQL, "acc_a", "nod_a", "acc_a"))
}
