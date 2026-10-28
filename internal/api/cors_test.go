// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
)

const appOrigin = "https://app.example.com"

// corsServer is a server that allows appOrigin.
func corsServer(t *testing.T) *Server {
	t.Helper()
	return newAuthServer(t, &mail.Recorder{}, func(c *config.Config) {
		c.CORS.AllowedOrigins = []string{appOrigin}
	})
}

// preflight sends a CORS preflight for method from origin, asking for the given request headers.
func preflight(t *testing.T, s *Server, origin, p, method, headers string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodOptions, p, nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", method)
	if headers != "" {
		req.Header.Set("Access-Control-Request-Headers", headers)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestCORSPreflightAnswersBeforeRouting(t *testing.T) {
	s := corsServer(t)
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/uploads"},
		{http.MethodPatch, "/api/v1/uploads/upl_00000000000000000000000000"},
		{http.MethodDelete, "/api/v1/nodes/nod_00000000000000000000000000"},
		{http.MethodGet, "/api/v1/accounts/acc_00000000000000000000000000/usage"},
	} {
		rec := preflight(t, s, appOrigin, tc.path, tc.method, "Authorization, Upload-Offset, Content-Type")
		if rec.Code != http.StatusNoContent {
			t.Fatalf("preflight %s %s = %d %s; want 204", tc.method, tc.path, rec.Code, rec.Body)
		}
		h := rec.Header()
		if h.Get("Access-Control-Allow-Origin") != appOrigin {
			t.Errorf("%s %s: Allow-Origin = %q", tc.method, tc.path, h.Get("Access-Control-Allow-Origin"))
		}
		if h.Get("Access-Control-Allow-Methods") != "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS" {
			t.Errorf("Allow-Methods = %q", h.Get("Access-Control-Allow-Methods"))
		}
		if h.Get("Access-Control-Max-Age") != "600" {
			t.Errorf("Max-Age = %q", h.Get("Access-Control-Max-Age"))
		}
		for _, want := range []string{"Authorization", "Upload-Offset", "Tus-Resumable", "If-Match"} {
			if !strings.Contains(h.Get("Access-Control-Allow-Headers"), want) {
				t.Errorf("Allow-Headers %q lacks %s", h.Get("Access-Control-Allow-Headers"), want)
			}
		}
		if h.Get("Access-Control-Allow-Credentials") != "" {
			t.Error("a CORS answer allows credentials")
		}
		if !strings.Contains(h.Get("Vary"), "Origin") {
			t.Errorf("Vary = %q; want Origin", h.Get("Vary"))
		}
	}
}

func TestCORSDisallowedOriginGetsNoHeaders(t *testing.T) {
	s := corsServer(t)
	rec := preflight(t, s, "https://evil.example.net", "/api/v1/uploads", http.MethodPost, "")
	wantError(t, rec, 403, apierr.Forbidden)
	for _, h := range []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Methods", "Access-Control-Allow-Headers"} {
		if rec.Header().Get(h) != "" {
			t.Errorf("a refused preflight carries %s", h)
		}
	}
	// The server's own origin is not a CORS origin, even when it is written in the list.
	own := preflight(t, s, s.origin, "/api/v1/uploads", http.MethodPost, "")
	if own.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("the server's own origin got CORS headers")
	}
}

func TestCORSPlainTusOptionsStillAnswers(t *testing.T) {
	s := corsServer(t)
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/uploads", nil)
	req.Header.Set("Origin", appOrigin)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Header().Get("Tus-Resumable") == "" || rec.Header().Get("Tus-Extension") == "" {
		t.Errorf("tus capability probe = %d %v; want the tus headers", rec.Code, rec.Header())
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != appOrigin {
		t.Errorf("an allowed origin's ordinary request got Allow-Origin %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORSActualResponseExposesHeaders(t *testing.T) {
	s := corsServer(t)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/uploads", strings.NewReader(""))
	req.Header.Set("Origin", appOrigin)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Upload-Length", "3")
	req.Header.Set("Upload-Metadata", md("folder", f.ID, "name", "a.txt"))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != appOrigin {
		t.Errorf("Allow-Origin = %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
	exposed := rec.Header().Get("Access-Control-Expose-Headers")
	for _, want := range []string{"Location", "Upload-Offset", "Upload-Expires", "Selfcloud-Upload-State",
		"Selfcloud-Node-ID", "Selfcloud-Max-Chunk-Size", "Tus-Resumable", "ETag", "Retry-After", "X-Request-Id"} {
		if !strings.Contains(exposed, want) {
			t.Errorf("Expose-Headers lacks %s: %q", want, exposed)
		}
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Error("a CORS response allows credentials")
	}
}

func TestCORSOffWithoutOrigins(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	rec := preflight(t, s, appOrigin, "/api/v1/uploads", http.MethodPost, "")
	if rec.Header().Get("Access-Control-Allow-Origin") != "" || rec.Header().Get("Vary") != "" {
		t.Errorf("CORS is off, yet the answer has %v", rec.Header())
	}
}

// crossSiteSession sends a request that carries a valid session cookie but came from another site.
func crossSiteSession(t *testing.T, s *Server, sess testSession, method, p, origin string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, p, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	req.Header.Set("X-CSRF-Token", sess.csrf)
	req.AddCookie(sess.cookie)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestCookiesNeverAuthenticateAcrossSites(t *testing.T) {
	s := corsServer(t)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	ann, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	wantStatus(t, call(t, s, "PUT", "/api/v1/accounts/"+acc.ID+"/folders/"+f.ID+"/grants/"+ann.ID, key,
		map[string]any{"level": "write"}), 200)

	// A valid session is refused for a request from another site, on every route, read or write.
	wantError(t, crossSiteSession(t, s, sess, http.MethodGet, "/api/v1/me", appOrigin), 401, apierr.Unauthenticated)
	wantError(t, crossSiteSession(t, s, sess, http.MethodGet, "/api/v1/folders", "https://evil.example.net"), 401, apierr.Unauthenticated)
	wantError(t, crossSiteSession(t, s, sess, http.MethodPost, "/api/v1/folders/"+f.ID+"/directories", "https://evil.example.net"),
		401, apierr.Unauthenticated)
	wantError(t, crossSiteSession(t, s, sess, http.MethodGet, "/api/v1/me", "null"), 401, apierr.Unauthenticated)

	// The same session from the server's own origin still works, and so does a request without Origin.
	wantStatus(t, callSession(t, s, sess, http.MethodGet, "/api/v1/me", nil), 200)
	wantStatus(t, rawSession(t, s, sess, http.MethodPost, "/api/v1/folders/"+f.ID+"/directories",
		`{"path":"/own-origin","parents":true}`, map[string]string{"Content-Type": "application/json"}), 201)
}

func TestConfiguredCORSDoesNotWeakenSigninCSRF(t *testing.T) {
	s := corsServer(t)
	// A sign-in from another site is still refused, whatever the CORS list says.
	rec := rawCall(t, s, "POST", "/api/v1/auth/login", "", `{"email":"a@example.com","password":"x"}`,
		map[string]string{"Content-Type": "application/json", "Origin": appOrigin})
	wantError(t, rec, 403, apierr.Forbidden)
}
