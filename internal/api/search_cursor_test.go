// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
)

// searchPage is one page of a search answer, as far as these tests read it.
type searchPage struct {
	Items []struct {
		Name string `json:"name"`
	} `json:"items"`
	NextCursor string `json:"next_cursor"`
}

// pageOf decodes a search answer that must be 200.
func pageOf(t *testing.T, rec interface {
	Result() *http.Response
}) searchPage {
	t.Helper()
	res := rec.Result()
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("search status = %d, want 200", res.StatusCode)
	}
	var p searchPage
	if err := json.NewDecoder(res.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	return p
}

func (p searchPage) names() []string {
	out := make([]string, 0, len(p.Items))
	for _, it := range p.Items {
		out = append(out, it.Name)
	}
	return out
}

// TestSearchLabelPagesAcrossThreePages: a label-filtered search with a page size of one reads three pages, each with the
// next cursor, and the last page has no cursor. Before the fix, the second page was refused with invalid_request.
func TestSearchLabelPagesAcrossThreePages(t *testing.T) {
	fx := newLabelFixture(t)
	for i := range 3 {
		name := "cur-" + strconv.Itoa(i) + ".txt"
		writeFile(t, fx.s, fx.key, fx.fld.ID, "/a/b/"+name, "x")
		setLabel(t, fx.keyDo, nodeIDOf(t, fx.s, fx.acc.ID, fx.b.ID, name), fx.note, "Hello "+strconv.Itoa(i), http.StatusNoContent)
	}
	path := "/api/v1/search?q=cur-&limit=1&label=" + fx.note + ":Hel"
	var got []string
	cursor := ""
	for page := 1; page <= 4; page++ {
		p := "/api/v1/search?q=cur-&limit=1&label=" + fx.note + ":Hel"
		if cursor != "" {
			p += "&cursor=" + cursor
		}
		res := pageOf(t, callSession(t, fx.s, fx.ownerSess, "GET", p, nil))
		got = append(got, res.names()...)
		cursor = res.NextCursor
		if cursor == "" {
			break
		}
	}
	if strings.Join(got, "|") != "cur-0.txt|cur-1.txt|cur-2.txt" {
		t.Fatalf("pages of %s gave %q; want the three files in name order", path, got)
	}
}

// TestSearchCursorRefusesAChangedLabelFilter: a cursor made for one label value does not open for another, and the same
// value still opens it.
func TestSearchCursorRefusesAChangedLabelFilter(t *testing.T) {
	fx := newLabelFixture(t)
	for i := range 3 {
		name := "cur-" + strconv.Itoa(i) + ".txt"
		writeFile(t, fx.s, fx.key, fx.fld.ID, "/a/b/"+name, "x")
		setLabel(t, fx.keyDo, nodeIDOf(t, fx.s, fx.acc.ID, fx.b.ID, name), fx.note, "Hello "+strconv.Itoa(i), http.StatusNoContent)
	}
	first := pageOf(t, callSession(t, fx.s, fx.ownerSess, "GET", "/api/v1/search?q=cur-&limit=1&label="+fx.note+":Hel", nil))
	if first.NextCursor == "" {
		t.Fatal("first page has no cursor")
	}
	changed := "/api/v1/search?q=cur-&limit=1&label=" + fx.note + ":Hex&cursor=" + first.NextCursor
	wantError(t, callSession(t, fx.s, fx.ownerSess, "GET", changed, nil), http.StatusBadRequest, apierr.InvalidRequest)
	again := "/api/v1/search?q=cur-&limit=1&label=" + fx.note + ":Hel&cursor=" + first.NextCursor
	if got := pageOf(t, callSession(t, fx.s, fx.ownerSess, "GET", again, nil)).names(); len(got) != 1 {
		t.Fatalf("same filter with its cursor found %q, want one name", got)
	}
}

// TestSearchCursorIsBoundToItsCallerAndAccount: a cursor opens only for the caller and the account that made it. The
// other account's key gets invalid_request for it, and so does another caller of the same account.
func TestSearchCursorIsBoundToItsCallerAndAccount(t *testing.T) {
	fx := newSearchFixture(t)
	first := pageOf(t, callSession(t, fx.s, fx.sess, "GET", "/api/v1/search?kind=file&limit=2", nil))
	if first.NextCursor == "" {
		t.Fatal("first page has no cursor")
	}
	path := "/api/v1/search?kind=file&limit=2&cursor=" + first.NextCursor
	// Another account's key, this account's key, and another user of this same account all get the refusal.
	_, otherKey := newAccount(t, fx.s, adminKey(t, fx.s), "Other")
	wantError(t, call(t, fx.s, "GET", path, otherKey, nil), http.StatusBadRequest, apierr.InvalidRequest)
	wantError(t, call(t, fx.s, "GET", path, fx.key, nil), http.StatusBadRequest, apierr.InvalidRequest)
	_, second := ownerUser(t, fx.s, fx.key, fx.acc.ID, "second@example.com")
	wantError(t, callSession(t, fx.s, second, "GET", path, nil), http.StatusBadRequest, apierr.InvalidRequest)
	if got := pageOf(t, callSession(t, fx.s, fx.sess, "GET", path, nil)).names(); len(got) == 0 {
		t.Fatal("the caller's own cursor found nothing")
	}
}

// TestSearchCursorAfterAKeyRotationStartsAgain: a key rotation invalidates every cursor. The old cursor gets the one
// invalid_request answer that says to start again, and the search without it gives the first page.
func TestSearchCursorAfterAKeyRotationStartsAgain(t *testing.T) {
	fx := newSearchFixture(t)
	const query = "/api/v1/search?kind=file&limit=2"
	first := pageOf(t, call(t, fx.s, "GET", query, fx.key, nil))
	if first.NextCursor == "" {
		t.Fatal("first page has no cursor")
	}
	fx.s.links.current = bytes.Repeat([]byte{9}, 32)
	rec := call(t, fx.s, "GET", query+"&cursor="+first.NextCursor, fx.key, nil)
	wantError(t, rec, http.StatusBadRequest, apierr.InvalidRequest)
	if !strings.Contains(rec.Body.String(), "start the search again") {
		t.Fatalf("rotated cursor answer = %s; want the start-again message", rec.Body)
	}
	restarted := pageOf(t, call(t, fx.s, "GET", query, fx.key, nil))
	if strings.Join(restarted.names(), "|") != strings.Join(first.names(), "|") {
		t.Fatalf("restarted page = %q, want the first page %q", restarted.names(), first.names())
	}
}

// TestAccountKeySearchesItsAccount: an account key may search, and finds the names of its own account.
func TestAccountKeySearchesItsAccount(t *testing.T) {
	fx := newSearchFixture(t)
	if got := hitNames(t, call(t, fx.s, "GET", "/api/v1/search?q=report", fx.key, nil)); len(got) == 0 {
		t.Fatal("the account key found nothing; it reaches every folder of its account")
	}
}

// TestAccountKeyOfAnotherAccountFindsNothing: a key of another account searches only that account, so a name that is in
// this account is not found with it.
func TestAccountKeyOfAnotherAccountFindsNothing(t *testing.T) {
	fx := newSearchFixture(t)
	admin := adminKey(t, fx.s)
	_, otherKey := newAccount(t, fx.s, admin, "Other")
	rec := call(t, fx.s, "GET", "/api/v1/search?q=report", otherKey, nil)
	if got := hitNames(t, rec); len(got) != 0 {
		t.Fatalf("another account's key found %q", got)
	}
	if got := hitNames(t, call(t, fx.s, "GET", "/api/v1/search?q=report", fx.key, nil)); len(got) == 0 {
		t.Fatal("the account's own key found nothing")
	}
}

// TestTokenMintedByAccountKeyIsConfined: a token minted by an account key, with no user, searches only the folders it
// names. Names in another folder of the same account are not found with it.
func TestTokenMintedByAccountKeyIsConfined(t *testing.T) {
	fx := newSearchFixture(t)
	other := newFolder(t, fx.s, fx.key, fx.acc.ID, "Other")
	putTyped(t, fx.s, fx.key, other.ID, "other-plan.txt", "text/plain", "x")
	tok := mintToken(t, fx.s, fx.key, fx.acc.ID, other.ID, "")
	if got := hitNames(t, call(t, fx.s, "GET", "/api/v1/search?q=plan", tok, nil)); strings.Join(got, "|") != "other-plan.txt" {
		t.Fatalf("account-key token in folder Other found %q", got)
	}
	if got := hitNames(t, call(t, fx.s, "GET", "/api/v1/search?q=report", tok, nil)); len(got) != 0 {
		t.Fatalf("account-key token outside its folder found %q", got)
	}
}

// TestOneFileCopyIntoALargeFolderQueuesNothing: a one-file copy into a folder of 100,000 nodes indexes its own node in the
// copy's write. The backlog stays empty, so the search job reads no batch of the folder, and the copy is found at once.
func TestOneFileCopyIntoALargeFolderQueuesNothing(t *testing.T) {
	fx := newSearchFixture(t)
	seedTree(t, fx.s, fx.acc.ID, fx.fld.ID, teamRoot(t, fx), 100000)
	writeFile(t, fx.s, fx.key, fx.fld.ID, "/lonely-marker.txt", "x")
	file := nodeIDOf(t, fx.s, fx.acc.ID, teamRoot(t, fx), "lonely-marker.txt")
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+file+"/copy", fx.key, map[string]any{"name": "lonely-copy.txt"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("copy: status %d: %s", rec.Code, rec.Body)
	}
	if left, err := fx.s.store.SearchBacklogLeft(context.Background()); err != nil || left != 0 {
		t.Fatalf("backlog = %d, %v after a one-file copy; want 0 batches queued", left, err)
	}
	if got := hitNames(t, fx.search(t, "q=lonely")); len(got) != 2 {
		t.Fatalf("found %q; want the original and the copy at once", got)
	}
}
