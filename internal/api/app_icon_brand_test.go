// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
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
	"github.com/shukiv/selfcloud/internal/web"
)

// The app icon while the server runs: an installation admin uploads or removes the picture of the installed app's icons
// through PUT and DELETE /api/v1/settings/brand/app-icon, by the same rule as the logo and the icon (decision 141). It
// never reaches the favicon.

const appIconRoute = "/api/v1/settings/brand/app-icon"

// iconPixel reads the corner pixel of a served PNG icon.
func iconPixel(t *testing.T, s *Server, path string) [4]uint8 {
	t.Helper()
	rec := serveRaw(t, s, http.MethodGet, path)
	if rec.Code != 200 {
		t.Fatalf("%s: status %d", path, rec.Code)
	}
	img, err := png.Decode(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	r, g, b, a := img.At(0, 0).RGBA()
	return [4]uint8{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
}

// TestAppIconUploadDrawsTheIconsAndIsAudited: an uploaded app icon draws the app icons, leaves the favicon as it was, is
// stored under the data folder with a name the server chose, and is audited with its type and hash; the settings answer
// lists it beside the logo and the icon, sorted by key.
func TestAppIconUploadDrawsTheIconsAndIsAudited(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	before := serveRaw(t, s, http.MethodGet, "/brand/icon-512.png").Header().Get("ETag")
	faviconBefore := serveRaw(t, s, http.MethodGet, "/favicon.svg").Body.String()
	png := squarePNG(t, 512, 512)
	sum := sha256Hex(png)
	rec := putPicture(t, s, admin, appIconRoute, "image/png", png)
	wantStatus(t, rec, 200)
	got := decode[testSettings](t, rec)
	if p := pictureIn(t, got, "ui.brand.app_icon"); p != (testPicture{Key: "ui.brand.app_icon", State: "uploaded", Type: "image/png", SHA256: sum}) {
		t.Errorf("app icon = %+v", p)
	}
	var keys []string
	for _, p := range got.Pictures {
		keys = append(keys, p.Key)
	}
	if !slices.Equal(keys, []string{"ui.brand.app_icon", "ui.brand.icon", "ui.brand.logo"}) {
		t.Errorf("pictures = %v, want the three sorted by key", keys)
	}
	if e := serveRaw(t, s, http.MethodGet, "/brand/icon-512.png").Header().Get("ETag"); e == before {
		t.Error("the icons kept the mark's ETag")
	}
	if px := iconPixel(t, s, "/brand/icon-512.png"); px != [4]uint8{9, 9, 9, 255} {
		t.Errorf("the icon's corner is %v, want the app icon's colour", px)
	}
	if f := serveRaw(t, s, http.MethodGet, "/favicon.svg").Body.String(); f != faviconBefore {
		t.Error("the app icon changed the favicon")
	}
	if names := brandFiles(t, s); !slices.Equal(names, []string{"app-icon-" + sum + ".png"}) {
		t.Errorf("brand folder = %v", names)
	}
	if body := rec.Body.String(); strings.Contains(body, filepath.Join(s.cfg.DataDir, "brand")) || strings.Contains(body, "app-icon-"+sum) {
		t.Errorf("the settings answer holds the picture's path: %s", body)
	}
	rows := settingsAudit(t, s)
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d", len(rows))
	}
	want := map[string]any{"ui.brand.app_icon": map[string]any{"old": "none", "new": "image/png sha256:" + sum, "source": "settings"}}
	details, _ := json.Marshal(rows[0].Details)
	if string(details) != mustJSON(t, want) {
		t.Errorf("audit details = %s, want %s", details, mustJSON(t, want))
	}
	// The same picture again changes nothing: no entry.
	wantStatus(t, putPicture(t, s, admin, appIconRoute, "image/png", png), 200)
	if n := len(settingsAudit(t, s)); n != 1 {
		t.Errorf("audit rows after the same upload = %d", n)
	}
	// DELETE puts the icons back to what they were, and the favicon never moved.
	wantStatus(t, call(t, s, http.MethodDelete, appIconRoute, admin, nil), 200)
	if e := serveRaw(t, s, http.MethodGet, "/brand/icon-512.png").Header().Get("ETag"); e != before {
		t.Errorf("after DELETE the icons' ETag is %q, want %q", e, before)
	}
	if f := serveRaw(t, s, http.MethodGet, "/favicon.svg").Body.String(); f != faviconBefore {
		t.Error("DELETE changed the favicon")
	}
	if names := brandFiles(t, s); len(names) != 0 {
		t.Errorf("brand folder after DELETE = %v", names)
	}
	rows = settingsAudit(t, s)
	if len(rows) != 2 {
		t.Fatalf("audit rows after DELETE = %d", len(rows))
	}
	want = map[string]any{"ui.brand.app_icon": map[string]any{"old": "image/png sha256:" + sum, "new": "none", "source": "default"}}
	details, _ = json.Marshal(rows[0].Details)
	if string(details) != mustJSON(t, want) {
		t.Errorf("DELETE audit details = %s, want %s", details, mustJSON(t, want))
	}
	// A DELETE with nothing uploaded changes nothing.
	wantStatus(t, call(t, s, http.MethodDelete, appIconRoute, admin, nil), 200)
	if n := len(settingsAudit(t, s)); n != 2 {
		t.Errorf("audit rows after a second DELETE = %d", n)
	}
}

// TestAppIconBesideTheIcon: with an icon uploaded, the app icon takes the icons' art and the favicon keeps the icon's.
func TestAppIconBesideTheIcon(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	icon := squarePNG(t, 512, 512)
	wantStatus(t, putPicture(t, s, admin, iconRoute, "image/png", icon), 200)
	faviconBefore := serveRaw(t, s, http.MethodGet, "/favicon.svg").Body.String()
	app := squarePNG(t, 600, 600)
	wantStatus(t, putPicture(t, s, admin, appIconRoute, "image/png", app), 200)
	if f := serveRaw(t, s, http.MethodGet, "/favicon.svg").Body.String(); f != faviconBefore {
		t.Error("the app icon changed the favicon the icon file draws")
	}
	if px := iconPixel(t, s, "/brand/icon-512.png"); px != [4]uint8{9, 9, 9, 255} {
		t.Errorf("the icon's corner is %v, want the app icon's colour", px)
	}
	wantStatus(t, call(t, s, http.MethodDelete, appIconRoute, admin, nil), 200)
	if f := serveRaw(t, s, http.MethodGet, "/favicon.svg").Body.String(); f != faviconBefore {
		t.Error("DELETE of the app icon changed the favicon")
	}
}

// TestAppIconContentIsChecked: each wrong body is refused, and leaves no file, no row and no entry behind. An SVG is
// refused: the app icon is a PNG only.
func TestAppIconContentIsChecked(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	big := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, web.MaxLogoBytes)...)
	for _, tc := range []struct {
		label, ct string
		body      []byte
		status    int
		code      apierr.Code
		msg       string
	}{
		{"an SVG", "image/svg+xml", []byte(testLogoSVG), 400, apierr.InvalidRequest, "PNG"},
		{"a text type", "text/plain", []byte(testLogoSVG), 415, apierr.UnsupportedMedia, "image/png or image/svg+xml"},
		{"a non-square PNG", "image/png", squarePNG(t, 600, 512), 400, apierr.InvalidRequest, "square PNG of 512 to 2048"},
		{"511 pixels", "image/png", squarePNG(t, 511, 511), 400, apierr.InvalidRequest, "square PNG of 512 to 2048"},
		{"2049 pixels", "image/png", squarePNG(t, 2049, 2049), 400, apierr.InvalidRequest, "square PNG of 512 to 2048"},
		{"cut short", "image/png", squarePNG(t, 512, 512)[:900], 400, apierr.InvalidRequest, "cannot be decoded"},
		{"over 256 KiB", "image/png", big, 413, apierr.TooLarge, "262144"},
	} {
		e := wantError(t, putPicture(t, s, admin, appIconRoute, tc.ct, tc.body), tc.status, tc.code)
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

// TestAppIconFileLocksIt: the config file writing ui.brand.app_icon_file locks the app icon against the API, as it locks
// the logo and the icon; the settings API cannot name the row or the file key.
func TestAppIconFileLocksIt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	appFile := filepath.Join(dir, "app.png")
	png := squarePNG(t, 512, 512)
	if err := os.WriteFile(appFile, png, 0o600); err != nil {
		t.Fatal(err)
	}
	s := newSettingsServer(t, "ui:\n  brand:\n    app_icon_file: "+appFile+"\n", nil)
	admin := adminKey(t, s)
	for _, rec := range []*httptest.ResponseRecorder{
		putPicture(t, s, admin, appIconRoute, "image/png", squarePNG(t, 600, 600)),
		call(t, s, http.MethodDelete, appIconRoute, admin, nil),
	} {
		e := wantError(t, rec, 409, apierr.Conflict)
		if keys, _ := e.Details["keys"].([]any); e.Details["reason"] != "setting_locked" || !slices.Equal(keys, []any{"ui.brand.app_icon"}) {
			t.Errorf("details %v", e.Details)
		}
	}
	for _, change := range []map[string]any{
		{"set": map[string]any{"ui.brand.app_icon": "image/png sha256:" + strings.Repeat("0", 64)}},
		{"set": map[string]any{"ui.brand.app_icon_file": "/etc/passwd"}},
		{"reset": []string{"ui.brand.app_icon"}},
		{"reset": []string{"ui.brand.app_icon_file"}},
	} {
		e := wantError(t, call(t, s, "PATCH", "/api/v1/settings", admin, change), 400, apierr.InvalidRequest)
		if e.Details["reason"] != "unknown_setting" {
			t.Errorf("%v: details %v", change, e.Details)
		}
	}
	got := getSettings(t, s, admin)
	if p := pictureIn(t, got, "ui.brand.app_icon"); p != (testPicture{Key: "ui.brand.app_icon", State: "file", Type: "image/png",
		SHA256: sha256Hex(png), Locked: true}) {
		t.Errorf("app icon = %+v", p)
	}
	if body := call(t, s, "GET", "/api/v1/settings", admin, nil).Body.String(); strings.Contains(body, appFile) {
		t.Errorf("the settings name the app icon's path: %s", body)
	}
	if px := iconPixel(t, s, "/brand/icon-512.png"); px != [4]uint8{9, 9, 9, 255} {
		t.Errorf("the icon's corner is %v, want the file's app icon", px)
	}
	if n := len(settingsAudit(t, s)); n != 0 {
		t.Errorf("refused changes wrote %d entries", n)
	}
	// The icon is not set in the file, so it is open.
	wantStatus(t, putPicture(t, s, admin, iconRoute, "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)), 200)
}

// TestAppIconRefusesWhoIsNotAnInstallationAdmin: an account key, a member, a sign-in-as and no credential are refused, as
// on the other picture routes; an admin's own session may change the app icon.
func TestAppIconRefusesWhoIsNotAnInstallationAdmin(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	s := fx.s
	body := string(squarePNG(t, 512, 512))
	hdr := map[string]string{"Content-Type": "image/png"}
	imp, _ := fx.signedInAsMember(t)
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		wantError(t, rawCall(t, s, method, appIconRoute, fx.accKey, body, hdr), 403, apierr.Forbidden)
		wantError(t, rawSession(t, s, fx.memSess, method, appIconRoute, body, hdr), 403, apierr.Forbidden)
		wantError(t, rawCall(t, s, method, appIconRoute, "", body, hdr), 401, apierr.Unauthenticated)
		e := wantError(t, rawSession(t, s, imp, method, appIconRoute, body, hdr), 403, apierr.Forbidden)
		if e.Details["reason"] != reasonImpersonation {
			t.Errorf("%s as a sign-in-as: reason %v", method, e.Details["reason"])
		}
	}
	if names := brandFiles(t, s); len(names) != 0 {
		t.Errorf("refused callers left files: %v", names)
	}
	if n := len(settingsAudit(t, s)); n != 0 {
		t.Errorf("refused callers wrote %d entries", n)
	}
	wantStatus(t, rawSession(t, s, fx.adminSess, http.MethodPut, appIconRoute, body, hdr), 200)
	rows := settingsAudit(t, s)
	if len(rows) != 1 || rows[0].ActorKind != "user" || rows[0].ActorID != fx.adminUser.ID {
		t.Errorf("the admin session's upload is audited as %+v", rows)
	}
	wantStatus(t, rawSession(t, s, fx.adminSess, http.MethodDelete, appIconRoute, "", nil), 200)
}

// TestAppIconFileRefusesStartNamingTheSetting: an app icon file that is the wrong shape, too small, too big or missing stops
// the start, and the error names ui.brand.app_icon_file. A wrong extension is refused by the config's own check (see
// TestAppIconFileExtension), before the start reads the file.
func TestAppIconFileRefusesStartNamingTheSetting(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		label string
		path  string
	}{
		{"511 pixels", iconTestFile(t, "small.png", squarePNG(t, 511, 511))},
		{"2049 pixels", iconTestFile(t, "big.png", squarePNG(t, 2049, 2049))},
		{"a non-square PNG", iconTestFile(t, "wide.png", squarePNG(t, 600, 512))},
		{"SVG content in a .png", iconTestFile(t, "fake.png", []byte("<svg></svg>"))},
		{"a missing file", filepath.Join(t.TempDir(), "missing.png")},
	} {
		var buf bytes.Buffer
		_, err := startWithLog(t, &buf, func(c *config.Config) { c.UI.Brand.AppIconFile = tc.path })
		if err == nil || !strings.Contains(err.Error(), "ui.brand.app_icon_file") {
			t.Errorf("%s: start returned %v, want a refusal naming ui.brand.app_icon_file", tc.label, err)
		}
	}
}

// TestAppIconSurvivesARestart: an uploaded app icon is there after a restart, from the brand folder, and draws the same
// icons.
func TestAppIconSurvivesARestart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first := serverOn(t, configFrom(t, dir, "", nil), true)
	admin := adminKey(t, first)
	png := squarePNG(t, 512, 512)
	wantStatus(t, putPicture(t, first, admin, appIconRoute, "image/png", png), 200)
	icon := serveRaw(t, first, http.MethodGet, "/brand/icon-512.png").Header().Get("ETag")

	var logs bytes.Buffer
	second := serverWithLog(t, configFrom(t, dir, "", nil), &logs)
	if e := serveRaw(t, second, http.MethodGet, "/brand/icon-512.png").Header().Get("ETag"); e != icon {
		t.Errorf("the icons after a restart: %q, want %q", e, icon)
	}
	got := getSettings(t, second, adminKey(t, second))
	if p := pictureIn(t, got, "ui.brand.app_icon"); p.State != "uploaded" || p.SHA256 != sha256Hex(png) {
		t.Errorf("app icon after a restart = %+v", p)
	}
	if w := warnLinesBeyondTheMachine(logs.String()); w != "" {
		t.Errorf("the restart logged a warning:\n%s", w)
	}
}
