// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"fmt"
	"strings"
	"testing"
)

// TestContentSearchPagesWithFilters: a content search pages in full with a limit of two, with no filter and with each
// filter a content search takes (an extension, a type group, a kind, a modified range, a size bound, a folder scope). No
// hit comes back twice, and exactly the matching files are found. Found by bisect at a partial index that made the planner
// put nodes outside the full-text scan (ff1a): the pages then followed the nodes' order, not the text rows', and repeated or
// skipped hits.
func TestContentSearchPagesWithFilters(t *testing.T) {
	t.Parallel()
	const (
		csvFile = "c.csv"
		bigFile = "big.txt"
		inside  = "inside.txt"
	)
	// The fixture: five small text files, a small csv, a text file over 100 bytes, and a text file in the folder Dir.
	everything := []string{"p0.txt", "p1.txt", "p2.txt", "p3.txt", "p4.txt", csvFile, bigFile, inside}
	txt := []string{"p0.txt", "p1.txt", "p2.txt", "p3.txt", "p4.txt", bigFile, inside}
	bounded := []string{"p0.txt", "p1.txt", "p2.txt", "p3.txt", "p4.txt", csvFile, inside}
	variants := []struct {
		name, query string
		inDir       bool
		want        []string
	}{
		{"no filter", "", false, everything},
		{"extension", "&ext=txt", false, txt},
		{"modified range", "&modified_after=0", false, everything},
		{"type wordprocessing", "&type=wordprocessing", false, txt},
		{"type spreadsheet", "&type=spreadsheet", false, []string{csvFile}},
		{"kind file", "&kind=file", false, everything},
		{"size bound", "&size_max=100", false, bounded},
		{"modified before", "&modified_before=1", false, nil},
		{"folder scope", "", true, []string{inside}},
	}
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			t.Parallel()
			fx := newContentFixture(t)
			for _, name := range []string{"p0.txt", "p1.txt", "p2.txt", "p3.txt", "p4.txt"} {
				fx.file(t, name, "pagedword")
			}
			putTyped(t, fx.s, fx.key, fx.fld.ID, csvFile, "text/csv", "pagedword")
			fx.file(t, bigFile, strings.Repeat("x ", 200)+"pagedword")
			dir := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Dir")
			putTyped(t, fx.s, fx.key, fx.fld.ID, "Dir/"+inside, "text/plain", "pagedword")
			drainText(t, fx.s.textWorker)
			query := v.query
			if v.inDir {
				query = "&scope=" + dir.ID
			}
			want := map[string]bool{}
			for _, name := range v.want {
				want[name] = true
			}
			seen := map[string]bool{}
			cursor := ""
			for page := 0; page < 10; page++ {
				url := "/api/v1/search?in=content&q=pagedword&limit=2" + query
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
