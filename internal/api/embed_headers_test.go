// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// serveCookie sends a GET through the whole stack with one cookie.
func serveCookie(t *testing.T, s *Server, target string, c *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if c != nil {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// wantFramable checks the page allows the given origins and drops X-Frame-Options.
func wantFramable(t *testing.T, rec *httptest.ResponseRecorder, origins string) {
	t.Helper()
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "frame-ancestors 'self' "+origins) {
		t.Errorf("CSP %q lacks frame-ancestors 'self' %s", csp, origins)
	}
	if xfo := rec.Header().Get("X-Frame-Options"); xfo != "" {
		t.Errorf("X-Frame-Options = %q; want none when framable", xfo)
	}
}

// wantNotFramable checks the page keeps frame-ancestors 'none' and X-Frame-Options DENY.
func wantNotFramable(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP %q lacks frame-ancestors 'none'", csp)
	}
	if xfo := rec.Header().Get("X-Frame-Options"); xfo != "DENY" {
		t.Errorf("X-Frame-Options = %q; want DENY", xfo)
	}
}

// TestEmbedPageHeaders: the sign-in page named by a frame link, and the pages of a frame session, allow
// the account's origins; everything else keeps frame-ancestors 'none' and DENY (decision 197).
func TestEmbedPageHeaders(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.PublicURL = "https://drive.example.com" })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "member@example.com")
	call(t, s, "PUT", "/api/v1/accounts/"+acc.ID+"/embed", key, embedBody{Origins: []string{"https://host.example"}})

	// The sign-in page the frame link names.
	wantFramable(t, serveRaw(t, s, http.MethodGet, "/sign-in?frame="+acc.ID), "https://host.example")
	// No frame query, an unknown account, a malformed one: none.
	wantNotFramable(t, serveRaw(t, s, http.MethodGet, "/sign-in"))
	wantNotFramable(t, serveRaw(t, s, http.MethodGet, "/sign-in?frame="+id.New("acc")))
	wantNotFramable(t, serveRaw(t, s, http.MethodGet, "/sign-in?frame=not-an-id"))

	// A frame session's pages are framable; a tab session's and a cookieless browser's are not.
	_, tok := frameLinkOf(t, s, key, acc.ID, usr.ID)
	frameSess := sessionFrom(t, s, redeem(t, s, tok))
	wantFramable(t, serveCookie(t, s, "/files", frameSess.cookie), "https://host.example")
	_, tabTok := signInLinkOf(t, s, key, acc.ID, usr.ID, nil)
	tabSess := sessionFrom(t, s, redeem(t, s, tabTok))
	wantNotFramable(t, serveCookie(t, s, "/files", tabSess.cookie))
	wantNotFramable(t, serveRaw(t, s, http.MethodGet, "/files"))

	// An asset keeps the strict policy even for a frame session.
	wantNotFramable(t, serveCookie(t, s, "/assets/missing.js", frameSess.cookie))

	// An expired frame session no longer frames.
	if err := s.store.Write(context.Background(), func(tx *store.Tx) error {
		past := time.Now().Add(-time.Hour)
		return tx.TouchSession(context.Background(), auth.HashToken(frameSess.cookie.Value), past, past)
	}); err != nil {
		t.Fatal(err)
	}
	wantNotFramable(t, serveCookie(t, s, "/files", frameSess.cookie))

	// A deleted account's sign-in page no longer frames.
	if err := s.store.Write(context.Background(), func(tx *store.Tx) error { return tx.DeleteAccount(context.Background(), acc.ID) }); err != nil {
		t.Fatal(err)
	}
	wantNotFramable(t, serveRaw(t, s, http.MethodGet, "/sign-in?frame="+acc.ID))
}

// TestEmbedPageHeadersPerAccount: one account's origins never reach another account's page (decision 197).
func TestEmbedPageHeadersPerAccount(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.PublicURL = "https://drive.example.com" })
	admin := adminKey(t, s)
	a, keyA := newAccount(t, s, admin, "A")
	b, keyB := newAccount(t, s, admin, "B")
	usrA, _ := verifiedUser(t, s, keyA, a.ID, "a@example.com")
	call(t, s, "PUT", "/api/v1/accounts/"+a.ID+"/embed", keyA, embedBody{Origins: []string{"https://a.example"}})
	call(t, s, "PUT", "/api/v1/accounts/"+b.ID+"/embed", keyB, embedBody{Origins: []string{"https://b.example"}})

	rec := serveRaw(t, s, http.MethodGet, "/sign-in?frame="+b.ID)
	wantFramable(t, rec, "https://b.example")
	if strings.Contains(rec.Header().Get("Content-Security-Policy"), "https://a.example") {
		t.Error("account A's origin leaked onto account B's page")
	}

	_, tok := frameLinkOf(t, s, keyA, a.ID, usrA.ID)
	sessA := sessionFrom(t, s, redeem(t, s, tok))
	rec = serveCookie(t, s, "/files", sessA.cookie)
	wantFramable(t, rec, "https://a.example")
	if strings.Contains(rec.Header().Get("Content-Security-Policy"), "https://b.example") {
		t.Error("account B's origin leaked onto account A's page")
	}
}

// TestEmbedFrameAncestorsHasNoSeparators: an account's list, built from the accepted origin forms, can never
// put a CSP directive separator (;), a policy separator (,) or a quote into frame-ancestors. The directive
// is exactly 'self' followed by the space-joined, normalised, sorted origins (decision 197).
func TestEmbedFrameAncestorsHasNoSeparators(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.PublicURL = "https://drive.example.com" })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	in := []string{"https://xn--9ca.example", "https://b.example:8443", "http://localhost:3000/", "https://A.example"}
	call(t, s, "PUT", "/api/v1/accounts/"+acc.ID+"/embed", key, embedBody{Origins: in})
	want := []string{"http://localhost:3000", "https://a.example", "https://b.example:8443", "https://xn--9ca.example"}

	rec := serveRaw(t, s, http.MethodGet, "/sign-in?frame="+acc.ID)
	csp := rec.Header().Get("Content-Security-Policy")
	directive := ""
	for _, part := range strings.Split(csp, ";") {
		if part = strings.TrimSpace(part); strings.HasPrefix(part, "frame-ancestors ") {
			directive = strings.TrimPrefix(part, "frame-ancestors ")
		}
	}
	if exact := "'self' " + strings.Join(want, " "); directive != exact {
		t.Fatalf("frame-ancestors = %q; want exactly %q", directive, exact)
	}
	origins := strings.TrimPrefix(directive, "'self' ")
	for _, bad := range []string{";", ",", "\"", "'"} {
		if strings.Contains(origins, bad) {
			t.Errorf("frame-ancestors origins %q hold %q", origins, bad)
		}
	}
	if xfo := rec.Header().Get("X-Frame-Options"); xfo != "" {
		t.Errorf("X-Frame-Options = %q; want none when framable", xfo)
	}
}

// TestEmbedSignInPageFollowsTemplate: the sign-in page a frame link names is the path of
// users.sign_in_url_template, not a literal /sign-in. A custom template path frames; the old path does not
// (decision 197).
func TestEmbedSignInPageFollowsTemplate(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) {
		c.PublicURL = "https://drive.example.com"
		c.Users.SignInURLTemplate = "{public_url}/welcome#token={token}"
	})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	call(t, s, "PUT", "/api/v1/accounts/"+acc.ID+"/embed", key, embedBody{Origins: []string{"https://host.example"}})

	wantFramable(t, serveRaw(t, s, http.MethodGet, "/welcome?frame="+acc.ID), "https://host.example")
	wantNotFramable(t, serveRaw(t, s, http.MethodGet, "/sign-in?frame="+acc.ID))
}

// TestEmbedSignInPageForeignTemplateHost: when the template's host is not this installation's public URL,
// the sign-in page case never applies here; a frame session's own pages still frame (decision 197).
func TestEmbedSignInPageForeignTemplateHost(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) {
		c.PublicURL = "https://drive.example.com"
		c.Users.SignInURLTemplate = "https://other.example.com/sign-in#token={token}"
	})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "member@example.com")
	call(t, s, "PUT", "/api/v1/accounts/"+acc.ID+"/embed", key, embedBody{Origins: []string{"https://host.example"}})

	wantNotFramable(t, serveRaw(t, s, http.MethodGet, "/sign-in?frame="+acc.ID))
	rec := call(t, s, "POST", "/api/v1/accounts/"+acc.ID+"/users/"+usr.ID+"/sign-in-links", key, map[string]any{"mode": "frame"})
	wantStatus(t, rec, 201)
	_, tok, ok := strings.Cut(decode[signInLinkJSON](t, rec).URL, "#token=")
	if !ok {
		t.Fatal("the frame link has no #token= fragment")
	}
	sess := sessionFrom(t, s, redeem(t, s, tok))
	wantFramable(t, serveCookie(t, s, "/files", sess.cookie), "https://host.example")
}
