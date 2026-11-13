// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/search"
	"github.com/shukiv/selfcloud/internal/store"
)

// TestMeasureUnreadableMatchCost ("what timing still shows"): a viewer searches a word that N files the viewer
// cannot reach all hold. The answer is the same bytes as for no such file, but the time is not: the walk reads each
// unreadable match. Gated by SELFCLOUD_MEASURE_CONTENT=1; figures in decision 46.
func TestMeasureUnreadableMatchCost(t *testing.T) {
	if os.Getenv("SELFCLOUD_MEASURE_CONTENT") != "1" {
		t.Skip("set SELFCLOUD_MEASURE_CONTENT=1 to measure the cost of unreadable matches")
	}
	fx := newContentFixture(t)
	hidden := newFolder(t, fx.s, fx.key, fx.acc.ID, "Hidden")
	files := 5000
	if v := os.Getenv("SELFCLOUD_MEASURE_N"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			t.Fatal(err)
		}
		files = n
	}
	start := time.Now()
	for i := range files {
		putTyped(t, fx.s, fx.key, hidden.ID, "h"+strconv.Itoa(i)+".txt", "text/plain", "hiddenword "+fmt.Sprint(i))
	}
	drainText(t, fx.s.textWorker)
	t.Logf("%d unreadable matches written and indexed in %v", files, time.Since(start).Round(time.Millisecond))

	// Control: the same word in no file.
	s0 := time.Now()
	rec := callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/search?in=content&q=nowhereword&limit=50", nil)
	wantStatus(t, rec, 200)
	t.Logf("control, no match anywhere: %v, answer %s", time.Since(s0).Round(time.Microsecond), rec.Body.String())

	s0 = time.Now()
	rec = callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/search?in=content&q=hiddenword&limit=50", nil)
	t.Logf("%d unreadable matches, limit 50: %v, status %d, answer %.200s", files, time.Since(s0).Round(time.Millisecond), rec.Code, rec.Body.String())
}

// TestMeasureContentPlanAfterPosition logs the query plan of a content batch that starts after a position, which is what
// every batch after the first runs. Gated by SELFCLOUD_MEASURE_CONTENT=1. The bound must be a constraint on the full-text
// scan (MATCH with a rowid range), not a filter after it.
func TestMeasureContentPlanAfterPosition(t *testing.T) {
	if os.Getenv("SELFCLOUD_MEASURE_CONTENT") != "1" {
		t.Skip("set SELFCLOUD_MEASURE_CONTENT=1 to log the plan of a later batch")
	}
	fx := newContentFixture(t)
	fx.file(t, "a.txt", "planword")
	drainText(t, fx.s.textWorker)
	req := &search.Request{AccountID: fx.acc.ID, In: search.InContent, Terms: []string{"planword"}, ModifiedFrom: -1,
		ModifiedTo: -1, SizeMin: -1, SizeMax: -1, Limit: 25}
	query, args, err := req.ContentSQL(search.Pos{A: 1000, B: 7}, 100)
	if err != nil {
		t.Fatal(err)
	}
	err = fx.s.store.Read(context.Background(), func(q store.Querier) error {
		rows, err := q.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				return err
			}
			t.Logf("plan: %s", detail)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
}
