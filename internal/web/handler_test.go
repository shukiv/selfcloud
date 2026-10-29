// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// testFiles is a small build: an index, one hashed asset, a dot-file and a file outside assets.
func testFiles() fstest.MapFS {
	return fstest.MapFS{
		"index.html":            {Data: []byte("<!doctype html><title>app</title>")},
		"assets/app-abc123.js":  {Data: []byte("console.log(1)")},
		"assets/app-abc123.css": {Data: []byte("body{}")},
		"fonts-OFL.txt":         {Data: []byte("licence")},
		".env":                  {Data: []byte("SECRET=1")},
		"assets/.hidden.js":     {Data: []byte("hidden")},
	}
}

func newTestHandler(t *testing.T, files fstest.MapFS, opts Options) *Handler {
	t.Helper()
	h, err := New(files, opts)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func get(t *testing.T, h *Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestRootAndClientRoutesGetTheIndex: "/" and an unknown client route both get index.html, with the
// UI headers and no caching.
func TestRootAndClientRoutesGetTheIndex(t *testing.T) {
	h := newTestHandler(t, testFiles(), Options{})
	for _, target := range []string{"/", "/files/x", "/login", "/index.html"} {
		rec := get(t, h, http.MethodGet, target)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<title>app</title>") {
			t.Fatalf("%s: %d %q", target, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control = %q, want no-store", target, got)
		}
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
			t.Errorf("%s: Content-Type = %q", target, got)
		}
	}
}

// TestUIHeaders checks the full policy on UI responses.
func TestUIHeaders(t *testing.T) {
	h := newTestHandler(t, testFiles(), Options{})
	rec := get(t, h, http.MethodGet, "/")
	want := map[string]string{
		"Content-Security-Policy": "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; " +
			"media-src 'self'; font-src 'self'; connect-src 'self'; frame-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "same-origin",
		"X-Frame-Options":        "DENY",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

// TestContentURLOriginIsAllowed adds the content origin to images, media and frames, and nothing else.
func TestContentURLOriginIsAllowed(t *testing.T) {
	h := newTestHandler(t, testFiles(), Options{ContentURL: "https://files.example.com/base/"})
	csp := get(t, h, http.MethodGet, "/").Header().Get("Content-Security-Policy")
	for _, want := range []string{"img-src 'self' data: blob: https://files.example.com;", "media-src 'self' https://files.example.com;", "frame-src 'self' https://files.example.com;"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	if strings.Contains(csp, "connect-src 'self' https") || strings.Contains(csp, "script-src 'self' https") {
		t.Errorf("content origin leaked into another directive: %q", csp)
	}
	if _, err := New(testFiles(), Options{ContentURL: "not a url"}); err == nil {
		t.Error("a content URL without an origin was accepted")
	}
}

// TestAssetsAreImmutable: a hashed file under assets/ is cached for a year.
func TestAssetsAreImmutable(t *testing.T) {
	h := newTestHandler(t, testFiles(), Options{})
	rec := get(t, h, http.MethodGet, "/assets/app-abc123.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/javascript; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if rec.Body.String() != "console.log(1)" {
		t.Errorf("body = %q", rec.Body.String())
	}
	if rec := get(t, h, http.MethodGet, "/assets/app-abc123.css"); rec.Header().Get("Content-Type") != "text/css; charset=utf-8" {
		t.Errorf("css Content-Type = %q", rec.Header().Get("Content-Type"))
	}
}

// TestMissingAssetFallsBackWithoutCache: a missing asset gets the index, never cached.
func TestMissingAssetIsNotFound(t *testing.T) {
	h := newTestHandler(t, testFiles(), Options{})
	rec := get(t, h, http.MethodGet, "/assets/gone-999.js")
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "<title>app</title>") ||
		rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("missing asset: %d %q headers=%v", rec.Code, rec.Body.String(), rec.Header())
	}
}

// TestHeadHasNoBody: HEAD gets the headers and no body.
func TestHeadHasNoBody(t *testing.T) {
	h := newTestHandler(t, testFiles(), Options{})
	rec := get(t, h, http.MethodHead, "/")
	if rec.Body.Len() != 0 || rec.Header().Get("Content-Security-Policy") == "" {
		t.Errorf("HEAD body=%d csp=%q", rec.Body.Len(), rec.Header().Get("Content-Security-Policy"))
	}
}

// TestDotFilesAreNeverServed: a dot-file is not served even when asked for directly, and neither
// is a path that climbs out of the build.
func TestDotFilesAreNeverServed(t *testing.T) {
	h := newTestHandler(t, testFiles(), Options{})
	for _, target := range []string{"/.env", "/assets/.hidden.js", "/../.env", "/..%2F.env", "/assets/../.env"} {
		rec := get(t, h, http.MethodGet, target)
		if strings.Contains(rec.Body.String(), "SECRET") || strings.Contains(rec.Body.String(), "hidden") {
			t.Errorf("%s served a hidden file: %q", target, rec.Body.String())
		}
	}
}

// TestServesRefusesReservedAndDotPaths: Serves says no for the API, the other server paths, dot
// names and non-GET methods, so those requests get the JSON 404 or the API's own answer.
func TestServesRefusesReservedAndDotPaths(t *testing.T) {
	h := newTestHandler(t, testFiles(), Options{})
	cases := []struct {
		method, target string
		want           bool
	}{
		{http.MethodGet, "/", true},
		{http.MethodHead, "/files/x", true},
		{http.MethodGet, "/brand/logo", false},
		{http.MethodGet, "/brand", false},
		{http.MethodGet, "/api/v1/nope", false},
		{http.MethodGet, "/api", false},
		{http.MethodGet, "/wopi/files/1", false},
		{http.MethodGet, "/metrics", false},
		{http.MethodGet, "/healthz", false},
		{http.MethodGet, "/readyz", false},
		{http.MethodGet, "/.env", false},
		{http.MethodGet, "/assets/.hidden.js", false},
		{http.MethodPost, "/", false},
		{http.MethodPut, "/files/x", false},
		{http.MethodDelete, "/files/x", false},
		{http.MethodGet, "/apix", true},
		{http.MethodGet, "/healthzz", true},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.target, nil)
		if got := h.Serves(req); got != c.want {
			t.Errorf("Serves(%s %s) = %v, want %v", c.method, c.target, got, c.want)
		}
	}
}

// unbuiltFiles is what the committed tree holds in dist/: only the .gitignore that keeps the build
// output out of git. The embed compiles with it alone.
func unbuiltFiles() fstest.MapFS {
	return fstest.MapFS{".gitignore": {Data: []byte("*\n!.gitignore\n")}}
}

// TestUnbuiltBuildServesTheNotBuiltPage: with no index.html, every path the interface owns gets the
// not-built page, with the UI headers and no caching, and the API is untouched (the server tests).
func TestUnbuiltBuildServesTheNotBuiltPage(t *testing.T) {
	h := newTestHandler(t, unbuiltFiles(), Options{ContentURL: "https://files.example.com"})
	for _, target := range []string{"/", "/files/x", "/index.html"} {
		rec := get(t, h, http.MethodGet, target)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "The web interface is not built. Run make build.") {
			t.Errorf("%s: %d %q", target, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control = %q, want no-store", target, rec.Header().Get("Cache-Control"))
		}
		if rec.Header().Get("X-Frame-Options") != "DENY" || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'self'") {
			t.Errorf("%s: UI headers missing: %v", target, rec.Header())
		}
	}
}

// TestBuiltIndexWinsOverNotBuilt: once a build has its index, the not-built page is not served.
func TestBuiltIndexWinsOverNotBuilt(t *testing.T) {
	files := unbuiltFiles()
	files["index.html"] = &fstest.MapFile{Data: []byte("<!doctype html><title>app</title>")}
	h := newTestHandler(t, files, Options{})
	if rec := get(t, h, http.MethodGet, "/"); strings.Contains(rec.Body.String(), "not built") {
		t.Errorf("built index replaced by the not-built page: %q", rec.Body.String())
	}
}

// TestSharePageIsNotReferredOrIndexed: the page that opens a share link gets no Referer and no index, and the
// rest of the interface gets neither. robots.txt keeps crawlers off that page, and only that page.
func TestSharePageIsNotReferredOrIndexed(t *testing.T) {
	h := newTestHandler(t, testFiles(), Options{})
	for _, target := range []string{"/s", "/s/"} {
		rec := get(t, h, http.MethodGet, target)
		if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
			t.Errorf("%s: Referrer-Policy = %q, want no-referrer", target, got)
		}
		if got := rec.Header().Get("X-Robots-Tag"); got != "noindex, nofollow" {
			t.Errorf("%s: X-Robots-Tag = %q", target, got)
		}
	}
	rec := get(t, h, http.MethodGet, "/sign-in")
	if rec.Header().Get("X-Robots-Tag") != "" || rec.Header().Get("Referrer-Policy") != "same-origin" {
		t.Errorf("/sign-in: headers %v, want the ordinary UI policy", rec.Header())
	}
	rec = get(t, h, http.MethodGet, "/robots.txt")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Disallow: /s$") ||
		!strings.Contains(rec.Body.String(), "Disallow: /s/") || strings.Contains(rec.Body.String(), "<title>") {
		t.Errorf("/robots.txt = %d %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Errorf("robots.txt Content-Type = %q", got)
	}
}
