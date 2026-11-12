// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
)

// The zip commands are checked here against a fake that answers as the zip routes do: 202 while a zip is being made, a
// ranged answer with a strong ETag when it is ready, and 404 for a zip that is gone or failed. The same commands are
// checked against the real server in zip_live_test.go.

var (
	testZip   = id.Derive("zip", "cli12-zip")
	testZipB  = id.Derive("zip", "cli12-zip-b")
	testJob   = id.Derive("job", "cli12-job")
	testNodeB = id.Derive("nod", "cli12-node-b")
)

// zreq is one request the zip fake saw.
type zreq struct {
	method, path, query string
	rng, ifRange        string
	body                string
	auth                string
}

// zipFake is a test server whose answers a test writes as a function of the request.
type zipFake struct {
	t    *testing.T
	srv  *httptest.Server
	mu   sync.Mutex
	reqs []zreq
	h    func(w http.ResponseWriter, r *http.Request, n int)
}

// newZipFake starts a server. n is the 1-based count of requests so far to the same method and path.
func newZipFake(t *testing.T, h func(w http.ResponseWriter, r *http.Request, n int)) *zipFake {
	t.Helper()
	f := &zipFake{t: t, h: h}
	counts := map[string]int{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.reqs = append(f.reqs, zreq{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery,
			rng: r.Header.Get("Range"), ifRange: r.Header.Get("If-Range"), body: string(body),
			auth: r.Header.Get("Authorization")})
		key := r.Method + " " + r.URL.Path
		counts[key]++
		n := counts[key]
		f.mu.Unlock()
		f.h(w, r, n)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *zipFake) seen() []zreq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]zreq(nil), f.reqs...)
}

// calls returns the requests as "METHOD /path".
func (f *zipFake) calls() []string {
	var out []string
	for _, r := range f.seen() {
		out = append(out, r.method+" "+r.path)
	}
	return out
}

func (f *zipFake) run(fn func([]string, io.Writer, io.Writer) int, args ...string) result {
	f.t.Helper()
	var out, errOut bytes.Buffer
	code := fn(append(append([]string{}, args...), "--url", f.srv.URL), &out, &errOut)
	return result{code: code, stdout: out.String(), stderr: errOut.String()}
}

func writeDoc(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, jsonOf(v))
}

func writeErr(w http.ResponseWriter, status int, code, reason string) {
	details := map[string]string{}
	if reason != "" {
		details["reason"] = reason
	}
	writeDoc(w, status, map[string]any{"error": map[string]any{"code": code, "message": "the server says " + code,
		"details": details}})
}

// jobDoc is the answer to a zip request.
func jobDoc(zip string) map[string]any {
	return map[string]any{"id": testJob, "kind": "node.zip", "target": zip, "status": "queued",
		"created_at": "2026-11-12T10:00:00Z", "updated_at": "2026-11-12T10:00:00Z", "finished_at": nil}
}

// serveZipFile answers as a ready zip does: a strong ETag that is the SHA-256, a name, the left-out count, and ranges.
func serveZipFile(w http.ResponseWriter, r *http.Request, data []byte, name string, skipped int) {
	w.Header().Set("ETag", `"`+sha(data)+`"`)
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("X-Selfcloud-Skipped", itoa(skipped))
	switch {
	case strings.Contains(name, "/"):
		// A name that cannot be written as an extended value is sent as a plain one, as a hostile server would.
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	case name != "":
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"; filename*=UTF-8''`+name)
	}
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func pending(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "5")
	writeDoc(w, http.StatusAccepted, map[string]string{"state": "pending"})
}

// noZipWait makes the polling repeat at once, for the length of the test.
func noZipWait(t *testing.T) {
	t.Helper()
	old := zipPollWait
	t.Cleanup(func() { zipPollWait = old })
	zipPollWait = func(int) time.Duration { return 0 }
}

// ---- zip create ----

// TestZipCreateSendsTheNodesAndPrintsTheZip: the request is POST /zips with the node ids in order, and the zip's id is
// the job's target.
func TestZipCreateSendsTheNodesAndPrintsTheZip(t *testing.T) {
	isolate(t, testAccountKey)
	f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) { writeDoc(w, http.StatusAccepted, jobDoc(testZip)) })
	res := f.run(ZipCreate, testNode, testNodeB)
	wantCode(t, res, 0)
	if res.stdout != "zip="+testZip+"\n" {
		t.Errorf("stdout %q", res.stdout)
	}
	reqs := f.seen()
	if len(reqs) != 1 || reqs[0].method != "POST" || reqs[0].path != "/api/v1/zips" || reqs[0].auth == "" {
		t.Fatalf("requests %+v", reqs)
	}
	var body struct {
		Nodes []string `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(reqs[0].body), &body); err != nil || len(body.Nodes) != 2 ||
		body.Nodes[0] != testNode || body.Nodes[1] != testNodeB {
		t.Errorf("body %q", reqs[0].body)
	}
}

// TestZipCreateJSONPrintsTheAnswer: --json prints the API's document.
func TestZipCreateJSONPrintsTheAnswer(t *testing.T) {
	isolate(t, testAccountKey)
	f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) { writeDoc(w, http.StatusAccepted, jobDoc(testZip)) })
	res := f.run(ZipCreate, testNode, "--json")
	wantCode(t, res, 0)
	var doc map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &doc); err != nil || doc["target"] != testZip || doc["id"] != testJob ||
		doc["kind"] != "node.zip" {
		t.Errorf("stdout %q: %v", res.stdout, err)
	}
}

// TestZipCreateResolvesPaths: a path is walked in --folder to a node id, as get does, and ids and paths mix.
func TestZipCreateResolvesPaths(t *testing.T) {
	isolate(t, testAccountKey)
	f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) {
		switch {
		case r.Method == "GET" && r.URL.Path == folderPath():
			writeDoc(w, 200, map[string]any{"items": []map[string]any{node(testOther, "Reports", "dir"),
				node(testDest, "other", "file")}})
		case r.Method == "POST" && r.URL.Path == "/api/v1/zips":
			writeDoc(w, http.StatusAccepted, jobDoc(testZip))
		}
	})
	res := f.run(ZipCreate, "/reports", testNode, "--folder", testFolder)
	wantCode(t, res, 0)
	reqs := f.seen()
	if len(reqs) != 2 || reqs[0].query != "path=%2F" {
		t.Fatalf("requests %+v", reqs)
	}
	if !strings.Contains(reqs[1].body, `"nodes":["`+testOther+`","`+testNode+`"]`) {
		t.Errorf("body %q", reqs[1].body)
	}
}

// TestZipCreateRefusesBadInputBeforeAnyCall: nothing is sent for a missing, malformed or repeated node, or a path with no
// folder, or a folder with no path.
func TestZipCreateRefusesBadInputBeforeAnyCall(t *testing.T) {
	for name, args := range map[string][]string{
		"no node":             {},
		"not an id":           {"nope"},
		"a folder id":         {testFolder},
		"a path, no folder":   {"/a"},
		"a folder, no path":   {testNode, "--folder", testFolder},
		"the same node twice": {testNode, testNode},
		"a bad path name":     {"/a/../b", "--folder", testFolder},
		"the root":            {"/", "--folder", testFolder},
		"a bad wait limit":    {testNode, "--wait", "--wait-timeout", "0s"},
		"a wait limit alone":  {testNode, "--wait-timeout", "5s"},
	} {
		t.Run(name, func(t *testing.T) {
			isolate(t, testAccountKey)
			f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) { writeDoc(w, 202, jobDoc(testZip)) })
			wantCode(t, f.run(ZipCreate, args...), 2)
			if c := f.calls(); len(c) != 0 {
				t.Errorf("calls %v", c)
			}
		})
	}
}

// TestZipCreateErrors: the server's refusals keep their exit codes and say what the server said.
func TestZipCreateErrors(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status       int
		code, reason string
		exit         int
	}{
		{"a node that is not there", 404, "not_found", "", 4},
		{"a download turned off", 403, "download_restricted", "", 3},
		{"too many unfinished zips", 409, "conflict", "too_many_zips", 3},
		{"a shortcut", 400, "invalid_request", "shortcut", 2},
		{"too many nodes", 400, "invalid_request", "", 2},
		{"rate limited", 429, "rate_limited", "", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t, testAccountKey)
			f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) { writeErr(w, tc.status, tc.code, tc.reason) })
			res := f.run(ZipCreate, testNode)
			wantCode(t, res, tc.exit)
			if res.stdout != "" || !strings.Contains(res.stderr, tc.code) {
				t.Errorf("stdout %q stderr %q", res.stdout, res.stderr)
			}
		})
	}
}

// TestZipCreateJSONErrorIsTheEnvelope: under --json a refusal is the error document, on standard error.
func TestZipCreateJSONErrorIsTheEnvelope(t *testing.T) {
	isolate(t, testAccountKey)
	f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) { writeErr(w, 403, "download_restricted", "") })
	res := f.run(ZipCreate, testNode, "--json")
	wantCode(t, res, 3)
	var doc struct{ Error struct{ Code string } }
	if err := json.Unmarshal([]byte(res.stderr), &doc); err != nil || doc.Error.Code != "download_restricted" {
		t.Errorf("stderr %q", res.stderr)
	}
}

// TestZipCreateRefusesAnAnswerWithoutAZip: a job whose target is not a zip id is not followed or printed.
func TestZipCreateRefusesAnAnswerWithoutAZip(t *testing.T) {
	for name, target := range map[string]string{"empty": "", "a node": testNode, "a path": "../x"} {
		t.Run(name, func(t *testing.T) {
			isolate(t, testAccountKey)
			f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) { writeDoc(w, 202, jobDoc(target)) })
			res := f.run(ZipCreate, testNode, "--wait")
			wantCode(t, res, 1)
			if res.stdout != "" || len(f.calls()) != 1 {
				t.Errorf("stdout %q calls %v", res.stdout, f.calls())
			}
		})
	}
}

// TestZipCreateWaitPollsTheZipUntilItIsReady: pending twice, then ready. Each poll asks for the zip's first byte, so the
// answer carries the state, the size and the number of items left out.
func TestZipCreateWaitPollsTheZipUntilItIsReady(t *testing.T) {
	isolate(t, testAccountKey)
	noZipWait(t)
	data := []byte(strings.Repeat("zipdata", 30))
	f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) {
		switch {
		case r.Method == "POST":
			writeDoc(w, 202, jobDoc(testZip))
		case n < 3:
			pending(w)
		default:
			serveZipFile(w, r, data, "x.zip", 2)
		}
	})
	res := f.run(ZipCreate, testNode, "--wait")
	wantCode(t, res, 0)
	want := "zip=" + testZip + "\nstate=ready\nsize=" + itoa(len(data)) + "\nskipped=2\n"
	if res.stdout != want {
		t.Errorf("stdout %q, want %q", res.stdout, want)
	}
	zp := "GET /api/v1/zips/" + testZip
	if got := strings.Join(f.calls(), ","); got != "POST /api/v1/zips,"+zp+","+zp+","+zp {
		t.Errorf("calls %s", got)
	}
	for _, r := range f.seen()[1:] {
		if r.rng != "bytes=0-0" {
			t.Errorf("a poll asked for %q, want the first byte", r.rng)
		}
	}
}

// TestZipCreateWaitJSON: the document is the API's answer with the zip's state, size and left-out count added.
func TestZipCreateWaitJSON(t *testing.T) {
	isolate(t, testAccountKey)
	noZipWait(t)
	data := []byte("some zip bytes")
	f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if r.Method == "POST" {
			writeDoc(w, 202, jobDoc(testZip))
			return
		}
		serveZipFile(w, r, data, "x.zip", 0)
	})
	res := f.run(ZipCreate, testNode, "--wait", "--json")
	wantCode(t, res, 0)
	var doc map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["target"] != testZip || doc["state"] != "ready" || doc["size"] != float64(len(data)) || doc["skipped"] != float64(0) {
		t.Errorf("document %v", doc)
	}
}

// TestZipCreateWaitReportsAZipThatIsGone: pending, then not found. The zip route gives a failed zip the answer of a
// zip that does not exist, so the command says it failed or was removed, exits 1, and prints nothing on standard output.
func TestZipCreateWaitReportsAZipThatIsGone(t *testing.T) {
	isolate(t, testAccountKey)
	noZipWait(t)
	f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) {
		switch {
		case r.Method == "POST":
			writeDoc(w, 202, jobDoc(testZip))
		case n < 2:
			pending(w)
		default:
			writeErr(w, 404, "not_found", "")
		}
	})
	res := f.run(ZipCreate, testNode, "--wait")
	wantCode(t, res, 1)
	if res.stdout != "" || !strings.Contains(res.stderr, "failed") || !strings.Contains(res.stderr, "zip_failed") {
		t.Errorf("stdout %q stderr %q", res.stdout, res.stderr)
	}
	jres := f.run(ZipCreate, testNode, "--wait", "--json")
	wantCode(t, jres, 1)
	var doc struct{ Error struct{ Code string } }
	if err := json.Unmarshal([]byte(jres.stderr), &doc); err != nil || doc.Error.Code != "zip_failed" {
		t.Errorf("json stderr %q", jres.stderr)
	}
}

// TestZipCreateWaitTimesOut: a zip still pending at the limit ends the wait with exit 1, names the zip so it can be
// fetched later, and has made no more polls than the limit allows.
func TestZipCreateWaitTimesOut(t *testing.T) {
	isolate(t, testAccountKey)
	old := zipPollWait
	t.Cleanup(func() { zipPollWait = old })
	zipPollWait = func(int) time.Duration { return 5 * time.Millisecond }
	f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if r.Method == "POST" {
			writeDoc(w, 202, jobDoc(testZip))
			return
		}
		pending(w)
	})
	start := time.Now()
	res := f.run(ZipCreate, testNode, "--wait", "--wait-timeout", "100ms")
	wantCode(t, res, 1)
	if time.Since(start) > 5*time.Second {
		t.Errorf("the wait took %v", time.Since(start))
	}
	if res.stdout != "" || !strings.Contains(res.stderr, testZip) || !strings.Contains(res.stderr, "timeout") {
		t.Errorf("stdout %q stderr %q", res.stdout, res.stderr)
	}
}

// TestZipCreateWaitStopsOnARefusal: a refusal while polling ends the wait with that refusal's exit code.
func TestZipCreateWaitStopsOnARefusal(t *testing.T) {
	isolate(t, testAccountKey)
	noZipWait(t)
	f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) {
		switch {
		case r.Method == "POST":
			writeDoc(w, 202, jobDoc(testZip))
		case n < 2:
			pending(w)
		default:
			writeErr(w, 403, "download_restricted", "")
		}
	})
	res := f.run(ZipCreate, testNode, "--wait")
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "download_restricted") {
		t.Errorf("stderr %q", res.stderr)
	}
}

// TestZipPollWaitGrowsAndStops: the wait between polls grows and is capped.
func TestZipPollWaitGrowsAndStops(t *testing.T) {
	prev := time.Duration(0)
	for try := 1; try <= 20; try++ {
		d := zipPollWait(try)
		if d < prev || d <= 0 || d > 10*time.Second {
			t.Fatalf("try %d waits %v after %v", try, d, prev)
		}
		prev = d
	}
	if zipPollWait(1) >= zipPollWait(20) {
		t.Error("the wait does not grow")
	}
}

// ---- zip rm ----

// TestZipRmAsksFirst: at a terminal the person types the zip's id; nothing is sent before the right answer.
func TestZipRmAsksFirst(t *testing.T) {
	isolate(t, testAccountKey)
	f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) { w.WriteHeader(http.StatusNoContent) })
	typeAtTerminal("wrong\n")
	wantCode(t, f.run(ZipRm, testZip), 2)
	if c := f.calls(); len(c) != 0 {
		t.Fatalf("calls after a wrong answer: %v", c)
	}
	typeAtTerminal(testZip + "\n")
	res := f.run(ZipRm, testZip)
	wantCode(t, res, 0)
	if c := f.calls(); len(c) != 1 || c[0] != "DELETE /api/v1/zips/"+testZip {
		t.Errorf("calls %v", c)
	}
	if !hasRow(res.stdout, testZip, "done") {
		t.Errorf("stdout %q", res.stdout)
	}
}

// TestZipRmWithoutATerminalRefusesUnlessYes.
func TestZipRmWithoutATerminalRefusesUnlessYes(t *testing.T) {
	isolate(t, testAccountKey)
	f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) { w.WriteHeader(http.StatusNoContent) })
	res := f.run(ZipRm, testZip)
	wantCode(t, res, 2)
	if !strings.Contains(res.stderr, "--yes") || len(f.calls()) != 0 {
		t.Errorf("stderr %q calls %v", res.stderr, f.calls())
	}
	wantCode(t, f.run(ZipRm, testZip, "--yes"), 0)
	if len(f.calls()) != 1 {
		t.Errorf("calls %v", f.calls())
	}
}

// TestZipRmRefusesAnIdThatIsNotAZipBeforeAnyCall: --yes never skips validation.
func TestZipRmRefusesAnIdThatIsNotAZipBeforeAnyCall(t *testing.T) {
	for name, args := range map[string][]string{"none": {}, "a node": {testNode}, "a path": {"../x"},
		"one good, one bad": {testZip, testNode}} {
		t.Run(name, func(t *testing.T) {
			isolate(t, testAccountKey)
			f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) { w.WriteHeader(204) })
			wantCode(t, f.run(ZipRm, append(args, "--yes")...), 2)
			if len(f.calls()) != 0 {
				t.Errorf("calls %v", f.calls())
			}
		})
	}
}

// TestZipRmErrors: a zip that is not there is exit 4; one still being made is refused with exit 3.
func TestZipRmErrors(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status       int
		code, reason string
		exit         int
	}{
		{"not found", 404, "not_found", "", 4},
		{"still being made", 409, "conflict", "zip_pending", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t, testAccountKey)
			f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) { writeErr(w, tc.status, tc.code, tc.reason) })
			res := f.run(ZipRm, testZip, "--yes")
			wantCode(t, res, tc.exit)
			if !strings.Contains(res.stderr, tc.code) {
				t.Errorf("stderr %q", res.stderr)
			}
		})
	}
}

// TestZipRmSeveralReportsEach: one that fails does not stop the others, and the exit code is 7.
func TestZipRmSeveralReportsEach(t *testing.T) {
	isolate(t, testAccountKey)
	f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if strings.HasSuffix(r.URL.Path, testZipB) {
			writeErr(w, 404, "not_found", "")
			return
		}
		w.WriteHeader(204)
	})
	res := f.run(ZipRm, testZip, testZipB, "--yes")
	wantCode(t, res, 7)
	if !hasRow(res.stdout, testZip, "done") || !hasRow(res.stdout, testZipB, "failed") {
		t.Errorf("stdout %q", res.stdout)
	}
}

// ---- zip get ----

// zipServer serves one ready zip, ranged, and counts the range requests.
func zipServer(t *testing.T, data []byte, name string, skipped int) *zipFake {
	return newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if r.Method == "GET" && r.URL.Path == "/api/v1/zips/"+testZip {
			serveZipFile(w, r, data, name, skipped)
			return
		}
		writeErr(w, 404, "not_found", "")
	})
}

// TestZipGetDownloadsInRangesAndChecksTheSum: the first request reads the version (the first byte), then each range
// asks with If-Range set to the zip's ETag. The file takes the server's name and its SHA-256 is checked.
func TestZipGetDownloadsInRangesAndChecksTheSum(t *testing.T) {
	isolate(t, testAccountKey)
	data := randomBytes(t, 3*mib+mib/2)
	f := zipServer(t, data, "Reports.zip", 0)
	dest := t.TempDir()
	res := f.run(ZipGet, testZip, dest, "--piece-size", "1MiB")
	wantCode(t, res, 0)
	got, err := os.ReadFile(filepath.Join(dest, "Reports.zip"))
	if err != nil || sha(got) != sha(data) {
		t.Fatalf("the downloaded file differs: %v", err)
	}
	if !strings.Contains(res.stdout, "checked=sha256") || !strings.Contains(res.stdout, "sha256="+sha(data)) {
		t.Errorf("stdout %q", res.stdout)
	}
	reqs := f.seen()
	want := []string{"bytes=0-0", "bytes=0-1048575", "bytes=1048576-2097151", "bytes=2097152-3145727", "bytes=3145728-3670015"}
	if len(reqs) != len(want) {
		t.Fatalf("%d requests, want %d", len(reqs), len(want))
	}
	for i, r := range reqs {
		if r.method != "GET" || r.path != "/api/v1/zips/"+testZip || r.rng != want[i] {
			t.Errorf("request %d: %s %s %q", i, r.method, r.path, r.rng)
		}
		if i > 0 && r.ifRange != `"`+sha(data)+`"` {
			t.Errorf("request %d: If-Range %q", i, r.ifRange)
		}
	}
	if names := dirEntries(t, dest); len(names) != 1 {
		t.Errorf("the destination holds %v", names)
	}
	if r := downloadRecords(t); len(r) != 0 {
		t.Errorf("download records left: %v", r)
	}
}

// TestZipGetNamesTheFileLikeGet: DEST is a directory, where the zip takes the server's name, or a new file's path.
func TestZipGetNamesTheFileLikeGet(t *testing.T) {
	isolate(t, testAccountKey)
	data := []byte("a small zip")
	f := zipServer(t, data, "Reports.zip", 0)
	dir := t.TempDir()
	wantCode(t, f.run(ZipGet, testZip, filepath.Join(dir, "mine.zip")), 0)
	if b, err := os.ReadFile(filepath.Join(dir, "mine.zip")); err != nil || string(b) != string(data) {
		t.Errorf("mine.zip: %q %v", b, err)
	}
	// With no name from the server, the zip's own id names the file.
	g := zipServer(t, data, "", 0)
	dir2 := t.TempDir()
	wantCode(t, g.run(ZipGet, testZip, dir2), 0)
	if _, err := os.Stat(filepath.Join(dir2, testZip+".zip")); err != nil {
		t.Errorf("no file named for the zip: %v", dirEntries(t, dir2))
	}
}

// TestZipGetRefusesAnUnsafeServerName: a name with a separator is not written; the hint says how to name the file.
func TestZipGetRefusesAnUnsafeServerName(t *testing.T) {
	isolate(t, testAccountKey)
	f := zipServer(t, []byte("x"), "../evil.zip", 0)
	dir := t.TempDir()
	res := f.run(ZipGet, testZip, dir)
	wantCode(t, res, 2)
	if names := dirEntries(t, dir); len(names) != 0 {
		t.Errorf("files written: %v", names)
	}
}

// TestZipGetNeverOverwritesWithoutTheFlag: an existing file is refused before any request for its bytes; --overwrite
// replaces it only when the download is complete and checked.
func TestZipGetNeverOverwritesWithoutTheFlag(t *testing.T) {
	isolate(t, testAccountKey)
	data := []byte("new zip bytes")
	f := zipServer(t, data, "a.zip", 0)
	dir := t.TempDir()
	target := filepath.Join(dir, "a.zip")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantCode(t, f.run(ZipGet, testZip, dir), 2)
	if b, _ := os.ReadFile(target); string(b) != "old" {
		t.Errorf("the file was changed: %q", b)
	}
	wantCode(t, f.run(ZipGet, testZip, target), 2)
	wantCode(t, f.run(ZipGet, testZip, dir, "--overwrite"), 0)
	if b, _ := os.ReadFile(target); string(b) != string(data) {
		t.Errorf("--overwrite did not replace: %q", b)
	}
}

// TestZipGetRefusesBadInputBeforeAnyCall: only a zip id is taken, and the destination follows get's rules.
func TestZipGetRefusesBadInputBeforeAnyCall(t *testing.T) {
	for name, args := range map[string][]string{
		"none": {}, "a node id": {testNode}, "a path": {"/a"}, "three words": {testZip, "a", "b"},
		"a share link": {"https://example.test/s#x"}, "a missing directory": {testZip, "/no/such/dir/x.zip"},
		"a small piece": {testZip, "--piece-size", "1KiB"},
	} {
		t.Run(name, func(t *testing.T) {
			isolate(t, testAccountKey)
			f := zipServer(t, []byte("x"), "a.zip", 0)
			wantCode(t, f.run(ZipGet, args...), 2)
			if len(f.calls()) != 0 {
				t.Errorf("calls %v", f.calls())
			}
		})
	}
}

// TestZipGetErrors: a zip not ready is exit 1 with nothing written; not found is 4; a refusal is 3.
func TestZipGetErrors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		serve func(w http.ResponseWriter)
		exit  int
		text  string
	}{
		{"not ready", pending, 1, "not ready"},
		{"not found", func(w http.ResponseWriter) { writeErr(w, 404, "not_found", "") }, 4, "not_found"},
		{"download turned off", func(w http.ResponseWriter) { writeErr(w, 403, "download_restricted", "") }, 3, "download_restricted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t, testAccountKey)
			f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) { tc.serve(w) })
			dir := t.TempDir()
			res := f.run(ZipGet, testZip, dir)
			wantCode(t, res, tc.exit)
			if !strings.Contains(res.stderr, tc.text) {
				t.Errorf("stderr %q", res.stderr)
			}
			if names := dirEntries(t, dir); len(names) != 0 {
				t.Errorf("files written: %v", names)
			}
		})
	}
}

// TestZipGetSaysHowManyItemsWereLeftOut: the count is on standard error, and not when it is zero.
func TestZipGetSaysHowManyItemsWereLeftOut(t *testing.T) {
	isolate(t, testAccountKey)
	f := zipServer(t, []byte("zip"), "a.zip", 3)
	res := f.run(ZipGet, testZip, t.TempDir())
	wantCode(t, res, 0)
	if !strings.Contains(res.stderr, "3 items") {
		t.Errorf("stderr %q", res.stderr)
	}
	g := zipServer(t, []byte("zip"), "a.zip", 0)
	res = g.run(ZipGet, testZip, t.TempDir())
	wantCode(t, res, 0)
	if strings.Contains(res.stderr, "left out") {
		t.Errorf("stderr %q", res.stderr)
	}
}

// TestZipGetKeepsThePartFileAndGoesOnFromIt: the server fails for good after some bytes. The part file is kept with its
// record. A later run reads from the part file's size, and the zip comes out whole, never with its start appended twice.
func TestZipGetKeepsThePartFileAndGoesOnFromIt(t *testing.T) {
	isolate(t, testAccountKey)
	noWait(t)
	data := randomBytes(t, 3*mib)
	var failing sync.Mutex
	broken := true
	f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) {
		failing.Lock()
		down := broken && strings.HasPrefix(r.Header.Get("Range"), "bytes=1048576-")
		failing.Unlock()
		if down {
			writeErr(w, 503, "unavailable", "")
			return
		}
		serveZipFile(w, r, data, "big.zip", 0)
	})
	dir := t.TempDir()
	res := f.run(ZipGet, testZip, dir, "--piece-size", "1MiB")
	wantCode(t, res, 1)
	if names := dirEntries(t, dir); len(names) != 1 || names[0] != "big.zip.part" {
		t.Fatalf("the destination holds %v, want the part file alone", names)
	}
	if !strings.Contains(res.stderr, "part file") {
		t.Errorf("stderr %q does not name the part file", res.stderr)
	}
	if r := downloadRecords(t); len(r) != 1 {
		t.Fatalf("records %v, want one", r)
	}
	failing.Lock()
	broken = false
	failing.Unlock()
	before := len(f.seen())
	res = f.run(ZipGet, testZip, dir, "--piece-size", "1MiB")
	wantCode(t, res, 0)
	got, err := os.ReadFile(filepath.Join(dir, "big.zip"))
	if err != nil || sha(got) != sha(data) {
		t.Fatalf("the resumed zip differs: %v", err)
	}
	if !strings.Contains(res.stderr, "going on from byte 1048576") {
		t.Errorf("stderr %q", res.stderr)
	}
	for _, r := range f.seen()[before:] {
		if r.rng != "bytes=0-0" && rangeStart(r.rng) < mib {
			t.Errorf("a range at %q fetched bytes the part file held", r.rng)
		}
	}
	if r := downloadRecords(t); len(r) != 0 {
		t.Errorf("records left: %v", r)
	}
}

// TestZipGetStartsAgainOnAPartFileOfAnotherZip: a part file begun for another zip of the same name is not continued.
func TestZipGetStartsAgainOnAPartFileOfAnotherZip(t *testing.T) {
	isolate(t, testAccountKey)
	data := []byte("the real zip")
	f := zipServer(t, data, "a.zip", 0)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.zip.part"), []byte("someone else's bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantCode(t, f.run(ZipGet, testZip, dir), 2)
	if b, _ := os.ReadFile(filepath.Join(dir, "a.zip.part")); string(b) != "someone else's bytes" {
		t.Errorf("the part file was changed: %q", b)
	}
	wantCode(t, f.run(ZipGet, testZip, dir, "--overwrite"), 0)
	if b, _ := os.ReadFile(filepath.Join(dir, "a.zip")); string(b) != string(data) {
		t.Errorf("zip %q", b)
	}
}

// TestZipGetIgnoredRangeIsReported: a server that answers a range with the whole zip of the same version is named, as
// get names it, and the command does not loop.
func TestZipGetIgnoredRangeIsReported(t *testing.T) {
	isolate(t, testAccountKey)
	data := randomBytes(t, 2*mib)
	f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if r.Header.Get("Range") == "bytes=0-0" {
			serveZipFile(w, r, data, "a.zip", 0)
			return
		}
		w.Header().Set("ETag", `"`+sha(data)+`"`)
		_, _ = w.Write(data)
	})
	res := f.run(ZipGet, testZip, t.TempDir(), "--piece-size", "1MiB")
	wantCode(t, res, 1)
	if !strings.Contains(res.stderr, "instead of a range") {
		t.Errorf("stderr %q", res.stderr)
	}
}
