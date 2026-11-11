// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// startWithLog starts a server with cfg mutated, as newTestServer does, but returns start's error, and logs to buf.
func startWithLog(t *testing.T, buf *bytes.Buffer, mutate ...func(*config.Config)) (*Server, error) {
	t.Helper()
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	cfg.Storage.MinFree = 0
	for _, f := range mutate {
		f(cfg)
	}
	cfg.Store.Path = filepath.Join(cfg.DataDir, "selfcloud.db")
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	seedMigratedDB(t, cfg.Store.Path)
	st, err := store.Open(context.Background(), store.Options{Path: cfg.Store.Path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	return New(Options{Config: cfg, Store: st, Mailer: &mail.Recorder{}, Logger: log})
}

// iconTestFile writes data to a file of the test's own and returns its path.
func iconTestFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// squarePNG is a w by h opaque PNG.
func squarePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 9, G: 9, B: 9, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestIconFileRefusesStartNamingTheSetting: an icon file that is the wrong shape, too small, too big or missing stops the start,
// and the error names ui.brand.icon_file.
func TestIconFileRefusesStartNamingTheSetting(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		label string
		path  string
	}{
		{"511 pixels", iconTestFile(t, "small.png", squarePNG(t, 511, 511))},
		{"2049 pixels", iconTestFile(t, "big.png", squarePNG(t, 2049, 2049))},
		{"a non-square PNG", iconTestFile(t, "wide.png", squarePNG(t, 600, 512))},
		{"SVG content in a .png", iconTestFile(t, "fake.png", []byte("<svg></svg>"))},
		{"a missing file", filepath.Join(t.TempDir(), "missing.png")},
	} {
		var buf bytes.Buffer
		_, err := startWithLog(t, &buf, func(c *config.Config) { c.UI.Brand.IconFile = tc.path })
		if err == nil || !strings.Contains(err.Error(), "ui.brand.icon_file") {
			t.Errorf("%s: start returned %v, want a refusal naming ui.brand.icon_file", tc.label, err)
		}
	}
}

// TestTruncatedIconFileNamesItsSettingOnce: an icon file whose header reads but whose pixels were cut short refuses the start, and
// the error names ui.brand.icon_file with one prefix, not a repeated web: prefix.
func TestTruncatedIconFileNamesItsSettingOnce(t *testing.T) {
	t.Parallel()
	full := squarePNG(t, 512, 512)
	var buf bytes.Buffer
	_, err := startWithLog(t, &buf, func(c *config.Config) { c.UI.Brand.IconFile = iconTestFile(t, "cut.png", full[:len(full)/2]) })
	if err == nil || !strings.Contains(err.Error(), "ui.brand.icon_file") || strings.Contains(err.Error(), "web: web:") {
		t.Errorf("start returned %v, want a refusal that names ui.brand.icon_file with one prefix", err)
	}
}

// TestLogoPNGStillStartsWhenTooBig: a logo of 3000 pixels starts the server, one Info line says the icons show the mark, and
// one Warn line says it is larger than an uploaded logo may be (decision 141).
func TestLogoPNGStillStartsWhenTooBig(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logo := iconTestFile(t, "logo.png", squarePNG(t, 3000, 3000))
	if _, err := startWithLog(t, &buf, func(c *config.Config) { c.UI.Brand.Name = "Acme Drive"; c.UI.Brand.LogoFile = logo }); err != nil {
		t.Fatalf("a logo of 3000 pixels stopped the start: %v", err)
	}
	info, warn := 0, 0
	for _, line := range strings.Split(buf.String(), "\n") {
		if !strings.Contains(line, "ui.brand.logo_file") {
			continue
		}
		switch {
		case strings.Contains(line, "level=INFO") && strings.Contains(line, "the SelfCloud mark"):
			info++
		case strings.Contains(line, "level=WARN") && strings.Contains(line, "4194304 pixels in all"):
			warn++
		default:
			t.Errorf("another line names the logo: %s", line)
		}
	}
	if info != 1 || warn != 1 {
		t.Errorf("the log names the logo in %d Info and %d Warn lines, want one each:\n%s", info, warn, buf.String())
	}
}

// TestIconLogLines: an SVG with no PNG gets one Info line at start that names ui.brand.icon_file; no file gets none.
func TestIconLogLines(t *testing.T) {
	t.Parallel()
	var svg bytes.Buffer
	icon := iconTestFile(t, "icon.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"></svg>`))
	if _, err := startWithLog(t, &svg, func(c *config.Config) { c.UI.Brand.IconFile = icon }); err != nil {
		t.Fatal(err)
	}
	lines := 0
	for _, l := range strings.Split(svg.String(), "\n") {
		if strings.Contains(l, "ui.brand.icon_file") {
			lines++
			if !strings.Contains(l, "level=INFO") || !strings.Contains(l, "SelfCloud mark") {
				t.Errorf("the line is %q, want an Info line that names the mark", l)
			}
		}
	}
	if lines != 1 {
		t.Errorf("the SVG icon logged %d lines naming ui.brand.icon_file, want one:\n%s", lines, svg.String())
	}

	var none bytes.Buffer
	if _, err := startWithLog(t, &none); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(none.String(), "ui.brand.") {
		t.Errorf("a start with no brand file logged a brand line:\n%s", none.String())
	}
}
