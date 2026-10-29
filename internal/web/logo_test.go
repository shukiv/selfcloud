// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestLoadLogoAcceptsPNGAndSVG: each kind loads and is served with its own type and the sandbox.
func TestLoadLogoAcceptsPNGAndSVG(t *testing.T) {
	png := writeFile(t, "logo.png", append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 8)...))
	l, err := LoadLogo(png)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	l.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/brand/logo", nil))
	if rec.Header().Get("Content-Type") != "image/png" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("png headers: %v", rec.Header())
	}
	if csp := rec.Header().Get("Content-Security-Policy"); csp != "default-src 'none'; sandbox" {
		t.Errorf("png CSP = %q", csp)
	}

	svg := writeFile(t, "logo.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"/></svg>`))
	l, err = LoadLogo(svg)
	if err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	l.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/brand/logo", nil))
	if rec.Header().Get("Content-Type") != "image/svg+xml" {
		t.Errorf("svg Content-Type = %q", rec.Header().Get("Content-Type"))
	}
	if csp := rec.Header().Get("Content-Security-Policy"); csp != "default-src 'none'; style-src 'unsafe-inline'; sandbox" {
		t.Errorf("svg CSP = %q", csp)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("<rect")) {
		t.Error("svg body not served")
	}
}

// TestLoadLogoRefusesBadFiles: an oversized file, a wrong extension, and content that does not match
// its extension are all refused at start.
func TestLoadLogoRefusesBadFiles(t *testing.T) {
	big := writeFile(t, "big.png", append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, MaxLogoBytes)...))
	if _, err := LoadLogo(big); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Errorf("oversized logo: %v", err)
	}
	exact := writeFile(t, "exact.png", append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, MaxLogoBytes-8)...))
	if _, err := LoadLogo(exact); err != nil {
		t.Errorf("a logo of exactly the cap was refused: %v", err)
	}
	if _, err := LoadLogo(writeFile(t, "logo.jpg", []byte("x"))); err == nil {
		t.Error("a .jpg logo was accepted")
	}
	if _, err := LoadLogo(writeFile(t, "fake.png", []byte("<svg>not png</svg>"))); err == nil {
		t.Error("a PNG name with SVG content was accepted")
	}
	if _, err := LoadLogo(writeFile(t, "fake.svg", []byte("plain text"))); err == nil {
		t.Error("an SVG name with no <svg> was accepted")
	}
	if _, err := LoadLogo(filepath.Join(t.TempDir(), "missing.png")); err == nil {
		t.Error("a missing logo was accepted")
	}
}
