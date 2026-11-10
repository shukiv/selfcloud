// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// encodedFiles is a build whose app-abc123.js has a brotli and a gzip variant beside it (the bytes are stand-ins: the server
// never decodes them). app-abc123.css has none. The traps beside the pages are never served as encoded answers.
func encodedFiles() fstest.MapFS {
	return fstest.MapFS{
		"index.html":              {Data: []byte(`<!doctype html><meta property="csp-nonce" nonce="__SELFCLOUD_CSP_NONCE__"><title>app</title>`)},
		"index.html.br":           {Data: []byte("TRAP-BR")},
		"offline.html":            {Data: []byte("<title>offline</title>")},
		"offline.html.br":         {Data: []byte("TRAP-BR")},
		"offline.html.gz":         {Data: []byte("TRAP-GZ")},
		"assets/app-abc123.js":    {Data: []byte("console.log(1)")},
		"assets/app-abc123.js.br": {Data: []byte("BR-BYTES")},
		"assets/app-abc123.js.gz": {Data: []byte("GZ-BYTES")},
		"assets/app-abc123.css":   {Data: []byte("body{}")},
	}
}

// fetchEnc sends one request with the given headers and returns the recorded answer.
func fetchEnc(t *testing.T, h *Handler, method, target string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestPickEncoding is the table for the Accept-Encoding rules: tokens are case-insensitive, q=0 refuses, * covers the codings
// the header does not name, br comes before gzip, identity never forces an encoding, and a malformed token is ignored.
func TestPickEncoding(t *testing.T) {
	cases := []struct {
		name          string
		accept        string
		hasBr, hasGzp bool
		want          string
	}{
		{"absent header", "", true, true, ""},
		{"both accepted, gzip first in the header", "gzip, br", true, true, "br"},
		{"both accepted, br first", "br, gzip", true, true, "br"},
		{"gzip only", "gzip", true, true, "gzip"},
		{"br only", "br", true, true, "br"},
		{"br accepted, no brotli variant", "br", false, true, ""},
		{"gzip accepted, no gzip variant, no brotli", "gzip", true, false, ""},
		{"gzip accepted, no brotli variant", "gzip", false, true, "gzip"},
		{"br refused by q=0", "br;q=0, gzip", true, true, "gzip"},
		{"br refused by q=0.0 and gzip weighted", "br;q=0.0, gzip;q=0.5", true, true, "gzip"},
		{"gzip refused, br weighted", "gzip;q=0, br;q=0.1", true, true, "br"},
		{"star covers both", "*", true, true, "br"},
		{"star covers gzip only without brotli", "*", false, true, "gzip"},
		{"star refused, gzip named", "*;q=0, gzip", true, true, "gzip"},
		{"gzip refused, star covers br", "gzip;q=0, *", true, true, "br"},
		{"gzip refused and star refused", "gzip;q=0, *;q=0", true, true, ""},
		{"upper case tokens", "BR, GZIP", true, true, "br"},
		{"mixed case token", "Gzip", true, true, "gzip"},
		{"identity refused alone forces nothing", "identity;q=0", true, true, ""},
		{"identity refused beside br", "identity;q=0, br", true, true, "br"},
		{"a prefix of br is not br", "brx, gzip", true, true, "gzip"},
		{"a prefix of gzip is not gzip", "x-gzip", true, true, ""},
		{"malformed q ignores that token", "br;q=abc, gzip", true, true, "gzip"},
		{"empty q ignores that token", "gzip;q=, br;q=1.0", true, true, "br"},
		{"deflate is not offered", "deflate", true, true, ""},
		{"spaces around tokens", " gzip ,  br ", true, true, "br"},
		{"zero q with decimals", "gzip;q=0.000, identity", true, true, ""},
		{"empty list members", ",,, gzip,,", true, true, "gzip"},
	}
	for _, c := range cases {
		if got := pickEncoding(c.accept, c.hasBr, c.hasGzp); got != c.want {
			t.Errorf("%s: pickEncoding(%q, %v, %v) = %q, want %q", c.name, c.accept, c.hasBr, c.hasGzp, got, c.want)
		}
	}
}

// TestBrotliVariantIsServedBeforeGzip: an asset with both variants and a client that takes both gets the brotli bytes, with the
// encoding named, the original type, the length of the variant, the immutable cache, and Vary.
func TestBrotliVariantIsServedBeforeGzip(t *testing.T) {
	h := newTestHandler(t, encodedFiles(), Options{})
	rec := fetchEnc(t, h, http.MethodGet, "/assets/app-abc123.js", map[string]string{"Accept-Encoding": "gzip, br"})
	if rec.Code != http.StatusOK || rec.Body.String() != "BR-BYTES" {
		t.Fatalf("status %d body %q, want 200 BR-BYTES", rec.Code, rec.Body.String())
	}
	checkHeader(t, rec, "Content-Encoding", "br")
	checkHeader(t, rec, "Content-Type", "text/javascript; charset=utf-8")
	checkHeader(t, rec, "Content-Length", "8")
	checkHeader(t, rec, "Cache-Control", "public, max-age=31536000, immutable")
	checkVary(t, rec)
}

// TestGzipVariantWhenBrotliIsNotAccepted: a client that takes gzip alone gets the gzip bytes.
func TestGzipVariantWhenBrotliIsNotAccepted(t *testing.T) {
	h := newTestHandler(t, encodedFiles(), Options{})
	rec := fetchEnc(t, h, http.MethodGet, "/assets/app-abc123.js", map[string]string{"Accept-Encoding": "gzip"})
	if rec.Body.String() != "GZ-BYTES" {
		t.Fatalf("body %q, want GZ-BYTES", rec.Body.String())
	}
	checkHeader(t, rec, "Content-Encoding", "gzip")
	checkHeader(t, rec, "Content-Type", "text/javascript; charset=utf-8")
	checkVary(t, rec)
}

// TestIdentityWhenNothingIsAccepted: no header, or only refusals, gets the file as it is, with no Content-Encoding and Vary still set.
func TestIdentityWhenNothingIsAccepted(t *testing.T) {
	h := newTestHandler(t, encodedFiles(), Options{})
	for _, accept := range []string{"", "br;q=0, gzip;q=0", "identity;q=0", "deflate"} {
		header := map[string]string{}
		if accept != "" {
			header["Accept-Encoding"] = accept
		}
		rec := fetchEnc(t, h, http.MethodGet, "/assets/app-abc123.js", header)
		if rec.Body.String() != "console.log(1)" {
			t.Errorf("Accept-Encoding %q: body %q, want the file as it is", accept, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Encoding"); got != "" {
			t.Errorf("Accept-Encoding %q: Content-Encoding = %q, want none", accept, got)
		}
		checkHeader(t, rec, "Content-Length", "14")
		checkVary(t, rec)
	}
}

// TestAcceptEncodingLinesAreAllRead: two Accept-Encoding header lines count as one list, so br on the second line is taken.
func TestAcceptEncodingLinesAreAllRead(t *testing.T) {
	h := newTestHandler(t, encodedFiles(), Options{})
	req := httptest.NewRequest(http.MethodGet, "/assets/app-abc123.js", nil)
	req.Header.Add("Accept-Encoding", "gzip")
	req.Header.Add("Accept-Encoding", "br")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Body.String() != "BR-BYTES" {
		t.Errorf("body %q, want BR-BYTES", rec.Body.String())
	}
}

// TestMalformedAcceptEncodingIsIdentity: a header with no usable token gets the file as it is, and no panic.
func TestMalformedAcceptEncodingIsIdentity(t *testing.T) {
	h := newTestHandler(t, encodedFiles(), Options{})
	for _, accept := range []string{",", ";;q=", "br;q=", "gzip;q=-1, ;", "\x00br"} {
		rec := fetchEnc(t, h, http.MethodGet, "/assets/app-abc123.js", map[string]string{"Accept-Encoding": accept})
		if rec.Code != http.StatusOK || rec.Body.String() != "console.log(1)" {
			t.Errorf("Accept-Encoding %q: %d %q, want the file as it is", accept, rec.Code, rec.Body.String())
		}
	}
}

// TestFileWithoutVariantIsNotEncodedOrVaried: an asset that has no variant is answered as before, whatever the client takes.
func TestFileWithoutVariantIsNotEncodedOrVaried(t *testing.T) {
	h := newTestHandler(t, encodedFiles(), Options{})
	rec := fetchEnc(t, h, http.MethodGet, "/assets/app-abc123.css", map[string]string{"Accept-Encoding": "gzip, br"})
	if rec.Body.String() != "body{}" {
		t.Fatalf("body %q", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want none", got)
	}
	if got := rec.Header().Get("Vary"); got != "" {
		t.Errorf("Vary = %q, want none for a file with no variant", got)
	}
}

// TestRangeGetsTheIdentityFile: a request with a Range is answered from the file as it is, with the range, and Vary.
func TestRangeGetsTheIdentityFile(t *testing.T) {
	h := newTestHandler(t, encodedFiles(), Options{})
	rec := fetchEnc(t, h, http.MethodGet, "/assets/app-abc123.js", map[string]string{"Accept-Encoding": "gzip, br", "Range": "bytes=0-4"})
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "conso" {
		t.Fatalf("status %d body %q, want 206 conso", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q on a range, want none", got)
	}
	checkHeader(t, rec, "Content-Range", "bytes 0-4/14")
	checkVary(t, rec)
}

// TestHeadAnswersLikeGetWithoutBody: HEAD gets the encoding, the length of the encoded bytes and Vary, and no body.
func TestHeadAnswersLikeGetWithoutBody(t *testing.T) {
	h := newTestHandler(t, encodedFiles(), Options{})
	rec := fetchEnc(t, h, http.MethodHead, "/assets/app-abc123.js", map[string]string{"Accept-Encoding": "gzip, br"})
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("HEAD: status %d body %d bytes, want 200 and no body", rec.Code, rec.Body.Len())
	}
	checkHeader(t, rec, "Content-Encoding", "br")
	checkHeader(t, rec, "Content-Length", "8")
	checkVary(t, rec)
}

// TestPagesAreNeverEncoded: index.html and offline.html are answered as files, whatever the client takes, even when a
// variant of them sits in the build. Their traps would be served if the handler looked beside them.
func TestPagesAreNeverEncoded(t *testing.T) {
	h := newTestHandler(t, encodedFiles(), Options{})
	for _, target := range []string{"/", "/offline.html"} {
		rec := fetchEnc(t, h, http.MethodGet, target, map[string]string{"Accept-Encoding": "gzip, br"})
		if got := rec.Header().Get("Content-Encoding"); got != "" {
			t.Errorf("%s: Content-Encoding = %q, want none", target, got)
		}
		if strings.Contains(rec.Body.String(), "TRAP") {
			t.Errorf("%s served a variant file: %q", target, rec.Body.String())
		}
	}
}

// TestDirectVariantRequestIsNotFound: one URL per asset. The .br and .gz names answer not found, as a missing asset does.
func TestDirectVariantRequestIsNotFound(t *testing.T) {
	h := newTestHandler(t, encodedFiles(), Options{})
	for _, target := range []string{"/assets/app-abc123.js.br", "/assets/app-abc123.js.gz"} {
		for _, header := range []map[string]string{nil, {"Accept-Encoding": "gzip, br"}} {
			rec := fetchEnc(t, h, http.MethodGet, target, header)
			if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "BYTES") {
				t.Errorf("%s: %d %q, want 404 with no body", target, rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("%s: Cache-Control = %q, want no-store", target, got)
			}
			if got := rec.Header().Get("Content-Encoding"); got != "" {
				t.Errorf("%s: Content-Encoding = %q on a 404", target, got)
			}
		}
	}
}

// checkHeader fails the test when the header is not the value.
func checkHeader(t *testing.T, rec *httptest.ResponseRecorder, name, want string) {
	t.Helper()
	if got := rec.Header().Get(name); got != want {
		t.Errorf("%s = %q, want %q", name, got, want)
	}
}

// checkVary fails the test when the answer does not vary on Accept-Encoding.
func checkVary(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if !strings.Contains(strings.Join(rec.Header().Values("Vary"), ","), "Accept-Encoding") {
		t.Errorf("Vary = %q, want Accept-Encoding", rec.Header().Values("Vary"))
	}
}
