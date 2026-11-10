// SPDX-License-Identifier: AGPL-3.0-or-later

package profilecmd

import (
	"bytes"
	"encoding/pem"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/cli/profile"
)

// Tests in this package replace package variables, so they do not run in parallel.

const (
	accountKey = "sc_acct_PROFILECMDVALUE0123456789"
	adminKey   = "sc_admin_PROFILECMDADMIN0123456789"
)

// isolate points the profiles directory at a path that does not exist yet, and gives the stdin seam a pipe by default.
func isolate(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "profiles")
	t.Setenv("SELFCLOUD_PROFILES_DIR", dir)
	t.Setenv("SELFCLOUD_PROFILE", "")
	t.Setenv("SELFCLOUD_URL", "")
	t.Setenv("SELFCLOUD_TOKEN", "")
	oldStdin, oldTerm := Stdin, IsTerminal
	Stdin = strings.NewReader("")
	IsTerminal = func(io.Reader) bool { return false }
	t.Cleanup(func() { Stdin, IsTerminal = oldStdin, oldTerm })
	return dir
}

func pipe(t *testing.T, value string) {
	t.Helper()
	Stdin = strings.NewReader(value + "\n")
}

func tokenFile(t *testing.T, value string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(p, []byte(value+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s differs.\n--- got\n%s--- want\n%s", name, got, want)
	}
}

// add runs profile add with the given arguments and fails the test on a non-zero exit.
func add(t *testing.T, args ...string) {
	t.Helper()
	var out, errOut bytes.Buffer
	if code := Add(args, &out, &errOut); code != 0 {
		t.Fatalf("add %v: exit %d, stderr %q", args, code, errOut.String())
	}
}

func fileText(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestAddStoresAPrivateProfile(t *testing.T) {
	dir := isolate(t)
	tf := tokenFile(t, accountKey, 0o600)
	var out, errOut bytes.Buffer
	if code := Add([]string{"home", "--url", "https://selfcloud.example", "--token-file", tf}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "home") || strings.Contains(out.String(), accountKey) {
		t.Errorf("stdout = %q", out.String())
	}
	fi, err := os.Stat(filepath.Join(dir, "profiles.json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("profiles.json mode = %v, %v; want 0600", fi.Mode().Perm(), err)
	}
	f, err := profile.Open(dir).Load()
	if err != nil {
		t.Fatal(err)
	}
	p := f.Profiles["home"]
	if p.Token != accountKey || p.Kind != profile.KindAccount || p.URL != "https://selfcloud.example" || f.Current != "home" {
		t.Errorf("stored profile = %+v, current %q", p, f.Current)
	}
}

// TestCredentialFromStdinNotArgv: the key is read from a pipe; a key given as a flag is an unknown flag.
func TestCredentialFromStdinNotArgv(t *testing.T) {
	dir := isolate(t)
	pipe(t, adminKey)
	var out, errOut bytes.Buffer
	if code := Add([]string{"ops", "--url", "https://selfcloud.example"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut.String())
	}
	if f, _ := profile.Open(dir).Load(); f.Profiles["ops"].Kind != profile.KindAdmin {
		t.Errorf("kind = %q, want admin", f.Profiles["ops"].Kind)
	}

	var out2, errOut2 bytes.Buffer
	code := Add([]string{"x", "--url", "https://selfcloud.example", "--token", accountKey}, &out2, &errOut2)
	if code != 2 || strings.Contains(errOut2.String(), accountKey) || strings.Contains(out2.String(), accountKey) {
		t.Errorf("a key in argv: exit %d, stderr %q; want 2 and no key", code, errOut2.String())
	}
}

// TestAddRefusals: each refusal leaves no profiles file behind, and exits 2.
func TestAddRefusals(t *testing.T) {
	cases := map[string]struct {
		args  []string
		stdin string
		term  bool
	}{
		"bad name":          {args: []string{"Bad Name", "--url", "https://x.example"}, stdin: accountKey},
		"no url":            {args: []string{"home"}, stdin: accountKey},
		"plain http remote": {args: []string{"home", "--url", "http://192.0.2.5"}, stdin: accountKey},
		"skip verify":       {args: []string{"home", "--url", "https://x.example", "--insecure-skip-verify"}, stdin: accountKey},
		"not a key":         {args: []string{"home", "--url", "https://x.example"}, stdin: "sct_NOTAKEY0123"},
		"empty stdin":       {args: []string{"home", "--url", "https://x.example"}, stdin: ""},
		"terminal":          {args: []string{"home", "--url", "https://x.example"}, term: true},
		"userinfo":          {args: []string{"home", "--url", "https://u:" + accountKey + "@x.example"}, stdin: accountKey},
		"bad ca file":       {args: []string{"home", "--url", "https://x.example", "--ca-file", "/nonexistent/ca.pem"}, stdin: accountKey},
	}
	for name, c := range cases {
		dir := isolate(t)
		pipe(t, c.stdin)
		IsTerminal = func(io.Reader) bool { return c.term }
		var out, errOut bytes.Buffer
		code := Add(c.args, &out, &errOut)
		if code != 2 {
			t.Errorf("%s: exit = %d, want 2 (stderr %q)", name, code, errOut.String())
		}
		if _, err := os.Stat(filepath.Join(dir, "profiles.json")); err == nil {
			t.Errorf("%s: a profiles file was written by a refused add", name)
		}
		if strings.Contains(errOut.String(), accountKey) || strings.Contains(out.String(), accountKey) {
			t.Errorf("%s: the credential was printed", name)
		}
	}
}

func TestAddRefusesAnExistingName(t *testing.T) {
	dir := isolate(t)
	pipe(t, accountKey)
	add(t, "home", "--url", "https://selfcloud.example")
	before := fileText(t, dir)
	pipe(t, adminKey)
	var out, errOut bytes.Buffer
	if code := Add([]string{"home", "--url", "https://other.example"}, &out, &errOut); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if fileText(t, dir) != before {
		t.Error("a refused add changed the existing profile")
	}
}

func TestAddInsecureHTTPWarnsAndSavesTheAddress(t *testing.T) {
	dir := isolate(t)
	pipe(t, accountKey)
	var out, errOut bytes.Buffer
	if code := Add([]string{"lan", "--url", "http://192.0.2.5", "--insecure-http"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "not encrypted") {
		t.Errorf("no warning on stderr: %q", errOut.String())
	}
	if f, _ := profile.Open(dir).Load(); f.Profiles["lan"].URL != "http://192.0.2.5" {
		t.Errorf("stored URL = %q", f.Profiles["lan"].URL)
	}
}

func TestAddLoopbackHTTPNeedsNoFlag(t *testing.T) {
	isolate(t)
	pipe(t, accountKey)
	var out, errOut bytes.Buffer
	if code := Add([]string{"local", "--url", "http://127.0.0.1:8080"}, &out, &errOut); code != 0 {
		t.Errorf("exit %d, stderr %q", code, errOut.String())
	}
}

func TestAddSavesTheCAFile(t *testing.T) {
	dir := isolate(t)
	srv := httptest.NewTLSServer(nil)
	defer srv.Close()
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(caPath, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	pipe(t, accountKey)
	add(t, "private", "--url", srv.URL, "--ca-file", caPath)
	if f, _ := profile.Open(dir).Load(); f.Profiles["private"].CAFile != caPath {
		t.Errorf("ca_file = %q, want %q", f.Profiles["private"].CAFile, caPath)
	}
}

func TestUseSetsTheCurrentProfile(t *testing.T) {
	dir := isolate(t)
	pipe(t, accountKey)
	add(t, "home", "--url", "https://a.example")
	pipe(t, adminKey)
	add(t, "work", "--url", "https://b.example")
	var out, errOut bytes.Buffer
	if code := Use([]string{"work"}, &out, &errOut); code != 0 {
		t.Fatalf("use: exit %d, stderr %q", code, errOut.String())
	}
	if f, _ := profile.Open(dir).Load(); f.Current != "work" {
		t.Errorf("current = %q, want work", f.Current)
	}
	if code := Use([]string{"nope"}, &out, &errOut); code != 4 {
		t.Errorf("use of a missing profile: exit %d, want 4", code)
	}
}

// TestLsOutputIsGolden: the table and the JSON of profile ls. Neither holds a credential.
func TestLsOutputIsGolden(t *testing.T) {
	isolate(t)
	pipe(t, accountKey)
	add(t, "home", "--url", "https://selfcloud.example")
	pipe(t, adminKey)
	add(t, "work", "--url", "https://work.example")
	var out, errOut bytes.Buffer
	if code := Use([]string{"work"}, &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	out.Reset()
	if code := List(nil, &out, &errOut); code != 0 {
		t.Fatalf("ls: exit %d, stderr %q", code, errOut.String())
	}
	golden(t, "ls.golden", out.String())
	if strings.Contains(out.String(), accountKey) || strings.Contains(out.String(), adminKey) {
		t.Error("ls printed a credential")
	}

	out.Reset()
	if code := List([]string{"--json"}, &out, &errOut); code != 0 {
		t.Fatalf("ls --json: exit %d", code)
	}
	golden(t, "ls.json.golden", out.String())
	if strings.Contains(out.String(), accountKey) || strings.Contains(out.String(), adminKey) {
		t.Error("ls --json printed a credential")
	}
}

func TestLsWithNoFile(t *testing.T) {
	isolate(t)
	var out, errOut bytes.Buffer
	if code := List(nil, &out, &errOut); code != 0 {
		t.Errorf("ls with no profiles: exit %d, stderr %q", code, errOut.String())
	}
	out.Reset()
	if code := List([]string{"--json"}, &out, &errOut); code != 0 || out.String() != "[]\n" {
		t.Errorf("ls --json with no profiles = %q, exit %d; want []", out.String(), code)
	}
}

// keyShaped are values a user might paste where a profile name belongs. The first is too long for the name rule; the
// second passes the name rule, so the name rule alone does not keep it out of a message.
var keyShaped = []string{"sc_acct_Probe0123456789abcdefghijklmnop", "sc_admin_x"}

// TestNameThatIsAKeyIsNotEchoed: a key given where a profile name goes is not printed, by use, rm or add.
func TestNameThatIsAKeyIsNotEchoed(t *testing.T) {
	isolate(t)
	pipe(t, accountKey)
	add(t, "home", "--url", "https://selfcloud.example")
	for _, k := range keyShaped {
		var out, errOut bytes.Buffer
		Use([]string{k}, &out, &errOut)
		Remove([]string{k}, &out, &errOut)
		Add([]string{k, "--url", "https://selfcloud.example"}, &out, &errOut)
		if strings.Contains(out.String(), k) || strings.Contains(errOut.String(), k) {
			t.Errorf("name %q was printed: stdout %q, stderr %q", k, out.String(), errOut.String())
		}
	}
	var out, errOut bytes.Buffer
	if code := Use([]string{"nosuch"}, &out, &errOut); code != 4 || !strings.Contains(errOut.String(), "nosuch") {
		t.Errorf("a valid name that is missing: exit %d, stderr %q; want 4 and the name", code, errOut.String())
	}
}

// TestRefusedCredentialIsNeverEchoed: a value that is not a key, given to profile add by a file or by standard input, is
// refused without being printed. This also catches a refusal that appends the value.
func TestRefusedCredentialIsNeverEchoed(t *testing.T) {
	isolate(t)
	value := "sct_REFUSEDSHAPED0123456789abcdef"
	pipe(t, value)
	var out, errOut bytes.Buffer
	if code := Add([]string{"x", "--url", "https://selfcloud.example"}, &out, &errOut); code != 2 {
		t.Errorf("stdin: exit %d, want 2", code)
	}
	tf := tokenFile(t, value, 0o600)
	out.Reset()
	errOut.Reset()
	if code := Add([]string{"x", "--url", "https://selfcloud.example", "--token-file", tf}, &out, &errOut); code != 2 {
		t.Errorf("token file: exit %d, want 2", code)
	}
	if strings.Contains(out.String(), value) || strings.Contains(errOut.String(), value) {
		t.Errorf("the refused credential was printed: stdout %q, stderr %q", out.String(), errOut.String())
	}
}

func TestRemoveOfTheCurrentProfileClearsIt(t *testing.T) {
	dir := isolate(t)
	pipe(t, accountKey)
	add(t, "home", "--url", "https://selfcloud.example")
	var out, errOut bytes.Buffer
	if code := Remove([]string{"home"}, &out, &errOut); code != 0 {
		t.Fatalf("rm: exit %d, stderr %q", code, errOut.String())
	}
	f, err := profile.Open(dir).Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Profiles["home"]; ok || f.Current != "" {
		t.Errorf("after rm: profiles %v, current %q", f.Profiles, f.Current)
	}
	if !strings.Contains(errOut.String(), "no profile is current") {
		t.Errorf("stderr = %q, want the note about the current profile", errOut.String())
	}
	if code := Remove([]string{"home"}, &out, &errOut); code != 4 {
		t.Errorf("rm of a missing profile: exit %d, want 4", code)
	}
}

// TestInsecureHTTPHelpSaysNotSaved: the flag is not stored, so the help must not say it is.
func TestInsecureHTTPHelpSaysNotSaved(t *testing.T) {
	var out, errOut bytes.Buffer
	Add([]string{"-h"}, &out, &errOut)
	help := out.String() + errOut.String()
	if !strings.Contains(help, "not saved") || strings.Contains(help, "(warns; saved)") {
		t.Errorf("the --insecure-http help does not say the flag is not saved:\n%s", help)
	}
}
