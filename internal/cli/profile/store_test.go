// SPDX-License-Identifier: AGPL-3.0-or-later

package profile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const token = "sc_acct_TOKENVALUE0123456789"

// privateDir is a fresh directory with mode 0700, as the store requires. t.TempDir's own mode is not relied on.
func privateDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "p")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func sample() File {
	return File{Version: 1, Current: "home", Profiles: map[string]Profile{
		"home": {URL: "https://selfcloud.example", Kind: KindAccount, Token: token, CAFile: "/etc/ca.pem"},
	}}
}

func saveSample(t *testing.T, dir string) *Store {
	t.Helper()
	s := Open(dir)
	if err := s.Save(sample()); err != nil {
		t.Fatal(err)
	}
	return s
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func TestMissingFileIsNoProfiles(t *testing.T) {
	t.Parallel()
	s := Open(filepath.Join(t.TempDir(), "not-yet"))
	f, err := s.Load()
	if err != nil {
		t.Fatalf("a missing file is no profiles, not an error: %v", err)
	}
	if len(f.Profiles) != 0 || f.Current != "" {
		t.Errorf("Load = %+v, want no profiles", f)
	}
}

func TestSaveCreatesA0700DirAnd0600File(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "selfcloud")
	saveSample(t, dir)
	if m := mode(t, dir); m != 0o700 {
		t.Errorf("directory mode = %o, want 700", m)
	}
	if m := mode(t, filepath.Join(dir, "profiles.json")); m != 0o600 {
		t.Errorf("file mode = %o, want 600", m)
	}
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	dir := privateDir(t)
	saveSample(t, dir)
	f, err := Open(dir).Load()
	if err != nil {
		t.Fatal(err)
	}
	if f.Current != "home" || f.Profiles["home"].Token != token || f.Profiles["home"].CAFile != "/etc/ca.pem" {
		t.Errorf("Load = %+v", f)
	}
}

// TestFileModeIsRefused: a file with any group or other bit is refused, and the message names the chmod that fixes it.
func TestFileModeIsRefused(t *testing.T) {
	t.Parallel()
	for _, m := range []os.FileMode{0o644, 0o640, 0o604, 0o620, 0o602} {
		dir := privateDir(t)
		saveSample(t, dir)
		path := filepath.Join(dir, "profiles.json")
		if err := os.Chmod(path, m); err != nil {
			t.Fatal(err)
		}
		_, err := Open(dir).Load()
		if err == nil {
			t.Errorf("mode %o: loaded, want a refusal", m)
			continue
		}
		if !strings.Contains(err.Error(), "chmod 600") {
			t.Errorf("mode %o: message %q does not give the chmod", m, err.Error())
		}
		if strings.Contains(err.Error(), token) {
			t.Errorf("mode %o: the refusal names the credential", m)
		}
	}
}

func TestPrivateFileLoads(t *testing.T) {
	t.Parallel()
	dir := privateDir(t)
	saveSample(t, dir)
	if _, err := Open(dir).Load(); err != nil {
		t.Fatalf("a 0600 file was refused: %v", err)
	}
}

func TestDirectoryWithGroupOrOtherBitsIsRefused(t *testing.T) {
	t.Parallel()
	dir := privateDir(t)
	saveSample(t, dir)
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Open(dir).Load()
	if err == nil || !strings.Contains(err.Error(), "chmod 700") {
		t.Errorf("err = %v, want a refusal with the chmod 700", err)
	}
}

func TestSymlinkedProfileFileIsRefused(t *testing.T) {
	t.Parallel()
	dir := privateDir(t)
	other := filepath.Join(t.TempDir(), "elsewhere.json")
	if err := os.WriteFile(other, []byte(`{"version":1,"profiles":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(dir, "profiles.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir).Load(); err == nil {
		t.Error("a symbolic link was followed")
	}
}

func TestUnknownFieldAndVersionAreRefused(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"unknown field": `{"version":1,"profiles":{},"extra":true}`,
		"other version": `{"version":2,"profiles":{}}`,
		"bad kind":      `{"version":1,"profiles":{"a":{"url":"https://x","kind":"root","token":"sc_admin_x"}}}`,
	} {
		dir := privateDir(t)
		if err := os.WriteFile(filepath.Join(dir, "profiles.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(dir).Load(); err == nil {
			t.Errorf("%s: loaded, want a refusal", name)
		}
	}
}

// TestSaveIsAtomic: a crash between the write and the rename leaves the old file whole.
func TestSaveIsAtomic(t *testing.T) {
	t.Parallel()
	dir := privateDir(t)
	s := saveSample(t, dir)
	s.beforeRename = func() error { return errors.New("simulated crash") }
	changed := sample()
	changed.Current = ""
	changed.Profiles["home"] = Profile{URL: "https://other.example", Kind: KindAdmin, Token: "sc_admin_NEWVALUE0123"}
	if err := s.Save(changed); err == nil {
		t.Fatal("Save reported success after the simulated crash")
	}
	f, err := Open(dir).Load()
	if err != nil {
		t.Fatal(err)
	}
	if f.Current != "home" || f.Profiles["home"].Token != token {
		t.Errorf("after the crash the file is %+v, want the old one", f)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "profiles.json" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("directory holds %v after a failed save; want only profiles.json", names)
	}
}

// TestSaveRefusesAnExistingTempFile: the temporary name is already taken, so Save fails and the file that holds it is
// left as it was. This is not parallel: it replaces newSuffix. Without O_EXCL the write would truncate that file and the
// rename would carry on.
func TestSaveRefusesAnExistingTempFile(t *testing.T) {
	dir := privateDir(t)
	s := saveSample(t, dir)
	planted := filepath.Join(dir, ".profiles-fixed.tmp")
	if err := os.WriteFile(planted, []byte("someone else's"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := newSuffix
	newSuffix = func() (string, error) { return "fixed", nil }
	t.Cleanup(func() { newSuffix = old })
	changed := sample()
	changed.Current = ""
	if err := s.Save(changed); err == nil {
		t.Fatal("Save reused a temporary name that already existed")
	}
	if data, _ := os.ReadFile(planted); string(data) != "someone else's" {
		t.Errorf("the existing temporary file was written: %q", data)
	}
	f, err := Open(dir).Load()
	if err != nil {
		t.Fatal(err)
	}
	if f.Current != "home" {
		t.Errorf("the profiles file changed after a refused save: %+v", f)
	}
}

func TestKindOf(t *testing.T) {
	t.Parallel()
	ok := map[string]string{
		"sc_admin_abc":  KindAdmin,
		"sc_acct_abc":   KindAccount,
		"scd_abc":       KindDevice,
		"sc_acct_x-y_z": KindAccount,
	}
	for tok, want := range ok {
		got, err := KindOf(tok)
		if err != nil || got != want {
			t.Errorf("KindOf(%q) = %q, %v; want %q", tok, got, err, want)
		}
	}
	for _, tok := range []string{"", "sct_abc", "scl_abc", "sc_admin_", "sc_acct_a b", "Bearer sc_acct_x", "sc_acct_a\nb", "sc_other_x"} {
		if _, err := KindOf(tok); err == nil {
			t.Errorf("KindOf(%q) accepted a value that is not a key", tok)
		}
	}
}

// TestKindOfRefusalNeverRepeatsTheValue: a refused value is not printed by the refusal. The marker is unique to the value,
// since the refusal names the key prefixes itself.
func TestKindOfRefusalNeverRepeatsTheValue(t *testing.T) {
	t.Parallel()
	_, err := KindOf("sct_REFUSEDVALUE0123456789")
	if err == nil {
		t.Fatal("a device-looking value of another kind was accepted")
	}
	if strings.Contains(err.Error(), "REFUSEDVALUE") {
		t.Errorf("the refusal repeats the value: %v", err)
	}
}

func TestValidName(t *testing.T) {
	t.Parallel()
	for _, n := range []string{"work", "a", "my-cloud_2", "x0"} {
		if err := ValidName(n); err != nil {
			t.Errorf("ValidName(%q) = %v", n, err)
		}
	}
	for _, n := range []string{"", "Work", "-x", "a b", "../x", "x/y", strings.Repeat("a", 33), "_x"} {
		if err := ValidName(n); err == nil {
			t.Errorf("ValidName(%q) accepted", n)
		}
	}
}

// TestLoadRefusesABadURL: a hand-edited address is checked when the file is read, with the rule that add uses. A user
// name or password in it would otherwise be printed by ls and then refused by every command.
func TestLoadRefusesABadURL(t *testing.T) {
	t.Parallel()
	for _, u := range []string{"https://admin:" + token + "@selfcloud.example", "ftp://selfcloud.example", "https://selfcloud.example/?q=1", "https://"} {
		dir := privateDir(t)
		body := `{"version":1,"profiles":{"home":{"url":"` + u + `","kind":"account","token":"` + token + `"}}}`
		if err := os.WriteFile(filepath.Join(dir, "profiles.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Open(dir).Load()
		if err == nil {
			t.Errorf("a profile with address %q loaded", u)
		} else if strings.Contains(err.Error(), token) {
			t.Errorf("the refusal for %q names the credential: %v", u, err)
		}
	}
}

// TestLoadAcceptsASavedPlainHTTPAddress: an http address to a host that is not loopback is saved only when add was given
// --insecure-http, so Load accepts the scheme; Connect still demands the flag on each use.
func TestLoadAcceptsASavedPlainHTTPAddress(t *testing.T) {
	t.Parallel()
	dir := privateDir(t)
	body := `{"version":1,"profiles":{"lan":{"url":"http://192.0.2.5","kind":"account","token":"` + token + `"}}}`
	if err := os.WriteFile(filepath.Join(dir, "profiles.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir).Load(); err != nil {
		t.Errorf("a saved http address was refused on load: %v", err)
	}
}

func TestReadCredentialFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	if err := os.WriteFile(good, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCredentialFile(good)
	if err != nil || got != token {
		t.Errorf("ReadCredentialFile = %q, %v", got, err)
	}

	loose := filepath.Join(dir, "loose")
	if err := os.WriteFile(loose, []byte(token), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(loose, 0o640); err != nil {
		t.Fatal(err)
	}
	_, err = ReadCredentialFile(loose)
	if err == nil || !strings.Contains(err.Error(), "chmod 600") || strings.Contains(err.Error(), token) {
		t.Errorf("a group-readable token file: err = %v, want a chmod message without the token", err)
	}

	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCredentialFile(empty); err == nil {
		t.Error("an empty token file was accepted")
	}

	big := filepath.Join(dir, "big")
	if err := os.WriteFile(big, []byte(strings.Repeat("a", 5000)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCredentialFile(big); err == nil {
		t.Error("an oversized token file was accepted")
	}
	if _, err := ReadCredentialFile(filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing token file was accepted")
	}
}

func TestReadCredentialReader(t *testing.T) {
	t.Parallel()
	got, err := ReadCredential(strings.NewReader(token + "\r\n"))
	if err != nil || got != token {
		t.Errorf("ReadCredential = %q, %v", got, err)
	}
	for _, in := range []string{"", "\n", "a b\n", "x\ny\n", "sct_abc\n"} {
		if _, err := ReadCredential(strings.NewReader(in)); err == nil {
			t.Errorf("ReadCredential(%q) accepted", in)
		}
	}
}
