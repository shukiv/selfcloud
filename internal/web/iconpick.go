// SPDX-License-Identifier: AGPL-3.0-or-later

// The installation's picture for the app icons and the favicon (decision 119). The server makes this choice once, at start, from
// the icon file and the logo file it was given as data. The first that applies is used:
//
//  1. ui.brand.icon_file, a PNG (the icons) or an SVG (the favicon);
//  2. ui.brand.logo_file, a PNG for the icons when no icon file is a PNG, and an SVG for the favicon when no icon file is set;
//  3. otherwise the SelfCloud mark on the accent.
//
// A logo PNG the icons cannot use (larger than maxIconPixels on a side, or not decodable) is refused for the icons and the favicon,
// and one line says why. The logo itself is still served at /brand/logo.

package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/png"
	"math"
)

// fullIconPixels is the largest app icon, the 512 pixel one, which the enlargement note compares a logo with.
const fullIconPixels = 512

// maskableRadius is the radius of the circle a platform crops a maskable icon to, as a share of the width (the maskable spec).
const maskableRadius = 0.40

// iconArt is the picture the app icons show. A nil src is the SelfCloud mark, white on the accent. Otherwise src is drawn on
// white: bleed says it fills the icon (an icon file), and otherwise it is fitted inside a margin (a logo). label and sum name the
// source in the icons' validators.
type iconArt struct {
	src   image.Image
	bleed bool
	label string
	sum   string
}

// iconPick is the server's choice of pictures: the icons' art, the favicon, and the lines the server logs at start.
type iconPick struct {
	art     iconArt
	favicon brandSVG
	notes   []string
}

// digest is the hex SHA-256 of a file's bytes, for the validators.
func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// decodeLogo reads a logo PNG for the icons. It is refused, with the line that says why, when it is larger than maxIconPixels on a
// side or cannot be decoded. The pixels are read only after the size is known, so a large file is refused without being decoded.
func decodeLogo(l *Logo) (image.Image, string) {
	cfg, err := png.DecodeConfig(bytes.NewReader(l.data))
	if err != nil {
		return nil, fmt.Sprintf("ui.brand.logo_file cannot be read as a PNG (%v), so the app icons and the favicon show the SelfCloud mark; the logo is still served at /brand/logo", err)
	}
	if cfg.Width > maxIconPixels || cfg.Height > maxIconPixels {
		return nil, fmt.Sprintf("ui.brand.logo_file is %dx%d pixels, more than %d on a side, so the app icons and the favicon show the SelfCloud mark; the logo is still served at /brand/logo", cfg.Width, cfg.Height, maxIconPixels)
	}
	img, err := png.Decode(bytes.NewReader(l.data))
	if err != nil {
		return nil, fmt.Sprintf("ui.brand.logo_file cannot be decoded (%v), so the app icons and the favicon show the SelfCloud mark; the logo is still served at /brand/logo", err)
	}
	return img, ""
}

// pickIcons applies the rule to the two files. The icon file is a PNG or an SVG and was checked at start; the logo is checked here,
// and only when the icons or the favicon would use it.
func pickIcons(icon, logo *Logo, b brandInfo) (iconPick, error) {
	var p iconPick
	iconPNG := icon != nil && icon.contentType == "image/png"
	var logoPic image.Image
	refused := false
	if !iconPNG && logo != nil && logo.contentType == "image/png" {
		img, note := decodeLogo(logo)
		if note != "" {
			p.notes = append(p.notes, note)
			refused = true
		} else {
			logoPic = img
			if w, h := img.Bounds().Dx(), img.Bounds().Dy(); max(w, h) < plainLogoSide(fullIconPixels) {
				p.notes = append(p.notes, fmt.Sprintf("ui.brand.logo_file is %dx%d pixels, smaller than the %d pixel icon, so the app icons enlarge it; a square PNG of %d pixels or more in ui.brand.icon_file gives sharper icons", w, h, fullIconPixels, fullIconPixels))
			}
		}
	}

	switch {
	case iconPNG:
		img, err := png.Decode(bytes.NewReader(icon.data))
		if err != nil {
			return p, fmt.Errorf("web: ui.brand.icon_file cannot be decoded: %w", err)
		}
		p.art = iconArt{src: img, bleed: true, label: "icon", sum: digest(icon.data)}
	case logoPic != nil:
		p.art = iconArt{src: logoPic, label: "logo", sum: digest(logo.data)}
	default:
		p.art = iconArt{label: "mark", sum: digest(brandFile("selfcloud-mark.svg"))}
		if (icon != nil || logo != nil) && !refused {
			p.notes = append(p.notes, "the app icons show the SelfCloud mark, because no PNG icon or logo is set; a square PNG in ui.brand.icon_file gives them a picture")
		}
	}

	p.favicon = brandSVG{body: faviconSVG(b), policy: svgPolicy}
	switch {
	case icon != nil && icon.contentType == "image/svg+xml":
		p.favicon = brandSVG{body: icon.data, policy: svgPolicy}
	case icon == nil && logo != nil && logo.contentType == "image/svg+xml":
		p.favicon = brandSVG{body: logo.data, policy: svgPolicy}
	}
	return p, nil
}

// plainLogoSide is the side of a logo's longer side on a plain icon of size pixels: 80% of the width, a 10% margin on each side.
func plainLogoSide(size int) int {
	return int(math.Round(0.8 * float64(size)))
}

// maskableLogoSide is the side of a logo's box on a maskable icon of size pixels: the square inscribed in the safe circle, whose
// corners are at the circle's radius.
func maskableLogoSide(size int) int {
	return int(math.Round(maskableRadius * math.Sqrt2 * float64(size)))
}
