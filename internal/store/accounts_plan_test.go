// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"strings"
	"testing"
)

// accountPlanStep is one step of a query plan: its id, its parent's id, and what it does.
type accountPlanStep struct {
	id, parent int
	detail     string
}

// accountPlan reads the query plan of query with args, on an empty database: the plan comes from the schema.
func accountPlan(t *testing.T, s *Store, query string, args []any) []accountPlanStep {
	t.Helper()
	rows, err := s.r.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []accountPlanStep
	for rows.Next() {
		var st accountPlanStep
		var unused int
		if err := rows.Scan(&st.id, &st.parent, &unused, &st.detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, st)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(plan) == 0 {
		t.Fatal("no query plan")
	}
	return plan
}

// TestAccountListsCountThePageOnly reads the plans of the account lists: by name and by creation, each way, a later page,
// and the plain list. Each account's user count must be evaluated for the page's rows only. The accounts are filtered,
// sorted and limited in a materialized page first, and the count's subquery belongs to the query over that page, not to
// the page's own query (the tl1 re-check: in name order the count ran for every account, before the sort and the LIMIT,
// so a page cost grew with the users of the whole installation).
func TestAccountListsCountThePageOnly(t *testing.T) {
	s := openTest(t)
	type planCase struct {
		name  string
		query string
		args  []any
	}
	sorted := func(name string, order AccountSort, after *AccountPos) planCase {
		q, args := accountsSortedSQL(AccountFilter{Status: AccountActive}, order, 50, after)
		return planCase{name, q, args}
	}
	after := &AccountPos{Name: "m", Created: 1, ID: "acc_m"}
	cases := []planCase{
		sorted("name ascending", AccountSort{ByName: true}, nil),
		sorted("name descending, a later page", AccountSort{ByName: true, Desc: true}, after),
		sorted("created ascending", AccountSort{}, nil),
		sorted("created descending, a later page", AccountSort{Desc: true}, after),
	}
	q, args, err := accountsFilteredSQL(AccountFilter{}, Page{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	cases = append(cases, planCase{"the plain list", q, args})

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan := accountPlan(t, s, c.query, c.args)
			var lines []string
			parent := map[int]int{}
			page, count := -1, -1
			for _, st := range plan {
				lines = append(lines, st.detail)
				parent[st.id] = st.parent
				if strings.HasPrefix(st.detail, "MATERIALIZE") && strings.Contains(st.detail, "page") {
					page = st.id
				}
				if strings.HasPrefix(st.detail, "CORRELATED SCALAR SUBQUERY") {
					count = st.id
				}
			}
			t.Logf("plan:\n  %s", strings.Join(lines, "\n  "))
			// under says whether step id is inside step top.
			under := func(id, top int) bool {
				for seen := 0; id != 0 && seen < len(plan); seen++ {
					if id == top {
						return true
					}
					id = parent[id]
				}
				return false
			}
			if page < 0 {
				t.Fatalf("the accounts are not paged before the count: no materialized page in the plan")
			}
			if count < 0 {
				t.Fatalf("no correlated count of the users in the plan")
			}
			if under(count, page) {
				t.Errorf("the user count is evaluated inside the page's query, before its LIMIT")
			}
			readsAccounts := false
			for _, st := range plan {
				if (strings.HasPrefix(st.detail, "SCAN accounts") || strings.HasPrefix(st.detail, "SEARCH accounts")) && under(st.id, page) {
					readsAccounts = true
				}
			}
			if !readsAccounts {
				t.Errorf("the page does not read the accounts table")
			}
		})
	}
}
