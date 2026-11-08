// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// pwaHandler is a handler for the brand used by these tests.
func pwaHandler(t *testing.T, name, accent string) *Handler {
	t.Helper()
	files := testFiles()
	files["offline.html"] = &fstest.MapFile{Data: []byte("<p>" + brandPlaceholder + "</p>")}
	return newTestHandler(t, files, Options{BrandName: name, BrandAccent: accent})
}

func decodePNG(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("not a PNG: %v", err)
	}
	return img
}

// TestManifestFromBrand: the manifest names the installation, carries its accent as the theme colour, starts on the
// file list in standalone mode, and lists the icons with the maskable one marked.
func TestManifestFromBrand(t *testing.T) {
	h := pwaHandler(t, "Acme Drive", "#053d8e")
	rec := get(t, h, http.MethodGet, manifestPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/manifest+json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var m webManifest
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("manifest is not JSON: %v", err)
	}
	if m.Name != "Acme Drive" || m.ShortName != "Acme Drive" {
		t.Errorf("name %q short_name %q", m.Name, m.ShortName)
	}
	if m.ThemeColor != "#053D8E" {
		t.Errorf("theme_color = %q, want the accent", m.ThemeColor)
	}
	if m.StartURL != "/files" || m.Scope != "/" || m.Display != "standalone" || m.ID != "/" {
		t.Errorf("start_url %q scope %q display %q id %q", m.StartURL, m.Scope, m.Display, m.ID)
	}
	if m.BackgroundColor != pageColour {
		t.Errorf("background_color = %q", m.BackgroundColor)
	}
	if len(m.Icons) != 3 {
		t.Fatalf("icons = %d, want 192, 512 and the maskable 512", len(m.Icons))
	}
	if m.Icons[2].Purpose != "maskable" || m.Icons[2].Sizes != "512x512" {
		t.Errorf("maskable icon = %+v", m.Icons[2])
	}
}

// TestShortNameIsCut: a long name is cut to twelve characters, whole ones, and no trailing space stays.
func TestShortNameIsCut(t *testing.T) {
	if got := shortName("Selfcloud"); got != "Selfcloud" {
		t.Errorf("short name = %q", got)
	}
	if got := shortName("Ab Cd Ef Gh Ij Kl Mn"); got != "Ab Cd Ef Gh" {
		// Twelve runes: the cut leaves no partial character.
		t.Errorf("short name = %q", got)
	}
	// Twelve runes end on a space here, which is trimmed: a whole word stays whole.
	if got := shortName("שלום ברוכים הבאים"); got != "שלום ברוכים" {
		t.Errorf("short name = %q", got)
	}
	if got := shortName("Twelve chars  x"); strings.HasSuffix(got, " ") {
		t.Errorf("short name %q ends with a space", got)
	}
}

// TestManifestNameDefaults: an empty name is SelfCloud, and a malformed accent is the default.
func TestManifestNameDefaults(t *testing.T) {
	h := pwaHandler(t, "", "not a colour")
	var m webManifest
	if err := json.Unmarshal(get(t, h, http.MethodGet, manifestPath).Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m.Name != "SelfCloud" || m.ThemeColor != defaultAccent {
		t.Errorf("name %q theme %q", m.Name, m.ThemeColor)
	}
}

// TestIconsAreDrawnInTheAccent: every icon is a PNG of its size, its corner is the accent, and its centre is the white
// glyph. The apple icon is 180 pixels.
func TestIconsAreDrawnInTheAccent(t *testing.T) {
	h := pwaHandler(t, "Acme", "#053D8E")
	accent := color.RGBA{R: 5, G: 61, B: 142, A: 255}
	for _, s := range iconSpecs {
		rec := get(t, h, http.MethodGet, s.path)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", s.path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
			t.Errorf("%s: Content-Type %q", s.path, ct)
		}
		img := decodePNG(t, rec.Body.Bytes())
		if b := img.Bounds(); b.Dx() != s.size || b.Dy() != s.size {
			t.Errorf("%s: %dx%d, want %d", s.path, b.Dx(), b.Dy(), s.size)
		}
		if got := color.RGBAModel.Convert(img.At(0, 0)).(color.RGBA); got != accent {
			t.Errorf("%s: corner %v, want the accent", s.path, got)
		}
		mid := s.size / 2
		if got := color.RGBAModel.Convert(img.At(mid, mid)).(color.RGBA); got.R < 250 || got.G < 250 || got.B < 250 {
			t.Errorf("%s: centre %v is not the white glyph", s.path, got)
		}
	}
}

// TestMaskableIconKeepsTheGlyphInTheSafeZone: outside the central circle (80% of the width) a maskable icon is only the
// accent, so a platform that crops to the circle never cuts the glyph.
func TestMaskableIconKeepsTheGlyphInTheSafeZone(t *testing.T) {
	h := pwaHandler(t, "Acme", "#053D8E")
	img := decodePNG(t, get(t, h, http.MethodGet, "/brand/icon-512-maskable.png").Body.Bytes())
	accent := color.RGBA{R: 5, G: 61, B: 142, A: 255}
	const n = 512
	r := 0.4 * n
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			dx, dy := float64(x)+0.5-n/2, float64(y)+0.5-n/2
			if math.Hypot(dx, dy) < r+1 {
				continue
			}
			if got := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA); got != accent {
				t.Fatalf("pixel (%d,%d) outside the safe zone is %v, not the accent", x, y, got)
			}
		}
	}
}

// TestIconValidator: a repeat request with the ETag gets 304, and the ETag changes with the accent.
func TestIconValidator(t *testing.T) {
	h := pwaHandler(t, "Acme", "#053D8E")
	first := get(t, h, http.MethodGet, "/brand/icon-192.png")
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}
	req := httptest.NewRequest(http.MethodGet, "/brand/icon-192.png", nil)
	req.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Errorf("repeat with ETag: status %d, want 304", rec.Code)
	}
	other := pwaHandler(t, "Acme", "#A00000")
	if got := get(t, other, http.MethodGet, "/brand/icon-192.png").Header().Get("ETag"); got == etag {
		t.Error("a different accent kept the same ETag")
	}
}

// TestPWAPathsAreServedOnlyWhenTheHandlerHasThem: the manifest and icons are answered by the web handler, and nothing else
// under /brand is.
func TestPWAPathsAreServedOnlyWhenTheHandlerHasThem(t *testing.T) {
	h := pwaHandler(t, "Acme", "#053D8E")
	for _, p := range []string{manifestPath, "/brand/icon-192.png", "/brand/apple-touch-icon.png"} {
		if !h.Serves(httptest.NewRequest(http.MethodGet, p, nil)) {
			t.Errorf("%s is not served", p)
		}
	}
	if h.Serves(httptest.NewRequest(http.MethodGet, "/brand/other.png", nil)) {
		t.Error("an unknown /brand path is served")
	}
	if h.Serves(httptest.NewRequest(http.MethodPost, manifestPath, nil)) {
		t.Error("a POST is served")
	}
}

// TestOfflinePageNamesTheBrand: the offline page carries the installation's name, escaped.
func TestOfflinePageNamesTheBrand(t *testing.T) {
	files := testFiles()
	files["offline.html"] = &fstest.MapFile{Data: []byte("<title>" + brandPlaceholder + "</title>")}
	h := newTestHandler(t, files, Options{BrandName: `<b>Acme & Co</b>`})
	body := get(t, h, http.MethodGet, "/offline.html").Body.String()
	if strings.Contains(body, brandPlaceholder) {
		t.Fatalf("the placeholder was not replaced: %q", body)
	}
	if !strings.Contains(body, "&lt;b&gt;Acme &amp; Co&lt;/b&gt;") {
		t.Errorf("name not escaped: %q", body)
	}
}

// TestCSPAllowsOnlyTheManifestAndWorkerFromSelf: the policy names the manifest and the worker, and nothing wider.
func TestCSPAllowsOnlyTheManifestAndWorkerFromSelf(t *testing.T) {
	h := pwaHandler(t, "Acme", "#053D8E")
	csp := get(t, h, http.MethodGet, "/offline.html").Header().Get("Content-Security-Policy")
	for _, want := range []string{"manifest-src 'self'", "worker-src 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("policy %q lacks %q", csp, want)
		}
	}
}

// glyphAlphaAt reads the glyph's coverage at (x, y) from a drawn icon: the white share of the pixel over the accent R channel.
func glyphAlphaAt(img image.Image, x, y int) float64 {
	const accentR = 5 // #053D8E
	r, _, _, _ := img.At(x, y).RGBA()
	a := (float64(r>>8) - accentR) / (255 - accentR)
	return math.Min(1, math.Max(0, a))
}

// TestGlyphEdgeIsAntiAliased: the glyph's edge has intermediate alpha at 180, 192 and 512, so the edge is smooth. The band of
// intermediate pixels is thin next to the solid glyph, which keeps it crisp rather than blurred.
func TestGlyphEdgeIsAntiAliased(t *testing.T) {
	accent := color.RGBA{R: 5, G: 61, B: 142, A: 255}
	for _, size := range []int{180, 192, 512} {
		img := drawIcon(size, false, accent, false)
		solid, partial := 0, 0
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				a := glyphAlphaAt(img, x, y)
				switch {
				case a >= 0.999:
					solid++
				case a > 0.02 && a < 0.98:
					partial++
				}
			}
		}
		if partial == 0 {
			t.Errorf("size %d: no pixel has intermediate alpha, so the edge is stepped", size)
		}
		if float64(partial) > 0.25*float64(solid) {
			t.Errorf("size %d: %d edge pixels for %d solid ones: the edge is blurred", size, partial, solid)
		}
	}
}

// TestGlyphBoundingBoxIsSixtyPercentOfWidth: on a plain icon the glyph's box is 60% of the width, centred in the icon, at
// 180, 192 and 512. The box is read from the pixels that are at least half glyph, to within two pixels.
func TestGlyphBoundingBoxIsSixtyPercentOfWidth(t *testing.T) {
	accent := color.RGBA{R: 5, G: 61, B: 142, A: 255}
	for _, size := range []int{180, 192, 512} {
		img := drawIcon(size, false, accent, false)
		minX, minY, maxX, maxY := size, size, -1, -1
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				if glyphAlphaAt(img, x, y) >= 0.5 {
					minX, maxX = min(minX, x), max(maxX, x)
					minY, maxY = min(minY, y), max(maxY, y)
				}
			}
		}
		want := 0.60 * float64(size)
		if w := float64(maxX - minX + 1); math.Abs(w-want) > 2 {
			t.Errorf("size %d: glyph width %v px, want %.1f (60%%)", size, w, want)
		}
		cx, cy := float64(minX+maxX+1)/2, float64(minY+maxY+1)/2
		if math.Abs(cx-float64(size)/2) > 2 || math.Abs(cy-float64(size)/2) > 2 {
			t.Errorf("size %d: glyph centre (%v, %v), want the icon's centre", size, cx, cy)
		}
	}
}

// TestMaskableGlyphIsInsideTheSafeCircle: every pixel of the glyph on a maskable icon lies within the circle of radius 40% of
// the size, so no platform crop cuts it.
func TestMaskableGlyphIsInsideTheSafeCircle(t *testing.T) {
	accent := color.RGBA{R: 5, G: 61, B: 142, A: 255}
	const n = 512
	img := drawIcon(n, true, accent, false)
	limit := 0.40 * n
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if glyphAlphaAt(img, x, y) <= 0 {
				continue
			}
			if d := math.Hypot(float64(x)+0.5-n/2, float64(y)+0.5-n/2); d > limit {
				t.Fatalf("glyph pixel (%d,%d) at radius %.1f is outside the safe circle (%.1f)", x, y, d, limit)
			}
		}
	}
}

// TestUnsafeAssetRequest: a path under /assets/ is unsafe unless it is its own clean form.
func TestUnsafeAssetRequest(t *testing.T) {
	cases := map[string]bool{
		"/assets/app-abc.js":         false,
		"/assets/a%20b.js":           false,
		"/assets//app.js":            true,
		"/assets/../index.html":      true,
		"/assets/..%2fapi%2fv1%2fme": true,
		"/assets/a%5cb.js":           true,
		"/assets/%2e%2e/x":           true,
		"/assets/x%25y.js":           true,
		"/files/../assets/x.js":      false,
	}
	for target, want := range cases {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		if got := UnsafeAssetRequest(req); got != want {
			t.Errorf("%s: unsafe = %v, want %v", target, got, want)
		}
	}
}

// TestOfflineETagCoversTheBrand: the offline page's validator changes when the installation is renamed, and not otherwise.
func TestOfflineETagCoversTheBrand(t *testing.T) {
	etag := func(name string) string {
		h := pwaHandler(t, name, "#053D8E")
		return get(t, h, http.MethodGet, "/offline.html").Header().Get("ETag")
	}
	a, b := etag("Acme"), etag("Acme")
	if a == "" || a != b {
		t.Fatalf("same brand gives ETag %q and %q", a, b)
	}
	if c := etag("Renamed"); c == a {
		t.Errorf("a rename kept the ETag %q", a)
	}
}

// TestManifestShareTarget: the share target takes files only, as a POST of multipart/form-data to /share-target, in one
// field called files that accepts any file (decision 82). No title, text or url member: the manifest carries exactly these.
// "*/*" is a criterion the W3C Web Share Target Level 2 spec lists for "any file" (its accept algorithm, "If criterion is */*").
func TestManifestShareTarget(t *testing.T) {
	h := pwaHandler(t, "Acme", "#053D8E")
	var m map[string]any
	if err := json.Unmarshal(get(t, h, http.MethodGet, manifestPath).Body.Bytes(), &m); err != nil {
		t.Fatalf("manifest is not JSON: %v", err)
	}
	want := map[string]any{
		"action":  "/share-target",
		"method":  "POST",
		"enctype": "multipart/form-data",
		"params": map[string]any{
			"files": []any{map[string]any{"name": "files", "accept": []any{"*/*"}}},
		},
	}
	wantJSON, _ := json.Marshal(want)
	gotJSON, _ := json.Marshal(m["share_target"])
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("share_target = %s, want %s", gotJSON, wantJSON)
	}
}
