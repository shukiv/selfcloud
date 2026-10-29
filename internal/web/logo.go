// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MaxLogoBytes caps the logo file. It is read into memory once, at start.
const MaxLogoBytes = 256 << 10

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// Logo is the installation's logo.
type Logo struct {
	data        []byte
	contentType string
}

// LoadLogo reads the logo at path. It must be a PNG or an SVG file of at most MaxLogoBytes, and
// its content must match its extension.
func LoadLogo(path string) (*Logo, error) {
	var ct string
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		ct = "image/png"
	case ".svg":
		ct = "image/svg+xml"
	default:
		return nil, fmt.Errorf("web: logo %s is not a .png or .svg file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("web: logo: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxLogoBytes+1))
	if err != nil {
		return nil, fmt.Errorf("web: logo: %w", err)
	}
	if len(data) > MaxLogoBytes {
		return nil, fmt.Errorf("web: logo %s is larger than %d bytes", path, MaxLogoBytes)
	}
	if ct == "image/png" && !bytes.HasPrefix(data, pngSignature) {
		return nil, fmt.Errorf("web: logo %s is not a PNG file", path)
	}
	if ct == "image/svg+xml" && !bytes.Contains(data, []byte("<svg")) {
		return nil, fmt.Errorf("web: logo %s is not an SVG file", path)
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
		hd.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	} else {
		hd.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	}
	http.ServeContent(w, r, "logo", time.Time{}, bytes.NewReader(l.data))
}
