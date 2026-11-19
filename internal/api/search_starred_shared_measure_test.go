// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/store"
)

// TestMeasureSearchStarredAndSharedAlone measures the two user filters alone, without a query, as a member of a large
// account: the member holds the team space's root, stars a few of its files, and asks for starred=true and shared=true.
// The starred read seeks the member's stars; the shared read walks the account's names applying the Shared with me
// page's exclusions, which drop the whole root-granted space. Both must stay well inside the search deadline. The node
// count comes from SELFCLOUD_MEASURE_N (20,000 by default).
func TestMeasureSearchStarredAndSharedAlone(t *testing.T) {
	fx := newContentFixture(t)
	nodes := 20000
	if v := os.Getenv("SELFCLOUD_MEASURE_N"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			t.Fatal(err)
		}
		nodes = n
	}
	ctx := context.Background()
	start := time.Now()
	seedTree(t, fx.s, fx.acc.ID, fx.fld.ID, fx.fld.RootNodeID, nodes)
	if err := fx.s.store.SearchRebuild(ctx); err != nil {
		t.Fatal(err)
	}
	for {
		done, err := fx.s.store.SearchIndexStep(ctx, store.SearchBatch)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
	}
	t.Logf("%d nodes seeded and indexed in %v", nodes, time.Since(start).Round(time.Millisecond))
	// The member stars twenty files of the team space; ann holds its root as a viewer, so she reaches them all.
	for i := range 20 {
		n := putTyped(t, fx.s, fx.key, fx.fld.ID, "starred-"+strconv.Itoa(i)+".txt", "text/plain", "x")
		wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+n.ID+"/star", nil), 204)
	}
	deadline := fx.s.cfg.Search.Timeout.D()
	for _, c := range []struct {
		query string
		want  int
	}{
		{"starred=true", 20},
		{"shared=true", 0},
	} {
		start := time.Now()
		rec := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/search?"+c.query, nil)
		d := time.Since(start)
		wantStatus(t, rec, 200)
		if got := len(decode[testPage](t, rec).Items); got != c.want {
			t.Errorf("%s over %d nodes: %d hits, want %d", c.query, nodes, got, c.want)
		}
		t.Logf("%s alone over %d nodes: %v (deadline %v)", c.query, nodes, d.Round(time.Microsecond), deadline)
		if d > deadline/2 {
			t.Errorf("%s took %v, more than half the %v search deadline", c.query, d, deadline)
		}
	}
}
