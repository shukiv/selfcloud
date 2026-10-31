// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"strings"
	"testing"
)

// TestSearchFollowsTheTokenNodeScope: a token confined to a directory finds the names below it, and not the same names
// outside it, though both are in the folder the token names.
func TestSearchFollowsTheTokenNodeScope(t *testing.T) {
	fx := newSearchFixture(t)
	mkdirs(t, fx.s, fx.key, fx.fld.ID, "/box")
	writeFile(t, fx.s, fx.key, fx.fld.ID, "/box/scoped-in.txt", "x")
	writeFile(t, fx.s, fx.key, fx.fld.ID, "/scoped-out.txt", "x")
	box := nodeIDOf(t, fx.s, fx.acc.ID, teamRoot(t, fx), "box")
	tok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}, "node": box})
	if got := hitNames(t, call(t, fx.s, "GET", "/api/v1/search?q=scoped", tok, nil)); strings.Join(got, "|") != "scoped-in.txt" {
		t.Fatalf("token confined to /box found %q; want only the name inside it", got)
	}
}
