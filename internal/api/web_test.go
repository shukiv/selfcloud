// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
)

// serveRaw sends a request through the whole handler stack with no credentials.
func serveRaw(t *testing.T, s *Server, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

const errorCSP = "default-src 'none'; sandbox"

// TestWebInterfaceServesTheShell: "/" and client routes get the app, with its headers.
func TestWebInterfaceServesTheShell(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	for _, target := range []string{"/", "/files/x", "/login", "/shared"} {
		rec := serveRaw(t, s, http.MethodGet, target)
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("%s: %d %q", target, rec.Code, rec.Header().Get("Content-Type"))
		}
		if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") ||
			strings.Contains(csp, "unsafe") {
			t.Errorf("%s: CSP %q", target, csp)
		}
		if rec.Header().Get("X-Frame-Options") != "DENY" || rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: headers %v", target, rec.Header())
		}
	}
}

// TestUnknownAPIPathIsJSONNotIndex: an unmatched API path keeps the JSON 404 and its own CSP, and
// a write to a UI path gets the same.
func TestUnknownAPIPathIsJSONNotIndex(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	for _, c := range []struct{ method, target string }{
		{http.MethodGet, "/api/v1/nope"},
		{http.MethodGet, "/api/v1/accounts/x/nope/y"},
		{http.MethodPost, "/"},
		{http.MethodPost, "/files/x"},
	} {
		rec := serveRaw(t, s, c.method, c.target)
		if c.method == http.MethodGet && strings.HasPrefix(c.target, "/api/") {
			wantError(t, rec, http.StatusNotFound, "not_found")
		} else if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"code":"not_found"`) {
			t.Errorf("%s %s: %d %q", c.method, c.target, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Security-Policy"); got != errorCSP {
			t.Errorf("%s %s: CSP = %q, want the error policy", c.method, c.target, got)
		}
	}
}

// TestUIDisabledServesNoUI: with ui.enabled false, "/" and the UI paths are the JSON 404, and the
// API still answers.
func TestUIDisabledServesNoUI(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, func(c *config.Config) { c.UI.Enabled = false })
	for _, target := range []string{"/", "/files/x", "/login", "/index.html", "/manifest.webmanifest", "/brand/icon-192.png", "/brand/apple-touch-icon.png"} {
		rec := serveRaw(t, s, http.MethodGet, target)
		wantError(t, rec, http.StatusNotFound, "not_found")
	}
	rec := serveRaw(t, s, http.MethodGet, "/api/v1/info")
	wantStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"web_interface":false`) {
		t.Errorf("info = %s", rec.Body.String())
	}
}

// TestPWAManifestAndIconsServedWithUI: with the web interface on, the manifest names the brand and the icons are PNG.
func TestPWAManifestAndIconsServedWithUI(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, func(c *config.Config) { c.UI.Brand.Name = "Acme" })
	rec := serveRaw(t, s, http.MethodGet, "/manifest.webmanifest")
	wantStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"name":"Acme"`) || !strings.Contains(rec.Body.String(), `"start_url":"/files"`) {
		t.Errorf("manifest = %s", rec.Body.String())
	}
	icon := serveRaw(t, s, http.MethodGet, "/brand/icon-512-maskable.png")
	wantStatus(t, icon, http.StatusOK)
	if icon.Header().Get("Content-Type") != "image/png" {
		t.Errorf("icon type %q", icon.Header().Get("Content-Type"))
	}
}

// TestAPIResponsesKeepTheirOwnHeaders: the UI's headers never reach an API response.
func TestAPIResponsesKeepTheirOwnHeaders(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	rec := serveRaw(t, s, http.MethodGet, "/api/v1/info")
	wantStatus(t, rec, http.StatusOK)
	for _, h := range []string{"Content-Security-Policy", "X-Frame-Options", "Referrer-Policy"} {
		if v := rec.Header().Get(h); v != "" {
			t.Errorf("info has %s = %q", h, v)
		}
	}
}

// TestContentResponsesKeepTheirPolicy: a file's content keeps its sandbox policy, with no UI
// headers added to it.
func TestContentResponsesKeepTheirPolicy(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	key, node := rangeFixture(t, s)
	rec := rawCall(t, s, "GET", "/api/v1/nodes/"+node.ID+"/content", key, "", nil)
	wantStatus(t, rec, http.StatusOK)
	if got := rec.Header().Get("Content-Security-Policy"); got != contentCSP {
		t.Errorf("content CSP = %q, want %q", got, contentCSP)
	}
	if rec.Header().Get("X-Frame-Options") != "" {
		t.Errorf("content has X-Frame-Options = %q", rec.Header().Get("X-Frame-Options"))
	}
}

// TestContentHostHasNoUI: the content origin answers only links, so the UI is not served there.
func TestContentHostHasNoUI(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, func(c *config.Config) { c.ContentURL = "https://files.example.com" })
	rec := fromHost(t, s, "files.example.com", "GET", "/", nil, nil)
	wantError(t, rec, http.StatusNotFound, "not_found")
	rec = fromHost(t, s, "files.example.com", "GET", "/files/x", nil, nil)
	wantError(t, rec, http.StatusNotFound, "not_found")
}

// TestContentURLIsAllowedInTheShellPolicy: the UI allows the content origin for images and frames.
func TestContentURLIsAllowedInTheShellPolicy(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, func(c *config.Config) { c.ContentURL = "https://files.example.com" })
	csp := serveRaw(t, s, http.MethodGet, "/").Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "img-src 'self' data: blob: https://files.example.com") ||
		!strings.Contains(csp, "frame-src 'self' https://files.example.com") {
		t.Errorf("CSP = %q", csp)
	}
}

// TestInfoBrand: /info carries the installation's name and accent, and the logo as a URL; the
// file's path never appears.
func TestInfoBrand(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	rec := serveRaw(t, s, http.MethodGet, "/api/v1/info")
	wantStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	for _, want := range []string{
		`"name":"SelfCloud"`,
		`"brand":{"name":"SelfCloud","accent":"#053D8E","logo_url":"/brand/logo","artwork":true}`,
		`"password_min_length":8`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("info = %s, want %s", body, want)
		}
	}

	logo := filepath.Join(t.TempDir(), "mark.png")
	if err := os.WriteFile(logo, append([]byte("\x89PNG\r\n\x1a\n"), 0, 0, 0, 0), 0o600); err != nil {
		t.Fatal(err)
	}
	s = newTestServer(t, func(c *config.Config) {
		c.UI.Brand = config.BrandConfig{Name: "Acme Drive", Accent: "#0F766E", LogoFile: logo}
	})
	body = serveRaw(t, s, http.MethodGet, "/api/v1/info").Body.String()
	if !strings.Contains(body, `"brand":{"name":"Acme Drive","accent":"#0F766E","logo_url":"/brand/logo","artwork":false}`) {
		t.Errorf("info = %s", body)
	}
	if strings.Contains(body, "palette") || strings.Contains(body, "sidebar") {
		t.Errorf("info names tokens nobody set: %s", body)
	}
	if strings.Contains(body, logo) || strings.Contains(body, t.TempDir()) {
		t.Errorf("info leaks the logo path: %s", body)
	}
}

// TestLogoRoute: the logo is served with its type, and is there whether or not the UI is on.
func TestLogoRoute(t *testing.T) {
	t.Parallel()
	logo := filepath.Join(t.TempDir(), "mark.svg")
	if err := os.WriteFile(logo, []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, ui := range []bool{true, false} {
		s := newTestServer(t, func(c *config.Config) {
			c.UI.Enabled = ui
			c.UI.Brand.LogoFile = logo
		})
		rec := serveRaw(t, s, http.MethodGet, "/brand/logo")
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/svg+xml" ||
			rec.Header().Get("X-Content-Type-Options") != "nosniff" ||
			rec.Header().Get("Content-Security-Policy") != "default-src 'none'; style-src 'unsafe-inline'; sandbox" {
			t.Errorf("ui=%v: %d %v", ui, rec.Code, rec.Header())
		}
	}
}

// TestLogoRouteWithoutLogo: no logo file and a name of the installation's own, so the route is the JSON 404.
func TestLogoRouteWithoutLogo(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, func(c *config.Config) { c.UI.Brand.Name = "Acme Drive" })
	wantError(t, serveRaw(t, s, http.MethodGet, "/brand/logo"), http.StatusNotFound, "not_found")
}

// TestLogoRouteArtwork: the default brand (name SelfCloud, no logo file) has the SelfCloud artwork at the logo route, with the
// UI on or off (decision 109).
func TestLogoRouteArtwork(t *testing.T) {
	t.Parallel()
	for _, ui := range []bool{true, false} {
		s := newTestServer(t, func(c *config.Config) { c.UI.Enabled = ui })
		rec := serveRaw(t, s, http.MethodGet, "/brand/logo")
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/svg+xml" ||
			!strings.Contains(rec.Body.String(), `aria-label="SelfCloud"`) {
			t.Errorf("ui=%v: %d %v", ui, rec.Code, rec.Header())
		}
	}
}

// TestConfigArtwork: the artwork shows only for the default name with no logo file (decision 109, Q4).
func TestConfigArtwork(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, logo string
		want       bool
	}{
		{"SelfCloud", "", true},
		{"SelfCloud", "/x/logo.png", false},
		{"Acme Drive", "", false},
	} {
		if got := (config.BrandConfig{Name: tc.name, LogoFile: tc.logo}).Artwork(); got != tc.want {
			t.Errorf("name %q logo %q: Artwork() = %v, want %v", tc.name, tc.logo, got, tc.want)
		}
	}
}

// TestLinkResponsesKeepTheirPolicy: a signed link's file keeps the content policy on the content
// origin, and the UI's headers are not added to it.
func TestLinkResponsesKeepTheirPolicy(t *testing.T) {
	t.Parallel()
	const contentHost = "files.example.test"
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.ContentURL = "https://" + contentHost })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	page := putTyped(t, s, key, f.ID, "page.html", "text/html", "<b>hi</b>")
	link := makeLink(t, s, key, page.ID, `{"disposition":"inline"}`)
	got := fromHost(t, s, contentHost, "GET", urlPath(t, link.URL), nil, nil)
	wantStatus(t, got, http.StatusOK)
	if csp := got.Header().Get("Content-Security-Policy"); csp != contentCSP {
		t.Errorf("link CSP = %q, want %q", csp, contentCSP)
	}
	if v := got.Header().Get("X-Frame-Options"); v != "" {
		t.Errorf("link has X-Frame-Options = %q", v)
	}
}

// TestInfoBrandTokens: /info carries only the palette and sidebar tokens the installation sets.
func TestInfoBrandTokens(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, func(c *config.Config) {
		c.UI.Brand.Palette.Dark = config.BrandTokens{AccentFill: "#1E3A8A"}
		c.UI.Brand.Sidebar = config.BrandSidebar{Active: "#FECC00"}
	})
	body := serveRaw(t, s, http.MethodGet, "/api/v1/info").Body.String()
	for _, want := range []string{
		`"palette":{"dark":{"accent_fill":"#1E3A8A"}}`,
		`"sidebar":{"active":"#FECC00"}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("info = %s, want %s", body, want)
		}
	}
	if strings.Contains(body, `"light"`) || strings.Contains(body, `"bg"`) {
		t.Errorf("info names unset tokens: %s", body)
	}
}

// TestAssetPathsAreCleanOrNotFound: a request for a file under /assets/ by a path that is not clean gets 404, never a
// redirect to the cleaned path and never the app page (the worker refuses redirected answers, so this is the server's half).
func TestAssetPathsAreCleanOrNotFound(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	for _, target := range []string{"/assets//index.html", "/assets/../index.html", "/assets/..%2findex.html", "/assets/..%2fapi%2fv1%2fme", "/assets/a%5cb.js", "/assets/%2e%2e%5cindex.html"} {
		rec := serveRaw(t, s, http.MethodGet, target)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", target, rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "" {
			t.Errorf("%s: redirects to %q", target, loc)
		}
	}
}

// unreadBody fails the test when anything reads it.
type unreadBody struct{ t *testing.T }

func (b unreadBody) Read(p []byte) (int, error) {
	b.t.Error("the share target read the request body")
	return 0, io.EOF
}

// TestShareTargetAnswers303WithoutReadingTheBody: with no service worker in control, a POST to /share-target (the share
// sheet's form post, decision 82) gets 303 to /share and its body is never read, so nothing the person shared reaches the
// server or is kept. A GET gets the same answer.
func TestShareTargetAnswers303WithoutReadingTheBody(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/share-target", unreadBody{t})
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	s.Handler().ServeHTTP(rec, req)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/share" {
		t.Errorf("Location = %q, want /share", loc)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	get := serveRaw(t, s, http.MethodGet, "/share-target")
	wantStatus(t, get, http.StatusSeeOther)
	if loc := get.Header().Get("Location"); loc != "/share" {
		t.Errorf("GET Location = %q, want /share", loc)
	}
}

// TestShareTargetWithUIOffIsJSON404: with the web interface off there is no share target, so the POST is the JSON 404 and
// its body is not read either.
func TestShareTargetWithUIOffIsJSON404(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, func(c *config.Config) { c.UI.Enabled = false })
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/share-target", unreadBody{t}))
	wantError(t, rec, http.StatusNotFound, "not_found")
}
