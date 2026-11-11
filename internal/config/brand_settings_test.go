// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// brandKeys are the brand's settings (decision 141), sorted: the name, the accent, the palette of each theme and the
// sidebar's colours.
var brandKeys = []string{
	"ui.brand.accent", "ui.brand.name",
	"ui.brand.palette.dark.accent", "ui.brand.palette.dark.accent_border", "ui.brand.palette.dark.accent_fill",
	"ui.brand.palette.dark.accent_fill_active", "ui.brand.palette.dark.accent_fill_hover", "ui.brand.palette.dark.accent_soft",
	"ui.brand.palette.dark.accent_text",
	"ui.brand.palette.light.accent", "ui.brand.palette.light.accent_border", "ui.brand.palette.light.accent_fill",
	"ui.brand.palette.light.accent_fill_active", "ui.brand.palette.light.accent_fill_hover", "ui.brand.palette.light.accent_soft",
	"ui.brand.palette.light.accent_text",
	"ui.brand.sidebar.active", "ui.brand.sidebar.bg", "ui.brand.sidebar.caption", "ui.brand.sidebar.on_active",
	"ui.brand.sidebar.text",
}

// TestBrandKeysAreSettings: every brand key is a string setting with the file's default, changeable while nothing writes it.
func TestBrandKeysAreSettings(t *testing.T) {
	t.Parallel()
	c := loadYAML(t, "", nil)
	for _, key := range brandKeys {
		s := settingOf(t, c, key)
		if s.Type != TypeString || s.Locked || s.Restart || s.Source != SourceDefault {
			t.Errorf("%s = %+v, want an unlocked string from the default", key, s)
		}
	}
	if s := settingOf(t, c, "ui.brand.name"); s.Value != "SelfCloud" || s.Default != "SelfCloud" {
		t.Errorf("ui.brand.name = %+v", s)
	}
	if s := settingOf(t, c, "ui.brand.accent"); s.Value != "#053D8E" {
		t.Errorf("ui.brand.accent = %+v", s)
	}
	if s := settingOf(t, c, "ui.brand.sidebar.bg"); s.Value != "" || s.Default != "" {
		t.Errorf("ui.brand.sidebar.bg = %+v, want an unset token", s)
	}
	// The pictures are not settings: their rows and the keys that name their files are not listed.
	for _, s := range c.Settings() {
		if strings.HasPrefix(s.Key, "ui.brand.logo") || strings.HasPrefix(s.Key, "ui.brand.icon") {
			t.Errorf("%s is listed as a setting", s.Key)
		}
	}
}

// TestBrandKeysAreLockedByTheFileAndTheEnvironment: a brand key the file or the environment writes is locked, as decision
// 139's keys are.
func TestBrandKeysAreLockedByTheFileAndTheEnvironment(t *testing.T) {
	t.Parallel()
	c := loadYAML(t, "ui:\n  brand:\n    accent: \"#0F766E\"\n    palette:\n      dark:\n        accent_fill: \"#1E3A8A\"\n",
		map[string]string{"SELFCLOUD_UI_BRAND_NAME": "Acme Drive"})
	for key, src := range map[string]Source{"ui.brand.accent": SourceFile, "ui.brand.palette.dark.accent_fill": SourceFile,
		"ui.brand.name": SourceEnv} {
		if s := settingOf(t, c, key); !s.Locked || s.Source != src {
			t.Errorf("%s = %+v, want locked from %s", key, s, src)
		}
	}
	err := c.CheckKeys([]string{"ui.brand.accent", "ui.brand.name", "ui.brand.sidebar.bg"})
	if ce := changeError(t, err); !slices.Equal(ce.Locked, []string{"ui.brand.accent", "ui.brand.name"}) {
		t.Errorf("locked = %v", ce.Locked)
	}
}

// TestBrandChangeUsesTheFileRules: a brand value is checked by the config file's own rules, and each refusal names its
// whole key, every refused key at once.
func TestBrandChangeUsesTheFileRules(t *testing.T) {
	t.Parallel()
	c := loadYAML(t, "", nil)
	for _, tc := range []struct {
		set  map[string]string
		keys []string
		msg  string
	}{
		{map[string]string{"ui.brand.accent": "red"}, []string{"ui.brand.accent"}, "not a colour"},
		{map[string]string{"ui.brand.accent": "#abc", "ui.brand.sidebar.bg": "navy", "ui.brand.palette.light.accent_soft": "rgb(1,2,3)"},
			[]string{"ui.brand.accent", "ui.brand.palette.light.accent_soft", "ui.brand.sidebar.bg"}, "not a colour"},
		{map[string]string{"ui.brand.name": ""}, []string{"ui.brand.name"}, "empty"},
		{map[string]string{"ui.brand.name": strings.Repeat("n", 65)}, []string{"ui.brand.name"}, "longer than 64"},
		{map[string]string{"ui.brand.name": "A\nB"}, []string{"ui.brand.name"}, "control character"},
		{map[string]string{"ui.brand.palette.dark.accent_border": "#12345"}, []string{"ui.brand.palette.dark.accent_border"}, "not a colour"},
	} {
		_, err := c.Change(tc.set, nil)
		if err == nil {
			t.Errorf("%v: accepted", tc.set)
			continue
		}
		ce := changeError(t, err)
		if got := problemKeys(ce); !slices.Equal(got, tc.keys) {
			t.Errorf("%v: keys %v, want %v", tc.set, got, tc.keys)
		}
		if !strings.Contains(ce.Error(), tc.msg) {
			t.Errorf("%v: %q does not say %q", tc.set, ce.Error(), tc.msg)
		}
	}
	next, err := c.Change(map[string]string{"ui.brand.name": strings.Repeat("ñ", 64), "ui.brand.accent": "#0f766e",
		"ui.brand.sidebar.active": "#FECC00", "ui.brand.palette.light.accent": ""}, nil)
	if err != nil {
		t.Fatalf("a valid brand was refused: %v", err)
	}
	if next.UI.Brand.Name != strings.Repeat("ñ", 64) || next.UI.Brand.Accent != "#0f766e" || next.UI.Brand.Sidebar.Active != "#FECC00" {
		t.Errorf("brand = %+v", next.UI.Brand)
	}
	if s := settingOf(t, next, "ui.brand.name"); s.Source != SourceSettings {
		t.Errorf("ui.brand.name = %+v, want from the settings", s)
	}
	// An empty token is what the file accepts: the default look for that token.
	if s := settingOf(t, next, "ui.brand.palette.light.accent"); s.Source != SourceSettings || s.Value != "" {
		t.Errorf("ui.brand.palette.light.accent = %+v", s)
	}
}

// TestBrandValidationNamesEveryWholeKey: the file's own check names the whole key of each wrong value, all of them.
func TestBrandValidationNamesEveryWholeKey(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	cfg.DataDir = "data"
	cfg.UI.Brand.Name = ""
	cfg.UI.Brand.Sidebar.Text = "white"
	cfg.UI.Brand.LogoFile = "logo.jpg"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("accepted")
	}
	for _, want := range []string{"config: ui.brand.name is empty", `config: ui.brand.sidebar.text "white" is not a colour`,
		`config: ui.brand.logo_file "logo.jpg" must be a .png or .svg file`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q does not hold %q", err, want)
		}
	}
}

// TestSettingTextReadsAString: a string setting takes a JSON string only. The name may be as long as the file allows,
// written with escapes; a colour's value is held to the usual 64 bytes, so a refusal never repeats a long one.
func TestSettingTextReadsAString(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("😀", 64) // 64 characters; with \u escapes each is 12 bytes of JSON
	escaped := `"` + strings.Repeat(`😀`, 64) + `"`
	for _, g := range []struct{ key, raw, text string }{
		{"ui.brand.name", `"Acme Drive"`, "Acme Drive"},
		{"ui.brand.name", escaped, long},
		{"ui.brand.accent", `"#0F766E"`, "#0F766E"},
		{"ui.brand.sidebar.bg", `""`, ""},
	} {
		text, err := SettingText(g.key, []byte(g.raw))
		if err != nil || text != g.text {
			t.Errorf("SettingText(%s, %.40s) = %.40q, %v; want %.40q", g.key, g.raw, text, err, g.text)
		}
	}
	tooLong, _ := json.Marshal(strings.Repeat("n", 1100))
	for _, b := range []struct{ key, raw string }{
		{"ui.brand.name", `5`},
		{"ui.brand.name", `true`},
		{"ui.brand.name", `null`},
		{"ui.brand.name", `["Acme"]`},
		{"ui.brand.name", string(tooLong)},
		{"ui.brand.accent", `"#` + strings.Repeat("0", 70) + `"`},
		{"office.discovery_url", `"https://office.example.com"`},
	} {
		text, err := SettingText(b.key, []byte(b.raw))
		if err == nil {
			t.Errorf("SettingText(%s, %.40s) = %q, want an error", b.key, b.raw, text)
			continue
		}
		if !strings.Contains(err.Error(), b.key) || len(err.Error()) > 200 {
			t.Errorf("SettingText(%s, %.40s): %q", b.key, b.raw, err)
		}
	}
}

// TestPictureRowsAreNotSettings: the uploaded pictures' rows are left out of the settings without a word, so a start logs
// nothing for them and a later change is not refused for them; the settings API cannot name them or the file keys.
func TestPictureRowsAreNotSettings(t *testing.T) {
	t.Parallel()
	c := loadYAML(t, "", nil)
	var skipped []string
	c.ApplyStored(map[string]string{LogoPicture: "image/png sha256:" + strings.Repeat("a", 64),
		IconPicture: "image/svg+xml sha256:" + strings.Repeat("b", 64), "trash.retention": "48h0m0s"},
		func(k, _ string) { skipped = append(skipped, k) })
	if len(skipped) != 0 {
		t.Errorf("skipped %v, want nothing", skipped)
	}
	if _, ok := c.StoredText(LogoPicture); ok {
		t.Error("the logo's row is held as a setting")
	}
	if c.Trash.Retention.D().Hours() != 48 {
		t.Errorf("trash.retention = %v", c.Trash.Retention)
	}
	if _, err := c.Change(map[string]string{"ui.brand.name": "Acme"}, nil); err != nil {
		t.Errorf("a change after the picture rows: %v", err)
	}
	for _, key := range []string{LogoPicture, IconPicture, "ui.brand.logo_file", "ui.brand.icon_file"} {
		ce := changeError(t, c.CheckKeys([]string{key}))
		if !slices.Equal(ce.Unknown, []string{key}) {
			t.Errorf("%s: unknown = %v", key, ce.Unknown)
		}
		if _, err := SettingText(key, []byte(`"x"`)); err == nil {
			t.Errorf("SettingText(%s) accepted a value", key)
		}
	}
	if !IsPicture(LogoPicture) || !IsPicture(IconPicture) || IsPicture("ui.brand.logo_file") || IsPicture("ui.brand.name") {
		t.Error("IsPicture names the wrong keys")
	}
}

// TestPictureIsLockedByItsFileKey: the file or the environment writing ui.brand.logo_file or ui.brand.icon_file locks that
// picture, even when it writes it empty; nothing else does.
func TestPictureIsLockedByItsFileKey(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		yaml       string
		env        map[string]string
		logo, icon bool
	}{
		{"", nil, false, false},
		{"ui:\n  brand:\n    logo_file: /srv/brand/logo.svg\n", nil, true, false},
		{"ui:\n  brand:\n    logo_file: \"\"\n", nil, true, false},
		{"", map[string]string{"SELFCLOUD_UI_BRAND_ICON_FILE": "/srv/brand/icon.svg"}, false, true},
		{"ui:\n  brand:\n    name: Acme\n    accent: \"#0F766E\"\n", nil, false, false},
	} {
		c := loadYAML(t, tc.yaml, tc.env)
		if got := c.PictureLocked(LogoPicture); got != tc.logo {
			t.Errorf("%q %v: logo locked = %v", tc.yaml, tc.env, got)
		}
		if got := c.PictureLocked(IconPicture); got != tc.icon {
			t.Errorf("%q %v: icon locked = %v", tc.yaml, tc.env, got)
		}
	}
	// A configuration built in code locks by the path it holds.
	c := Defaults()
	c.UI.Brand.IconFile = "/srv/brand/icon.png"
	if c.PictureLocked(LogoPicture) || !c.PictureLocked(IconPicture) || c.PictureLocked("ui.brand.name") {
		t.Error("a path set in code does not lock its picture alone")
	}
}
