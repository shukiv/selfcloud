// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/sweep"
)

// trashItems lists the trash as cred sees it, and returns the IDs of its items.
func trashItems(t *testing.T, fx *sharingFixture, cred string) []string {
	t.Helper()
	list := decode[listResponse[trashItemJSON]](t, call(t, fx.s, "GET", "/api/v1/trash", cred, nil))
	var ids []string
	for _, it := range list.Items {
		ids = append(ids, it.ID)
	}
	return ids
}

// contains reports whether ids holds id.
func hasID(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// TestTrashedSubtreeAnswersNotFoundEverywhere: once a folder is trashed, the folder and everything in it
// answer not_found on every route a normal caller uses, a delegated token and a user included, and a
// permanent purge is the only thing left to do with it.
func TestTrashedSubtreeAnswersNotFoundEverywhere(t *testing.T) {
	fx := newSharingFixture(t)
	fx.grant(t, fx.b.ID, fx.ann.ID, "editor")
	fx.grant(t, fx.deep.ID, fx.bob.ID, "viewer")
	tok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}, "permissions": "write"})
	before := fx.usage(t)

	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.b.ID, fx.key, nil), 204)

	b, deep, sib := fx.b.ID, fx.deep.ID, fx.sib.ID
	for _, c := range []struct {
		name, method, path string
		body               any
		cred               string
	}{
		{"stat the folder", "GET", "/api/v1/nodes/" + b, nil, fx.key},
		{"stat a file below", "GET", "/api/v1/nodes/" + deep, nil, fx.key},
		{"list the folder", "GET", "/api/v1/nodes/" + b + "/children", nil, fx.key},
		{"download below", "GET", "/api/v1/nodes/" + deep + "/content", nil, fx.key},
		{"move below", "POST", "/api/v1/nodes/" + deep + "/move", map[string]any{"parent": fx.c.ID}, fx.key},
		{"move into the folder", "POST", "/api/v1/nodes/" + sib + "/move", map[string]any{"parent": b}, fx.key},
		{"copy below", "POST", "/api/v1/nodes/" + deep + "/copy", map[string]any{"folder": fx.fld.ID, "path": "/"}, fx.key},
		{"link below", "POST", "/api/v1/nodes/" + deep + "/links", map[string]any{"ttl": 60}, fx.key},
		{"permissions below", "GET", "/api/v1/nodes/" + deep + "/permissions", nil, fx.key},
		{"share below", "PUT", "/api/v1/nodes/" + deep + "/permissions/" + fx.bob.ID, map[string]any{"role": "viewer"}, fx.key},
		{"rename below", "PATCH", "/api/v1/nodes/" + deep, map[string]any{"no_download": true}, fx.key},
		{"transfer below", "POST", "/api/v1/nodes/" + deep + "/transfer-ownership", map[string]any{"to": fx.bob.ID}, fx.key},
		{"versions below", "GET", "/api/v1/nodes/" + deep + "/versions", nil, fx.key},
		{"stat by token", "GET", "/api/v1/nodes/" + deep, nil, tok},
		{"list by path by token", "GET", "/api/v1/folders/" + fx.fld.ID + "/children?path=/a/b", nil, tok},
		{"create below by path", "POST", "/api/v1/folders/" + fx.fld.ID + "/directories", map[string]any{"path": "/a/b/new"}, fx.key},
	} {
		rec := call(t, fx.s, c.method, c.path, c.cred, c.body)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404: %s", c.name, rec.Code, rec.Body)
		}
	}
	if rec := rawCall(t, fx.s, "PUT", "/api/v1/nodes/"+deep+"/content", fx.key, "x", map[string]string{"If-Match": fx.deep.ETag}); rec.Code != http.StatusNotFound {
		t.Errorf("replace below: status %d, want 404", rec.Code)
	}

	// A user the folder was shared with does not see it in /shared, and a request for it is not recorded.
	shared := decode[listResponse[nodeJSON]](t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/shared", nil))
	for _, n := range shared.Items {
		if n.ID == b || n.ID == deep {
			t.Errorf("/shared still lists a trashed node %s", n.ID)
		}
	}
	wantStatus(t, callSession(t, fx.s, fx.bobSess, "POST", "/api/v1/nodes/"+deep+"/access-requests",
		map[string]any{"role": "editor"}), 202)
	pending := decode[listResponse[accessRequestJSON]](t, call(t, fx.s, "GET", "/api/v1/access-requests", fx.key, nil))
	for _, r := range pending.Items {
		if r.NodeID == deep {
			t.Error("an access request for a trashed node was recorded")
		}
	}

	if after := fx.usage(t); after.used != before.used || after.files != before.files {
		t.Errorf("trashing changed usage: %+v -> %+v", before, after)
	}
	if !hasID(trashItems(t, fx, fx.key), b) {
		t.Error("the trashed folder is not in the trash listing")
	}
}

// TestSignedLinkStopsWhenItsFileIsTrashed: a link made before the file was trashed stops working at once. The
// link's MAC still checks, since trashing does not change the ETag, so the trash itself is what refuses it.
func TestSignedLinkStopsWhenItsFileIsTrashed(t *testing.T) {
	fx := newSharingFixture(t)
	link := decode[linkJSON](t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.deep.ID+"/links", fx.key,
		map[string]any{"ttl": 300}))
	u, err := url.Parse(link.URL)
	if err != nil {
		t.Fatal(err)
	}
	wantStatus(t, call(t, fx.s, "GET", u.Path, "", nil), 200)

	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.deep.ID, fx.key, nil), 204)
	wantStatus(t, call(t, fx.s, "GET", u.Path, "", nil), 404)
}

// TestTrashedNameIsFreeForANewItem: the name of a trashed item may be taken again at once, and the trashed
// item keeps its name in the trash listing.
func TestTrashedNameIsFreeForANewItem(t *testing.T) {
	fx := newSharingFixture(t)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.sib.ID, fx.key, nil), 204)

	fresh := putNode(t, fx.s, fx.key, fx.fld.ID, "/a/c/x.txt", "new")
	if fresh.ID == fx.sib.ID {
		t.Fatal("the new file took the trashed file's ID")
	}
	list := decode[listResponse[trashItemJSON]](t, call(t, fx.s, "GET", "/api/v1/trash", fx.key, nil))
	if len(list.Items) != 1 || list.Items[0].ID != fx.sib.ID || list.Items[0].Name != "x.txt" {
		t.Errorf("trash = %+v, want the old x.txt", list.Items)
	}
}

// TestUsageUnchangedUntilPurge: a trashed file still counts toward usage, and only a purge takes it off.
func TestUsageUnchangedUntilPurge(t *testing.T) {
	fx := newSharingFixture(t)
	before := fx.usage(t)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.deep.ID, fx.key, nil), 204)
	if after := fx.usage(t); after.used != before.used || after.files != before.files {
		t.Fatalf("usage after trash = %+v, want %+v", after, before)
	}
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/trash/"+fx.deep.ID, fx.key, nil), 204)
	after := fx.usage(t)
	if before.used-after.used != 4 || before.files-after.files != 1 {
		t.Errorf("usage after purge = %+v, from %+v; want four bytes and one file less", after, before)
	}
}

// TestRestoreToOriginalParent: a restore puts the item back in its original directory, and the reply
// carries the new parent and the name.
func TestRestoreToOriginalParent(t *testing.T) {
	fx := newSharingFixture(t)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.sib.ID, fx.key, nil), 204)
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.sib.ID+"/restore", fx.key, nil)
	wantStatus(t, rec, 200)
	back := decode[nodeJSON](t, rec)
	if back.ParentID == nil || *back.ParentID != fx.c.ID || back.Name != "x.txt" {
		t.Errorf("restored = %+v, want x.txt back in c", back)
	}
	if !hasID(childNames(t, fx, fx.c.ID), "x.txt") {
		t.Error("the restored file is not in its directory")
	}
	if hasID(trashItems(t, fx, fx.key), fx.sib.ID) {
		t.Error("the restored file is still in the trash")
	}
}

// childNames lists the child IDs of a directory as the account key sees them.
func childNames(t *testing.T, fx *sharingFixture, dir string) []string {
	t.Helper()
	list := decode[listResponse[nodeJSON]](t, call(t, fx.s, "GET", "/api/v1/nodes/"+dir+"/children", fx.key, nil))
	var names []string
	for _, n := range list.Items {
		names = append(names, n.Name)
	}
	return names
}

// TestRestoreFallsBackToRootWhenParentIsTrashed: a child trashed before its parent is restored to the root
// of its folder while the parent is in the trash, and back to the parent once the parent is restored.
func TestRestoreFallsBackToRootWhenParentIsTrashed(t *testing.T) {
	fx := newSharingFixture(t)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.deep.ID, fx.key, nil), 204)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.b.ID, fx.key, nil), 204)

	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.deep.ID+"/restore", fx.key, nil)
	wantStatus(t, rec, 200)
	got := decode[nodeJSON](t, rec)
	if got.ParentID == nil || *got.ParentID != rootOf(t, fx, fx.fld.ID) {
		t.Errorf("restored under %v, want the folder root", got.ParentID)
	}

	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.b.ID+"/restore", fx.key, nil), 200)
	// deep went to the root, so the folder is back and empty.
	if got := childNames(t, fx, fx.b.ID); len(got) != 0 {
		t.Errorf("the restored folder holds %v", got)
	}
}

// rootOf returns the root ID of a team folder, as the account key sees it.
func rootOf(t *testing.T, fx *sharingFixture, fld string) string {
	t.Helper()
	return fx.rootOf(t, fld)
}

// TestRestoreRenamesANameClash: a name the original directory now has is taken by renaming the restored
// item, as a copy is.
func TestRestoreRenamesANameClash(t *testing.T) {
	fx := newSharingFixture(t)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.sib.ID, fx.key, nil), 204)
	putNode(t, fx.s, fx.key, fx.fld.ID, "/a/c/x.txt", "again")

	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.sib.ID+"/restore", fx.key, nil)
	wantStatus(t, rec, 200)
	if got := decode[nodeJSON](t, rec); got.Name != "x (1).txt" {
		t.Errorf("restored name = %q, want %q", got.Name, "x (1).txt")
	}
}

// TestRestoreNeedsTheTrasherOrAManager: a user with editor on the item who did not trash it cannot restore it.
// The one that trashed it can, and so can the account key.
func TestRestoreNeedsTheTrasherOrAManager(t *testing.T) {
	fx := newSharingFixture(t)
	fx.grant(t, fx.a.ID, fx.ann.ID, "editor")
	fx.grant(t, fx.a.ID, fx.bob.ID, "editor")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/nodes/"+fx.sib.ID, nil), 204)

	wantError(t, callSession(t, fx.s, fx.bobSess, "POST", "/api/v1/nodes/"+fx.sib.ID+"/restore", nil),
		403, apierr.Forbidden)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.sib.ID+"/restore", nil), 200)
}

// TestRestoreNeedsEditorOnTheDestination: the original parent is reached, but the caller holds only viewer
// there, so the restore is refused.
func TestRestoreNeedsEditorOnTheDestination(t *testing.T) {
	fx := newSharingFixture(t)
	fx.grant(t, rootOf(t, fx, fx.fld.ID), fx.ann.ID, "viewer")
	fx.grant(t, fx.deep.ID, fx.ann.ID, "editor")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/nodes/"+fx.deep.ID, nil), 204)

	wantError(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.deep.ID+"/restore", nil),
		403, apierr.Forbidden)
	if !hasID(trashItems(t, fx, fx.key), fx.deep.ID) {
		t.Error("a refused restore took the item out of the trash")
	}
}

// TestRestoreToRootNeedsEditorOnTheRoot: the parent is in the trash and the caller holds no role on the folder
// root, so there is nowhere the caller may restore to: a conflict. Once the parent is restored, the item goes
// back to it.
func TestRestoreToRootNeedsEditorOnTheRoot(t *testing.T) {
	fx := newSharingFixture(t)
	fx.grant(t, fx.sib.ID, fx.ann.ID, "editor")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/nodes/"+fx.sib.ID, nil), 204)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.c.ID, fx.key, nil), 204)

	wantError(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.sib.ID+"/restore", nil),
		409, apierr.Conflict)
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.c.ID+"/restore", fx.key, nil), 200)
	// Ann cannot reach c, so it cannot restore into it: refused. The account key can.
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.sib.ID+"/restore", nil),
		403, apierr.Forbidden)
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.sib.ID+"/restore", fx.key, nil), 200)
}

// TestPermanentDeleteNeedsManager: a permanent delete is for managers only, since it cannot be undone; the
// account key may do it.
func TestPermanentDeleteNeedsManager(t *testing.T) {
	fx := newSharingFixture(t)
	fx.grant(t, fx.deep.ID, fx.ann.ID, "editor")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/nodes/"+fx.deep.ID+"?permanent=true", nil), 403)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.deep.ID+"?permanent=true", fx.key, nil), 204)
	wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.deep.ID, fx.key, nil), 404, apierr.NotFound)
	if after := fx.usage(t); after.files != 1 {
		t.Errorf("files after a permanent delete = %d, want 1", after.files)
	}
}

// TestPurgeTrashNeedsManager: an editor who trashed an item cannot purge it from the trash; the account key can.
func TestPurgeTrashNeedsManager(t *testing.T) {
	fx := newSharingFixture(t)
	fx.grant(t, fx.deep.ID, fx.ann.ID, "editor")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/nodes/"+fx.deep.ID, nil), 204)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/trash/"+fx.deep.ID, nil), 403)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/trash/"+fx.deep.ID, fx.key, nil), 204)
}

// TestEmptyTrashIsBoundedAndPurgesOnlyWhatTheCallerManages: an empty call purges at most its limit and says
// whether more remain. A user who trashed an item but does not manage it purges nothing.
func TestEmptyTrashIsBoundedAndPurgesOnlyWhatTheCallerManages(t *testing.T) {
	fx := newSharingFixture(t)
	fx.s.emptyTrashLimit = 2
	loose := putNode(t, fx.s, fx.key, fx.fld.ID, "/o.txt", "o")
	fx.grant(t, fx.deep.ID, fx.ann.ID, "editor")
	// Three roots that do not sit in one another, so each one is purged on its own.
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/nodes/"+fx.deep.ID, nil), 204)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.sib.ID, fx.key, nil), 204)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+loose.ID, fx.key, nil), 204)

	// Ann manages nothing: she purges nothing, though she trashed deep.
	ann := decode[map[string]any](t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/trash/empty", nil))
	if ann["purged"] != float64(0) || ann["more"] != false {
		t.Errorf("ann's empty = %v, want nothing purged", ann)
	}

	first := decode[map[string]any](t, call(t, fx.s, "POST", "/api/v1/trash/empty", fx.key, nil))
	if first["purged"] != float64(2) || first["more"] != true {
		t.Errorf("first empty = %v, want 2 purged and more", first)
	}
	second := decode[map[string]any](t, call(t, fx.s, "POST", "/api/v1/trash/empty", fx.key, nil))
	if second["purged"] != float64(1) || second["more"] != false {
		t.Errorf("second empty = %v, want 1 purged and nothing more", second)
	}
}

// TestEmptyTrashStopsAtTheScanCap: ann can purge nothing, and the trash holds three roots she cannot reach or
// manage. With a scan cap of two, one call examines two of them and replies more, rather than scanning the
// rest; the client calls again. Without the cap the scan would go through all three and reply nothing more.
func TestEmptyTrashStopsAtTheScanCap(t *testing.T) {
	fx := newSharingFixture(t)
	fx.s.trashScanLimit = 2
	loose := putNode(t, fx.s, fx.key, fx.fld.ID, "/o.txt", "o")
	fx.grant(t, fx.deep.ID, fx.ann.ID, "editor")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/nodes/"+fx.deep.ID, nil), 204)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.sib.ID, fx.key, nil), 204)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+loose.ID, fx.key, nil), 204)

	ann := decode[map[string]any](t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/trash/empty", nil))
	if ann["purged"] != float64(0) || ann["more"] != true {
		t.Errorf("ann's empty = %v, want nothing purged and more (the scan stopped at the cap)", ann)
	}
}

// TestRetentionPurgesAfterTheRetentionWithAuditAndUsage: the sweep purges a trashed item once it has been in
// the trash for trash.retention, under the system actor, and takes its usage off. Before that it purges nothing.
func TestRetentionPurgesAfterTheRetentionWithAuditAndUsage(t *testing.T) {
	fx := newSharingFixture(t)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.deep.ID, fx.key, nil), 204)
	before := fx.usage(t)
	sw := sweep.New(sweep.Options{Store: fx.s.store, Blobs: fx.s.blobs, Interval: time.Hour,
		TrashRetention: 30 * 24 * time.Hour})

	res, err := sw.Once(context.Background(), store.Now().Add(29*24*time.Hour))
	if err != nil || res.Purged != 0 {
		t.Fatalf("before the retention: %+v, %v", res, err)
	}
	res, err = sw.Once(context.Background(), store.Now().Add(31*24*time.Hour))
	if err != nil || res.Purged != 1 {
		t.Fatalf("after the retention: %+v, %v", res, err)
	}
	after := fx.usage(t)
	if before.used-after.used != 4 || before.files-after.files != 1 {
		t.Errorf("usage %+v -> %+v, want four bytes and one file less", before, after)
	}
	log := decode[listResponse[auditJSON]](t, call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/audit", fx.key, nil))
	found := false
	for _, e := range log.Items {
		if e.Action == "node.purge" && e.Target == fx.deep.ID && e.Actor.Kind == "system" {
			found = true
		}
	}
	if !found {
		t.Error("no node.purge audit entry by the system for the retention purge")
	}
}

// TestTrashListingShowsOwnAndManagedOnly: a user sees the items it trashed and the ones it manages. It does
// not see another user's items, and an owner does not see another user's personal-folder trash.
func TestTrashListingShowsOwnAndManagedOnly(t *testing.T) {
	fx := newSharingFixture(t)
	annPF := fx.personalFolder(t, fx.annSess)
	mine := fx.sessionFile(t, fx.annSess, annPF, "/mine.txt", "mine")
	fx.grant(t, fx.sib.ID, fx.ann.ID, "editor")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/nodes/"+mine.ID, nil), 204)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/nodes/"+fx.sib.ID, nil), 204)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.deep.ID, fx.key, nil), 204)

	got := decode[listResponse[trashItemJSON]](t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/trash", nil))
	var ids []string
	for _, it := range got.Items {
		ids = append(ids, it.ID)
	}
	if !hasID(ids, mine.ID) || !hasID(ids, fx.sib.ID) || hasID(ids, fx.deep.ID) {
		t.Errorf("ann sees %v, want her two items and not the key's", ids)
	}

	// An owner does not see ann's personal-folder trash, but sees the team space's.
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.bob.ID, fx.key,
		map[string]any{"role": "owner"}), 200)
	bob := decode[listResponse[trashItemJSON]](t, callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/trash", nil))
	ids = nil
	for _, it := range bob.Items {
		ids = append(ids, it.ID)
	}
	if hasID(ids, mine.ID) {
		t.Error("an owner sees another user's personal-folder trash")
	}
	if !hasID(ids, fx.sib.ID) || !hasID(ids, fx.deep.ID) {
		t.Errorf("the owner sees %v, want the team space's items", ids)
	}

	all := decode[listResponse[trashItemJSON]](t, call(t, fx.s, "GET", "/api/v1/trash", fx.key, nil))
	if len(all.Items) != 3 {
		t.Errorf("the account key sees %d items, want 3", len(all.Items))
	}
	for _, it := range all.Items {
		if want := it.TrashedAt.Add(30 * 24 * time.Hour); !it.PurgeAt.Equal(want) {
			t.Errorf("purge_at = %v, want trashed_at plus the retention %v", it.PurgeAt, want)
		}
	}
}

// TestTokenSeesOnlyItsFolders: a delegated token lists only the trash inside its folders.
func TestTokenSeesOnlyItsFolders(t *testing.T) {
	fx := newSharingFixture(t)
	other := newFolder(t, fx.s, fx.key, fx.acc.ID, "Other")
	putNode(t, fx.s, fx.key, other.ID, "/o.txt", "o")
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.sib.ID, fx.key, nil), 204)

	inScope := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}, "permissions": "write"})
	outOfScope := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{other.ID}, "permissions": "write"})
	if n := len(trashItems(t, fx, inScope)); n != 0 {
		t.Errorf("a key token lists %d items; it trashed none, so it sees none", n)
	}
	if n := len(trashItems(t, fx, outOfScope)); n != 0 {
		t.Errorf("a token outside the folder lists %d items", n)
	}
	if !hasID(trashItems(t, fx, fx.key), fx.sib.ID) {
		t.Error("the account key does not see the trashed file")
	}
}

// TestRecheckRefusesTrashRoutesAfterRoleRevoked: a manager whose role is revoked between the early check
// and the transaction is refused, and nothing is written: the item stays in the trash, and nothing is purged.
func TestRecheckRefusesTrashRoutesAfterRoleRevoked(t *testing.T) {
	t.Run("restore", func(t *testing.T) {
		fx := newSharingFixture(t)
		// Editor on the parent keeps the restore's destination open, so only the node's own role decides
		// once the manager row is revoked: the editor the parent gives is then all that is left, and a restore
		// needs the trasher or a manager.
		fx.grant(t, fx.b.ID, fx.ann.ID, "editor")
		fx.grant(t, fx.deep.ID, fx.ann.ID, "manager")
		wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.deep.ID, fx.key, nil), 204)
		onceBefore(fx.s, func() { fx.revoke(t, fx.deep.ID, fx.ann.ID) })
		rec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.deep.ID+"/restore", nil)
		refused(t, rec)
		if !hasID(trashItems(t, fx, fx.key), fx.deep.ID) {
			t.Error("a refused restore took the item out of the trash")
		}
	})
	t.Run("purge", func(t *testing.T) {
		fx := newSharingFixture(t)
		fx.grant(t, fx.b.ID, fx.ann.ID, "editor")
		fx.grant(t, fx.deep.ID, fx.ann.ID, "manager")
		wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.deep.ID, fx.key, nil), 204)
		before := fx.usage(t)
		onceBefore(fx.s, func() { fx.revoke(t, fx.deep.ID, fx.ann.ID) })
		refused(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/trash/"+fx.deep.ID, nil))
		if after := fx.usage(t); after.used != before.used {
			t.Errorf("a refused purge changed usage: %+v -> %+v", before, after)
		}
		if !hasID(trashItems(t, fx, fx.key), fx.deep.ID) {
			t.Error("a refused purge removed the item")
		}
	})
}

// revoke removes a user's own permission on a node in the store. The API cannot do it once the node is in the
// trash, since every route answers not_found there; the recheck is what is under test, so the revoke goes around
// the API, as a change made by another request would.
func (fx *sharingFixture) revoke(t *testing.T, nodeID, userID string) {
	t.Helper()
	if err := fx.s.store.Write(context.Background(), func(tx *store.Tx) error {
		return tx.DeletePermission(context.Background(), nodeID, store.SubjectUser, userID)
	}); err != nil {
		t.Fatal(err)
	}
}

// refused checks that a response is a refusal: forbidden, or not_found because the role went.
func refused(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want a refusal (403 or 404): %s", rec.Code, rec.Body)
	}
}
