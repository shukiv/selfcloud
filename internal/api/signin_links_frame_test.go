// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
)

// frameLinkOf makes a frame sign-in link and returns its URL and the token in its fragment.
func frameLinkOf(t *testing.T, s *Server, key, accID, usr string) (string, string) {
	t.Helper()
	rec := call(t, s, "POST", "/api/v1/accounts/"+accID+"/users/"+usr+"/sign-in-links", key, map[string]any{"mode": "frame"})
	wantStatus(t, rec, 201)
	out := decode[signInLinkJSON](t, rec)
	base, frag, ok := strings.Cut(out.URL, "#token=")
	if !ok {
		t.Fatalf("frame url %q has no #token= fragment", out.URL)
	}
	if !strings.HasPrefix(base, s.cfg.PublicURL+"/sign-in?frame="+accID) {
		t.Fatalf("frame url %q does not name the account before the fragment", out.URL)
	}
	if !auth.ValidToken(frag) {
		t.Fatalf("fragment carries %q, not a bare token", frag)
	}
	return out.URL, frag
}

// sessionCookieOf returns the session cookie from a response.
func sessionCookieOf(t *testing.T, s *Server, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == s.sessionCookie {
			return c
		}
	}
	t.Fatalf("no session cookie %q in %v", s.sessionCookie, rec.Header())
	return nil
}

// TestSignInLinkTabUnchanged: a link with no mode, or mode tab, is the link as before: the token alone
// in the fragment, no frame query (decision 197).
func TestSignInLinkTabUnchanged(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "member@example.com")
	for _, body := range []map[string]any{nil, {"mode": "tab"}} {
		url, _ := signInLinkOf(t, s, key, acc.ID, usr.ID, body)
		if strings.Contains(url, "frame=") {
			t.Errorf("tab link %q carries a frame query", url)
		}
	}
}

// TestSignInLinkFrameRefused: a frame link needs allowed origins and an https public address (localhost
// excepted), and mode must be tab or frame (decision 197).
func TestSignInLinkFrameRefused(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "member@example.com")
	create := "/api/v1/accounts/" + acc.ID + "/users/" + usr.ID + "/sign-in-links"

	// No origins anywhere.
	wantError(t, call(t, s, "POST", create, key, map[string]any{"mode": "frame"}), 400, apierr.InvalidRequest)
	// An unknown mode.
	wantError(t, call(t, s, "POST", create, key, map[string]any{"mode": "bogus"}), 400, apierr.InvalidRequest)

	// Origins, but a plain-http public address that is not localhost.
	s2 := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.PublicURL = "http://drive.example.com" })
	admin2 := adminKey(t, s2)
	acc2, key2 := newAccount(t, s2, admin2, "Acme")
	usr2, _ := verifiedUser(t, s2, key2, acc2.ID, "member@example.com")
	call(t, s2, "PUT", "/api/v1/accounts/"+acc2.ID+"/embed", key2, embedBody{Origins: []string{"https://host.example"}})
	wantError(t, call(t, s2, "POST", "/api/v1/accounts/"+acc2.ID+"/users/"+usr2.ID+"/sign-in-links", key2,
		map[string]any{"mode": "frame"}), 400, apierr.InvalidRequest)
}

// TestSignInLinkFrameURL: a frame link names the account in a query before the fragment, and the
// installation's own origins suffice (decision 197).
func TestSignInLinkFrameURL(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) {
		c.PublicURL = "https://drive.example.com"
		c.Embed.Origins = []string{"https://install.example"}
	})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "member@example.com")
	// The installation list alone allows a frame link.
	frameLinkOf(t, s, key, acc.ID, usr.ID)
	// An account list works too.
	call(t, s, "PUT", "/api/v1/accounts/"+acc.ID+"/embed", key, embedBody{Origins: []string{"https://account.example"}})
	frameLinkOf(t, s, key, acc.ID, usr.ID)
}

// TestRedeemFrameSession: redeeming a frame link starts a frame session whose cookie is partitioned,
// Secure and SameSite=None; a tab link's cookie is unchanged; /me reports the frame (decision 197).
func TestRedeemFrameSession(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.PublicURL = "https://drive.example.com" })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "member@example.com")
	call(t, s, "PUT", "/api/v1/accounts/"+acc.ID+"/embed", key, embedBody{Origins: []string{"https://host.example"}})

	_, frameTok := frameLinkOf(t, s, key, acc.ID, usr.ID)
	rec := redeem(t, s, frameTok)
	wantStatus(t, rec, 200)
	c := sessionCookieOf(t, s, rec)
	if c.SameSite != http.SameSiteNoneMode || !c.Secure || !c.Partitioned {
		t.Fatalf("frame cookie = %+v; want SameSite=None, Secure, Partitioned", c)
	}
	sess, err := s.store.SessionByHash(context.Background(), auth.HashToken(c.Value))
	if err != nil || !sess.Frame {
		t.Fatalf("frame session = %+v, %v; want Frame", sess, err)
	}
	frameSess := sessionFrom(t, s, rec)
	me := decode[meResponse](t, callSession(t, s, frameSess, "GET", "/api/v1/me", nil))
	if !me.Frame {
		t.Error("me.frame is false for a frame session")
	}

	// A tab link's cookie is the ordinary one, and its session is not a frame.
	_, tabTok := signInLinkOf(t, s, key, acc.ID, usr.ID, nil)
	rec = redeem(t, s, tabTok)
	wantStatus(t, rec, 200)
	c = sessionCookieOf(t, s, rec)
	if c.SameSite != http.SameSiteLaxMode || c.Partitioned {
		t.Fatalf("tab cookie = %+v; want SameSite=Lax, not partitioned", c)
	}
	sess, err = s.store.SessionByHash(context.Background(), auth.HashToken(c.Value))
	if err != nil || sess.Frame {
		t.Fatalf("tab session = %+v, %v; want no Frame", sess, err)
	}
	tabSess := sessionFrom(t, s, rec)
	me = decode[meResponse](t, callSession(t, s, tabSess, "GET", "/api/v1/me", nil))
	if me.Frame {
		t.Error("me.frame is true for a tab session")
	}
}

// TestFrameSignOutClearsPartitioned: signing out of a frame session clears the partitioned cookie
// (decision 197).
func TestFrameSignOutClearsPartitioned(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.PublicURL = "https://drive.example.com" })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "member@example.com")
	call(t, s, "PUT", "/api/v1/accounts/"+acc.ID+"/embed", key, embedBody{Origins: []string{"https://host.example"}})
	_, tok := frameLinkOf(t, s, key, acc.ID, usr.ID)
	sess := sessionFrom(t, s, redeem(t, s, tok))
	rec := callSession(t, s, sess, "POST", "/api/v1/auth/logout", nil)
	wantStatus(t, rec, 204)
	c := sessionCookieOf(t, s, rec)
	if c.MaxAge >= 0 || c.SameSite != http.SameSiteNoneMode || !c.Secure || !c.Partitioned {
		t.Fatalf("frame sign-out cookie = %+v; want an expired partitioned clear", c)
	}
}

// TestFrameLocalhostHTTP: on a localhost http address a frame link is allowed, and its cookie is still
// Secure and partitioned (decision 197).
func TestFrameLocalhostHTTP(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}) // public_url defaults to http://localhost:8080
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "member@example.com")
	call(t, s, "PUT", "/api/v1/accounts/"+acc.ID+"/embed", key, embedBody{Origins: []string{"http://localhost:3000"}})
	_, tok := frameLinkOf(t, s, key, acc.ID, usr.ID)
	rec := redeem(t, s, tok)
	wantStatus(t, rec, 200)
	c := sessionCookieOf(t, s, rec)
	if c.SameSite != http.SameSiteNoneMode || !c.Secure || !c.Partitioned {
		t.Fatalf("localhost frame cookie = %+v; want SameSite=None, Secure, Partitioned", c)
	}
}
