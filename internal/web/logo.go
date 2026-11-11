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

// setting is what the notes call a logo: its config key, or the uploaded logo.
func (l *Logo) setting() string {
	if l.uploaded {
		return "the uploaded logo"
	}
	return "ui.brand.logo_file"
}

// checkPicture is the one check of a picture's bytes against its declared type, whether they come from the config file, an
// upload or the brand folder (decision 141). A picture is at most MaxLogoBytes. A PNG must carry the PNG signature. An SVG
// must contain <svg and must not be a PNG. An icon that is a PNG must be square, 512 to 2048 pixels on a side, read from its
// header before its pixels are, so a picture that claims to be larger is never decoded; then it must decode. The error names
// no file: the caller says which picture it is.
func checkPicture(data []byte, ct string, icon bool) error {
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
	if !icon || ct != TypePNG {
		return nil
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("is not a readable PNG: %w", err)
	}
	if cfg.Width != cfg.Height || cfg.Width < minIconPixels || cfg.Width > maxIconPixels {
		return fmt.Errorf("must be a square PNG of %d to %d pixels, not %dx%d", minIconPixels, maxIconPixels, cfg.Width, cfg.Height)
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return fmt.Errorf("cannot be decoded: %w", err)
	}
	return nil
}

// NewLogo checks the bytes of an uploaded logo, declared as contentType, as the logo file is checked (decision 141). The
// picture keeps data, which the caller must not change.
func NewLogo(data []byte, contentType string) (*Logo, error) {
	return newUpload("the logo", data, contentType, false)
}

// NewIcon checks the bytes of an uploaded icon, declared as contentType, as the icon file is checked (decision 141). The
// picture keeps data, which the caller must not change.
func NewIcon(data []byte, contentType string) (*Logo, error) {
	return newUpload("the icon", data, contentType, true)
}

func newUpload(what string, data []byte, ct string, icon bool) (*Logo, error) {
	if err := checkPicture(data, ct, icon); err != nil {
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
// extension.
func LoadLogo(path string) (*Logo, error) {
	return loadBrandImage("ui.brand.logo_file", path, false)
}

// LoadIconFile reads the icon file at path: an SVG, or a square PNG of 512 to 2048 pixels on a side. The PNG's size is read
// before its pixels are, so an oversized one is refused without being decoded.
func LoadIconFile(path string) (*Logo, error) {
	return loadBrandImage("ui.brand.icon_file", path, true)
}

// loadBrandImage reads a PNG or SVG file for the setting named setting, checks it, and names that setting in every error.
func loadBrandImage(setting, path string, icon bool) (*Logo, error) {
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
	if err := checkPicture(data, ct, icon); err != nil {
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
