// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"strings"
	"testing"
)

// TestTrashChainSQLDoesNotScanNodes reads the query plan of the ancestor walk that every access check runs. A scan of
// the nodes table inside that plan makes each check cost the size of the whole table, so a walk over a tree costs
// O(n²) in its nodes: a 20,000-entry zip took 4 min 19 s, and 5,000 entries took 11 s (decision 35). The walk must reach
// each node through its primary key. The chain's own scan is allowed: it is a recursive common table expression.
func TestTrashChainSQLDoesNotScanNodes(t *testing.T) {
	s := openTest(t)
	rows, err := s.r.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+trashChainSQL, "acc_a", "nod_a", "acc_a")
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
		for _, bad := range []string{"SCAN nodes", "SCAN n", "SCAN p"} {
			if detail == bad || strings.HasPrefix(detail, bad+" ") {
				t.Errorf("the ancestor walk scans the nodes table: %q\nplan: %v", detail, plan)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(plan) == 0 {
		t.Fatal("no query plan")
	}
}
