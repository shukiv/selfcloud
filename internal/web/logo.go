// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MaxLogoBytes caps the logo file and the icon file. Each is read into memory once, at start.
const MaxLogoBytes = 256 << 10

// The icon file is a square PNG of at least minIconPixels and at most maxIconPixels on a side (decision 119). The icons need at
// most 512 pixels; the cap keeps the decoded picture at 16 MB.
const (
	minIconPixels = 512
	maxIconPixels = 2048
)

// svgPolicy is the policy of an SVG the server serves: it may carry styles, and it runs nothing and loads nothing.
const svgPolicy = "default-src 'none'; style-src 'unsafe-inline'; sandbox"

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// Logo is an installation's picture file: the logo, or the icon file (decision 119).
type Logo struct {
	data        []byte
	contentType string
}

// LoadLogo reads the logo at path. It must be a PNG or an SVG file of at most MaxLogoBytes, and its content must match its
// extension.
func LoadLogo(path string) (*Logo, error) {
	return loadBrandImage("ui.brand.logo_file", path)
}

// LoadIconFile reads the icon file at path: an SVG, or a square PNG of 512 to 2048 pixels on a side. The PNG's size is read
// before its pixels are, so an oversized one is refused without being decoded.
func LoadIconFile(path string) (*Logo, error) {
	l, err := loadBrandImage("ui.brand.icon_file", path)
	if err != nil {
		return nil, err
	}
	if l.contentType == "image/png" {
		cfg, err := png.DecodeConfig(bytes.NewReader(l.data))
		if err != nil {
			return nil, fmt.Errorf("web: ui.brand.icon_file %s is not a readable PNG: %w", path, err)
		}
		if cfg.Width != cfg.Height || cfg.Width < minIconPixels || cfg.Width > maxIconPixels {
			return nil, fmt.Errorf("web: ui.brand.icon_file %s must be a square PNG of %d to %d pixels, not %dx%d",
				path, minIconPixels, maxIconPixels, cfg.Width, cfg.Height)
		}
	}
	return l, nil
}

// loadBrandImage reads a PNG or SVG file for the setting named setting, and names that setting in every error.
func loadBrandImage(setting, path string) (*Logo, error) {
	var ct string
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		ct = "image/png"
	case ".svg":
		ct = "image/svg+xml"
	default:
		return nil, fmt.Errorf("web: %s %s is not a .png or .svg file", setting, path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("web: %s: %w", setting, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxLogoBytes+1))
	if err != nil {
		return nil, fmt.Errorf("web: %s: %w", setting, err)
	}
	if len(data) > MaxLogoBytes {
		return nil, fmt.Errorf("web: %s %s is larger than %d bytes", setting, path, MaxLogoBytes)
	}
	if ct == "image/png" && !bytes.HasPrefix(data, pngSignature) {
		return nil, fmt.Errorf("web: %s %s is not a PNG file", setting, path)
	}
	if ct == "image/svg+xml" && !bytes.Contains(data, []byte("<svg")) {
		return nil, fmt.Errorf("web: %s %s is not an SVG file", setting, path)
	}
	return &Logo{data: data, contentType: ct}, nil
}

// ServeHTTP serves the logo. An SVG may carry styles but runs nothing and loads nothing, and a
// browser that opens it directly sandboxes it.
func (l *Logo) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hd := w.Header()
	hd.Set("Content-Type", l.contentType)
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Cache-Control", "no-cache")
	if l.contentType == "image/svg+xml" {
		hd.Set("Content-Security-Policy", svgPolicy)
	} else {
		hd.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	}
	http.ServeContent(w, r, "logo", time.Time{}, bytes.NewReader(l.data))
}
