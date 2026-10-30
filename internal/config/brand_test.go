// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// TestUIIsOnByDefault: the web interface is served unless an operator turns it off.
func TestUIIsOnByDefault(t *testing.T) {
	if !Defaults().UI.Enabled {
		t.Fatal("ui.enabled defaults to false")
	}
	cfg, err := Load("", func(k string) (string, bool) {
		if k == "SELFCLOUD_UI_ENABLED" {
			return "false", true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.Enabled {
		t.Error("SELFCLOUD_UI_ENABLED=false did not turn the UI off")
	}
}

// TestBrandFromEnvironment: the brand is set with SELFCLOUD_UI_BRAND_*.
func TestBrandFromEnvironment(t *testing.T) {
	env := map[string]string{
		"SELFCLOUD_UI_BRAND_NAME":   "Acme Drive",
		"SELFCLOUD_UI_BRAND_ACCENT": "#0f766e",
	}
	cfg, err := Load("", func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.Brand.Name != "Acme Drive" || cfg.UI.Brand.Accent != "#0f766e" {
		t.Errorf("brand = %+v", cfg.UI.Brand)
	}
}

// TestBrandValidation refuses a brand that cannot be shown, and reports what is wrong.
func TestBrandValidation(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*BrandConfig)
		wantErr string
	}{
		{"defaults are valid", func(b *BrandConfig) {}, ""},
		{"empty name", func(b *BrandConfig) { b.Name = "" }, "name is empty"},
		{"long name", func(b *BrandConfig) { b.Name = strings.Repeat("n", 65) }, "longer than 64"},
		{"64-rune name", func(b *BrandConfig) { b.Name = strings.Repeat("ñ", 64) }, ""},
		{"control character", func(b *BrandConfig) { b.Name = "A\nB" }, "control character"},
		{"short accent", func(b *BrandConfig) { b.Accent = "#abc" }, "not a colour"},
		{"accent without hash", func(b *BrandConfig) { b.Accent = "2563EB" }, "not a colour"},
		{"accent with a name", func(b *BrandConfig) { b.Accent = "red" }, "not a colour"},
		{"accent with trailing space", func(b *BrandConfig) { b.Accent = "#2563EB " }, "not a colour"},
		{"png logo", func(b *BrandConfig) { b.LogoFile = "/srv/logo.PNG" }, ""},
		{"svg logo", func(b *BrandConfig) { b.LogoFile = "logo.svg" }, ""},
		{"jpg logo", func(b *BrandConfig) { b.LogoFile = "logo.jpg" }, "must be a .png or .svg"},
		{"no extension", func(b *BrandConfig) { b.LogoFile = "logo" }, "must be a .png or .svg"},
		{"palette tokens unset", func(b *BrandConfig) {}, ""},
		{"palette token set", func(b *BrandConfig) { b.Palette.Dark.AccentFill = "#1E3A8A" }, ""},
		{"palette token not a colour", func(b *BrandConfig) { b.Palette.Light.AccentSoft = "rgb(1,2,3)" }, "palette.light.accent_soft"},
		{"palette dark token short", func(b *BrandConfig) { b.Palette.Dark.AccentBorder = "#abc" }, "palette.dark.accent_border"},
		{"sidebar colour set", func(b *BrandConfig) { b.Sidebar.Active = "#FECC00" }, ""},
		{"sidebar colour not a colour", func(b *BrandConfig) { b.Sidebar.Bg = "navy" }, "sidebar.bg"},
		{"sidebar on_active not a colour", func(b *BrandConfig) { b.Sidebar.OnActive = "#GGGGGG" }, "sidebar.on_active"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.DataDir = "data"
			c.mutate(&cfg.UI.Brand)
			err := cfg.Validate()
			switch {
			case c.wantErr == "" && err != nil:
				t.Errorf("valid brand refused: %v", err)
			case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
				t.Errorf("error = %v, want one containing %q", err, c.wantErr)
			}
		})
	}
}

// TestPaletteAndSidebarFromEnvironment: the nested tokens take their names from the structure, so the
// environment reaches them with no extra code.
func TestPaletteAndSidebarFromEnvironment(t *testing.T) {
	env := map[string]string{
		"SELFCLOUD_UI_BRAND_PALETTE_DARK_ACCENT_FILL": "#1E3A8A",
		"SELFCLOUD_UI_BRAND_SIDEBAR_BG":               "#0F766E",
	}
	cfg, err := Load("", func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.Brand.Palette.Dark.AccentFill != "#1E3A8A" || cfg.UI.Brand.Sidebar.Bg != "#0F766E" {
		t.Errorf("brand = %+v", cfg.UI.Brand)
	}
}

// TestDefaultAccentIsTheBrandBlue: the default accent is the brand blue, and no token is overridden.
func TestDefaultAccentIsTheBrandBlue(t *testing.T) {
	b := Defaults().UI.Brand
	if b.Accent != "#053D8E" {
		t.Errorf("default accent = %s", b.Accent)
	}
	if b.Palette != (BrandPalette{}) || b.Sidebar != (BrandSidebar{}) {
		t.Errorf("defaults override tokens: %+v", b)
	}
}

// TestPaletteFromYAML: the strict decoder reads the nested tokens and refuses a misspelt one.
func TestPaletteFromYAML(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.yaml"
	if err := os.WriteFile(path, []byte("ui:\n  brand:\n    name: Acme\n    palette:\n      light:\n        accent_soft: \"#EBF0F6\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.Brand.Palette.Light.AccentSoft != "#EBF0F6" {
		t.Errorf("palette = %+v", cfg.UI.Brand.Palette)
	}
	if err := os.WriteFile(path, []byte("ui:\n  brand:\n    name: Acme\n    palette:\n      light:\n        accent_softt: \"#EBF0F6\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, func(string) (string, bool) { return "", false }); err == nil {
		t.Error("a misspelt token was accepted")
	}
}

// TestEveryBrandTokenIsValidated (M2): each colour token of the palette and the sidebar is refused
// when it is not a colour, and the test finds the tokens by reflection, so a token added to the structs
// without a check in validate fails here.
func TestEveryBrandTokenIsValidated(t *testing.T) {
	bad := []string{"red", "#abc", "#12345", "#1234567", "#gggggg", "2563EB", "#2563EB ", "#2563EB\n"}
	tokens := []struct {
		prefix string
		value  reflect.Value
	}{}
	walk := func(prefix string, v reflect.Value) {
		ty := v.Type()
		for i := 0; i < ty.NumField(); i++ {
			key := ty.Field(i).Tag.Get("yaml")
			if key == "" || ty.Field(i).Type.Kind() != reflect.String {
				t.Fatalf("field %s.%s has no yaml name or is not a string", ty.Name(), ty.Field(i).Name)
			}
			tokens = append(tokens, struct {
				prefix string
				value  reflect.Value
			}{prefix + key, v.Field(i)})
		}
	}
	walk("palette.light.", reflect.ValueOf(BrandTokens{}))
	walk("palette.dark.", reflect.ValueOf(BrandTokens{}))
	walk("sidebar.", reflect.ValueOf(BrandSidebar{}))
	if len(tokens) != 19 {
		t.Fatalf("found %d colour tokens, want 19: update the test with the struct", len(tokens))
	}
	for _, tok := range tokens {
		for _, value := range bad {
			b := Defaults().UI.Brand
			setToken(&b, tok.prefix, value)
			err := b.validate()
			if err == nil || !strings.HasPrefix(err.Error(), tok.prefix+" ") {
				t.Errorf("%s = %q: accepted or wrong error %v", tok.prefix, value, err)
			}
		}
	}
}

// setToken writes value into the token named by prefix, which is "palette.<theme>.<yaml>" or
// "sidebar.<yaml>", as the validation names it.
func setToken(b *BrandConfig, prefix string, value string) {
	field := func(v reflect.Value, yamlName string) {
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).Tag.Get("yaml") == yamlName {
				v.Field(i).SetString(value)
				return
			}
		}
	}
	parts := strings.SplitN(prefix, ".", 3)
	switch parts[0] {
	case "sidebar":
		field(reflect.ValueOf(&b.Sidebar).Elem(), parts[1])
	case "palette":
		theme := reflect.ValueOf(&b.Palette).Elem().FieldByName("Light")
		if parts[1] == "dark" {
			theme = reflect.ValueOf(&b.Palette).Elem().FieldByName("Dark")
		}
		field(theme, parts[2])
	}
}

// TestContrastWarningsForExplicitPairs (L5): an explicit colour pair under 4.5:1 is reported, once per
// pair, and the config is still accepted. Pairs of defaults are not reported.
func TestContrastWarningsForExplicitPairs(t *testing.T) {
	b := Defaults().UI.Brand
	if w := b.ContrastWarnings(); len(w) != 0 {
		t.Fatalf("the defaults warn: %v", w)
	}
	b.Sidebar.Text = "#1C3F7A" // dark blue text on the blue sidebar
	w := b.ContrastWarnings()
	if len(w) != 1 || !strings.Contains(w[0], "sidebar.text") {
		t.Fatalf("warnings for a low sidebar text = %v, want one about sidebar.text", w)
	}
	if err := b.validate(); err != nil {
		t.Errorf("a low-contrast pair refused the config: %v", err)
	}
	b = Defaults().UI.Brand
	b.Palette.Light.AccentFill = "#FDE68A" // white text on a pale yellow fill
	w = b.ContrastWarnings()
	if len(w) != 1 || !strings.Contains(w[0], "palette.light.accent_fill") {
		t.Errorf("warnings for a pale light fill = %v", w)
	}
	if err := b.validate(); err != nil {
		t.Errorf("a low-contrast fill refused the config: %v", err)
	}
}
