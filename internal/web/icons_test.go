// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// The icons take their picture from ui.brand.icon_file or ui.brand.logo_file (decision 119). These tests build the handler
// the way the server does, with the files as data.

const (
	testAccent = "#053D8E"
	iconSVG    = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect width="10" height="10" fill="#FF0000"/></svg>`
	logoSVG    = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 40 10"><rect width="40" height="10" fill="#008000"/></svg>`
	// faviconPolicy is the policy of an SVG file that a PNG favicon wraps: the logo's SVG policy, plus the data image.
	faviconPolicy = "default-src 'none'; style-src 'unsafe-inline'; img-src data:; sandbox"
	// svgOnlyPolicy is the policy of an SVG served as it is.
	svgOnlyPolicy = "default-src 'none'; style-src 'unsafe-inline'; sandbox"
)

// encodePNG encodes img as a PNG.
func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// solidPNG is a w by h PNG of one colour.
func solidPNG(t *testing.T, w, h int, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	return encodePNG(t, img)
}

// quadrantPNG is a square PNG of four colours: red top left, green top right, blue bottom left, yellow bottom right.
func quadrantPNG(t *testing.T, n int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			c := color.NRGBA{R: 255, A: 255}
			switch {
			case x >= n/2 && y < n/2:
				c = color.NRGBA{G: 255, A: 255}
			case x < n/2 && y >= n/2:
				c = color.NRGBA{B: 255, A: 255}
			case x >= n/2 && y >= n/2:
				c = color.NRGBA{R: 255, G: 255, A: 255}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return encodePNG(t, img)
}

// logoPNG is a 4:1 PNG of dark green, 800 by 200 pixels.
func logoPNG(t *testing.T) []byte {
	return solidPNG(t, 800, 200, color.NRGBA{G: 128, A: 255})
}

// pngFile and svgFile are the picture files, as the server reads them.
func pngFile(data []byte) *Logo { return &Logo{data: data, contentType: "image/png"} }
func svgFile(s string) *Logo    { return &Logo{data: []byte(s), contentType: "image/svg+xml"} }

// brandHandler builds the handler for a name and the two files. The artwork flag is set as the server sets it: the default
// name with no logo file.
func brandHandler(t *testing.T, name string, icon, logo *Logo) *Handler {
	t.Helper()
	files := testFiles()
	files["offline.html"] = &fstest.MapFile{Data: []byte("<p>" + brandPlaceholder + "</p>")}
	return newTestHandler(t, files, Options{
		BrandName:    name,
		BrandAccent:  testAccent,
		BrandArtwork: name == "SelfCloud" && logo == nil,
		IconFile:     icon,
		LogoFile:     logo,
	})
}

// rgbaAt reads a pixel of a decoded image as an RGBA colour.
func rgbaAt(img image.Image, x, y int) color.RGBA {
	return color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
}

// iconImage fetches an icon and decodes it.
func iconImage(t *testing.T, h *Handler, path string) image.Image {
	t.Helper()
	rec := get(t, h, http.MethodGet, path)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status %d", path, rec.Code)
	}
	return decodePNG(t, rec.Body.Bytes())
}

// favicon fetches /favicon.svg as text.
func favicon(t *testing.T, h *Handler) string {
	t.Helper()
	return get(t, h, http.MethodGet, faviconPath).Body.String()
}

// wrappedPNG returns the PNG a favicon wraps, and fails when the favicon is not the wrapper of a PNG.
func wrappedPNG(t *testing.T, body string) image.Image {
	t.Helper()
	const prefix = `href="data:image/png;base64,`
	i := strings.Index(body, prefix)
	if i < 0 {
		t.Fatalf("the favicon does not wrap a PNG: %.200s", body)
	}
	rest := body[i+len(prefix):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		t.Fatal("the data URI is not closed")
	}
	data, err := base64.StdEncoding.DecodeString(rest[:j])
	if err != nil {
		t.Fatalf("the data URI is not base64: %v", err)
	}
	return decodePNG(t, data)
}

// quadrantsOK checks the four quadrant colours of quadrantPNG at the centres and the corners of an icon of size n.
func quadrantsOK(t *testing.T, what string, img image.Image) {
	t.Helper()
	n := img.Bounds().Dx()
	want := map[[2]int]color.RGBA{
		{n / 4, n / 4}:         {R: 255, A: 255},
		{3 * n / 4, n / 4}:     {G: 255, A: 255},
		{n / 4, 3 * n / 4}:     {B: 255, A: 255},
		{3 * n / 4, 3 * n / 4}: {R: 255, G: 255, A: 255},
		{0, 0}:                 {R: 255, A: 255},
		{n - 1, n - 1}:         {R: 255, G: 255, A: 255},
	}
	for p, c := range want {
		if got := rgbaAt(img, p[0], p[1]); got != c {
			t.Errorf("%s: pixel %v is %v, want %v", what, p, got, c)
		}
	}
}

// markOK checks an icon that shows the mark: the accent in the corner, and some white of the mark. The mark's centre is open,
// so the white is searched for rather than read at the centre.
func markOK(t *testing.T, what string, img image.Image) {
	t.Helper()
	if got := rgbaAt(img, 0, 0); got != (color.RGBA{R: 5, G: 61, B: 142, A: 255}) {
		t.Errorf("%s: corner %v, want the accent", what, got)
	}
	if !hasWhite(img) {
		t.Errorf("%s: no white of the mark on the icon", what)
	}
}

// hasWhite reports whether some pixel of img is white.
func hasWhite(img image.Image) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if c := rgbaAt(img, x, y); c.R >= 240 && c.G >= 240 && c.B >= 240 {
				return true
			}
		}
	}
	return false
}

// logoOK checks a 4:1 logo on a 512 icon: its colour in the centre, and white in the 10% margin on the left and at the top.
func logoOK(t *testing.T, what string, img image.Image) {
	t.Helper()
	if got := rgbaAt(img, 256, 256); got != (color.RGBA{G: 128, A: 255}) {
		t.Errorf("%s: centre %v, want the logo's green", what, got)
	}
	for y := 0; y < 51; y++ {
		for x := 0; x < 512; x++ {
			if got := rgbaAt(img, x, y); got.R != 255 || got.G != 255 || got.B != 255 {
				t.Fatalf("%s: the top margin pixel (%d,%d) is %v, want white", what, x, y, got)
			}
		}
	}
	for y := 0; y < 512; y++ {
		for x := 0; x < 51; x++ {
			if got := rgbaAt(img, x, y); got.R != 255 || got.G != 255 || got.B != 255 {
				t.Fatalf("%s: the left margin pixel (%d,%d) is %v, want white", what, x, y, got)
			}
		}
	}
}

// pickCase is one combination of the two files.
type pickCase struct {
	label string
	icon  *Logo
	logo  *Logo
	// favicon is what /favicon.svg is: "mark", "svg" (the file as it is), or "png" (a PNG wrapped).
	favicon string
	// art is what the 512 icon shows: "mark", "quadrants" (the icon file), or "logo".
	art string
}

// pickCases are the seven combinations the rule distinguishes.
func pickCases(t *testing.T) []pickCase {
	pngIcon := pngFile(quadrantPNG(t, 512))
	svgIcon := svgFile(iconSVG)
	pngLogo := pngFile(logoPNG(t))
	svgLogo := svgFile(logoSVG)
	return []pickCase{
		{label: "no file", favicon: "mark", art: "mark"},
		{label: "icon PNG", icon: pngIcon, favicon: "png", art: "quadrants"},
		{label: "icon SVG", icon: svgIcon, favicon: "svg", art: "mark"},
		{label: "logo PNG", logo: pngLogo, favicon: "png", art: "logo"},
		{label: "logo SVG", logo: svgLogo, favicon: "svg", art: "mark"},
		{label: "icon SVG and logo PNG", icon: svgIcon, logo: pngLogo, favicon: "svg", art: "logo"},
		{label: "icon PNG and logo SVG", icon: pngIcon, logo: svgLogo, favicon: "png", art: "quadrants"},
	}
}

// TestPickTable: for each combination of files and for the default and a custom name, /favicon.svg and the 512 icon are the
// ones the rule names. The mark shows on the accent for the default name and for a custom one alike.
func TestPickTable(t *testing.T) {
	for _, tc := range pickCases(t) {
		for _, name := range []string{"SelfCloud", "Acme Drive"} {
			what := tc.label + " / " + name
			h := brandHandler(t, name, tc.icon, tc.logo)

			body := favicon(t, h)
			switch tc.favicon {
			case "svg":
				want := iconSVG
				if tc.icon == nil {
					want = logoSVG
				}
				if body != want {
					t.Errorf("%s: the favicon is not the SVG file as it is: %.120s", what, body)
				}
			case "png":
				wrappedPNG(t, body)
			case "mark":
				if !strings.Contains(body, "M 252.24 119.85") || !strings.Contains(body, `fill="#053D8E"`) || strings.Contains(body, "<circle") {
					t.Errorf("%s: the favicon is not the mark on the accent: %.120s", what, body)
				}
			}

			img := iconImage(t, h, "/brand/icon-512.png")
			switch tc.art {
			case "mark":
				markOK(t, what, img)
			case "quadrants":
				quadrantsOK(t, what, img)
			case "logo":
				logoOK(t, what, img)
			}
		}
	}
}

// TestMaskableIconFileIsFullBleed: an icon file fills the maskable icon too, to its corners, with no white margin.
func TestMaskableIconFileIsFullBleed(t *testing.T) {
	h := brandHandler(t, "Acme Drive", pngFile(quadrantPNG(t, 512)), nil)
	quadrantsOK(t, "maskable", iconImage(t, h, "/brand/icon-512-maskable.png"))
	quadrantsOK(t, "icon-512", iconImage(t, h, "/brand/icon-512.png"))
}

// TestLogoIsFittedWithItsMargin: a 4:1 logo on the 512 icon is 80% of the width, centred, on white; the apple icon too.
func TestLogoIsFittedWithItsMargin(t *testing.T) {
	h := brandHandler(t, "Acme Drive", nil, pngFile(logoPNG(t)))
	logoOK(t, "icon-512", iconImage(t, h, "/brand/icon-512.png"))
	apple := iconImage(t, h, "/brand/apple-touch-icon.png")
	if got := rgbaAt(apple, 90, 90); got != (color.RGBA{G: 128, A: 255}) {
		t.Errorf("apple icon centre %v, want the logo's green", got)
	}
	if got := rgbaAt(apple, 5, 5); got != (color.RGBA{R: 255, G: 255, B: 255, A: 255}) {
		t.Errorf("apple icon margin %v, want white", got)
	}
}

// TestMaskableLogoStaysInTheSafeSquare: a logo on the maskable icon is inside the square inscribed in the 0.40 circle, so its
// farthest non-white pixel is at most 0.40 of the width from the centre, plus one pixel. A square logo fills that square.
func TestMaskableLogoStaysInTheSafeSquare(t *testing.T) {
	for _, logo := range []*Logo{pngFile(solidPNG(t, 300, 300, color.NRGBA{R: 200, A: 255})), pngFile(logoPNG(t))} {
		h := brandHandler(t, "Acme Drive", nil, logo)
		img := iconImage(t, h, "/brand/icon-512-maskable.png")
		far := 0.0
		for y := 0; y < 512; y++ {
			for x := 0; x < 512; x++ {
				if rgbaAt(img, x, y) == (color.RGBA{R: 255, G: 255, B: 255, A: 255}) {
					continue
				}
				far = math.Max(far, math.Hypot(float64(x)+0.5-256, float64(y)+0.5-256))
			}
		}
		if far > 0.40*512+1 {
			t.Errorf("the farthest logo pixel is %.1f from the centre, want at most %.1f", far, 0.40*512+1)
		}
	}
}

// TestPNGIconsAreOpaque: every PNG icon is opaque everywhere, with a transparent source too: iOS fills transparency with black.
func TestPNGIconsAreOpaque(t *testing.T) {
	half := image.NewNRGBA(image.Rect(0, 0, 512, 512))
	for y := 0; y < 512; y++ {
		for x := 0; x < 256; x++ {
			half.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
		}
	}
	for _, tc := range []struct {
		label string
		icon  *Logo
		logo  *Logo
	}{
		{"icon with transparency", pngFile(encodePNG(t, half)), nil},
		{"logo with transparency", nil, pngFile(encodePNG(t, half))},
		{"logo PNG", nil, pngFile(logoPNG(t))},
	} {
		h := brandHandler(t, "Acme Drive", tc.icon, tc.logo)
		for _, s := range iconSpecs {
			img := iconImage(t, h, s.path)
			b := img.Bounds()
			for y := b.Min.Y; y < b.Max.Y; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					if _, _, _, a := img.At(x, y).RGBA(); a != 0xffff {
						t.Fatalf("%s: %s pixel (%d,%d) has alpha %d, want opaque", tc.label, s.path, x, y, a>>8)
					}
				}
			}
		}
	}
}

// TestIconFileTransparencyIsWhiteOnTheIcon: a transparent part of an icon file shows as white on the icon.
func TestIconFileTransparencyIsWhiteOnTheIcon(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 512, 512))
	for y := 0; y < 512; y++ {
		for x := 256; x < 512; x++ {
			img.SetNRGBA(x, y, color.NRGBA{B: 255, A: 255})
		}
	}
	h := brandHandler(t, "Acme Drive", pngFile(encodePNG(t, img)), nil)
	if got := rgbaAt(iconImage(t, h, "/brand/icon-512.png"), 10, 10); got != (color.RGBA{R: 255, G: 255, B: 255, A: 255}) {
		t.Errorf("the transparent corner is %v, want white", got)
	}
}

// TestFaviconWrapsAPNG: a PNG favicon is an SVG that is well-formed, has a 64-unit viewBox, and holds a PNG of at most 64 by 64
// pixels. Its transparency is kept, and its own policy adds the data image and nothing else.
func TestFaviconWrapsAPNG(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 512, 512))
	for y := 0; y < 512; y++ {
		for x := 256; x < 512; x++ {
			img.SetNRGBA(x, y, color.NRGBA{G: 255, A: 255})
		}
	}
	h := brandHandler(t, "Acme Drive", pngFile(encodePNG(t, img)), nil)
	rec := get(t, h, http.MethodGet, faviconPath)
	body := rec.Body.String()

	d := xml.NewDecoder(strings.NewReader(body))
	for {
		_, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("the favicon is not well-formed XML: %v", err)
		}
	}
	if !strings.HasPrefix(body, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">`) {
		t.Errorf("the favicon's root is %.120s, want a 64-unit viewBox", body)
	}
	if !strings.Contains(body, `<image width="64" height="64" href="data:image/png;base64,`) {
		t.Errorf("the favicon's image is not 64 by 64 with a PNG data URI: %.160s", body)
	}
	if got := rec.Header().Get("Content-Security-Policy"); got != faviconPolicy {
		t.Errorf("the favicon's policy is %q, want %q", got, faviconPolicy)
	}
	wrapped := wrappedPNG(t, body)
	if b := wrapped.Bounds(); b.Dx() != 64 || b.Dy() != 64 {
		t.Errorf("the wrapped PNG is %dx%d, want 64x64", b.Dx(), b.Dy())
	}
	if _, _, _, a := wrapped.At(0, 0).RGBA(); a != 0 {
		t.Errorf("the transparent corner of the favicon has alpha %d, want 0", a>>8)
	}
	if got := rgbaAt(wrapped, 63, 0); got.G != 255 || got.A != 255 {
		t.Errorf("the green corner of the favicon is %v", got)
	}
}

// TestFaviconWrapsALogoPNGWithItsAspect: a 4:1 logo's favicon is 64 by 16 inside the 64-unit box, and keeps its colour.
func TestFaviconWrapsALogoPNGWithItsAspect(t *testing.T) {
	h := brandHandler(t, "Acme Drive", nil, pngFile(logoPNG(t)))
	wrapped := wrappedPNG(t, favicon(t, h))
	if b := wrapped.Bounds(); b.Dx() != 64 || b.Dy() != 16 {
		t.Errorf("the logo's favicon is %dx%d, want 64x16", b.Dx(), b.Dy())
	}
	if got := rgbaAt(wrapped, 32, 8); got != (color.RGBA{G: 128, A: 255}) {
		t.Errorf("the logo's favicon centre is %v, want its green", got)
	}
}

// TestFaviconPolicyForSVGIsUnchanged: an SVG file served as the favicon has the SVG policy, with no image directive.
func TestFaviconPolicyForSVGIsUnchanged(t *testing.T) {
	h := brandHandler(t, "Acme Drive", svgFile(iconSVG), nil)
	if got := get(t, h, http.MethodGet, faviconPath).Header().Get("Content-Security-Policy"); got != svgOnlyPolicy {
		t.Errorf("the SVG favicon's policy is %q, want %q", got, svgOnlyPolicy)
	}
}

// TestIconETagFollowsTheSource: the icons' validator changes when the source's bytes change, and when the source changes from
// the icon file to the logo, and stays the same for the same source.
func TestIconETagFollowsTheSource(t *testing.T) {
	a := quadrantPNG(t, 512)
	b := solidPNG(t, 512, 512, color.NRGBA{R: 1, A: 255})
	etag := func(h *Handler) string { return get(t, h, http.MethodGet, "/brand/icon-512.png").Header().Get("ETag") }
	first := etag(brandHandler(t, "Acme Drive", pngFile(a), nil))
	if again := etag(brandHandler(t, "Acme Drive", pngFile(a), nil)); again != first {
		t.Errorf("the same source gives a different ETag")
	}
	if other := etag(brandHandler(t, "Acme Drive", pngFile(b), nil)); other == first {
		t.Error("a changed icon file kept the ETag")
	}
	if asLogo := etag(brandHandler(t, "Acme Drive", nil, pngFile(a))); asLogo == first {
		t.Error("the same bytes as the logo kept the ETag")
	}
}

// TestGlyphIsGone: a custom name with no logo file shows the SelfCloud mark, in the rail's mark file byte for byte, and in the
// favicon, as the default name does.
func TestGlyphIsGone(t *testing.T) {
	h := brandHandler(t, "Acme Drive", nil, nil)
	if got := get(t, h, http.MethodGet, markPath).Body.Bytes(); !bytes.Equal(got, brandFile("selfcloud-mark.svg")) {
		t.Error("the custom name's mark is not the mark file")
	}
	body := favicon(t, h)
	if !strings.Contains(body, "M 252.24 119.85") || strings.Contains(body, "<circle") {
		t.Errorf("the custom name's favicon is not the mark: %.120s", body)
	}
}

// TestIconFileIsCheckedAtStart: an icon file is refused when its extension, its size, its shape or its content is wrong, and
// when it is missing; each refusal names ui.brand.icon_file.
func TestIconFileIsCheckedAtStart(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	sq := func(n int) []byte { return solidPNG(t, n, n, color.NRGBA{R: 9, A: 255}) }
	for _, tc := range []struct {
		label string
		path  string
	}{
		{"a .jpg", write("icon.jpg", sq(512))},
		{"no extension", write("icon", sq(512))},
		{"a non-square PNG", write("wide.png", solidPNG(t, 512, 256, color.NRGBA{A: 255}))},
		{"511 pixels", write("511.png", sq(511))},
		{"2049 pixels", write("2049.png", sq(2049))},
		{"over 256 KiB", write("big.png", append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, MaxLogoBytes)...))},
		{"SVG content in a .png", write("fake.png", []byte("<svg></svg>"))},
		{"plain text in a .svg", write("fake.svg", []byte("plain text"))},
		{"a missing file", filepath.Join(dir, "missing.png")},
	} {
		_, err := LoadIconFile(tc.path)
		if err == nil {
			t.Errorf("%s: accepted", tc.label)
		} else if !strings.Contains(err.Error(), "ui.brand.icon_file") {
			t.Errorf("%s: the refusal does not name the setting: %v", tc.label, err)
		}
	}
	for _, p := range []string{write("ok512.png", sq(512)), write("ok2048.png", sq(2048)), write("ok.svg", []byte(iconSVG))} {
		if _, err := LoadIconFile(p); err != nil {
			t.Errorf("%s refused: %v", filepath.Base(p), err)
		}
	}
}

// TestLogoPNGIsCheckedBeforeItIsUsed: a logo that is too big or cannot be decoded is still served and still starts, but the
// icons do not use it: they show the mark, and one line says why.
func TestLogoPNGIsCheckedBeforeItIsUsed(t *testing.T) {
	big := pngFile(encodePNG(t, image.NewNRGBA(image.Rect(0, 0, 3000, 3000))))
	good := logoPNG(t)
	for _, tc := range []struct {
		label string
		logo  *Logo
	}{
		{"3000 pixels", big},
		{"truncated", pngFile(good[:len(good)/2])},
	} {
		h := brandHandler(t, "Acme Drive", nil, tc.logo)
		markOK(t, tc.label, iconImage(t, h, "/brand/icon-512.png"))
		notes := h.IconNotes()
		if len(notes) != 1 || !strings.Contains(notes[0], "ui.brand.logo_file") {
			t.Errorf("%s: the notes are %q, want one line naming ui.brand.logo_file", tc.label, notes)
		}
	}
}

// TestIconNotes: the start lines. An SVG with no PNG for the icons gets one line that names ui.brand.icon_file; no file and a
// PNG icon get none; a logo PNG smaller than the 512 icon gets one that suggests an icon file of 512 pixels or more.
func TestIconNotes(t *testing.T) {
	svgIcon := svgFile(iconSVG)
	cases := []struct {
		label     string
		icon      *Logo
		logo      *Logo
		wantLines int
		mentions  string
	}{
		{"no file", nil, nil, 0, ""},
		{"icon PNG", pngFile(quadrantPNG(t, 512)), nil, 0, ""},
		{"icon SVG", svgIcon, nil, 1, "ui.brand.icon_file"},
		{"logo SVG", nil, svgFile(logoSVG), 1, "ui.brand.icon_file"},
		{"logo PNG of 800 pixels", nil, pngFile(logoPNG(t)), 0, ""},
		{"logo PNG of 200 pixels", nil, pngFile(solidPNG(t, 200, 50, color.NRGBA{A: 255})), 1, "512"},
	}
	for _, tc := range cases {
		notes := brandHandler(t, "Acme Drive", tc.icon, tc.logo).IconNotes()
		if len(notes) != tc.wantLines {
			t.Errorf("%s: %d lines %q, want %d", tc.label, len(notes), notes, tc.wantLines)
			continue
		}
		if tc.mentions != "" && !strings.Contains(notes[0], tc.mentions) {
			t.Errorf("%s: the line %q does not mention %s", tc.label, notes[0], tc.mentions)
		}
	}
}
