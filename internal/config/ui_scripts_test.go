// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// TestUIScriptShapes pins the rules for one page script: an https URL with a host, no userinfo and no fragment (a path
// and a query are allowed); an http URL on 127.0.0.1, [::1] or localhost only; or a same-origin absolute path with one
// leading slash, no backslash, no .. segment and no space or control character. Every refusal names the entry.
func TestUIScriptShapes(t *testing.T) {
	t.Parallel()
	accept := []string{
		"https://cdn.example.com/menu.js",
		"https://cdn.example.com:8443/menu.js?v=2",
		"https://cdn.example.com",
		"https://cdn.example.com/",
		"https://cdn.example.com/menu.js?q=\"x<y>&z=1",
		"https://192.0.2.1/menu.js",
		"https://[2001:db8::1]/menu.js",
		"HTTPS://CDN.Example.COM/menu.js",
		"http://127.0.0.1:8080/ps.js",
		"http://127.0.0.1/ps.js",
		"http://[::1]:8080/ps.js",
		"http://localhost:3000/ps.js",
		"http://localhost/ps.js",
		"/menu.js",
		"/brand/menu.js?v=2",
	}
	for _, entry := range accept {
		cfg := Defaults()
		cfg.UI.Scripts = []string{entry}
		if err := cfg.Validate(); err != nil {
			t.Errorf("ui.scripts [%q] was refused: %v", entry, err)
		}
	}
	reject := []string{
		"",
		"http://cdn.example.com/menu.js",
		"http://192.0.2.1/menu.js",
		"http://[2001:db8::1]/menu.js",
		"javascript:alert(1)",
		"data:text/javascript,alert(1)",
		"ftp://cdn.example.com/menu.js",
		"//cdn.example.com/menu.js",
		"/a/../b.js",
		"/a/../../b.js",
		"/a/b/../c.js",
		"/a\\b.js",
		"https://cdn.example.com\\menu.js",
		"/a b.js",
		"https://cdn.example.com/a b.js",
		"/a\tb.js",
		"https://user:pass@cdn.example.com/menu.js",
		"https://cdn.example.com/menu.js#frag",
		"https://cdn.example.com/menu.js#",
		"https://",
		"https:///menu.js",
		"menu.js",
		"https://cdn.example.com:0/menu.js",
		"https://cdn.example.com:65536/menu.js",
		"https://cdn.example.com;script-src/menu.js",
		"https://cdn.example.com,evil/menu.js",
		"/" + strings.Repeat("a", 2048),
	}
	for _, entry := range reject {
		cfg := Defaults()
		cfg.UI.Scripts = []string{entry}
		err := cfg.Validate()
		if err == nil {
			t.Errorf("ui.scripts [%q] was accepted; want a refusal", entry)
			continue
		}
		if !strings.Contains(err.Error(), "ui.scripts") || !strings.Contains(err.Error(), fmt.Sprintf("%q", entry)) {
			t.Errorf("ui.scripts [%q] error %q does not name the key and the entry", entry, err)
		}
	}
}

// TestUIScriptsRefuseIPv6Zone: a host with an IPv6 zone is refused, for the zone's '%' (percent-decoded from %25) can
// carry a CSP separator — a ';', a space, a quote — into script-src. The refusal names the key, the entry and the zone.
func TestUIScriptsRefuseIPv6Zone(t *testing.T) {
	t.Parallel()
	for _, entry := range []string{
		"https://[fe80::1%25;x]/a.js",
		"https://[fe80::1%25eth0]/a.js",
		"https://[fe80::1%25%20'unsafe-inline']/a.js",
	} {
		cfg := Defaults()
		cfg.UI.Scripts = []string{entry}
		err := cfg.Validate()
		if err == nil {
			t.Errorf("ui.scripts [%q] was accepted; want a refusal", entry)
			continue
		}
		if !strings.Contains(err.Error(), "ui.scripts") || !strings.Contains(err.Error(), fmt.Sprintf("%q", entry)) ||
			!strings.Contains(err.Error(), "zone") {
			t.Errorf("ui.scripts [%q] error %q does not name the key, the entry and the zone", entry, err)
		}
	}
	// An IPv6 host with no zone is still allowed.
	for _, entry := range []string{"https://[2001:db8::1]/menu.js", "http://[::1]:8080/ps.js"} {
		cfg := Defaults()
		cfg.UI.Scripts = []string{entry}
		if err := cfg.Validate(); err != nil {
			t.Errorf("ui.scripts [%q] was refused: %v", entry, err)
		}
	}
}

// TestUIScriptsLimits: the list holds at most ten entries, each 1 to 2048 bytes, and no exact duplicate; the cap and the
// duplicate are refused naming the key, and the duplicate names the entry.
func TestUIScriptsLimits(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	for i := 0; i < 9; i++ {
		cfg.UI.Scripts = append(cfg.UI.Scripts, "https://cdn.example.com/"+string(rune('a'+i))+".js")
	}
	cfg.UI.Scripts = append(cfg.UI.Scripts, "/"+strings.Repeat("a", 2047))
	if err := cfg.Validate(); err != nil {
		t.Fatalf("ten entries, one of 2048 bytes, were refused: %v", err)
	}
	eleven := append(slices.Clone(cfg.UI.Scripts), "/eleventh.js")
	cfg.UI.Scripts = eleven
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "ui.scripts") {
		t.Fatalf("eleven entries were accepted: %v", err)
	}
	cfg.UI.Scripts = []string{"/menu.js", "/menu.js"}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "ui.scripts") || !strings.Contains(err.Error(), "/menu.js") {
		t.Fatalf("a duplicate was not named: %v", err)
	}
	cfg.UI.Scripts = []string{""}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "ui.scripts") {
		t.Fatalf("an empty entry was accepted: %v", err)
	}
}

// TestUIScriptsFileAndEnv: the file and the environment read ui.scripts exactly as embed.origins is read, and each locks
// the key against the settings API.
func TestUIScriptsFileAndEnv(t *testing.T) {
	t.Parallel()
	cfg := loadYAML(t, "ui:\n  scripts:\n    - https://cdn.example.com/menu.js\n    - /help.js\n", nil)
	if !slices.Equal(cfg.UI.Scripts, []string{"https://cdn.example.com/menu.js", "/help.js"}) {
		t.Fatalf("ui.scripts = %q", cfg.UI.Scripts)
	}
	if s := settingOf(t, cfg, "ui.scripts"); s.Source != SourceFile || !s.Locked {
		t.Errorf("the file's list: %+v, want source file and locked", s)
	}
	cfg, err := Load("", env(map[string]string{"SELFCLOUD_UI_SCRIPTS": " https://cdn.example.com/menu.js , /help.js "}))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.UI.Scripts, []string{"https://cdn.example.com/menu.js", "/help.js"}) {
		t.Fatalf("the environment's list = %q", cfg.UI.Scripts)
	}
	if s := settingOf(t, cfg, "ui.scripts"); s.Source != SourceEnv || !s.Locked {
		t.Errorf("the environment's list: %+v, want source env and locked", s)
	}
	if _, err := Load("", env(map[string]string{"SELFCLOUD_UI_SCRIPTS": "http://cdn.example.com/menu.js"})); err == nil ||
		!strings.Contains(err.Error(), "ui.scripts") || !strings.Contains(err.Error(), "http://cdn.example.com/menu.js") {
		t.Fatalf("a bad environment entry was not named: %v", err)
	}
}

// TestUIScriptsSetting: ui.scripts is a list setting, empty by default, shown and stored as a JSON array of strings.
func TestUIScriptsSetting(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	s := settingOf(t, cfg, "ui.scripts")
	if s.Type != TypeList || s.Source != SourceDefault || s.Locked || s.Restart {
		t.Fatalf("ui.scripts = %+v, want a list setting at its default", s)
	}
	if v, ok := s.Value.([]string); !ok || len(v) != 0 {
		t.Errorf("the default value = %#v, want an empty list", s.Value)
	}
	if v, ok := s.Default.([]string); !ok || len(v) != 0 {
		t.Errorf("the default = %#v, want an empty list", s.Default)
	}
	if got := cfg.ValueText("ui.scripts"); got != "[]" {
		t.Errorf("ValueText = %q, want []", got)
	}

	text, err := SettingText("ui.scripts", []byte(`["https://cdn.example.com/menu.js","/help.js"]`))
	if err != nil || text != `["https://cdn.example.com/menu.js","/help.js"]` {
		t.Fatalf("SettingText = %q, %v", text, err)
	}
	next, err := cfg.Change(map[string]string{"ui.scripts": text}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(next.UI.Scripts, []string{"https://cdn.example.com/menu.js", "/help.js"}) {
		t.Errorf("the changed list = %q", next.UI.Scripts)
	}
	changedSetting := settingOf(t, next, "ui.scripts")
	if changedSetting.Source != SourceSettings || changedSetting.Locked {
		t.Errorf("the changed setting = %+v, want source settings and not locked", changedSetting)
	}
	if v, ok := changedSetting.Value.([]string); !ok || !slices.Equal(v, []string{"https://cdn.example.com/menu.js", "/help.js"}) {
		t.Errorf("the changed value = %#v", changedSetting.Value)
	}
	if got := next.ValueText("ui.scripts"); got != text {
		t.Errorf("ValueText after the change = %q, want %q", got, text)
	}
	if stored, ok := next.StoredText("ui.scripts"); !ok || stored != text {
		t.Errorf("StoredText = %q, %v", stored, ok)
	}

	// A reset removes the row, so the empty default applies again.
	back, err := next.Change(nil, []string{"ui.scripts"})
	if err != nil {
		t.Fatal(err)
	}
	if len(back.UI.Scripts) != 0 || back.ValueText("ui.scripts") != "[]" {
		t.Errorf("after a reset: %q, %q", back.UI.Scripts, back.ValueText("ui.scripts"))
	}

	// A stored row applies at start, and a row the file locks is skipped.
	applied := Defaults()
	applied.ApplyStored(map[string]string{"ui.scripts": text}, nil)
	if !slices.Equal(applied.UI.Scripts, []string{"https://cdn.example.com/menu.js", "/help.js"}) {
		t.Errorf("ApplyStored: %q", applied.UI.Scripts)
	}
	locked := loadYAML(t, "ui:\n  scripts:\n    - /file.js\n", nil)
	locked.ApplyStored(map[string]string{"ui.scripts": text}, nil)
	if !slices.Equal(locked.UI.Scripts, []string{"/file.js"}) {
		t.Errorf("the file's list must win over the row: %q", locked.UI.Scripts)
	}
}

// TestUIScriptsSettingRefusals: the JSON must be an array of strings, and every entry is held to the file's rules, naming
// the key and the entry; the whole change is refused.
func TestUIScriptsSettingRefusals(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`"https://cdn.example.com/menu.js"`,
		`42`,
		`true`,
		`null`,
		`{"a":1}`,
		`[1]`,
		`["a",null]`,
		`[[]]`,
		`["a"`,
	} {
		if _, err := SettingText("ui.scripts", []byte(raw)); err == nil || !strings.Contains(err.Error(), "ui.scripts") {
			t.Errorf("SettingText(%s) = %v; want a refusal naming the key", raw, err)
		}
	}
	cfg := Defaults()
	for _, entry := range []string{"http://cdn.example.com/menu.js", "javascript:alert(1)", "//cdn.example.com/menu.js", "/a/../b.js"} {
		text, err := SettingText("ui.scripts", []byte(`["`+entry+`"]`))
		if err != nil {
			t.Fatalf("SettingText of [%q]: %v", entry, err)
		}
		_, err = cfg.Change(map[string]string{"ui.scripts": text}, nil)
		ce := changeError(t, err)
		if !slices.Contains(problemKeys(ce), "ui.scripts") {
			t.Errorf("entry %q: problems name %v, want ui.scripts", entry, problemKeys(ce))
		}
		var named bool
		for _, p := range ce.Problems {
			if strings.Contains(p.Message, entry) {
				named = true
			}
		}
		if !named {
			t.Errorf("entry %q: no problem names it: %+v", entry, ce.Problems)
		}
	}
	// Eleven entries and a duplicate are refused through the settings too.
	eleven := make([]string, 11)
	for i := range eleven {
		eleven[i] = "/s" + string(rune('a'+i)) + ".js"
	}
	elevenJSON, err := json.Marshal(eleven)
	if err != nil {
		t.Fatal(err)
	}
	text, err := SettingText("ui.scripts", elevenJSON)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Change(map[string]string{"ui.scripts": text}, nil); err == nil {
		t.Error("eleven entries were accepted through the settings")
	}
	text, err = SettingText("ui.scripts", []byte(`["/menu.js","/menu.js"]`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Change(map[string]string{"ui.scripts": text}, nil); err == nil {
		t.Error("a duplicate was accepted through the settings")
	}
}
