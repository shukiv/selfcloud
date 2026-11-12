// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

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
// bearer. The fake answers with what a test queues. The tests are not parallel: they replace the file commands' standard
// input and terminal check, which the confirmations read.

const (
	testAccountKey = "sc_acct_TESTKEY_0123456789abcdefghijklmnopqrstuvwxyz"
	testDeviceTok  = "scd_TESTDEVICE_0123456789abcdefghijklmnopqrstuvwxyz"
)

var (
	testAcc    = id.Derive("acc", "cli8-account")
	testFolder = id.Derive("fld", "cli8-folder")
	testUser   = id.Derive("usr", "cli8-user")
	testGroup  = id.Derive("grp", "cli8-group")
)

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

// table writes rows as the output package's tables are written.
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

// set replaces the answers queued for a method and a path.
func (f *fake) set(method, path string, rs ...reply) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies[method+" "+path] = append([]reply(nil), rs...)
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

// runAcc is run with --account set to the test account.
func (f *fake) runAcc(fn func([]string, io.Writer, io.Writer) int, args ...string) result {
	f.t.Helper()
	return f.run(fn, append(append([]string{}, args...), "--account", testAcc)...)
}

func (f *fake) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// wantCalls checks the calls the fake saw, in order.
func (f *fake) wantCalls(want ...string) {
	f.t.Helper()
	if got := f.requests(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		f.t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func (f *fake) wantNoCalls() {
	f.t.Helper()
	if calls := f.requests(); len(calls) != 0 {
		f.t.Fatalf("calls %v; want none", calls)
	}
}

// wantBearer checks that every call carried the credential.
func (f *fake) wantBearer(token string) {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, b := range f.bearers {
		if b != "Bearer "+token {
			f.t.Errorf("call %d (%s) carried %q", i, f.calls[i], b)
		}
	}
}

// bodyOf returns the i-th call's body as a JSON object.
func (f *fake) bodyOf(i int) map[string]any {
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

func (f *fake) rawBody(i int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[i]
}

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

func jsonOf(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func apiErr(status int, code, message string) reply {
	return reply{status: status, body: jsonOf(map[string]any{"error": map[string]string{"code": code, "message": message}})}
}

func apiErrWith(status int, code, message, reason string) reply {
	return reply{status: status, body: jsonOf(map[string]any{"error": map[string]any{
		"code": code, "message": message, "details": map[string]string{"reason": reason}}})}
}

func done(status int) reply { return reply{status: status} }

func doc(status int, v map[string]any) reply { return reply{status: status, body: jsonOf(v)} }

func page(cursor string, items ...map[string]any) reply {
	d := map[string]any{"items": items}
	if cursor != "" {
		d["next_cursor"] = cursor
	}
	return reply{status: http.StatusOK, body: jsonOf(d)}
}

// folder returns a folder document as the API answers it.
func folder(folderID, name string) map[string]any {
	return map[string]any{
		"id": folderID, "external_id": nil, "name": name, "kind": "team", "root_node_id": "nod_ROOT",
		"created_at": "2026-11-01T10:00:00Z", "updated_at": "2026-11-02T10:00:00Z",
		"members_only": false, "sharing_managers_only": false, "no_download_for_viewers": false, "allow_links": true,
	}
}

// group returns a group document as the API answers it.
func group(groupID, name string) map[string]any {
	return map[string]any{
		"id": groupID, "external_id": nil, "name": name,
		"created_at": "2026-11-01T10:00:00Z", "updated_at": "2026-11-02T10:00:00Z",
	}
}

func accFolders() string        { return "/api/v1/accounts/" + testAcc + "/folders" }
func accFolder(f string) string { return accFolders() + "/" + f }
func accGroups() string         { return "/api/v1/accounts/" + testAcc + "/groups" }
func accGroup(g string) string  { return accGroups() + "/" + g }
