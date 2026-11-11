// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MaxLogoBytes caps a picture: the logo file and the icon file, read at start, and a logo or an icon an admin uploads
// (decision 141).
const MaxLogoBytes = 256 << 10

// The icon file is a square PNG of at least minIconPixels and at most maxIconPixels on a side (decision 119). The icons need at
// most 512 pixels; the cap keeps the decoded picture at 16 MB.
const (
	minIconPixels = 512
	maxIconPixels = 2048
)

// An uploaded logo PNG is at most maxLogoSide pixels on a side and maxLogoPixels in all, read from its header (decision
// 141). Every page that shows the logo has the browser decode it whole, so the cap keeps that at 16 MB, as the icon's cap
// does, while a wide logo of 4096 by 1024 still fits. The page shows the logo at most 64 pixels high.
const (
	maxLogoSide   = 4096
	maxLogoPixels = maxIconPixels * maxIconPixels
)

// The checks a picture gets: the logo file's, an uploaded logo's, or an icon's, from the file or an upload.
type pictureRule int

const (
	ruleLogoFile pictureRule = iota
	ruleLogo
	ruleIcon
)

// errUnreadablePNG is a PNG whose size cannot be read from its header.
var errUnreadablePNG = errors.New("is not a readable PNG")

// The types a picture may have.
const (
	TypePNG = "image/png"
	TypeSVG = "image/svg+xml"
)

// svgPolicy is the policy of an SVG the server serves: it may carry styles, and it runs nothing and loads nothing.
const svgPolicy = "default-src 'none'; style-src 'unsafe-inline'; sandbox"

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// Logo is an installation's picture: the logo, or the icon (decision 119), from the config file or an upload (decision 141).
// It is never changed.
type Logo struct {
	data        []byte
	contentType string
	// sum is the hex SHA-256 of data: the logo's strong validator, and the hash the settings answer and the audit show.
	sum string
	// uploaded says an admin uploaded the picture, so the start's notes do not call it by the config file's key.
	uploaded bool
}

// ContentType is the picture's type: TypePNG or TypeSVG.
func (l *Logo) ContentType() string { return l.contentType }

// SHA256 is the hex SHA-256 of the picture's bytes.
func (l *Logo) SHA256() string { return l.sum }

// SizeWarning says when the logo file is a PNG larger than an uploaded logo may be. The file is not refused for it, so an
// installation that starts today still starts, but every page that shows the logo has the browser decode it whole. It is
// empty for any other logo.
func (l *Logo) SizeWarning() string {
	if l.uploaded || l.contentType != TypePNG {
		return ""
	}
	if err := checkLogoPixels(l.data); err != nil && !errors.Is(err, errUnreadablePNG) {
		return fmt.Sprintf("ui.brand.logo_file %s. The file is still used, but every page that shows the logo has the "+
			"browser decode it whole, so a smaller one loads faster", err)
	}
	return ""
}

// setting is what the notes call a logo: its config key, or the uploaded logo.
func (l *Logo) setting() string {
	if l.uploaded {
		return "the uploaded logo"
	}
	return "ui.brand.logo_file"
}

// checkPicture is the one check of a picture's bytes against its declared type, whether they come from the config file, an
// upload or the brand folder (decision 141). A picture is at most MaxLogoBytes. A PNG must carry the PNG signature. An SVG
// must contain <svg and must not be a PNG. An uploaded logo that is a PNG must have a header that gives its size, within
// the logo's pixel cap; the logo file is not held to that cap. An icon that is a PNG must be square, 512 to 2048 pixels on
// a side, read from its header before its pixels are, so a picture that claims to be larger is never decoded; then it must
// decode. The error names no file: the caller says which picture it is.
func checkPicture(data []byte, ct string, rule pictureRule) error {
	if len(data) > MaxLogoBytes {
		return fmt.Errorf("is larger than %d bytes", MaxLogoBytes)
	}
	switch ct {
	case TypePNG:
		if !bytes.HasPrefix(data, pngSignature) {
			return errors.New("is not a PNG file")
		}
	case TypeSVG:
		if bytes.HasPrefix(data, pngSignature) {
			return errors.New("is a PNG file, not an SVG file")
		}
		if !bytes.Contains(data, []byte("<svg")) {
			return errors.New("is not an SVG file")
		}
	default:
		return errors.New("is not a PNG or an SVG picture")
	}
	if ct != TypePNG || rule == ruleLogoFile {
		return nil
	}
	if rule == ruleLogo {
		return checkLogoPixels(data)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("%w: %w", errUnreadablePNG, err)
	}
	if cfg.Width != cfg.Height || cfg.Width < minIconPixels || cfg.Width > maxIconPixels {
		return fmt.Errorf("must be a square PNG of %d to %d pixels, not %dx%d", minIconPixels, maxIconPixels, cfg.Width, cfg.Height)
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return fmt.Errorf("cannot be decoded: %w", err)
	}
	return nil
}

// checkLogoPixels reads a logo PNG's size from its header, and nothing past it, and holds it to the logo's pixel cap. Each
// side is compared before the two are multiplied, so the product cannot overflow.
func checkLogoPixels(data []byte) error {
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("%w: %w", errUnreadablePNG, err)
	}
	if cfg.Width > maxLogoSide || cfg.Height > maxLogoSide || cfg.Width*cfg.Height > maxLogoPixels {
		return fmt.Errorf("is %dx%d pixels; a logo PNG is at most %d on a side and %d pixels in all", cfg.Width, cfg.Height,
			maxLogoSide, maxLogoPixels)
	}
	return nil
}

// NewLogo checks the bytes of an uploaded logo, declared as contentType, as the logo file is checked, and a PNG against the
// logo's pixel cap too (decision 141). The picture keeps data, which the caller must not change.
func NewLogo(data []byte, contentType string) (*Logo, error) {
	return newUpload("the logo", data, contentType, ruleLogo)
}

// NewIcon checks the bytes of an uploaded icon, declared as contentType, as the icon file is checked (decision 141). The
// picture keeps data, which the caller must not change.
func NewIcon(data []byte, contentType string) (*Logo, error) {
	return newUpload("the icon", data, contentType, ruleIcon)
}

func newUpload(what string, data []byte, ct string, rule pictureRule) (*Logo, error) {
	if err := checkPicture(data, ct, rule); err != nil {
		return nil, fmt.Errorf("%s %w", what, err)
	}
	return &Logo{data: data, contentType: ct, sum: digest(data), uploaded: true}, nil
}

// ReadPicture reads the picture file at path, and no more than one byte past MaxLogoBytes of it, so a larger file is refused
// by the check without being read whole.
func ReadPicture(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, MaxLogoBytes+1))
}

// LoadLogo reads the logo at path. It must be a PNG or an SVG file of at most MaxLogoBytes, and its content must match its
// extension. A PNG larger than an upload may be is kept, with SizeWarning saying so.
func LoadLogo(path string) (*Logo, error) {
	return loadBrandImage("ui.brand.logo_file", path, ruleLogoFile)
}

// LoadIconFile reads the icon file at path: an SVG, or a square PNG of 512 to 2048 pixels on a side. The PNG's size is read
// before its pixels are, so an oversized one is refused without being decoded.
func LoadIconFile(path string) (*Logo, error) {
	return loadBrandImage("ui.brand.icon_file", path, ruleIcon)
}

// loadBrandImage reads a PNG or SVG file for the setting named setting, checks it, and names that setting in every error.
func loadBrandImage(setting, path string, rule pictureRule) (*Logo, error) {
	var ct string
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		ct = TypePNG
	case ".svg":
		ct = TypeSVG
	default:
		return nil, fmt.Errorf("web: %s %s is not a .png or .svg file", setting, path)
	}
	data, err := ReadPicture(path)
	if err != nil {
		return nil, fmt.Errorf("web: %s: %w", setting, err)
	}
	if err := checkPicture(data, ct, rule); err != nil {
		return nil, fmt.Errorf("web: %s %s %w", setting, path, err)
	}
	return &Logo{data: data, contentType: ct, sum: digest(data)}, nil
}

// ServeHTTP serves the logo. An SVG may carry styles but runs nothing and loads nothing, and a browser that opens it directly
// sandboxes it. Its strong validator is the hash of its bytes, so a repeat request gets 304, and no-cache makes a browser ask
// each time, so a changed logo shows on the next load (decision 141).
func (l *Logo) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hd := w.Header()
	hd.Set("Content-Type", l.contentType)
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Cache-Control", "no-cache")
	if l.sum != "" {
		hd.Set("ETag", `"`+l.sum+`"`)
	}
	if l.contentType == TypeSVG {
		hd.Set("Content-Security-Policy", svgPolicy)
	} else {
		hd.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	}
	http.ServeContent(w, r, "logo", time.Time{}, bytes.NewReader(l.data))
}
