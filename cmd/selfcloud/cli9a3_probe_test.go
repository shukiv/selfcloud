// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
)

// cli9a3 registers the installation admin commands' probes in adminProbes, which admin_probe_test.go declares.
func init() {
	adminProbes["admin ls"] = func(p *fileProbe) []string { return nil }
	adminProbes["admin add"] = func(p *fileProbe) []string {
		return []string{"--verification", "link", "probe-admin-add@example.test"}
	}
	adminProbes["admin rm"] = func(p *fileProbe) []string {
		adminID := valueOf(p.t, p.do("admin", "add", "--verification", "link", "probe-admin-rm@example.test"), "id=")
		p.answer = adminID + "\n"
		return []string{adminID}
	}
}

// TestAdminHelpListsTheRemoteCommands: admin help covers the commands that run against a server too, with the words
// that reach them from the top.
func TestAdminHelpListsTheRemoteCommands(t *testing.T) {
	isolateRemote(t)
	code, out, errOut := runCmd(t, "admin", "help")
	if code != 0 {
		t.Fatalf("admin help: exit %d, stderr %q", code, errOut)
	}
	for _, want := range []string{"admin ls", "admin add", "admin rm", "installation admin key"} {
		if !strings.Contains(out, want) {
			t.Errorf("usage does not mention %q:\n%s", want, out)
		}
	}
	for _, local := range adminCommands {
		if !strings.Contains(out, local.help) {
			t.Errorf("usage does not list the local command %q:\n%s", local.name, out)
		}
	}
}

// TestAdminLocalCommandsStayLocal: every local admin command still reaches its own usage with -h, and none of them calls
// the server the remote commands would use. generate-key prints one key and reads no flag.
func TestAdminLocalCommandsStayLocal(t *testing.T) {
	isolateRemote(t)
	var reached atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		http.Error(w, "a local command called the server", http.StatusTeapot)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("SELFCLOUD_URL", srv.URL)

	for _, c := range adminCommands {
		c := c
		t.Run(c.name, func(t *testing.T) {
			code, out, errOut := runCmd(t, "admin", c.name, "-h")
			if code != 0 {
				t.Fatalf("admin %s -h: exit %d, stderr %q", c.name, code, errOut)
			}
			if c.name == "generate-key" {
				lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
				if len(lines) != 1 || strings.TrimSpace(lines[0]) == "" {
					t.Fatalf("generate-key printed %q, want one key on one line", out)
				}
				return
			}
			if !strings.Contains(errOut, "admin "+c.name) {
				t.Fatalf("admin %s -h did not reach the local command: stderr %q", c.name, errOut)
			}
		})
	}
	if n := reached.Load(); n != 0 {
		t.Fatalf("the server got %d requests from local commands", n)
	}
}

// TestAdminRemoteCommandsOpenNoDatabase: the three installation admin commands reach a server, and the configuration's
// data directory is never made, so the local database was never opened.
func TestAdminRemoteCommandsOpenNoDatabase(t *testing.T) {
	isolateRemote(t)
	const adminKey = "sc_admin_TESTKEY_0123456789abcdefghijklmnopqrstuvwxyz"
	t.Setenv("SELFCLOUD_TOKEN", adminKey)

	dataDir := filepath.Join(t.TempDir(), "data")
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, []byte("data_dir: "+dataDir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SELFCLOUD_CONFIG", cfg)

	usr := id.New("usr")
	var mu sync.Mutex
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/admins":
			fmt.Fprint(w, `{"items":[]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/admins":
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"id":"%s","email":"probe-remote@example.test","display_name":"","status":"pending",`+
				`"verification":{"method":"link","url":"https://selfcloud.example/set-password#secret",`+
				`"expires_at":"2026-11-13T10:00:00Z"}}`, id.New("usr"))
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v1/admins/"):
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected call", http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("SELFCLOUD_URL", srv.URL)

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"admin ls", []string{"admin", "ls"}, "GET /api/v1/admins"},
		{"admin add", []string{"admin", "add", "--verification", "link", "probe-remote@example.test"}, "POST /api/v1/admins"},
		{"admin rm", []string{"admin", "rm", usr, "--yes"}, "DELETE /api/v1/admins/" + usr},
	}
	for _, tc := range cases {
		mu.Lock()
		calls = nil
		mu.Unlock()
		code, out, errOut := runCmd(t, tc.args...)
		if code != 0 {
			t.Fatalf("%s: exit %d, stdout %q, stderr %q", tc.name, code, out, errOut)
		}
		mu.Lock()
		got := append([]string(nil), calls...)
		mu.Unlock()
		if len(got) != 1 || got[0] != tc.want {
			t.Fatalf("%s called %v, want [%s]", tc.name, got, tc.want)
		}
		if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
			t.Fatalf("%s touched the data directory: %v", tc.name, err)
		}
	}
}
