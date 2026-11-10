// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
)

// wantDeviceRefused asserts a 403 forbidden answer whose reason is device_token: the route refuses a terminal's device
// token, because it would mint a credential that outlives the device (decision 125, the derived-credential audit).
func wantDeviceRefused(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	e := wantError(t, rec, http.StatusForbidden, apierr.Forbidden)
	if e.Details["reason"] != "device_token" {
		t.Fatalf("reason = %v, want device_token (body %s)", e.Details["reason"], rec.Body.String())
	}
}

// A device token opens no editor: neither a view nor an edit, and no WOPI token is minted from it. The WOPI token would
// otherwise carry the device's empty key and outlive the device's revocation (review H1).
func TestDeviceTokenRefusedTheEditor(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"edit", "view"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			fx := newOfficeFixture(t)
			fx.grant(t, fx.doc.ID, fx.ann.ID, "editor")
			_, dtok := approvedToken(t, fx.s, fx.annSess)
			rec := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.doc.ID+"/office", dtok, map[string]any{"mode": mode})
			wantDeviceRefused(t, rec)
		})
	}
}

// Routes that mint or change a credential another person (or a link anyone can open) uses refuse a device token, whatever
// its role: a link, a share link and its reset or change, a sign-in link, a password reset, a verification, and a renewed
// share credential. Each answer is the device refusal, and nothing is minted.
func TestDeviceTokenRefusedDerivedCredentialRoutes(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.grant(t, fx.doc.ID, fx.ann.ID, "editor")
	_, dtok := approvedToken(t, fx.s, fx.annSess)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/folders/"+fx.fld.ID+"/settings", fx.key, map[string]any{"allow_links": true}), 200)
	made := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.doc.ID+"/share-links", fx.key,
		map[string]any{"audience": "organisation", "role": "viewer"})
	shl := decode[struct {
		Link struct {
			ID string `json:"id"`
		} `json:"link"`
	}](t, made)
	if shl.Link.ID == "" {
		t.Fatalf("no share link made by the account key: %d %s", made.Code, made.Body.String())
	}
	routes := []struct{ method, path string }{
		{"POST", "/api/v1/nodes/" + fx.doc.ID + "/links"},
		{"POST", "/api/v1/nodes/" + fx.doc.ID + "/share-links"},
		{"PATCH", "/api/v1/share-links/" + shl.Link.ID},
		{"POST", "/api/v1/share-links/" + shl.Link.ID + "/reset"},
		{"POST", "/api/v1/accounts/" + fx.acc.ID + "/users/" + fx.ann.ID + "/sign-in-links"},
		{"POST", "/api/v1/accounts/" + fx.acc.ID + "/users/" + fx.ann.ID + "/password-reset"},
		{"POST", "/api/v1/accounts/" + fx.acc.ID + "/users/" + fx.ann.ID + "/verification"},
		{"POST", "/api/v1/share/renew"},
	}
	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			wantDeviceRefused(t, call(t, fx.s, rt.method, rt.path, dtok, map[string]any{}))
		})
	}
}

// The zip a device makes is read with the device's token on each call, so it ends with the device: the read after
// revocation is refused (review H1, the derived-credential audit). This is a regression test; it passes once the reads
// check the device on every call.
func TestDeviceDerivedZipEndsWithRevoke(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.grant(t, fx.doc.ID, fx.ann.ID, "editor")
	_, dtok := approvedToken(t, fx.s, fx.annSess)
	zip := decode[struct {
		ID string `json:"id"`
	}](t, call(t, fx.s, "POST", "/api/v1/zips", dtok, map[string]any{"nodes": []string{fx.doc.ID}}))
	if zip.ID == "" {
		t.Fatal("the device could not make a zip")
	}
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/auth/cli/devices/"+deviceIDOf(t, fx, fx.annSess), nil), 204)
	wantError(t, call(t, fx.s, "GET", "/api/v1/zips/"+zip.ID, dtok, nil), http.StatusUnauthorized, apierr.Unauthenticated)
}

// deviceIDOf reads the id of the one device of the person in sess, from the device list.
func deviceIDOf(t *testing.T, fx *officeFixture, sess testSession) string {
	t.Helper()
	var list struct {
		Devices []struct {
			ID string `json:"id"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(callSession(t, fx.s, sess, "GET", "/api/v1/auth/cli/devices", nil).Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Devices) != 1 {
		t.Fatalf("devices = %+v, want one", list.Devices)
	}
	return list.Devices[0].ID
}

// A device token gets no Save As: saveAsPrincipal does not lift its node scope or its write cap. Its MintedBy is empty, so
// the old check on MintedBy alone let it through (review H1).
func TestDeviceTokenNeverGetsSaveAs(t *testing.T) {
	t.Parallel()
	p := &auth.Principal{Kind: auth.KindUser, UserID: "usr_x", Token: &auth.TokenScope{
		Audience: auth.CLIAudience, Node: "nod_a", Write: false}}
	got := saveAsPrincipal(p)
	if got.Token.Node != "nod_a" || got.Token.Write {
		t.Fatalf("saveAsPrincipal lifted a device token: node %q write %v", got.Token.Node, got.Token.Write)
	}
}
