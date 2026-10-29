// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/sweep"
)

// sweepOnce runs one housekeeping pass judged at now, as the server's sweeper does.
func sweepOnce(t *testing.T, fx *dropFixture, now time.Time) sweep.Result {
	t.Helper()
	sw := sweep.New(sweep.Options{Store: fx.s.store, Blobs: fx.s.blobs, DeleteGrace: time.Hour})
	res, err := sw.Once(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// countRows reads one COUNT from the test database.
func (fx *anyoneFixture) countRows(t *testing.T, query string, args ...any) int {
	t.Helper()
	n, err := strconv.Atoi(fx.queryString(t, query, args...))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestEndedLinkWaitsADayThenTheSweepRemovesIt: an ended link keeps its row for a day, answering not_found from the
// moment it ends; then the sweep removes it with its visits and its drop uploads, audits it, and leaves the live
// links alone. A second pass finds nothing to do.
func TestEndedLinkWaitsADayThenTheSweepRemovesIt(t *testing.T) {
	fx := newDropFixture(t)
	ctx := context.Background()
	now := time.Now()

	// An organisation link with a member's visit, which the sweep must remove with it.
	org, orgURL := fx.makeLink(t, fx.c.ID, store.RoleViewer)
	fx.openedAs(t, fx.annSess, secretOf(t, orgURL))
	// A drop link with an upload left open: five of ten bytes received.
	drop, dropSecret := fx.makeDrop(t, fx.b.ID, nil)
	tok := fx.visitDrop(t, dropSecret, "")
	upl := uploadID(t, createUpload(t, fx.s, tok, 10, md("name", "open.bin")))
	wantStatus(t, piece(t, fx.s, tok, upl, 0, "12345", nil), 204)
	// A live anyone link that the sweep must not touch.
	live, _ := fx.makeAnyone(t, fx.sib.ID, nil)

	fx.setExpiry(t, org.ID, now.Add(-2*time.Hour))
	fx.setExpiry(t, drop.ID, now.Add(-2*time.Hour))
	if res := sweepOnce(t, fx, now); res.Links != 0 {
		t.Fatalf("a link that ended two hours ago was removed (%d); it must wait a day", res.Links)
	}
	if _, err := fx.s.store.ShareLink(ctx, fx.acc.ID, org.ID); err != nil {
		t.Fatalf("the link within its grace is gone: %v", err)
	}
	deadLink(t, fx.open(t, fx.annSess, secretOf(t, orgURL)))

	fx.setExpiry(t, org.ID, now.Add(-25*time.Hour))
	fx.setExpiry(t, drop.ID, now.Add(-25*time.Hour))
	res := sweepOnce(t, fx, now)
	if res.Links != 2 {
		t.Fatalf("sweep removed %d links; want the two that ended a day ago", res.Links)
	}
	if _, err := fx.s.store.ShareLink(ctx, fx.acc.ID, org.ID); err == nil {
		t.Errorf("the ended organisation link is still there")
	}
	if n := fx.countRows(t, "SELECT COUNT(*) FROM share_link_visits WHERE link_id = ?", org.ID); n != 0 {
		t.Errorf("%d visits of the ended link are left", n)
	}
	if _, err := fx.s.store.Upload(ctx, fx.acc.ID, upl); err == nil {
		t.Errorf("the drop link's open upload survived its link")
	}
	if got := fx.reservedBytes(t); got != 0 {
		t.Errorf("the upload's reservation of %d bytes was not released", got)
	}
	if n := fx.countRows(t, "SELECT COUNT(*) FROM audit_log WHERE action = 'link.expired' AND target IN (?, ?)",
		org.ID, drop.ID); n != 2 {
		t.Errorf("link.expired entries = %d, want 2", n)
	}
	if _, err := fx.s.store.ShareLink(ctx, fx.acc.ID, live.ID); err != nil {
		t.Errorf("the live anyone link was swept: %v", err)
	}
	if again := sweepOnce(t, fx, now); again.Links != 0 {
		t.Errorf("a second pass removed %d more links; want none", again.Links)
	}
}

// TestSweptLinkOfAMembersFilesIsPrivate: the link.expired entry for a link in a personal folder names that folder,
// so only its user and the account key read it, as every entry about My files.
func TestSweptLinkOfAMembersFilesIsPrivate(t *testing.T) {
	fx := newDropFixture(t)
	pf := fx.personalFolder(t, fx.annSess)
	keep := fx.sessionFile(t, fx.annSess, pf, "/keep.txt", "keep")
	link, _ := fx.makeAnyone(t, keep.ID, nil)
	fx.setExpiry(t, link.ID, time.Now().Add(-25*time.Hour))
	sweepOnce(t, fx, time.Now())

	if !auditHas(fx.auditSeenBy(t, fx.ann.ID), "link.expired", link.ID) {
		t.Errorf("the owner of the folder does not see its link's expiry")
	}
	for _, viewer := range []string{fx.bob.ID, ""} {
		if auditHas(fx.auditSeenBy(t, viewer), "link.expired", link.ID) {
			t.Errorf("viewer %q sees the expiry of a link in another member's My files", viewer)
		}
	}
}

// ownerSession makes a verified owner of the fixture's account and returns its session.
func (fx *linkFixture) ownerSession(t *testing.T, email string) testSession {
	t.Helper()
	rec := call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users", fx.key,
		map[string]any{"email": email, "role": "owner", "verification": "link"})
	wantStatus(t, rec, 201)
	created := decode[userResponse](t, rec)
	rec = call(t, fx.s, "POST", "/api/v1/auth/verify", "",
		map[string]any{"token": created.Verification.Token, "password": testPassword})
	wantStatus(t, rec, 200)
	return sessionFrom(t, fx.s, rec)
}

// accountLinks lists the account's links as the caller reads them, with an optional query.
func accountLinks(t *testing.T, s *Server, cred, acc, query string) *httptest.ResponseRecorder {
	t.Helper()
	return call(t, s, "GET", "/api/v1/accounts/"+acc+"/share-links"+query, cred, nil)
}

// listedBy returns the rows of a list answer, by link ID.
func listedBy(t *testing.T, rec *httptest.ResponseRecorder) map[string]accountShareLinkJSON {
	t.Helper()
	wantStatus(t, rec, 200)
	out := map[string]accountShareLinkJSON{}
	for _, it := range decode[listResponse[accountShareLinkJSON]](t, rec).Items {
		out[it.ID] = it
	}
	return out
}

// TestAccountLinkListWhoSeesWhat: the key sees every item; the owner sees the items of other members' My files without
// their name or path; an admin sees no item at all; members and tokens are refused; another account's links never appear.
func TestAccountLinkListWhoSeesWhat(t *testing.T) {
	fx := newDropFixture(t)
	owner := fx.ownerSession(t, "boss@example.com")
	pf := fx.personalFolder(t, fx.annSess)
	keep := fx.sessionFile(t, fx.annSess, pf, "/keep.txt", "keep")
	orgLink, orgURL := fx.makeLink(t, fx.c.ID, store.RoleViewer)
	fx.openedAs(t, fx.annSess, secretOf(t, orgURL))
	anyone, _ := fx.makeAnyone(t, fx.sib.ID, nil)
	drop, _ := fx.makeDrop(t, fx.b.ID, nil)
	mine, _ := fx.makeAnyone(t, keep.ID, nil)
	// Another account's link must never be listed here.
	other, otherKey := newAccount(t, fx.s, fx.admin, "Other")
	otherFld := newFolder(t, fx.s, otherKey, other.ID, "Elsewhere")
	otherRoot, err := fx.s.store.RootNode(context.Background(), other.ID, otherFld.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/folders/"+otherFld.ID+"/settings", otherKey,
		map[string]any{"allow_links": true}), 200)
	setPolicy(t, fx.s, otherKey, other.ID, true)
	foreign := call(t, fx.s, "POST", "/api/v1/nodes/"+otherRoot.ID+"/share-links", otherKey,
		map[string]any{"audience": "anyone"})
	wantStatus(t, foreign, 201)

	acc := fx.acc.ID
	byKey := listedBy(t, accountLinks(t, fx.s, fx.key, acc, ""))
	if len(byKey) != 4 {
		t.Fatalf("the key lists %d links; want the 4 of this account", len(byKey))
	}
	for _, id := range []string{orgLink.ID, anyone.ID, drop.ID, mine.ID} {
		if it, ok := byKey[id]; !ok || it.Hidden || it.ItemName == nil || it.ItemPath == nil {
			t.Errorf("the key does not see link %s with its item: %+v", id, it)
		}
	}
	if it := byKey[mine.ID]; it.FolderKind != store.KindPersonal || it.FolderOwnerName == "" {
		t.Errorf("a My files link names its folder as %q, %q; want a personal folder with its owner", it.FolderKind, it.FolderOwnerName)
	}
	if it := byKey[mine.ID]; it.ItemPath == nil || *it.ItemPath != "/keep.txt" {
		t.Errorf("the item path of the My files link is %v; want /keep.txt", it.ItemPath)
	}
	if it := byKey[orgLink.ID]; it.Visitors == nil || *it.Visitors != 1 || it.ItemPath == nil || *it.ItemPath != "/a/c" {
		t.Errorf("the organisation link shows visitors %v and path %v; want 1 and /a/c", it.Visitors, it.ItemPath)
	}
	if byKey[anyone.ID].Visitors != nil {
		t.Errorf("an anyone link shows visitors; it must not")
	}

	// The owner: its own links and the team space's show; another member's My files shows no name or path.
	byOwner := listedBy(t, callSession(t, fx.s, owner, "GET", "/api/v1/accounts/"+acc+"/share-links", nil))
	if it := byOwner[mine.ID]; !it.Hidden || it.ItemName != nil || it.ItemPath != nil || it.NodeID != "" {
		t.Errorf("the owner sees the item of a member's My files: %+v", it)
	}
	if it := byOwner[mine.ID]; it.FolderOwnerName == "" || it.CreatedByKind != "account_key" {
		t.Errorf("the owner does not see whose My files the link is in, or who made it: %+v", it)
	}
	if it := byOwner[anyone.ID]; it.Hidden || it.ItemName == nil {
		t.Errorf("the owner does not see a team-space item: %+v", it)
	}
	// The owner's own My files show normally: its name, path and node.
	ownPF := fx.personalFolder(t, owner)
	ownKeep := fx.sessionFile(t, owner, ownPF, "/mine.txt", "mine")
	ownLink, _ := fx.makeAnyone(t, ownKeep.ID, nil)
	if it := listedBy(t, callSession(t, fx.s, owner, "GET", "/api/v1/accounts/"+acc+"/share-links", nil))[ownLink.ID]; it.Hidden ||
		it.ItemName == nil || it.NodeID != ownKeep.ID || it.ItemPath == nil || *it.ItemPath != "/mine.txt" {
		t.Errorf("the owner's own My files link is hidden or unnamed: %+v", it)
	}

	// The admin sees the same rows, and no item at all.
	byAdmin := listedBy(t, accountLinks(t, fx.s, fx.admin, acc, ""))
	if len(byAdmin) != 5 {
		t.Fatalf("the admin lists %d links; want the 5 of this account", len(byAdmin))
	}
	for id, it := range byAdmin {
		if !it.Hidden || it.ItemName != nil || it.ItemPath != nil || it.NodeID != "" {
			t.Errorf("the admin sees the item of link %s: %+v", id, it)
		}
	}

	// A member and a token are refused as the account's usual not found.
	wantError(t, accountLinks(t, fx.s, "", acc, ""), 401, apierr.Unauthenticated)
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/accounts/"+acc+"/share-links", nil), 404, apierr.NotFound)
	tok := mintFor(t, fx.s, fx.key, acc, map[string]any{"folders": []string{fx.fld.ID}})
	wantError(t, accountLinks(t, fx.s, tok, acc, ""), 404, apierr.NotFound)
}

// TestAccountLinkListFiltersPagingAndEnds: the filters narrow the list, a page's cursor leads to the next, a link
// past its end is left out, and a paused link is said to be paused.
func TestAccountLinkListFiltersPagingAndEnds(t *testing.T) {
	fx := newDropFixture(t)
	orgLink, orgURL := fx.makeLink(t, fx.c.ID, store.RoleViewer)
	fx.openedAs(t, fx.annSess, secretOf(t, orgURL))
	a1, _ := fx.makeAnyone(t, fx.sib.ID, nil)
	a2, _ := fx.makeAnyone(t, fx.deep.ID, nil)
	drop, _ := fx.makeDrop(t, fx.b.ID, nil)
	acc := fx.acc.ID

	count := func(query string) int {
		t.Helper()
		return len(listedBy(t, accountLinks(t, fx.s, fx.key, acc, query)))
	}
	if n := count("?audience=organisation"); n != 1 {
		t.Errorf("audience=organisation lists %d; want 1", n)
	}
	if n := count("?audience=anyone"); n != 3 {
		t.Errorf("audience=anyone lists %d; want 3", n)
	}
	if n := count("?kind=drop"); n != 1 {
		t.Errorf("kind=drop lists %d; want 1", n)
	}
	if n := count("?kind=access&audience=anyone"); n != 2 {
		t.Errorf("kind=access&audience=anyone lists %d; want 2", n)
	}
	wantError(t, accountLinks(t, fx.s, fx.key, acc, "?audience=members"), 400, apierr.InvalidRequest)
	wantError(t, accountLinks(t, fx.s, fx.key, acc, "?kind=everything"), 400, apierr.InvalidRequest)

	// Paging: two rows a page, following next_cursor until it runs out, shows every link once.
	seen := map[string]bool{}
	query := "?limit=2"
	for pages := 0; pages < 5; pages++ {
		rec := accountLinks(t, fx.s, fx.key, acc, query)
		wantStatus(t, rec, 200)
		page := decode[listResponse[accountShareLinkJSON]](t, rec)
		for _, it := range page.Items {
			if seen[it.ID] {
				t.Errorf("link %s is listed on two pages", it.ID)
			}
			seen[it.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		query = "?limit=2&cursor=" + page.NextCursor
	}
	if len(seen) != 4 {
		t.Errorf("paging lists %d links; want all 4", len(seen))
	}

	// A link past its end leaves the list. Within its grace the sweep still keeps it, but no listing shows it.
	fx.setExpiry(t, a2.ID, time.Now().Add(-time.Minute))
	if _, ok := listedBy(t, accountLinks(t, fx.s, fx.key, acc, ""))[a2.ID]; ok {
		t.Errorf("the ended link %s is still listed", a2.ID)
	}

	// Switching the account's policy off pauses the anyone links and nothing else.
	setPolicy(t, fx.s, fx.key, acc, false)
	byKey := listedBy(t, accountLinks(t, fx.s, fx.key, acc, ""))
	if !byKey[a1.ID].Paused || !byKey[drop.ID].Paused {
		t.Errorf("anyone and drop links are not paused while the switch is off")
	}
	if byKey[orgLink.ID].Paused {
		t.Errorf("an organisation link is paused by the anyone switch")
	}
}

// publicLinksFixture makes an organisation link with a member's visit, an anyone link with a visitor, and a drop link
// with an open upload. It returns the fixture, the organisation link, the anyone link's and the drop link's visitor
// credentials, and the drop link's open upload.
func publicLinksFixture(t *testing.T) (fx *dropFixture, org shareLinkJSON, anyoneTok, dropTok, upl string) {
	t.Helper()
	fx = newDropFixture(t)
	org, orgURL := fx.makeLink(t, fx.c.ID, store.RoleViewer)
	fx.openedAs(t, fx.annSess, secretOf(t, orgURL))
	_, anyoneSecret := fx.makeAnyone(t, fx.sib.ID, nil)
	anyoneTok = fx.visitAnyone(t, anyoneSecret, "")
	_, dropSecret := fx.makeDrop(t, fx.b.ID, nil)
	dropTok = fx.visitDrop(t, dropSecret, "")
	upl = uploadID(t, createUpload(t, fx.s, dropTok, 10, md("name", "open.bin")))
	wantStatus(t, piece(t, fx.s, dropTok, upl, 0, "12345", nil), 204)
	return fx, org, anyoneTok, dropTok, upl
}

// TestDeletePublicShareLinksRules: a member and a token are refused; the account key and an owner session delete
// every link that anyone can open, and only those.
func TestDeletePublicShareLinksRules(t *testing.T) {
	path := func(fx *dropFixture) string { return "/api/v1/accounts/" + fx.acc.ID + "/share-links/delete-public" }

	t.Run("refused to a member and a token", func(t *testing.T) {
		fx, _, _, _, _ := publicLinksFixture(t)
		wantError(t, callSession(t, fx.s, fx.annSess, "POST", path(fx), nil), 404, apierr.NotFound)
		tok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}})
		wantError(t, call(t, fx.s, "POST", path(fx), tok, nil), 404, apierr.NotFound)
	})

	for name, who := range map[string]func(fx *dropFixture) (*httptest.ResponseRecorder, string){
		"key": func(fx *dropFixture) (*httptest.ResponseRecorder, string) {
			return call(t, fx.s, "POST", path(fx), fx.key, nil), "account_key"
		},
		"owner": func(fx *dropFixture) (*httptest.ResponseRecorder, string) {
			owner := fx.ownerSession(t, "boss"+strconv.FormatInt(time.Now().UnixNano(), 10)+"@example.com")
			return callSession(t, fx.s, owner, "POST", path(fx), nil), "owner"
		},
	} {
		who := who
		t.Run(name, func(t *testing.T) {
			fx, org, anyoneTok, dropTok, upl := publicLinksFixture(t)
			ctx := context.Background()
			rec, _ := who(fx)
			wantStatus(t, rec, 200)
			if got := decode[map[string]int](t, rec)["deleted"]; got != 2 {
				t.Fatalf("deleted %d links; want the anyone link and the drop link", got)
			}
			// Visitors lose access at once; the drop link's open upload is gone with its reservation.
			deadVisitor(t, fx.visitor(t, anyoneTok, "GET", "/api/v1/nodes/"+fx.sib.ID))
			deadVisitor(t, fx.visitor(t, dropTok, "HEAD", "/api/v1/uploads/"+upl))
			if _, err := fx.s.store.Upload(ctx, fx.acc.ID, upl); err == nil {
				t.Errorf("the open upload of the deleted drop link survived")
			}
			if got := fx.reservedBytes(t); got != 0 {
				t.Errorf("reserved %d bytes after the drop link was deleted; want 0", got)
			}
			// The organisation link stays, and its member keeps the access it gives.
			if _, err := fx.s.store.ShareLink(ctx, fx.acc.ID, org.ID); err != nil {
				t.Errorf("the organisation link was deleted: %v", err)
			}
			wantStatus(t, fx.stat(t, fx.annSess, fx.c.ID), 200)
			// Each link has its entry in the audit log, and one entry names the count.
			entries := fx.auditSeenBy(t, "")
			if !auditHas(entries, "link.delete_public", fx.acc.ID) {
				t.Errorf("no link.delete_public entry")
			}
			if n := fx.countRows(t, "SELECT COUNT(*) FROM audit_log WHERE action = 'link.delete'"); n < 2 {
				t.Errorf("link.delete entries = %d; want one per deleted link", n)
			}
			// Nothing left to delete: the second call answers zero.
			rec, _ = who(fx)
			wantStatus(t, rec, 200)
			if got := decode[map[string]int](t, rec)["deleted"]; got != 0 {
				t.Errorf("a second call deleted %d; want 0", got)
			}
		})
	}
}

// TestOwnerDeletesAMembersMyFilesLink: an owner session, and the account key, delete a link on a member's My files, which
// the owner cannot reach. A member still cannot delete a link it does not reach, nor another member's link.
func TestOwnerDeletesAMembersMyFilesLink(t *testing.T) {
	fx := newDropFixture(t)
	pf := fx.personalFolder(t, fx.annSess)
	keep := fx.sessionFile(t, fx.annSess, pf, "/keep.txt", "keep")
	mine, _ := fx.makeAnyone(t, keep.ID, nil)
	team, _ := fx.makeAnyone(t, fx.sib.ID, nil)
	owner := fx.ownerSession(t, "owner"+strconv.FormatInt(time.Now().UnixNano(), 10)+"@example.com")
	ctx := context.Background()

	// Bob, a member with no role on Ann's files, cannot delete the link there, nor a team link he does not share.
	wantError(t, callSession(t, fx.s, fx.bobSess, "DELETE", "/api/v1/share-links/"+mine.ID, nil), 404, apierr.NotFound)
	wantError(t, callSession(t, fx.s, fx.bobSess, "DELETE", "/api/v1/share-links/"+team.ID, nil), 404, apierr.NotFound)
	if _, err := fx.s.store.ShareLink(ctx, fx.acc.ID, mine.ID); err != nil {
		t.Fatalf("a member removed a link it does not reach: %v", err)
	}

	// The owner removes it; the visitors' credential would now be refused, and the audit names the folder's user.
	wantStatus(t, callSession(t, fx.s, owner, "DELETE", "/api/v1/share-links/"+mine.ID, nil), 204)
	if _, err := fx.s.store.ShareLink(ctx, fx.acc.ID, mine.ID); err == nil {
		t.Errorf("the owner's delete left the link")
	}
	if !auditHas(fx.auditSeenBy(t, fx.ann.ID), "link.delete", mine.ID) {
		t.Errorf("the folder's user does not see the owner's delete of its link")
	}
	if auditHas(fx.auditSeenBy(t, fx.bob.ID), "link.delete", mine.ID) {
		t.Errorf("a member sees the delete of a link in another member's My files")
	}

	// The account key may delete a team link as before.
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/share-links/"+team.ID, fx.key, nil), 204)
}

// badgesFor lists a directory's children as a session or key reads them, and returns each name's badge.
func (fx *dropFixture) badgesFor(t *testing.T, cred string, sess *testSession, dirID string) map[string]bool {
	t.Helper()
	var rec *httptest.ResponseRecorder
	if sess != nil {
		rec = callSession(t, fx.s, *sess, "GET", "/api/v1/nodes/"+dirID+"/children", nil)
	} else {
		rec = call(t, fx.s, "GET", "/api/v1/nodes/"+dirID+"/children", cred, nil)
	}
	wantStatus(t, rec, 200)
	out := map[string]bool{}
	for _, it := range decode[listResponse[nodeJSON]](t, rec).Items {
		out[it.Name] = it.HasLink
	}
	return out
}

// TestChildrenShowLinkBadges: a listing marks the items that carry a link, for what the caller may know of. A sharer
// sees every live link, drop links included; a member sees the access links its listing would show, and loses them
// with the team space's links switch; a visitor sees none.
func TestChildrenShowLinkBadges(t *testing.T) {
	fx := newDropFixture(t)
	fx.setPerm(t, fx.a.ID, fx.ann.ID, store.RoleViewer)
	fx.makeLink(t, fx.b.ID, store.RoleViewer)
	fx.makeDrop(t, fx.c.ID, nil)

	key := fx.badgesFor(t, fx.key, nil, fx.a.ID)
	if !key["b"] || !key["c"] {
		t.Errorf("the key sees badges %v; want b (organisation) and c (drop)", key)
	}
	member := fx.badgesFor(t, "", &fx.annSess, fx.a.ID)
	if !member["b"] || member["c"] {
		t.Errorf("a member sees badges %v; want b, and no badge for the drop link c", member)
	}
	fx.settings(t, fx.fld.ID, map[string]any{"allow_links": false})
	if member := fx.badgesFor(t, "", &fx.annSess, fx.a.ID); member["b"] {
		t.Errorf("a member sees a badge for a paused link: %v", member)
	}
	if key := fx.badgesFor(t, fx.key, nil, fx.a.ID); !key["b"] {
		t.Errorf("the key loses the badge of a paused link; a sharer keeps it: %v", key)
	}
}

// listedNodeIDs returns the IDs of a session's listing, and the folder IDs of its folders listing.
func folderListIDs(t *testing.T, fx *linkFixture, sess testSession) []string {
	t.Helper()
	rec := callSession(t, fx.s, sess, "GET", "/api/v1/folders", nil)
	wantStatus(t, rec, 200)
	var ids []string
	for _, it := range decode[listResponse[folderAccessJSON]](t, rec).Items {
		ids = append(ids, it.ID)
	}
	return ids
}

// TestOrganisationLinkOnATeamRootListsTheSpace: a member who opens an organisation link on a team space's root finds
// the space under Team spaces, as a member with a permission on the root does, and not only under Shared with me.
func TestOrganisationLinkOnATeamRootListsTheSpace(t *testing.T) {
	fx := newLinkFixture(t)
	_, url := fx.makeLink(t, fx.root, store.RoleViewer)
	before := folderListIDs(t, fx, fx.annSess)
	fx.openedAs(t, fx.annSess, secretOf(t, url))
	after := folderListIDs(t, fx, fx.annSess)
	if slicesContains(before, fx.fld.ID) || !slicesContains(after, fx.fld.ID) {
		t.Errorf("team spaces before the visit %v, after %v; the visited space must appear after it", before, after)
	}
	// It appears with the link's role, the same as a permission on the root would give.
	rec := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/folders", nil)
	for _, it := range decode[listResponse[folderAccessJSON]](t, rec).Items {
		if it.ID == fx.fld.ID && it.Role != store.RoleViewer {
			t.Errorf("the visited space shows role %q; want viewer", it.Role)
		}
	}
	// A role below the root is not a root: a link on a directory does not list the space.
	fx2 := newLinkFixture(t)
	_, url2 := fx2.makeLink(t, fx2.b.ID, store.RoleViewer)
	fx2.openedAs(t, fx2.annSess, secretOf(t, url2))
	if slicesContains(folderListIDs(t, fx2, fx2.annSess), fx2.fld.ID) {
		t.Errorf("a link on a directory listed its whole team space")
	}
}

// TestSharedWithMeListsANodeOnce: a node a member reaches by a permission and by a link it opened is listed once.
func TestSharedWithMeListsANodeOnce(t *testing.T) {
	fx := newLinkFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, store.RoleViewer)
	_, url := fx.makeLink(t, fx.b.ID, store.RoleEditor)
	fx.openedAs(t, fx.annSess, secretOf(t, url))
	rec := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/shared", nil)
	wantStatus(t, rec, 200)
	seen := 0
	for _, it := range decode[listResponse[nodeJSON]](t, rec).Items {
		if it.ID == fx.b.ID {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("the shared list shows the node %d times; want once", seen)
	}
}

func slicesContains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// TestChildBadgesNeverReachATokenSharer: a delegated token never manages links, so its listing shows no badge for a
// drop link, an anyone link or a paused link, even when the account key or a user that manages the folder would see one.
func TestChildBadgesNeverReachATokenSharer(t *testing.T) {
	fx := newDropFixture(t)
	fx.setPerm(t, fx.a.ID, fx.ann.ID, store.RoleManager)
	fx.makeDrop(t, fx.b.ID, nil)
	_, anyoneSecret := fx.makeAnyone(t, fx.c.ID, nil)
	_ = anyoneSecret

	// The key sees the badges on a's children.
	key := fx.badgesFor(t, fx.key, nil, fx.a.ID)
	if !key["b"] || !key["c"] {
		t.Fatalf("the key sees badges %v; want b (drop) and c (anyone)", key)
	}
	// The user who manages a sees them too, through its own session.
	if user := fx.badgesFor(t, "", &fx.annSess, fx.a.ID); !user["b"] || !user["c"] {
		t.Fatalf("the manager sees badges %v; want b and c", user)
	}

	// A key-minted read token: its caller is the account key, reading only what the token reaches.
	keyTok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}})
	// A user-minted read token for the manager.
	userTok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}, "user": fx.ann.ID})
	// Both tokens are capped to viewer, so they get the viewer's badges: the anyone link on c, which any member of the
	// account sees, but never the drop link on b, which only a sharer sees.
	for name, want := range map[string]map[string]bool{
		"key token":  {"b": false, "c": true},
		"user token": {"b": false, "c": true},
	} {
		tok := keyTok
		if name == "user token" {
			tok = userTok
		}
		got := fx.badgesFor(t, tok, nil, fx.a.ID)
		for child, w := range want {
			if got[child] != w {
				t.Errorf("%s: child %s has_link = %v; want %v (a token may not manage links)", name, child, got[child], w)
			}
		}
	}
}

// TestOneBadEndedLinkDoesNotStopTheSweep: a link whose removal fails is skipped and logged. The other ended links are
// removed in the same pass, the failed one is kept for the next pass, and the sweeps after the links still run.
func TestOneBadEndedLinkDoesNotStopTheSweep(t *testing.T) {
	fx := newDropFixture(t)
	ctx := context.Background()
	past := time.Now().Add(-25 * time.Hour)
	// Three ended links, the first in order: its removal is made to fail.
	bad, _ := fx.makeAnyone(t, fx.b.ID, nil)
	good1, _ := fx.makeAnyone(t, fx.c.ID, nil)
	good2, _ := fx.makeAnyone(t, fx.sib.ID, nil)
	for _, l := range []shareLinkJSON{bad, good1, good2} {
		fx.setExpiry(t, l.ID, past)
	}
	// SQLite triggers take no bound values, so the refused IDs live in a table the trigger reads.
	fx.execDB(t, `CREATE TABLE refused_links (id TEXT PRIMARY KEY)`)
	fx.execDB(t, `INSERT INTO refused_links (id) VALUES (?)`, bad.ID)
	fx.execDB(t, `CREATE TRIGGER refuse_bad_link BEFORE DELETE ON share_links
		WHEN OLD.id IN (SELECT id FROM refused_links)
		BEGIN SELECT RAISE(ABORT, 'injected failure'); END`)
	// A later sweep stage: a permission that ended more than a day ago, which the same pass must still remove.
	fx.insertPermission(t, &store.Permission{NodeID: fx.deep.ID, AccountID: fx.acc.ID, SubjectKind: store.SubjectUser,
		SubjectID: fx.ann.ID, Role: store.RoleViewer, CreatedAt: past.Add(-time.Hour), CreatedBy: "test", ExpiresAt: &past})

	sw := sweep.New(sweep.Options{Store: fx.s.store, Blobs: fx.s.blobs, DeleteGrace: time.Hour})
	res, err := sw.Once(ctx, time.Now())
	if err != nil {
		t.Fatalf("a failed link stopped the sweep: %v", err)
	}
	if res.Links != 2 {
		t.Errorf("removed %d links; want the two that did not fail", res.Links)
	}
	for _, id := range []string{good1.ID, good2.ID} {
		if _, err := fx.s.store.ShareLink(ctx, fx.acc.ID, id); err == nil {
			t.Errorf("ended link %s survived the sweep", id)
		}
	}
	if _, err := fx.s.store.ShareLink(ctx, fx.acc.ID, bad.ID); err != nil {
		t.Errorf("the failed link is gone: %v", err)
	}
	if res.Permissions != 1 {
		t.Errorf("the later permission sweep removed %d; want 1", res.Permissions)
	}
}
