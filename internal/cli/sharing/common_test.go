// SPDX-License-Identifier: AGPL-3.0-or-later

package sharing

import (
	"bytes"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAccountArgTakesTheFlagThenTheEnvironment(t *testing.T) {
	isolate(t, testKey)
	if got, err := accountArg(testAccount); err != nil || got != testAccount {
		t.Fatalf("accountArg(flag) = %q, %v", got, err)
	}
	t.Setenv("SELFCLOUD_ACCOUNT", testAccount)
	if got, err := accountArg(""); err != nil || got != testAccount {
		t.Fatalf("accountArg(env) = %q, %v", got, err)
	}
	other := "acc_" + strings.Repeat("a", 26)
	if got, _ := accountArg(other); got != other {
		t.Fatalf("the flag must win over the environment, got %q", got)
	}
}

func TestAccountArgRefusesAMissingOrBadID(t *testing.T) {
	isolate(t, testKey)
	for _, bad := range []string{"", "acc_short", testNode, "../x", testAccount + "/x"} {
		if _, err := accountArg(bad); err == nil {
			t.Errorf("accountArg(%q) accepted", bad)
		}
	}
	t.Setenv("SELFCLOUD_ACCOUNT", "not-an-id")
	if _, err := accountArg(""); err == nil {
		t.Error("a bad SELFCLOUD_ACCOUNT was accepted")
	}
}

func TestExpiryArg(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		at, in, want string
		bad          bool
	}{
		{"", "", "", false},
		{"2026-12-01T00:00:00Z", "", "2026-12-01T00:00:00Z", false},
		{"2026-12-01T02:00:00+02:00", "", "2026-12-01T00:00:00Z", false},
		{"", "48h", "2026-11-14T10:00:00Z", false},
		{"", "30d", "2026-12-12T10:00:00Z", false},
		{"2026-12-01T00:00:00Z", "48h", "", true},
		{"tomorrow", "", "", true},
		{"", "0s", "", true},
		{"", "-5h", "", true},
		{"", "0d", "", true},
		{"", "xd", "", true},
		{"", "forever", "", true},
	}
	for _, c := range cases {
		got, err := expiryArg(c.at, c.in, now)
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("expiryArg(%q, %q) = %q, %v; want %q, bad %v", c.at, c.in, got, err, c.want, c.bad)
		}
	}
}

func TestOptBoolTellsAbsentFromFalse(t *testing.T) {
	set := flag.NewFlagSet("x", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	var a, b, c optBool
	set.Var(&a, "a", "")
	set.Var(&b, "b", "")
	set.Var(&c, "c", "")
	if err := set.Parse([]string{"-a", "-b=false"}); err != nil {
		t.Fatal(err)
	}
	if !a.set || !a.val || !b.set || b.val || c.set {
		t.Fatalf("a=%+v b=%+v c=%+v", a, b, c)
	}
	if err := set.Parse([]string{"-a=maybe"}); err == nil {
		t.Fatal("a value that is not a boolean was accepted")
	}
}

func TestReadPasswordFromAFile(t *testing.T) {
	isolate(t, testKey)
	dir := t.TempDir()
	write := func(name, data string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if got, err := readPassword(write("a", testPassword+"\n")); err != nil || got != testPassword {
		t.Fatalf("read = %q, %v", got, err)
	}
	if got, err := readPassword(write("b", testPassword+"\r\nsecond line\n")); err != nil || got != testPassword {
		t.Fatalf("only the first line is the password: %q, %v", got, err)
	}
	if got, err := readPassword(write("c", testPassword)); err != nil || got != testPassword {
		t.Fatalf("no final newline: %q, %v", got, err)
	}
	if _, err := readPassword(write("empty", "\n")); err == nil {
		t.Error("an empty password was accepted")
	}
	if _, err := readPassword(write("long", strings.Repeat("x", maxPassword+1)+"\n")); err == nil {
		t.Error("a password past the limit was accepted")
	}
	if _, err := readPassword(filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing file was accepted")
	}
	if _, err := readPassword(dir); err == nil {
		t.Error("a directory was accepted")
	}
}

func TestReadPasswordFromStandardInput(t *testing.T) {
	isolate(t, testKey)
	pipeIn(testPassword + "\n")
	if got, err := readPassword("-"); err != nil || got != testPassword {
		t.Fatalf("read = %q, %v", got, err)
	}
	typeAtTerminal(testPassword + "\n")
	if _, err := readPassword("-"); err == nil {
		t.Error("a terminal was read: the password would be shown as it is typed")
	}
	pipeIn("")
	if _, err := readPassword("-"); err == nil {
		t.Error("empty standard input was accepted")
	}
}

func TestReadPasswordErrorsNameNoPassword(t *testing.T) {
	isolate(t, testKey)
	p := filepath.Join(t.TempDir(), testPassword)
	_, err := readPassword(p)
	if err == nil {
		t.Fatal("a missing file was accepted")
	}
	if strings.Contains(err.Error(), testPassword) {
		t.Fatalf("the error repeats the file name, which may be mistaken for the password: %v", err)
	}
}

func TestScrubTextHidesSecrets(t *testing.T) {
	in := "bad password " + testPassword + " for https://h.example/s#" + testSecret + " and /api/v1/links/" + testSecret + ".x.y done"
	got := scrubText(in, testPassword)
	for _, s := range []string{testPassword, testSecret} {
		if strings.Contains(got, s) {
			t.Fatalf("scrubText left %q in %q", s, got)
		}
	}
	if !strings.Contains(got, "done") || !strings.Contains(got, "bad password") {
		t.Fatalf("scrubText cut too much: %q", got)
	}
	if scrubText("nothing here", "") != "nothing here" {
		t.Error("an empty secret must not match everywhere")
	}
}

func TestScrubberScrubsEveryWrite(t *testing.T) {
	var out bytes.Buffer
	s := &scrubber{w: &out}
	s.add(testPassword)
	if _, err := io.WriteString(s, "x "+testPassword+" y\n"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), testPassword) {
		t.Fatalf("the writer let the secret through: %q", out.String())
	}
}

func TestLinkFieldsKeepsKnownFieldsOnly(t *testing.T) {
	raw := `{"id":"` + testLink + `","audience":"anyone","role":"viewer","password_set":true,` +
		`"secret":"` + testSecret + `","url":"https://h/s#` + testSecret + `","password":"` + testPassword + `","token":"t",` +
		`"secret_hash":"h","expires_at":null}`
	m := linkFields([]byte(raw))
	for _, k := range []string{"secret", "url", "password", "token", "secret_hash"} {
		if _, ok := m[k]; ok {
			t.Errorf("field %q kept", k)
		}
	}
	for _, k := range []string{"id", "audience", "role", "password_set", "expires_at"} {
		if _, ok := m[k]; !ok {
			t.Errorf("field %q dropped", k)
		}
	}
}
