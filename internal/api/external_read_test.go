// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// extReadFix is two accounts: a sharer A with a team space F holding /D/f.txt, a no-download /D/nd.txt, a
// shortcut /D/sc and /h.txt outside D; and a recipient B with a proved reader (b1) and a verified-but-unproved
// user (b2). Both switches are on and D is granted to b1's address, so b1 reaches D and everything below it
// and nothing else (decision 173, slice xs1b).
type extReadFix struct {
	s          *Server
	rec        *mail.Recorder
	admin      string
	accA, accB accountJSON
	keyA, keyB string
	fld        folderJSON
	root       string // F's root node, above D
	d          nodeJSON
	f          nodeJSON // /D/f.txt
	nd         nodeJSON // /D/nd.txt, no-download
	sc         nodeJSON // /D/sc, a shortcut to h
	h          nodeJSON // /h.txt, outside D
	b1         userResponse
	b1Sess     testSession
	b2         userResponse
	b2Sess     testSession
}

// newExtReadFix builds the fixture. grantExpiry, when given, is the expiry of the grant on D. mutate configures
// the server (the impersonation case turns admin.tenant_access on).
func newExtReadFix(t *testing.T, grantExpiry *time.Time, mutate ...func(*config.Config)) *extReadFix {
	t.Helper()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec, mutate...)
	admin := adminKey(t, s)
	accA, keyA := newAccount(t, s, admin, "Sharer")
	accB, keyB := newAccount(t, s, admin, "Recipient")
	fld := newFolder(t, s, keyA, accA.ID, "Team")
	rn, err := s.store.RootNode(context.Background(), accA.ID, fld.ID)
	if err != nil {
		t.Fatal(err)
	}
	d := mkdirs(t, s, keyA, fld.ID, "/D")
	f := putNode(t, s, keyA, fld.ID, "/D/f.txt", "hello")
	nd := putNode(t, s, keyA, fld.ID, "/D/nd.txt", "secret")
	wantStatus(t, call(t, s, "PATCH", "/api/v1/nodes/"+nd.ID, keyA, map[string]any{"no_download": true}), 200)
	h := putNode(t, s, keyA, fld.ID, "/h.txt", "outside")
	scRec := call(t, s, "POST", "/api/v1/nodes/"+d.ID+"/shortcuts", keyA, map[string]any{"target": h.ID, "name": "sc"})
	wantStatus(t, scRec, 201)
	sc := decode[nodeJSON](t, scRec)
	b1, b1Sess := verifiedUser(t, s, keyB, accB.ID, "reader@example.test")
	wantStatus(t, redeemProof(t, s, proveByMail(t, s, b1Sess, rec)), 204)
	b2, b2Sess := verifiedUser(t, s, keyB, accB.ID, "unproved@example.test")
	enableExternal(t, s, keyA, accA.ID)
	enableExternal(t, s, keyB, accB.ID)
	body := map[string]any{"email": "reader@example.test", "role": "viewer"}
	if grantExpiry != nil {
		body["expires_at"] = grantExpiry
	}
	wantStatus(t, extGrantCall(t, s, keyA, d.ID, body), 200)
	return &extReadFix{s: s, rec: rec, admin: admin, accA: accA, accB: accB, keyA: keyA, keyB: keyB,
		fld: fld, root: rn.ID, d: d, f: f, nd: nd, sc: sc, h: h, b1: b1, b1Sess: b1Sess, b2: b2, b2Sess: b2Sess}
}

// sixRoutes calls the six routes an external reader reaches, on f (D for the children listing), as sess.
func (fx *extReadFix) sixRoutes(t *testing.T, sess testSession) []*httptest.ResponseRecorder {
	t.Helper()
	return []*httptest.ResponseRecorder{
		callSession(t, fx.s, sess, "GET", "/api/v1/nodes/"+fx.f.ID, nil),
		callSession(t, fx.s, sess, "GET", "/api/v1/nodes/"+fx.f.ID+"/ancestors", nil),
		callSession(t, fx.s, sess, "GET", "/api/v1/nodes/"+fx.d.ID+"/children", nil),
		callSession(t, fx.s, sess, "GET", "/api/v1/nodes/"+fx.f.ID+"/content", nil),
		callSession(t, fx.s, sess, "HEAD", "/api/v1/nodes/"+fx.f.ID+"/content", nil),
		callSession(t, fx.s, sess, "POST", "/api/v1/nodes/"+fx.f.ID+"/links", nil),
	}
}

func (fx *extReadFix) stat(t *testing.T, sess testSession, nodeID string) *httptest.ResponseRecorder {
	t.Helper()
	return callSession(t, fx.s, sess, "GET", "/api/v1/nodes/"+nodeID, nil)
}

// TestExternalReadSwitchGatesAllSixRoutes: both accounts' switches gate every one of the six routes. With both
// on the reader reaches them; with either off each answers not_found, the same as a node it cannot reach.
func TestExternalReadSwitchGatesAllSixRoutes(t *testing.T) {
	t.Parallel()
	fx := newExtReadFix(t, nil)
	wantOpen := func(on bool) {
		t.Helper()
		for i, rec := range fx.sixRoutes(t, fx.b1Sess) {
			if on {
				if rec.Code < 200 || rec.Code >= 300 {
					t.Errorf("route %d with both switches on: %d %s", i, rec.Code, rec.Body)
				}
			} else if rec.Code != 404 {
				t.Errorf("route %d with a switch off: %d, want 404: %s", i, rec.Code, rec.Body)
			}
		}
	}
	wantOpen(true)
	// The sharer's switch off closes all six.
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.accA.ID+"/sharing-policy", fx.keyA,
		map[string]any{"external_sharing": false}), 200)
	wantOpen(false)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.accA.ID+"/sharing-policy", fx.keyA,
		map[string]any{"external_sharing": true}), 200)
	// The reader's switch off closes all six too.
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.accB.ID+"/sharing-policy", fx.keyB,
		map[string]any{"external_sharing": false}), 200)
	wantOpen(false)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.accB.ID+"/sharing-policy", fx.keyB,
		map[string]any{"external_sharing": true}), 200)
	wantOpen(true)
}

// TestExternalReadOutsiderView: the reader sees the item as a link's visitor does — no creator, owner or folder,
// no badges or thumbnails, no shortcut, and the granted root has no parent. Its ancestors stop at the granted
// root. It reads the bytes, mints a link that serves them, and the no-download rule still binds it.
func TestExternalReadOutsiderView(t *testing.T) {
	t.Parallel()
	fx := newExtReadFix(t, nil)
	// The granted root D: no parent, no ids of the sharer's people or folder.
	dj := decode[nodeJSON](t, fx.stat(t, fx.b1Sess, fx.d.ID))
	if dj.ParentID != nil {
		t.Errorf("the granted root has parent_id %v, want null", *dj.ParentID)
	}
	if dj.CreatedBy != "" || dj.OwnerID != "" || dj.FolderID != "" {
		t.Errorf("the granted root leaks ids: %+v", dj)
	}
	if dj.Role != store.RoleViewer {
		t.Errorf("the granted root role = %q, want viewer", dj.Role)
	}
	// f: no ids, no thumbnail; its parent is D, which the reader reaches.
	fj := decode[nodeJSON](t, fx.stat(t, fx.b1Sess, fx.f.ID))
	if fj.CreatedBy != "" || fj.OwnerID != "" || fj.FolderID != "" {
		t.Errorf("f leaks ids: %+v", fj)
	}
	if fj.HasThumbnail != nil {
		t.Error("an external reader sees has_thumbnail")
	}
	if fj.ParentID == nil || *fj.ParentID != fx.d.ID {
		t.Errorf("f parent = %v, want D", fj.ParentID)
	}
	// The children of D: f and nd only, no shortcut, no badge, no thumbnail, no ids.
	kids := decode[listResponse[nodeJSON]](t, callSession(t, fx.s, fx.b1Sess, "GET", "/api/v1/nodes/"+fx.d.ID+"/children", nil))
	if len(kids.Items) != 2 {
		t.Fatalf("children of D = %d items, want f and nd only: %+v", len(kids.Items), kids.Items)
	}
	for _, k := range kids.Items {
		if k.Kind == store.KindShortcut {
			t.Errorf("an external reader sees the shortcut %q", k.Name)
		}
		if k.HasLink {
			t.Errorf("child %q carries a link badge", k.Name)
		}
		if k.HasThumbnail != nil {
			t.Errorf("child %q carries has_thumbnail", k.Name)
		}
		if k.CreatedBy != "" || k.FolderID != "" {
			t.Errorf("child %q leaks ids: %+v", k.Name, k)
		}
	}
	// A shortcut is not reached at all.
	wantError(t, fx.stat(t, fx.b1Sess, fx.sc.ID), 404, apierr.NotFound)
	// The ancestors of f stop at D; D has none.
	anc := decode[ancestorsJSON](t, callSession(t, fx.s, fx.b1Sess, "GET", "/api/v1/nodes/"+fx.f.ID+"/ancestors", nil))
	if len(anc.Ancestors) != 1 || anc.Ancestors[0].ID != fx.d.ID || anc.Truncated {
		t.Errorf("ancestors of f = %+v, want D alone", anc)
	}
	anc = decode[ancestorsJSON](t, callSession(t, fx.s, fx.b1Sess, "GET", "/api/v1/nodes/"+fx.d.ID+"/ancestors", nil))
	if len(anc.Ancestors) != 0 {
		t.Errorf("ancestors of D = %+v, want none", anc.Ancestors)
	}
	// GET and HEAD of the content.
	rec := callSession(t, fx.s, fx.b1Sess, "GET", "/api/v1/nodes/"+fx.f.ID+"/content", nil)
	wantStatus(t, rec, 200)
	if rec.Body.String() != "hello" {
		t.Errorf("content = %q, want hello", rec.Body.String())
	}
	rec = callSession(t, fx.s, fx.b1Sess, "HEAD", "/api/v1/nodes/"+fx.f.ID+"/content", nil)
	wantStatus(t, rec, 200)
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD carried a body: %q", rec.Body.String())
	}
	// A minted link serves the bytes to anyone who holds it.
	rec = callSession(t, fx.s, fx.b1Sess, "POST", "/api/v1/nodes/"+fx.f.ID+"/links", nil)
	wantStatus(t, rec, 201)
	lj := decode[linkJSON](t, rec)
	path := strings.TrimPrefix(lj.URL, fx.s.cfg.PublicURL)
	rec = rawCall(t, fx.s, "GET", path, "", "", nil)
	wantStatus(t, rec, 200)
	if rec.Body.String() != "hello" {
		t.Errorf("the link served %q, want hello", rec.Body.String())
	}
	// The no-download file: a download is refused, inline viewing of the text is allowed.
	wantError(t, callSession(t, fx.s, fx.b1Sess, "GET", "/api/v1/nodes/"+fx.nd.ID+"/content", nil),
		403, apierr.DownloadRestricted)
	wantStatus(t, callSession(t, fx.s, fx.b1Sess, "GET", "/api/v1/nodes/"+fx.nd.ID+"/content?disposition=inline", nil), 200)
	// h sits outside D, and F's root sits above it: both are not_found.
	wantError(t, fx.stat(t, fx.b1Sess, fx.h.ID), 404, apierr.NotFound)
	wantError(t, fx.stat(t, fx.b1Sess, fx.root), 404, apierr.NotFound)
}

// TestExternalReadClosedRoutes: every node route without the ext flag stays closed to the external reader. The
// loop walks the whole route table; the named writes and reads are checked to answer not_found exactly. None of
// them changes the sharer's nodes.
func TestExternalReadClosedRoutes(t *testing.T) {
	t.Parallel()
	fx := newExtReadFix(t, nil)
	for _, rt := range fx.s.routes() {
		if !strings.Contains(rt.path, "{nod}") || rt.ext {
			continue
		}
		// Deleting the caller's own star or recent mark is an idempotent no-op that never reads or changes the
		// node: it answers 204 for any signed-in user, even one who cannot reach the node, so it is not a read
		// path. The sharer's nodes are still checked unchanged below.
		if rt.method == "DELETE" && (rt.path == "/api/v1/nodes/{nod}/star" || rt.path == "/api/v1/recent/{nod}") {
			continue
		}
		path := strings.ReplaceAll(rt.path, "{nod}", fx.f.ID)
		path = placeholders.ReplaceAllString(path, fx.f.ID)
		rec := callSession(t, fx.s, fx.b1Sess, rt.method, path, nil)
		if rec.Code >= 200 && rec.Code < 300 {
			t.Errorf("non-ext route %s %s answered %d for an external reader", rt.method, rt.path, rec.Code)
		}
	}
	// The named routes answer not_found, not a kind-based refusal.
	wantError(t, callSession(t, fx.s, fx.b1Sess, "PATCH", "/api/v1/nodes/"+fx.f.ID, map[string]any{"name": "renamed"}),
		404, apierr.NotFound)
	wantError(t, callSession(t, fx.s, fx.b1Sess, "DELETE", "/api/v1/nodes/"+fx.f.ID, nil), 404, apierr.NotFound)
	wantError(t, callSession(t, fx.s, fx.b1Sess, "POST", "/api/v1/nodes/"+fx.f.ID+"/move", map[string]any{"folder": fx.fld.ID}),
		404, apierr.NotFound)
	wantError(t, callSession(t, fx.s, fx.b1Sess, "POST", "/api/v1/nodes/"+fx.f.ID+"/copy", map[string]any{"folder": fx.fld.ID}),
		404, apierr.NotFound)
	wantError(t, callSession(t, fx.s, fx.b1Sess, "GET", "/api/v1/nodes/"+fx.f.ID+"/thumbnail", nil), 404, apierr.NotFound)
	wantError(t, callSession(t, fx.s, fx.b1Sess, "GET", "/api/v1/nodes/"+fx.f.ID+"/preview", nil), 404, apierr.NotFound)
	wantError(t, callSession(t, fx.s, fx.b1Sess, "GET", "/api/v1/nodes/"+fx.f.ID+"/permissions", nil), 404, apierr.NotFound)
	wantError(t, rawSession(t, fx.s, fx.b1Sess, "PUT", "/api/v1/nodes/"+fx.f.ID+"/content", "x",
		map[string]string{"If-Match": `"x"`}), 404, apierr.NotFound)
	// The sharer's nodes are unchanged.
	wantStatus(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.f.ID, fx.keyA, nil), 200)
	wantStatus(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.d.ID, fx.keyA, nil), 200)
	rec := call(t, fx.s, "GET", "/api/v1/nodes/"+fx.f.ID+"/content", fx.keyA, nil)
	wantStatus(t, rec, 200)
	if rec.Body.String() != "hello" {
		t.Errorf("f changed: content = %q, want hello", rec.Body.String())
	}
}

// TestExternalReadRefusals: every caller and state that must not open the read path answers not_found (or, for an
// admin, the unchanged forbidden). Each case builds its own fixture so a destructive step cannot reach the next.
func TestExternalReadRefusals(t *testing.T) {
	t.Parallel()
	t.Run("unproved user", func(t *testing.T) {
		fx := newExtReadFix(t, nil)
		wantError(t, fx.stat(t, fx.b2Sess, fx.f.ID), 404, apierr.NotFound)
	})
	t.Run("expired grant", func(t *testing.T) {
		fx := newExtReadFix(t, nil)
		past := time.Now().Add(-time.Hour)
		err := fx.s.store.Write(context.Background(), func(tx *store.Tx) error {
			return tx.SetExternalPermission(context.Background(), &store.ExternalPermission{
				NodeID: fx.d.ID, AccountID: fx.accA.ID, Email: "reader@example.test", EmailKey: "reader@example.test",
				Role: store.RoleViewer, CreatedBy: "test", CreatedAt: store.Now(), ExpiresAt: &past})
		})
		if err != nil {
			t.Fatal(err)
		}
		wantError(t, fx.stat(t, fx.b1Sess, fx.f.ID), 404, apierr.NotFound)
	})
	t.Run("removed grant", func(t *testing.T) {
		fx := newExtReadFix(t, nil)
		wantStatus(t, call(t, fx.s, "DELETE", extPath(fx.d.ID)+"/reader@example.test", fx.keyA, nil), 204)
		wantError(t, fx.stat(t, fx.b1Sess, fx.f.ID), 404, apierr.NotFound)
	})
	t.Run("trashed directory", func(t *testing.T) {
		fx := newExtReadFix(t, nil)
		wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.d.ID, fx.keyA, nil), 204)
		wantError(t, fx.stat(t, fx.b1Sess, fx.f.ID), 404, apierr.NotFound)
		wantError(t, fx.stat(t, fx.b1Sess, fx.d.ID), 404, apierr.NotFound)
	})
	t.Run("trashed file", func(t *testing.T) {
		fx := newExtReadFix(t, nil)
		wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.f.ID, fx.keyA, nil), 204)
		wantError(t, fx.stat(t, fx.b1Sess, fx.f.ID), 404, apierr.NotFound)
		// D itself is not trashed, so it is still reached.
		wantStatus(t, fx.stat(t, fx.b1Sess, fx.d.ID), 200)
	})
	t.Run("delegated token", func(t *testing.T) {
		fx := newExtReadFix(t, nil)
		tok := mintFor(t, fx.s, fx.keyB, fx.accB.ID, map[string]any{"user": fx.b1.ID, "all": true, "permissions": "read"})
		wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.f.ID, tok, nil), 404, apierr.NotFound)
	})
	t.Run("account key of B", func(t *testing.T) {
		fx := newExtReadFix(t, nil)
		wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.f.ID, fx.keyB, nil), 404, apierr.NotFound)
	})
	t.Run("impersonation of the reader", func(t *testing.T) {
		fx := newExtReadFix(t, nil, func(c *config.Config) { c.Admin.TenantAccess = true })
		_, adminSess := newAdminUser(t, fx.s, fx.admin, "admin@example.test")
		rec := callSession(t, fx.s, adminSess, "POST", "/api/v1/accounts/"+fx.accB.ID+"/users/"+fx.b1.ID+"/sign-in",
			map[string]any{"admin_password": testPassword})
		wantStatus(t, rec, 200)
		impSess := sessionFrom(t, fx.s, rec)
		wantError(t, fx.stat(t, impSess, fx.f.ID), 404, apierr.NotFound)
	})
	t.Run("link visitor of B", func(t *testing.T) {
		fx := newExtReadFix(t, nil)
		fldB := newFolder(t, fx.s, fx.keyB, fx.accB.ID, "BTeam")
		bFile := putNode(t, fx.s, fx.keyB, fldB.ID, "/b.txt", "b")
		visitor := &auth.Principal{Kind: auth.KindShareLink, ID: "visitor", AccountID: fx.accB.ID,
			Link: &auth.LinkScope{Node: bFile.ID, Role: store.RoleViewer}}
		_, _, err := nodeAccessWith(context.Background(), fx.s.store, visitor, fx.f.ID)
		if apierr.From(err).Code != apierr.NotFound {
			t.Errorf("a link visitor of B read A's node: %v", err)
		}
	})
	t.Run("admin unchanged", func(t *testing.T) {
		fx := newExtReadFix(t, nil)
		wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.f.ID, fx.admin, nil), 403, apierr.Forbidden)
		_, adminSess := newAdminUser(t, fx.s, fx.admin, "admin@example.test")
		wantError(t, fx.stat(t, adminSess, fx.f.ID), 403, apierr.Forbidden)
	})
}

// TestExternalReadNodeRoleIn: the access walk's external branch. A nil scope is unauthenticated; a scope rooted
// elsewhere is not_found; a correct scope gives the grant's viewer role, never manager; and a kind the walk does
// not know reaches nothing of another account.
func TestExternalReadNodeRoleIn(t *testing.T) {
	t.Parallel()
	fx := newExtReadFix(t, nil)
	ctx := context.Background()
	p := &auth.Principal{Kind: auth.KindExternal, ID: fx.b1.ID, AccountID: fx.accA.ID}
	if _, _, err := nodeRoleIn(ctx, fx.s.store, p, fx.f.ID, false, true); apierr.From(err).Code != apierr.Unauthenticated {
		t.Errorf("nil Ext answered %v, want unauthenticated", err)
	}
	p.Ext = &auth.ExternalScope{Node: fx.h.ID, Role: store.RoleViewer}
	if _, _, err := nodeRoleIn(ctx, fx.s.store, p, fx.f.ID, false, true); apierr.From(err).Code != apierr.NotFound {
		t.Errorf("a scope rooted at h answered %v, want not_found", err)
	}
	p.Ext = &auth.ExternalScope{Node: fx.d.ID, Role: store.RoleViewer}
	n, role, err := nodeRoleIn(ctx, fx.s.store, p, fx.f.ID, false, true)
	if err != nil || n.ID != fx.f.ID || role != store.RoleViewer {
		t.Errorf("a correct scope answered (%v, %q, %v), want f at viewer", n, role, err)
	}
	unknown := &auth.Principal{Kind: auth.Kind("mystery"), ID: "x", AccountID: fx.accB.ID}
	if _, _, err := nodeRoleIn(ctx, fx.s.store, unknown, fx.f.ID, false, true); apierr.From(err).Code != apierr.NotFound {
		t.Errorf("an unknown kind answered %v, want not_found, never manager", err)
	}
}

// TestExternalReadAddressChangeVoidsProof: the proof follows the address. Once the reader moves to another
// address (verified, but not proved), the grant no longer opens the node.
func TestExternalReadAddressChangeVoidsProof(t *testing.T) {
	t.Parallel()
	fx := newExtReadFix(t, nil)
	wantStatus(t, fx.stat(t, fx.b1Sess, fx.f.ID), 200)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.accB.ID+"/users/"+fx.b1.ID, fx.keyB,
		map[string]any{"email": "moved@example.test", "verification": "none"}), 200)
	rec := login(t, fx.s, "moved@example.test", testPassword, fx.accB.ID)
	wantStatus(t, rec, 200)
	moved := sessionFrom(t, fx.s, rec)
	wantError(t, fx.stat(t, moved, fx.f.ID), 404, apierr.NotFound)
}

// TestExternalReadMembersOnly: a members-only team space reaches its root only. A grant below the root stops
// working once the space becomes members-only; a grant on the root still opens it.
func TestExternalReadMembersOnly(t *testing.T) {
	t.Parallel()
	fx := newExtReadFix(t, nil)
	wantStatus(t, fx.stat(t, fx.b1Sess, fx.f.ID), 200)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/folders/"+fx.fld.ID+"/settings", fx.keyA,
		map[string]any{"members_only": true}), 200)
	wantError(t, fx.stat(t, fx.b1Sess, fx.f.ID), 404, apierr.NotFound)
	wantError(t, fx.stat(t, fx.b1Sess, fx.d.ID), 404, apierr.NotFound)
	// A grant on the space root is reached even while members-only.
	wantStatus(t, extGrantCall(t, fx.s, fx.keyA, fx.root, map[string]any{"email": "reader@example.test", "role": "viewer"}), 200)
	wantStatus(t, fx.stat(t, fx.b1Sess, fx.root), 200)
}

// TestExternalReadLinkLifeAndAudit: an external reader's signed link lives at most an hour and at most as long as
// the grant. The mint is audited in the sharer's account with the reader's address and account, the activity actor
// is an external reader, and no view is recorded.
func TestExternalReadLinkLifeAndAudit(t *testing.T) {
	t.Parallel()
	t.Run("hour cap", func(t *testing.T) {
		fx := newExtReadFix(t, nil)
		// An hour is the most an external reader may ask for.
		rec := callSession(t, fx.s, fx.b1Sess, "POST", "/api/v1/nodes/"+fx.f.ID+"/links", map[string]any{"ttl": 3601})
		e := wantError(t, rec, 400, apierr.InvalidRequest)
		if max, _ := e.Details["max_ttl_seconds"].(float64); max > 3600 {
			t.Errorf("max_ttl_seconds = %v, want at most 3600", e.Details["max_ttl_seconds"])
		}
		rec = callSession(t, fx.s, fx.b1Sess, "POST", "/api/v1/nodes/"+fx.f.ID+"/links", map[string]any{"ttl": 3600})
		wantStatus(t, rec, 201)
		lj := decode[linkJSON](t, rec)
		if lj.ExpiresAt.After(time.Now().Add(time.Hour + 2*time.Second)) {
			t.Errorf("link expires at %v, more than an hour away", lj.ExpiresAt)
		}
	})
	t.Run("grant end cap", func(t *testing.T) {
		end := time.Now().Add(30 * time.Minute).UTC().Truncate(time.Second)
		fx := newExtReadFix(t, &end)
		rec := callSession(t, fx.s, fx.b1Sess, "POST", "/api/v1/nodes/"+fx.f.ID+"/links", map[string]any{"ttl": 3600})
		e := wantError(t, rec, 400, apierr.InvalidRequest)
		if max, _ := e.Details["max_ttl_seconds"].(float64); max > 1800 {
			t.Errorf("max_ttl_seconds = %v, want at most the grant's 1800", e.Details["max_ttl_seconds"])
		}
		rec = callSession(t, fx.s, fx.b1Sess, "POST", "/api/v1/nodes/"+fx.f.ID+"/links", map[string]any{"ttl": 1700})
		wantStatus(t, rec, 201)
		lj := decode[linkJSON](t, rec)
		if lj.ExpiresAt.After(end.Add(2 * time.Second)) {
			t.Errorf("link expires at %v, past the grant's end %v", lj.ExpiresAt, end)
		}
	})
	t.Run("audit and no view", func(t *testing.T) {
		fx := newExtReadFix(t, nil)
		wantStatus(t, callSession(t, fx.s, fx.b1Sess, "GET", "/api/v1/nodes/"+fx.f.ID+"/content", nil), 200)
		wantStatus(t, callSession(t, fx.s, fx.b1Sess, "POST", "/api/v1/nodes/"+fx.f.ID+"/links", nil), 201)
		// The mint is audited in the sharer's account, naming the reader.
		audit := decode[listResponse[auditJSON]](t, call(t, fx.s, "GET", "/api/v1/accounts/"+fx.accA.ID+"/audit", fx.keyA, nil))
		var entry *auditJSON
		for i := range audit.Items {
			if audit.Items[i].Action == "node.link" && audit.Items[i].Target == fx.f.ID {
				entry = &audit.Items[i]
			}
		}
		if entry == nil {
			t.Fatal("no node.link audit entry for the external reader's mint")
		}
		if entry.Actor.Kind != string(auth.KindExternal) || entry.Actor.ID != fx.b1.ID {
			t.Errorf("audit actor = %+v, want external_user %s", entry.Actor, fx.b1.ID)
		}
		if entry.Details["reader_email"] != "reader@example.test" || entry.Details["reader_account"] != fx.accB.ID {
			t.Errorf("audit details = %v, want the reader's email and account", entry.Details)
		}
		// The activity actor of an external reader's row is an external reader.
		actor := activityActorOf(&auth.Principal{Kind: auth.KindExternal},
			&store.ActivityRow{ActorKind: string(auth.KindExternal)}, &activityPeople{})
		if actor.Kind != "external" {
			t.Errorf("activity actor kind = %q, want external", actor.Kind)
		}
		// No view was recorded for the reader: the gate holds no mark for it.
		fx.s.views.mu.Lock()
		_, seen := fx.s.views.last[fx.b1.ID+"|"+fx.f.ID]
		fx.s.views.mu.Unlock()
		if seen {
			t.Error("an external reader's read was recorded as a view")
		}
	})
}
