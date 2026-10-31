// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// sampleNames is the sample of the tokenizer study (section 0.3 of the slice q proposal), numbered from 1 as the study
// numbers its rows. Row 16 is added with the article form, so the article case has a row to find.
var sampleNames = []string{
	"דוח שנתי 2026.pdf",       // 1
	"הדוחות של בית הספר.docx", // 2
	"בבית.txt",                // 3
	"שָׁלוֹם.txt",             // 4 (with niqqud)
	"שלום לכולם.md",           // 5
	"תמונה-חוף.jpg",           // 6
	"Report 2026 דוח.xlsx",    // 7
	"reports-q3.pdf",          // 8
	"تقرير سنوي.pdf",          // 9
	"التقارير في المكتب.docx", // 10
	"تقرير.txt",               // 11
	"كتاب.txt",                // 12
	"مكتب-2026.jpg",           // 13
	"Résumé café.docx",        // 14
	"resume.docx",             // 15
	"التقرير النهائي.pdf",     // 16
}

// searchFixture is an account with one team folder holding the sample names at its root. The owner of the account is a
// user who reaches the team folder, so searches run as the owner's session.
type searchFixture struct {
	s     *Server
	key   string
	acc   accountJSON
	fld   folderJSON
	owner userResponse
	sess  testSession
}

func newSearchFixture(t *testing.T, mutate ...func(*config.Config)) *searchFixture {
	t.Helper()
	s := newAuthServer(t, &mail.Recorder{}, mutate...)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	fx := &searchFixture{s: s, key: key, acc: acc}
	fx.fld = newFolder(t, s, key, acc.ID, "Team")
	fx.owner, fx.sess = ownerUser(t, s, key, acc.ID, "owner@example.com")
	for _, name := range sampleNames {
		putTyped(t, s, key, fx.fld.ID, name, "text/plain", "x")
	}
	return fx
}

// search runs a search as the owner's session.
func (fx *searchFixture) search(t *testing.T, query string) *httptest.ResponseRecorder {
	t.Helper()
	return callSession(t, fx.s, fx.sess, "GET", "/api/v1/search?"+query, nil)
}

// hitNames decodes the names of a search answer, sorted.
func hitNames(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	wantStatus(t, rec, http.StatusOK)
	got := decode[listResponse[nodeJSON]](t, rec)
	out := []string{}
	for _, n := range got.Items {
		out = append(out, n.Name)
	}
	sort.Strings(out)
	return out
}

// nodeIDOf returns the ID of the node named name in directory parent of the account.
func nodeIDOf(t *testing.T, s *Server, accountID, parent, name string) string {
	t.Helper()
	n, err := s.store.Child(context.Background(), accountID, parent, strings.ToLower(name))
	if err != nil {
		t.Fatalf("node %q: %v", name, err)
	}
	return n.ID
}

// TestSearchFindsTheSampleWithTheChosenTokenizers is the sample of the tokenizer study as a test. A name of three or more
// characters is a substring search (the trigram table); a shorter one is a word-prefix search (the word table). A change to
// the tokenizer or the niqqud step fails here.
func TestSearchFindsTheSampleWithTheChosenTokenizers(t *testing.T) {
	fx := newSearchFixture(t)
	at := func(nums ...int) []string {
		out := []string{}
		for _, n := range nums {
			out = append(out, sampleNames[n-1])
		}
		sort.Strings(out)
		return out
	}
	cases := []struct {
		q    string
		want []string
		why  string
	}{
		{"דוח", at(1, 2, 7), "trigram: finds הדוחות (row 2) as well as the whole word"},
		{"בית", at(2, 3), "trigram: finds the attached form בבית (row 3)"},
		{"שלום", at(4, 5), "niqqud is folded: row 4 is שָׁלוֹם"},
		{"תקרא", at(), "control: no match"},
		{"تقرير", at(9, 11, 16), "trigram: the article form التقرير (row 16) matches the bare word"},
		{"التقرير", at(16), "the article form matches only its own name"},
		{"التقارير", at(10), "a different word is a different word"},
		{"reports", at(8), "no stemming: reports does not find Report 2026"},
		{"report", at(7, 8), "a substring of both names"},
		{"résumé", at(14, 15), "accents are folded"},
		{"2026", at(1, 7, 13), "digits are text"},
		{"דו", at(1, 7), "two letters: word prefix, so הדוחות is not found"},
	}
	for _, c := range cases {
		got := hitNames(t, fx.search(t, "q="+url.QueryEscape(c.q)))
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("q=%q: got %q, want %q (%s)", c.q, got, c.want, c.why)
		}
	}
	wantError(t, fx.search(t, "q=a"), http.StatusBadRequest, "invalid_request")
}

// TestSearchShowsNothingTheCallerCannotRead: a name the caller cannot reach gives the same answer as a name that does not
// exist, byte for byte, and no cursor.
func TestSearchShowsNothingTheCallerCannotRead(t *testing.T) {
	fx := newLabelFixture(t)
	writeFile(t, fx.s, fx.key, fx.fld.ID, "/classified-merger.txt", "x")
	writeFile(t, fx.s, fx.key, fx.fld.ID, "/a/b/shared-note.txt", "x")
	hidden := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/search?q=classified", nil)
	none := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/search?q=zzzz-nothing", nil)
	wantStatus(t, hidden, http.StatusOK)
	wantStatus(t, none, http.StatusOK)
	if hidden.Body.String() != none.Body.String() {
		t.Fatalf("an unreadable match answers differently from no match:\n%s\n%s", hidden.Body, none.Body)
	}
	got := hitNames(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/search?q=shared-note", nil))
	if strings.Join(got, "|") != "shared-note.txt" {
		t.Fatalf("editor sees %q, want its own shared file", got)
	}
}

// TestSearchHidesTrashedNodesUntilRestored: a trashed node is not found, and a restored one is.
func TestSearchHidesTrashedNodesUntilRestored(t *testing.T) {
	fx := newSearchFixture(t)
	id := nodeIDOf(t, fx.s, fx.acc.ID, teamRoot(t, fx), "resume.docx")
	ctx := context.Background()
	if err := fx.s.store.Write(ctx, func(tx *store.Tx) error {
		return tx.TrashNode(ctx, fx.acc.ID, id, fx.owner.ID, store.Now())
	}); err != nil {
		t.Fatal(err)
	}
	if got := hitNames(t, fx.search(t, "q=resume.docx")); len(got) != 0 {
		t.Fatalf("trashed node found: %q", got)
	}
	if err := fx.s.store.Write(ctx, func(tx *store.Tx) error { return tx.Untrash(ctx, fx.acc.ID, id) }); err != nil {
		t.Fatal(err)
	}
	if got := hitNames(t, fx.search(t, "q=resume.docx")); strings.Join(got, "|") != "resume.docx" {
		t.Fatalf("restored node: got %q", got)
	}
}

// teamRoot returns the root node of the team folder.
func teamRoot(t *testing.T, fx *searchFixture) string {
	t.Helper()
	root, err := fx.s.store.RootNode(context.Background(), fx.acc.ID, fx.fld.ID)
	if err != nil {
		t.Fatal(err)
	}
	return root.ID
}

// TestSearchDoesNotReadAnotherAccount: the same name in two accounts is found only in the caller's.
func TestSearchDoesNotReadAnotherAccount(t *testing.T) {
	fx := newSearchFixture(t)
	admin := adminKey(t, fx.s)
	other, otherKey := newAccount(t, fx.s, admin, "Other")
	ofld := newFolder(t, fx.s, otherKey, other.ID, "Team")
	putTyped(t, fx.s, otherKey, ofld.ID, "resume-other.docx", "text/plain", "x")
	// "resume" is a substring of both of the sample's names with it; the other account's file is not among them.
	if got := hitNames(t, fx.search(t, "q=resume")); strings.Join(got, "|") != "Résumé café.docx|resume.docx" {
		t.Fatalf("got %q; the other account's file must not appear", got)
	}
}

// TestSearchFollowsTheTokenScope: a token sees what its folder list reaches, and nothing outside it.
func TestSearchFollowsTheTokenScope(t *testing.T) {
	fx := newSearchFixture(t)
	other := newFolder(t, fx.s, fx.key, fx.acc.ID, "Other")
	putTyped(t, fx.s, fx.key, other.ID, "other-plan.txt", "text/plain", "x")
	tok := mintToken(t, fx.s, fx.key, fx.acc.ID, other.ID, fx.owner.ID)
	if got := hitNames(t, call(t, fx.s, "GET", "/api/v1/search?q=plan", tok, nil)); strings.Join(got, "|") != "other-plan.txt" {
		t.Fatalf("token in folder Other found %q", got)
	}
	if got := hitNames(t, call(t, fx.s, "GET", "/api/v1/search?q=report", tok, nil)); len(got) != 0 {
		t.Fatalf("token outside its folder found %q", got)
	}
}

// TestSearchValidatesItsParameters: each parameter out of bounds is invalid_request.
func TestSearchValidatesItsParameters(t *testing.T) {
	fx := newSearchFixture(t)
	bad := map[string]string{
		"no query and no filter": "",
		"query over 200 bytes":   "q=" + strings.Repeat("ab", 101),
		"nine terms":             "q=" + strings.Repeat("ab+", 8) + "ab",
		"term over 64 bytes":     "q=" + strings.Repeat("a", 65),
		"control character":      "q=ab%01cd",
		"content search":         "q=ab&in=content",
		"unknown in":             "q=ab&in=owner",
		"unknown kind":           "q=ab&kind=link",
		"ext with a dot":         "q=ab&ext=.pdf,x.y",
		"too many extensions":    "ext=a,b,c,d,e,f,g,h,i",
		"negative size":          "size_min=-1",
		"bad date":               "modified_after=soon",
		"limit zero":             "q=ab&limit=0",
		"limit over 50":          "q=ab&limit=51",
		"bad cursor":             "q=ab&cursor=***",
		"scope not an ID":        "q=ab&scope=bad!",
		"unknown label field":    "label=lbf_none:x",
		"label without a value":  "label=lbf_none",
		"too many labels":        "label=lbfone_x:1&label=lbftwo_x:1&label=lbfthree_x:1&label=lbffour_x:1&label=lbffive_x:1",
	}
	for name, query := range bad {
		rec := fx.search(t, query)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s (%s): status %d, want 400: %s", name, query, rec.Code, rec.Body)
		}
	}
	// A filter alone is a search: the kind lists every file the owner reaches.
	if got := hitNames(t, fx.search(t, "kind=file&limit=50")); len(got) != len(sampleNames) {
		t.Errorf("kind alone found %d, want %d", len(got), len(sampleNames))
	}
}

// TestSearchPagesWithinTheScanBudget: a page that the scan budget cuts short carries a cursor, even when it holds no hit,
// and the walk over the pages finds the one readable match, once.
func TestSearchPagesWithinTheScanBudget(t *testing.T) {
	fx := newLabelFixture(t)
	fx.s.cfg.Search.ScanBudget = 50
	for i := range 120 {
		writeFile(t, fx.s, fx.key, fx.fld.ID, fmt.Sprintf("/budget-%03d.txt", i), "x")
	}
	writeFile(t, fx.s, fx.key, fx.fld.ID, "/a/b/budget-visible.txt", "x")
	first := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/search?q=budget&limit=25", nil)
	wantStatus(t, first, http.StatusOK)
	page := decode[listResponse[nodeJSON]](t, first)
	if len(page.Items) != 0 || page.NextCursor == "" {
		t.Fatalf("first page: %d items, cursor %q; want a short empty page with a cursor", len(page.Items), page.NextCursor)
	}
	seen := map[string]int{}
	cursor := ""
	for range 10 {
		q := "/api/v1/search?q=budget&limit=25"
		if cursor != "" {
			q += "&cursor=" + url.QueryEscape(cursor)
		}
		p := decode[listResponse[nodeJSON]](t, callSession(t, fx.s, fx.annSess, "GET", q, nil))
		for _, it := range p.Items {
			seen[it.Name]++
		}
		if cursor = p.NextCursor; cursor == "" {
			break
		}
	}
	if len(seen) != 1 || seen["budget-visible.txt"] != 1 {
		t.Fatalf("walk found %v; want budget-visible.txt once", seen)
	}
}

// TestSearchPagesWithoutTotals: a full walk returns each match once, and no response names a total.
func TestSearchPagesWithoutTotals(t *testing.T) {
	fx := newSearchFixture(t)
	for i := range 60 {
		putTyped(t, fx.s, fx.key, fx.fld.ID, fmt.Sprintf("batch-%02d.txt", i), "text/plain", "x")
	}
	seen := map[string]bool{}
	cursor := ""
	for range 10 {
		q := "q=batch&limit=25"
		if cursor != "" {
			q += "&cursor=" + url.QueryEscape(cursor)
		}
		rec := fx.search(t, q)
		wantStatus(t, rec, http.StatusOK)
		if strings.Contains(rec.Body.String(), "total") {
			t.Fatalf("a response names a total: %s", rec.Body)
		}
		p := decode[listResponse[nodeJSON]](t, rec)
		for _, it := range p.Items {
			if seen[it.Name] {
				t.Fatalf("%s returned twice", it.Name)
			}
			seen[it.Name] = true
		}
		if cursor = p.NextCursor; cursor == "" {
			break
		}
	}
	if len(seen) != 60 {
		t.Fatalf("walk returned %d names; want 60", len(seen))
	}
}

// TestSearchRestrictedLabelMatchesOnlyAManager: a restricted label predicate matches for a manager of the node. For anyone
// else the answer is the same as when the value is not set, byte for byte.
func TestSearchRestrictedLabelMatchesOnlyAManager(t *testing.T) {
	fx := newLabelFixture(t)
	writeFile(t, fx.s, fx.key, fx.fld.ID, "/a/b/lead-note.txt", "x")
	nodeID := nodeIDOf(t, fx.s, fx.acc.ID, fx.b.ID, "lead-note.txt")
	query := "/api/v1/search?q=lead-note&label=" + fx.lead + ":" + fx.owner.ID
	before := callSession(t, fx.s, fx.annSess, "GET", query, nil)
	setLabel(t, fx.keyDo, nodeID, fx.lead, fx.owner.ID, http.StatusNoContent)
	after := callSession(t, fx.s, fx.annSess, "GET", query, nil)
	if before.Body.String() != after.Body.String() {
		t.Fatalf("an editor's answer changed with a restricted value:\n%s\n%s", before.Body, after.Body)
	}
	if got := hitNames(t, callSession(t, fx.s, fx.ownerSess, "GET", query, nil)); strings.Join(got, "|") != "lead-note.txt" {
		t.Fatalf("manager found %q, want lead-note.txt", got)
	}
}

// rootOfLabel returns the root of a label fixture's team folder.
func rootOfLabel(t *testing.T, fx *labelFixture) string {
	t.Helper()
	root, err := fx.s.store.RootNode(context.Background(), fx.acc.ID, fx.fld.ID)
	if err != nil {
		t.Fatal(err)
	}
	return root.ID
}

// TestSearchLabelFilters covers the typed predicates: a text prefix, and number and date ranges.
func TestSearchLabelFilters(t *testing.T) {
	fx := newLabelFixture(t)
	writeFile(t, fx.s, fx.key, fx.fld.ID, "/a/b/priced.txt", "x")
	nodeID := nodeIDOf(t, fx.s, fx.acc.ID, fx.b.ID, "priced.txt")
	setLabel(t, fx.keyDo, nodeID, fx.note, "Hello world", http.StatusNoContent)
	setLabel(t, fx.keyDo, nodeID, fx.cost, 5, http.StatusNoContent)
	cases := map[string]bool{
		"label=" + fx.note + ":Hel":                   true,
		"label=" + fx.note + ":hel":                   false, // the stored value is case-sensitive
		"label=" + fx.cost + ":1..9":                  true,
		"label=" + fx.cost + ":6..9":                  false,
		"label=" + fx.due + ":2020-01-01..2030-01-01": false,
	}
	for query, want := range cases {
		got := hitNames(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/search?q=priced&"+query, nil))
		if (len(got) == 1) != want {
			t.Errorf("%s: found %q, want found=%v", query, got, want)
		}
	}
}

// TestSearchRateLimit: the 61st search of a minute is refused.
func TestSearchRateLimit(t *testing.T) {
	fx := newSearchFixture(t)
	for i := range 60 {
		if rec := fx.search(t, "q=report"); rec.Code != http.StatusOK {
			t.Fatalf("search %d: status %d", i+1, rec.Code)
		}
	}
	wantError(t, fx.search(t, "q=report"), http.StatusTooManyRequests, "rate_limited")
}

// TestSearchIsOffWhenConfiguredOff: the route answers not_found.
func TestSearchIsOffWhenConfiguredOff(t *testing.T) {
	fx := newSearchFixture(t, func(c *config.Config) { c.Search.Enabled = false })
	wantError(t, fx.search(t, "q=report"), http.StatusNotFound, "not_found")
}

// TestSearchDeadlineAnswersUnavailable: a search past its deadline is refused, not answered with what it had.
func TestSearchDeadlineAnswersUnavailable(t *testing.T) {
	fx := newSearchFixture(t, func(c *config.Config) { c.Search.Timeout = config.Duration(1) })
	wantError(t, fx.search(t, "q=report"), http.StatusServiceUnavailable, "unavailable")
}

// TestSmallCopyIsSearchableAtOnce: a copy of at most store.SearchSmallCopy nodes is indexed in the copy's own write, so its
// names are found at once and nothing is queued for the search job.
func TestSmallCopyIsSearchableAtOnce(t *testing.T) {
	fx := newSearchFixture(t)
	mkdirs(t, fx.s, fx.key, fx.fld.ID, "/pkg")
	writeFile(t, fx.s, fx.key, fx.fld.ID, "/pkg/copy-marker.txt", "x")
	pkg := nodeIDOf(t, fx.s, fx.acc.ID, teamRoot(t, fx), "pkg")
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+pkg+"/copy", fx.key, map[string]any{"name": "pkg-two"})
	if rec.Code != http.StatusCreated && rec.Code != http.StatusAccepted {
		t.Fatalf("copy: status %d: %s", rec.Code, rec.Body)
	}
	if got := hitNames(t, fx.search(t, "q=copy-marker")); len(got) != 2 {
		t.Fatalf("after the copy, found %q; want the original and the copy", got)
	}
	left, err := fx.s.store.SearchBacklogLeft(context.Background())
	if err != nil || left != 0 {
		t.Fatalf("backlog = %d, %v after a small copy; want 0", left, err)
	}
}

// TestLargeCopyAppearsAfterTheIndexJob: a copy above the small limit records its rowid range, and its names are found only
// after the search job has indexed the range.
func TestLargeCopyAppearsAfterTheIndexJob(t *testing.T) {
	fx := newSearchFixture(t)
	mkdirs(t, fx.s, fx.key, fx.fld.ID, "/pkg")
	writeFile(t, fx.s, fx.key, fx.fld.ID, "/pkg/copy-marker.txt", "x")
	for i := range 120 {
		writeFile(t, fx.s, fx.key, fx.fld.ID, "/pkg/filler-"+strconv.Itoa(i)+".txt", "x")
	}
	pkg := nodeIDOf(t, fx.s, fx.acc.ID, teamRoot(t, fx), "pkg")
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+pkg+"/copy", fx.key, map[string]any{"name": "pkg-two"})
	if rec.Code != http.StatusCreated && rec.Code != http.StatusAccepted {
		t.Fatalf("copy: status %d: %s", rec.Code, rec.Body)
	}
	if got := hitNames(t, fx.search(t, "q=copy-marker")); len(got) != 1 {
		t.Fatalf("before the job, found %q; want only the original (the copy's range is queued)", got)
	}
	ctx := context.Background()
	if err := fx.s.jobs.RunPending(ctx); err != nil {
		t.Fatal(err)
	}
	if got := hitNames(t, fx.search(t, "q=copy-marker")); len(got) != 2 {
		t.Fatalf("after the job, found %q; want the original and the copy", got)
	}
}
