// SPDX-License-Identifier: AGPL-3.0-or-later

// The installable web app (decision 65): the manifest at /manifest.webmanifest and the icons under /brand/. Both are built
// from the installation's own brand, so each installation installs as itself. They exist only with the web interface:
// the handler that serves them is the one the server builds when ui.enabled is on, and headless mode answers 404.
//
// The icons are drawn here with the standard library only. Each shows the installation's picture: its icon file, or its logo
// (a PNG), or else the SelfCloud mark white on the accent (decisions 65 and 119; iconpick.go makes the choice). The picture is
// scaled by scale.go. The sidebar still shows the logo as it did.

package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"html"
	"image"
	"image/color"
	"image/draw"
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

// brandInfo is the installation's name and accent, for the manifest and the icons. artwork says the installation shows the
// SelfCloud artwork, which is the wordmark in the sidebar (decision 109); the icons do not depend on it.
type brandInfo struct {
	name    string
	accent  color.RGBA
	hex     string
	artwork bool
}

// newBrandInfo reads the brand's name and accent. A missing name is "SelfCloud", a missing or malformed accent the default.
func newBrandInfo(name, accent string, artwork bool) brandInfo {
	if name == "" {
		name = "SelfCloud"
	}
	c, ok := parseHexColour(accent)
	if !ok {
		c, _ = parseHexColour(defaultAccent)
		accent = defaultAccent
	}
	return brandInfo{name: name, accent: c, hex: strings.ToUpper(accent), artwork: artwork}
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

// isPWAPath reports whether p is the manifest, one of the icons, or one of the brand's SVG files.
func isPWAPath(p string) bool {
	if p == manifestPath || isBrandSVGPath(p) {
		return true
	}
	for _, s := range iconSpecs {
		if p == s.path {
			return true
		}
	}
	return false
}

// drawIcon draws one icon. With no picture it is the accent with the white SelfCloud mark over it, each pixel blended by its
// coverage. With a picture it is drawn on white (decision 119): an icon file fills the icon, edge to edge, and a logo is fitted
// inside a margin, the 80% of the width on a plain icon and the safe square on a maskable one. Either way the icon is opaque.
func drawIcon(spec iconSpec, accent color.RGBA, art iconArt) *image.RGBA {
	size := spec.size
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	if art.src != nil {
		draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
		if art.bleed {
			draw.Draw(img, img.Bounds(), scaleTo(art.src, size, size), image.Point{}, draw.Over)
			return img
		}
		side := plainLogoSide(size)
		if spec.maskable {
			side = maskableLogoSide(size)
		}
		b := art.src.Bounds()
		w, h := fitSize(b.Dx(), b.Dy(), side)
		x0, y0 := (size-w)/2, (size-h)/2
		draw.Draw(img, image.Rect(x0, y0, x0+w, y0+h), scaleTo(art.src, w, h), image.Point{}, draw.Over)
		return img
	}
	cover := markCoverage(size)
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			a := cover[py*size+px]
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

// iconRenderer is the version of how the icons' pixels are drawn: the scaler, the geometry and the mark. It is in every icon's
// validator, so a change to the drawing refreshes the icons a client has cached. Bump it when the pixels change.
const iconRenderer = 1

// encodeIcon draws the icon and encodes it as a PNG. Its validator is a hash of the accent, the picture (the mark, or the icon
// file or the logo, with the digest of its bytes), the icon's path and its size, so a change to any of them changes the ETag.
func encodeIcon(spec iconSpec, b brandInfo, art iconArt) (icon, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, drawIcon(spec, b.accent, art)); err != nil {
		return icon{}, err
	}
	sum := sha256.Sum256([]byte(b.hex + "\n" + art.label + "\n" + art.sum + "\n" + spec.path + "\n" + strconv.Itoa(spec.size) +
		"\nrender=" + strconv.Itoa(iconRenderer)))
	return icon{data: buf.Bytes(), etag: `"` + hex.EncodeToString(sum[:16]) + `"`}, nil
}

// manifestIcon is one entry of the manifest's icons.
type manifestIcon struct {
	Src     string `json:"src"`
	Sizes   string `json:"sizes"`
	Type    string `json:"type"`
	Purpose string `json:"purpose,omitempty"`
}

// shareTargetPath is the share target's address: the share sheet's form post lands here (decision 82). The service worker
// answers the post; with no worker in control the server answers 303 to /share and keeps nothing.
const shareTargetPath = "/share-target"

// shareTarget is the manifest's share_target (decision 82): files only, a POST of multipart/form-data, one field called
// files that accepts any file. The W3C Web Share Target spec allows "*/*" in accept.
type shareTarget struct {
	Action  string           `json:"action"`
	Method  string           `json:"method"`
	Enctype string           `json:"enctype"`
	Params  shareTargetParam `json:"params"`
}

// shareTargetParam names the form fields of a share.
type shareTargetParam struct {
	Files []shareTargetFile `json:"files"`
}

// shareTargetFile is one files entry: the field's name and the media types it accepts.
type shareTargetFile struct {
	Name   string   `json:"name"`
	Accept []string `json:"accept"`
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
	ShareTarget     shareTarget    `json:"share_target"`
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
		ShareTarget: shareTarget{
			Action:  shareTargetPath,
			Method:  http.MethodPost,
			Enctype: "multipart/form-data",
			Params: shareTargetParam{Files: []shareTargetFile{
				{Name: "files", Accept: []string{"*/*"}},
			}},
		},
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
	if body, ok := h.svgs[p]; ok {
		serveBrandSVG(w, r, body)
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

// serveBrandSVG answers one of the brand's SVG files, under the policy it was given. Its ETag is a hash of the file, so a repeat
// request gets 304. Its policy runs no script and loads nothing, as the installation's logo does.
func serveBrandSVG(w http.ResponseWriter, r *http.Request, f brandSVG) {
	sum := sha256.Sum256(f.body)
	hd := w.Header()
	hd.Set("Content-Type", "image/svg+xml")
	hd.Set("Cache-Control", "no-cache")
	hd.Set("ETag", `"`+hex.EncodeToString(sum[:16])+`"`)
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Content-Security-Policy", f.policy)
	http.ServeContent(w, r, "brand.svg", time.Time{}, bytes.NewReader(f.body))
}

// buildIcons draws every icon for the brand, from the picture the rule chose (decision 119).
func buildIcons(b brandInfo, art iconArt) (map[string]icon, error) {
	out := make(map[string]icon, len(iconSpecs))
	for _, s := range iconSpecs {
		ic, err := encodeIcon(s, b, art)
		if err != nil {
			return nil, err
		}
		out[s.path] = ic
	}
	return out, nil
}
