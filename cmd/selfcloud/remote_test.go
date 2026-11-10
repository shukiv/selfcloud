// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/shukiv/selfcloud/internal/api"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/store"
)

// Tests in this file set environment variables, so they do not run in parallel.

// isolateRemote gives the remote commands a private profiles directory and clears the variables the resolution reads.
func isolateRemote(t *testing.T) {
	t.Helper()
	t.Setenv("SELFCLOUD_PROFILES_DIR", filepath.Join(t.TempDir(), "profiles"))
	t.Setenv("SELFCLOUD_PROFILE", "")
	t.Setenv("SELFCLOUD_URL", "")
	t.Setenv("SELFCLOUD_TOKEN", "")
}

// newRealServer serves the real API from a temporary store, the way the server does, on loopback.
func newRealServer(t *testing.T) (*httptest.Server, *api.Server) {
	t.Helper()
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	cfg.Storage.MinFree = 0
	cfg.Store.Path = filepath.Join(cfg.DataDir, "selfcloud.db")
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), store.Options{Path: cfg.Store.Path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s, err := api.New(api.Options{Config: cfg, Store: st})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv, s
}

// recorder wraps a handler and keeps the method and path of each call, the way a recording transport would.
type recorder struct {
	mu    sync.Mutex
	calls []string
	next  http.Handler
}

func (r *recorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	r.mu.Unlock()
	r.next.ServeHTTP(w, req)
}

// probes is how the recording check calls each remote command that reaches a route. A new remote command with a route
// needs its probe here; the check fails until it has one.
var probes = map[string][]string{
	"info":     {},
	"api spec": {},
}

// TestRemoteCommandsCallTheirRoute: each remote command runs against the real server through a recording handler, and
// the only call it makes is the route its table entry names (design section 5.2).
func TestRemoteCommandsCallTheirRoute(t *testing.T) {
	isolateRemote(t)
	_, s := newRealServer(t)
	rec := &recorder{next: s.Handler()}
	recorded := httptest.NewServer(rec)
	defer recorded.Close()
	for _, c := range remoteCommands {
		if c.route == "" {
			continue
		}
		args, ok := probes[c.name]
		if !ok {
			t.Errorf("remote command %q reaches %s and has no probe in this test", c.name, c.route)
			continue
		}
		rec.mu.Lock()
		rec.calls = nil
		rec.mu.Unlock()
		var out, errOut bytes.Buffer
		words := strings.Fields(c.name)
		full := append(append([]string{}, words...), args...)
		full = append(full, "--url", recorded.URL)
		code, ok2 := remote(full, &out, &errOut)
		if !ok2 || code != 0 {
			t.Errorf("%s: exit %d, matched %v, stderr %q", c.name, code, ok2, errOut.String())
			continue
		}
		rec.mu.Lock()
		calls := append([]string(nil), rec.calls...)
		rec.mu.Unlock()
		if !reflect.DeepEqual(calls, []string{c.route}) {
			t.Errorf("%s: recorded calls %v, want exactly [%s]", c.name, calls, c.route)
		}
	}
}

func TestInfoOnTheRealServerPrintsItsVersion(t *testing.T) {
	isolateRemote(t)
	srv, _ := newRealServer(t)
	var out, errOut bytes.Buffer
	if code := run([]string{"info", "--url", srv.URL}, &out, &errOut); code != 0 {
		t.Fatalf("info: exit %d, stderr %q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "version=") || !strings.Contains(out.String(), "password_min_length=") {
		t.Errorf("stdout = %q", out.String())
	}
}

func TestAPISpecOnTheRealServerIsTheDocument(t *testing.T) {
	isolateRemote(t)
	srv, _ := newRealServer(t)
	var out, errOut bytes.Buffer
	if code := run([]string{"api", "spec", "--url", srv.URL}, &out, &errOut); code != 0 {
		t.Fatalf("api spec: exit %d, stderr %q", code, errOut.String())
	}
	if !strings.HasPrefix(out.String(), "openapi:") {
		t.Errorf("stdout does not start as an OpenAPI document: %.60q", out.String())
	}
}

// TestRemoteDispatch: the words select a command, and a word that selects none is a usage error.
func TestRemoteDispatch(t *testing.T) {
	isolateRemote(t)
	for _, args := range [][]string{{"profile"}, {"profile", "nope"}, {"api"}, {"api", "nope"}} {
		if code, _, _ := runCmd(t, args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	if code, out, _ := runCmd(t, "api", "spec", "-h"); code != 0 || !strings.Contains(out, "-out") {
		t.Errorf("api spec -h: exit %d, stdout %q", code, out)
	}
	if code, _, _ := runCmd(t, "profile", "ls", "extra"); code != 2 {
		t.Errorf("profile ls with an argument: exit %d, want 2", code)
	}
}

// TestExitCodesReachTheTop: the exit code a command returns is the one run returns.
func TestExitCodesReachTheTop(t *testing.T) {
	isolateRemote(t)
	srv, _ := newRealServer(t)
	if code, _, _ := runCmd(t, "info", "--url", srv.URL); code != 0 {
		t.Errorf("done: exit %d, want 0", code)
	}
	if code, _, _ := runCmd(t, "info", "--url", "https://127.0.0.1:1/"); code != 1 {
		t.Errorf("unreachable: exit %d, want 1", code)
	}
	if code, _, _ := runCmd(t, "info"); code != 2 {
		t.Errorf("no address: exit %d, want 2", code)
	}
	if code, _, _ := runCmd(t, "profile", "rm", "nope"); code != 4 {
		t.Errorf("a missing profile: exit %d, want 4", code)
	}
	if code, _, _ := runCmd(t, "api", "spec", "--url", srv.URL+"/no-such-path-anywhere", "--out", filepath.Join(t.TempDir(), "x.yaml")); code == 0 {
		t.Errorf("a 404 for the spec: exit %d, want a failure", code)
	}
}
