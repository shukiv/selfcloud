// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image/color"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

// ogFiles is a build whose index carries the link-preview placeholder right after the title, as web/index.html does.
func ogFiles() fstest.MapFS {
	files := testFiles()
	files["index.html"] = &fstest.MapFile{Data: []byte(`<!doctype html><meta property="csp-nonce" nonce="__SELFCLOUD_CSP_NONCE__">` +
		`<title>` + brandPlaceholder + `</title><!--__SELFCLOUD_OG__-->` +
		`<script type="module" nonce="__SELFCLOUD_CSP_NONCE__" src="/assets/app-abc123.js"></script>`)}
	return files
}

func ogHandler(t *testing.T, name, accent, publicURL string) *Handler {
	t.Helper()
	return newTestHandler(t, ogFiles(), Options{BrandName: name, BrandAccent: accent, PublicURL: publicURL})
}

// ogImageHash reads the hash the og:image address carries, and fails when the tag is not there exactly once with an
// absolute address and twelve hex digits.
func ogImageHash(t *testing.T, body, publicURL string) string {
	t.Helper()
	re := regexp.MustCompile(`<meta property="og:image" content="` + regexp.QuoteMeta(publicURL) + `/brand/og-image\.png\?v=([0-9a-f]{12})">`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no og:image tag with an absolute address and a 12 hex digit hash in %q", body)
	}
	if c := strings.Count(body, `<meta property="og:image" content="`); c != 1 {
		t.Fatalf("the og:image tag appears %d times, want once", c)
	}
	return m[1]
}

// TestOGTagsWithPublicURL: with public_url set, the page carries each link-preview tag exactly once, the name escaped,
// and the image address absolute with the hash of the served image's bytes.
func TestOGTagsWithPublicURL(t *testing.T) {
	const name = `Acme "<&> Drive`
	h := ogHandler(t, name, "#053D8E", "https://drive.example.com")
	body := get(t, h, http.MethodGet, "/").Body.String()
	n := brandHTML(name)
	desc := n + ": files, folders and sharing in your own drive."
	for _, tag := range []string{
		`<meta name="description" content="` + desc + `">`,
		`<meta property="og:type" content="website">`,
		`<meta property="og:site_name" content="` + n + `">`,
		`<meta property="og:title" content="` + n + `">`,
		`<meta property="og:description" content="` + desc + `">`,
		`<meta property="og:url" content="https://drive.example.com/">`,
		`<meta property="og:image:type" content="image/png">`,
		`<meta property="og:image:width" content="1200">`,
		`<meta property="og:image:height" content="630">`,
		`<meta property="og:image:alt" content="` + n + `">`,
		`<meta name="twitter:card" content="summary_large_image">`,
	} {
		if c := strings.Count(body, tag); c != 1 {
			t.Errorf("tag %q appears %d times, want once", tag, c)
		}
	}
	if strings.Contains(body, name) {
		t.Errorf("the name is not escaped: %q", body)
	}
	hash := ogImageHash(t, body, "https://drive.example.com")
	rec := get(t, h, http.MethodGet, "/brand/og-image.png")
	if rec.Code != http.StatusOK {
		t.Fatalf("the image: status %d", rec.Code)
	}
	sum := sha256.Sum256(rec.Body.Bytes())
	if want := hex.EncodeToString(sum[:6]); hash != want {
		t.Errorf("og:image hash %q, want the first 12 hex digits of the image's SHA-256 %q", hash, want)
	}
}

// TestOGTagsWithoutPublicURL: with no public_url the page carries no address and no image tags, and the card is summary.
// The name and the description stay.
func TestOGTagsWithoutPublicURL(t *testing.T) {
	h := ogHandler(t, "Acme", "#053D8E", "")
	body := get(t, h, http.MethodGet, "/").Body.String()
	for _, absent := range []string{"og:url", "og:image"} {
		if strings.Contains(body, absent) {
			t.Errorf("without public_url the page carries %q: %q", absent, body)
		}
	}
	for _, tag := range []string{
		`<meta name="description" content="Acme: files, folders and sharing in your own drive.">`,
		`<meta property="og:type" content="website">`,
		`<meta property="og:site_name" content="Acme">`,
		`<meta property="og:title" content="Acme">`,
		`<meta property="og:description" content="Acme: files, folders and sharing in your own drive.">`,
		`<meta name="twitter:card" content="summary">`,
	} {
		if c := strings.Count(body, tag); c != 1 {
			t.Errorf("tag %q appears %d times, want once", tag, c)
		}
	}
	if strings.Contains(body, "summary_large_image") {
		t.Errorf("without public_url the card is not summary: %q", body)
	}
}

// TestOGTagsAreTheSameOnEveryPage: a share link, a folder address and the root all carry the same tags, and no token or
// folder name reaches them: a preview is fetched by a third-party server.
func TestOGTagsAreTheSameOnEveryPage(t *testing.T) {
	h := ogHandler(t, "Acme", "#053D8E", "https://drive.example.com")
	block := func(body string) string {
		i := strings.Index(body, `<meta name="description"`)
		j := strings.Index(body, `<meta name="twitter:card"`)
		if i < 0 || j < 0 {
			t.Fatalf("the page has no link-preview tags: %q", body)
		}
		k := strings.Index(body[j:], ">")
		return body[i : j+k+1]
	}
	first := ""
	for _, target := range []string{"/", "/s/sometoken", "/files/fld_x"} {
		body := get(t, h, http.MethodGet, target).Body.String()
		if strings.Contains(body, "sometoken") || strings.Contains(body, "fld_x") {
			t.Errorf("%s: the page leaks the address into the tags: %q", target, body)
		}
		got := block(body)
		if first == "" {
			first = got
		} else if got != first {
			t.Errorf("%s: tags %q, want the root's %q", target, got, first)
		}
	}
}

// TestOGImageIsServedWithoutAuth: /brand/og-image.png answers 200 with a 1200x630 PNG whose corner is the accent, with
// the icons' cache headers and validator, and no sign-in.
func TestOGImageIsServedWithoutAuth(t *testing.T) {
	h := ogHandler(t, "Acme", "#053D8E", "")
	req := httptest.NewRequest(http.MethodGet, "/brand/og-image.png", nil)
	if !h.Serves(req) {
		t.Fatal("the web handler does not serve /brand/og-image.png")
	}
	rec := get(t, h, http.MethodGet, "/brand/og-image.png")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}
	img := decodePNG(t, rec.Body.Bytes())
	if b := img.Bounds(); b.Dx() != 1200 || b.Dy() != 630 {
		t.Errorf("image %dx%d, want 1200x630", b.Dx(), b.Dy())
	}
	accent := color.RGBA{R: 5, G: 61, B: 142, A: 255}
	if got := color.RGBAModel.Convert(img.At(0, 0)).(color.RGBA); got != accent {
		t.Errorf("top-left pixel %v, want the accent", got)
	}
	if !hasWhite(img) {
		t.Error("the image has no white of the mark: the icon art is not on it")
	}
	req = httptest.NewRequest(http.MethodGet, "/brand/og-image.png", nil)
	req.Header.Set("If-None-Match", etag)
	rep := httptest.NewRecorder()
	h.ServeHTTP(rep, req)
	if rep.Code != http.StatusNotModified {
		t.Errorf("repeat with ETag: status %d, want 304", rep.Code)
	}
}

// TestOGImageChangesWithTheAccent: a brand change rebuilds the image with the icons: new bytes, a new validator and a new
// hash in the og:image address, and the new accent in the corner.
func TestOGImageChangesWithTheAccent(t *testing.T) {
	h := ogHandler(t, "Acme", "#053D8E", "https://drive.example.com")
	first := get(t, h, http.MethodGet, "/brand/og-image.png")
	firstHash := ogImageHash(t, get(t, h, http.MethodGet, "/").Body.String(), "https://drive.example.com")
	b, err := h.BuildBrand(BrandOptions{Name: "Acme", Accent: "#A00000"})
	if err != nil {
		t.Fatal(err)
	}
	h.UseBrand(b)
	second := get(t, h, http.MethodGet, "/brand/og-image.png")
	if bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) {
		t.Error("a changed accent kept the same image")
	}
	if first.Header().Get("ETag") == second.Header().Get("ETag") {
		t.Error("a changed accent kept the same ETag")
	}
	secondHash := ogImageHash(t, get(t, h, http.MethodGet, "/").Body.String(), "https://drive.example.com")
	if firstHash == secondHash {
		t.Errorf("a changed accent kept the hash %q in the og:image address", firstHash)
	}
	img := decodePNG(t, second.Body.Bytes())
	accent := color.RGBA{R: 160, G: 0, B: 0, A: 255}
	if got := color.RGBAModel.Convert(img.At(0, 0)).(color.RGBA); got != accent {
		t.Errorf("top-left pixel %v, want the new accent", got)
	}
}

// TestOGPlaceholderNeverServed: the placeholder comment is replaced on every page, with and without a public address.
func TestOGPlaceholderNeverServed(t *testing.T) {
	for _, publicURL := range []string{"", "https://drive.example.com"} {
		h := ogHandler(t, "Acme", "#053D8E", publicURL)
		for _, target := range []string{"/", "/s/sometoken", "/files/fld_x"} {
			body := get(t, h, http.MethodGet, target).Body.String()
			if strings.Contains(body, "__SELFCLOUD_OG__") {
				t.Errorf("public_url %q, %s: the placeholder was served: %q", publicURL, target, body)
			}
		}
	}
}
