// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
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
	srv, _, s := serveDir(t, t.TempDir())
	return srv, s
}

// serveDir serves the real API from the store in dir, on loopback, the way the server does. It returns the store too,
// for a test that makes rows the API has no route for yet.
func serveDir(t *testing.T, dir string) (*httptest.Server, *store.Store, *api.Server) {
	t.Helper()
	cfg := config.Defaults()
	cfg.DataDir = dir
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
	return srv, st, s
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

// undeclaredCalls returns the routes, as the route table writes them, that a command called and that are neither its
// route, a route it calls after that, nor a read it declares. Any such call is a fault: the command reaches a route the
// table does not name.
func undeclaredCalls(c remoteCommand, routes []string) []string {
	allowed := append(c.routes(), c.reads...)
	var out []string
	for _, r := range routes {
		if !slices.Contains(allowed, r) {
			out = append(out, r)
		}
	}
	return out
}

// probes is how the recording check calls each remote command that reaches a route and needs no fixture. The file
// commands need one, and are in fileProbes, and the settings commands, which need an admin key, in settingsProbes
// (files_probe_test.go). A new remote command with a route needs its probe in
// one of the two; the check fails until it has one.
var probes = map[string][]string{
	"info":     {},
	"api spec": {},
}

// TestRemoteCommandsCallTheirRoute: each remote command runs against the real server through a recording handler. Every
// call it makes is its own route, or a read it declares (remote.go, reads), and its route is among them (design
// section 5.2). A command that calls anything else fails the build.
func TestRemoteCommandsCallTheirRoute(t *testing.T) {
	isolateRemote(t)
	p := newFileProbe(t)
	for _, c := range remoteCommands {
		if c.route == "" || c.notProbed != "" {
			continue
		}
		p.answer = ""
		var args []string
		restoreKey := func() {}
		if fixture, ok := settingsProbes[c.name]; ok {
			restoreKey = p.useAdminKey()
			args = fixture(p)
		} else if fixture, ok := fileProbes[c.name]; ok {
			args = fixture(p)
		} else if static, ok := probes[c.name]; ok {
			args = static
		} else {
			t.Errorf("remote command %q reaches %s and has no probe in this test", c.name, c.route)
			continue
		}
		p.reset()
		var out, errOut bytes.Buffer
		full := append(append([]string{}, strings.Fields(c.name)...), args...)
		full = append(full, "--url", p.recURL)
		restore := typeAt(p.answer)
		code, matched := remote(full, &out, &errOut)
		restore()
		restoreKey()
		if !matched || code != 0 {
			t.Errorf("%s: exit %d, matched %v, stderr %q", c.name, code, matched, errOut.String())
			continue
		}
		var routes []string
		for _, call := range p.recorded() {
			routes = append(routes, routeOf(call))
		}
		for _, r := range undeclaredCalls(c, routes) {
			t.Errorf("%s: call %s is neither its route nor a read it declares", c.name, r)
		}
		if !slices.Contains(routes, c.route) {
			t.Errorf("%s: its route %s was never called; calls %v", c.name, c.route, routes)
		}
	}
}

// TestUndeclaredCallsAreReported: a call that is neither the command's route, a route it calls after that, nor a read it
// declares is reported; a declared read is not.
func TestUndeclaredCallsAreReported(t *testing.T) {
	c := remoteCommand{name: "synthetic", route: "GET /api/v1/a", more: []string{"POST /api/v1/m"},
		reads: []string{"GET /api/v1/r"}}
	got := undeclaredCalls(c, []string{"GET /api/v1/a", "GET /api/v1/r", "POST /api/v1/m", "DELETE /api/v1/x"})
	if !slices.Equal(got, []string{"DELETE /api/v1/x"}) {
		t.Errorf("undeclaredCalls = %v, want [DELETE /api/v1/x]", got)
	}
	if got := undeclaredCalls(c, []string{"GET /api/v1/a", "GET /api/v1/r"}); len(got) != 0 {
		t.Errorf("declared calls reported: %v", got)
	}
	if got := undeclaredCalls(c, nil); len(got) != 0 {
		t.Errorf("no calls reported: %v", got)
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

// TestEveryRemoteCommandIsReachedFromTheTop: each command of the remote table is reached by its words from run, the
// program's own entry, and prints its flags for -h.
func TestEveryRemoteCommandIsReachedFromTheTop(t *testing.T) {
	isolateRemote(t)
	for _, c := range remoteCommands {
		code, out, errOut := runCmd(t, append(strings.Fields(c.name), "-h")...)
		if code != 0 || !strings.Contains(out, "usage: selfcloud "+c.name) {
			t.Errorf("%s -h: exit %d, stdout %.60q, stderr %.60q", c.name, code, out, errOut)
		}
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
