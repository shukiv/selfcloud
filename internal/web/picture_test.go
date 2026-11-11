// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The pictures an admin uploads go through the same checker as the config file's pictures (decision 141). These tests give
// it bytes and a declared type, as the upload route does.

// TestNewLogoChecksTheBytes: a logo is a PNG with the PNG signature or an SVG that has <svg, of at most 256 KiB, and its
// declared type must be its content's. No refusal names a path.
func TestNewLogoChecksTheBytes(t *testing.T) {
	pngBytes := logoPNG(t)
	for _, tc := range []struct {
		label    string
		data     []byte
		ct, want string
	}{
		{"SVG sent as PNG", []byte(logoSVG), "image/png", "not a PNG"},
		{"PNG sent as SVG", append(bytes.Clone(pngBytes), []byte("<svg>")...), "image/svg+xml", "is a PNG"},
		{"text sent as SVG", []byte("plain text"), "image/svg+xml", "not an SVG"},
		{"over 256 KiB", append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, MaxLogoBytes)...), "image/png", "larger than 262144 bytes"},
		{"a JPEG", []byte("\xff\xd8\xff"), "image/jpeg", "PNG or an SVG"},
		{"empty", nil, "image/png", "not a PNG"},
	} {
		l, err := NewLogo(tc.data, tc.ct)
		if err == nil {
			t.Errorf("%s: accepted as %s", tc.label, l.ContentType())
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %q does not say %q", tc.label, err, tc.want)
		}
		if strings.Contains(err.Error(), "/") && !strings.Contains(err.Error(), "image/") {
			t.Errorf("%s: %q names a path", tc.label, err)
		}
	}
	for _, tc := range []struct {
		data []byte
		ct   string
	}{
		{pngBytes, "image/png"},
		{[]byte(logoSVG), "image/svg+xml"},
		{append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, MaxLogoBytes-8)...), "image/png"},
	} {
		l, err := NewLogo(tc.data, tc.ct)
		if err != nil {
			t.Errorf("%s of %d bytes refused: %v", tc.ct, len(tc.data), err)
			continue
		}
		sum := sha256.Sum256(tc.data)
		if l.ContentType() != tc.ct || l.SHA256() != hex.EncodeToString(sum[:]) {
			t.Errorf("%s: type %q sum %q", tc.ct, l.ContentType(), l.SHA256())
		}
	}
}

// TestNewIconChecksTheBytes: an icon is an SVG or a square PNG of 512 to 2048 pixels that decodes. Its size is read before its
// pixels, so a PNG that claims 3000 pixels is refused for its size even when it is cut short.
func TestNewIconChecksTheBytes(t *testing.T) {
	sq := func(n int) []byte { return solidPNG(t, n, n, color.NRGBA{R: 9, A: 255}) }
	good := sq(512)
	big := encodePNG(t, image.NewNRGBA(image.Rect(0, 0, 3000, 3000)))
	for _, tc := range []struct {
		label    string
		data     []byte
		ct, want string
	}{
		{"non-square", solidPNG(t, 512, 256, color.NRGBA{A: 255}), "image/png", "square PNG of 512 to 2048"},
		{"511 pixels", sq(511), "image/png", "square PNG of 512 to 2048"},
		{"2049 pixels", sq(2049), "image/png", "square PNG of 512 to 2048"},
		{"3000 pixels cut short", big[:200], "image/png", "square PNG of 512 to 2048"},
		{"512 pixels cut short", good[:len(good)/2], "image/png", "cannot be decoded"},
		{"SVG sent as PNG", []byte(iconSVG), "image/png", "not a PNG"},
		{"PNG sent as SVG", append(bytes.Clone(good), []byte("<svg>")...), "image/svg+xml", "is a PNG"},
		{"over 256 KiB", append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, MaxLogoBytes)...), "image/png", "larger than"},
	} {
		if _, err := NewIcon(tc.data, tc.ct); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", tc.label, err, tc.want)
		}
	}
	for _, tc := range []struct {
		data []byte
		ct   string
	}{{sq(512), "image/png"}, {sq(2048), "image/png"}, {[]byte(iconSVG), "image/svg+xml"}} {
		if _, err := NewIcon(tc.data, tc.ct); err != nil {
			t.Errorf("%s of %d bytes refused: %v", tc.ct, len(tc.data), err)
		}
	}
}

// TestLogoHasAStrongETag: /brand/logo answers with a strong validator from the content's hash, a repeat request with it gets
// 304 and no body, and Cache-Control stays no-cache, so a changed logo shows on the next load. The artwork has one too.
func TestLogoHasAStrongETag(t *testing.T) {
	l, err := NewLogo([]byte(logoSVG), "image/svg+xml")
	if err != nil {
		t.Fatal(err)
	}
	for _, logo := range []*Logo{l, ArtworkLogo()} {
		rec := httptest.NewRecorder()
		logo.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/brand/logo", nil))
		etag := rec.Header().Get("ETag")
		if etag != `"`+logo.SHA256()+`"` || len(logo.SHA256()) != 64 {
			t.Errorf("ETag = %q, want the hash %q", etag, logo.SHA256())
		}
		if rec.Header().Get("Cache-Control") != "no-cache" || rec.Header().Get("Content-Security-Policy") != svgOnlyPolicy ||
			rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("headers = %v", rec.Header())
		}
		req := httptest.NewRequest(http.MethodGet, "/brand/logo", nil)
		req.Header.Set("If-None-Match", etag)
		rec = httptest.NewRecorder()
		logo.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
			t.Errorf("If-None-Match %s: %d, %d bytes", etag, rec.Code, rec.Body.Len())
		}
		req = httptest.NewRequest(http.MethodGet, "/brand/logo", nil)
		req.Header.Set("If-None-Match", `"`+strings.Repeat("0", 64)+`"`)
		rec = httptest.NewRecorder()
		logo.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("another validator: %d", rec.Code)
		}
	}
	if l.SHA256() == ArtworkLogo().SHA256() {
		t.Error("two logos share a validator")
	}
}
