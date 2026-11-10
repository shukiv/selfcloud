// SPDX-License-Identifier: AGPL-3.0-or-later

package conn

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/profile"
)

const (
	accountKey = "sc_acct_PROFILEVALUE0123456789"
	envKey     = "sc_acct_ENVVALUE0123456789"
	flagKey    = "sc_admin_FLAGVALUE0123456789"
)

// isolate points the profiles directory at a fresh private directory and clears the variables the resolution reads, so a
// developer's own environment cannot change a test.
func isolate(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "profiles")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SELFCLOUD_PROFILES_DIR", dir)
	t.Setenv("SELFCLOUD_PROFILE", "")
	t.Setenv("SELFCLOUD_URL", "")
	t.Setenv("SELFCLOUD_TOKEN", "")
	return dir
}

func saveHome(t *testing.T, dir string, caFile string) {
	t.Helper()
	s := profile.Open(dir)
	f := profile.File{Version: 1, Current: "home", Profiles: map[string]profile.Profile{
		"home": {URL: "https://profile.example", Kind: profile.KindAccount, Token: accountKey, CAFile: caFile},
	}}
	if err := s.Save(f); err != nil {
		t.Fatal(err)
	}
}

func tokenFile(t *testing.T, value string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(p, []byte(value+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func goodGlobal() Global {
	return Global{Timeout: time.Minute, ConnectTimeout: time.Second, HeaderTimeout: time.Second}
}

func TestAddressPrecedenceFlagThenEnvThenProfile(t *testing.T) {
	dir := isolate(t)
	saveHome(t, dir, "")
	var errOut bytes.Buffer

	g := goodGlobal()
	g.URL = "https://flag.example"
	t.Setenv("SELFCLOUD_URL", "https://env.example")
	c, err := Connect(g, &errOut, false)
	if err != nil || c.URL != "https://flag.example" || c.URLFrom != "flag" {
		t.Fatalf("flag beats env: %+v, %v", c, err)
	}

	c, err = Connect(goodGlobal(), &errOut, false)
	if err != nil || c.URL != "https://env.example" || c.URLFrom != "env" {
		t.Fatalf("env beats the profile: %+v, %v", c, err)
	}

	t.Setenv("SELFCLOUD_URL", "")
	c, err = Connect(goodGlobal(), &errOut, false)
	if err != nil || c.URL != "https://profile.example" || c.URLFrom != "profile" || c.Profile != "home" {
		t.Fatalf("the current profile supplies the address: %+v, %v", c, err)
	}
}

func TestCredentialPrecedenceFlagThenEnvThenProfile(t *testing.T) {
	dir := isolate(t)
	saveHome(t, dir, "")
	var errOut bytes.Buffer

	g := goodGlobal()
	g.TokenFile = tokenFile(t, flagKey)
	t.Setenv("SELFCLOUD_TOKEN", envKey)
	c, err := Connect(g, &errOut, true)
	if err != nil || c.CredFrom != "flag" || c.Kind != profile.KindAdmin {
		t.Fatalf("the token file beats the environment: %+v, %v", c, err)
	}

	c, err = Connect(goodGlobal(), &errOut, true)
	if err != nil || c.CredFrom != "env" || c.Kind != profile.KindAccount {
		t.Fatalf("the environment beats the profile: %+v, %v", c, err)
	}

	t.Setenv("SELFCLOUD_TOKEN", "")
	c, err = Connect(goodGlobal(), &errOut, true)
	if err != nil || c.CredFrom != "profile" {
		t.Fatalf("the profile supplies the credential: %+v, %v", c, err)
	}
}

// TestProfileCredentialIsNotSentToAnotherAddress: the profile's key goes only to the profile's own address.
func TestProfileCredentialIsNotSentToAnotherAddress(t *testing.T) {
	dir := isolate(t)
	saveHome(t, dir, "")
	var errOut bytes.Buffer

	g := goodGlobal()
	g.URL = "https://other.example"
	if _, err := Connect(g, &errOut, true); !isUsage(err) {
		t.Errorf("a command that needs the key went to another address: err = %v", err)
	}
	c, err := Connect(g, &errOut, false)
	if err != nil {
		t.Fatal(err)
	}
	if c.CredFrom != "" {
		t.Errorf("a public command sent the profile's key to %s (credential source %q)", c.URL, c.CredFrom)
	}
}

func TestEnvironmentKeyMustBeAKey(t *testing.T) {
	isolate(t)
	t.Setenv("SELFCLOUD_URL", "https://x.example")
	t.Setenv("SELFCLOUD_TOKEN", "sct_NOTAKEY0123")
	var errOut bytes.Buffer
	_, err := Connect(goodGlobal(), &errOut, true)
	if !isUsage(err) {
		t.Fatalf("err = %v, want a usage error", err)
	}
	if strings.Contains(err.Error(), "NOTAKEY") {
		t.Errorf("the refusal repeats the value: %q", err.Error())
	}
}

func TestNeedsACredential(t *testing.T) {
	isolate(t)
	g := goodGlobal()
	g.URL = "https://x.example"
	var errOut bytes.Buffer
	if _, err := Connect(g, &errOut, true); !isUsage(err) {
		t.Errorf("no credential, needed: err = %v, want usage", err)
	}
	if _, err := Connect(g, &errOut, false); err != nil {
		t.Errorf("no credential, not needed: %v", err)
	}
}

func TestNoAddressIsUsage(t *testing.T) {
	isolate(t)
	var errOut bytes.Buffer
	if _, err := Connect(goodGlobal(), &errOut, false); !isUsage(err) {
		t.Errorf("no address: err = %v, want usage", err)
	}
}

func TestMissingNamedProfileIsNotFound(t *testing.T) {
	isolate(t)
	g := goodGlobal()
	g.Profile = "nope"
	var errOut bytes.Buffer
	_, err := Connect(g, &errOut, false)
	code, _, _ := exitcode.Of(err)
	if code != exitcode.NotFound {
		t.Errorf("exit = %d, want %d (err %v)", code, exitcode.NotFound, err)
	}
}

func TestSkipVerifyWarnsOnEveryUse(t *testing.T) {
	isolate(t)
	g := goodGlobal()
	g.URL = "https://x.example"
	g.InsecureSkipVerify = true
	var errOut bytes.Buffer
	for i := 0; i < 2; i++ {
		if _, err := Connect(g, &errOut, false); err != nil {
			t.Fatal(err)
		}
	}
	if n := strings.Count(errOut.String(), "--insecure-skip-verify"); n != 2 {
		t.Errorf("warnings = %d, want one per use:\n%s", n, errOut.String())
	}
}

func TestPlainHTTPWarnsOnlyOffLoopback(t *testing.T) {
	isolate(t)
	var errOut bytes.Buffer
	g := goodGlobal()
	g.URL = "http://127.0.0.1:9"
	if _, err := Connect(g, &errOut, false); err != nil || errOut.Len() != 0 {
		t.Errorf("loopback http: err %v, warning %q; want none", err, errOut.String())
	}
	g.URL = "http://192.0.2.5"
	if _, err := Connect(g, &errOut, false); !isUsage(err) {
		t.Errorf("plain http to a network host was accepted without --insecure-http: %v", err)
	}
	g.InsecureHTTP = true
	if _, err := Connect(g, &errOut, false); err != nil || !strings.Contains(errOut.String(), "not encrypted") {
		t.Errorf("--insecure-http: err %v, stderr %q; want the warning", err, errOut.String())
	}
}

func TestInsecureProfileFileIsRefused(t *testing.T) {
	dir := isolate(t)
	saveHome(t, dir, "")
	if err := os.Chmod(filepath.Join(dir, "profiles.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := goodGlobal()
	g.URL = "https://x.example"
	var errOut bytes.Buffer
	if _, err := Connect(g, &errOut, false); !isUsage(err) {
		t.Errorf("a readable profile file was used: err = %v", err)
	}
}

func TestErrorsNeverCarryAUserInfoCredential(t *testing.T) {
	isolate(t)
	g := goodGlobal()
	g.URL = "https://admin:" + flagKey + "@x.example"
	var errOut bytes.Buffer
	_, err := Connect(g, &errOut, false)
	if err == nil || strings.Contains(err.Error(), flagKey) || strings.Contains(errOut.String(), flagKey) {
		t.Errorf("err = %v; the address's credential must not be echoed", err)
	}
}

func TestParseAllowsFlagsAfterPositionals(t *testing.T) {
	var out bytes.Buffer
	fs := NewFlagSet("profile add")
	var g Global
	g.Register(fs)
	pos, err := Parse(fs, []string{"home", "--url", "https://x.example", "extra", "--json"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if len(pos) != 2 || pos[0] != "home" || pos[1] != "extra" || g.URL != "https://x.example" || !g.JSON {
		t.Errorf("pos = %v, global = %+v", pos, g)
	}
}

func TestParseRefusesAnUnknownFlagAndHelpPrintsFlags(t *testing.T) {
	var out bytes.Buffer
	fs := NewFlagSet("info")
	var g Global
	g.Register(fs)
	_, err := Parse(fs, []string{"--token", "sc_acct_x"}, &out)
	if !isUsage(err) || strings.Contains(err.Error(), "sc_acct_x") {
		t.Errorf("an unknown flag: err = %v, want a usage error that does not repeat the value", err)
	}
	_, err = Parse(fs, []string{"-h"}, &out)
	if !errors.Is(err, flag.ErrHelp) || !strings.Contains(out.String(), "-url") {
		t.Errorf("-h: err = %v, out = %q; want ErrHelp and the flags", err, out.String())
	}
}

func isUsage(err error) bool {
	code, _, _ := exitcode.Of(err)
	return err != nil && code == exitcode.Usage
}
