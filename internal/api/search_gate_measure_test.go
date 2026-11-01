// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/search"
	"github.com/shukiv/selfcloud/internal/store"
)

// TestMeasureNameSearchUnreadable measures a member's name search for a word that N files the member cannot reach all
// hold, against the same search with none. It logs the plan of the candidate query and the time of each batch. Gated by
// SELFCLOUD_MEASURE_NAMES=1; N from SELFCLOUD_MEASURE_N (default 5000).
func TestMeasureNameSearchUnreadable(t *testing.T) {
	if os.Getenv("SELFCLOUD_MEASURE_NAMES") != "1" {
		t.Skip("set SELFCLOUD_MEASURE_NAMES=1 to measure a name search past unreadable matches")
	}
	fx := newContentFixture(t)
	n := 5000
	if v := os.Getenv("SELFCLOUD_MEASURE_N"); v != "" {
		x, err := strconv.Atoi(v)
		if err != nil {
			t.Fatal(err)
		}
		n = x
	}
	hidden := newFolder(t, fx.s, fx.key, fx.acc.ID, "Hidden")
	start := time.Now()
	// The hidden names come first in the index, so the walk reads every one of them before it reaches a readable name.
	for i := range n {
		putTyped(t, fx.s, fx.key, hidden.ID, "budget hidden "+strconv.Itoa(i)+".txt", "text/plain", "x")
	}
	for i := range 40 {
		putTyped(t, fx.s, fx.key, fx.fld.ID, "budget team "+strconv.Itoa(i)+".txt", "text/plain", "x")
	}
	t.Logf("%d unreadable names written in %v", n, time.Since(start).Round(time.Millisecond))

	req := &search.Request{AccountID: fx.acc.ID, Terms: []string{"budget"}, ModifiedFrom: -1, ModifiedTo: -1,
		SizeMin: -1, SizeMax: -1, Limit: 25}
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
		t.Logf("name plan: %s", strings.Join(plan, " | "))
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}

	// The batches the handler walks, timed one by one.
	pos := search.Pos{}
	batches := 0
	walk := time.Now()
	for {
		b0 := time.Now()
		var cands []search.Candidate
		err := fx.s.store.Read(context.Background(), func(q store.Querier) error {
			var err error
			cands, err = req.Candidates(context.Background(), q, pos, 100)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if batches < 3 || len(cands) == 0 {
			t.Logf("batch %d: %d candidates in %v", batches, len(cands), time.Since(b0).Round(time.Millisecond))
		}
		if len(cands) == 0 {
			break
		}
		pos = cands[len(cands)-1].At
		batches++
	}
	t.Logf("walk of %d batches with the candidate query alone: %v", batches, time.Since(walk).Round(time.Millisecond))

	s0 := time.Now()
	rec := callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/search?q=budget&limit=25", nil)
	t.Logf("handler, q=budget limit 25: %v, status %d", time.Since(s0).Round(time.Millisecond), rec.Code)
}
