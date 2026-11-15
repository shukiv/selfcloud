// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// The installation's outside sign-in (decision 204): the two URLs and the password switch are
// settings, checked at start and on a settings write with the file's own rules.

func TestExternalSignInDefaults(t *testing.T) {
	c := Defaults()
	if c.Users.ExternalSignInURL != "" || c.Users.ExternalSignedOutURL != "" {
		t.Errorf("external URLs default to %q and %q, want empty", c.Users.ExternalSignInURL, c.Users.ExternalSignedOutURL)
	}
	if !c.Users.PasswordSignIn {
		t.Error("users.password_sign_in defaults to false, want true")
	}
	for _, tc := range []struct {
		key string
		typ SettingType
		def any
	}{
		{"users.external_sign_in_url", TypeString, ""},
		{"users.external_signed_out_url", TypeString, ""},
		{"users.password_sign_in", TypeBool, true},
	} {
		st := settingOf(t, c, tc.key)
		if st.Type != tc.typ || st.Value != tc.def || st.Default != tc.def || st.Source != SourceDefault || st.Locked {
			t.Errorf("%s = %+v, want type %s value %v from the default, unlocked", tc.key, st, tc.typ, tc.def)
		}
	}
}

// externalURLCases are the URL rules both settings share: an absolute https URL (http only for
// localhost or 127.0.0.1), no user info, no fragment, at most 2048 bytes. The sign-in URL may carry
// {next} once; the signed-out URL not at all.
func externalURLCases() map[string]struct {
	url string
	ok  bool
} {
	return map[string]struct {
		url string
		ok  bool
	}{
		"empty is off":         {"", true},
		"https with next":      {"https://app.example.com/sign-in?next={next}", true},
		"https without next":   {"https://app.example.com/sign-in", true},
		"https with path":      {"https://app.example.com/a/b?x=1&y=2", true},
		"http localhost":       {"http://localhost:3000/sign-in?next={next}", true},
		"http loopback":        {"http://127.0.0.1:3000/sign-in", true},
		"longest allowed":      {"https://app.example.com/" + strings.Repeat("a", 2048-len("https://app.example.com/")), true},
		"relative":             {"app.example.com/sign-in", false},
		"path only":            {"/sign-in", false},
		"other scheme":         {"ftp://app.example.com/sign-in", false},
		"http away from home":  {"http://app.example.com/sign-in", false},
		"http other host":      {"http://localhost.example.com/sign-in", false},
		"user info":            {"https://user:pass@app.example.com/sign-in", false},
		"fragment":             {"https://app.example.com/sign-in#top", false},
		"empty fragment":       {"https://app.example.com/sign-in#", false},
		"one byte too long":    {"https://app.example.com/" + strings.Repeat("a", 2049-len("https://app.example.com/")), false},
		"next twice":           {"https://app.example.com/sign-in?next={next}&also={next}", false},
		"space":                {"https://app.example.com/sign in", false},
		"tab":                  {"https://app.example.com/sign-in\t", false},
		"newline":              {"https://app.example.com/sign-in\n", false},
		"no host":              {"https://", false},
		"mailto":               {"mailto:app@example.com", false},
		"backslash in host":    {"https://app.example.com\\@evil.example.com/", false},
		"signed out with next": {"https://app.example.com/signed-out?next={next}", false},
	}
}

func TestExternalSignInURLsAtStart(t *testing.T) {
	for name, tc := range externalURLCases() {
		if name == "signed out with next" {
			continue // the signed-out URL's own rule, below
		}
		_, err := Load("", env(map[string]string{"SELFCLOUD_USERS_EXTERNAL_SIGN_IN_URL": tc.url}))
		if tc.ok && err != nil {
			t.Errorf("%s: start refused %q: %v", name, tc.url, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s: start accepted %q", name, tc.url)
		}
		if !tc.ok && err != nil && !strings.Contains(err.Error(), "users.external_sign_in_url") {
			t.Errorf("%s: error does not name the key: %v", name, err)
		}
	}
}

func TestExternalSignInURLsOnWrite(t *testing.T) {
	for name, tc := range externalURLCases() {
		if name == "signed out with next" {
			continue
		}
		next, err := Defaults().Change(map[string]string{"users.external_sign_in_url": tc.url}, nil)
		if tc.ok {
			if err != nil {
				t.Errorf("%s: write refused %q: %v", name, tc.url, err)
			} else if next.Users.ExternalSignInURL != tc.url {
				t.Errorf("%s: write kept %q, want %q", name, next.Users.ExternalSignInURL, tc.url)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: write accepted %q", name, tc.url)
			continue
		}
		if keys := problemKeys(changeError(t, err)); !slices.Contains(keys, "users.external_sign_in_url") {
			t.Errorf("%s: problem names %v, want users.external_sign_in_url", name, keys)
		}
	}
}

func TestExternalSignedOutURLOnWrite(t *testing.T) {
	for name, tc := range externalURLCases() {
		wantOK := tc.ok && !strings.Contains(tc.url, "{next}")
		next, err := Defaults().Change(map[string]string{"users.external_signed_out_url": tc.url}, nil)
		if wantOK {
			if err != nil {
				t.Errorf("%s: write refused %q: %v", name, tc.url, err)
			} else if next.Users.ExternalSignedOutURL != tc.url {
				t.Errorf("%s: write kept %q, want %q", name, next.Users.ExternalSignedOutURL, tc.url)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: write accepted %q", name, tc.url)
			continue
		}
		if keys := problemKeys(changeError(t, err)); !slices.Contains(keys, "users.external_signed_out_url") {
			t.Errorf("%s: problem names %v, want users.external_signed_out_url", name, keys)
		}
	}
	// The signed-out URL carries no placeholder at all.
	if _, err := Defaults().Change(map[string]string{"users.external_signed_out_url": "https://app.example.com/bye?next={next}"}, nil); err == nil {
		t.Error("the signed-out URL accepted {next}")
	}
}

func TestPasswordSignInOnWrite(t *testing.T) {
	next, err := Defaults().Change(map[string]string{"users.password_sign_in": "false"}, nil)
	if err != nil {
		t.Fatalf("write refused: %v", err)
	}
	if next.Users.PasswordSignIn {
		t.Error("password_sign_in stayed true after a write of false")
	}
	back, err := next.Change(nil, []string{"users.password_sign_in"})
	if err != nil {
		t.Fatalf("reset refused: %v", err)
	}
	if !back.Users.PasswordSignIn {
		t.Error("reset did not put the default true back")
	}
	if _, err := Defaults().Change(map[string]string{"users.password_sign_in": "maybe"}, nil); err == nil {
		t.Error("password_sign_in accepted a value that is not a bool")
	}
}

// A settings write reads the URL as JSON: a long one is refused before it is parsed, and the
// longest the rule allows (2048 bytes, each written as a \u escape) is still read.
func TestExternalSignInURLJSONLength(t *testing.T) {
	long := "https://app.example.com/" + strings.Repeat("a", 2048-len("https://app.example.com/"))
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(long); i++ {
		fmt.Fprintf(&b, `\u%04x`, long[i])
	}
	b.WriteByte('"')
	escaped := b.String()
	if _, err := SettingText("users.external_sign_in_url", []byte(escaped)); err != nil {
		t.Errorf("the longest URL as JSON escapes refused: %v", err)
	}
	if _, err := SettingText("users.external_sign_in_url", []byte(escaped+"x")); err == nil {
		t.Error("a JSON value longer than the limit was read")
	}
}
