// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"sort"
	"strings"
	"testing"
)

// TestContentSearchTypeFilter: a content search takes ?type= as a name search does (decision 110). A file group keeps the
// text files of that group and no other: a .txt is wordprocessing, a .csv is spreadsheet, and no type gives both.
func TestContentSearchTypeFilter(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	fx.file(t, "a.txt", "grpword")
	putTyped(t, fx.s, fx.key, fx.fld.ID, "b.csv", "text/csv", "grpword")
	drainText(t, fx.s.textWorker)
	hits := func(q string) string {
		t.Helper()
		rec := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/search?in=content&q=grpword"+q, nil)
		wantStatus(t, rec, 200)
		var names []string
		for _, h := range decode[testPage](t, rec).Items {
			names = append(names, h.Name)
		}
		sort.Strings(names)
		return strings.Join(names, ",")
	}
	for _, c := range []struct{ query, want string }{
		{"", "a.txt,b.csv"},
		{"&type=spreadsheet", "b.csv"},
		{"&type=wordprocessing", "a.txt"},
		{"&type=pdf", ""},
	} {
		if got := hits(c.query); got != c.want {
			t.Errorf("content search%s: found %q, want %q", c.query, got, c.want)
		}
	}
}
