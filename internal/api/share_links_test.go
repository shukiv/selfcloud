// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"

	_ "modernc.org/sqlite" // the driver the store uses; the tests set expiries directly
)

// linkFixture is the sharing fixture with a team space that allows links. The fixture's members are
// ann and bob, with no role yet.
type linkFixture struct {
	*sharingFixture
	root string // the team space's root node
}

func newLinkFixture(t *testing.T) *linkFixture {
	t.Helper()
	fx := newSharingFixture(t)
	fx.settings(t, fx.fld.ID, map[string]any{"allow_links": true})
	return &linkFixture{sharingFixture: fx, root: fx.rootOf(t, fx.fld.ID)}
}

// makeLink makes an organisation link on nodeID as the account key and returns the link and its URL.
func (fx *linkFixture) makeLink(t *testing.T, nodeID, role string) (shareLinkJSON, string) {
	t.Helper()
	body := map[string]any{"audience": "organisation"}
	if role != "" {
		body["role"] = role
	}
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+nodeID+"/share-links", fx.key, body)
	wantStatus(t, rec, 201)
	out := decode[shareLinkCreated](t, rec)
	return out.Link, out.URL
}

// secretOf is the fragment of a link's URL: the secret.
func secretOf(t *testing.T, url string) string {
	t.Helper()
	_, secret, ok := strings.Cut(url, "#")
	if !ok || secret == "" {
		t.Fatalf("the URL %q has no fragment", url)
	}
	return secret
}

// open is a session's POST /share/open of a secret.
func (fx *linkFixture) open(t *testing.T, sess testSession, secret string) *httptest.ResponseRecorder {
	t.Helper()
	return callSession(t, fx.s, sess, "POST", "/api/v1/share/open", map[string]any{"secret": secret})
}

// openedAs opens a link as a member and wants it to work.
func (fx *linkFixture) openedAs(t *testing.T, sess testSession, secret string) {
	t.Helper()
	wantStatus(t, fx.open(t, sess, secret), 200)
}

// deadLink wants the one answer every dead or unavailable link gives.
func deadLink(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	wantError(t, rec, 404, apierr.NotFound)
}

// setExpiry moves a link's expiry in the database, since a test cannot wait for one.
func (fx *linkFixture) setExpiry(t *testing.T, linkID string, at time.Time) {
	t.Helper()
	db, err := sql.Open("sqlite", fx.s.cfg.Store.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE share_links SET expires_at = ? WHERE id = ?", at.UnixMilli(), linkID); err != nil {
		t.Fatal(err)
	}
}

func TestLinkCreateFollowsMayShare(t *testing.T) {
	fx := newLinkFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	fx.setPerm(t, fx.b.ID, fx.bob.ID, "contributor")
	// A viewer and a contributor do not share.
	for _, sess := range []testSession{fx.annSess, fx.bobSess} {
		rec := callSession(t, fx.s, sess, "POST", "/api/v1/nodes/"+fx.b.ID+"/share-links", map[string]any{"audience": "organisation"})
		wantError(t, rec, 403, apierr.Forbidden)
	}
	// An editor does not share unless the node's editors_can_share allows it.
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "editor")
	rec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.b.ID+"/share-links", map[string]any{"audience": "organisation"})
	wantError(t, rec, 403, apierr.Forbidden)
	fx.flag(t, fx.b.ID, map[string]any{"editors_can_share": true})
	rec = callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.b.ID+"/share-links",
		map[string]any{"audience": "organisation", "role": "editor"})
	wantStatus(t, rec, 201)
	// An editor may not give manager, since a link never gives it.
	rec = callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.sib.ID+"/share-links",
		map[string]any{"audience": "organisation", "role": "manager"})
	wantError(t, rec, 400, apierr.InvalidRequest)
	// A user with no role on the node is not reached: not_found.
	_, carlSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "carl@example.com")
	rec = callSession(t, fx.s, carlSess, "POST", "/api/v1/nodes/"+fx.deep.ID+"/share-links", map[string]any{"audience": "organisation"})
	wantError(t, rec, 404, apierr.NotFound)
	// An account of another tenant does not reach the node: not_found.
	_, otherKey := newAccount(t, fx.s, fx.admin, "Other")
	rec = call(t, fx.s, "POST", "/api/v1/nodes/"+fx.deep.ID+"/share-links", otherKey, map[string]any{"audience": "organisation"})
	wantError(t, rec, 404, apierr.NotFound)
	// A delegated token never manages links, even one that acts for the owner-level key.
	tok := mintToken(t, fx.s, fx.key, fx.acc.ID, fx.fld.ID, "")
	rec = call(t, fx.s, "POST", "/api/v1/nodes/"+fx.deep.ID+"/share-links", tok, map[string]any{"audience": "organisation"})
	wantError(t, rec, 403, apierr.Forbidden)
}

func TestLinkRefusesWhatIsNotBuiltYet(t *testing.T) {
	// A drop link is an anyone link: an organisation drop link is refused (see share_drop_test.go).
	fx := newLinkFixture(t)
	for _, body := range []map[string]any{
		{"audience": "organisation", "upload_only": true},
		{"audience": "bogus"},
		{"audience": "organisation", "role": "manager"},
		{"audience": "organisation", "password": "secret-password"},
	} {
		rec := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.b.ID+"/share-links", fx.key, body)
		wantError(t, rec, 400, apierr.InvalidRequest)
	}
	// The one that is built: no role means viewer.
	link, _ := fx.makeLink(t, fx.b.ID, "")
	if link.Role != "viewer" {
		t.Errorf("default role = %q, want viewer", link.Role)
	}
	// One access link per node: a second one is a conflict.
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.b.ID+"/share-links", fx.key, map[string]any{"audience": "organisation"})
	if e := wantError(t, rec, 409, apierr.Conflict); e.Details["reason"] != "exists" {
		t.Errorf("reason = %v, want exists", e.Details["reason"])
	}
}

func TestLinkPolicyAllowLinksAndMembersOnly(t *testing.T) {
	fx := newLinkFixture(t)
	fx.settings(t, fx.fld.ID, map[string]any{"allow_links": false})
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.b.ID+"/share-links", fx.key, map[string]any{"audience": "organisation"})
	e := wantError(t, rec, 403, apierr.Forbidden)
	if e.Details["reason"] != "links_off" {
		t.Errorf("reason = %v, want links_off", e.Details["reason"])
	}
	fx.settings(t, fx.fld.ID, map[string]any{"allow_links": true, "members_only": true})
	// Members-only: a link is on the root alone.
	rec = call(t, fx.s, "POST", "/api/v1/nodes/"+fx.b.ID+"/share-links", fx.key, map[string]any{"audience": "organisation"})
	if e := wantError(t, rec, 409, apierr.Conflict); e.Details["reason"] != "members_only" {
		t.Errorf("reason = %v, want members_only", e.Details["reason"])
	}
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.root+"/share-links", fx.key,
		map[string]any{"audience": "organisation"}), 201)
}

func TestLinkInMyFilesNeedsNoSwitch(t *testing.T) {
	fx := newLinkFixture(t)
	pf := fx.personalFolder(t, fx.annSess)
	file := fx.sessionFile(t, fx.annSess, pf, "/mine.txt", "mine")
	rec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+file.ID+"/share-links", map[string]any{"audience": "organisation"})
	wantStatus(t, rec, 201)
}

func TestSecretIsStoredHashedAndShownOnce(t *testing.T) {
	fx := newLinkFixture(t)
	link, url := fx.makeLink(t, fx.b.ID, "viewer")
	secret := secretOf(t, url)
	if !strings.HasPrefix(url, fx.s.cfg.PublicURL) || !strings.Contains(url, "/s#") {
		t.Errorf("URL %q is not {public_url}/s#secret", url)
	}
	// The stored form is the hash, which finds the link.
	got, err := fx.s.store.ShareLinkBySecretHash(context.Background(), auth.HashToken(secret))
	if err != nil || got.ID != link.ID {
		t.Fatalf("hash lookup: %v, %v", got, err)
	}
	sum := sha256.Sum256([]byte(secret))
	if string(got.SecretHash) != string(sum[:]) || strings.Contains(string(got.SecretHash), secret) {
		t.Error("the stored secret is not the SHA-256 of the secret")
	}
	// Listings never show the secret or the URL.
	rec := call(t, fx.s, "GET", "/api/v1/nodes/"+fx.b.ID+"/share-links", fx.key, nil)
	wantStatus(t, rec, 200)
	if body := rec.Body.String(); strings.Contains(body, secret) || strings.Contains(body, "/s#") {
		t.Errorf("a listing shows the secret or URL: %s", body)
	}
	// The audit log holds neither.
	for _, e := range fx.auditSeenBy(t, "") {
		if strings.Contains(e.Target+e.Action, secret) || strings.Contains(strings.Join(mapValues(e.Details), " "), secret) {
			t.Errorf("audit entry %q holds the secret", e.Action)
		}
	}
	if !auditHas(fx.auditSeenBy(t, ""), "link.create", link.ID) {
		t.Error("link.create is not audited")
	}
}

// mapValues lists the values of a details map, as strings, for the audit test.
func mapValues(m map[string]any) []string {
	var out []string
	for _, v := range m {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func TestResetInvalidatesOldSecret(t *testing.T) {
	fx := newLinkFixture(t)
	link, oldURL := fx.makeLink(t, fx.b.ID, "viewer")
	oldSecret := secretOf(t, oldURL)
	fx.openedAs(t, fx.annSess, oldSecret)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.deep.ID, nil), 200)

	rec := call(t, fx.s, "POST", "/api/v1/share-links/"+link.ID+"/reset", fx.key, nil)
	wantStatus(t, rec, 201)
	newURL := decode[shareLinkCreated](t, rec).URL
	newSecret := secretOf(t, newURL)
	if newSecret == oldSecret {
		t.Fatal("reset kept the secret")
	}
	// The old secret opens nothing, and the visit made with it no longer counts.
	deadLink(t, fx.open(t, fx.bobSess, oldSecret))
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.deep.ID, nil), 404, apierr.NotFound)
	// The new secret opens it again.
	fx.openedAs(t, fx.annSess, newSecret)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.deep.ID, nil), 200)
}

func TestOpenGrantsRoleAndRecordsAVisit(t *testing.T) {
	fx := newLinkFixture(t)
	link, url := fx.makeLink(t, fx.b.ID, "commenter")
	secret := secretOf(t, url)
	// Before the visit ann reaches nothing under the link.
	wantError(t, fx.stat(t, fx.annSess, fx.deep.ID), 404, apierr.NotFound)

	rec := fx.open(t, fx.annSess, secret)
	wantStatus(t, rec, 200)
	if got := decode[map[string]string](t, rec)["node_id"]; got != fx.b.ID {
		t.Errorf("open answered node %q, want %q", got, fx.b.ID)
	}
	// The role shows in node reads, the children listing, and the shared list.
	got := decode[nodeJSON](t, fx.stat(t, fx.annSess, fx.deep.ID))
	if got.Role != "commenter" {
		t.Errorf("role on the deep file = %q, want commenter", got.Role)
	}
	kids := decode[listResponse[nodeJSON]](t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.b.ID+"/children", nil))
	if len(kids.Items) != 1 || kids.Items[0].Role != "commenter" {
		t.Errorf("children of the link node = %v, want the file as commenter", namesOf(kids.Items))
	}
	shared := decode[listResponse[nodeJSON]](t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/shared", nil))
	if len(shared.Items) != 1 || shared.Items[0].ID != fx.b.ID {
		t.Errorf("shared with ann = %v, want the link node", namesOf(shared.Items))
	}
	// The folder root stays closed: a link on a directory does not reach the root.
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", childrenPath(fx.fld.ID), nil), 404, apierr.NotFound)
	// The visit is not a permission row.
	perms := decode[listResponse[permissionJSON]](t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.deep.ID+"/permissions", fx.key, nil))
	if len(perms.Items) != 0 {
		t.Errorf("a visit shows as %d permission rows", len(perms.Items))
	}
	// The link's audit entry is written, and names the link, not the secret.
	if !auditHas(fx.auditSeenBy(t, ""), "link.open", link.ID) {
		t.Error("link.open is not audited")
	}
}

func TestOpenKeepsAHigherOwnRole(t *testing.T) {
	fx := newLinkFixture(t)
	fx.setPerm(t, fx.deep.ID, fx.ann.ID, "editor")
	_, url := fx.makeLink(t, fx.b.ID, "viewer")
	fx.openedAs(t, fx.annSess, secretOf(t, url))
	if got := decode[nodeJSON](t, fx.stat(t, fx.annSess, fx.deep.ID)); got.Role != "editor" {
		t.Errorf("role = %q, want editor (the higher one)", got.Role)
	}
}

func TestOpenRefusesEveryCallerButAMemberOfTheAccount(t *testing.T) {
	fx := newLinkFixture(t)
	_, url := fx.makeLink(t, fx.b.ID, "viewer")
	secret := secretOf(t, url)

	// Another account's member.
	other, otherKey := newAccount(t, fx.s, fx.admin, "Other")
	_, otherSess := verifiedUser(t, fx.s, otherKey, other.ID, "out@other.example")
	deadLink(t, fx.open(t, otherSess, secret))
	// An installation admin, a delegated token, an account key, and no session at all.
	deadLink(t, call(t, fx.s, "POST", "/api/v1/share/open", fx.admin, map[string]any{"secret": secret}))
	tok := mintToken(t, fx.s, fx.key, fx.acc.ID, fx.fld.ID, fx.ann.ID)
	deadLink(t, call(t, fx.s, "POST", "/api/v1/share/open", tok, map[string]any{"secret": secret}))
	deadLink(t, call(t, fx.s, "POST", "/api/v1/share/open", fx.key, map[string]any{"secret": secret}))
	deadLink(t, call(t, fx.s, "POST", "/api/v1/share/open", "", map[string]any{"secret": secret}))
	// A cookie from another site is not a session either.
	req := jsonRequest(t, "POST", "/api/v1/share/open", map[string]any{"secret": secret})
	req.AddCookie(fx.annSess.cookie)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	fx.s.Handler().ServeHTTP(rec, req)
	deadLink(t, rec)
	// A session without the CSRF token is refused as dead too.
	req = jsonRequest(t, "POST", "/api/v1/share/open", map[string]any{"secret": secret})
	req.AddCookie(fx.annSess.cookie)
	req.Header.Set("Origin", fx.s.origin)
	rec = httptest.NewRecorder()
	fx.s.Handler().ServeHTTP(rec, req)
	deadLink(t, rec)
	// A secret that is not a token, and one that matches nothing.
	deadLink(t, fx.open(t, fx.annSess, "not-a-secret"))
	deadLink(t, fx.open(t, fx.annSess, strings.Repeat("A", 43)))
	// None of those opened anything for ann.
	wantError(t, fx.stat(t, fx.annSess, fx.deep.ID), 404, apierr.NotFound)
}

func TestDeadLinksAreAllTheSameAnswer(t *testing.T) {
	fx := newLinkFixture(t)
	link, url := fx.makeLink(t, fx.b.ID, "viewer")
	secret := secretOf(t, url)

	// Expired: a link with an expiry in the past opens nothing.
	fx.setExpiry(t, link.ID, time.Now().Add(-time.Hour))
	deadLink(t, fx.open(t, fx.annSess, secret))
	fx.setExpiry(t, link.ID, time.Now().Add(time.Hour))
	fx.openedAs(t, fx.annSess, secret)
	// Expired after a visit: the visit gives nothing at once.
	fx.setExpiry(t, link.ID, time.Now().Add(-time.Minute))
	wantError(t, fx.stat(t, fx.annSess, fx.deep.ID), 404, apierr.NotFound)
	fx.setExpiry(t, link.ID, time.Now().Add(time.Hour))
	fx.openedAs(t, fx.annSess, secret)
	wantStatus(t, fx.stat(t, fx.annSess, fx.deep.ID), 200)

	// Paused by policy: allow_links off ends the access at once, and the open is refused too.
	fx.settings(t, fx.fld.ID, map[string]any{"allow_links": false})
	wantError(t, fx.stat(t, fx.annSess, fx.deep.ID), 404, apierr.NotFound)
	deadLink(t, fx.open(t, fx.bobSess, secret))
	fx.settings(t, fx.fld.ID, map[string]any{"allow_links": true})
	wantStatus(t, fx.stat(t, fx.annSess, fx.deep.ID), 200) // revived when the switch comes back

	// Deleted: the access ends at once, and the open is refused.
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/share-links/"+link.ID, fx.key, nil), 204)
	wantError(t, fx.stat(t, fx.annSess, fx.deep.ID), 404, apierr.NotFound)
	deadLink(t, fx.open(t, fx.bobSess, secret))
}

func TestTrashedNodeEndsAccessAtOnce(t *testing.T) {
	fx := newLinkFixture(t)
	_, url := fx.makeLink(t, fx.b.ID, "viewer")
	fx.openedAs(t, fx.annSess, secretOf(t, url))
	wantStatus(t, fx.stat(t, fx.annSess, fx.deep.ID), 200)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.b.ID, fx.key, nil), 204)
	wantError(t, fx.stat(t, fx.annSess, fx.deep.ID), 404, apierr.NotFound)
	wantError(t, fx.stat(t, fx.annSess, fx.b.ID), 404, apierr.NotFound)
}

func TestMembersOnlyPausesALinkBelowTheRootAtUse(t *testing.T) {
	fx := newLinkFixture(t)
	_, url := fx.makeLink(t, fx.b.ID, "viewer")
	secret := secretOf(t, url)
	fx.openedAs(t, fx.annSess, secret)
	fx.settings(t, fx.fld.ID, map[string]any{"members_only": true})
	wantError(t, fx.stat(t, fx.annSess, fx.deep.ID), 404, apierr.NotFound)
	deadLink(t, fx.open(t, fx.bobSess, secret))
}

func TestLinkListingShowsSharersMoreThanMembers(t *testing.T) {
	fx := newLinkFixture(t)
	link, url := fx.makeLink(t, fx.b.ID, "viewer")
	fx.openedAs(t, fx.annSess, secretOf(t, url))
	fx.setPerm(t, fx.b.ID, fx.bob.ID, "viewer")

	sharer := decode[listResponse[shareLinkJSON]](t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.deep.ID+"/share-links", fx.key, nil))
	if len(sharer.Items) != 1 || sharer.Items[0].ID != link.ID {
		t.Fatalf("sharer sees %v, want the link above the deep file", sharer.Items)
	}
	if sharer.Items[0].CreatedBy == nil || sharer.Items[0].Visitors == nil || *sharer.Items[0].Visitors != 1 ||
		sharer.Items[0].Paused == nil || *sharer.Items[0].Paused {
		t.Errorf("sharer fields = %+v, want created_by, one visitor and not paused", sharer.Items[0])
	}
	if sharer.Items[0].InheritedFrom == nil || *sharer.Items[0].InheritedFrom != fx.b.ID {
		t.Errorf("inherited_from = %v, want the link node", sharer.Items[0].InheritedFrom)
	}
	// Bob reaches the link's node as a viewer, so he sees the link without the sharer fields.
	bob := decode[listResponse[shareLinkJSON]](t, callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/nodes/"+fx.deep.ID+"/share-links", nil))
	if len(bob.Items) != 1 || bob.Items[0].Visitors != nil {
		t.Errorf("bob sees %+v, want the link without sharer fields", bob.Items)
	}
	// Carl reaches nothing above the link's node, so the link is not shown to him.
	_, carlSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "carl2@example.com")
	if carl := decode[listResponse[shareLinkJSON]](t, callSession(t, fx.s, carlSess, "GET", "/api/v1/nodes/"+fx.deep.ID+"/share-links", nil)); len(carl.Items) != 0 {
		t.Errorf("carl sees %d links on nodes he does not reach", len(carl.Items))
	}
	mine := decode[listResponse[shareLinkJSON]](t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.deep.ID+"/share-links", nil))
	if len(mine.Items) != 1 || mine.Items[0].CreatedBy != nil || mine.Items[0].Visitors != nil {
		t.Errorf("a member sees %+v, want the link without sharer fields", mine.Items)
	}
}

func TestChangingALinkNeedsTheRightSharer(t *testing.T) {
	fx := newLinkFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	link, _ := fx.makeLink(t, fx.b.ID, "viewer")
	// A viewer may not change, reset or delete it, and a token may not either.
	tok := mintToken(t, fx.s, fx.key, fx.acc.ID, fx.fld.ID, "")
	for _, c := range []struct{ method, path string }{
		{"PATCH", "/api/v1/share-links/" + link.ID},
		{"POST", "/api/v1/share-links/" + link.ID + "/reset"},
		{"DELETE", "/api/v1/share-links/" + link.ID},
	} {
		wantError(t, callSession(t, fx.s, fx.annSess, c.method, c.path, map[string]any{"role": "editor"}), 403, apierr.Forbidden)
		wantError(t, call(t, fx.s, c.method, c.path, tok, map[string]any{"role": "editor"}), 403, apierr.Forbidden)
	}
	// Another account cannot reach the link: not_found.
	_, otherKey := newAccount(t, fx.s, fx.admin, "Other")
	wantError(t, call(t, fx.s, "PATCH", "/api/v1/share-links/"+link.ID, otherKey, map[string]any{"role": "editor"}), 404, apierr.NotFound)
	// The account key may change it: the role and the expiry, and clear the expiry.
	rec := call(t, fx.s, "PATCH", "/api/v1/share-links/"+link.ID, fx.key, map[string]any{"role": "contributor"})
	wantStatus(t, rec, 200)
	if got := decode[shareLinkJSON](t, rec).Role; got != "contributor" {
		t.Errorf("role after change = %q, want contributor", got)
	}
	// A change may not give manager: a link never gives it.
	wantError(t, call(t, fx.s, "PATCH", "/api/v1/share-links/"+link.ID, fx.key, map[string]any{"role": "manager"}), 400, apierr.InvalidRequest)
}

func TestSharingPolicyRoutes(t *testing.T) {
	fx := newLinkFixture(t)
	path := "/api/v1/accounts/" + fx.acc.ID + "/sharing-policy"
	if got := decode[sharingPolicyJSON](t, call(t, fx.s, "GET", path, fx.key, nil)); got.PublicLinks {
		t.Error("public_links is on by default")
	}
	// A member and a token are not the account's managers.
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", path, nil), 404, apierr.NotFound)
	tok := mintToken(t, fx.s, fx.key, fx.acc.ID, fx.fld.ID, "")
	wantError(t, call(t, fx.s, "PATCH", path, tok, map[string]any{"public_links": true}), 404, apierr.NotFound)
	wantError(t, call(t, fx.s, "PATCH", path, fx.key, map[string]any{}), 400, apierr.InvalidRequest)
	wantStatus(t, call(t, fx.s, "PATCH", path, fx.key, map[string]any{"public_links": true}), 200)
	if got := decode[sharingPolicyJSON](t, call(t, fx.s, "GET", path, fx.key, nil)); !got.PublicLinks {
		t.Error("the policy did not stick")
	}
	if !auditHas(fx.auditSeenBy(t, ""), "account.sharing_policy", fx.acc.ID) {
		t.Error("account.sharing_policy is not audited")
	}
}
