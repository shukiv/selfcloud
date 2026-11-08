// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"image/color"
)

// artworkHandler serves the default brand: the name is SelfCloud and the installation has no logo file, so the artwork shows
// (decision 109).
func artworkHandler(t *testing.T, accent string) *Handler {
	t.Helper()
	return newTestHandler(t, testFiles(), Options{BrandName: "SelfCloud", BrandAccent: accent, BrandArtwork: true})
}

// coveredPixels returns the pixels of an icon that are not the plain accent, with their distance from the centre.
func coveredPixels(t *testing.T, h *Handler, path string, accent color.RGBA) (xs, ys []int) {
	t.Helper()
	img := decodePNG(t, get(t, h, http.MethodGet, path).Body.Bytes())
	n := img.Bounds().Dx()
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if got := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA); got != accent {
				xs = append(xs, x)
				ys = append(ys, y)
			}
		}
	}
	return xs, ys
}

// TestDefaultNameAndManifest: the default name is SelfCloud, and the manifest carries it.
func TestDefaultNameAndManifest(t *testing.T) {
	h := newTestHandler(t, testFiles(), Options{BrandName: "", BrandAccent: "#053D8E", BrandArtwork: true})
	var m webManifest
	if err := json.Unmarshal(get(t, h, http.MethodGet, manifestPath).Body.Bytes(), &m); err != nil {
		t.Fatalf("manifest is not JSON: %v", err)
	}
	if m.Name != "SelfCloud" || m.ShortName != "SelfCloud" {
		t.Errorf("name %q short_name %q, want SelfCloud", m.Name, m.ShortName)
	}
}

// TestArtworkIconDrawsTheMark: the default brand's plain icon has the mark, white on the accent. Its top edge is at the
// mark's top point (252.24, 119.85 in the icon's own units), so a pixel just inside it is white and one just above it is the
// accent; and the mark spans 60% of the icon's width.
func TestArtworkIconDrawsTheMark(t *testing.T) {
	accent := color.RGBA{R: 5, G: 61, B: 142, A: 255}
	h := artworkHandler(t, "#053D8E")
	img := decodePNG(t, get(t, h, http.MethodGet, "/brand/icon-512.png").Body.Bytes())
	inside := color.RGBAModel.Convert(img.At(249, 160)).(color.RGBA)
	above := color.RGBAModel.Convert(img.At(249, 150)).(color.RGBA)
	if inside.R < 240 || inside.G < 240 {
		t.Errorf("pixel inside the mark's top is %v, want white", inside)
	}
	if above != accent {
		t.Errorf("pixel above the mark's top is %v, want the accent", above)
	}
	xs, _ := coveredPixels(t, h, "/brand/icon-512.png", accent)
	lo, hi := 512, 0
	for _, x := range xs {
		lo, hi = min(lo, x), max(hi, x)
	}
	if w := hi - lo + 1; math.Abs(float64(w)-0.6*512) > 3 {
		t.Errorf("the mark is %d px wide, want 60%% of 512 (307)", w)
	}
}

// TestArtworkMaskableStaysInTheSafeCircle: every pixel of the mark on the maskable icon is inside the 0.40 circle, measured to
// the pixel's centre. This is the outline, not its box.
func TestArtworkMaskableStaysInTheSafeCircle(t *testing.T) {
	accent := color.RGBA{R: 5, G: 61, B: 142, A: 255}
	h := artworkHandler(t, "#053D8E")
	xs, ys := coveredPixels(t, h, "/brand/icon-512-maskable.png", accent)
	if len(xs) == 0 {
		t.Fatal("the maskable icon has no mark")
	}
	for i := range xs {
		dx, dy := float64(xs[i])+0.5-256, float64(ys[i])+0.5-256
		if math.Hypot(dx, dy) > 0.4*512 {
			t.Fatalf("mark pixel (%d,%d) is outside the safe circle (radius %.1f)", xs[i], ys[i], math.Hypot(dx, dy))
		}
	}
}

// TestCustomNameKeepsTheGlyph: an installation with its own name and no logo keeps the neutral cloud glyph, and its icon
// has its own validator.
func TestCustomNameKeepsTheGlyph(t *testing.T) {
	h := pwaHandler(t, "Acme", "#053D8E")
	img := decodePNG(t, get(t, h, http.MethodGet, "/brand/icon-512.png").Body.Bytes())
	if c := color.RGBAModel.Convert(img.At(256, 256)).(color.RGBA); c.R < 240 {
		t.Errorf("the glyph's centre is %v, want white", c)
	}
	artwork := get(t, artworkHandler(t, "#053D8E"), http.MethodGet, "/brand/icon-512.png").Header().Get("ETag")
	if got := get(t, h, http.MethodGet, "/brand/icon-512.png").Header().Get("ETag"); got == artwork {
		t.Errorf("the glyph and the mark share the ETag %q", got)
	}
}

// TestFaviconFollowsTheBrand: /favicon.svg is built from the brand. The default brand has the mark, the custom name the glyph,
// both on the accent; a repeat with the ETag gets 304.
func TestFaviconFollowsTheBrand(t *testing.T) {
	art := artworkHandler(t, "#0F766E")
	rec := get(t, art, http.MethodGet, faviconPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/svg+xml" {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `fill="#0F766E"`) || !strings.Contains(body, "M 252.24 119.85") {
		t.Errorf("the default favicon lacks the accent or the mark: %.120s", body)
	}
	if strings.Contains(body, "#2563EB") {
		t.Error("the favicon still has the old static colour")
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}
	req := httptest.NewRequest(http.MethodGet, faviconPath, nil)
	req.Header.Set("If-None-Match", etag)
	again := httptest.NewRecorder()
	art.ServeHTTP(again, req)
	if again.Code != http.StatusNotModified {
		t.Errorf("repeat with the ETag: %d, want 304", again.Code)
	}
	custom := get(t, pwaHandler(t, "Acme", "#0F766E"), http.MethodGet, faviconPath).Body.String()
	if !strings.Contains(custom, "<circle") || strings.Contains(custom, "M 252.24") {
		t.Errorf("the custom favicon is not the glyph: %.120s", custom)
	}
}

// TestBrandSVGsAreServed: the wordmark is served for every installation, the mark is the mark for the default brand and the
// glyph for a custom name. Both are served only with the web interface, like the icons.
func TestBrandSVGsAreServed(t *testing.T) {
	art := artworkHandler(t, "#053D8E")
	for _, p := range []string{wordmarkPath, markPath} {
		if !art.Serves(httptest.NewRequest(http.MethodGet, p, nil)) {
			t.Errorf("%s is not served by the web interface", p)
		}
	}
	if w := get(t, art, http.MethodGet, wordmarkPath).Body.String(); !strings.Contains(w, `aria-label="SelfCloud"`) {
		t.Error("the wordmark file is not the SelfCloud wordmark")
	}
	if m := get(t, art, http.MethodGet, markPath).Body.String(); !strings.Contains(m, "M 252.24 119.85") {
		t.Error("the default mark is not the mark")
	}
	if m := get(t, pwaHandler(t, "Acme", "#053D8E"), http.MethodGet, markPath).Body.String(); !strings.Contains(m, "<circle") {
		t.Error("a custom name's mark is not the glyph")
	}
}

// TestBrandFilesAreTheDocsFiles: the SVGs the server embeds are the ones in docs/brand, byte for byte, so the README and the
// interface show the same artwork.
func TestBrandFilesAreTheDocsFiles(t *testing.T) {
	for _, name := range []string{"selfcloud-logo.svg", "selfcloud-logo-transparent.svg", "selfcloud-mark.svg"} {
		embedded, err := os.ReadFile(filepath.Join("brand", name))
		if err != nil {
			t.Fatal(err)
		}
		docs, err := os.ReadFile(filepath.Join("..", "..", "docs", "brand", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(embedded, docs) {
			t.Errorf("%s differs between internal/web/brand and docs/brand", name)
		}
	}
}
