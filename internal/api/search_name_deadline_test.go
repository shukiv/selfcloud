// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"strconv"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/config"
)

// TestNameSearchPastUnreadableMatchesWithinTheDeadline: a member's name search for a word that many files the member cannot
// reach hold must answer inside the production deadline (2 s) and still find the readable names. Before the candidate query
// was fixed (search_name_plan_test.go), the batches of such a walk cost the matches that follow them, so 1,500 unreadable
// names took the request past the deadline and it answered 503 unavailable, as the gate run showed on a loaded machine.
// Mutation: the candidate query's full-text table made the inner loop again, or the ORDER BY restored on the term path,
// makes this test answer 503.
//
// The deadline is real time, so the test does not run in parallel, and not under the race detector, which slows the walk many
// times over and failed it at 2 s on a loaded machine. TestNameQueryPlanMatchIsTheOuterLoop checks the query's shape in both.
func TestNameSearchPastUnreadableMatchesWithinTheDeadline(t *testing.T) {
	if raceEnabled {
		t.Skip("a deadline in real time means nothing under the race detector")
	}
	fx := newContentFixture(t, func(c *config.Config) { c.Search.Timeout = config.Duration(2 * time.Second) })
	hidden := newFolder(t, fx.s, fx.key, fx.acc.ID, "Hidden")
	for i := range 1500 {
		putTyped(t, fx.s, fx.key, hidden.ID, "budget hidden "+strconv.Itoa(i)+".txt", "text/plain", "x")
	}
	for i := range 40 {
		putTyped(t, fx.s, fx.key, fx.fld.ID, "budget team "+strconv.Itoa(i)+".txt", "text/plain", "x")
	}
	for _, path := range []string{"/api/v1/search?q=budget&limit=25", "/api/v1/search?q=budget&kind=file&limit=25"} {
		start := time.Now()
		rec := callSession(t, fx.s, fx.bobSess, "GET", path, nil)
		if rec.Code != 200 {
			t.Fatalf("%s: status %d after %v, want 200: %s", path, rec.Code, time.Since(start).Round(time.Millisecond), rec.Body.String())
		}
		hits := decode[testPage](t, rec).Items
		if len(hits) != 25 {
			t.Fatalf("%s: %d hits, want the page of 25 readable names", path, len(hits))
		}
	}
}
