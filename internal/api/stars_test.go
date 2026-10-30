// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/store"
)

// flushViews writes the views the recorder holds, as the server does every two seconds.
func flushViews(t *testing.T, s *Server) {
	t.Helper()
	s.views.flush(context.Background())
}

// starredIDs lists the node IDs in a member's starred list.
func starredIDs(t *testing.T, fx *sharingFixture, sess testSession) []string {
	t.Helper()
	rec := callSession(t, fx.s, sess, "GET", "/api/v1/starred", nil)
	wantStatus(t, rec, 200)
	var ids []string
	for _, it := range decode[listResponse[starredItem]](t, rec).Items {
		ids = append(ids, it.ID)
	}
	return ids
}

// recentIDs lists the node IDs in a member's recent list.
func recentIDs(t *testing.T, fx *sharingFixture, sess testSession) []string {
	t.Helper()
	rec := callSession(t, fx.s, sess, "GET", "/api/v1/recent", nil)
	wantStatus(t, rec, 200)
	var ids []string
	for _, it := range decode[listResponse[recentItem]](t, rec).Items {
		ids = append(ids, it.ID)
	}
	return ids
}

func contains(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func TestStarAndListStarred(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")

	// A viewer may star; starring again changes nothing.
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/star", nil), 204)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/star", nil), 204)
	rec := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/starred", nil)
	wantStatus(t, rec, 200)
	items := decode[listResponse[starredItem]](t, rec).Items
	if len(items) != 1 || items[0].ID != fx.deep.ID || items[0].Role != "viewer" || items[0].StarredAt.IsZero() {
		t.Fatalf("starred = %+v, want the one file, with its role and starred_at", items)
	}
	// The star shows on stat and on the listing of its directory.
	if st := decode[nodeJSON](t, fx.stat(t, fx.annSess, fx.deep.ID)); st.Starred == nil || !*st.Starred {
		t.Errorf("stat starred = %v, want true", st.Starred)
	}
	kids := decode[listResponse[nodeJSON]](t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.b.ID+"/children", nil))
	if len(kids.Items) != 1 || kids.Items[0].Starred == nil || !*kids.Items[0].Starred {
		t.Errorf("children = %+v, want the file starred", kids.Items)
	}
	// Another user never sees it: bob cannot reach the file and has no stars.
	wantError(t, callSession(t, fx.s, fx.bobSess, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/star", nil), 404, apierr.NotFound)
	if ids := starredIDs(t, fx, fx.bobSess); len(ids) != 0 {
		t.Errorf("bob's starred = %v, want none", ids)
	}

	// Removing the star clears it everywhere, and removing again is not an error.
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/nodes/"+fx.deep.ID+"/star", nil), 204)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/nodes/"+fx.deep.ID+"/star", nil), 204)
	if ids := starredIDs(t, fx, fx.annSess); len(ids) != 0 {
		t.Errorf("starred after removal = %v", ids)
	}
	if st := decode[nodeJSON](t, fx.stat(t, fx.annSess, fx.deep.ID)); st.Starred == nil || *st.Starred {
		t.Errorf("stat starred after removal = %v, want present and false", st.Starred)
	}
}

func TestStarRefusesKeysAndAdmins(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	for _, cred := range []string{fx.key, fx.admin} {
		wantError(t, call(t, fx.s, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/star", cred, nil), 403, apierr.Forbidden)
		wantError(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.deep.ID+"/star", cred, nil), 403, apierr.Forbidden)
		wantError(t, call(t, fx.s, "GET", "/api/v1/starred", cred, nil), 403, apierr.Forbidden)
		wantError(t, call(t, fx.s, "GET", "/api/v1/recent", cred, nil), 403, apierr.Forbidden)
		wantError(t, call(t, fx.s, "DELETE", "/api/v1/recent", cred, nil), 403, apierr.Forbidden)
	}
}

func TestStarTrashedAndUnreachableAreKeptNotListed(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/star", nil), 204)

	// Trashed: not listed, still kept, and listed again after a restore.
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.deep.ID, fx.key, nil), 204)
	if ids := starredIDs(t, fx, fx.annSess); contains(ids, fx.deep.ID) {
		t.Errorf("a trashed star is listed: %v", ids)
	}
	if n := countStars(t, fx, fx.ann.ID); n != 1 {
		t.Errorf("stars kept while trashed = %d, want 1", n)
	}
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.deep.ID+"/restore", fx.key, nil), 200)
	if ids := starredIDs(t, fx, fx.annSess); !contains(ids, fx.deep.ID) {
		t.Errorf("a restored star is not listed: %v", ids)
	}

	// Unreachable: the grant goes, the star stays in the database and is not listed.
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.b.ID+"/permissions/"+fx.ann.ID, fx.key, nil), 204)
	if ids := starredIDs(t, fx, fx.annSess); len(ids) != 0 {
		t.Errorf("an unreachable star is listed: %v", ids)
	}
	if n := countStars(t, fx, fx.ann.ID); n != 1 {
		t.Errorf("stars kept while unreachable = %d, want 1", n)
	}
	// A new grant shows it again.
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	if ids := starredIDs(t, fx, fx.annSess); !contains(ids, fx.deep.ID) {
		t.Errorf("a star is not listed again after a new grant: %v", ids)
	}
}

// countStars reads how many stars a user holds, listed or not.
func countStars(t *testing.T, fx *sharingFixture, userID string) int {
	t.Helper()
	var n int
	err := fx.s.store.Write(context.Background(), func(tx *store.Tx) error {
		var err error
		n, err = tx.CountStars(context.Background(), userID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestStarLimitIsConflict(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.s.starLimit = 2
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	fx.setPerm(t, fx.c.ID, fx.ann.ID, "viewer")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+fx.b.ID+"/star", nil), 204)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/star", nil), 204)
	// A third star is refused; starring one again is not a new star, so it still works.
	rec := callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+fx.sib.ID+"/star", nil)
	e := wantError(t, rec, 409, apierr.Conflict)
	if e.Details["reason"] != "limit" {
		t.Errorf("reason = %v, want limit", e.Details["reason"])
	}
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+fx.b.ID+"/star", nil), 204)
}

func TestStarTokenWithinItsReach(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	fx.setPerm(t, fx.c.ID, fx.ann.ID, "viewer")
	tok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"user": fx.ann.ID, "folders": []string{fx.fld.ID},
		"node": fx.b.ID, "permissions": "write"})
	// The token stars inside its directory and not outside it; its stars are the user's own.
	wantStatus(t, rawCall(t, fx.s, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/star", tok, "", nil), 204)
	wantError(t, rawCall(t, fx.s, "PUT", "/api/v1/nodes/"+fx.sib.ID+"/star", tok, "", nil), 404, apierr.NotFound)
	if ids := starredIDs(t, fx, fx.annSess); !contains(ids, fx.deep.ID) {
		t.Errorf("the token's star is not the user's: %v", ids)
	}
	rec := getWith(t, fx.s, tok, "/api/v1/starred")
	wantStatus(t, rec, 200)
	if items := decode[listResponse[starredItem]](t, rec).Items; len(items) != 1 || items[0].ID != fx.deep.ID {
		t.Errorf("the token lists %+v, want only the file inside its reach", items)
	}
}

func TestVisitorCannotStar(t *testing.T) {
	t.Parallel()
	fx := newAnyoneFixture(t)
	_, secret := fx.makeAnyone(t, fx.b.ID, nil)
	tok := fx.visitAnyone(t, secret, "")
	wantError(t, fx.visitor(t, tok, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/star"), 403, apierr.Forbidden)
	wantError(t, fx.visitor(t, tok, "GET", "/api/v1/starred"), 403, apierr.Forbidden)
	wantError(t, fx.visitor(t, tok, "GET", "/api/v1/recent"), 403, apierr.Forbidden)
	// A visitor's stat carries no star field at all.
	rec := fx.visitor(t, tok, "GET", "/api/v1/nodes/"+fx.deep.ID)
	wantStatus(t, rec, 200)
	if strings.Contains(rec.Body.String(), `"starred"`) {
		t.Errorf("a visitor sees a starred field: %s", rec.Body)
	}
}

func TestRecentViewsAndEdits(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "editor")
	// A session view counts, a HEAD does not.
	wantStatus(t, callSession(t, fx.s, fx.annSess, "HEAD", "/api/v1/nodes/"+fx.deep.ID+"/content", nil), 200)
	flushViews(t, fx.s)
	if ids := recentIDs(t, fx, fx.annSess); len(ids) != 0 {
		t.Fatalf("a HEAD recorded a view: %v", ids)
	}
	wantStatus(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.deep.ID+"/content", nil), 200)
	flushViews(t, fx.s)
	rec := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/recent", nil)
	wantStatus(t, rec, 200)
	items := decode[listResponse[recentItem]](t, rec).Items
	if len(items) != 1 || items[0].ID != fx.deep.ID || items[0].ViewedAt == nil || items[0].EditedAt != nil {
		t.Fatalf("recent after a view = %+v", items)
	}

	// A token acting as ann, and the account key, record no views.
	fresh := putNode(t, fx.s, fx.key, fx.fld.ID, "/a/b/fresh.txt", "fresh")
	tok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"user": fx.ann.ID, "folders": []string{fx.fld.ID},
		"permissions": "read"})
	wantStatus(t, getWith(t, fx.s, tok, "/api/v1/nodes/"+fresh.ID+"/content"), 200)
	wantStatus(t, getWith(t, fx.s, fx.key, "/api/v1/nodes/"+fresh.ID+"/content"), 200)
	flushViews(t, fx.s)
	if ids := recentIDs(t, fx, fx.annSess); contains(ids, fresh.ID) {
		t.Errorf("a token or the key recorded a view: %v", ids)
	}

	// Creating a file is an edit; so is writing its content.
	created := rawCall(t, fx.s, "POST", "/api/v1/folders/"+fx.fld.ID+"/files?"+q("/a/b/made.txt"), fx.annCred(t), "made", nil)
	wantStatus(t, created, 201)
	madeID := decode[nodeJSON](t, created).ID
	if ids := recentIDs(t, fx, fx.annSess); !contains(ids, madeID) {
		t.Errorf("a file the user created is not in recent: %v", ids)
	}

	// A rename records nothing: the file the key made and ann renames is not in recent.
	renamed := putNode(t, fx.s, fx.key, fx.fld.ID, "/a/b/old.txt", "old")
	wantStatus(t, rawCall(t, fx.s, "PATCH", "/api/v1/nodes/"+renamed.ID, fx.annCred(t), `{"name":"new.txt"}`,
		map[string]string{"Content-Type": "application/json"}), 200)
	if ids := recentIDs(t, fx, fx.annSess); contains(ids, renamed.ID) {
		t.Errorf("a rename was recorded: %v", ids)
	}

	// Removing one entry, then clearing the list.
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/recent/"+madeID, nil), 204)
	if ids := recentIDs(t, fx, fx.annSess); contains(ids, madeID) {
		t.Errorf("a removed entry is still listed: %v", ids)
	}
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/recent", nil), 204)
	if ids := recentIDs(t, fx, fx.annSess); len(ids) != 0 {
		t.Errorf("recent after clearing = %v", ids)
	}
}

// annCred mints a token acting as ann over the team space, with write, for requests that need a credential
// rather than a cookie. It records edits as ann, as a session does.
func (fx *sharingFixture) annCred(t *testing.T) string {
	t.Helper()
	return mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"user": fx.ann.ID, "folders": []string{fx.fld.ID},
		"permissions": "write"})
}

func TestRecentEditsReplaceAndUpload(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "editor")
	cred := fx.annCred(t)

	// A replaced content, and a version restored, are both edits.
	before := fx.stat(t, fx.annSess, fx.deep.ID)
	wantStatus(t, before, 200)
	etag := decode[nodeJSON](t, before).ETag
	rec := rawCall(t, fx.s, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/content", fx.annCred(t), "replaced",
		map[string]string{"If-Match": etag})
	wantStatus(t, rec, 200)
	if ids := recentIDs(t, fx, fx.annSess); !contains(ids, fx.deep.ID) {
		t.Errorf("a replaced file is not in recent: %v", ids)
	}

	// A file completed from an upload, by a token acting as ann.
	data := "chunked upload"
	up := createUpload(t, fx.s, cred, int64(len(data)), md("folder", fx.fld.ID, "name", "up.txt",
		"path", "/a/b", "sha256", digest(data)))
	upl := uploadID(t, up)
	done := piece(t, fx.s, cred, upl, 0, data, nil)
	wantStatus(t, done, 204)
	node := done.Header().Get("Selfcloud-Node-ID")
	if node == "" {
		t.Fatal("the completed upload names no node")
	}
	if ids := recentIDs(t, fx, fx.annSess); !contains(ids, node) {
		t.Errorf("a completed upload is not in recent: %v", ids)
	}

	// Restoring an earlier version is an edit as well.
	vers := decode[listResponse[versionJSON]](t, callSession(t, fx.s, fx.annSess, "GET",
		"/api/v1/nodes/"+fx.deep.ID+"/versions", nil))
	var earlier string
	for _, v := range vers.Items {
		if !v.Current {
			earlier = v.ID
		}
	}
	if earlier == "" {
		t.Fatal("no earlier version to restore")
	}
	cur := decode[nodeJSON](t, fx.stat(t, fx.annSess, fx.deep.ID)).ETag
	restore := rawCall(t, fx.s, "POST", "/api/v1/nodes/"+fx.deep.ID+"/versions/"+earlier+"/restore",
		cred, "", map[string]string{"If-Match": cur})
	wantStatus(t, restore, 200)
}

func TestRecentPrunesByAgeAndCount(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	fx.setPerm(t, fx.c.ID, fx.ann.ID, "viewer")
	now := time.Now()
	ctx := context.Background()
	// Five entries: one older than thirty days, and four that the count keeps two of, latest first.
	views := []store.ViewRecord{
		{AccountID: fx.acc.ID, UserID: fx.ann.ID, NodeID: fx.a.ID, At: now.Add(-40 * 24 * time.Hour)},
		{AccountID: fx.acc.ID, UserID: fx.ann.ID, NodeID: fx.b.ID, At: now.Add(-3 * time.Hour)},
		{AccountID: fx.acc.ID, UserID: fx.ann.ID, NodeID: fx.c.ID, At: now.Add(-2 * time.Hour)},
		{AccountID: fx.acc.ID, UserID: fx.ann.ID, NodeID: fx.deep.ID, At: now.Add(-time.Hour)},
		{AccountID: fx.acc.ID, UserID: fx.ann.ID, NodeID: fx.sib.ID, At: now},
	}
	if err := fx.s.store.WriteViews(ctx, views); err != nil {
		t.Fatal(err)
	}
	var removed int64
	err := fx.s.store.Write(ctx, func(tx *store.Tx) error {
		var err error
		removed, err = tx.PruneRecent(ctx, now.Add(-30*24*time.Hour), 2)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if removed != 3 {
		t.Errorf("removed %d, want 3 (one by age, two by count)", removed)
	}
	ids := recentIDs(t, fx, fx.annSess)
	if len(ids) != 2 || ids[0] != fx.sib.ID || ids[1] != fx.deep.ID {
		t.Errorf("recent after pruning = %v, want sib and deep", ids)
	}
}

func TestSharedRecentOrder(t *testing.T) {
	t.Parallel()
	fx := newLinkFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	time.Sleep(3 * time.Millisecond)
	fx.setPerm(t, fx.sib.ID, fx.ann.ID, "viewer")
	// A visit to a link on c, after both grants, makes c the most recent share.
	_, url := fx.makeLink(t, fx.c.ID, "viewer")
	time.Sleep(3 * time.Millisecond)
	wantStatus(t, fx.open(t, fx.annSess, secretOf(t, url)), 200)

	rec := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/shared?order=recent", nil)
	wantStatus(t, rec, 200)
	items := decode[listResponse[sharedItem]](t, rec).Items
	if len(items) != 3 {
		t.Fatalf("shared = %d items, want 3", len(items))
	}
	if items[0].ID != fx.c.ID || items[1].ID != fx.sib.ID || items[2].ID != fx.b.ID {
		t.Errorf("order = %s %s %s, want c, sib, b", items[0].Name, items[1].Name, items[2].Name)
	}
	for _, it := range items {
		if it.SharedAt == nil {
			t.Errorf("%s carries no shared_at", it.Name)
		}
	}
	// The default order is node ID order, and carries no shared_at.
	def := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/shared", nil)
	wantStatus(t, def, 200)
	for _, it := range decode[listResponse[sharedItem]](t, def).Items {
		if it.SharedAt != nil {
			t.Errorf("the default order carries shared_at on %s", it.Name)
		}
	}
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/shared?order=name", nil), 400, apierr.InvalidRequest)
}

// TestRecorderGateAndFullQueue checks the view recorder's gate and its queue without a server running: one view per
// user and file in ten minutes, and a full queue drops a view at once rather than blocking.
func TestRecorderGateAndFullQueue(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	fx.setPerm(t, fx.c.ID, fx.ann.ID, "viewer")
	v := newViewRecorderSize(fx.s.store, fx.s.log, 2)
	t0 := time.Now()
	rec := func(node string, at time.Time) store.ViewRecord {
		return store.ViewRecord{AccountID: fx.acc.ID, UserID: fx.ann.ID, NodeID: node, At: at}
	}
	if !v.record(rec(fx.deep.ID, t0)) {
		t.Fatal("the first view was not queued")
	}
	if v.record(rec(fx.deep.ID, t0.Add(time.Minute))) {
		t.Error("a second view within ten minutes was queued")
	}
	if !v.record(rec(fx.sib.ID, t0)) {
		t.Fatal("a second file was not queued")
	}
	// The queue holds two: a third view is dropped at once, and its gate mark is undone.
	if v.record(rec(fx.a.ID, t0)) {
		t.Error("a view was queued into a full queue")
	}
	v.flush(context.Background())
	if n := len(v.ch); n != 0 {
		t.Errorf("queue after flush = %d", n)
	}
	// Written: the two views, and not the dropped one.
	ids := recentIDs(t, fx, fx.annSess)
	if !contains(ids, fx.deep.ID) || !contains(ids, fx.sib.ID) || contains(ids, fx.a.ID) {
		t.Errorf("recent after the flush = %v", ids)
	}
	// The dropped view's gate mark is gone, so the file can be viewed again straight away; and after ten minutes,
	// the first file can be viewed again too.
	if !v.record(rec(fx.a.ID, t0)) {
		t.Error("a dropped view could not be taken again")
	}
	if !v.record(rec(fx.deep.ID, t0.Add(11*time.Minute))) {
		t.Error("a view after ten minutes was not queued")
	}
}
