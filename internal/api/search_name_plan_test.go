// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/search"
	"github.com/shukiv/selfcloud/internal/store"
)

// TestNameQueryPlanMatchIsTheOuterLoop: a name search reads its candidates in batches, each batch after the last. The
// full-text table must be the outer loop of the candidate query, so each batch seeks past the last row and reads only the
// matches that follow. When the account's name rows are the outer loop instead, SQLite runs the full-text match once for
// each name row of the account, and a batch costs about the account's size times the match (measured: 2,000 unreadable
// names, one batch of a walk 0.6 s, a 25-result search past them 13.6 s; the content query fixed the same shape in
// decision 46). Mutation: the bound moved back to sn.id, or the account's name rows made the outer loop, fails this test.
func TestNameQueryPlanMatchIsTheOuterLoop(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	fx.file(t, "budget.txt", "x")
	for _, terms := range [][]string{{"budget"}, {"ab", "budget"}} {
		req := &search.Request{AccountID: fx.acc.ID, Terms: terms, ModifiedFrom: -1, ModifiedTo: -1, SizeMin: -1,
			SizeMax: -1, Limit: 25}
		query, args, err := req.NameSQL(0, 100)
		if err != nil {
			t.Fatal(err)
		}
		err = fx.s.store.Read(context.Background(), func(q store.Querier) error {
			rows, err := q.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+query, args...)
			if err != nil {
				return err
			}
			defer rows.Close()
			var plan []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					return err
				}
				plan = append(plan, detail)
			}
			t.Logf("terms %v plan: %s", terms, strings.Join(plan, " | "))
			if len(plan) == 0 || !strings.HasPrefix(plan[0], "SCAN search_") {
				t.Errorf("terms %v: the full-text table is not the outer loop: %v", terms, plan)
			}
			// A sort reads every match after the position before the first batch is returned, so a batch costs the matches
			// that follow it, not the batch. The match yields rows in rowid order, as the content query relies on.
			for _, d := range plan {
				if strings.Contains(d, "TEMP B-TREE") {
					t.Errorf("terms %v: the candidate query sorts every match: %s", terms, d)
				}
			}
			return rows.Err()
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
