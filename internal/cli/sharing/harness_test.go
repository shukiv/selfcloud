// SPDX-License-Identifier: AGPL-3.0-or-later

package sharing

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

	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/id"
)

// Every test here runs against a fake server (httptest) that records each request: its method, path, raw query, body and
// bearer. The fake answers with what a test queues. The tests are not parallel: they replace the standard input and the
// terminal check that the file commands share.

const (
	testKey      = "sc_acct_TESTKEY_0123456789abcdefghijklmnopqrstuvwxyz"
	testDeviceTk = "scd_TESTDEVICE_0123456789abcdefghijklmnopqrstuvwxyz"
	// testSecret is a link secret as the server makes one: 43 characters of URL-safe base64.
	testSecret = "AbCdEfGhIjKlMnOpQrStUvWxYz0123456789-_AbCde"
	// testPassword is a link password a test types; it must never come back.
	testPassword = "hunter2-correct-horse"
)

var (
	testNode    = id.Derive("nod", "cli7-node")
	testOther   = id.Derive("nod", "cli7-other")
	testFolder  = id.Derive("fld", "cli7-folder")
	testAccount = id.Derive("acc", "cli7-account")
	testUser    = id.Derive("usr", "cli7-user")
	testUser2   = id.Derive("usr", "cli7-user-2")
	testGroup   = id.Derive("grp", "cli7-group")
	testLink    = id.Derive("shl", "cli7-link")
	testLink2   = id.Derive("shl", "cli7-link-2")
	testReq     = id.Derive("arq", "cli7-request")
	testReq2    = id.Derive("arq", "cli7-request-2")
)

// isolate points the profiles at a temporary directory, clears what the commands read, and sets the credential the calls
// send. Standard input is empty and is not a terminal unless a test says otherwise.
func isolate(t *testing.T, token string) {
	t.Helper()
	t.Setenv("SELFCLOUD_PROFILES_DIR", filepath.Join(t.TempDir(), "profiles"))
	t.Setenv("SELFCLOUD_PROFILE", "")
	t.Setenv("SELFCLOUD_URL", "")
	t.Setenv("SELFCLOUD_ACCOUNT", "")
	t.Setenv("SELFCLOUD_TOKEN", token)
	oldIn, oldTTY := files.Stdin, files.IsTerminal
	t.Cleanup(func() { files.Stdin, files.IsTerminal = oldIn, oldTTY })
	files.Stdin = strings.NewReader("")
	files.IsTerminal = func(io.Reader) bool { return false }
}

// typeAtTerminal makes standard input a terminal where text is typed, then ends.
func typeAtTerminal(text string) {
	files.Stdin = strings.NewReader(text)
	files.IsTerminal = func(io.Reader) bool { return true }
}

// pipeIn makes standard input a pipe that holds text.
func pipeIn(text string) {
	files.Stdin = strings.NewReader(text)
	files.IsTerminal = func(io.Reader) bool { return false }
}

type reply struct {
	status int
	body   string
}

type fake struct {
	t       *testing.T
	srv     *httptest.Server
	mu      sync.Mutex
	replies map[string][]reply
	calls   []string
	bodies  []string
	bearers []string
}

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
	rep := reply{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"no such thing"}}`}
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

func (f *fake) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

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

func (f *fake) wantNoCalls() {
	f.t.Helper()
	if calls := f.requests(); len(calls) != 0 {
		f.t.Fatalf("calls %v; want none", calls)
	}
}

// body returns the body of the i-th call as a JSON object.
func (f *fake) body(i int) map[string]any {
	f.t.Helper()
	f.mu.Lock()
	raw := f.bodies[i]
	f.mu.Unlock()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		f.t.Fatalf("call %d body %q is not a JSON object: %v", i, raw, err)
	}
	return m
}

// rawBody returns the body of the i-th call.
func (f *fake) rawBody(i int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[i]
}

// wantCode checks the exit code, and that no stream carries a credential or a link password.
func wantCode(t *testing.T, res result, want int) {
	t.Helper()
	if res.code != want {
		t.Fatalf("exit %d, want %d; stdout %q, stderr %q", res.code, want, res.stdout, res.stderr)
	}
	for _, s := range []string{testKey, testDeviceTk, testPassword} {
		if strings.Contains(res.stdout+res.stderr, s) {
			t.Fatalf("a secret was printed: stdout %q, stderr %q", res.stdout, res.stderr)
		}
	}
}

func jsonOf(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func ok(v any) reply      { return reply{status: http.StatusOK, body: jsonOf(v)} }
func created(v any) reply { return reply{status: http.StatusCreated, body: jsonOf(v)} }
func done(status int) reply {
	return reply{status: status}
}

func apiErr(status int, code, message string) reply {
	return reply{status: status, body: jsonOf(map[string]any{"error": map[string]string{"code": code, "message": message}})}
}

// apiErrReason is an error answer that carries details.reason.
func apiErrReason(status int, code, message, reason string) reply {
	return reply{status: status, body: jsonOf(map[string]any{"error": map[string]any{
		"code": code, "message": message, "details": map[string]string{"reason": reason}}})}
}

func listOf(cursor string, items ...map[string]any) reply {
	doc := map[string]any{"items": items}
	if cursor != "" {
		doc["next_cursor"] = cursor
	}
	return reply{status: http.StatusOK, body: jsonOf(doc)}
}

func nodeRoute(nodeID, suffix string) string { return "/api/v1/nodes/" + nodeID + suffix }

func accountRoute(suffix string) string { return "/api/v1/accounts/" + testAccount + suffix }

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
