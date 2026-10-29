// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"

	_ "modernc.org/sqlite" // the driver the store uses; the tests set rows directly
)

// anyoneFixture is the link fixture with the account's switch for links that anyone can open turned on.
type anyoneFixture struct {
	*linkFixture
}

func newAnyoneFixture(t *testing.T) *anyoneFixture {
	t.Helper()
	fx := newLinkFixture(t)
	setPolicy(t, fx.s, fx.key, fx.acc.ID, true)
	return &anyoneFixture{linkFixture: fx}
}

func setPolicy(t *testing.T, s *Server, key, accID string, on bool) {
	t.Helper()
	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+accID+"/sharing-policy", key,
		map[string]any{"public_links": on}), 200)
}

// makeAnyone makes an anyone link on nodeID as the account key, with extra fields merged into the body. It
// returns the link and its secret.
func (fx *anyoneFixture) makeAnyone(t *testing.T, nodeID string, extra map[string]any) (shareLinkJSON, string) {
	t.Helper()
	body := map[string]any{"audience": "anyone"}
	for k, v := range extra {
		body[k] = v
	}
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+nodeID+"/share-links", fx.key, body)
	wantStatus(t, rec, 201)
	out := decode[shareLinkCreated](t, rec)
	return out.Link, secretOf(t, out.URL)
}

// visitAnyone opens an anyone link without a session, with the password when one is given, and wants it to work.
// It returns the visitor's credential.
func (fx *anyoneFixture) visitAnyone(t *testing.T, secret, password string) string {
	t.Helper()
	tok, rec := fx.tryAnyone(t, secret, password)
	wantStatus(t, rec, 200)
	return tok
}

// tryAnyone opens an anyone link without a session, and returns the credential the answer carries, if any.
func (fx *anyoneFixture) tryAnyone(t *testing.T, secret, password string) (string, *httptest.ResponseRecorder) {
	t.Helper()
	body := map[string]any{"secret": secret}
	if password != "" {
		body["password"] = password
	}
	rec := call(t, fx.s, "POST", "/api/v1/share/open", "", body)
	if rec.Code != http.StatusOK {
		return "", rec
	}
	return decode[struct {
		Token string `json:"token"`
	}](t, rec).Token, rec
}

// execDB runs one statement against the test database, for the changes a test cannot make through the API.
func (fx *anyoneFixture) execDB(t *testing.T, query string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", fx.s.cfg.Store.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

// queryString reads one text value from the test database.
func (fx *anyoneFixture) queryString(t *testing.T, query string, args ...any) string {
	t.Helper()
	db, err := sql.Open("sqlite", fx.s.cfg.Store.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v sql.NullString
	if err := db.QueryRow(query, args...).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v.String
}

// visitor is the credential's call: a GET of path with the bearer.
func (fx *anyoneFixture) visitor(t *testing.T, tok, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	return call(t, fx.s, method, path, tok, nil)
}

// deadVisitor wants the one answer a dead credential gets: not_found.
func deadVisitor(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	wantError(t, rec, 404, apierr.NotFound)
}

func TestAnyoneNeedsManagerAndTheSwitch(t *testing.T) {
	fx := newLinkFixture(t)
	// The account's switch is off by default: a manager is refused.
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.b.ID+"/share-links", fx.key, map[string]any{"audience": "anyone"})
	if e := wantError(t, rec, 403, apierr.Forbidden); e.Details["reason"] != "public_links_off" {
		t.Errorf("reason = %v, want public_links_off", e.Details["reason"])
	}
	setPolicy(t, fx.s, fx.key, fx.acc.ID, true)
	// A viewer does not share, and an editor with editors_can_share is refused: only a manager may.
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	fx.setPerm(t, fx.b.ID, fx.bob.ID, "editor")
	fx.flag(t, fx.b.ID, map[string]any{"editors_can_share": true})
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.b.ID+"/share-links",
		map[string]any{"audience": "anyone"}), 403, apierr.Forbidden)
	rec = callSession(t, fx.s, fx.bobSess, "POST", "/api/v1/nodes/"+fx.b.ID+"/share-links", map[string]any{"audience": "anyone"})
	if e := wantError(t, rec, 403, apierr.Forbidden); e.Details["reason"] != "manager_required" {
		t.Errorf("editor: reason = %v, want manager_required", e.Details["reason"])
	}
	// A delegated token is refused.
	tok := mintToken(t, fx.s, fx.key, fx.acc.ID, fx.fld.ID, "")
	wantError(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.b.ID+"/share-links", tok,
		map[string]any{"audience": "anyone"}), 403, apierr.Forbidden)
	// A link-only editor does not count: its role came from an organisation link it opened.
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	_, url := fx.makeLink(t, fx.b.ID, "editor")
	fx.openedAs(t, fx.annSess, secretOf(t, url))
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.b.ID+"/share-links",
		map[string]any{"audience": "anyone"}), 403, apierr.Forbidden)
	// A manager of another node makes one; only the creator or a manager may delete it.
	fx.setPerm(t, fx.c.ID, fx.bob.ID, "manager")
	afx := &anyoneFixture{linkFixture: fx}
	anyone, _ := afx.makeAnyone(t, fx.c.ID, nil)
	wantStatus(t, callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/nodes/"+fx.c.ID+"/share-links", nil), 200)
	wantError(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/share-links/"+anyone.ID, nil), 404, apierr.NotFound)
	wantStatus(t, callSession(t, fx.s, fx.bobSess, "DELETE", "/api/v1/share-links/"+anyone.ID, nil), 204)
}

func TestAnyoneLinkIsViewerOnlyAndEnds(t *testing.T) {
	fx := newAnyoneFixture(t)
	for _, extra := range []map[string]any{
		{"role": "editor"},
		{"role": "contributor"},
		{"password": "short"},
		{"expires_at": time.Now().Add(-time.Hour)},
		{"expires_at": time.Now().Add(400 * 24 * time.Hour)},
	} {
		body := map[string]any{"audience": "anyone"}
		for k, v := range extra {
			body[k] = v
		}
		rec := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.b.ID+"/share-links", fx.key, body)
		wantError(t, rec, 400, apierr.InvalidRequest)
	}
	// Without an end date the link ends in 30 days.
	link, _ := fx.makeAnyone(t, fx.b.ID, nil)
	if link.Role != "viewer" || link.ExpiresAt == nil {
		t.Fatalf("link = %+v, want viewer with an end date", link)
	}
	if d := time.Until(*link.ExpiresAt); d < 29*24*time.Hour || d > 30*24*time.Hour+time.Minute {
		t.Errorf("default end is %v away, want about 30 days", d)
	}
	// A PATCH cannot clear the end of an anyone link, nor give it a role.
	wantError(t, call(t, fx.s, "PATCH", "/api/v1/share-links/"+link.ID, fx.key,
		map[string]any{"expires_at": nil}), 400, apierr.InvalidRequest)
	wantError(t, call(t, fx.s, "PATCH", "/api/v1/share-links/"+link.ID, fx.key,
		map[string]any{"role": "editor"}), 400, apierr.InvalidRequest)
	// A password of the wrong length is refused; an organisation link has no password.
	wantError(t, call(t, fx.s, "PATCH", "/api/v1/share-links/"+link.ID, fx.key,
		map[string]any{"password": "short"}), 400, apierr.InvalidRequest)
	wantError(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.sib.ID+"/share-links", fx.key,
		map[string]any{"audience": "organisation", "password": "long-enough-pw"}), 400, apierr.InvalidRequest)
}

func TestAnyonePasswordIsHashedAndNeverShown(t *testing.T) {
	fx := newAnyoneFixture(t)
	const password = "correct horse battery"
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.b.ID+"/share-links", fx.key,
		map[string]any{"audience": "anyone", "password": password})
	wantStatus(t, rec, 201)
	if strings.Contains(rec.Body.String(), password) {
		t.Error("the create answer shows the password")
	}
	out := decode[shareLinkCreated](t, rec)
	if out.Link.PasswordSet == nil || !*out.Link.PasswordSet {
		t.Errorf("password_set = %v, want true", out.Link.PasswordSet)
	}
	stored := fx.queryString(t, "SELECT password_hash FROM share_links WHERE id = ?", out.Link.ID)
	if !strings.HasPrefix(stored, "$argon2id$") || strings.Contains(stored, password) {
		t.Errorf("stored password = %q, want an argon2id hash", stored)
	}
	// Listings show that a password is set, never the password.
	list := call(t, fx.s, "GET", "/api/v1/nodes/"+fx.b.ID+"/share-links", fx.key, nil)
	wantStatus(t, list, 200)
	if strings.Contains(list.Body.String(), password) || strings.Contains(list.Body.String(), stored) {
		t.Error("the listing shows the password or its hash")
	}
	// An empty password removes it.
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/share-links/"+out.Link.ID, fx.key,
		map[string]any{"password": ""}), 200)
	if got := fx.queryString(t, "SELECT COALESCE(password_hash, '') FROM share_links WHERE id = ?", out.Link.ID); got != "" {
		t.Errorf("password still stored after a removal: %q", got)
	}
}

func TestOpenPasswordFlowAndWrongPasswords(t *testing.T) {
	fx := newAnyoneFixture(t)
	const password = "correct horse battery"
	_, secret := fx.makeAnyone(t, fx.b.ID, map[string]any{"password": password})
	// No password: the answer says one is needed, and that is not a wrong guess.
	_, rec := fx.tryAnyone(t, secret, "")
	if e := wantError(t, rec, 401, apierr.Unauthenticated); e.Details["reason"] != "password_required" {
		t.Errorf("reason = %v, want password_required", e.Details["reason"])
	}
	_, rec = fx.tryAnyone(t, secret, "a wrong guess")
	if e := wantError(t, rec, 401, apierr.Unauthenticated); e.Details["reason"] != "password_wrong" {
		t.Errorf("reason = %v, want password_wrong", e.Details["reason"])
	}
	tok, rec := fx.tryAnyone(t, secret, password)
	wantStatus(t, rec, 200)
	if tok == "" || !strings.HasPrefix(tok, auth.SharePrefix) {
		t.Errorf("token = %q, want a scl_ credential", tok)
	}
	if !strings.Contains(rec.Body.String(), `"node":"`+fx.b.ID+`"`) {
		t.Errorf("answer does not name the node: %s", rec.Body.String())
	}
	wantStatus(t, fx.visitor(t, tok, "GET", "/api/v1/nodes/"+fx.b.ID), 200)
}

func TestWrongPasswordLocksAnAddressOutOfTheLink(t *testing.T) {
	fx := newAnyoneFixture(t)
	const password = "correct horse battery"
	_, secret := fx.makeAnyone(t, fx.b.ID, map[string]any{"password": password})
	// Ten wrong guesses from one address lock it out of the link; the tenth answers rate_limited.
	for i := 0; i < passwordIPMax-1; i++ {
		_, rec := fx.tryAnyone(t, secret, "guess number")
		wantError(t, rec, 401, apierr.Unauthenticated)
	}
	_, rec := fx.tryAnyone(t, secret, "guess number")
	wantError(t, rec, 429, apierr.RateLimited)
	// While locked, even the right password is refused, from that address.
	_, rec = fx.tryAnyone(t, secret, password)
	wantError(t, rec, 429, apierr.RateLimited)
	// Another address is not locked.
	req := jsonRequest(t, "POST", "/api/v1/share/open", map[string]any{"secret": secret, "password": password})
	req.RemoteAddr = "203.0.113.9:4000"
	other := httptest.NewRecorder()
	fx.s.Handler().ServeHTTP(other, req)
	wantStatus(t, other, 200)
	// The lock is audited once.
	throttled := 0
	for _, e := range fx.auditSeenBy(t, "") {
		if e.Action == "link.throttled" {
			throttled++
			if e.ActorKind != string(auth.KindShareLink) {
				t.Errorf("throttled actor = %q, want share_link", e.ActorKind)
			}
		}
	}
	if throttled != 1 {
		t.Errorf("link.throttled entries = %d, want 1", throttled)
	}
}

func TestWrongPasswordsSlowTheLinkWithoutRefusingIt(t *testing.T) {
	fx := newAnyoneFixture(t)
	const password = "correct horse battery"
	link, secret := fx.makeAnyone(t, fx.b.ID, map[string]any{"password": password})
	// Thirty wrong guesses in the hour spend the link's budget.
	now := time.Now()
	fx.s.guard.mu.Lock()
	for i := 0; i < passwordLinkBudget; i++ {
		fx.s.guard.linkFails[link.ID] = append(fx.s.guard.linkFails[link.ID], now)
	}
	fx.s.guard.mu.Unlock()
	// The right password still opens the link, after the delay.
	start := time.Now()
	fx.visitAnyone(t, secret, password)
	if d := time.Since(start); d < passwordSlowdown {
		t.Errorf("attempt took %v, want at least %v", d, passwordSlowdown)
	}
}

func TestOpenNeedsNoSessionAndDeadLinksAreOneAnswer(t *testing.T) {
	fx := newAnyoneFixture(t)
	link, secret := fx.makeAnyone(t, fx.b.ID, nil)
	// A signed-in member of the account may open it too, and a stranger too: no session is needed.
	fx.visitAnyone(t, secret, "")
	// Expired.
	fx.setExpiry(t, link.ID, time.Now().Add(-time.Minute))
	_, rec := fx.tryAnyone(t, secret, "")
	deadVisitor(t, rec)
	// Unknown and malformed secrets look the same.
	_, rec = fx.tryAnyone(t, strings.Repeat("A", 43), "")
	deadVisitor(t, rec)
	_, rec = fx.tryAnyone(t, "not-a-secret", "")
	deadVisitor(t, rec)
	// An installation admin's or an account key's header is refused as dead, as for organisation links.
	deadLink(t, call(t, fx.s, "POST", "/api/v1/share/open", fx.key, map[string]any{"secret": secret}))
}

func TestVisitorCredentialDiesWithTheLinkState(t *testing.T) {
	cases := []struct {
		name string
		kill func(t *testing.T, fx *anyoneFixture, link shareLinkJSON, secret string)
	}{
		{"reset", func(t *testing.T, fx *anyoneFixture, link shareLinkJSON, _ string) {
			wantStatus(t, call(t, fx.s, "POST", "/api/v1/share-links/"+link.ID+"/reset", fx.key, nil), 201)
		}},
		{"deleted", func(t *testing.T, fx *anyoneFixture, link shareLinkJSON, _ string) {
			wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/share-links/"+link.ID, fx.key, nil), 204)
		}},
		{"switch off", func(t *testing.T, fx *anyoneFixture, _ shareLinkJSON, _ string) {
			setPolicy(t, fx.s, fx.key, fx.acc.ID, false)
		}},
		{"node trashed", func(t *testing.T, fx *anyoneFixture, _ shareLinkJSON, _ string) {
			wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.b.ID, fx.key, nil), 204)
		}},
		{"expired", func(t *testing.T, fx *anyoneFixture, link shareLinkJSON, _ string) {
			fx.setExpiry(t, link.ID, time.Now().Add(-time.Minute))
		}},
		{"team links off", func(t *testing.T, fx *anyoneFixture, _ shareLinkJSON, _ string) {
			fx.settings(t, fx.fld.ID, map[string]any{"allow_links": false})
		}},
		{"members only below the root", func(t *testing.T, fx *anyoneFixture, _ shareLinkJSON, _ string) {
			fx.settings(t, fx.fld.ID, map[string]any{"members_only": true})
		}},
		{"password changed", func(t *testing.T, fx *anyoneFixture, link shareLinkJSON, _ string) {
			wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/share-links/"+link.ID, fx.key,
				map[string]any{"password": "another password"}), 200)
		}},
		{"account suspended", func(t *testing.T, fx *anyoneFixture, _ shareLinkJSON, _ string) {
			fx.execDB(t, "UPDATE accounts SET status = 'suspended' WHERE id = ?", fx.acc.ID)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newAnyoneFixture(t)
			link, secret := fx.makeAnyone(t, fx.b.ID, nil)
			tok := fx.visitAnyone(t, secret, "")
			wantStatus(t, fx.visitor(t, tok, "GET", "/api/v1/nodes/"+fx.deep.ID), 200)
			tc.kill(t, fx, link, secret)
			deadVisitor(t, fx.visitor(t, tok, "GET", "/api/v1/nodes/"+fx.deep.ID))
			deadVisitor(t, fx.visitor(t, tok, "POST", "/api/v1/share/renew"))
			_, rec := fx.tryAnyone(t, secret, "")
			if tc.name == "password changed" {
				// The new password is needed; without it the answer is the password's, not a dead link.
				wantError(t, rec, 401, apierr.Unauthenticated)
			} else {
				deadVisitor(t, rec)
			}
		})
	}
}

func TestSwitchingPolicyOffAndOnAtUse(t *testing.T) {
	fx := newAnyoneFixture(t)
	_, secret := fx.makeAnyone(t, fx.b.ID, nil)
	tok := fx.visitAnyone(t, secret, "")
	setPolicy(t, fx.s, fx.key, fx.acc.ID, false)
	deadVisitor(t, fx.visitor(t, tok, "GET", "/api/v1/nodes/"+fx.deep.ID))
	_, rec := fx.tryAnyone(t, secret, "")
	deadVisitor(t, rec)
	// The listing shows the link as paused, and a new one is refused.
	list := decode[listResponse[shareLinkJSON]](t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.b.ID+"/share-links", fx.key, nil))
	if len(list.Items) != 1 || list.Items[0].Paused == nil || !*list.Items[0].Paused {
		t.Errorf("listing = %+v, want one paused link", list.Items)
	}
	wantError(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.sib.ID+"/share-links", fx.key,
		map[string]any{"audience": "anyone"}), 403, apierr.Forbidden)
	// Turned back on, the credential works again: the switch is read at each use.
	setPolicy(t, fx.s, fx.key, fx.acc.ID, true)
	wantStatus(t, fx.visitor(t, tok, "GET", "/api/v1/nodes/"+fx.deep.ID), 200)
}

func TestMyFilesNeedTheSwitchForAnyoneOnly(t *testing.T) {
	fx := newAnyoneFixture(t)
	setPolicy(t, fx.s, fx.key, fx.acc.ID, false)
	pf := fx.personalFolder(t, fx.annSess)
	file := fx.sessionFile(t, fx.annSess, pf, "/mine.txt", "mine")
	// An organisation link in My files needs no switch.
	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+file.ID+"/share-links",
		map[string]any{"audience": "organisation"}), 201)
	// An anyone link does.
	rec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+file.ID+"/share-links", map[string]any{"audience": "anyone"})
	if e := wantError(t, rec, 403, apierr.Forbidden); e.Details["reason"] != "public_links_off" {
		t.Errorf("reason = %v, want public_links_off", e.Details["reason"])
	}
}

func TestVisitorStaysInTheLinkSubtree(t *testing.T) {
	fx := newAnyoneFixture(t)
	_, secret := fx.makeAnyone(t, fx.b.ID, nil)
	tok := fx.visitAnyone(t, secret, "")
	// The link's node and what is below it.
	stat := decode[nodeJSON](t, fx.visitor(t, tok, "GET", "/api/v1/nodes/"+fx.b.ID))
	if stat.ParentID != nil || stat.CreatedBy != "" || stat.OwnerID != "" || stat.Role != "viewer" {
		t.Errorf("link root = %+v, want no parent, no creator or owner, role viewer", stat)
	}
	wantStatus(t, fx.visitor(t, tok, "GET", "/api/v1/nodes/"+fx.deep.ID), 200)
	kids := decode[listResponse[nodeJSON]](t, fx.visitor(t, tok, "GET", "/api/v1/nodes/"+fx.b.ID+"/children"))
	if len(kids.Items) != 1 || kids.Items[0].ID != fx.deep.ID {
		t.Errorf("children = %+v, want the file below the link", kids.Items)
	}
	// Above the link, beside it, and the parent itself: not_found, the same as a missing node.
	deadVisitor(t, fx.visitor(t, tok, "GET", "/api/v1/nodes/"+fx.a.ID))
	deadVisitor(t, fx.visitor(t, tok, "GET", "/api/v1/nodes/"+fx.sib.ID))
	deadVisitor(t, fx.visitor(t, tok, "GET", "/api/v1/nodes/"+fx.a.ID+"/children"))
	// A signed link to a file below is capped at the credential's life; one outside is not_found.
	rec := fx.visitor(t, tok, "POST", "/api/v1/nodes/"+fx.deep.ID+"/links")
	wantStatus(t, rec, 201)
	made := decode[linkJSON](t, rec)
	if made.ExpiresAt.After(time.Now().Add(shareCredentialTTL + time.Minute)) {
		t.Errorf("signed link lives %v, want at most the credential's 15 minutes", time.Until(made.ExpiresAt))
	}
	deadVisitor(t, fx.visitor(t, tok, "POST", "/api/v1/nodes/"+fx.sib.ID+"/links"))
	// No audit entry is made for a visitor's signed link.
	for _, e := range fx.auditSeenBy(t, "") {
		if e.Action == "node.link" && e.ActorKind == string(auth.KindShareLink) {
			t.Error("a visitor's signed link was audited")
		}
	}
}

func TestVisitorIsCutOffFromEveryRouteButTheLinkedOnes(t *testing.T) {
	fx := newAnyoneFixture(t)
	_, secret := fx.makeAnyone(t, fx.b.ID, nil)
	tok := fx.visitAnyone(t, secret, "")
	allowed := []string{
		"GET /api/v1/nodes/{nod}",
		"GET /api/v1/nodes/{nod}/children",
		"GET /api/v1/nodes/{nod}/content",
		"HEAD /api/v1/nodes/{nod}/content",
		"POST /api/v1/nodes/{nod}/links",
		"POST /api/v1/share/renew",
	}
	placeholder := regexp.MustCompile(`\{[a-z]+\}`)
	var linked []string
	for _, rt := range fx.s.routes() {
		if !rt.auth && !rt.link {
			continue
		}
		key := rt.method + " " + rt.path
		if rt.link {
			linked = append(linked, key)
		}
		path := placeholder.ReplaceAllString(rt.path, "x")
		rec := call(t, fx.s, rt.method, path, tok, map[string]any{})
		if rt.link {
			if strings.Contains(rec.Body.String(), `"reason":"link_route"`) {
				t.Errorf("%s is refused a visitor, but it is on the allowlist", key)
			}
			continue
		}
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"reason":"link_route"`) {
			t.Errorf("%s answered %d to a visitor, want 403 link_route: %s", key, rec.Code, rec.Body.String())
		}
	}
	slices.Sort(linked)
	want := slices.Clone(allowed)
	slices.Sort(want)
	if !slices.Equal(linked, want) {
		t.Errorf("the allowlist is\n  %v\nwant\n  %v", linked, want)
	}
}

func TestVisitorDownloadRulesApply(t *testing.T) {
	fx := newAnyoneFixture(t)
	fx.flag(t, fx.deep.ID, map[string]any{"no_download": true})
	_, secret := fx.makeAnyone(t, fx.b.ID, nil)
	tok := fx.visitAnyone(t, secret, "")
	wantError(t, fx.visitor(t, tok, "GET", "/api/v1/nodes/"+fx.deep.ID+"/content"), 403, apierr.DownloadRestricted)
	wantError(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.deep.ID+"/links", tok,
		map[string]any{"disposition": "attachment"}), 403, apierr.DownloadRestricted)
	wantStatus(t, fx.visitor(t, tok, "GET", "/api/v1/nodes/"+fx.deep.ID+"/content?disposition=inline"), 200)
}

func TestVisitorCredentialForgedExpiredOrOfAnotherLinkIsRefused(t *testing.T) {
	fx := newAnyoneFixture(t)
	_, secret := fx.makeAnyone(t, fx.b.ID, nil)
	tok := fx.visitAnyone(t, secret, "")
	// A changed MAC, and a malformed credential.
	wantError(t, fx.visitor(t, flipMAC(tok), "GET", "/api/v1/nodes/"+fx.deep.ID), 401, apierr.Unauthenticated)
	wantError(t, fx.visitor(t, auth.SharePrefix+"garbage", "GET", "/api/v1/nodes/"+fx.deep.ID), 401, apierr.Unauthenticated)

	st, err := fx.s.store.ShareLinkStateBySecretHash(t.Context(), auth.HashToken(secret))
	if err != nil {
		t.Fatal(err)
	}
	pl := credentialPayload{Version: shareCredentialVer, KeyID: fx.s.shares.current.kid, AccountID: st.Link.AccountID,
		LinkID: st.Link.ID, NodeID: st.Link.NodeID, IssuedAt: time.Now().Add(-20 * time.Minute).UnixMilli(),
		ExpiresAt: time.Now().Add(-5 * time.Minute).UnixMilli()}
	expired, _ := fx.s.shares.sign(pl)
	wantError(t, fx.visitor(t, expired, "GET", "/api/v1/nodes/"+fx.deep.ID), 401, apierr.Unauthenticated)

	// A credential signed under another key.
	foreign := &shareKeys{current: tokenKey{mac: bytes.Repeat([]byte{7}, 32), kid: "foreign-kid"}}
	live := pl
	live.IssuedAt, live.ExpiresAt = time.Now().UnixMilli(), time.Now().Add(time.Minute).UnixMilli()
	forged, _ := foreign.sign(live)
	wantError(t, fx.visitor(t, forged, "GET", "/api/v1/nodes/"+fx.deep.ID), 401, apierr.Unauthenticated)

	// A credential that names a node other than the link's own: not_found, since the MAC is the server's.
	live.KeyID = fx.s.shares.current.kid
	live.NodeID = fx.sib.ID
	other, _ := fx.s.shares.sign(live)
	deadVisitor(t, fx.visitor(t, other, "GET", "/api/v1/nodes/"+fx.sib.ID))
}

func TestVisitorRenewsWhileLive(t *testing.T) {
	fx := newAnyoneFixture(t)
	_, secret := fx.makeAnyone(t, fx.b.ID, nil)
	tok := fx.visitAnyone(t, secret, "")
	// A renewal a millisecond later than the credential is a new credential; the same millisecond is the same one.
	time.Sleep(2 * time.Millisecond)
	rec := fx.visitor(t, tok, "POST", "/api/v1/share/renew")
	wantStatus(t, rec, 200)
	renewed := decode[struct {
		Token string `json:"token"`
	}](t, rec).Token
	if renewed == tok || !strings.HasPrefix(renewed, auth.SharePrefix) {
		t.Errorf("renewed credential = %q, want a new scl_ credential", renewed)
	}
	wantStatus(t, fx.visitor(t, renewed, "GET", "/api/v1/nodes/"+fx.deep.ID), 200)
	// A signed-in account key is not a visitor: renewal is refused to it.
	wantError(t, call(t, fx.s, "POST", "/api/v1/share/renew", fx.key, nil), 403, apierr.Forbidden)
}

func TestCredentialIssuedBeforeAResetIsRefusedAtOnce(t *testing.T) {
	fx := newAnyoneFixture(t)
	link, secret := fx.makeAnyone(t, fx.b.ID, nil)
	tok := fx.visitAnyone(t, secret, "")
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/share-links/"+link.ID+"/reset", fx.key, nil), 201)
	deadVisitor(t, fx.visitor(t, tok, "GET", "/api/v1/nodes/"+fx.deep.ID))
	// The old secret is dead too.
	_, rec := fx.tryAnyone(t, secret, "")
	deadVisitor(t, rec)
}

func TestAnyoneAuditHoldsNoSecretOrPassword(t *testing.T) {
	fx := newAnyoneFixture(t)
	const password = "correct horse battery"
	_, secret := fx.makeAnyone(t, fx.b.ID, map[string]any{"password": password})
	fx.visitAnyone(t, secret, password)
	_, _ = fx.tryAnyone(t, secret, "a wrong guess")
	entries := fx.auditSeenBy(t, "")
	var raw bytes.Buffer
	for _, e := range entries {
		b, _ := json.Marshal(e)
		raw.Write(b)
	}
	if strings.Contains(raw.String(), secret) || strings.Contains(raw.String(), password) {
		t.Error("the audit log holds the secret or the password")
	}
	opened := false
	for _, e := range entries {
		if e.Action == "link.open" && e.ActorKind == string(auth.KindShareLink) {
			opened = true
		}
	}
	if !opened {
		t.Error("no link.open entry with the share_link actor")
	}
}

func TestAnyoneAndOrganisationSwitch(t *testing.T) {
	fx := newAnyoneFixture(t)
	link, url := fx.makeLink(t, fx.b.ID, "viewer")
	secret := secretOf(t, url)
	fx.openedAs(t, fx.annSess, secret)
	// Switching to anyone keeps the secret, gives viewer, sheds the members' visits and sets an end date.
	rec := call(t, fx.s, "PATCH", "/api/v1/share-links/"+link.ID, fx.key, map[string]any{"audience": "anyone"})
	wantStatus(t, rec, 200)
	changed := decode[shareLinkJSON](t, rec)
	if changed.Audience != "anyone" || changed.ExpiresAt == nil {
		t.Fatalf("after the switch: %+v", changed)
	}
	tok := fx.visitAnyone(t, secret, "")
	wantStatus(t, fx.visitor(t, tok, "GET", "/api/v1/nodes/"+fx.deep.ID), 200)
	wantError(t, fx.stat(t, fx.annSess, fx.deep.ID), 404, apierr.NotFound)
	// Back to organisation: the anyone visitor is refused, and ann opens the link as a member again.
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/share-links/"+link.ID, fx.key, map[string]any{"audience": "organisation"}), 200)
	deadVisitor(t, fx.visitor(t, tok, "GET", "/api/v1/nodes/"+fx.deep.ID))
	fx.openedAs(t, fx.annSess, secret)
	wantStatus(t, fx.stat(t, fx.annSess, fx.deep.ID), 200)
}

func TestMeSaysWhetherPublicLinksAreOn(t *testing.T) {
	fx := newLinkFixture(t)
	me := decode[meResponse](t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/me", nil))
	if me.PublicLinks {
		t.Error("public_links = true before the owner turned it on")
	}
	setPolicy(t, fx.s, fx.key, fx.acc.ID, true)
	me = decode[meResponse](t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/me", nil))
	if !me.PublicLinks {
		t.Error("public_links = false after the owner turned it on")
	}
}

// The store's state read is what every visitor request depends on; its fields must match the schema's.
func TestShareLinkStateReadsTheSwitchAndTheNode(t *testing.T) {
	fx := newAnyoneFixture(t)
	link, _ := fx.makeAnyone(t, fx.b.ID, nil)
	st, err := fx.s.store.ShareLinkStateByID(t.Context(), fx.acc.ID, link.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !st.PublicLinks || st.NodeTrashed || st.NodeIsRoot || st.FolderKind != store.KindTeam || st.Link.Audience != "anyone" {
		t.Errorf("state = %+v", st)
	}
	if st.AccountStatus != store.AccountActive {
		t.Errorf("account status = %q", st.AccountStatus)
	}
}

func TestAnyoneCreateRefusedByTeamPolicy(t *testing.T) {
	fx := newAnyoneFixture(t)
	fx.settings(t, fx.fld.ID, map[string]any{"members_only": true})
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.sib.ID+"/share-links", fx.key, map[string]any{"audience": "anyone"})
	wantError(t, rec, 409, apierr.Conflict)
	fx.settings(t, fx.fld.ID, map[string]any{"members_only": false, "allow_links": false})
	rec = call(t, fx.s, "POST", "/api/v1/nodes/"+fx.sib.ID+"/share-links", fx.key, map[string]any{"audience": "anyone"})
	if e := wantError(t, rec, 403, apierr.Forbidden); e.Details["reason"] != "links_off" {
		t.Errorf("reason = %v, want links_off", e.Details["reason"])
	}
}

func TestOpenIsRateLimitedPerAddress(t *testing.T) {
	fx := newAnyoneFixture(t)
	_, secret := fx.makeAnyone(t, fx.b.ID, nil)
	var last *httptest.ResponseRecorder
	for i := 0; i < 31; i++ {
		_, last = fx.tryAnyone(t, secret, "")
		if i < 30 && last.Code != http.StatusOK {
			t.Fatalf("open %d answered %d, want 200", i+1, last.Code)
		}
	}
	wantError(t, last, 429, apierr.RateLimited)
}

func TestVisitorNoDownloadForViewersApplies(t *testing.T) {
	fx := newAnyoneFixture(t)
	fx.settings(t, fx.fld.ID, map[string]any{"no_download_for_viewers": true})
	_, secret := fx.makeAnyone(t, fx.b.ID, nil)
	tok := fx.visitAnyone(t, secret, "")
	wantError(t, fx.visitor(t, tok, "GET", "/api/v1/nodes/"+fx.deep.ID+"/content"), 403, apierr.DownloadRestricted)
	wantStatus(t, fx.visitor(t, tok, "GET", "/api/v1/nodes/"+fx.deep.ID+"/content?disposition=inline"), 200)
}

// tryFrom opens an anyone link from one client address, with the password given, and returns the answer.
func (fx *anyoneFixture) tryFrom(t *testing.T, addr, secret, password string) *httptest.ResponseRecorder {
	t.Helper()
	body := map[string]any{"secret": secret, "password": password}
	req := jsonRequest(t, "POST", "/api/v1/share/open", body)
	req.RemoteAddr = addr
	rec := httptest.NewRecorder()
	fx.s.Handler().ServeHTTP(rec, req)
	return rec
}

// M4: a switch to anyone keeps no end past the anyone cap.
func TestSwitchToAnyoneCapsAnEarlierEnd(t *testing.T) {
	fx := newLinkFixture(t)
	setPolicy(t, fx.s, fx.key, fx.acc.ID, true)
	link, _ := fx.makeLink(t, fx.b.ID, "viewer")
	fx.setExpiry(t, link.ID, time.Now().Add(10*365*24*time.Hour))
	rec := call(t, fx.s, "PATCH", "/api/v1/share-links/"+link.ID, fx.key, map[string]any{"audience": "anyone"})
	wantStatus(t, rec, 200)
	out := decode[shareLinkJSON](t, rec)
	if out.ExpiresAt == nil || out.ExpiresAt.After(time.Now().Add(anyoneLinkMax+time.Minute)) {
		t.Fatalf("expires_at = %v, want at most the 365-day anyone cap", out.ExpiresAt)
	}
	found := false
	for _, e := range fx.auditSeenBy(t, "") {
		if e.Action == "link.update" && e.Target == link.ID {
			found = true
			if e.Details["expires_at"] == nil {
				t.Errorf("link.update details %v do not record the end", e.Details)
			}
		}
	}
	if !found {
		t.Error("no link.update entry for the switch")
	}
}

// L1: a link under a trashed ancestor is dead before its password is checked.
func TestTrashedAncestorRefusesAnyoneLinkBeforeThePassword(t *testing.T) {
	fx := newAnyoneFixture(t)
	const password = "correct horse battery"
	_, secret := fx.makeAnyone(t, fx.deep.ID, map[string]any{"password": password})
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.b.ID, fx.key, nil), 204)
	_, rec := fx.tryAnyone(t, secret, "a wrong guess")
	deadVisitor(t, rec)
	_, rec = fx.tryAnyone(t, secret, password)
	deadVisitor(t, rec)
}

// M1, L5: one password check per link at a time. With the budget spent, each attempt waits two seconds, so two
// concurrent wrong guesses take two waits in turn, not one.
func TestPasswordChecksOfALinkRunOneAtATime(t *testing.T) {
	fx := newAnyoneFixture(t)
	const password = "correct horse battery"
	link, secret := fx.makeAnyone(t, fx.b.ID, map[string]any{"password": password})
	now := time.Now()
	fx.s.guard.mu.Lock()
	for i := 0; i < passwordLinkBudget; i++ {
		fx.s.guard.linkFails[link.ID] = append(fx.s.guard.linkFails[link.ID], now)
	}
	fx.s.guard.mu.Unlock()
	start := time.Now()
	done := make(chan struct{}, 2)
	for i := 0; i < 2; i++ {
		go func() {
			req := jsonRequest(t, "POST", "/api/v1/share/open", map[string]any{"secret": secret, "password": "a wrong guess"})
			req.RemoteAddr = "198.51.100.7:4000"
			fx.s.Handler().ServeHTTP(httptest.NewRecorder(), req)
			done <- struct{}{}
		}()
	}
	<-done
	<-done
	if d := time.Since(start); d < 3500*time.Millisecond {
		t.Errorf("two concurrent wrong guesses took %v, want them serialised (at least 3.5s)", d)
	}
}

// M1, L5: an address is the /64 of an IPv6 client, so one address cannot shed its lock by changing its low bits.
func TestLockoutCountsAnIPv6PrefixOnce(t *testing.T) {
	fx := newAnyoneFixture(t)
	const password = "correct horse battery"
	_, secret := fx.makeAnyone(t, fx.b.ID, map[string]any{"password": password})
	for i := 1; i < passwordIPMax; i++ {
		rec := fx.tryFrom(t, fmt.Sprintf("[2001:db8:1:1::%x]:4000", i), secret, "a wrong guess")
		wantError(t, rec, 401, apierr.Unauthenticated)
	}
	rec := fx.tryFrom(t, "[2001:db8:1:1::ff]:4000", secret, "a wrong guess")
	wantError(t, rec, 429, apierr.RateLimited)
}

// M2: the in-memory guard keeps at most passwordLinkBudget timestamps per link, and at most 100000 keys in each
// per-address map. A full map takes no new key: the address is not recorded, and nothing else is refused.
func TestGuardKeepsItsMapsBounded(t *testing.T) {
	now := time.Now()
	g := newShareGuard()
	for i := 0; i < passwordLinkBudget+10; i++ {
		g.wrongPassword(fmt.Sprintf("10.0.%d.1|shl_x", i), "shl_x", now)
	}
	if n := len(g.linkFails["shl_x"]); n > passwordLinkBudget {
		t.Errorf("linkFails holds %d timestamps, want at most %d", n, passwordLinkBudget)
	}

	g = newShareGuard()
	for i := 0; len(g.ipFails) < 100000; i++ {
		g.ipFails[fmt.Sprintf("full%d|x", i)] = []time.Time{now}
	}
	g.wrongPassword("new|x", "shl_y", now)
	if n := len(g.ipFails); n > 100000 {
		t.Errorf("ipFails holds %d keys, want at most 100000", n)
	}

	g = newShareGuard()
	for i := 0; len(g.lockedUntil) < 100000; i++ {
		g.lockedUntil[fmt.Sprintf("locked%d|x", i)] = now.Add(time.Hour)
	}
	for i := 0; i < passwordIPMax; i++ {
		g.wrongPassword("lock|x", "shl_z", now)
	}
	if n := len(g.lockedUntil); n > 100000 {
		t.Errorf("lockedUntil holds %d keys, want at most 100000", n)
	}

	g = newShareGuard()
	for i := 0; len(g.lastAudit) < 100000; i++ {
		g.lastAudit[fmt.Sprintf("audit%d", i)] = now
	}
	g.auditDue("new", now)
	if n := len(g.lastAudit); n > 100000 {
		t.Errorf("lastAudit holds %d keys, want at most 100000", n)
	}
}

// A full map is swept at most once a second, so a flood that keeps it full does not pay for a whole sweep on
// every attempt.
func TestGuardSweepsAFullMapAtMostOnceASecond(t *testing.T) {
	now := time.Now()
	g := newShareGuard()
	for i := 0; len(g.ipFails) < guardMaxKeys; i++ {
		g.ipFails[fmt.Sprintf("full%d|x", i)] = []time.Time{now}
	}
	g.wrongPassword("first|x", "shl_y", now)
	swept := g.lastSweep
	g.wrongPassword("second|x", "shl_y", now.Add(100*time.Millisecond))
	if !g.lastSweep.Equal(swept) {
		t.Errorf("a full map was swept again after 100ms")
	}
	g.wrongPassword("third|x", "shl_y", now.Add(2*time.Second))
	if g.lastSweep.Equal(swept) {
		t.Errorf("a full map was not swept again after 2s")
	}
}

// M3: an anyone link writes one link.open entry per throttle gap, not one per open.
func TestAnyoneOpenAuditsOncePerGap(t *testing.T) {
	fx := newAnyoneFixture(t)
	_, secret := fx.makeAnyone(t, fx.b.ID, nil)
	for i := 0; i < 3; i++ {
		fx.visitAnyone(t, secret, "")
	}
	opens := 0
	for _, e := range fx.auditSeenBy(t, "") {
		if e.Action == "link.open" {
			opens++
		}
	}
	if opens != 1 {
		t.Errorf("link.open entries = %d, want 1 for three opens", opens)
	}
}

// L4: the signed-link budget of a visitor is its own, not the link's.
func TestVisitorsHaveTheirOwnSignedLinkBudget(t *testing.T) {
	fx := newAnyoneFixture(t)
	_, secret := fx.makeAnyone(t, fx.b.ID, nil)
	tok := fx.visitAnyone(t, secret, "")
	makeFrom := func(addr string) *httptest.ResponseRecorder {
		req := jsonRequest(t, "POST", "/api/v1/nodes/"+fx.deep.ID+"/links", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		fx.s.Handler().ServeHTTP(rec, req)
		return rec
	}
	limited := false
	for i := 0; i < 500 && !limited; i++ {
		limited = makeFrom("198.51.100.1:4000").Code == http.StatusTooManyRequests
	}
	if !limited {
		t.Fatal("the first visitor was never limited")
	}
	wantStatus(t, makeFrom("198.51.100.2:4000"), http.StatusCreated)
}

// L2: a visitor is not told the folder or the editors-can-share flag.
func TestVisitorSeesNoFolderOrEditorFlag(t *testing.T) {
	fx := newAnyoneFixture(t)
	_, secret := fx.makeAnyone(t, fx.b.ID, nil)
	tok := fx.visitAnyone(t, secret, "")
	rec := fx.visitor(t, tok, "GET", "/api/v1/nodes/"+fx.b.ID)
	wantStatus(t, rec, 200)
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["folder_id"] != "" {
		t.Errorf("folder_id = %v, want blank for a visitor", m["folder_id"])
	}
	if v, ok := m["editors_can_share"]; ok && v != false {
		t.Errorf("editors_can_share = %v, want false or absent for a visitor", v)
	}
}

// L6: a visitor's role is viewer whatever the link's row says.
func TestAuthenticateShareClampsTheRoleToViewer(t *testing.T) {
	fx := newAnyoneFixture(t)
	_, secret := fx.makeAnyone(t, fx.b.ID, nil)
	tok := fx.visitAnyone(t, secret, "")
	fx.execDB(t, "UPDATE share_links SET role = 'editor' WHERE node_id = ?", fx.b.ID)
	p, err := fx.s.authenticateShare(context.Background(), tok)
	if err != nil {
		t.Fatal(err)
	}
	if p.Link.Role != store.RoleViewer {
		t.Errorf("visitor role = %q, want viewer", p.Link.Role)
	}
}

// L6: a visitor is never the account key in a share, transfer or listing; the principal is built here directly.
func TestTransferRefusesAVisitorWhateverRoleItHolds(t *testing.T) {
	fx := newAnyoneFixture(t)
	link, _ := fx.makeAnyone(t, fx.b.ID, nil)
	p := &auth.Principal{Kind: auth.KindShareLink, ID: link.ID, AccountID: fx.acc.ID,
		Link: &auth.LinkScope{Node: fx.deep.ID, Role: store.RoleEditor, ExpiresAt: time.Now().Add(time.Hour)}}
	req := jsonRequest(t, "POST", "/api/v1/nodes/"+fx.deep.ID+"/transfer-ownership", map[string]any{"to": fx.bob.ID})
	req.SetPathValue("nod", fx.deep.ID)
	req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, &reqInfo{principal: p}))
	err := fx.s.handleTransferOwnership(httptest.NewRecorder(), req)
	if got := apierr.From(err).Code; got != apierr.Forbidden {
		t.Errorf("transfer by a visitor = %v, want forbidden", err)
	}
}

func TestSeesFullChainIsNotAVisitor(t *testing.T) {
	p := &auth.Principal{Kind: auth.KindShareLink, ID: "shl_x", AccountID: "acc_x",
		Link: &auth.LinkScope{Node: "nod_x", Role: store.RoleViewer}}
	if seesFullChain(p, store.RoleViewer) {
		t.Error("a visitor sees the full permission chain, want only the account key does")
	}
}

// L6: the folder list of a visitor is its own, never every folder of the account as manager.
func TestVisitorIsNotListedEveryFolder(t *testing.T) {
	fx := newAnyoneFixture(t)
	link, _ := fx.makeAnyone(t, fx.b.ID, nil)
	p := &auth.Principal{Kind: auth.KindShareLink, ID: link.ID, AccountID: fx.acc.ID,
		Link: &auth.LinkScope{Node: fx.b.ID, Role: store.RoleViewer, ExpiresAt: time.Now().Add(time.Hour)}}
	req := jsonRequest(t, "GET", "/api/v1/folders", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, &reqInfo{principal: p}))
	rec := httptest.NewRecorder()
	if err := fx.s.handleMyFolders(rec, req); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, it := range out.Items {
		if it["role"] == store.RoleManager {
			t.Errorf("a visitor is listed folder %v as manager", it["id"])
		}
	}
}

// Item 9: the sharing policy answers how many anyone links the account has, live or paused.
func TestSharingPolicyCountsAnyoneLinks(t *testing.T) {
	fx := newAnyoneFixture(t)
	fx.makeAnyone(t, fx.b.ID, nil)
	expired, _ := fx.makeAnyone(t, fx.deep.ID, nil)
	fx.setExpiry(t, expired.ID, time.Now().Add(-time.Hour))
	policy := func() map[string]any {
		rec := call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/sharing-policy", fx.key, nil)
		wantStatus(t, rec, 200)
		var m map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if m := policy(); m["anyone_links"] != float64(1) || m["public_links"] != true {
		t.Errorf("policy = %v, want anyone_links 1 (the expired one is not live) and public_links true", m)
	}
	setPolicy(t, fx.s, fx.key, fx.acc.ID, false)
	if m := policy(); m["anyone_links"] != float64(1) || m["public_links"] != false {
		t.Errorf("paused policy = %v, want anyone_links 1 and public_links false", m)
	}
}
