// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/sweep"
)

// The helpers below serve the sharing controls of slice n2. They are methods on the sharing fixture,
// so each test reads as the setup it needs.

// grant gives a subject a role on a node as the account key, without mail, so the mail a test counts
// is only the mail its own request causes.
func (fx *sharingFixture) grant(t *testing.T, nodeID, subject, role string) {
	t.Helper()
	wantStatus(t, call(t, fx.s, "PUT", "/api/v1/nodes/"+nodeID+"/permissions/"+subject, fx.key,
		map[string]any{"role": role, "notify": false}), 200)
}

// flag changes a node's own settings as the account key.
func (fx *sharingFixture) flag(t *testing.T, nodeID string, body map[string]any) {
	t.Helper()
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+nodeID, fx.key, body), 200)
}

// settings changes a team space's settings as the account key.
func (fx *sharingFixture) settings(t *testing.T, fld string, body map[string]any) {
	t.Helper()
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/folders/"+fld+"/settings", fx.key, body), 200)
}

// sent returns the mail the fixture's server has sent so far, oldest first.
func (fx *sharingFixture) sent(t *testing.T) []mail.Message {
	t.Helper()
	rec, ok := fx.s.mailer.(*mail.Recorder)
	if !ok {
		t.Fatal("the fixture's mailer is not a mail.Recorder")
	}
	return rec.Messages()
}

// member is a verified user with the session its verification started.
type member struct {
	user userResponse
	sess testSession
}

// newMember creates and verifies a user, as verifiedUser does, and returns it with its session.
func newMember(t *testing.T, fx *sharingFixture, email string) member {
	t.Helper()
	u, sess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, email)
	return member{user: u, sess: sess}
}

// activeUser creates a user that needs no verification, so it can be named without a session.
func (fx *sharingFixture) activeUser(t *testing.T, email, displayName, locale string) userResponse {
	t.Helper()
	rec := call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users", fx.key,
		map[string]any{"email": email, "display_name": displayName, "locale": locale, "verification": "none"})
	wantStatus(t, rec, 201)
	return decode[userResponse](t, rec)
}

// pendingUser creates a user that has not verified yet, so it is not active.
func (fx *sharingFixture) pendingUser(t *testing.T, email string) userResponse {
	t.Helper()
	rec := call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users", fx.key,
		map[string]any{"email": email, "verification": "link"})
	wantStatus(t, rec, 201)
	return decode[userResponse](t, rec)
}

// sessionDir creates a directory in a folder as a signed-in user, so the user owns it.
func (fx *sharingFixture) sessionDir(t *testing.T, sess testSession, fld, p string) nodeJSON {
	t.Helper()
	rec := callSession(t, fx.s, sess, "POST", "/api/v1/folders/"+fld+"/directories", map[string]any{"path": p, "parents": true})
	wantStatus(t, rec, 201)
	return decode[nodeJSON](t, rec)
}

// sessionFile writes a file in a folder as a signed-in user, so the user owns it.
func (fx *sharingFixture) sessionFile(t *testing.T, sess testSession, fld, p, body string) nodeJSON {
	t.Helper()
	rec := rawSession(t, fx.s, sess, "POST", "/api/v1/folders/"+fld+"/files?"+q(p), body, nil)
	wantStatus(t, rec, 201)
	return decode[nodeJSON](t, rec)
}

// nodeOf reads a node from the store, as the server sees it after a request.
func (fx *sharingFixture) nodeOf(t *testing.T, nodeID string) *store.Node {
	t.Helper()
	n, err := fx.s.store.Node(context.Background(), fx.acc.ID, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// rowsOn reads every permission row on a node from the store, expired ones included.
func (fx *sharingFixture) rowsOn(t *testing.T, nodeID string) []*store.Permission {
	t.Helper()
	rows, err := fx.s.store.NodePermissions(context.Background(), fx.acc.ID, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// childrenOf lists a node's children as the account key.
func (fx *sharingFixture) childrenOf(t *testing.T, nodeID string) []nodeJSON {
	t.Helper()
	rec := call(t, fx.s, "GET", "/api/v1/nodes/"+nodeID+"/children", fx.key, nil)
	wantStatus(t, rec, 200)
	return decode[listResponse[nodeJSON]](t, rec).Items
}

// namesOf returns the names of nodes.
func namesOf(nodes []nodeJSON) []string {
	var out []string
	for _, n := range nodes {
		out = append(out, n.Name)
	}
	return out
}

// auditSeenBy returns the audit entries a reader sees through the store: a user sees the entries that
// are not private and the ones private to it, and viewer "" sees only the entries that are not private.
func (fx *sharingFixture) auditSeenBy(t *testing.T, viewer string) []*store.AuditEntry {
	t.Helper()
	entries, _, err := fx.s.store.AuditLog(context.Background(), fx.acc.ID, viewer, false, store.Page{Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// auditHas reports whether entries hold one with the action and target.
func auditHas(entries []*store.AuditEntry, action, target string) bool {
	for _, e := range entries {
		if e.Action == action && e.Target == target {
			return true
		}
	}
	return false
}

// insertPermission writes a permission row through the store, bypassing the API, so a test can make
// rows the API would refuse, such as one that expired long ago.
func (fx *sharingFixture) insertPermission(t *testing.T, pm *store.Permission) {
	t.Helper()
	ctx := context.Background()
	if err := fx.s.store.Write(ctx, func(tx *store.Tx) error { return tx.SetPermission(ctx, pm) }); err != nil {
		t.Fatal(err)
	}
}

// usageKey is the part of an account's usage a test compares. The pointer fields of usageJSON are left
// out, since they compare by address.
type usageKey struct{ used, reserved, files, folders int64 }

func (fx *sharingFixture) usage(t *testing.T) usageKey {
	t.Helper()
	u := usageOf(t, fx.s, fx.key, fx.acc.ID)
	return usageKey{used: u.UsedBytes, reserved: u.ReservedBytes, files: u.Files, folders: u.Folders}
}

// --- 1. Access expiry -----------------------------------------------------------------------------

// TestPermissionExpiresAtIsCheckedAgainstTheCap: expires_at must be in the future and within the
// sharing cap, and a zero cap means no limit at all.
func TestPermissionExpiresAtIsCheckedAgainstTheCap(t *testing.T) {
	fx := newSharingFixture(t)
	path := "/api/v1/nodes/" + fx.a.ID + "/permissions/" + fx.bob.ID
	put := func(at time.Time) *httptest.ResponseRecorder {
		return call(t, fx.s, "PUT", path, fx.key, map[string]any{"role": "viewer", "notify": false, "expires_at": at})
	}
	now := time.Now()
	wantError(t, put(now.Add(-time.Hour)), 400, apierr.InvalidRequest)

	fx.s.cfg.Sharing.MaxExpiry = config.Duration(time.Hour)
	wantError(t, put(now.Add(2*time.Hour)), 400, apierr.InvalidRequest)
	wantStatus(t, put(now.Add(30*time.Minute)), 200)

	fx.s.cfg.Sharing.MaxExpiry = 0
	wantStatus(t, put(now.Add(10*365*24*time.Hour)), 200)
}

// TestExpiredPermissionGrantsNothing: a permission grants access until its expires_at and none after,
// even though its row is still there.
func TestExpiredPermissionGrantsNothing(t *testing.T) {
	fx := newSharingFixture(t)
	expires := time.Now().Add(time.Second)
	wantStatus(t, call(t, fx.s, "PUT", "/api/v1/nodes/"+fx.a.ID+"/permissions/"+fx.bob.ID, fx.key,
		map[string]any{"role": "viewer", "notify": false, "expires_at": expires}), 200)
	wantStatus(t, fx.stat(t, fx.bobSess, fx.deep.ID), 200)

	time.Sleep(time.Until(expires) + 100*time.Millisecond)
	wantError(t, fx.stat(t, fx.bobSess, fx.deep.ID), 404, apierr.NotFound)
}

// TestPermissionListingShowsExpiresAt: the listing gives each permission's expires_at, and null for a
// permission that never ends.
func TestPermissionListingShowsExpiresAt(t *testing.T) {
	fx := newSharingFixture(t)
	at := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	wantStatus(t, call(t, fx.s, "PUT", "/api/v1/nodes/"+fx.a.ID+"/permissions/"+fx.bob.ID, fx.key,
		map[string]any{"role": "viewer", "notify": false, "expires_at": at}), 200)
	fx.grant(t, fx.a.ID, fx.ann.ID, "viewer")

	got := decode[listResponse[permissionJSON]](t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.a.ID+"/permissions", fx.key, nil))
	seen := map[string]bool{}
	for _, p := range got.Items {
		switch p.SubjectID {
		case fx.bob.ID:
			if p.ExpiresAt == nil || !p.ExpiresAt.Equal(at) {
				t.Errorf("bob's expires_at = %v, want %v", p.ExpiresAt, at)
			}
			seen["bob"] = true
		case fx.ann.ID:
			if p.ExpiresAt != nil {
				t.Errorf("ann's expires_at = %v, want null", p.ExpiresAt)
			}
			seen["ann"] = true
		}
	}
	if !seen["bob"] || !seen["ann"] {
		t.Errorf("listing = %+v, want bob's and ann's rows", got.Items)
	}
}

// TestSweepRemovesPermissionsExpiredOverADay: the sweep deletes a row that expired more than a day ago,
// audits the removal as a system action, and keeps a row that expired inside the day.
func TestSweepRemovesPermissionsExpiredOverADay(t *testing.T) {
	fx := newSharingFixture(t)
	ctx := context.Background()
	now := time.Now()
	stale := now.Add(-48 * time.Hour)
	recent := now.Add(-time.Hour)
	fx.insertPermission(t, &store.Permission{NodeID: fx.a.ID, AccountID: fx.acc.ID, SubjectKind: store.SubjectUser,
		SubjectID: fx.bob.ID, Role: store.RoleViewer, CreatedAt: now.Add(-72 * time.Hour), CreatedBy: "test", ExpiresAt: &stale})
	fx.insertPermission(t, &store.Permission{NodeID: fx.c.ID, AccountID: fx.acc.ID, SubjectKind: store.SubjectUser,
		SubjectID: fx.ann.ID, Role: store.RoleViewer, CreatedAt: now.Add(-72 * time.Hour), CreatedBy: "test", ExpiresAt: &recent})

	sw := sweep.New(sweep.Options{Store: fx.s.store, Blobs: fx.s.blobs, DeleteGrace: time.Hour})
	res, err := sw.Once(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if res.Permissions != 1 {
		t.Errorf("sweep removed %d permissions, want 1", res.Permissions)
	}
	if rows := fx.rowsOn(t, fx.a.ID); len(rows) != 0 {
		t.Errorf("the 48-hour-old row is still there: %+v", rows)
	}

	entries, _, err := fx.s.store.AuditLog(ctx, fx.acc.ID, "", true, store.Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var found []*store.AuditEntry
	for _, e := range entries {
		if e.Action == "permission.expired" {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("permission.expired entries = %d, want 1", len(found))
	}
	if found[0].ActorKind != "system" || found[0].Target != fx.a.ID || found[0].Details["subject_id"] != fx.bob.ID {
		t.Errorf("audit entry = %+v, want a system entry on the node for bob", found[0])
	}
	// The row that expired inside the day was kept by the first pass, so a pass a day later finds it.
	later, err := sw.Once(ctx, now.Add(25*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if later.Permissions != 1 {
		t.Errorf("a pass a day later removed %d permissions, want the one kept by the first pass", later.Permissions)
	}
}

// --- 2. No download -------------------------------------------------------------------------------

// TestNoDownloadRestrictsViewersAndCommentersOnly: the node flag, inherited from /a, refuses downloads,
// signed links and copies to a viewer or a commenter. Inline viewing and a contributor or editor are
// unaffected, and only an editor may set the flag.
func TestNoDownloadRestrictsViewersAndCommentersOnly(t *testing.T) {
	fx := newSharingFixture(t)
	carl := newMember(t, fx, "carl@example.com")
	dan := newMember(t, fx, "dan@example.com")
	fx.grant(t, fx.a.ID, fx.bob.ID, "viewer")
	fx.grant(t, fx.a.ID, fx.ann.ID, "commenter")
	fx.grant(t, fx.a.ID, carl.user.ID, "contributor")
	fx.grant(t, fx.a.ID, dan.user.ID, "editor")
	fx.flag(t, fx.a.ID, map[string]any{"no_download": true})

	wantError(t, callSession(t, fx.s, fx.bobSess, "PATCH", "/api/v1/nodes/"+fx.deep.ID, map[string]any{"no_download": false}), 403, apierr.Forbidden)

	for name, sess := range map[string]testSession{"viewer": fx.bobSess, "commenter": fx.annSess} {
		t.Run(name, func(t *testing.T) {
			wantError(t, callSession(t, fx.s, sess, "GET", "/api/v1/nodes/"+fx.deep.ID+"/content", nil), 403, apierr.DownloadRestricted)
			wantStatus(t, callSession(t, fx.s, sess, "GET", "/api/v1/nodes/"+fx.deep.ID+"/content?disposition=inline", nil), 200)
			wantError(t, callSession(t, fx.s, sess, "POST", "/api/v1/nodes/"+fx.deep.ID+"/links", map[string]any{}), 403, apierr.DownloadRestricted)
			wantError(t, callSession(t, fx.s, sess, "POST", "/api/v1/nodes/"+fx.deep.ID+"/copy",
				map[string]any{"parent": fx.c.ID, "name": "copy.txt"}), 403, apierr.DownloadRestricted)
		})
	}

	for name, sess := range map[string]testSession{"contributor": carl.sess, "editor": dan.sess} {
		t.Run(name, func(t *testing.T) {
			wantStatus(t, callSession(t, fx.s, sess, "GET", "/api/v1/nodes/"+fx.deep.ID+"/content", nil), 200)
			wantStatus(t, callSession(t, fx.s, sess, "POST", "/api/v1/nodes/"+fx.deep.ID+"/copy",
				map[string]any{"parent": fx.c.ID, "name": "copy-" + name + ".txt"}), 201)
		})
	}
}

// TestNoDownloadForViewersSpaceSettingRestrictsViewers: the team-space flag applies to every node of the
// space, for viewers only.
func TestNoDownloadForViewersSpaceSettingRestrictsViewers(t *testing.T) {
	fx := newSharingFixture(t)
	carl := newMember(t, fx, "carl@example.com")
	fx.grant(t, fx.a.ID, fx.bob.ID, "viewer")
	fx.grant(t, fx.a.ID, carl.user.ID, "contributor")
	fx.settings(t, fx.fld.ID, map[string]any{"no_download_for_viewers": true})

	wantError(t, callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/nodes/"+fx.sib.ID+"/content", nil), 403, apierr.DownloadRestricted)
	wantStatus(t, callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/nodes/"+fx.sib.ID+"/content?disposition=inline", nil), 200)
	wantError(t, callSession(t, fx.s, fx.bobSess, "POST", "/api/v1/nodes/"+fx.sib.ID+"/links", map[string]any{}), 403, apierr.DownloadRestricted)
	wantStatus(t, callSession(t, fx.s, carl.sess, "GET", "/api/v1/nodes/"+fx.sib.ID+"/content", nil), 200)
}

// TestCopyOfSubtreeWithRestrictedNodeIsRefused: a viewer may not copy a directory that holds a restricted
// file, since the copy would carry the file out. The flag here is on the file only, so the directory
// itself is not restricted and only the subtree rule applies.
func TestCopyOfSubtreeWithRestrictedNodeIsRefused(t *testing.T) {
	fx := newSharingFixture(t)
	carl := newMember(t, fx, "carl@example.com")
	fx.grant(t, fx.a.ID, fx.bob.ID, "viewer")
	fx.grant(t, fx.a.ID, carl.user.ID, "contributor")
	fx.flag(t, fx.deep.ID, map[string]any{"no_download": true})

	wantError(t, callSession(t, fx.s, fx.bobSess, "POST", "/api/v1/nodes/"+fx.b.ID+"/copy",
		map[string]any{"parent": fx.c.ID, "name": "b2"}), 403, apierr.DownloadRestricted)
	wantStatus(t, callSession(t, fx.s, carl.sess, "POST", "/api/v1/nodes/"+fx.b.ID+"/copy",
		map[string]any{"parent": fx.c.ID, "name": "b2"}), 201)
}

// --- 3. Editors can share -------------------------------------------------------------------------

// TestEditorsCanShareIsOffByDefaultAndFollowsTheNode: an editor may share only where the node allows it,
// and the flag reaches the nodes below.
func TestEditorsCanShareIsOffByDefaultAndFollowsTheNode(t *testing.T) {
	fx := newSharingFixture(t)
	fx.grant(t, fx.a.ID, fx.ann.ID, "editor")
	put := func(nodeID string) *httptest.ResponseRecorder {
		return callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+nodeID+"/permissions/"+fx.bob.ID,
			map[string]any{"role": "viewer", "notify": false})
	}
	wantError(t, put(fx.b.ID), 403, apierr.Forbidden)

	fx.flag(t, fx.a.ID, map[string]any{"editors_can_share": true})
	wantStatus(t, put(fx.b.ID), 200)
	wantStatus(t, put(fx.deep.ID), 200)
}

// TestEditorSharingCannotGrantOrChangeManager: an editor may give up to editor, never manager, and may
// not change or remove a manager's row.
func TestEditorSharingCannotGrantOrChangeManager(t *testing.T) {
	fx := newSharingFixture(t)
	dan := newMember(t, fx, "dan@example.com")
	fx.grant(t, fx.a.ID, fx.ann.ID, "editor")
	fx.grant(t, fx.a.ID, dan.user.ID, "manager")
	fx.flag(t, fx.a.ID, map[string]any{"editors_can_share": true})

	ann := func(method, path string, body map[string]any) *httptest.ResponseRecorder {
		return callSession(t, fx.s, fx.annSess, method, "/api/v1/nodes/"+fx.a.ID+"/permissions/"+path, body)
	}
	wantError(t, ann("PUT", fx.bob.ID, map[string]any{"role": "manager", "notify": false}), 403, apierr.Forbidden)
	wantError(t, ann("PUT", dan.user.ID, map[string]any{"role": "viewer", "notify": false}), 403, apierr.Forbidden)
	wantError(t, ann("DELETE", dan.user.ID, nil), 403, apierr.Forbidden)
	wantStatus(t, ann("PUT", fx.bob.ID, map[string]any{"role": "editor", "notify": false}), 200)
}

// TestSharingManagersOnlyOverridesEditorsCanShare: with the team space keeping sharing for managers, an
// editor is refused even where the node allows editors to share. An account key still shares.
func TestSharingManagersOnlyOverridesEditorsCanShare(t *testing.T) {
	fx := newSharingFixture(t)
	fx.grant(t, fx.a.ID, fx.ann.ID, "editor")
	fx.flag(t, fx.a.ID, map[string]any{"editors_can_share": true})
	fx.settings(t, fx.fld.ID, map[string]any{"sharing_managers_only": true})

	wantError(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+fx.b.ID+"/permissions/"+fx.bob.ID,
		map[string]any{"role": "viewer", "notify": false}), 403, apierr.Forbidden)
	fx.grant(t, fx.b.ID, fx.bob.ID, "viewer")
}

// TestDelegatedTokenCannotChangePermissions: a delegated token may not put or remove a permission, even
// where it has write access and the node allows editors to share.
func TestDelegatedTokenCannotChangePermissions(t *testing.T) {
	fx := newSharingFixture(t)
	fx.grant(t, fx.a.ID, fx.bob.ID, "viewer")
	fx.flag(t, fx.a.ID, map[string]any{"editors_can_share": true})
	tok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}, "path": "/a", "permissions": "write"})

	wantError(t, call(t, fx.s, "PUT", "/api/v1/nodes/"+fx.b.ID+"/permissions/"+fx.ann.ID, tok,
		map[string]any{"role": "viewer", "notify": false}), 403, apierr.Forbidden)
	wantError(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.a.ID+"/permissions/"+fx.bob.ID, tok, nil), 403, apierr.Forbidden)
	if rows := fx.rowsOn(t, fx.a.ID); len(rows) != 1 || rows[0].SubjectID != fx.bob.ID {
		t.Errorf("the token changed permissions: %+v", rows)
	}
}

// --- 4. Ownership transfer ------------------------------------------------------------------------

// TestTransferPersonalItemMovesIntoRecipientFiles: a personal item moves with its subtree into the new
// owner's My files, takes a free name when the new owner has one, leaves the old owner editor, and does
// not change usage.
func TestTransferPersonalItemMovesIntoRecipientFiles(t *testing.T) {
	fx := newSharingFixture(t)
	annPF := fx.personalFolder(t, fx.annSess)
	bobPF := fx.personalFolder(t, fx.bobSess)
	fx.sessionDir(t, fx.bobSess, bobPF, "/docs") // so the moved directory must take another name
	docs := fx.sessionDir(t, fx.annSess, annPF, "/docs")
	file := fx.sessionFile(t, fx.annSess, annPF, "/docs/a.txt", "alpha")
	fx.grant(t, docs.ID, fx.bob.ID, "viewer")
	before := fx.usage(t)

	rec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+docs.ID+"/transfer-ownership", map[string]any{"to": fx.bob.ID})
	wantStatus(t, rec, 200)

	moved := fx.nodeOf(t, docs.ID)
	if moved.OwnerID != fx.bob.ID || moved.FolderID != bobPF {
		t.Errorf("directory owner %q folder %q, want bob's and %q", moved.OwnerID, moved.FolderID, bobPF)
	}
	if moved.Name != "docs (1)" {
		t.Errorf("directory name = %q, want %q", moved.Name, "docs (1)")
	}
	if root := fx.rootOf(t, bobPF); moved.ParentID != root {
		t.Errorf("directory parent = %q, want the root of bob's My files", moved.ParentID)
	}
	if f := fx.nodeOf(t, file.ID); f.OwnerID != fx.bob.ID || f.FolderID != bobPF {
		t.Errorf("the file under it: owner %q folder %q, want bob's", f.OwnerID, f.FolderID)
	}
	ann := false
	for _, r := range fx.rowsOn(t, docs.ID) {
		if r.SubjectID == fx.ann.ID && r.Role == store.RoleEditor {
			ann = true
		}
	}
	if !ann {
		t.Error("the previous owner did not keep editor on the item")
	}
	if after := fx.usage(t); after != before {
		t.Errorf("usage changed from %+v to %+v", before, after)
	}
}

// TestTransferTeamItemStaysInPlace: in a team space an item keeps its folder and parent, and only its
// owner changes, for the whole subtree. The owner may transfer it, and a user who is not the owner may not.
func TestTransferTeamItemStaysInPlace(t *testing.T) {
	fx := newSharingFixture(t)
	fx.grant(t, fx.c.ID, fx.ann.ID, "editor")
	fx.grant(t, fx.c.ID, fx.bob.ID, "viewer")
	mine := fx.sessionFile(t, fx.annSess, fx.fld.ID, "/a/c/ann.txt", "mine")

	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+mine.ID+"/transfer-ownership",
		map[string]any{"to": fx.bob.ID}), 200)
	got := fx.nodeOf(t, mine.ID)
	if got.OwnerID != fx.bob.ID || got.FolderID != fx.fld.ID || got.ParentID != fx.c.ID {
		t.Errorf("after transfer owner %q folder %q parent %q; want bob's, unchanged place", got.OwnerID, got.FolderID, got.ParentID)
	}

	wantError(t, callSession(t, fx.s, fx.bobSess, "POST", "/api/v1/nodes/"+fx.sib.ID+"/transfer-ownership",
		map[string]any{"to": fx.ann.ID}), 403, apierr.Forbidden)

	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.b.ID+"/transfer-ownership", fx.key,
		map[string]any{"to": fx.ann.ID}), 200)
	if deep := fx.nodeOf(t, fx.deep.ID); deep.OwnerID != fx.ann.ID || deep.FolderID != fx.fld.ID {
		t.Errorf("subtree owner %q folder %q, want ann's and unchanged", deep.OwnerID, deep.FolderID)
	}
}

// TestTransferOwnershipRefusals: the recipient must be an active user of the same account. A user with no
// access gets not_found, and a user who reaches the item but does not own it gets forbidden.
func TestTransferOwnershipRefusals(t *testing.T) {
	fx := newSharingFixture(t)
	other, otherKey := newAccount(t, fx.s, fx.admin, "Other")
	zed, _ := verifiedUser(t, fx.s, otherKey, other.ID, "zed@example.com")
	pending := fx.pendingUser(t, "pending@example.com")
	fx.grant(t, fx.c.ID, fx.bob.ID, "viewer")
	ownerBefore := fx.nodeOf(t, fx.sib.ID).OwnerID
	transfer := func(cred, nodeID, to string) *httptest.ResponseRecorder {
		return call(t, fx.s, "POST", "/api/v1/nodes/"+nodeID+"/transfer-ownership", cred, map[string]any{"to": to})
	}

	wantError(t, transfer(fx.key, fx.sib.ID, zed.ID), 404, apierr.NotFound)
	wantError(t, transfer(fx.key, fx.sib.ID, pending.ID), 400, apierr.InvalidRequest)
	wantError(t, transfer(fx.key, fx.rootOf(t, fx.fld.ID), fx.bob.ID), 400, apierr.InvalidRequest)
	wantError(t, callSession(t, fx.s, fx.bobSess, "POST", "/api/v1/nodes/"+fx.deep.ID+"/transfer-ownership",
		map[string]any{"to": fx.ann.ID}), 404, apierr.NotFound)
	wantError(t, callSession(t, fx.s, fx.bobSess, "POST", "/api/v1/nodes/"+fx.sib.ID+"/transfer-ownership",
		map[string]any{"to": fx.ann.ID}), 403, apierr.Forbidden)
	if got := fx.nodeOf(t, fx.sib.ID); got.OwnerID != ownerBefore {
		t.Errorf("a refused transfer changed the owner from %q to %q", ownerBefore, got.OwnerID)
	}
}

// --- 5. User delete with transfer_to --------------------------------------------------------------

// TestDeleteUserTransferKeepsUsageAndMakesDirectory: the deleted user's My files land in a new directory
// named after the user, in the recipient's My files, and usage does not change.
func TestDeleteUserTransferKeepsUsageAndMakesDirectory(t *testing.T) {
	fx := newSharingFixture(t)
	annPF := fx.personalFolder(t, fx.annSess)
	bobPF := fx.personalFolder(t, fx.bobSess)
	fx.sessionDir(t, fx.annSess, annPF, "/notes")
	fx.sessionFile(t, fx.annSess, annPF, "/notes/n.txt", "note")
	fx.sessionFile(t, fx.annSess, annPF, "/b.txt", "bee")
	before := fx.usage(t)

	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.ann.ID+"?transfer_to="+fx.bob.ID, fx.key, nil), 204)

	// The bytes and files are the same. The folder count drops by one, since the deleted user's My files
	// go with the user: usage counts top-level folders.
	after := fx.usage(t)
	if after.used != before.used || after.reserved != before.reserved || after.files != before.files {
		t.Errorf("usage changed from %+v to %+v", before, after)
	}
	if after.folders != before.folders-1 {
		t.Errorf("folders went from %d to %d, want one fewer", before.folders, after.folders)
	}
	var dir *nodeJSON
	children := fx.childrenOf(t, fx.rootOf(t, bobPF))
	for i := range children {
		if children[i].Name == "ann@example.com (files)" {
			dir = &children[i]
		}
	}
	if dir == nil {
		t.Fatalf("bob's My files hold %v, want a directory named for ann", namesOf(children))
	}
	if got := namesOf(fx.childrenOf(t, dir.ID)); len(got) != 2 {
		t.Errorf("the directory holds %v, want notes and b.txt", got)
	}
	wantError(t, call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/folders/"+annPF, fx.key, nil), 404, apierr.NotFound)
}

// TestDeleteUserTransferUsesDisplayNameAndRenamesClash: the directory takes the user's display name, and
// a second user with the same name gets a name of its own.
func TestDeleteUserTransferUsesDisplayNameAndRenamesClash(t *testing.T) {
	fx := newSharingFixture(t)
	bobPF := fx.personalFolder(t, fx.bobSess)
	for _, email := range []string{"carl@example.com", "carl2@example.com"} {
		u := fx.activeUser(t, email, "Carl Reed", "")
		pf, err := fx.s.store.PersonalFolder(context.Background(), fx.acc.ID, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		putNode(t, fx.s, fx.key, pf.ID, "/x.txt", "x")
		wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/accounts/"+fx.acc.ID+"/users/"+u.ID+"?transfer_to="+fx.bob.ID, fx.key, nil), 204)
	}
	var names []string
	for _, n := range fx.childrenOf(t, fx.rootOf(t, bobPF)) {
		if strings.HasPrefix(n.Name, "Carl Reed (files)") {
			names = append(names, n.Name)
		}
	}
	if len(names) != 2 || names[0] == names[1] {
		t.Errorf("directories for the two Carls = %v, want two different names", names)
	}
}

// TestDeleteUserTransferRefusals: transfer_to and delete_files cannot both be given, a user cannot take
// its own files, and a pending user cannot receive them. A refused delete changes nothing.
func TestDeleteUserTransferRefusals(t *testing.T) {
	fx := newSharingFixture(t)
	annPF := fx.personalFolder(t, fx.annSess)
	fx.sessionFile(t, fx.annSess, annPF, "/b.txt", "bee")
	pending := fx.pendingUser(t, "pending@example.com")
	del := func(query string) *httptest.ResponseRecorder {
		return call(t, fx.s, "DELETE", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.ann.ID+"?"+query, fx.key, nil)
	}

	wantError(t, del("transfer_to="+fx.bob.ID+"&delete_files=true"), 400, apierr.InvalidRequest)
	wantError(t, del("transfer_to="+fx.ann.ID), 400, apierr.InvalidRequest)
	wantError(t, del("transfer_to="+pending.ID), 400, apierr.InvalidRequest)
	wantStatus(t, call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.ann.ID, fx.key, nil), 200)
	if children := fx.childrenOf(t, fx.rootOf(t, annPF)); len(children) != 1 {
		t.Errorf("a refused delete moved files: ann's My files hold %v", namesOf(children))
	}
}

// --- 6. Access requests ---------------------------------------------------------------------------

// TestAccessRequestRecordedOnceAndMailsManagers: a user who reaches a node may ask for more on it. A repeat
// collapses into the pending row, and managers are mailed only for a new request.
func TestAccessRequestRecordedOnceAndMailsManagers(t *testing.T) {
	fx := newSharingFixture(t)
	dan := newMember(t, fx, "dan@example.com")
	fx.grant(t, fx.rootOf(t, fx.fld.ID), dan.user.ID, "manager")
	base := len(fx.sent(t))

	ask := "/api/v1/nodes/" + fx.deep.ID + "/access-requests"
	rec := callSession(t, fx.s, fx.annSess, "POST", ask, map[string]any{"role": "editor", "message": "please"})
	wantStatus(t, rec, 202)
	if rec.Body.Len() != 0 {
		t.Errorf("202 body = %q, want empty", rec.Body)
	}
	msgs := fx.sent(t)[base:]
	if len(msgs) != 1 || msgs[0].To != dan.user.Email || !strings.Contains(msgs[0].Body, "please") {
		t.Fatalf("mail to managers = %+v, want one to dan with the message", msgs)
	}

	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", ask, map[string]any{"role": "editor"}), 202)
	if len(fx.sent(t)) != base+1 {
		t.Errorf("a repeat request mailed again")
	}
	list := decode[listResponse[accessRequestJSON]](t, call(t, fx.s, "GET", ask, fx.key, nil))
	if len(list.Items) != 1 || list.Items[0].Status != store.RequestPending || list.Items[0].UserID != fx.ann.ID {
		t.Errorf("requests on the node = %+v, want one pending from ann", list.Items)
	}
}

// TestAccessRequestAnswersMissingAndUnreachableAlike: a missing node, a node in another account, and a
// key or a delegated token asking all get 202 with no body and record nothing, so the answer tells nothing
// about the nodes that exist. A user with no role on a node in its own account is recorded: having no
// role is exactly why it asks.
func TestAccessRequestAnswersMissingAndUnreachableAlike(t *testing.T) {
	fx := newSharingFixture(t)
	pending := func() int {
		rec := call(t, fx.s, "GET", "/api/v1/access-requests", fx.key, nil)
		wantStatus(t, rec, 200)
		return len(decode[listResponse[accessRequestJSON]](t, rec).Items)
	}
	post := func(sess testSession, nodeID, role string) *httptest.ResponseRecorder {
		return callSession(t, fx.s, sess, "POST", "/api/v1/nodes/"+nodeID+"/access-requests", map[string]any{"role": role})
	}

	// A node in another account, which ann's account cannot see.
	acc2, key2 := newAccount(t, fx.s, fx.admin, "Other")
	fld2 := newFolder(t, fx.s, key2, acc2.ID, "Team")
	foreign := putNode(t, fx.s, key2, fld2.ID, "/x.txt", "x")

	for name, nodeID := range map[string]string{"missing": id.New("nod"), "foreign": foreign.ID} {
		rec := post(fx.annSess, nodeID, "viewer")
		wantStatus(t, rec, 202)
		if rec.Body.Len() != 0 {
			t.Errorf("%s: 202 body = %q, want empty", name, rec.Body)
		}
	}
	// A key and a delegated token get the same silent 202, whatever the node.
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.deep.ID+"/access-requests", fx.key,
		map[string]any{"role": "viewer"}), 202)
	if n := pending(); n != 0 {
		t.Fatalf("%d pending requests were recorded for the silent cases, want none", n)
	}

	// A user with no role on a node of its own account is recorded.
	wantStatus(t, post(fx.annSess, fx.deep.ID, "editor"), 202)
	if n := pending(); n != 1 {
		t.Errorf("a no-role request: %d pending, want 1", n)
	}

	// Validation still refuses a bad body, which says nothing about any node.
	wantError(t, post(fx.annSess, fx.deep.ID, "manager"), 400, apierr.InvalidRequest)
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.deep.ID+"/access-requests",
		map[string]any{"role": "viewer", "message": strings.Repeat("m", 501)}), 400, apierr.InvalidRequest)
}

// TestAccessRequestRateLimit: a user may make 20 requests an hour, and the 21st is refused.
func TestAccessRequestRateLimit(t *testing.T) {
	fx := newSharingFixture(t)
	eve := newMember(t, fx, "eve@example.com")
	for i := 0; i < 20; i++ {
		wantStatus(t, callSession(t, fx.s, eve.sess, "POST", "/api/v1/nodes/"+fx.deep.ID+"/access-requests",
			map[string]any{"role": "editor"}), 202)
	}
	wantError(t, callSession(t, fx.s, eve.sess, "POST", "/api/v1/nodes/"+fx.deep.ID+"/access-requests",
		map[string]any{"role": "editor"}), 429, apierr.RateLimited)
}

// TestAccessRequestApproveRaisesRoleOnly: an approval grants the asked role but never lowers a role the
// requester gained in the meantime.
func TestAccessRequestApproveRaisesRoleOnly(t *testing.T) {
	fx := newSharingFixture(t)
	dan := newMember(t, fx, "dan@example.com")
	fx.grant(t, fx.rootOf(t, fx.fld.ID), dan.user.ID, "manager")
	fx.grant(t, fx.a.ID, fx.ann.ID, "viewer")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.deep.ID+"/access-requests",
		map[string]any{"role": "editor"}), 202)
	arq := onlyPending(t, fx)
	fx.grant(t, fx.deep.ID, fx.ann.ID, "contributor")

	wantStatus(t, callSession(t, fx.s, dan.sess, "POST", "/api/v1/access-requests/"+arq+"/approve",
		map[string]any{"role": "viewer"}), 200)
	rows := fx.rowsOn(t, fx.deep.ID)
	if len(rows) != 1 || rows[0].SubjectID != fx.ann.ID || rows[0].Role != store.RoleContributor {
		t.Errorf("rows after approving a viewer request = %+v, want ann still contributor", rows)
	}
}

// TestAccessRequestApprovalCappedByApprover: an editor who may share approves a request. Manager is not a
// role a request can ask for, so an approval naming it is refused as an invalid role, before the approver's
// own role is compared. Approving with no role grants what was asked. The spec's 403 for a grant above the
// approver's role is therefore not reachable through approve: every approver holds editor or more, and
// approve grants at most editor.
func TestAccessRequestApprovalCappedByApprover(t *testing.T) {
	fx := newSharingFixture(t)
	dan := newMember(t, fx, "dan@example.com")
	fx.grant(t, fx.c.ID, dan.user.ID, "editor")
	fx.flag(t, fx.c.ID, map[string]any{"editors_can_share": true})
	fx.grant(t, fx.c.ID, fx.bob.ID, "viewer")
	wantStatus(t, callSession(t, fx.s, fx.bobSess, "POST", "/api/v1/nodes/"+fx.sib.ID+"/access-requests",
		map[string]any{"role": "editor"}), 202)
	arq := onlyPending(t, fx)

	wantError(t, callSession(t, fx.s, dan.sess, "POST", "/api/v1/access-requests/"+arq+"/approve",
		map[string]any{"role": "manager"}), 400, apierr.InvalidRequest)
	wantStatus(t, callSession(t, fx.s, dan.sess, "POST", "/api/v1/access-requests/"+arq+"/approve", map[string]any{}), 200)
	if rows := fx.rowsOn(t, fx.sib.ID); len(rows) != 1 || rows[0].Role != store.RoleEditor || rows[0].SubjectID != fx.bob.ID {
		t.Errorf("rows after approval = %+v, want bob editor", rows)
	}
	all, _, err := fx.s.store.AuditLog(context.Background(), fx.acc.ID, "", true, store.Page{Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if !auditHas(all, "access.approve", arq) || !auditHas(all, "access.request", arq) {
		t.Error("an approval is not audited with its request")
	}
}

// TestAccessRequestDecisionsAreManagersOnlyAndOnce: a viewer may not decide, a manager decides once, and
// each request and decision is audited.
func TestAccessRequestDecisionsAreManagersOnlyAndOnce(t *testing.T) {
	fx := newSharingFixture(t)
	dan := newMember(t, fx, "dan@example.com")
	fx.grant(t, fx.rootOf(t, fx.fld.ID), dan.user.ID, "manager")
	fx.grant(t, fx.sib.ID, fx.ann.ID, "viewer")
	fx.grant(t, fx.c.ID, fx.bob.ID, "viewer")
	wantStatus(t, callSession(t, fx.s, fx.bobSess, "POST", "/api/v1/nodes/"+fx.sib.ID+"/access-requests",
		map[string]any{"role": "editor"}), 202)
	arq := onlyPending(t, fx)

	wantError(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/access-requests/"+arq+"/deny", nil), 403, apierr.Forbidden)
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/access-requests/"+arq+"/approve", nil), 403, apierr.Forbidden)
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.sib.ID+"/access-requests", nil), 403, apierr.Forbidden)

	wantStatus(t, callSession(t, fx.s, dan.sess, "POST", "/api/v1/access-requests/"+arq+"/deny", nil), 200)
	wantError(t, callSession(t, fx.s, dan.sess, "POST", "/api/v1/access-requests/"+arq+"/deny", nil), 409, apierr.Conflict)
	wantError(t, callSession(t, fx.s, dan.sess, "POST", "/api/v1/access-requests/"+arq+"/approve", nil), 409, apierr.Conflict)
	if rows := fx.rowsOn(t, fx.sib.ID); len(rows) != 1 || rows[0].SubjectID != fx.ann.ID {
		t.Errorf("a denied request granted something: %+v", rows)
	}

	all, _, err := fx.s.store.AuditLog(context.Background(), fx.acc.ID, "", true, store.Page{Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if !auditHas(all, "access.request", arq) {
		t.Error("no access.request audit entry for the request")
	}
	if !auditHas(all, "access.deny", arq) {
		t.Error("no access.deny audit entry for the request")
	}
}

// onlyPending returns the ID of the one pending access request in the account, read as the account key.
func onlyPending(t *testing.T, fx *sharingFixture) string {
	t.Helper()
	list := decode[listResponse[accessRequestJSON]](t, call(t, fx.s, "GET", "/api/v1/access-requests", fx.key, nil))
	if len(list.Items) != 1 {
		t.Fatalf("pending requests = %+v, want one", list.Items)
	}
	return list.Items[0].ID
}

// --- 7. Mail --------------------------------------------------------------------------------------

// TestShareMailUsesRecipientLocale: a share to a user whose locale is Hebrew is written in Hebrew, and its
// subject has no line break.
func TestShareMailUsesRecipientLocale(t *testing.T) {
	fx := newSharingFixture(t)
	heidi := fx.activeUser(t, "heidi@example.com", "", "he")
	base := len(fx.sent(t))

	rec := call(t, fx.s, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/permissions/"+heidi.ID, fx.key, map[string]any{"role": "viewer"})
	wantStatus(t, rec, 200)
	if got := decode[setPermissionJSON](t, rec); !got.Notified {
		t.Fatalf("notified = false, reason %q", got.Reason)
	}
	msgs := fx.sent(t)[base:]
	if len(msgs) != 1 || msgs[0].To != heidi.Email {
		t.Fatalf("mail = %+v, want one to heidi", msgs)
	}
	data := shareData{Actor: fx.acc.Name, Item: "file.txt", Role: mail.RoleLabel("he", "viewer"), Link: fx.s.nodeLink(fx.deep.ID)}
	want, _, err := fx.s.templates.Render("share", "he", data)
	if err != nil {
		t.Fatal(err)
	}
	en, _, err := fx.s.templates.Render("share", "en", data)
	if err != nil {
		t.Fatal(err)
	}
	if want == en {
		t.Fatal("the Hebrew and English subjects are the same; the test cannot tell them apart")
	}
	if msgs[0].Subject != want {
		t.Errorf("subject = %q, want the Hebrew subject %q", msgs[0].Subject, want)
	}
	if strings.ContainsAny(msgs[0].Subject, "\r\n") {
		t.Errorf("subject has a line break: %q", msgs[0].Subject)
	}
}

// TestShareMailNotifyAndMessageText: notify false sends nothing, and the note is put in the body as typed.
func TestShareMailNotifyAndMessageText(t *testing.T) {
	fx := newSharingFixture(t)
	base := len(fx.sent(t))

	rec := call(t, fx.s, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/permissions/"+fx.ann.ID, fx.key,
		map[string]any{"role": "viewer", "notify": false, "message": "not for mail"})
	got := decode[setPermissionJSON](t, rec)
	if got.Notified || got.Reason != "not_requested" || len(fx.sent(t)) != base {
		t.Errorf("notify false: notified %v reason %q, mails %d", got.Notified, got.Reason, len(fx.sent(t))-base)
	}

	note := "Line one\nLine two"
	rec = call(t, fx.s, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/permissions/"+fx.bob.ID, fx.key,
		map[string]any{"role": "viewer", "message": note})
	if got := decode[setPermissionJSON](t, rec); !got.Notified {
		t.Fatalf("notified = false, reason %q", got.Reason)
	}
	msgs := fx.sent(t)[base:]
	if len(msgs) != 1 || !strings.Contains(msgs[0].Body, note) {
		t.Errorf("body does not carry the note verbatim: %+v", msgs)
	}

	wantError(t, call(t, fx.s, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/permissions/"+fx.ann.ID, fx.key,
		map[string]any{"role": "viewer", "message": strings.Repeat("m", 501)}), 400, apierr.InvalidRequest)
}

// TestShareMailSkipsGroupsOverFifty: a group of more than 50 members gets no mail, and the reply says why.
func TestShareMailSkipsGroupsOverFifty(t *testing.T) {
	fx := newSharingFixture(t)
	grp := fx.group(t, "everyone")
	for i := 0; i < 51; i++ {
		u := fx.activeUser(t, fmt.Sprintf("member%02d@example.com", i), "", "")
		fx.inGroup(t, grp, u.ID, true)
	}
	base := len(fx.sent(t))

	rec := call(t, fx.s, "PUT", "/api/v1/nodes/"+fx.a.ID+"/permissions/"+grp, fx.key, map[string]any{"role": "viewer"})
	wantStatus(t, rec, 200)
	got := decode[setPermissionJSON](t, rec)
	if got.Notified || got.Reason != "too_many_recipients" || len(fx.sent(t)) != base {
		t.Errorf("large group: notified %v reason %q, mails %d", got.Notified, got.Reason, len(fx.sent(t))-base)
	}
}

// TestGrantsAliasSendsNoMail: the /grants route sets a permission without mail, as it always has.
func TestGrantsAliasSendsNoMail(t *testing.T) {
	fx := newSharingFixture(t)
	base := len(fx.sent(t))
	wantStatus(t, call(t, fx.s, "PUT", "/api/v1/accounts/"+fx.acc.ID+"/folders/"+fx.fld.ID+"/grants/"+fx.ann.ID, fx.key,
		map[string]any{"level": "write"}), 200)
	if len(fx.sent(t)) != base {
		t.Errorf("the grants route sent %d mails", len(fx.sent(t))-base)
	}
}

// TestMailTextReplacesLineBreaks: mailText turns every kind of line break into a space, so no value can
// start a header.
func TestMailTextReplacesLineBreaks(t *testing.T) {
	if got, want := mailText("a\r\nBcc: x@example.com\ny\rz"), "a Bcc: x@example.com y z"; got != want {
		t.Errorf("mailText = %q, want %q", got, want)
	}
}

// TestMailSubjectHasNoLineBreakFromActorName: an actor's display name with a line break never reaches a
// subject. The API refuses a control character in a display name, so this end-to-end path cannot be reached
// and the test skips; TestMailTextReplacesLineBreaks covers the helper that protects the subject.
func TestMailSubjectHasNoLineBreakFromActorName(t *testing.T) {
	fx := newSharingFixture(t)
	rec := callSession(t, fx.s, fx.annSess, "PATCH", "/api/v1/me", map[string]any{"display_name": "Ann\r\nBcc: x@example.com"})
	if rec.Code != 200 {
		t.Skipf("the API refuses a display name with a line break (%d); the path is unreachable end to end", rec.Code)
	}
	base := len(fx.sent(t))
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/permissions/"+fx.bob.ID,
		map[string]any{"role": "viewer"}), 200)
	msgs := fx.sent(t)[base:]
	if len(msgs) != 1 {
		t.Fatalf("mails = %d, want 1", len(msgs))
	}
	if strings.ContainsAny(msgs[0].Subject, "\r\n") {
		t.Errorf("subject has a line break: %q", msgs[0].Subject)
	}
}

// --- 8. Members only ------------------------------------------------------------------------------

// TestMembersOnlyAdmitsOnlyHoldersOfTheRoot: below the root, a members-only space names only users and
// groups that already hold something on the root. The root itself is never refused.
func TestMembersOnlyAdmitsOnlyHoldersOfTheRoot(t *testing.T) {
	fx := newSharingFixture(t)
	root := fx.rootOf(t, fx.fld.ID)
	carl := newMember(t, fx, "carl@example.com")
	fx.grant(t, root, fx.bob.ID, "viewer")
	staff := fx.group(t, "staff")
	fx.grant(t, root, staff, "viewer")
	fx.inGroup(t, staff, carl.user.ID, true)
	outsiders := fx.group(t, "outsiders")
	fx.settings(t, fx.fld.ID, map[string]any{"members_only": true})

	put := func(nodeID, subject string) *httptest.ResponseRecorder {
		return call(t, fx.s, "PUT", "/api/v1/nodes/"+nodeID+"/permissions/"+subject, fx.key, map[string]any{"role": "viewer", "notify": false})
	}
	wantError(t, put(fx.b.ID, fx.ann.ID), 409, apierr.Conflict)
	wantStatus(t, put(fx.b.ID, fx.bob.ID), 200)
	wantStatus(t, put(fx.c.ID, carl.user.ID), 200)
	wantError(t, put(fx.c.ID, outsiders), 409, apierr.Conflict)
	wantStatus(t, put(root, fx.ann.ID), 200)
}

// --- 9. Hidden spaces -----------------------------------------------------------------------------

// TestHiddenSpaceLeavesListingNotAccess: a user hides a team space from its own list, and access does not
// change. Only a user can hide, a user who cannot reach the space gets not_found, and a personal folder
// cannot be hidden.
func TestHiddenSpaceLeavesListingNotAccess(t *testing.T) {
	fx := newSharingFixture(t)
	fx.grant(t, fx.rootOf(t, fx.fld.ID), fx.bob.ID, "viewer")
	bobPF := fx.personalFolder(t, fx.bobSess)
	hide := func(sess testSession, fld, method string) *httptest.ResponseRecorder {
		return callSession(t, fx.s, sess, method, "/api/v1/folders/"+fld+"/hidden", nil)
	}
	list := func(query string) []folderAccessJSON {
		rec := callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/folders"+query, nil)
		wantStatus(t, rec, 200)
		return decode[listResponse[folderAccessJSON]](t, rec).Items
	}
	has := func(items []folderAccessJSON, fid string) bool {
		for _, f := range items {
			if f.ID == fid {
				return true
			}
		}
		return false
	}

	wantStatus(t, hide(fx.bobSess, fx.fld.ID, "PUT"), 204)
	if has(list(""), fx.fld.ID) {
		t.Error("a hidden space is still in the default listing")
	}
	hidden := list("?hidden=true")
	if !has(hidden, fx.fld.ID) || has(hidden, bobPF) {
		t.Errorf("?hidden=true lists %+v, want the team space and not the personal folder", hidden)
	}
	for _, f := range hidden {
		if f.ID == fx.fld.ID && !f.Hidden {
			t.Error("the hidden space is not marked hidden")
		}
	}
	wantStatus(t, fx.stat(t, fx.bobSess, fx.deep.ID), 200)

	wantStatus(t, hide(fx.bobSess, fx.fld.ID, "DELETE"), 204)
	if !has(list(""), fx.fld.ID) || has(list("?hidden=true"), fx.fld.ID) {
		t.Error("a shown space did not come back to the default listing")
	}

	wantError(t, call(t, fx.s, "PUT", "/api/v1/folders/"+fx.fld.ID+"/hidden", fx.key, nil), 403, apierr.Forbidden)
	wantError(t, hide(fx.annSess, fx.fld.ID, "PUT"), 404, apierr.NotFound)
	wantError(t, hide(fx.bobSess, bobPF, "PUT"), 400, apierr.InvalidRequest)
}

// --- 10. Audit privacy ----------------------------------------------------------------------------

// TestPersonalAuditEntriesArePrivate: entries about a personal folder are seen only by its user, and a
// transfer is private to the recipient, which the store's reader filter shows. A team-space entry is seen
// by every reader.
func TestPersonalAuditEntriesArePrivate(t *testing.T) {
	fx := newSharingFixture(t)
	annPF := fx.personalFolder(t, fx.annSess)
	docs := fx.sessionDir(t, fx.annSess, annPF, "/docs")
	keep := fx.sessionFile(t, fx.annSess, annPF, "/keep.txt", "keep")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+docs.ID+"/transfer-ownership",
		map[string]any{"to": fx.bob.ID}), 200)

	past := time.Now().Add(-48 * time.Hour)
	fx.insertPermission(t, &store.Permission{NodeID: keep.ID, AccountID: fx.acc.ID, SubjectKind: store.SubjectUser,
		SubjectID: fx.bob.ID, Role: store.RoleViewer, CreatedAt: past, CreatedBy: "test", ExpiresAt: &past})
	fx.insertPermission(t, &store.Permission{NodeID: fx.a.ID, AccountID: fx.acc.ID, SubjectKind: store.SubjectUser,
		SubjectID: fx.bob.ID, Role: store.RoleViewer, CreatedAt: past, CreatedBy: "test", ExpiresAt: &past})
	sw := sweep.New(sweep.Options{Store: fx.s.store, Blobs: fx.s.blobs, DeleteGrace: time.Hour})
	if _, err := sw.Once(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}

	transferred := func(e *store.AuditEntry) bool { return e.Action == "node.transfer" && e.Target == docs.ID }
	swept := func(e *store.AuditEntry) bool { return e.Action == "permission.expired" && e.Target == keep.ID }
	teamSwept := func(e *store.AuditEntry) bool { return e.Action == "permission.expired" && e.Target == fx.a.ID }
	count := func(entries []*store.AuditEntry, pred func(*store.AuditEntry) bool) int {
		n := 0
		for _, e := range entries {
			if pred(e) {
				n++
			}
		}
		return n
	}

	ann := fx.auditSeenBy(t, fx.ann.ID)
	bob := fx.auditSeenBy(t, fx.bob.ID)
	nobody := fx.auditSeenBy(t, "")
	if count(bob, transferred) != 1 || count(ann, transferred) != 0 || count(nobody, transferred) != 0 {
		t.Errorf("transfer entry seen by bob %d, ann %d, nobody %d; want 1, 0, 0",
			count(bob, transferred), count(ann, transferred), count(nobody, transferred))
	}
	if count(ann, swept) != 1 || count(bob, swept) != 0 || count(nobody, swept) != 0 {
		t.Errorf("sweep entry about ann's folder seen by ann %d, bob %d, nobody %d; want 1, 0, 0",
			count(ann, swept), count(bob, swept), count(nobody, swept))
	}
	if count(nobody, teamSwept) != 1 || count(ann, teamSwept) != 1 || count(bob, teamSwept) != 1 {
		t.Errorf("team-space sweep entry is not seen by every reader")
	}
}

// TestAccessRequestByDelegatedTokenIsSilentAndUnrecorded: a delegated user token never asks for access. It
// gets the same 202 with no body as every other silent answer, and nothing is recorded, even for a node
// the user would be allowed to ask about through a session.
func TestAccessRequestByDelegatedTokenIsSilentAndUnrecorded(t *testing.T) {
	fx := newSharingFixture(t)
	tok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"user": fx.ann.ID, "folders": []string{fx.fld.ID}})
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.deep.ID+"/access-requests", tok, map[string]any{"role": "editor"})
	wantStatus(t, rec, 202)
	if rec.Body.Len() != 0 {
		t.Errorf("202 body = %q, want empty", rec.Body)
	}
	list := decode[listResponse[accessRequestJSON]](t, call(t, fx.s, "GET", "/api/v1/access-requests", fx.key, nil))
	if len(list.Items) != 0 {
		t.Errorf("a token request was recorded: %+v", list.Items)
	}
}

// TestAccessRequestOnAnotherUsersPersonalFolderMailsItsOwner: a request for a node in another user's My files
// is recorded, and the owner of that folder is mailed, as Drive does.
func TestAccessRequestOnAnotherUsersPersonalFolderMailsItsOwner(t *testing.T) {
	fx := newSharingFixture(t)
	bob := newMember(t, fx, "bob-pf@example.com")
	ann := newMember(t, fx, "ann-pf@example.com")
	pf := fx.personalFolder(t, ann.sess)
	note := putNode(t, fx.s, fx.key, pf, "/note.txt", "private")
	base := len(fx.sent(t))

	rec := callSession(t, fx.s, bob.sess, "POST", "/api/v1/nodes/"+note.ID+"/access-requests",
		map[string]any{"role": "viewer", "message": "may I see it"})
	wantStatus(t, rec, 202)
	msgs := fx.sent(t)[base:]
	if len(msgs) != 1 || msgs[0].To != ann.user.Email {
		t.Fatalf("mail = %+v, want one to the folder's owner", msgs)
	}
	list := decode[listResponse[accessRequestJSON]](t, call(t, fx.s, "GET", "/api/v1/nodes/"+note.ID+"/access-requests", fx.key, nil))
	if len(list.Items) != 1 || list.Items[0].UserID != bob.user.ID {
		t.Errorf("requests on the file = %+v, want one from bob", list.Items)
	}
}

// TestTransferClearsEditorsCanShare: an item moved into the recipient's My files loses editors_can_share,
// with everything below it. The caller manages the recipient's files, not the item, so the flag does not
// come along.
func TestTransferClearsEditorsCanShare(t *testing.T) {
	fx := newSharingFixture(t)
	annPF := fx.personalFolder(t, fx.annSess)
	bobPF := fx.personalFolder(t, fx.bobSess)
	docs := fx.sessionDir(t, fx.annSess, annPF, "/docs")
	file := fx.sessionFile(t, fx.annSess, annPF, "/docs/a.txt", "alpha")
	fx.flag(t, docs.ID, map[string]any{"editors_can_share": true})
	fx.flag(t, file.ID, map[string]any{"editors_can_share": true})

	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+docs.ID+"/transfer-ownership",
		map[string]any{"to": fx.bob.ID}), 200)

	if n := fx.nodeOf(t, docs.ID); n.FolderID != bobPF || n.EditorsCanShare {
		t.Errorf("moved directory folder %q editors_can_share %v, want bob's folder and false", n.FolderID, n.EditorsCanShare)
	}
	if n := fx.nodeOf(t, file.ID); n.EditorsCanShare {
		t.Error("the file below the moved directory kept editors_can_share")
	}
}

// TestDeleteUserTransferClearsEditorsCanShare: the files a deleted user hands over lose editors_can_share,
// since the deleting owner does not manage the recipient's My files.
func TestDeleteUserTransferClearsEditorsCanShare(t *testing.T) {
	fx := newSharingFixture(t)
	annPF := fx.personalFolder(t, fx.annSess)
	bobPF := fx.personalFolder(t, fx.bobSess)
	notes := fx.sessionDir(t, fx.annSess, annPF, "/notes")
	fx.flag(t, notes.ID, map[string]any{"editors_can_share": true})

	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.ann.ID+"?transfer_to="+fx.bob.ID,
		fx.key, nil), 204)

	var dir *nodeJSON
	children := fx.childrenOf(t, fx.rootOf(t, bobPF))
	for i := range children {
		if children[i].Name == "ann@example.com (files)" {
			dir = &children[i]
		}
	}
	if dir == nil {
		t.Fatalf("bob's My files hold %v, want a directory for ann", namesOf(children))
	}
	if n := fx.nodeOf(t, notes.ID); n.EditorsCanShare {
		t.Error("the moved directory kept editors_can_share")
	}
}

// TestShareBurstLeavesVerificationMailWorking: shares have buckets of their own. Thirty shares, each to a
// different active user, would use up the account's verification allowance under the shared budget. They
// do not, so a new user still gets its verification mail.
func TestShareBurstLeavesVerificationMailWorking(t *testing.T) {
	fx := newSharingFixture(t)
	for i := 0; i < 10; i++ {
		m := newMember(t, fx, fmt.Sprintf("m%d@example.com", i))
		for j := 0; j < 3; j++ {
			wantStatus(t, call(t, fx.s, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/permissions/"+m.user.ID, fx.key,
				map[string]any{"role": "viewer"}), 200)
		}
	}
	base := len(fx.sent(t))
	rec := call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users", fx.key,
		map[string]any{"email": "newcomer@example.com", "verification": "email"})
	wantStatus(t, rec, 201)
	msgs := fx.sent(t)[base:]
	if len(msgs) != 1 || msgs[0].To != "newcomer@example.com" {
		t.Fatalf("verification mail after the share burst = %+v, want one to newcomer", msgs)
	}
}

// TestShareToOneUserStopsMailingAtItsBucket: a share mails the subject at most ten times an hour. Later
// shares still happen, and say so: notified is false with the reason mail_budget.
func TestShareToOneUserStopsMailingAtItsBucket(t *testing.T) {
	fx := newSharingFixture(t)
	for i := 0; i < 11; i++ {
		rec := call(t, fx.s, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/permissions/"+fx.bob.ID, fx.key,
			map[string]any{"role": "viewer", "message": "hi"})
		wantStatus(t, rec, 200)
		notice := decode[setPermissionJSON](t, rec)
		if i < 10 && !notice.Notified {
			t.Fatalf("share %d not mailed: reason %q", i+1, notice.Reason)
		}
		if i == 10 && (notice.Notified || notice.Reason != "mail_budget") {
			t.Errorf("share 11: notified %v reason %q, want false and mail_budget", notice.Notified, notice.Reason)
		}
	}
}

// TestRequestFloodFromOneUserStopsMailing: one requester gets three request mails an hour. Its further
// requests are recorded and listed, but not mailed, and another user's request still mails the managers.
func TestRequestFloodFromOneUserStopsMailing(t *testing.T) {
	fx := newSharingFixture(t)
	dan := newMember(t, fx, "dan@example.com")
	fx.grant(t, fx.rootOf(t, fx.fld.ID), dan.user.ID, "manager")
	nodes := []nodeJSON{fx.deep, fx.sib,
		putNode(t, fx.s, fx.key, fx.fld.ID, "/a/c/y.txt", "y"),
		putNode(t, fx.s, fx.key, fx.fld.ID, "/a/c/z.txt", "z")}
	base := len(fx.sent(t))
	for _, n := range nodes {
		wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+n.ID+"/access-requests",
			map[string]any{"role": "viewer"}), 202)
	}
	if got := len(fx.sent(t)) - base; got != 3 {
		t.Fatalf("mails for ann's four requests = %d, want 3", got)
	}
	list := decode[listResponse[accessRequestJSON]](t, call(t, fx.s, "GET",
		"/api/v1/nodes/"+nodes[3].ID+"/access-requests", fx.key, nil))
	if len(list.Items) != 1 || list.Items[0].UserID != fx.ann.ID {
		t.Errorf("the fourth request was not recorded: %+v", list.Items)
	}

	base = len(fx.sent(t))
	wantStatus(t, callSession(t, fx.s, fx.bobSess, "POST", "/api/v1/nodes/"+nodes[2].ID+"/access-requests",
		map[string]any{"role": "viewer"}), 202)
	if got := len(fx.sent(t)) - base; got != 1 {
		t.Errorf("bob's request mails = %d, want 1", got)
	}
}

// TestRequesterBudgetIsChargedOncePerRequest: a request to a node with five managers mails all five, since the
// requester's bucket is charged once for the request, not once per manager. Ann's fourth request in the hour
// is recorded and mails nobody.
func TestRequesterBudgetIsChargedOncePerRequest(t *testing.T) {
	fx := newSharingFixture(t)
	root := fx.rootOf(t, fx.fld.ID)
	for i := 0; i < 5; i++ {
		m := newMember(t, fx, fmt.Sprintf("manager%d@example.com", i))
		fx.grant(t, root, m.user.ID, "manager")
	}
	nodes := []nodeJSON{fx.deep, fx.sib,
		putNode(t, fx.s, fx.key, fx.fld.ID, "/a/c/y.txt", "y"),
		putNode(t, fx.s, fx.key, fx.fld.ID, "/a/c/z.txt", "z")}
	base := len(fx.sent(t))
	for i, n := range nodes {
		wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+n.ID+"/access-requests",
			map[string]any{"role": "viewer"}), 202)
		got := len(fx.sent(t)) - base
		base = len(fx.sent(t))
		switch i {
		case 0, 1, 2:
			if got != 5 {
				t.Errorf("request %d mailed %d managers, want all five", i+1, got)
			}
		case 3:
			if got != 0 {
				t.Errorf("the fourth request in the hour mailed %d, want nobody", got)
			}
		}
	}
	list := decode[listResponse[accessRequestJSON]](t, call(t, fx.s, "GET",
		"/api/v1/nodes/"+nodes[3].ID+"/access-requests", fx.key, nil))
	if len(list.Items) != 1 || list.Items[0].UserID != fx.ann.ID {
		t.Errorf("the fourth request was not recorded: %+v", list.Items)
	}
}
