// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/web"
)

// The brand changes while the server runs (decision 141): the brand keys are settings, and an admin uploads or removes the
// logo and the icon.

const (
	logoRoute = "/api/v1/settings/brand/logo"
	iconRoute = "/api/v1/settings/brand/icon"
	// testLogoSVG carries a marker that no answer other than the logo's own may hold.
	testLogoSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 40 10"><title>se2-logo-marker</title>` +
		`<rect width="40" height="10" fill="#0F766E"/></svg>`
)

// sha256Hex is the hex SHA-256 of data.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// pngClaiming is a PNG signature and a header chunk that claims w by h pixels, padded with zeros to size bytes when size is
// larger. Reading its size needs nothing more; decoding it fails.
func pngClaiming(w, h, size int) []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], uint32(w))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(h))
	ihdr[8], ihdr[9] = 8, 6 // eight bits a channel, RGBA
	out := []byte("\x89PNG\r\n\x1a\n")
	out = binary.BigEndian.AppendUint32(out, uint32(len(ihdr)))
	chunk := append([]byte("IHDR"), ihdr...)
	out = append(out, chunk...)
	out = binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(chunk))
	if size > len(out) {
		out = append(out, make([]byte, size-len(out))...)
	}
	return out
}

// putPicture sends a picture with a bearer credential and a declared type.
func putPicture(t *testing.T, s *Server, cred, route, ct string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, route, bytes.NewReader(body))
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	if cred != "" {
		req.Header.Set("Authorization", "Bearer "+cred)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// pictureIn returns the picture named key in a settings answer.
func pictureIn(t *testing.T, ts testSettings, key string) testPicture {
	t.Helper()
	for _, p := range ts.Pictures {
		if p.Key == key {
			return p
		}
	}
	t.Fatalf("no picture %q in %+v", key, ts.Pictures)
	return testPicture{}
}

// brandFiles lists the files in the server's brand folder.
func brandFiles(t *testing.T, s *Server) []string {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(s.cfg.DataDir, "brand"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names
}

// serverWithLog starts a server on cfg's database, which a server before it seeded, with its log in buf.
func serverWithLog(t *testing.T, cfg *config.Config, buf *bytes.Buffer) *Server {
	t.Helper()
	st, err := store.Open(context.Background(), store.Options{Path: cfg.Store.Path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s, err := New(Options{Config: cfg, Store: st, Mailer: &mail.Recorder{},
		Logger: slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))})
	if err != nil {
		t.Fatalf("the start failed: %v", err)
	}
	return s
}

// TestBrandKeysApplyToTheNextPage: a change to the name and the accent shows in the next page, the manifest and /info, and
// is audited as decision 139's changes are.
func TestBrandKeysApplyToTheNextPage(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	oldIcon := serveRaw(t, s, http.MethodGet, "/brand/icon-512.png").Header().Get("ETag")
	rec := patchSettings(t, s, admin, map[string]any{"ui.brand.name": "Acme Drive", "ui.brand.accent": "#0F766E",
		"ui.brand.sidebar.bg": "#112233", "ui.brand.palette.dark.accent_fill": "#1E3A8A"})
	wantStatus(t, rec, 200)
	if v := decode[testSettings](t, rec).get(t, "ui.brand.name"); v.Value != "Acme Drive" || v.Source != "settings" || v.Type != "string" {
		t.Errorf("ui.brand.name = %+v", v)
	}
	if body := serveRaw(t, s, http.MethodGet, "/").Body.String(); !strings.Contains(body, "<title>Acme Drive</title>") {
		t.Errorf("the next page: %.300s", body)
	}
	var m struct {
		Name       string `json:"name"`
		ThemeColor string `json:"theme_color"`
	}
	if err := json.Unmarshal(serveRaw(t, s, http.MethodGet, "/manifest.webmanifest").Body.Bytes(), &m); err != nil ||
		m.Name != "Acme Drive" || m.ThemeColor != "#0F766E" {
		t.Errorf("manifest = %+v, %v", m, err)
	}
	if e := serveRaw(t, s, http.MethodGet, "/brand/icon-512.png").Header().Get("ETag"); e == oldIcon {
		t.Error("the icons kept the old accent")
	}
	info := decode[infoResponse](t, do(t, s.Handler(), "GET", "/api/v1/info"))
	if info.Brand.Name != "Acme Drive" || info.Brand.Accent != "#0F766E" || info.Brand.Sidebar == nil ||
		info.Brand.Sidebar.Bg != "#112233" || info.Brand.Palette == nil || info.Brand.Palette.Dark.AccentFill != "#1E3A8A" {
		t.Errorf("info brand = %+v", info.Brand)
	}
	rows := settingsAudit(t, s)
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d", len(rows))
	}
	if d, _ := rows[0].Details["ui.brand.name"].(map[string]any); !maps.Equal(d, map[string]any{"old": "SelfCloud", "new": "Acme Drive",
		"source": "settings"}) {
		t.Errorf("audit = %v", rows[0].Details)
	}
	// A reset goes back to the default, live.
	wantStatus(t, patchSettings(t, s, admin, nil, "ui.brand.name"), 200)
	if body := serveRaw(t, s, http.MethodGet, "/").Body.String(); !strings.Contains(body, "<title>SelfCloud</title>") {
		t.Errorf("the page after a reset: %.300s", body)
	}
}

// TestBrandNameReachesTheUnsubscribePage: the page an unsubscribe link opens reads the name in use.
func TestBrandNameReachesTheUnsubscribePage(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	wantStatus(t, patchSettings(t, s, adminKey(t, s), map[string]any{"ui.brand.name": "Acme Drive"}), 200)
	if body := serveRaw(t, s, http.MethodGet, notifyUnsubPath+"?t=not-a-token").Body.String(); !strings.Contains(body, "<title>Acme Drive</title>") {
		t.Errorf("unsubscribe page: %.300s", body)
	}
}

// TestBrandKeysFollowTheFileRules: a brand value is held to the config file's rules through the API, each refusal naming
// its keys; the name may be as long as the file allows.
func TestBrandKeysFollowTheFileRules(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	for _, tc := range []struct {
		set  map[string]any
		keys []any
	}{
		{map[string]any{"ui.brand.accent": "red"}, []any{"ui.brand.accent"}},
		{map[string]any{"ui.brand.accent": "#abc", "ui.brand.sidebar.text": "white"}, []any{"ui.brand.accent", "ui.brand.sidebar.text"}},
		{map[string]any{"ui.brand.name": strings.Repeat("n", 65)}, []any{"ui.brand.name"}},
		{map[string]any{"ui.brand.name": ""}, []any{"ui.brand.name"}},
		{map[string]any{"ui.brand.name": "A\u0007B"}, []any{"ui.brand.name"}},
		{map[string]any{"ui.brand.name": 5}, []any{"ui.brand.name"}},
	} {
		e := wantError(t, patchSettings(t, s, admin, tc.set), 400, apierr.InvalidRequest)
		if keys, _ := e.Details["keys"].([]any); e.Details["reason"] != "invalid_setting" || !slices.Equal(keys, tc.keys) {
			t.Errorf("%v: details %v", tc.set, e.Details)
		}
	}
	long := strings.Repeat("ñ", 64)
	wantStatus(t, patchSettings(t, s, admin, map[string]any{"ui.brand.name": long}), 200)
	if body := serveRaw(t, s, http.MethodGet, "/").Body.String(); !strings.Contains(body, "<title>"+long+"</title>") {
		t.Errorf("a 64-character name: %.300s", body)
	}
	if n := len(settingsAudit(t, s)); n != 1 {
		t.Errorf("audit rows = %d, want the one change", n)
	}
}

// TestBrandKeyWrittenInTheFileIsLocked: a brand key the file writes is 409 setting_locked, as decision 139's keys are.
func TestBrandKeyWrittenInTheFileIsLocked(t *testing.T) {
	t.Parallel()
	s := newSettingsServer(t, "ui:\n  brand:\n    accent: \"#0F766E\"\n", map[string]string{"SELFCLOUD_UI_BRAND_NAME": "Acme"})
	admin := adminKey(t, s)
	for _, key := range []string{"ui.brand.accent", "ui.brand.name"} {
		e := wantError(t, patchSettings(t, s, admin, map[string]any{key: "#123456"}), 409, apierr.Conflict)
		if keys, _ := e.Details["keys"].([]any); e.Details["reason"] != "setting_locked" || !slices.Equal(keys, []any{key}) {
			t.Errorf("%s: details %v", key, e.Details)
		}
	}
}

// TestPictureUploadIsServedAndAudited: an uploaded logo is served at /brand/logo with its ETag, /info and the settings show
// it, it is stored under the data folder with a name the server chose, and one audit entry carries its type and hash and
// never its bytes.
func TestPictureUploadIsServedAndAudited(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	sum := sha256Hex([]byte(testLogoSVG))
	rec := putPicture(t, s, admin, logoRoute, "image/svg+xml; charset=utf-8", []byte(testLogoSVG))
	wantStatus(t, rec, 200)
	got := decode[testSettings](t, rec)
	if p := pictureIn(t, got, "ui.brand.logo"); p != (testPicture{Key: "ui.brand.logo", State: "uploaded", Type: "image/svg+xml",
		SHA256: sum}) {
		t.Errorf("logo = %+v", p)
	}
	if p := pictureIn(t, got, "ui.brand.icon"); p != (testPicture{Key: "ui.brand.icon", State: "none"}) {
		t.Errorf("icon = %+v", p)
	}
	if body := rec.Body.String(); strings.Contains(body, filepath.Join(s.cfg.DataDir, "brand")) || strings.Contains(body, "logo-"+sum) ||
		strings.Contains(body, "se2-logo-marker") {
		t.Errorf("the settings answer holds the picture's path or its bytes: %s", body)
	}

	logo := serveRaw(t, s, http.MethodGet, "/brand/logo")
	if logo.Code != 200 || logo.Body.String() != testLogoSVG || logo.Header().Get("ETag") != `"`+sum+`"` ||
		logo.Header().Get("Content-Type") != "image/svg+xml" || logo.Header().Get("Cache-Control") != "no-cache" ||
		logo.Header().Get("X-Content-Type-Options") != "nosniff" ||
		logo.Header().Get("Content-Security-Policy") != "default-src 'none'; style-src 'unsafe-inline'; sandbox" {
		t.Errorf("/brand/logo: %d %v %.80s", logo.Code, logo.Header(), logo.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/brand/logo", nil)
	req.Header.Set("If-None-Match", `"`+sum+`"`)
	again := httptest.NewRecorder()
	s.Handler().ServeHTTP(again, req)
	if again.Code != http.StatusNotModified || again.Body.Len() != 0 {
		t.Errorf("If-None-Match: %d, %d bytes", again.Code, again.Body.Len())
	}
	info := decode[infoResponse](t, do(t, s.Handler(), "GET", "/api/v1/info"))
	if info.Brand.LogoURL == nil || *info.Brand.LogoURL != "/brand/logo" || info.Brand.Artwork {
		t.Errorf("info brand = %+v", info.Brand)
	}
	// The installation has its own logo, so the sidebar's wordmark is gone (decision 126).
	wantError(t, serveRaw(t, s, http.MethodGet, "/brand/wordmark.svg"), http.StatusNotFound, apierr.NotFound)

	if names := brandFiles(t, s); !slices.Equal(names, []string{"logo-" + sum + ".svg"}) {
		t.Errorf("brand folder = %v", names)
	}
	dir := filepath.Join(s.cfg.DataDir, "brand")
	if st, err := os.Stat(dir); err != nil || st.Mode().Perm() != 0o700 {
		t.Errorf("brand folder mode: %v %v", st, err)
	}
	if st, err := os.Stat(filepath.Join(dir, "logo-"+sum+".svg")); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("logo file mode: %v %v", st, err)
	}

	rows := settingsAudit(t, s)
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d", len(rows))
	}
	want := map[string]any{"ui.brand.logo": map[string]any{"old": "none", "new": "image/svg+xml sha256:" + sum, "source": "settings"}}
	details, _ := json.Marshal(rows[0].Details)
	if string(details) != mustJSON(t, want) {
		t.Errorf("audit details = %s, want %s", details, mustJSON(t, want))
	}
	if bytes.Contains(details, []byte("se2-logo-marker")) || len(details) > 200 {
		t.Errorf("the audit carries the bytes: %s", details)
	}

	// The same picture again changes nothing: no entry.
	wantStatus(t, putPicture(t, s, admin, logoRoute, "image/svg+xml", []byte(testLogoSVG)), 200)
	if n := len(settingsAudit(t, s)); n != 1 {
		t.Errorf("audit rows after the same upload = %d", n)
	}
}

// mustJSON is v as JSON.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestPictureReplacedAndRemoved: a second logo replaces the first and its file; DELETE returns to the SelfCloud artwork
// (decision 126), removes the file and the row, and is audited; a DELETE with nothing uploaded changes nothing.
func TestPictureReplacedAndRemoved(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	artwork := serveRaw(t, s, http.MethodGet, "/brand/logo").Header().Get("ETag")
	wantStatus(t, putPicture(t, s, admin, logoRoute, "image/svg+xml", []byte(testLogoSVG)), 200)
	png := squarePNG(t, 600, 150)
	wantStatus(t, putPicture(t, s, admin, logoRoute, "image/png", png), 200)
	if names := brandFiles(t, s); !slices.Equal(names, []string{"logo-" + sha256Hex(png) + ".png"}) {
		t.Errorf("brand folder after the second logo = %v", names)
	}
	if logo := serveRaw(t, s, http.MethodGet, "/brand/logo"); !bytes.Equal(logo.Body.Bytes(), png) || logo.Header().Get("Content-Type") != "image/png" ||
		logo.Header().Get("Content-Security-Policy") != "default-src 'none'; sandbox" {
		t.Errorf("/brand/logo after the second logo: %v", logo.Header())
	}

	rec := call(t, s, http.MethodDelete, logoRoute, admin, nil)
	wantStatus(t, rec, 200)
	if p := pictureIn(t, decode[testSettings](t, rec), "ui.brand.logo"); p.State != "none" || p.Type != "" || p.SHA256 != "" {
		t.Errorf("logo after DELETE = %+v", p)
	}
	logo := serveRaw(t, s, http.MethodGet, "/brand/logo")
	if logo.Header().Get("ETag") != artwork || !strings.Contains(logo.Body.String(), `aria-label="SelfCloud"`) {
		t.Errorf("/brand/logo after DELETE is not the artwork: %v", logo.Header())
	}
	info := decode[infoResponse](t, do(t, s.Handler(), "GET", "/api/v1/info"))
	if info.Brand.LogoURL != nil || !info.Brand.Artwork {
		t.Errorf("info brand after DELETE = %+v", info.Brand)
	}
	wantStatus(t, serveRaw(t, s, http.MethodGet, "/brand/wordmark.svg"), 200)
	if names := brandFiles(t, s); len(names) != 0 {
		t.Errorf("brand folder after DELETE = %v", names)
	}
	if rows, err := s.store.Settings(context.Background()); err != nil || len(rows) != 0 {
		t.Errorf("settings rows after DELETE = %v, %v", rows, err)
	}
	rows := settingsAudit(t, s)
	if len(rows) != 3 {
		t.Fatalf("audit rows = %d, want three", len(rows))
	}
	if d, _ := rows[0].Details["ui.brand.logo"].(map[string]any); !maps.Equal(d, map[string]any{"old": "image/png sha256:" + sha256Hex(png),
		"new": "none", "source": "default"}) {
		t.Errorf("DELETE audit = %v", rows[0].Details)
	}
	wantStatus(t, call(t, s, http.MethodDelete, logoRoute, admin, nil), 200)
	if n := len(settingsAudit(t, s)); n != 3 {
		t.Errorf("a DELETE of nothing wrote an entry: %d rows", n)
	}
}

// TestUploadedIconDrawsTheIcons: an uploaded square PNG draws the app icons and the favicon; removed, the icons go back to
// the file's logo, as decision 119 picks them.
func TestUploadedIconDrawsTheIcons(t *testing.T) {
	t.Parallel()
	logoFile := iconTestFile(t, "logo.png", squarePNG(t, 800, 200))
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.UI.Brand.LogoFile = logoFile })
	admin := adminKey(t, s)
	fromLogo := serveRaw(t, s, http.MethodGet, "/brand/icon-512.png").Header().Get("ETag")
	wantStatus(t, putPicture(t, s, admin, iconRoute, "image/png", squarePNG(t, 512, 512)), 200)
	if e := serveRaw(t, s, http.MethodGet, "/brand/icon-512.png").Header().Get("ETag"); e == fromLogo {
		t.Error("the icons still come from the logo")
	}
	if f := serveRaw(t, s, http.MethodGet, "/favicon.svg").Body.String(); !strings.Contains(f, "data:image/png;base64,") {
		t.Errorf("favicon = %.120s", f)
	}
	wantStatus(t, call(t, s, http.MethodDelete, iconRoute, admin, nil), 200)
	if e := serveRaw(t, s, http.MethodGet, "/brand/icon-512.png").Header().Get("ETag"); e != fromLogo {
		t.Errorf("after DELETE the icons' ETag is %q, want the logo's %q", e, fromLogo)
	}
}

// TestPictureContentIsChecked: each wrong body is refused, and leaves no file, no row and no entry behind.
func TestPictureContentIsChecked(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	big := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, web.MaxLogoBytes)...)
	bomb := squarePNG(t, 3000, 3000)[:300]
	for _, tc := range []struct {
		label, route, ct string
		body             []byte
		status           int
		code             apierr.Code
		msg              string
	}{
		{"a text type", logoRoute, "text/plain", []byte(testLogoSVG), 415, apierr.UnsupportedMedia, "image/png or image/svg+xml"},
		{"no type", logoRoute, "", []byte(testLogoSVG), 415, apierr.UnsupportedMedia, "image/png or image/svg+xml"},
		{"a JPEG", logoRoute, "image/jpeg", []byte("\xff\xd8\xff\xe0"), 415, apierr.UnsupportedMedia, "image/png or image/svg+xml"},
		{"an SVG sent as PNG", logoRoute, "image/png", []byte(testLogoSVG), 400, apierr.InvalidRequest, "not a PNG"},
		{"a PNG sent as SVG", logoRoute, "image/svg+xml", append(squarePNG(t, 600, 150), []byte("<svg>")...), 400, apierr.InvalidRequest, "is a PNG"},
		{"text sent as SVG", logoRoute, "image/svg+xml", []byte("plain text"), 400, apierr.InvalidRequest, "not an SVG"},
		{"an empty body", logoRoute, "image/png", nil, 400, apierr.InvalidRequest, "not a PNG"},
		{"over 256 KiB", logoRoute, "image/png", big, 413, apierr.TooLarge, "262144"},
		{"a non-square icon", iconRoute, "image/png", squarePNG(t, 600, 512), 400, apierr.InvalidRequest, "square PNG of 512 to 2048"},
		{"an icon of 511", iconRoute, "image/png", squarePNG(t, 511, 511), 400, apierr.InvalidRequest, "square PNG of 512 to 2048"},
		{"an icon of 2049", iconRoute, "image/png", squarePNG(t, 2049, 2049), 400, apierr.InvalidRequest, "square PNG of 512 to 2048"},
		{"an icon that claims 3000 and is cut short", iconRoute, "image/png", bomb, 400, apierr.InvalidRequest, "square PNG of 512 to 2048"},
		{"an icon cut short", iconRoute, "image/png", squarePNG(t, 512, 512)[:900], 400, apierr.InvalidRequest, "cannot be decoded"},
		// Each viewer's browser decodes the logo whole, so a small file that claims a huge picture is refused by its header.
		{"a logo of 250 KiB that claims 30000 pixels a side", logoRoute, "image/png", pngClaiming(30000, 30000, 250<<10), 400,
			apierr.InvalidRequest, "30000x30000"},
		{"a logo of 4097 pixels wide", logoRoute, "image/png", pngClaiming(4097, 16, 0), 400, apierr.InvalidRequest, "at most 4096 on a side"},
	} {
		e := wantError(t, putPicture(t, s, admin, tc.route, tc.ct, tc.body), tc.status, tc.code)
		if !strings.Contains(e.Message, tc.msg) {
			t.Errorf("%s: %q does not say %q", tc.label, e.Message, tc.msg)
		}
		if tc.status == 400 && e.Details["reason"] != "invalid_picture" {
			t.Errorf("%s: details %v", tc.label, e.Details)
		}
	}
	if names := brandFiles(t, s); len(names) != 0 {
		t.Errorf("refused uploads left files: %v", names)
	}
	if rows, err := s.store.Settings(context.Background()); err != nil || len(rows) != 0 {
		t.Errorf("refused uploads left rows: %v, %v", rows, err)
	}
	if n := len(settingsAudit(t, s)); n != 0 {
		t.Errorf("refused uploads wrote %d entries", n)
	}
}

// pngStream is a body of n bytes, a PNG signature and then zeros, that counts the bytes read of it.
type pngStream struct {
	n, read int
}

func (c *pngStream) Read(p []byte) (int, error) {
	if c.read >= c.n {
		return 0, io.EOF
	}
	k := min(len(p), c.n-c.read)
	for i := range k {
		p[i] = 0
		if j := c.read + i; j < 8 {
			p[i] = "\x89PNG\r\n\x1a\n"[j]
		}
	}
	c.read += k
	return k, nil
}

// TestPictureSizeIsCappedBeforeTheBodyIsRead: a body that declares more than 256 KiB is refused unread, and one with no
// declared length is read no further than the cap.
func TestPictureSizeIsCappedBeforeTheBodyIsRead(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	for _, declared := range []bool{true, false} {
		body := &pngStream{n: 64 << 20}
		req := httptest.NewRequest(http.MethodPut, logoRoute, body)
		req.ContentLength = -1
		if declared {
			req.ContentLength = int64(body.n)
		}
		req.Header.Set("Content-Type", "image/png")
		req.Header.Set("Authorization", "Bearer "+admin)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		e := wantError(t, rec, 413, apierr.TooLarge)
		if e.Details["limit_bytes"] != float64(web.MaxLogoBytes) {
			t.Errorf("declared %v: details %v", declared, e.Details)
		}
		if declared && body.read != 0 {
			t.Errorf("a declared 64 MiB body was read: %d bytes", body.read)
		}
		if !declared && body.read > web.MaxLogoBytes+1 {
			t.Errorf("an undeclared body was read past the cap: %d bytes", body.read)
		}
	}
}

// TestPictureFileLocksIt: a picture the config file sets is locked: PUT, DELETE and an empty body are 409, a PATCH cannot
// name it or its file key, and the settings show the file's picture with no path. The other picture stays open.
func TestPictureFileLocksIt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logoFile := filepath.Join(dir, "own-logo.svg")
	if err := os.WriteFile(logoFile, []byte(testLogoSVG), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newSettingsServer(t, "ui:\n  brand:\n    logo_file: "+logoFile+"\n", nil)
	admin := adminKey(t, s)
	for _, rec := range []*httptest.ResponseRecorder{
		putPicture(t, s, admin, logoRoute, "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)),
		putPicture(t, s, admin, logoRoute, "", nil),
		putPicture(t, s, admin, logoRoute, "image/png", squarePNG(t, 600, 150)),
		call(t, s, http.MethodDelete, logoRoute, admin, nil),
	} {
		e := wantError(t, rec, 409, apierr.Conflict)
		if keys, _ := e.Details["keys"].([]any); e.Details["reason"] != "setting_locked" || !slices.Equal(keys, []any{"ui.brand.logo"}) {
			t.Errorf("details %v", e.Details)
		}
	}
	for _, change := range []map[string]any{
		{"set": map[string]any{"ui.brand.logo": "image/svg+xml sha256:" + strings.Repeat("0", 64)}},
		{"set": map[string]any{"ui.brand.logo_file": "/etc/passwd"}},
		{"reset": []string{"ui.brand.logo"}},
		{"reset": []string{"ui.brand.logo_file"}},
	} {
		e := wantError(t, call(t, s, "PATCH", "/api/v1/settings", admin, change), 400, apierr.InvalidRequest)
		if e.Details["reason"] != "unknown_setting" {
			t.Errorf("%v: details %v", change, e.Details)
		}
	}
	got := getSettings(t, s, admin)
	if p := pictureIn(t, got, "ui.brand.logo"); p != (testPicture{Key: "ui.brand.logo", State: "file", Type: "image/svg+xml",
		SHA256: sha256Hex([]byte(testLogoSVG)), Locked: true}) {
		t.Errorf("logo = %+v", p)
	}
	if body := call(t, s, "GET", "/api/v1/settings", admin, nil).Body.String(); strings.Contains(body, logoFile) {
		t.Errorf("the settings name the logo's path: %s", body)
	}
	if logo := serveRaw(t, s, http.MethodGet, "/brand/logo"); logo.Body.String() != testLogoSVG {
		t.Errorf("/brand/logo is not the file's logo: %.80s", logo.Body.String())
	}
	if n := len(settingsAudit(t, s)); n != 0 {
		t.Errorf("refused changes wrote %d entries", n)
	}
	// The icon is not set in the file, so it is open.
	wantStatus(t, putPicture(t, s, admin, iconRoute, "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)), 200)
}

// TestPictureRefusesWhoIsNotAnInstallationAdmin: an account key, a member, a sign-in-as, no credential and a session without
// its CSRF token are refused, as on /settings; an admin's own session may change the pictures.
func TestPictureRefusesWhoIsNotAnInstallationAdmin(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	s := fx.s
	svg := testLogoSVG
	hdr := map[string]string{"Content-Type": "image/svg+xml"}
	imp, _ := fx.signedInAsMember(t)
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		for _, route := range []string{logoRoute, iconRoute} {
			wantError(t, rawCall(t, s, method, route, fx.accKey, svg, hdr), 403, apierr.Forbidden)
			wantError(t, rawSession(t, s, fx.memSess, method, route, svg, hdr), 403, apierr.Forbidden)
			wantError(t, rawCall(t, s, method, route, "", svg, hdr), 401, apierr.Unauthenticated)
			e := wantError(t, rawSession(t, s, imp, method, route, svg, hdr), 403, apierr.Forbidden)
			if e.Details["reason"] != reasonImpersonation {
				t.Errorf("%s %s as a sign-in-as: reason %v", method, route, e.Details["reason"])
			}
			// The admin's own session without its CSRF token, and from another site.
			req := httptest.NewRequest(method, route, strings.NewReader(svg))
			req.Header.Set("Content-Type", "image/svg+xml")
			req.Header.Set("Origin", s.origin)
			req.AddCookie(fx.adminSess.cookie)
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			wantError(t, rec, 403, apierr.Forbidden)
			req = httptest.NewRequest(method, route, strings.NewReader(svg))
			req.Header.Set("Content-Type", "image/svg+xml")
			req.Header.Set("Origin", "https://evil.example")
			req.Header.Set("X-CSRF-Token", fx.adminSess.csrf)
			req.AddCookie(fx.adminSess.cookie)
			rec = httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden && rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s from another site: %d", method, route, rec.Code)
			}
		}
	}
	if names := brandFiles(t, s); len(names) != 0 {
		t.Errorf("refused callers left files: %v", names)
	}
	if n := len(settingsAudit(t, s)); n != 0 {
		t.Errorf("refused callers wrote %d entries", n)
	}
	wantStatus(t, rawSession(t, s, fx.adminSess, http.MethodPut, logoRoute, svg, hdr), 200)
	rows := settingsAudit(t, s)
	if len(rows) != 1 || rows[0].ActorKind != "user" || rows[0].ActorID != fx.adminUser.ID {
		t.Errorf("the admin session's upload is audited as %+v", rows)
	}
	wantStatus(t, rawSession(t, s, fx.adminSess, http.MethodDelete, logoRoute, "", nil), 200)
}

// TestPictureSurvivesARestart: an uploaded logo and icon are there after a restart, from the brand folder.
func TestPictureSurvivesARestart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first := serverOn(t, configFrom(t, dir, "", nil), true)
	admin := adminKey(t, first)
	png := squarePNG(t, 512, 512)
	wantStatus(t, putPicture(t, first, admin, logoRoute, "image/svg+xml", []byte(testLogoSVG)), 200)
	wantStatus(t, putPicture(t, first, admin, iconRoute, "image/png", png), 200)
	icon := serveRaw(t, first, http.MethodGet, "/brand/icon-512.png").Header().Get("ETag")

	var logs bytes.Buffer
	second := serverWithLog(t, configFrom(t, dir, "", nil), &logs)
	if logo := serveRaw(t, second, http.MethodGet, "/brand/logo"); logo.Body.String() != testLogoSVG {
		t.Errorf("/brand/logo after a restart: %.80s", logo.Body.String())
	}
	if e := serveRaw(t, second, http.MethodGet, "/brand/icon-512.png").Header().Get("ETag"); e != icon {
		t.Errorf("the icons after a restart: %q, want %q", e, icon)
	}
	got := getSettings(t, second, adminKey(t, second))
	if p := pictureIn(t, got, "ui.brand.icon"); p.State != "uploaded" || p.SHA256 != sha256Hex(png) {
		t.Errorf("icon after a restart = %+v", p)
	}
	if strings.Contains(logs.String(), "level=WARN") {
		t.Errorf("the restart logged a warning:\n%s", logs.String())
	}
}

// TestStoredPictureThatFailsIsIgnoredAtStart: a stored picture whose file changed, went missing or whose row is not one the
// server writes is logged and ignored; the server starts on the artwork.
func TestStoredPictureThatFailsIsIgnoredAtStart(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		label  string
		spoil  func(t *testing.T, s *Server, path string)
		reason string
	}{
		{"changed", func(t *testing.T, s *Server, path string) {
			if err := os.WriteFile(path, []byte(`<svg xmlns="http://www.w3.org/2000/svg"><title>other</title></svg>`), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "hash"},
		{"no longer an SVG", func(t *testing.T, s *Server, path string) {
			if err := os.WriteFile(path, []byte("plain text"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "not an SVG"},
		{"missing", func(t *testing.T, s *Server, path string) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}, "no such file"},
		{"a link to the same bytes", func(t *testing.T, s *Server, path string) {
			elsewhere := filepath.Join(t.TempDir(), "same.svg")
			if err := os.WriteFile(elsewhere, []byte(testLogoSVG), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(elsewhere, path); err != nil {
				t.Fatal(err)
			}
		}, "not a regular file"},
		{"a row the server does not write", func(t *testing.T, s *Server, path string) {
			if err := s.store.Write(context.Background(), func(tx *store.Tx) error {
				return tx.SetSetting(context.Background(), "ui.brand.logo", "image/svg+xml sha256:../../etc/passwd")
			}); err != nil {
				t.Fatal(err)
			}
		}, "not a picture"},
	} {
		dir := t.TempDir()
		first := serverOn(t, configFrom(t, dir, "", nil), true)
		admin := adminKey(t, first)
		wantStatus(t, putPicture(t, first, admin, logoRoute, "image/svg+xml", []byte(testLogoSVG)), 200)
		tc.spoil(t, first, filepath.Join(dir, "brand", "logo-"+sha256Hex([]byte(testLogoSVG))+".svg"))

		var logs bytes.Buffer
		second := serverWithLog(t, configFrom(t, dir, "", nil), &logs)
		if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "key=ui.brand.logo") ||
			!strings.Contains(logs.String(), tc.reason) {
			t.Errorf("%s: the log does not say why:\n%s", tc.label, logs.String())
		}
		if logo := serveRaw(t, second, http.MethodGet, "/brand/logo"); !strings.Contains(logo.Body.String(), `aria-label="SelfCloud"`) {
			t.Errorf("%s: /brand/logo is not the artwork", tc.label)
		}
		if p := pictureIn(t, getSettings(t, second, adminKey(t, second)), "ui.brand.logo"); p.State != "none" {
			t.Errorf("%s: logo = %+v", tc.label, p)
		}
	}
}

// TestStoredPictureUnderTheFileIsKept: a file that starts setting the logo hides the upload, which stays stored and comes
// back when the file stops setting it.
func TestStoredPictureUnderTheFileIsKept(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first := serverOn(t, configFrom(t, dir, "", nil), true)
	wantStatus(t, putPicture(t, first, adminKey(t, first), logoRoute, "image/svg+xml", []byte(testLogoSVG)), 200)

	own := filepath.Join(t.TempDir(), "file-logo.svg")
	if err := os.WriteFile(own, []byte(`<svg xmlns="http://www.w3.org/2000/svg"><title>file</title></svg>`), 0o600); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	second := serverWithLog(t, configFrom(t, dir, "ui:\n  brand:\n    logo_file: "+own+"\n", nil), &logs)
	if logo := serveRaw(t, second, http.MethodGet, "/brand/logo"); !strings.Contains(logo.Body.String(), "<title>file</title>") {
		t.Errorf("the file's logo does not win: %.80s", logo.Body.String())
	}
	if !strings.Contains(logs.String(), "key=ui.brand.logo") || !strings.Contains(logs.String(), "config file") {
		t.Errorf("the hidden upload is not logged:\n%s", logs.String())
	}

	third := serverWithLog(t, configFrom(t, dir, "", nil), &bytes.Buffer{})
	if logo := serveRaw(t, third, http.MethodGet, "/brand/logo"); logo.Body.String() != testLogoSVG {
		t.Errorf("the upload did not come back: %.80s", logo.Body.String())
	}
}

// TestLogoFileOverThePixelCapIsWarnedAtStart: a logo file larger than an upload may be still starts the server and is served
// as it is, with one warning at start that names its key and its size and no path.
func TestLogoFileOverThePixelCapIsWarnedAtStart(t *testing.T) {
	t.Parallel()
	big := pngClaiming(30000, 30000, 250<<10)
	own := filepath.Join(t.TempDir(), "big-logo.png")
	if err := os.WriteFile(own, big, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := configFrom(t, t.TempDir(), "ui:\n  brand:\n    logo_file: "+own+"\n", nil)
	seedMigratedDB(t, cfg.Store.Path)
	var logs bytes.Buffer
	s := serverWithLog(t, cfg, &logs)
	var warned []string
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, "level=WARN") && strings.Contains(line, "30000x30000") {
			warned = append(warned, line)
		}
	}
	if len(warned) != 1 || !strings.Contains(warned[0], "ui.brand.logo_file") || strings.Contains(warned[0], own) {
		t.Errorf("warnings about the logo's size = %q\nlog:\n%s", warned, logs.String())
	}
	if logo := serveRaw(t, s, http.MethodGet, "/brand/logo"); logo.Code != 200 || !bytes.Equal(logo.Body.Bytes(), big) {
		t.Errorf("/brand/logo: %d, %d bytes", logo.Code, logo.Body.Len())
	}
}

// TestFailedRebuildKeepsTheOldBrand: when the web interface's brand cannot be built, the change is refused, nothing is
// stored, no file is left, and the brand in use stays, for a brand key and for each picture route.
func TestFailedRebuildKeepsTheOldBrand(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	icon := squarePNG(t, 512, 512)
	wantStatus(t, putPicture(t, s, admin, iconRoute, "image/png", icon), 200)
	iconETag := serveRaw(t, s, http.MethodGet, "/brand/icon-512.png").Header().Get("ETag")
	s.buildBrand = func(web.BrandOptions) (*web.Brand, error) { return nil, errors.New("the icons cannot be drawn") }

	wantStatus(t, patchSettings(t, s, admin, map[string]any{"ui.brand.name": "Acme Drive"}), 500)
	wantStatus(t, putPicture(t, s, admin, logoRoute, "image/svg+xml", []byte(testLogoSVG)), 500)
	wantStatus(t, putPicture(t, s, admin, iconRoute, "image/png", squarePNG(t, 600, 600)), 500)
	wantStatus(t, call(t, s, http.MethodDelete, iconRoute, admin, nil), 500)

	if body := serveRaw(t, s, http.MethodGet, "/").Body.String(); !strings.Contains(body, "<title>SelfCloud</title>") {
		t.Errorf("the page after a failed change: %.200s", body)
	}
	if e := serveRaw(t, s, http.MethodGet, "/brand/icon-512.png").Header().Get("ETag"); e != iconETag {
		t.Errorf("the icons changed: %q, want %q", e, iconETag)
	}
	if logo := serveRaw(t, s, http.MethodGet, "/brand/logo"); !strings.Contains(logo.Body.String(), `aria-label="SelfCloud"`) {
		t.Error("/brand/logo is not the artwork after a failed upload")
	}
	got := getSettings(t, s, admin)
	if v := got.get(t, "ui.brand.name"); v.Value != "SelfCloud" || v.Source != "default" {
		t.Errorf("ui.brand.name = %+v", v)
	}
	if p := pictureIn(t, got, "ui.brand.icon"); p.State != "uploaded" || p.SHA256 != sha256Hex(icon) {
		t.Errorf("icon = %+v", p)
	}
	if p := pictureIn(t, got, "ui.brand.logo"); p.State != "none" {
		t.Errorf("logo = %+v", p)
	}
	if names := brandFiles(t, s); !slices.Equal(names, []string{"icon-" + sha256Hex(icon) + ".png"}) {
		t.Errorf("brand folder = %v", names)
	}
	rows, err := s.store.Settings(context.Background())
	if err != nil || !maps.Equal(rows, map[string]string{"ui.brand.icon": "image/png sha256:" + sha256Hex(icon)}) {
		t.Errorf("rows = %v, %v", rows, err)
	}
	if n := len(settingsAudit(t, s)); n != 1 {
		t.Errorf("audit rows = %d, want the first upload's", n)
	}
}

// TestPictureThatCannotBeStoredIsNotUsed: an upload whose transaction fails answers an error, and leaves no file, no row and
// the old logo.
func TestPictureThatCannotBeStoredIsNotUsed(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	execOn(t, s, "CREATE TRIGGER refuse_settings_audit BEFORE INSERT ON audit_log "+
		"WHEN NEW.action = 'installation.settings' BEGIN SELECT RAISE(ABORT, 'audit refused'); END")
	wantStatus(t, putPicture(t, s, admin, logoRoute, "image/svg+xml", []byte(testLogoSVG)), 500)
	if names := brandFiles(t, s); len(names) != 0 {
		t.Errorf("brand folder = %v", names)
	}
	if rows, err := s.store.Settings(context.Background()); err != nil || len(rows) != 0 {
		t.Errorf("rows = %v, %v", rows, err)
	}
	if logo := serveRaw(t, s, http.MethodGet, "/brand/logo"); !strings.Contains(logo.Body.String(), `aria-label="SelfCloud"`) {
		t.Error("/brand/logo is not the artwork")
	}
	if info := decode[infoResponse](t, do(t, s.Handler(), "GET", "/api/v1/info")); !info.Brand.Artwork {
		t.Errorf("info brand = %+v", info.Brand)
	}
}

// TestPictureWithTheInterfaceOff: a headless server takes and serves the logo.
func TestPictureWithTheInterfaceOff(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.UI.Enabled = false })
	wantStatus(t, putPicture(t, s, adminKey(t, s), logoRoute, "image/svg+xml", []byte(testLogoSVG)), 200)
	if logo := serveRaw(t, s, http.MethodGet, "/brand/logo"); logo.Body.String() != testLogoSVG {
		t.Errorf("/brand/logo: %.80s", logo.Body.String())
	}
}
