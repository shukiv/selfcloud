// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/mail"
)

// TestMeasureAncestors times a folder open's trail on loopback, at depth 1 and depth 10, over real HTTP. It compares
// the one request GET /nodes/{nod}/ancestors with the walk the web made before (one GET /nodes/{id} per level, in
// series, from the directory up to the folder root). It runs only when SELFCLOUD_MEASURE_ANCESTORS is set.
func TestMeasureAncestors(t *testing.T) {
	if os.Getenv("SELFCLOUD_MEASURE_ANCESTORS") == "" {
		t.Skip("set SELFCLOUD_MEASURE_ANCESTORS=1 to run the ancestors measurement")
	}
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")

	shallow := putNode(t, s, key, f.ID, "/shallow.txt", "x")
	var segs []string
	for i := 1; i <= 10; i++ {
		segs = append(segs, fmt.Sprintf("m%d", i))
	}
	mkdirs(t, s, key, f.ID, "/"+strings.Join(segs, "/"))
	deep := putNode(t, s, key, f.ID, "/"+strings.Join(segs, "/")+"/f.txt", "x")
	// Sixty-four directories down: the answer is capped at the 64 nearest ancestors, the most one request can give.
	var segs64 []string
	for i := 1; i <= 64; i++ {
		segs64 = append(segs64, fmt.Sprintf("n%d", i))
	}
	mkdirs(t, s, key, f.ID, "/"+strings.Join(segs64, "/"))
	deepest := putNode(t, s, key, f.ID, "/"+strings.Join(segs64, "/")+"/f.txt", "x")

	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	get := func(path string) (int, time.Duration) {
		req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+key)
		start := time.Now()
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, time.Since(start)
	}
	const runs = 200
	p50 := func(d []time.Duration) time.Duration {
		slices.Sort(d)
		return d[len(d)/2]
	}

	for _, c := range []struct {
		name string
		nod  string
	}{{"depth 1", shallow.ID}, {"depth 10", deep.ID}, {"depth 64", deepest.ID}} {
		// The chain the walk visits, read once from the answer itself.
		var chain []string
		{
			rec := call(t, s, "GET", "/api/v1/nodes/"+c.nod+"/ancestors", key, nil)
			wantStatus(t, rec, 200)
			for _, n := range decode[struct {
				Ancestors []nodeJSON `json:"ancestors"`
			}](t, rec).Ancestors {
				chain = append(chain, n.ID)
			}
		}
		one := make([]time.Duration, 0, runs)
		walk := make([]time.Duration, 0, runs)
		for range runs {
			status, d := get("/api/v1/nodes/" + c.nod + "/ancestors")
			if status != 200 {
				t.Fatalf("ancestors status %d", status)
			}
			one = append(one, d)
		}
		for range runs {
			start := time.Now()
			// The walk the web made: the node's own stat, then one stat per level, in series.
			statusOwn, _ := get("/api/v1/nodes/" + c.nod)
			for _, id := range chain {
				if st, _ := get("/api/v1/nodes/" + id); st != 200 {
					t.Fatalf("stat of %s: %d", id, st)
				}
			}
			if statusOwn != 200 {
				t.Fatalf("stat status %d", statusOwn)
			}
			walk = append(walk, time.Since(start))
		}
		t.Logf("%s (%d ancestors): one request p50 %v; the per-level walk (%d requests in series) p50 %v",
			c.name, len(chain), p50(one), len(chain)+1, p50(walk))
	}
}
