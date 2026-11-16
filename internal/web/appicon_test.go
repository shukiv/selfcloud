// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

// The app icon (ui.brand.app_icon_file) is the picture of the installed app's icons alone: it never reaches the favicon,
// which keeps decision 119's rule. These tests build the handler the way the server does, with the files as data.

// appColour is the app icon's own colour, which no other picture in these tests carries.
var appColour = color.NRGBA{R: 128, B: 128, A: 255}

// appPNG is a square PNG of the app icon's colour.
func appPNG(t *testing.T, n int) []byte {
	t.Helper()
	return solidPNG(t, n, n, appColour)
}

// appBrandHandler builds the handler with the app icon beside the icon and the logo.
func appBrandHandler(t *testing.T, name string, appIcon, icon, logo *Logo) *Handler {
	t.Helper()
	files := testFiles()
	files["offline.html"] = &fstest.MapFile{Data: []byte("<p>" + brandPlaceholder + "</p>")}
	return newTestHandler(t, files, Options{
		BrandName:    name,
		BrandAccent:  testAccent,
		BrandArtwork: logo == nil,
		AppIconFile:  appIcon,
		IconFile:     icon,
		LogoFile:     logo,
	})
}

// appOK checks an icon that shows the app icon: its colour at the centre and in every corner, edge to edge.
func appOK(t *testing.T, what string, img image.Image) {
	t.Helper()
	n := img.Bounds().Dx()
	want := color.RGBA{R: 128, B: 128, A: 255}
	for _, p := range [][2]int{{n / 2, n / 2}, {0, 0}, {n - 1, 0}, {0, n - 1}, {n - 1, n - 1}} {
		if got := rgbaAt(img, p[0], p[1]); got != want {
			t.Errorf("%s: pixel %v is %v, want the app icon's colour", what, p, got)
		}
	}
}

// TestAppIconRule: the app icon supplies the icons' art ahead of the icon file and the logo, and the favicon keeps
// decision 119's rule: the icon file, else the logo, else the mark.
func TestAppIconRule(t *testing.T) {
	t.Parallel()
	app := pngFile(appPNG(t, 512))
	pngIcon := pngFile(quadrantPNG(t, 512))
	svgIcon := svgFile(iconSVG)
	pngLogo := pngFile(logoPNG(t))
	svgLogo := svgFile(logoSVG)
	for _, tc := range []struct {
		label   string
		icon    *Logo
		logo    *Logo
		favicon string
	}{
		{"app icon alone", nil, nil, "mark"},
		{"app icon and icon PNG", pngIcon, nil, "icon-png"},
		{"app icon and icon SVG", svgIcon, nil, "svg"},
		{"app icon and logo PNG", nil, pngLogo, "logo-png"},
		{"app icon and logo SVG", nil, svgLogo, "svg-logo"},
		{"app icon, icon SVG and logo PNG", svgIcon, pngLogo, "svg"},
	} {
		h := appBrandHandler(t, "Acme Drive", app, tc.icon, tc.logo)
		appOK(t, tc.label, iconImage(t, h, "/brand/icon-512.png"))
		body := favicon(t, h)
		switch tc.favicon {
		case "mark":
			if !strings.Contains(body, "M 252.24 119.85") || !strings.Contains(body, `fill="#053D8E"`) {
				t.Errorf("%s: the favicon is not the mark: %.120s", tc.label, body)
			}
		case "svg":
			if body != iconSVG {
				t.Errorf("%s: the favicon is not the icon SVG as it is: %.120s", tc.label, body)
			}
		case "svg-logo":
			if body != logoSVG {
				t.Errorf("%s: the favicon is not the logo SVG as it is: %.120s", tc.label, body)
			}
		case "icon-png":
			quadrantsOK(t, tc.label+" favicon", wrappedPNG(t, body))
		case "logo-png":
			wrapped := wrappedPNG(t, body)
			if b := wrapped.Bounds(); b.Dx() != 64 || b.Dy() != 16 {
				t.Errorf("%s: the logo's favicon is %dx%d, want 64x16", tc.label, b.Dx(), b.Dy())
			}
			if got := rgbaAt(wrapped, 32, 8); got != (color.RGBA{G: 128, A: 255}) {
				t.Errorf("%s: the favicon's centre is %v, want the logo's green", tc.label, got)
			}
		}
	}
}

// TestAppIconFillsEveryIcon: every icon the server draws shows the app icon edge to edge, the maskable and the apple one
// too, with no white margin.
func TestAppIconFillsEveryIcon(t *testing.T) {
	t.Parallel()
	h := appBrandHandler(t, "Acme Drive", pngFile(appPNG(t, 512)), nil, nil)
	for _, s := range iconSpecs {
		appOK(t, s.path, iconImage(t, h, s.path))
	}
}

// TestAppIconValidator: the icons' validator names the app icon as its own source, so the same bytes as the icon file give
// a different ETag, and the link-preview picture follows the app icon too.
func TestAppIconValidator(t *testing.T) {
	t.Parallel()
	data := appPNG(t, 512)
	h := appBrandHandler(t, "Acme Drive", pngFile(data), nil, nil)
	want := sha256.Sum256([]byte(strings.ToUpper(testAccent) + "\n" + "app-icon" + "\n" + digest(data) + "\n" +
		"/brand/icon-512.png" + "\n512\n" + "render=" + strconv.Itoa(iconRenderer)))
	if got := get(t, h, http.MethodGet, "/brand/icon-512.png").Header().Get("ETag"); got != `"`+hex.EncodeToString(want[:16])+`"` {
		t.Errorf("the icon's ETag %s is not the hash of the app-icon source", got)
	}
	asIcon := get(t, brandHandler(t, "Acme Drive", pngFile(data), nil), http.MethodGet, "/brand/icon-512.png").Header().Get("ETag")
	if asIcon == get(t, h, http.MethodGet, "/brand/icon-512.png").Header().Get("ETag") {
		t.Error("the same bytes as the icon file kept the ETag")
	}
	plain := get(t, brandHandler(t, "Acme Drive", nil, nil), http.MethodGet, ogImagePath).Header().Get("ETag")
	if got := get(t, h, http.MethodGet, ogImagePath).Header().Get("ETag"); got == plain {
		t.Error("the link-preview picture kept the mark's ETag")
	}
}

// TestAppIconNotes: with the app icon set there is no line about the icons: no mark line, no enlargement line and no
// refusal line for a logo the icons do not use. Without it, the mark line names ui.brand.app_icon_file.
func TestAppIconNotes(t *testing.T) {
	t.Parallel()
	app := pngFile(appPNG(t, 512))
	smallLogo := pngFile(solidPNG(t, 200, 50, color.NRGBA{A: 255}))
	bigLogo := pngFile(encodePNG(t, image.NewNRGBA(image.Rect(0, 0, 3000, 3000))))
	for _, tc := range []struct {
		label string
		icon  *Logo
		logo  *Logo
	}{
		{"app icon alone", nil, nil},
		{"app icon and icon SVG", svgFile(iconSVG), nil},
		{"app icon and icon PNG", pngFile(quadrantPNG(t, 512)), nil},
		{"app icon and a small logo", nil, smallLogo},
		{"app icon and a refused logo", nil, bigLogo},
	} {
		if notes := appBrandHandler(t, "Acme Drive", app, tc.icon, tc.logo).IconNotes(); len(notes) != 0 {
			t.Errorf("%s: notes %q, want none", tc.label, notes)
		}
	}
	notes := appBrandHandler(t, "Acme Drive", nil, svgFile(iconSVG), nil).IconNotes()
	if len(notes) != 1 || !strings.Contains(notes[0], "ui.brand.app_icon_file") {
		t.Errorf("the mark line %q does not name ui.brand.app_icon_file", notes)
	}
}

// TestAppIconWithRefusedLogo: a logo the favicon cannot use leaves the favicon on the mark while the app icon still
// supplies the icons, and no line says the icons show the mark.
func TestAppIconWithRefusedLogo(t *testing.T) {
	t.Parallel()
	big := pngFile(encodePNG(t, image.NewNRGBA(image.Rect(0, 0, 3000, 3000))))
	h := appBrandHandler(t, "Acme Drive", pngFile(appPNG(t, 512)), nil, big)
	appOK(t, "icon-512", iconImage(t, h, "/brand/icon-512.png"))
	body := favicon(t, h)
	if !strings.Contains(body, "M 252.24 119.85") || strings.Contains(body, "data:image/png") {
		t.Errorf("the favicon is not the mark: %.120s", body)
	}
	if notes := h.IconNotes(); len(notes) != 0 {
		t.Errorf("notes %q, want none", notes)
	}
}

// TestNoAppIconKeepsTheIcons: with no app icon, every combination of the icon and the logo draws the same icons, the same
// favicon and the same validators as before the app icon existed.
func TestNoAppIconKeepsTheIcons(t *testing.T) {
	t.Parallel()
	for _, tc := range pickCases(t) {
		old := brandHandler(t, "Acme Drive", tc.icon, tc.logo)
		new := appBrandHandler(t, "Acme Drive", nil, tc.icon, tc.logo)
		if a, b := favicon(t, old), favicon(t, new); a != b {
			t.Errorf("%s: the favicon changed", tc.label)
		}
		for _, s := range iconSpecs {
			a := get(t, old, http.MethodGet, s.path)
			b := get(t, new, http.MethodGet, s.path)
			if !bytes.Equal(a.Body.Bytes(), b.Body.Bytes()) || a.Header().Get("ETag") != b.Header().Get("ETag") {
				t.Errorf("%s: %s changed", tc.label, s.path)
			}
		}
		if a, b := old.IconNotes(), new.IconNotes(); strings.Join(a, "\n") != strings.Join(b, "\n") {
			t.Errorf("%s: the notes changed: %q vs %q", tc.label, a, b)
		}
	}
}

// TestAppIconFileIsCheckedAtStart: an app icon file is refused when its extension, its size, its shape or its content is
// wrong, and when it is missing; each refusal names ui.brand.app_icon_file. An SVG is refused by its extension alone.
func TestAppIconFileIsCheckedAtStart(t *testing.T) {
	t.Parallel()
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
		{"an .svg", write("app.svg", []byte(iconSVG))},
		{"a .jpg", write("app.jpg", sq(512))},
		{"no extension", write("app", sq(512))},
		{"a non-square PNG", write("wide.png", solidPNG(t, 512, 256, color.NRGBA{A: 255}))},
		{"511 pixels", write("511.png", sq(511))},
		{"2049 pixels", write("2049.png", sq(2049))},
		{"over 256 KiB", write("big.png", append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, MaxLogoBytes)...))},
		{"SVG content in a .png", write("fake.png", []byte("<svg></svg>"))},
		{"a missing file", filepath.Join(dir, "missing.png")},
	} {
		_, err := LoadAppIconFile(tc.path)
		if err == nil {
			t.Errorf("%s: accepted", tc.label)
		} else if !strings.Contains(err.Error(), "ui.brand.app_icon_file") {
			t.Errorf("%s: the refusal does not name the setting: %v", tc.label, err)
		}
	}
	for _, p := range []string{write("ok512.png", sq(512)), write("ok2048.png", sq(2048))} {
		if _, err := LoadAppIconFile(p); err != nil {
			t.Errorf("%s refused: %v", filepath.Base(p), err)
		}
	}
}

// TestNewAppIconChecksTheBytes: an uploaded app icon is a PNG only, square, 512 to 2048 pixels, at most MaxLogoBytes, and
// decodable; an SVG is refused whatever its bytes.
func TestNewAppIconChecksTheBytes(t *testing.T) {
	t.Parallel()
	good := appPNG(t, 512)
	for _, tc := range []struct {
		label string
		data  []byte
		ct    string
	}{
		{"an SVG", []byte(iconSVG), TypeSVG},
		{"SVG bytes as PNG", []byte(iconSVG), TypePNG},
		{"a non-square PNG", solidPNG(t, 600, 512, appColour), TypePNG},
		{"511 pixels", appPNG(t, 511), TypePNG},
		{"2049 pixels", appPNG(t, 2049), TypePNG},
		{"over 256 KiB", append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, MaxLogoBytes)...), TypePNG},
		{"cut short", good[:len(good)/2], TypePNG},
		{"another type", good, "image/jpeg"},
	} {
		if _, err := NewAppIcon(tc.data, tc.ct); err == nil {
			t.Errorf("%s: accepted", tc.label)
		}
	}
	img, err := NewAppIcon(good, TypePNG)
	if err != nil {
		t.Fatalf("a square PNG of 512 refused: %v", err)
	}
	if img.ContentType() != TypePNG || img.SHA256() != digest(good) {
		t.Errorf("the picture is %s %s", img.ContentType(), img.SHA256())
	}
	if _, err := png.Decode(bytes.NewReader(good)); err != nil {
		t.Fatal(err)
	}
}
