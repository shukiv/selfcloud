// SPDX-License-Identifier: AGPL-3.0-or-later

// The installable web app (decision 65): the manifest at /manifest.webmanifest and the icons under /brand/. Both are built
// from the installation's own brand, so each installation installs as itself. They exist only with the web interface:
// the handler that serves them is the one the server builds when ui.enabled is on, and headless mode answers 404.
//
// The icons are drawn here with the standard library only: the accent is the background, and the white glyph of
// pwa_glyph.go is drawn over it, each pixel blended by its coverage. A custom logo is not used as an icon; the
// logo stays the sidebar's picture (decision 65).

package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"html"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// manifestPath is the web app manifest.
	manifestPath = "/manifest.webmanifest"
	// pageColour is the page background of the light theme (web/src/theme/tokens.ts, LIGHT.page). The manifest's
	// background_color is one value, so it takes the light theme's page.
	pageColour = "#EDF0F6"
	// defaultAccent is the accent the configuration starts with (internal/config).
	defaultAccent = "#053D8E"
	// shortNameRunes caps short_name, which the home screen shows under the icon.
	shortNameRunes = 12
	// brandPlaceholder is the text the offline page carries where the installation's name goes; the server replaces it
	// when it serves the page, so the page the worker caches shows the name.
	brandPlaceholder = "__SELFCLOUD_BRAND__"
)

// brandHTML is the name for the offline page, escaped for HTML.
func brandHTML(name string) string {
	return html.EscapeString(name)
}

// iconSpec is one icon the server draws.
type iconSpec struct {
	path     string
	size     int
	maskable bool
}

// iconSpecs are the icons, at fixed paths. The apple-touch-icon is not maskable: iOS rounds it itself.
var iconSpecs = []iconSpec{
	{path: "/brand/icon-192.png", size: 192},
	{path: "/brand/icon-512.png", size: 512},
	{path: "/brand/icon-512-maskable.png", size: 512, maskable: true},
	{path: "/brand/apple-touch-icon.png", size: 180},
}

// icon is a drawn icon, with the validator that answers a repeat request with 304.
type icon struct {
	data []byte
	etag string
}

// brandInfo is the installation's name and accent, for the manifest and the icons.
type brandInfo struct {
	name   string
	accent color.RGBA
	hex    string
}

// newBrandInfo reads the brand's name and accent. A missing name is "Selfcloud", a missing or malformed accent the default.
func newBrandInfo(name, accent string) brandInfo {
	if name == "" {
		name = "Selfcloud"
	}
	c, ok := parseHexColour(accent)
	if !ok {
		c, _ = parseHexColour(defaultAccent)
		accent = defaultAccent
	}
	return brandInfo{name: name, accent: c, hex: strings.ToUpper(accent)}
}

// parseHexColour reads #rrggbb.
func parseHexColour(s string) (color.RGBA, bool) {
	if len(s) != 7 || s[0] != '#' {
		return color.RGBA{}, false
	}
	n, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return color.RGBA{}, false
	}
	return color.RGBA{R: uint8(n >> 16), G: uint8(n >> 8), B: uint8(n), A: 255}, true
}

// isPWAPath reports whether p is the manifest or one of the icons.
func isPWAPath(p string) bool {
	if p == manifestPath {
		return true
	}
	for _, s := range iconSpecs {
		if p == s.path {
			return true
		}
	}
	return false
}

// drawIcon draws one icon: the accent everywhere, and the white glyph over it, each pixel blended by its coverage.
func drawIcon(size int, maskable bool, accent color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	scale := glyphScale(size, maskable)
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			a := glyphCoverage(px, py, size, scale)
			img.SetRGBA(px, py, color.RGBA{
				R: blend(accent.R, a),
				G: blend(accent.G, a),
				B: blend(accent.B, a),
				A: 255,
			})
		}
	}
	return img
}

// blend mixes a channel of the accent with white by a: 0 keeps the accent, 1 is white.
func blend(c uint8, a float64) uint8 {
	return uint8(float64(c)*(1-a) + 255*a + 0.5)
}

// encodeIcon draws the icon and encodes it as a PNG. Its validator is a hash of the accent, the glyph, the kind of icon
// and its size, so a change to any of them changes the ETag.
func encodeIcon(spec iconSpec, b brandInfo) (icon, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, drawIcon(spec.size, spec.maskable, b.accent)); err != nil {
		return icon{}, err
	}
	sum := sha256.Sum256([]byte(b.hex + "\n" + glyphSpec() + "\n" + spec.path + "\n" + strconv.Itoa(spec.size)))
	return icon{data: buf.Bytes(), etag: `"` + hex.EncodeToString(sum[:16]) + `"`}, nil
}

// manifestIcon is one entry of the manifest's icons.
type manifestIcon struct {
	Src     string `json:"src"`
	Sizes   string `json:"sizes"`
	Type    string `json:"type"`
	Purpose string `json:"purpose,omitempty"`
}

// webManifest is the manifest. It is built per request from the brand.
type webManifest struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	ShortName       string         `json:"short_name"`
	StartURL        string         `json:"start_url"`
	Scope           string         `json:"scope"`
	Display         string         `json:"display"`
	ThemeColor      string         `json:"theme_color"`
	BackgroundColor string         `json:"background_color"`
	Icons           []manifestIcon `json:"icons"`
}

// shortName cuts the name to shortNameRunes characters, on a whole character, and drops a trailing space.
func shortName(name string) string {
	if utf8.RuneCountInString(name) <= shortNameRunes {
		return name
	}
	r := []rune(name)[:shortNameRunes]
	return strings.TrimSpace(string(r))
}

// manifestFor builds the manifest for the brand. The start page is the file list: the people who install the app open it
// on their files.
func manifestFor(b brandInfo) webManifest {
	icons := make([]manifestIcon, 0, len(iconSpecs))
	for _, s := range iconSpecs {
		if s.path == "/brand/apple-touch-icon.png" {
			continue
		}
		mi := manifestIcon{Src: s.path, Sizes: strconv.Itoa(s.size) + "x" + strconv.Itoa(s.size), Type: "image/png"}
		if s.maskable {
			mi.Purpose = "maskable"
		}
		icons = append(icons, mi)
	}
	return webManifest{
		ID:              "/",
		Name:            b.name,
		ShortName:       shortName(b.name),
		StartURL:        "/files",
		Scope:           "/",
		Display:         "standalone",
		ThemeColor:      b.hex,
		BackgroundColor: pageColour,
		Icons:           icons,
	}
}

// servePWA answers the manifest or an icon. It reports false when p is neither.
func (h *Handler) servePWA(w http.ResponseWriter, r *http.Request) bool {
	p := r.URL.Path
	if p == manifestPath {
		data, err := json.Marshal(manifestFor(h.brand))
		if err != nil {
			http.Error(w, "web: cannot build the manifest", http.StatusInternalServerError)
			return true
		}
		hd := w.Header()
		hd.Set("Content-Type", "application/manifest+json")
		hd.Set("Cache-Control", "no-cache")
		hd.Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, "manifest.webmanifest", time.Time{}, bytes.NewReader(data))
		return true
	}
	ic, ok := h.icons[p]
	if !ok {
		return false
	}
	hd := w.Header()
	hd.Set("Content-Type", "image/png")
	hd.Set("Cache-Control", "no-cache")
	hd.Set("ETag", ic.etag)
	hd.Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "icon.png", time.Time{}, bytes.NewReader(ic.data))
	return true
}

// buildIcons draws every icon for the brand.
func buildIcons(b brandInfo) (map[string]icon, error) {
	out := make(map[string]icon, len(iconSpecs))
	for _, s := range iconSpecs {
		ic, err := encodeIcon(s, b)
		if err != nil {
			return nil, err
		}
		out[s.path] = ic
	}
	return out, nil
}
