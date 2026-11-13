// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// The confirmation oracle. A file the caller cannot read, in another folder, either contains the word or does
// not. A search may not tell the caller which: not by a cursor, a count, a short page, or a page that is empty where a
// full one should be. The walk below pages through the whole answer, with a small page, and compares every page. Node IDs
// are random in each world, so they are masked; a cursor is masked to its presence, since the walk follows it anyway.
// Each world is a fresh server.

// walkRandom are the values that differ between two servers with the same content: generated IDs, times and etags.
var (
	walkID     = regexp.MustCompile(`\b[a-z]{3}_[a-z0-9]{20,}\b`)
	walkTime   = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T[0-9:.]+Z`)
	walkEtag   = regexp.MustCompile(`\\"[0-9a-f]{32}\\"`)
	walkCursor = regexp.MustCompile(`"next_cursor":"[^"]*"`)
)

// maskWalk replaces what differs between two worlds by chance, and keeps the rest: names, snippets, the order, the page
// breaks, and whether a cursor follows.
func maskWalk(body string) string {
	body = walkID.ReplaceAllString(body, "ID")
	body = walkTime.ReplaceAllString(body, "TIME")
	body = walkEtag.ReplaceAllString(body, "ETAG")
	return walkCursor.ReplaceAllString(body, `"next_cursor":"<present>"`)
}

// oracleWorld runs the attack once and returns every page of the walk, masked. hiddenHas decides whether the unreadable
// file holds the word. Bob can read the team folder, and can write into it.
func oracleWorld(t *testing.T, hiddenHas bool, fill int) string {
	t.Helper()
	fx := newContentFixture(t)
	// A second folder bob (an editor of the team folder only) cannot reach.
	hidden := newFolder(t, fx.s, fx.key, fx.acc.ID, "Hidden")
	secret := "unrelated words"
	if hiddenHas {
		secret = "dictword appears here"
	}
	putTyped(t, fx.s, fx.key, hidden.ID, "secret.txt", "text/plain", secret)
	// Two files bob may read, which the walk must show in both worlds.
	putTyped(t, fx.s, fx.key, fx.fld.ID, "visible-a.txt", "text/plain", "dictword a")
	putTyped(t, fx.s, fx.key, fx.fld.ID, "visible-b.txt", "text/plain", "dictword b")
	// Bob fills the rest of the budget with matching files in his folder, and trashes each one.
	for i := range fill {
		n := putTyped(t, fx.s, fx.key, fx.fld.ID, fmt.Sprintf("own-%03d.txt", i), "text/plain", "dictword")
		wantStatus(t, callSession(t, fx.s, fx.bobSess, "DELETE", "/api/v1/nodes/"+n.ID, nil), 204)
	}
	drainText(t, fx.s.textWorker)
	var pages []string
	cursor := ""
	for range 20 {
		path := "/api/v1/search?in=content&q=dictword&limit=1"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		rec := callSession(t, fx.s, fx.bobSess, "GET", path, nil)
		wantStatus(t, rec, 200)
		body := rec.Body.String()
		pages = append(pages, maskWalk(body))
		var out struct {
			NextCursor string `json:"next_cursor"`
		}
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatal(err)
		}
		if out.NextCursor == "" {
			return strings.Join(pages, "\n")
		}
		cursor = out.NextCursor
	}
	t.Fatal("the walk did not end")
	return ""
}

// TestSearchAnswerDoesNotRevealAnUnreadableFile: with trashed matches filling the caller's own folder, every
// page of the walk is the same bytes, cursors included, whether or not the unreadable file holds the word. The walk must
// find the two visible matches, or it proves nothing.
func TestSearchAnswerDoesNotRevealAnUnreadableFile(t *testing.T) {
	t.Parallel()
	const fill = 49
	with := oracleWorld(t, true, fill)
	without := oracleWorld(t, false, fill)
	if !strings.Contains(without, "visible-b.txt") || !strings.Contains(without, "<present>") {
		t.Fatalf("the walk did not show the visible matches with a cursor between them:\n%s", without)
	}
	if with != without {
		t.Fatalf("the walk tells an unreadable match apart:\n with the word:    %s\n without the word: %s", with, without)
	}
}
