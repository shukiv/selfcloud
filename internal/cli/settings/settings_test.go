// SPDX-License-Identifier: AGPL-3.0-or-later

package settings

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
)

// Every test here runs against a fake server that records each request: its method, path, content type, body and bearer.
// The tests change the environment, so none is parallel.

const (
	adminKey   = "sc_admin_TESTKEY_0123456789abcdefghijklmnopqrstuvwxyz"
	accountKey = "sc_acct_TESTKEY_0123456789abcdefghijklmnopqrstuvwxyz"
)

const hint = "settings needs an installation admin key (sc_admin_...); create one on the server with selfcloud admin create-key"

// isolate points the profiles at a temporary directory, clears what the commands read, and sets the credential.
func isolate(t *testing.T, token string) {
	t.Helper()
	t.Setenv("SELFCLOUD_PROFILES_DIR", filepath.Join(t.TempDir(), "profiles"))
	t.Setenv("SELFCLOUD_PROFILE", "")
	t.Setenv("SELFCLOUD_URL", "")
	t.Setenv("SELFCLOUD_TOKEN", token)
}

type reply struct {
	status int
	body   string
}

type fake struct {
	t        *testing.T
	srv      *httptest.Server
	mu       sync.Mutex
	replies  map[string][]reply
	calls    []string
	bodies   [][]byte
	types    []string
	bearers  []string
	accepted []string
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
	key := r.Method + " " + r.URL.Path
	f.mu.Lock()
	f.calls = append(f.calls, key)
	f.bodies = append(f.bodies, body)
	f.types = append(f.types, r.Header.Get("Content-Type"))
	f.bearers = append(f.bearers, r.Header.Get("Authorization"))
	rep := reply{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"no such route"}}`}
	if q := f.replies[key]; len(q) > 0 {
		rep = q[0]
		if len(q) > 1 {
			f.replies[key] = q[1:]
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

func (f *fake) run(fn func([]string, io.Writer, io.Writer) int, args ...string) result {
	f.t.Helper()
	var out, errOut bytes.Buffer
	code := fn(append(append([]string{}, args...), "--url", f.srv.URL), &out, &errOut)
	return result{code: code, stdout: out.String(), stderr: errOut.String()}
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
		if !strings.HasPrefix(b, "Bearer sc_") {
			f.t.Errorf("call %d (%s) carried bearer %q", i, calls[i], b)
		}
	}
}

func (f *fake) body(i int) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[i]
}

func (f *fake) contentType(i int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.types[i]
}

// jsonBody returns the i-th call's body decoded as a JSON value.
func (f *fake) jsonBody(i int) any {
	f.t.Helper()
	var v any
	if err := json.Unmarshal(f.body(i), &v); err != nil {
		f.t.Fatalf("call %d body %q is not JSON: %v", i, f.body(i), err)
	}
	return v
}

// table writes rows as the output package does: each cell but the last is padded to its column's widest cell plus two.
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

func sameJSON(t *testing.T, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("got %q: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	gb, _ := json.Marshal(g)
	wb, _ := json.Marshal(w)
	if string(gb) != string(wb) {
		t.Errorf("got %s\nwant %s", gb, wb)
	}
}

// listing is the server's answer for the keys the tests use. Types follow the key's name.
const listing = `{"settings":[
 {"key":"flag.enabled","type":"bool","value":false,"default":false,"source":"default","locked":false,"restart":false},
 {"key":"count.key","type":"int","value":5,"default":5,"source":"default","locked":false,"restart":false},
 {"key":"dur.key","type":"duration","value":"168h0m0s","default":"168h0m0s","source":"file","locked":true,"restart":false},
 {"key":"size.key","type":"bytes","value":"64MiB","default":"32MiB","source":"settings","locked":false,"restart":true},
 {"key":"name.key","type":"string","value":"","default":"Selfcloud","source":"env","locked":true,"restart":true},
 {"key":"big.key","type":"int","value":17592186044416,"default":0,"source":"settings","locked":false,"restart":false}],
 "facts":[{"key":"data_dir","value":"/data","source":"default"}],
 "pictures":[
 {"key":"ui.brand.icon","state":"none","locked":false},
 {"key":"ui.brand.logo","state":"uploaded","type":"image/png","sha256":"abc123","locked":true}]}`

func TestGetPrintsTheSettingsAndThePictures(t *testing.T) {
	isolate(t, adminKey)
	f := newFake(t)
	f.on("GET", settingsPath, reply{200, listing})
	r := f.run(Get)
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	f.wantCalls("GET " + settingsPath)
	want := table(
		[]string{"KEY", "VALUE", "SOURCE", "DEFAULT", "NOTE"},
		[]string{"flag.enabled", "false", "default", "false", ""},
		[]string{"count.key", "5", "default", "5", ""},
		[]string{"dur.key", "168h0m0s", "file", "168h0m0s", "locked"},
		[]string{"size.key", "64MiB", "settings", "32MiB", "restart"},
		[]string{"name.key", `""`, "env", "Selfcloud", "locked,restart"},
		[]string{"big.key", "17592186044416", "settings", "0", ""},
	) + "\n" + table(
		[]string{"PICTURE", "STATE", "TYPE", "NOTE"},
		[]string{"ui.brand.icon", "none", "", ""},
		[]string{"ui.brand.logo", "uploaded", "image/png", "locked"},
	)
	// Trailing spaces of an empty last cell are part of the table's own writing; compare line by line without them.
	if trimLines(r.stdout) != trimLines(want) {
		t.Errorf("stdout:\n%s\nwant:\n%s", r.stdout, want)
	}
}

func trimLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(lines, "\n")
}

func TestGetJSONPrintsTheBodyIndented(t *testing.T) {
	isolate(t, adminKey)
	f := newFake(t)
	f.on("GET", settingsPath, reply{200, listing})
	r := f.run(Get, "--json")
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if !strings.Contains(r.stdout, "\n  ") {
		t.Errorf("not indented: %q", r.stdout)
	}
	sameJSON(t, r.stdout, listing)
}

func TestGetOneKeyPrintsKeyValueLines(t *testing.T) {
	isolate(t, adminKey)
	f := newFake(t)
	f.on("GET", settingsPath, reply{200, listing})
	r := f.run(Get, "name.key")
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	want := "key=name.key\ntype=string\nvalue=\"\"\ndefault=Selfcloud\nsource=env\nlocked=true\nrestart=true\n"
	if r.stdout != want {
		t.Errorf("stdout %q, want %q", r.stdout, want)
	}
	f.wantCalls("GET " + settingsPath)
}

func TestGetOneKeyAsJSONIsThatObject(t *testing.T) {
	isolate(t, adminKey)
	f := newFake(t)
	f.on("GET", settingsPath, reply{200, listing})
	r := f.run(Get, "size.key", "--json")
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	sameJSON(t, r.stdout, `{"key":"size.key","type":"bytes","value":"64MiB","default":"32MiB","source":"settings","locked":false,"restart":true}`)
}

func TestGetAPictureKeyPrintsItsLines(t *testing.T) {
	isolate(t, adminKey)
	f := newFake(t)
	f.on("GET", settingsPath, reply{200, listing})
	r := f.run(Get, "ui.brand.logo")
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	want := "key=ui.brand.logo\nstate=uploaded\ntype=image/png\nsha256=abc123\nlocked=true\n"
	if r.stdout != want {
		t.Errorf("stdout %q, want %q", r.stdout, want)
	}
}

func TestGetAPictureKeyAsJSONIsThatObject(t *testing.T) {
	isolate(t, adminKey)
	f := newFake(t)
	f.on("GET", settingsPath, reply{200, listing})
	r := f.run(Get, "ui.brand.logo", "--json")
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if !strings.Contains(r.stdout, "\n  ") {
		t.Errorf("not indented: %q", r.stdout)
	}
	sameJSON(t, r.stdout, `{"key":"ui.brand.logo","state":"uploaded","type":"image/png","sha256":"abc123","locked":true}`)
}

func TestGetAnUnknownKeyExitsFour(t *testing.T) {
	isolate(t, adminKey)
	f := newFake(t)
	f.on("GET", settingsPath, reply{200, listing})
	r := f.run(Get, "nope.key")
	if r.code != 4 || !strings.Contains(r.stderr, `no setting named "nope.key"`) {
		t.Errorf("exit %d, stderr %q", r.code, r.stderr)
	}
	f.wantCalls("GET " + settingsPath)
}

func TestGetTwoKeysIsAUsageErrorWithNoRequest(t *testing.T) {
	isolate(t, adminKey)
	f := newFake(t)
	r := f.run(Get, "a", "b")
	if r.code != 2 {
		t.Errorf("exit %d, stderr %q", r.code, r.stderr)
	}
	f.wantCalls()
}

// 9007199254740993 is 2^53+1, which a float64 cannot hold: read through a float, it prints as ...992. A null default prints
// as an empty field.
func TestGetPrintsALargeIntegerExactlyAndANullAsNothing(t *testing.T) {
	isolate(t, adminKey)
	f := newFake(t)
	f.on("GET", settingsPath, reply{200, `{"settings":[
	 {"key":"huge.key","type":"int","value":9007199254740993,"default":null,"source":"settings","locked":false,"restart":false}],
	 "facts":[],"pictures":[]}`})
	r := f.run(Get, "huge.key")
	if r.code != 0 || !strings.Contains(r.stdout, "value=9007199254740993\n") || !strings.Contains(r.stdout, "default=\n") {
		t.Errorf("exit %d, stdout %q", r.code, r.stdout)
	}
	r = f.run(Get)
	if !strings.Contains(r.stdout, "9007199254740993") {
		t.Errorf("table: %q", r.stdout)
	}
}

// A 403 with an account key tells the person what key is needed; with an admin key it does not.
func TestA403HintsAtTheAdminKeyOnlyForOtherKeys(t *testing.T) {
	forbidden := reply{403, `{"error":{"code":"forbidden","message":"an installation admin is required"}}`}
	for _, tc := range []struct {
		name, key string
		hint      bool
	}{{"account key", accountKey, true}, {"admin key", adminKey, false}} {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t, tc.key)
			f := newFake(t)
			f.on("GET", settingsPath, forbidden)
			r := f.run(Get)
			if r.code != 3 {
				t.Errorf("exit %d, stderr %q", r.code, r.stderr)
			}
			if got := strings.Contains(r.stderr, hint); got != tc.hint {
				t.Errorf("hint present %v, want %v; stderr %q", got, tc.hint, r.stderr)
			}
		})
	}
}
