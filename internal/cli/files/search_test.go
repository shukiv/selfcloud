// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
)

// searchHit is a search result as the API answers it.
func searchHit(nodeID, name string) map[string]any {
	h := node(nodeID, name, "file")
	h["size"] = 12
	h["place"] = "Docs"
	h["snippet"] = map[string]any{"text": "line\nwith \x1b needle", "ranges": [][2]int{{9, 15}}}
	return h
}

func TestSearchQuery(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on(http.MethodGet, "/api/v1/search", page("", searchHit(testNode, "a.txt")))
	q := url.Values{}
	q.Set("q", "needle")
	q.Set("in", "content")
	q.Set("kind", "file")
	q.Set("type", "pdf")
	q.Set("ext", "txt,pdf,docx")
	q.Set("modified_after", "1793491200000")
	q.Set("modified_before", "1796083200000")
	q.Set("size_min", "67108864")
	q.Set("size_max", "2147483648")
	q.Add("label", testFolder+":one")
	q.Add("label", testFolder+":two")
	q.Set("scope", testDest)
	q.Set("limit", "10")
	res := f.run(Search, "needle", "--in", "content", "--kind", "file", "--type", "pdf",
		"--ext", "txt,pdf", "--ext", "docx",
		"--modified-after", "2026-11-01T00:00:00Z", "--modified-before", "2026-12-01T00:00:00Z",
		"--size-min", "64MiB", "--size-max", "2GiB",
		"--label", testFolder+":one", "--label", testFolder+":two",
		"--scope", testDest, "--limit", "10")
	wantCode(t, res, 0)
	f.wantCalls("GET /api/v1/search?" + q.Encode())
	want := table(
		[]string{"ID", "KIND", "SIZE", "MODIFIED", "PLACE", "NAME", "MATCH"},
		[]string{testNode, "file", "12", "2026-11-02T10:00:00Z", "Docs", "a.txt", "line with needle"},
	)
	if res.stdout != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", res.stdout, want)
	}
	if res.stderr != "" {
		t.Fatalf("stderr %q; want none", res.stderr)
	}
}

func TestSearchOnlyFilterNeedsNoQuery(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on(http.MethodGet, "/api/v1/search", page("", searchHit(testNode, "a.txt")))
	res := f.run(Search, "--kind", "file")
	wantCode(t, res, 0)
	f.wantCalls("GET /api/v1/search?kind=file")
}

func TestSearchJSON(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on(http.MethodGet, "/api/v1/search", page("", searchHit(testNode, "a.txt")))
	res := f.run(Search, "needle", "--json")
	wantCode(t, res, 0)
	var items []map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &items); err != nil {
		t.Fatalf("stdout %q is not a JSON list: %v", res.stdout, err)
	}
	if len(items) != 1 || items[0]["place"] != "Docs" {
		t.Fatalf("items %v; want one hit with its place", items)
	}
	if _, ok := items[0]["snippet"]; !ok {
		t.Fatalf("items %v; want the API's snippet", items)
	}
}

func TestSearchMoreAndAll(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on(http.MethodGet, "/api/v1/search", page("c2", searchHit(testNode, "a.txt")))
	res := f.run(Search, "needle")
	wantCode(t, res, 0)
	f.wantCalls("GET /api/v1/search?q=needle")
	if !strings.Contains(res.stderr, "more items exist; --all prints every page") {
		t.Fatalf("stderr %q; want the more-items note", res.stderr)
	}

	f = newFake(t)
	f.on(http.MethodGet, "/api/v1/search",
		page("c2", searchHit(testNode, "a.txt")),
		page("", searchHit(testOther, "b.txt")))
	res = f.run(Search, "needle", "--all")
	wantCode(t, res, 0)
	f.wantCalls("GET /api/v1/search?q=needle", "GET /api/v1/search?cursor=c2&q=needle")
	if res.stderr != "" {
		t.Fatalf("stderr %q; want no more-items note after the last page", res.stderr)
	}
	if !strings.Contains(res.stdout, "a.txt") || !strings.Contains(res.stdout, "b.txt") {
		t.Fatalf("stdout %q; want both pages", res.stdout)
	}
}

func TestSearchUsage(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"nothing", nil},
		{"in content without a query", []string{"--in", "content"}},
		{"in name alone", []string{"--in", "name"}},
		{"bad in", []string{"q", "--in", "body"}},
		{"bad kind", []string{"q", "--kind", "person"}},
		{"five labels", []string{"q", "--label", "f:a", "--label", "f:b", "--label", "f:c", "--label", "f:d", "--label", "f:e"}},
		{"label without a value", []string{"q", "--label", "field"}},
		{"nine extensions", []string{"q", "--ext", "a,b,c,d,e,f,g,h,i"}},
		{"limit zero", []string{"q", "--limit", "0"}},
		{"limit too big", []string{"q", "--limit", "51"}},
		{"bad time", []string{"q", "--modified-after", "tomorrow"}},
		{"time before 1970", []string{"q", "--modified-before", "1969-12-31T23:59:59Z"}},
		{"bad size", []string{"q", "--size-min", "-1"}},
		{"bad scope", []string{"q", "--scope", "folder"}},
		{"two queries", []string{"one", "two"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolate(t, testDeviceTok)
			f := newFake(t)
			res := f.run(Search, tt.args...)
			wantCode(t, res, 2)
			f.wantNoCalls()
			if !strings.HasPrefix(res.stderr, "selfcloud: ") {
				t.Fatalf("stderr %q; want a usage message", res.stderr)
			}
		})
	}
}

func TestSearchSnippetIsOneSafeLine(t *testing.T) {
	got := searchMatch(&searchSnippet{Text: strings.Repeat("é", 90)})
	if n := len([]rune(got)); n != 80 {
		t.Fatalf("match has %d runes; want 80", n)
	}
	if got != strings.Repeat("é", 80) {
		t.Fatalf("match %q; want the first 80 characters", got)
	}
}

func TestTagSinglePendingThenReady(t *testing.T) {
	isolate(t, testDeviceTok)
	old := tagPollWait
	tagPollWait = func(int, time.Duration) time.Duration { return 0 }
	t.Cleanup(func() { tagPollWait = old })
	f := newFake(t)
	f.on(http.MethodGet, nodePath(testNode, "/tags"),
		reply{status: http.StatusAccepted, body: `{"state":"pending"}`},
		reply{status: http.StatusOK, body: jsonOf(map[string]any{
			"state": "ready", "title": "Song", "artist": "Band", "album": "Record", "duration_ms": 65000,
		})})
	res := f.run(TagLs, testNode)
	wantCode(t, res, 0)
	f.wantCalls("GET "+nodePath(testNode, "/tags"), "GET "+nodePath(testNode, "/tags"))
	want := table([]string{"NODE", "STATE", "TITLE", "ARTIST", "ALBUM", "DURATION"},
		[]string{testNode, "ready", "Song", "Band", "Record", "1:05"})
	if res.stdout != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", res.stdout, want)
	}
}

func TestTagSinglePendingStopsAtSix(t *testing.T) {
	isolate(t, testDeviceTok)
	old := tagPollWait
	tagPollWait = func(int, time.Duration) time.Duration { return 0 }
	t.Cleanup(func() { tagPollWait = old })
	f := newFake(t)
	f.on(http.MethodGet, nodePath(testNode, "/tags"), reply{status: http.StatusAccepted, body: `{"state":"pending"}`})
	res := f.run(TagLs, testNode)
	wantCode(t, res, 0)
	calls := f.requests()
	if len(calls) != tagMaxAsks {
		t.Fatalf("calls %v; want %d", calls, tagMaxAsks)
	}
	if !hasRow(res.stdout, testNode, "pending") {
		t.Fatalf("stdout %q; want the pending row", res.stdout)
	}
}

func TestTagSingleMissing(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	res := f.run(TagLs, testNode, "--json")
	wantCode(t, res, 4)
	f.wantCalls("GET " + nodePath(testNode, "/tags"))
	if strings.TrimSpace(res.stdout) != "{}" {
		t.Fatalf("stdout %q; want an empty object", res.stdout)
	}
	if !strings.Contains(res.stderr, "no tags for "+testNode) {
		t.Fatalf("stderr %q; want the missing note", res.stderr)
	}
}

func TestTagBatchAsksAgainOnlyForPending(t *testing.T) {
	isolate(t, testDeviceTok)
	old := tagPollWait
	tagPollWait = func(int, time.Duration) time.Duration { return 0 }
	t.Cleanup(func() { tagPollWait = old })
	f := newFake(t)
	f.on(http.MethodGet, "/api/v1/media/tags",
		reply{status: http.StatusOK, body: jsonOf(map[string]any{"items": map[string]any{
			testNode:  map[string]any{"state": "pending"},
			testOther: map[string]any{"state": "ready", "title": "Other"},
		}})},
		reply{status: http.StatusOK, body: jsonOf(map[string]any{"items": map[string]any{
			testNode: map[string]any{"state": "ready", "title": "Node"},
		}})})
	res := f.run(TagLs, testNode, testOther)
	wantCode(t, res, 0)
	f.wantCalls(
		"GET /api/v1/media/tags?ids="+testNode+"%2C"+testOther,
		"GET /api/v1/media/tags?ids="+testNode,
	)
	if !hasRow(res.stdout, testNode, "ready") || !hasRow(res.stdout, testOther, "ready") {
		t.Fatalf("stdout %q; want both rows", res.stdout)
	}
}

func TestTagBatchMissingIsPartial(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on(http.MethodGet, "/api/v1/media/tags", reply{status: http.StatusOK, body: jsonOf(map[string]any{
		"items": map[string]any{testNode: map[string]any{"state": "ready"}},
	})})
	res := f.run(TagLs, testNode, testOther)
	wantCode(t, res, 7)
	if !strings.Contains(res.stderr, "no tags for "+testOther) {
		t.Fatalf("stderr %q; want the missing note", res.stderr)
	}
}

func TestTagBatchAllMissing(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on(http.MethodGet, "/api/v1/media/tags", reply{status: http.StatusOK, body: `{"items":{}}`})
	res := f.run(TagLs, testNode, testOther, "--json")
	wantCode(t, res, 4)
	if strings.TrimSpace(res.stdout) != "{}" {
		t.Fatalf("stdout %q; want an empty object", res.stdout)
	}
}

func TestTagJSONUsesAView(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on(http.MethodGet, nodePath(testNode, "/tags"), reply{status: http.StatusOK, body: jsonOf(map[string]any{
		"state": "ready", "title": "Song", "artist": "Band", "album": "Record", "duration_ms": 1000,
		"extra": "not printed",
	})})
	res := f.run(TagLs, testNode, "--json")
	wantCode(t, res, 0)
	var got map[string]map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &got); err != nil {
		t.Fatalf("stdout %q is not an object: %v", res.stdout, err)
	}
	if got[testNode]["title"] != "Song" || got[testNode]["duration_ms"] != float64(1000) {
		t.Fatalf("object %v; want the tag view", got)
	}
	if _, ok := got[testNode]["extra"]; ok {
		t.Fatalf("object %v; want only printed fields", got)
	}
}

func TestTagIDsAreCheckedAndDeduplicated(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	res := f.run(TagLs, "not-an-id", testNode)
	wantCode(t, res, 2)
	f.wantNoCalls()

	f = newFake(t)
	f.on(http.MethodGet, nodePath(testNode, "/tags"), reply{status: http.StatusOK, body: `{"state":"ready"}`})
	res = f.run(TagLs, testNode, testNode)
	wantCode(t, res, 0)
	f.wantCalls("GET " + nodePath(testNode, "/tags"))
}

func TestTagTooManyIDs(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	ids := make([]string, 101)
	for i := range ids {
		ids[i] = id.Derive("nod", "cli11e-tag-"+strconv.Itoa(i))
	}
	res := f.run(TagLs, ids...)
	wantCode(t, res, 2)
	f.wantNoCalls()
}

func TestTagRetryAfter(t *testing.T) {
	h := http.Header{"Retry-After": {"3"}}
	if got := tagRetryAfter(h); got != 3*time.Second {
		t.Fatalf("Retry-After 3 gave %v", got)
	}
	if got := tagRetryAfter(http.Header{"Retry-After": {"soon"}}); got != 0 {
		t.Fatalf("a bad Retry-After gave %v; want zero", got)
	}
	if got := defaultTagPollWait(1, 60*time.Second); got != tagPollCap {
		t.Fatalf("a long Retry-After gave %v; want the cap", got)
	}
}

func TestTagDuration(t *testing.T) {
	for ms, want := range map[int64]string{0: "", 1000: "0:01", 65000: "1:05", 3600000: "60:00"} {
		if got := tagDuration(ms); got != want {
			t.Errorf("tagDuration(%d) = %q, want %q", ms, got, want)
		}
	}
}
