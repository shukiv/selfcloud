// SPDX-License-Identifier: AGPL-3.0-or-later

package apispec

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const spec = "openapi: 3.0.3\ninfo:\n  title: test\n"

func isolate(t *testing.T) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "profiles")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SELFCLOUD_PROFILES_DIR", dir)
	t.Setenv("SELFCLOUD_PROFILE", "")
	t.Setenv("SELFCLOUD_URL", "")
	t.Setenv("SELFCLOUD_TOKEN", "")
}

func specServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/openapi.yaml" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write([]byte(spec))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSpecToStdout(t *testing.T) {
	isolate(t)
	srv := specServer(t)
	var out, errOut bytes.Buffer
	if code := Run([]string{"--url", srv.URL}, &out, &errOut); code != 0 {
		t.Fatalf("exit = %d, stderr %q", code, errOut.String())
	}
	if out.String() != spec {
		t.Errorf("stdout = %q, want the spec", out.String())
	}
}

func TestSpecToNewFile(t *testing.T) {
	isolate(t)
	srv := specServer(t)
	path := filepath.Join(t.TempDir(), "openapi.yaml")
	var out, errOut bytes.Buffer
	if code := Run([]string{"--url", srv.URL, "--out", path}, &out, &errOut); code != 0 {
		t.Fatalf("exit = %d, stderr %q", code, errOut.String())
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != spec {
		t.Errorf("file = %q, %v", data, err)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want nothing when --out is given", out.String())
	}
}

// TestSpecNeverOverwritesWithoutFlag: an existing file is refused, and it is not changed.
func TestSpecNeverOverwritesWithoutFlag(t *testing.T) {
	isolate(t)
	srv := specServer(t)
	path := filepath.Join(t.TempDir(), "openapi.yaml")
	if err := os.WriteFile(path, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := Run([]string{"--url", srv.URL, "--out", path}, &out, &errOut); code != 2 {
		t.Errorf("exit = %d, want 2 for an existing file", code)
	}
	if data, _ := os.ReadFile(path); string(data) != "mine" {
		t.Errorf("the file was changed to %q", data)
	}
}

func TestSpecOverwriteWithFlag(t *testing.T) {
	isolate(t)
	srv := specServer(t)
	path := filepath.Join(t.TempDir(), "openapi.yaml")
	if err := os.WriteFile(path, []byte("much longer old content than the spec itself, to test truncation"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := Run([]string{"--url", srv.URL, "--out", path, "--overwrite"}, &out, &errOut); code != 0 {
		t.Fatalf("exit = %d, stderr %q", code, errOut.String())
	}
	if data, _ := os.ReadFile(path); string(data) != spec {
		t.Errorf("file = %q, want the spec with the old bytes gone", data)
	}
}

// TestOverwriteFailureKeepsTheOriginal: the replacement is written beside the file and renamed over it, so a write that
// fails part-way leaves the original whole and no temporary file behind.
func TestOverwriteFailureKeepsTheOriginal(t *testing.T) {
	isolate(t)
	srv := specServer(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "openapi.yaml")
	if err := os.WriteFile(path, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeFault = func() error { return errors.New("simulated disk full") }
	t.Cleanup(func() { writeFault = func() error { return nil } })
	var out, errOut bytes.Buffer
	if code := Run([]string{"--url", srv.URL, "--out", path, "--overwrite"}, &out, &errOut); code == 0 {
		t.Fatal("a failed write was reported as done")
	}
	if data, _ := os.ReadFile(path); string(data) != "mine" {
		t.Errorf("the original was lost after a failed write: %q", data)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("a failed write left %d entries in the directory; want only the original", len(entries))
	}
}

// TestSpecNeverFollowsASymlink: a link at the destination is refused with and without --overwrite, and its target is not
// written.
func TestSpecNeverFollowsASymlink(t *testing.T) {
	isolate(t)
	srv := specServer(t)
	// The target is inside the directory: a root would refuse one outside it anyway, and only the link check stops a write
	// through a link that stays inside.
	dir := t.TempDir()
	target := filepath.Join(dir, "victim")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "openapi.yaml")
	// A relative target: os.Root refuses an absolute one by itself, which would hide a missing link check.
	if err := os.Symlink("victim", link); err != nil {
		t.Fatal(err)
	}
	for _, extra := range [][]string{{}, {"--overwrite"}} {
		var out, errOut bytes.Buffer
		args := append([]string{"--url", srv.URL, "--out", link}, extra...)
		if code := Run(args, &out, &errOut); code == 0 {
			t.Errorf("args %v: exit 0 through a symbolic link", extra)
		}
	}
	if data, _ := os.ReadFile(target); string(data) != "keep" {
		t.Errorf("the link's target was written: %q", data)
	}
}

func TestSpecRefusesNamesThatAreNotFiles(t *testing.T) {
	isolate(t)
	srv := specServer(t)
	dir := t.TempDir()
	for _, p := range []string{filepath.Join(dir, "."), filepath.Join(dir, ".."), dir + string(filepath.Separator)} {
		var out, errOut bytes.Buffer
		if code := Run([]string{"--url", srv.URL, "--out", p}, &out, &errOut); code != 2 {
			t.Errorf("--out %q: exit = %d, want 2", p, code)
		}
	}
}

func TestSpecRefusesJSONAndArguments(t *testing.T) {
	isolate(t)
	srv := specServer(t)
	var out, errOut bytes.Buffer
	if code := Run([]string{"--url", srv.URL, "--json"}, &out, &errOut); code != 2 {
		t.Errorf("--json: exit = %d, want 2", code)
	}
	if code := Run([]string{"--url", srv.URL, "extra"}, &out, &errOut); code != 2 {
		t.Errorf("an argument: exit = %d, want 2", code)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q after a refusal", out.String())
	}
}

// TestSpecRefusesAnAnswerThatIsNotYAML: a 200 with an HTML page (a proxy, or the web interface's fallback) is not the
// document, and nothing is printed or written.
func TestSpecRefusesAnAnswerThatIsNotYAML(t *testing.T) {
	isolate(t)
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><title>app</title>"))
	}))
	defer page.Close()
	path := filepath.Join(t.TempDir(), "openapi.yaml")
	var out, errOut bytes.Buffer
	if code := Run([]string{"--url", page.URL}, &out, &errOut); code == 0 || out.Len() != 0 {
		t.Errorf("an HTML page as the spec: exit %d, stdout %q; want a failure and no output", code, out.String())
	}
	if code := Run([]string{"--url", page.URL, "--out", path}, &out, &errOut); code == 0 {
		t.Error("an HTML page was written as the spec")
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("a file was written for an HTML page")
	}
}

func TestSpecFailureWritesNoFile(t *testing.T) {
	isolate(t)
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusNotFound)
	}))
	defer broken.Close()
	path := filepath.Join(t.TempDir(), "openapi.yaml")
	var out, errOut bytes.Buffer
	if code := Run([]string{"--url", broken.URL, "--out", path}, &out, &errOut); code == 0 {
		t.Error("a 404 for the spec was reported as done")
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("a file was written for a failed fetch")
	}
	if strings.Contains(errOut.String(), "\n\n") {
		t.Errorf("stderr = %q", errOut.String())
	}
}
