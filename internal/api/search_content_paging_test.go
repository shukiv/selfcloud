// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"fmt"
	"testing"
)

// TestContentPagingWithSharedBlobs: the copies of one file share one blob and so one content row, and a page can end
// between two of them. The cursor carries the node as well as the content row, so each copy comes back once, in every
// page. Mutation: a cursor that drops the node position repeats the copies a page boundary falls inside.
func TestContentPagingWithSharedBlobs(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	src := fx.file(t, "orig.txt", "copiedword")
	want := map[string]bool{src.Name: true}
	for i := 0; i < 4; i++ {
		name := fmt.Sprintf("copy%d.txt", i)
		rec := call(t, fx.s, "POST", "/api/v1/nodes/"+src.ID+"/copy", fx.key, map[string]any{"name": name})
		wantStatus(t, rec, 201)
		want[name] = true
	}
	drainText(t, fx.s.textWorker)
	seen := map[string]int{}
	cursor := ""
	for page := 0; page < 10; page++ {
		url := "/api/v1/search?in=content&q=copiedword&limit=2"
		if cursor != "" {
			url += "&cursor=" + cursor
		}
		rec := callSession(t, fx.s, fx.annSess, "GET", url, nil)
		wantStatus(t, rec, 200)
		p := decode[testPage](t, rec)
		for _, h := range p.Items {
			seen[h.Name]++
		}
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}
	for name := range want {
		if seen[name] != 1 {
			t.Errorf("%s came back %d times across the pages, want once", name, seen[name])
		}
	}
	if len(seen) != len(want) {
		t.Errorf("pages found %v, want %v", seen, want)
	}
}
