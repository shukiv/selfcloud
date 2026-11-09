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
	"encoding/base64"
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
	var iconPic image.Image
	if icon != nil && icon.contentType == "image/png" {
		img, err := png.Decode(bytes.NewReader(icon.data))
		if err != nil {
			return p, fmt.Errorf("web: ui.brand.icon_file cannot be decoded: %w", err)
		}
		// The picture is reduced once, to the largest size an icon or the favicon uses, and every size is drawn from that: a 2048
		// pixel file is read in full once, not once per icon.
		iconPic = scaleTo(img, fullIconPixels, fullIconPixels)
	}
	var logoPic image.Image
	refused := false
	if iconPic == nil && logo != nil && logo.contentType == "image/png" {
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

	if logoPic != nil {
		// The logo is reduced once, to the largest box a plain icon fits it in; the smaller sizes are drawn from that.
		b := logoPic.Bounds()
		w, h := fitSize(b.Dx(), b.Dy(), plainLogoSide(fullIconPixels))
		logoPic = scaleTo(logoPic, w, h)
	}

	switch {
	case iconPic != nil:
		p.art = iconArt{src: iconPic, bleed: true, label: "icon", sum: digest(icon.data)}
	case logoPic != nil:
		p.art = iconArt{src: logoPic, label: "logo", sum: digest(logo.data)}
	default:
		p.art = iconArt{label: "mark", sum: digest(brandFile("selfcloud-mark.svg"))}
		if (icon != nil || logo != nil) && !refused {
			p.notes = append(p.notes, "the app icons show the SelfCloud mark, because no PNG icon or logo is set; a square PNG in ui.brand.icon_file gives them a picture")
		}
	}

	// The favicon: the icon file if it is set, else the logo, else the mark (decision 119). An SVG is served as it is; a PNG is
	// wrapped.
	var err error
	p.favicon = brandSVG{body: faviconSVG(b), policy: svgPolicy}
	switch {
	case icon != nil && icon.contentType == "image/svg+xml":
		p.favicon = brandSVG{body: icon.data, policy: svgPolicy}
	case iconPic != nil:
		p.favicon, err = faviconPNG(iconPic)
	case icon == nil && logoPic != nil:
		p.favicon, err = faviconPNG(logoPic)
	case icon == nil && logo != nil && logo.contentType == "image/svg+xml":
		p.favicon = brandSVG{body: logo.data, policy: svgPolicy}
	}
	if err != nil {
		return p, fmt.Errorf("web: cannot draw the favicon: %w", err)
	}
	return p, nil
}

// faviconPolicy is the policy of a favicon that wraps a PNG: the logo's SVG policy, plus the data image the wrapper loads.
const faviconPolicy = "default-src 'none'; style-src 'unsafe-inline'; img-src data:; sandbox"

// faviconPNG wraps a picture as the favicon: an SVG of 64 units holding the picture as a PNG, scaled to fit 64 by 64 with its
// aspect kept. The picture's transparency is kept, so a transparent part of it shows the tab's own colour (decision 119).
func faviconPNG(img image.Image) (brandSVG, error) {
	b := img.Bounds()
	w, h := fitSize(b.Dx(), b.Dy(), 64)
	var buf bytes.Buffer
	if err := png.Encode(&buf, scaleTo(img, w, h)); err != nil {
		return brandSVG{}, err
	}
	body := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><image width="64" height="64" href="data:image/png;base64,` +
		base64.StdEncoding.EncodeToString(buf.Bytes()) + `"/></svg>`
	return brandSVG{body: []byte(body), policy: faviconPolicy}, nil
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
