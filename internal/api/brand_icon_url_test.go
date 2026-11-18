// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"testing"

	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
)

// The icon's URL in the brand answer (decision 225): the favicon's path when the installation has an icon picture, the config
// file's or an upload's, which the favicon is drawn from (decision 119); null with no icon. The app icon never sets it
// (decision 220), and a logo alone is not an icon.

// iconURLOf reads the brand's icon_url from /api/v1/info.
func iconURLOf(t *testing.T, s *Server) *string {
	t.Helper()
	info := decode[infoResponse](t, do(t, s.Handler(), "GET", "/api/v1/info"))
	return info.Brand.IconURL
}

// TestInfoIconURL: icon_url is null with no icon and with a logo alone, the favicon's path with the config file's icon, and
// null with only an app icon.
func TestInfoIconURL(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	if u := iconURLOf(t, s); u != nil {
		t.Errorf("icon_url = %q, want null with no icon", *u)
	}

	logo := iconTestFile(t, "logo.png", squarePNG(t, 800, 200))
	s = newTestServer(t, func(c *config.Config) { c.UI.Brand.LogoFile = logo })
	if u := iconURLOf(t, s); u != nil {
		t.Errorf("icon_url = %q, want null with a logo alone", *u)
	}

	icon := iconTestFile(t, "icon.png", squarePNG(t, 512, 512))
	s = newTestServer(t, func(c *config.Config) { c.UI.Brand.IconFile = icon })
	if u := iconURLOf(t, s); u == nil || *u != "/favicon.svg" {
		t.Errorf("icon_url = %v, want /favicon.svg with the config file's icon", u)
	}

	app := iconTestFile(t, "app.png", squarePNG(t, 512, 512))
	s = newTestServer(t, func(c *config.Config) { c.UI.Brand.AppIconFile = app })
	if u := iconURLOf(t, s); u != nil {
		t.Errorf("icon_url = %q, want null with only an app icon", *u)
	}
}

// TestUploadedIconURL: an uploaded icon sets icon_url to the favicon's path, removing the upload puts it back to null, and an
// uploaded app icon never sets it.
func TestUploadedIconURL(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	if u := iconURLOf(t, s); u != nil {
		t.Errorf("icon_url = %q, want null before any upload", *u)
	}

	wantStatus(t, putPicture(t, s, admin, iconRoute, "image/png", squarePNG(t, 512, 512)), 200)
	if u := iconURLOf(t, s); u == nil || *u != "/favicon.svg" {
		t.Errorf("icon_url = %v, want /favicon.svg with the uploaded icon", u)
	}

	wantStatus(t, call(t, s, http.MethodDelete, iconRoute, admin, nil), 200)
	if u := iconURLOf(t, s); u != nil {
		t.Errorf("icon_url = %q, want null after the upload is removed", *u)
	}

	wantStatus(t, putPicture(t, s, admin, appIconRoute, "image/png", squarePNG(t, 512, 512)), 200)
	if u := iconURLOf(t, s); u != nil {
		t.Errorf("icon_url = %q, want null with only an uploaded app icon", *u)
	}
}
