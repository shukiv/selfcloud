// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
)

// Every test here runs against a fake server (httptest) that records each request: its method, path, raw query, body and
// bearer. The fake answers with what a test queues, so no test needs a real store. The tests in this package are not
// parallel: they replace the package's standard input and terminal check.

const (
	testAccountKey = "sc_acct_TESTKEY_0123456789abcdefghijklmnopqrstuvwxyz"
	testDeviceTok  = "scd_TESTDEVICE_0123456789abcdefghijklmnopqrstuvwxyz"
)

// Identifiers the tests use. They have the shape the server makes (internal/id), derived from a fixed seed so the
// goldens are stable.
var (
	testFolder = id.Derive("fld", "cli5-folder")
	testNode   = id.Derive("nod", "cli5-node")
	testDest   = id.Derive("nod", "cli5-dest")
	testOther  = id.Derive("nod", "cli5-other")
)

// isolate points the profiles at a temporary directory, clears what the commands read, and sets the credential the calls
// send. Standard input is empty and is not a terminal unless a test says otherwise.
func isolate(t *testing.T, token string) {
	t.Helper()
	t.Setenv("SELFCLOUD_PROFILES_DIR", filepath.Join(t.TempDir(), "profiles"))
	t.Setenv("SELFCLOUD_PROFILE", "")
	t.Setenv("SELFCLOUD_URL", "")
	t.Setenv("SELFCLOUD_TOKEN", token)
	oldIn, oldTTY := stdin, isTerminal
	t.Cleanup(func() { stdin, isTerminal = oldIn, oldTTY })
	stdin = strings.NewReader("")
	isTerminal = func(io.Reader) bool { return false }
}

// typeAtTerminal makes standard input a terminal where text is typed, then ends.
func typeAtTerminal(text string) {
	stdin = strings.NewReader(text)
	isTerminal = func(io.Reader) bool { return true }
}

// table writes rows the way the output package's tables are written: each cell but the last is padded to its column's
// widest cell plus two spaces, and the last cell is not padded.
func table(rows ...[]string) string {
	widths := make([]int, len(rows[0]))
	for _, r := range rows {
		for i, c := range r[:len(r)-1] {
			widths[i] = max(widths[i], len(c)+2)
		}
	}
	var b strings.Builder
	for _, r := range rows {
		for i, c := range r {
			b.WriteString(c)
			if i < len(r)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-len(c)))
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// hasRow reports whether a result table has a row whose first two cells are target and status.
func hasRow(stdout, target, status string) bool {
	for _, line := range strings.Split(stdout, "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == target && f[1] == status {
			return true
		}
	}
	return false
}

// reply is one answer of the fake server.
type reply struct {
	status int
	body   string
}

// fake is the httptest server and what it saw.
type fake struct {
	t       *testing.T
	srv     *httptest.Server
	mu      sync.Mutex
	replies map[string][]reply
	calls   []string
	bodies  []string
	bearers []string
}

// newFake starts a server. A request with no queued answer gets a 404 with the API's error envelope.
func newFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{t: t, replies: map[string][]reply{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

// on queues answers for a method and a path. The last one repeats.
func (f *fake) on(method, path string, rs ...reply) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies[method+" "+path] = append(f.replies[method+" "+path], rs...)
}

func (f *fake) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	call := r.Method + " " + r.URL.Path
	if r.URL.RawQuery != "" {
		call += "?" + r.URL.RawQuery
	}
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.bodies = append(f.bodies, string(body))
	f.bearers = append(f.bearers, r.Header.Get("Authorization"))
	rep := reply{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"no such node"}}`}
	if q := f.replies[r.Method+" "+r.URL.Path]; len(q) > 0 {
		rep = q[0]
		if len(q) > 1 {
			f.replies[r.Method+" "+r.URL.Path] = q[1:]
		}
	}
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(rep.status)
	_, _ = io.WriteString(w, rep.body)
}

// result is what a command wrote and returned.
type result struct {
	code   int
	stdout string
	stderr string
}

// run calls a command with the fake's address appended.
func (f *fake) run(fn func([]string, io.Writer, io.Writer) int, args ...string) result {
	f.t.Helper()
	var out, errOut bytes.Buffer
	code := fn(append(append([]string{}, args...), "--url", f.srv.URL), &out, &errOut)
	return result{code: code, stdout: out.String(), stderr: errOut.String()}
}

// requests returns the calls the fake saw, in order, as "METHOD /path?query".
func (f *fake) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// wantCalls checks the calls the fake saw, in order, and that each carried the credential the test set.
func (f *fake) wantCalls(want ...string) {
	f.t.Helper()
	f.mu.Lock()
	calls, bearers := append([]string(nil), f.calls...), append([]string(nil), f.bearers...)
	f.mu.Unlock()
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		f.t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}
	for i, b := range bearers {
		if b == "" {
			f.t.Errorf("call %d (%s) carried no credential", i, calls[i])
		}
	}
}

// wantBody returns the body of the i-th call, decoded as a JSON object.
func (f *fake) wantBody(i int) map[string]any {
	f.t.Helper()
	f.mu.Lock()
	body := f.bodies[i]
	f.mu.Unlock()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		f.t.Fatalf("call %d body %q is not a JSON object: %v", i, body, err)
	}
	return m
}

// wantNoCalls checks that no request reached the server.
func (f *fake) wantNoCalls() {
	f.t.Helper()
	if calls := f.requests(); len(calls) != 0 {
		f.t.Fatalf("calls %v; want none", calls)
	}
}

// wantCode checks the exit code of a result, and that neither stream carries a credential.
func wantCode(t *testing.T, res result, want int) {
	t.Helper()
	if res.code != want {
		t.Fatalf("exit %d, want %d; stdout %q, stderr %q", res.code, want, res.stdout, res.stderr)
	}
	for _, secret := range []string{testAccountKey, testDeviceTok} {
		if strings.Contains(res.stdout+res.stderr, secret) {
			t.Fatalf("a credential was printed: stdout %q, stderr %q", res.stdout, res.stderr)
		}
	}
}

// jsonOf encodes v for a fixture. A fixture that cannot be encoded is a bug in the test, so it panics.
func jsonOf(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// node returns a node document as the API answers it.
func node(nodeID, name, kind string) map[string]any {
	return map[string]any{
		"id": nodeID, "folder_id": testFolder, "parent_id": testDest, "name": name, "kind": kind, "size": 0,
		"mime_type": "", "created_at": "2026-11-01T10:00:00Z", "modified_at": "2026-11-02T10:00:00Z",
		"role": "manager", "no_download": false, "editors_can_share": false,
	}
}

// page returns a list answer with an optional next cursor.
func page(cursor string, items ...map[string]any) reply {
	doc := map[string]any{"items": items}
	if cursor != "" {
		doc["next_cursor"] = cursor
	}
	return reply{status: http.StatusOK, body: jsonOf(doc)}
}

// apiErr is an error answer with the API's envelope.
func apiErr(status int, code, message string) reply {
	return reply{status: status, body: jsonOf(map[string]any{"error": map[string]string{"code": code, "message": message}})}
}

// done is an answer with no body.
func done(status int) reply { return reply{status: status} }

// created is a 201 with a node document.
func created(n map[string]any) reply { return reply{status: http.StatusCreated, body: jsonOf(n)} }

// ok is a 200 with a node document.
func ok(n map[string]any) reply { return reply{status: http.StatusOK, body: jsonOf(n)} }

// folderPath is the children route of the test folder.
func folderPath() string { return "/api/v1/folders/" + testFolder + "/children" }

// nodePath is the route of a node, the one given by its suffix.
func nodePath(nodeID, suffix string) string { return "/api/v1/nodes/" + nodeID + suffix }
