// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
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
