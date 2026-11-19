// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// changesReply is a feed answer with the given cursor, has_more and entries.
func changesReply(cursor string, hasMore bool, entries ...map[string]any) reply {
	if entries == nil {
		entries = []map[string]any{}
	}
	return reply{status: http.StatusOK, body: jsonOf(map[string]any{
		"changes": entries, "cursor": cursor, "has_more": hasMore,
	})}
}

// liveEntry is a feed entry carrying a node.
func liveEntry(nodeID, name string, relist bool) map[string]any {
	return map[string]any{"node_id": nodeID, "removed": false, "relist": relist, "node": node(nodeID, name, "file")}
}

// removedEntry is a feed entry with no node.
func removedEntry(nodeID string) map[string]any {
	return map[string]any{"node_id": nodeID, "removed": true, "relist": false, "node": nil}
}

// TestChangesStartPrintAndCursor: without a cursor the command starts at /changes/start, reads one page, prints the
// entries and the cursor last.
func TestChangesStartPrintAndCursor(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", "/api/v1/changes/start", reply{status: http.StatusOK, body: `{"cursor":"c1_start"}`})
	f.on("GET", "/api/v1/changes", changesReply("c1_next", false, liveEntry(testNode, "report.txt", false)))
	res := f.run(Changes, "--space", testFolder)
	wantCode(t, res, 0)
	f.wantCalls(
		"GET /api/v1/changes/start?space="+testFolder,
		"GET /api/v1/changes?cursor=c1_start&limit=200&space="+testFolder,
	)
	if !strings.HasSuffix(res.stdout, "cursor c1_next\n") {
		t.Errorf("stdout does not end with the cursor line: %q", res.stdout)
	}
	if !strings.Contains(res.stdout, testNode) || !strings.Contains(res.stdout, "report.txt") {
		t.Errorf("stdout misses the entry: %q", res.stdout)
	}
}

// TestChangesFlags: a given cursor skips the start call; space and limit go on the query.
func TestChangesFlags(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", "/api/v1/changes", changesReply("c1_next", false, liveEntry(testNode, "x.txt", false)))
	res := f.run(Changes, "--space", testFolder, "--cursor", "c1_given", "--limit", "5")
	wantCode(t, res, 0)
	f.wantCalls("GET /api/v1/changes?cursor=c1_given&limit=5&space=" + testFolder)
}

// TestChangesAccountFeed: with no space the command reads the account feed, starting at /changes/start with no query.
func TestChangesAccountFeed(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", "/api/v1/changes/start", reply{status: http.StatusOK, body: `{"cursor":"c1_acct"}`})
	f.on("GET", "/api/v1/changes", changesReply("c1_next", false))
	res := f.run(Changes)
	wantCode(t, res, 0)
	f.wantCalls(
		"GET /api/v1/changes/start",
		"GET /api/v1/changes?cursor=c1_acct&limit=200",
	)
	if !strings.HasSuffix(res.stdout, "cursor c1_next\n") {
		t.Errorf("stdout does not end with the cursor line: %q", res.stdout)
	}
}

// TestChangesRemovedAndRelist: a removed entry and a relist entry print their state.
func TestChangesRemovedAndRelist(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", "/api/v1/changes/start", reply{status: http.StatusOK, body: `{"cursor":"c1_start"}`})
	f.on("GET", "/api/v1/changes", changesReply("c1_next", false,
		removedEntry(testNode), liveEntry(testOther, "dir", true)))
	res := f.run(Changes, "--space", testFolder)
	wantCode(t, res, 0)
	if !strings.Contains(res.stdout, "removed") {
		t.Errorf("stdout misses the removed state: %q", res.stdout)
	}
	if !strings.Contains(res.stdout, "relist") {
		t.Errorf("stdout misses the relist state: %q", res.stdout)
	}
}

// TestChangesJSON: --json prints the API's own document.
func TestChangesJSON(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", "/api/v1/changes/start", reply{status: http.StatusOK, body: `{"cursor":"c1_start"}`})
	f.on("GET", "/api/v1/changes", changesReply("c1_next", false, liveEntry(testNode, "x.txt", false)))
	res := f.run(Changes, "--space", testFolder, "--json")
	wantCode(t, res, 0)
	var doc struct {
		Changes []map[string]any `json:"changes"`
		Cursor  string           `json:"cursor"`
		HasMore bool             `json:"has_more"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &doc); err != nil {
		t.Fatalf("stdout is not the API document: %q: %v", res.stdout, err)
	}
	if doc.Cursor != "c1_next" || len(doc.Changes) != 1 {
		t.Errorf("document = %+v, want the feed answer", doc)
	}
}

// TestChangesGoneMessage: a 410 gone answer prints a plain message and exits 1, in plain and in JSON mode alike.
func TestChangesGoneMessage(t *testing.T) {
	gone := reply{status: http.StatusGone,
		body: `{"error":{"code":"gone","message":"the cursor is too old; start again","details":{"reason":"reset"}}}`}
	for _, mode := range []string{"plain", "json"} {
		t.Run(mode, func(t *testing.T) {
			isolate(t, testAccountKey)
			f := newFake(t)
			f.on("GET", "/api/v1/changes/start", reply{status: http.StatusOK, body: `{"cursor":"c1_start"}`})
			f.on("GET", "/api/v1/changes", gone)
			args := []string{"--space", testFolder}
			if mode == "json" {
				args = append(args, "--json")
			}
			res := f.run(Changes, args...)
			wantCode(t, res, 1)
			if res.stdout != "" {
				t.Errorf("stdout = %q, want nothing", res.stdout)
			}
			if !strings.Contains(res.stderr, "start again") {
				t.Errorf("stderr misses the message: %q", res.stderr)
			}
			if strings.HasPrefix(strings.TrimSpace(res.stderr), "{") {
				t.Errorf("the gone message is JSON, want plain: %q", res.stderr)
			}
		})
	}
}

// TestChangesBadFlags: out-of-range limit, a malformed space and a positional argument are usage errors before any call.
func TestChangesBadFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--limit", "0"},
		{"--limit", "1001"},
		{"--limit", "-1"},
		{"--space", "not-an-id"},
		{"extra"},
	} {
		isolate(t, testAccountKey)
		f := newFake(t)
		res := f.run(Changes, args...)
		wantCode(t, res, 2)
		f.wantNoCalls()
	}
}

// lockedBuffer is a bytes.Buffer safe to read while the command writes it, so the test can watch the output.
type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// TestChangesFollowStopsOnInterrupt: --follow long-polls with wait=60, prints each page, and ends cleanly when its
// context is cancelled, as Interrupt does. The third poll holds until the interrupt, as a wait with no change does.
func TestChangesFollowStopsOnInterrupt(t *testing.T) {
	isolate(t, testAccountKey)
	ctx, cancel := context.WithCancel(context.Background())
	saved := followContext
	followContext = func() (context.Context, context.CancelFunc) { return ctx, cancel }
	t.Cleanup(func() { followContext = saved; cancel() })

	var mu sync.Mutex
	changesCalls := 0
	var waits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/changes/start":
			_, _ = io.WriteString(w, `{"cursor":"c1_a"}`)
		case "/api/v1/changes":
			mu.Lock()
			changesCalls++
			n := changesCalls
			waits = append(waits, r.URL.Query().Get("wait"))
			mu.Unlock()
			switch n {
			case 1:
				_, _ = io.WriteString(w, jsonOf(map[string]any{
					"changes": []map[string]any{liveEntry(testNode, "one.txt", false)}, "cursor": "c1_b", "has_more": false}))
			case 2:
				_, _ = io.WriteString(w, jsonOf(map[string]any{
					"changes": []map[string]any{liveEntry(testOther, "two.txt", false)}, "cursor": "c1_c", "has_more": false}))
			default:
				// A long poll with no change holds until the follow is interrupted.
				<-r.Context().Done()
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	var out, errOut lockedBuffer
	done := make(chan int, 1)
	go func() { done <- Changes([]string{"--space", testFolder, "--follow", "--url", srv.URL}, &out, &errOut) }()
	// Interrupt once the second page is printed, as a signal arriving between polls would.
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), "cursor c1_c") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	code := <-done
	if code != 0 {
		t.Fatalf("exit %d, want 0; stdout %q stderr %q", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "one.txt") || !strings.Contains(out.String(), "two.txt") {
		t.Errorf("both pages were not printed: %q", out.String())
	}
	if !strings.Contains(out.String(), "cursor c1_c") {
		t.Errorf("the last cursor was not printed: %q", out.String())
	}
	// Every follow read carried wait=60, and at least the two pages were read.
	mu.Lock()
	defer mu.Unlock()
	if changesCalls < 2 {
		t.Errorf("changes calls = %d, want at least 2", changesCalls)
	}
	for _, w := range waits {
		if w != "60" {
			t.Errorf("a follow read carried wait=%q, want 60", w)
		}
	}
}
