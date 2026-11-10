// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"fmt"
	"testing"
)

// TestContentSearchPagesWithFilters: a content search pages in full with a limit of two, with no filter and with each of
// the filters that a content search takes (an extension, a modified range). No hit comes back twice, and every matching
// file is found. Found by bisect at a partial index that made the planner put nodes outside the full-text scan (ff1a):
// the pages then followed the nodes' order, not the text rows', and repeated or skipped hits.
func TestContentSearchPagesWithFilters(t *testing.T) {
	t.Parallel()
	variants := []struct{ name, query string }{
		{"no filter", ""},
		{"extension", "&ext=txt"},
		{"modified range", "&modified_after=0"},
	}
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			t.Parallel()
			fx := newContentFixture(t)
			want := map[string]bool{}
			for i := 0; i < 5; i++ {
				want[fx.file(t, fmt.Sprintf("p%d.txt", i), "pagedword").Name] = true
			}
			drainText(t, fx.s.textWorker)
			seen := map[string]bool{}
			cursor := ""
			for page := 0; page < 10; page++ {
				url := "/api/v1/search?in=content&q=pagedword&limit=2" + v.query
				if cursor != "" {
					url += "&cursor=" + cursor
				}
				rec := callSession(t, fx.s, fx.annSess, "GET", url, nil)
				wantStatus(t, rec, 200)
				p := decode[testPage](t, rec)
				for _, h := range p.Items {
					if seen[h.Name] {
						t.Fatalf("%s: hit %s came back twice (page %d)", v.name, h.Name, page)
					}
					seen[h.Name] = true
				}
				if p.NextCursor == "" {
					break
				}
				cursor = p.NextCursor
			}
			if fmt.Sprint(seen) != fmt.Sprint(want) {
				t.Errorf("%s: pages found %v, want %v", v.name, seen, want)
			}
		})
	}
}
