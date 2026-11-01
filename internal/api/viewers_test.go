// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/store"
)

// viewersSwitch turns the account's who-viewed switch on or off as the account key, and wants 200.
func viewersSwitch(t *testing.T, fx *activityFixture, on bool) {
	t.Helper()
	rec := fx.keyDo("PATCH", "/api/v1/accounts/"+fx.acc.ID+"/sharing-policy", map[string]any{"who_viewed": on})
	wantStatus(t, rec, 200)
	if got := decode[sharingPolicyJSON](t, rec).WhoViewed; got != on {
		t.Fatalf("the policy answer says who_viewed %v after setting %v", got, on)
	}
}

// viewersRead reads the file's content as a session user, which is a view, and wants 200.
func viewersRead(t *testing.T, fx *activityFixture, sess testSession, nodeID string) {
	t.Helper()
	wantStatus(t, callSession(t, fx.s, sess, "GET", "/api/v1/nodes/"+nodeID+"/content", nil), 200)
	flushViews(t, fx.s)
}

// viewersList reads the viewers of nodeID as do, with query appended, and wants 200.
func viewersList(t *testing.T, do asCaller, nodeID, query string) listResponse[viewerJSON] {
	t.Helper()
	rec := do("GET", "/api/v1/nodes/"+nodeID+"/viewers"+query, nil)
	wantStatus(t, rec, 200)
	return decode[listResponse[viewerJSON]](t, rec)
}

// viewersOf returns the user IDs of a list, in order.
func viewersOf(l listResponse[viewerJSON]) []string {
	out := make([]string, len(l.Items))
	for i, it := range l.Items {
		out[i] = it.UserID
	}
	return out
}

// TestViewersOffByDefaultAndOnForManagers: the switch is off at first, and the list is refused with 403 then. Once the
// key turns it on, the owner reads the views made since.
func TestViewersOffByDefaultAndOnForManagers(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	f := fx.deep.ID
	viewersRead(t, fx, fx.bobSess, f)
	wantError(t, fx.ownerDo("GET", "/api/v1/nodes/"+f+"/viewers", nil), 403, apierr.Forbidden)
	wantError(t, fx.keyDo("GET", "/api/v1/nodes/"+f+"/viewers", nil), 403, apierr.Forbidden)

	viewersSwitch(t, fx, true)
	time.Sleep(5 * time.Millisecond)
	viewersRead(t, fx, fx.annSess, f)
	if l := viewersList(t, fx.ownerDo, f, ""); !slices.Equal(viewersOf(l), []string{fx.ann.ID}) {
		t.Fatalf("after the switch is on the owner reads %v, want the viewer since then", viewersOf(l))
	}
	viewersSwitch(t, fx, false)
	wantError(t, fx.ownerDo("GET", "/api/v1/nodes/"+f+"/viewers", nil), 403, apierr.Forbidden)
}

// TestViewersListsTheLastViewPerUser: a second view within the ten-minute gap is not written, and each user is listed
// once, newest view first, with a name.
func TestViewersListsTheLastViewPerUser(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	f := fx.deep.ID
	viewersSwitch(t, fx, true)
	viewersRead(t, fx, fx.bobSess, f)
	time.Sleep(3 * time.Millisecond)
	viewersRead(t, fx, fx.annSess, f)
	viewersRead(t, fx, fx.bobSess, f)
	l := viewersList(t, fx.ownerDo, f, "")
	if !slices.Equal(viewersOf(l), []string{fx.ann.ID, fx.bob.ID}) {
		t.Fatalf("the list is %v, want ann (the later view) then bob", viewersOf(l))
	}
	if l.Items[0].Name != "Ann Cohen" || l.Items[1].Name != "Bob Levi" {
		t.Errorf("the names are %q and %q", l.Items[0].Name, l.Items[1].Name)
	}
	if l.Items[0].ViewedAt.Before(l.Items[1].ViewedAt) {
		t.Errorf("the list is not newest first: %v before %v", l.Items[0].ViewedAt, l.Items[1].ViewedAt)
	}
}

// TestViewersRoleTiersAndCallers: a viewer and an editor are refused, a user with no role is not found, a delegated token
// is refused whoever minted it (a user's token, an owner's token, a key's token), and the account key and a manager read.
func TestViewersRoleTiersAndCallers(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	f := fx.deep.ID
	viewersSwitch(t, fx, true)
	viewersRead(t, fx, fx.bobSess, f)

	wantError(t, fx.bobDo("GET", "/api/v1/nodes/"+f+"/viewers", nil), 403, apierr.Forbidden)
	wantError(t, fx.annDo("GET", "/api/v1/nodes/"+f+"/viewers", nil), 403, apierr.Forbidden)
	wantError(t, fx.carolDo("GET", "/api/v1/nodes/"+f+"/viewers", nil), 404, apierr.NotFound)

	ownerTok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}, "user": fx.owner.ID})
	wantError(t, byCred(t, fx.s, ownerTok)("GET", "/api/v1/nodes/"+f+"/viewers", nil), 403, apierr.Forbidden)
	keyTok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}})
	wantError(t, byCred(t, fx.s, keyTok)("GET", "/api/v1/nodes/"+f+"/viewers", nil), 403, apierr.Forbidden)

	// A manager of the file reads it, and so does the account key.
	for name, do := range map[string]asCaller{"a manager": fx.ownerDo, "the account key": fx.keyDo} {
		if l := viewersList(t, do, f, ""); !slices.Equal(viewersOf(l), []string{fx.bob.ID}) {
			t.Errorf("%s reads %v, want bob", name, viewersOf(l))
		}
	}
}

// TestViewersTokensDoNotViewAndAreNotListed: a read made with a token is not a view, so it is not listed; a user's
// session read is. An installation admin is refused.
func TestViewersTokensDoNotViewAndAreNotListed(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	f := fx.deep.ID
	viewersSwitch(t, fx, true)
	tok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}, "user": fx.bob.ID})
	wantStatus(t, getWith(t, fx.s, tok, "/api/v1/nodes/"+f+"/content"), 200)
	flushViews(t, fx.s)
	if l := viewersList(t, fx.ownerDo, f, ""); len(l.Items) != 0 {
		t.Fatalf("a token read is listed: %v", viewersOf(l))
	}
	wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+f+"/viewers", fx.admin, nil), 403, apierr.Forbidden)
}

// TestViewersNamesAndGone: a person who no longer reaches the file is listed as gone, with no name. The name of a person
// who still reaches it is shown.
func TestViewersNamesAndGone(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	f := fx.deep.ID
	viewersSwitch(t, fx, true)
	viewersRead(t, fx, fx.bobSess, f)
	viewersRead(t, fx, fx.annSess, f)
	wantStatus(t, fx.keyDo("DELETE", "/api/v1/nodes/"+fx.b.ID+"/permissions/"+fx.bob.ID, nil), 204)
	l := viewersList(t, fx.ownerDo, f, "")
	for _, it := range l.Items {
		switch it.UserID {
		case fx.bob.ID:
			if !it.Gone || it.Name != "" {
				t.Errorf("bob lost access and is %+v, want gone with no name", it)
			}
		case fx.ann.ID:
			if it.Gone || it.Name != "Ann Cohen" {
				t.Errorf("ann still reaches the file and is %+v, want her name", it)
			}
		}
	}
	if len(l.Items) != 2 {
		t.Fatalf("the list has %d lines, want 2", len(l.Items))
	}
}

// TestViewersPastTheBoundNobodyIsNamedOrCalledGone: with a walk bound of one, the second person is neither named nor
// called gone, since nobody checked. The first is checked.
func TestViewersPastTheBoundNobodyIsNamedOrCalledGone(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	f := fx.deep.ID
	viewersSwitch(t, fx, true)
	viewersRead(t, fx, fx.bobSess, f)
	viewersRead(t, fx, fx.annSess, f)
	fx.s.activityWalks = 1
	l := viewersList(t, fx.ownerDo, f, "")
	if len(l.Items) != 2 {
		t.Fatalf("the list has %d lines, want 2", len(l.Items))
	}
	named := 0
	for _, it := range l.Items {
		if it.Name != "" {
			named++
		}
		if it.Name == "" && it.Gone {
			t.Errorf("a person nobody checked is called gone: %+v", it)
		}
	}
	if named != 1 {
		t.Fatalf("%d people are named with a bound of one, want 1", named)
	}
}

// TestViewersPaging: a page holds the limit and carries a cursor to the rest; a cursor from another caller's list or a
// bad limit is refused.
func TestViewersPaging(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	f := fx.deep.ID
	viewersSwitch(t, fx, true)
	viewersRead(t, fx, fx.bobSess, f)
	viewersRead(t, fx, fx.annSess, f)
	first := viewersList(t, fx.ownerDo, f, "?limit=1")
	if len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("the first page is %v with cursor %q, want one line and a cursor", viewersOf(first), first.NextCursor)
	}
	second := viewersList(t, fx.ownerDo, f, "?limit=1&cursor="+first.NextCursor)
	if len(second.Items) != 1 || second.NextCursor != "" || second.Items[0].UserID == first.Items[0].UserID {
		t.Fatalf("the second page is %v with cursor %q, want the other viewer and no cursor", viewersOf(second), second.NextCursor)
	}
	wantError(t, fx.ownerDo("GET", "/api/v1/nodes/"+f+"/viewers?limit=0", nil), 400, apierr.InvalidRequest)
	wantError(t, fx.ownerDo("GET", "/api/v1/nodes/"+f+"/viewers?cursor=not-a-cursor!", nil), 400, apierr.InvalidRequest)
}

// TestViewersMintIsAView: a signed-in person who mints a signed link counts as a view, in Recent and in who-viewed, since
// the web interface opens files through signed links. The fact that the person minted and did not open is the stated
// limit (decision 51). Without the call in the mint, both lists lack the person and this test fails.
func TestViewersMintIsAView(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	f := fx.deep.ID
	viewersSwitch(t, fx, true)
	rec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+f+"/links", map[string]any{})
	wantStatus(t, rec, 201)
	flushViews(t, fx.s)
	if l := viewersList(t, fx.ownerDo, f, ""); !slices.Equal(viewersOf(l), []string{fx.ann.ID}) {
		t.Fatalf("who-viewed after a mint is %v, want ann", viewersOf(l))
	}
	recent := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/recent", nil)
	wantStatus(t, recent, 200)
	if !strings.Contains(recent.Body.String(), f) {
		t.Fatalf("Recent after a mint does not list the file: %s", recent.Body.String())
	}
}

// TestViewersClearingRecentLeavesTheList: who-viewed reads the recent table, so a person who clears the file from Recent,
// or clears Recent, is off the list. The list is a convenience and not evidence (decision 51).
func TestViewersClearingRecentLeavesTheList(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	f := fx.deep.ID
	viewersSwitch(t, fx, true)
	viewersRead(t, fx, fx.bobSess, f)
	viewersRead(t, fx, fx.annSess, f)
	if l := viewersList(t, fx.ownerDo, f, ""); len(l.Items) != 2 {
		t.Fatalf("before clearing the list has %d lines, want 2", len(l.Items))
	}
	wantStatus(t, callSession(t, fx.s, fx.bobSess, "DELETE", "/api/v1/recent/"+f, nil), 204)
	if l := viewersList(t, fx.ownerDo, f, ""); !slices.Equal(viewersOf(l), []string{fx.ann.ID}) {
		t.Fatalf("after bob removed the file from Recent the list is %v, want ann only", viewersOf(l))
	}
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/recent", nil), 204)
	if l := viewersList(t, fx.ownerDo, f, ""); len(l.Items) != 0 {
		t.Fatalf("after ann cleared Recent the list has %v", viewersOf(l))
	}
}

// TestViewersRefusesAShortcutAndATrashedFile: a shortcut and a trashed file answer not_found, as they do for every node
// route, and the list comes back after a restore.
func TestViewersRefusesAShortcutAndATrashedFile(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	f := fx.deep.ID
	viewersSwitch(t, fx, true)
	viewersRead(t, fx, fx.bobSess, f)
	rec := fx.keyDo("POST", "/api/v1/nodes/"+fx.b.ID+"/shortcuts", map[string]any{"target": f, "name": "shortcut.txt"})
	wantStatus(t, rec, 201)
	sc := decode[nodeJSON](t, rec)
	wantError(t, fx.ownerDo("GET", "/api/v1/nodes/"+sc.ID+"/viewers", nil), 404, apierr.NotFound)

	wantStatus(t, fx.keyDo("DELETE", "/api/v1/nodes/"+f, nil), 204)
	wantError(t, fx.ownerDo("GET", "/api/v1/nodes/"+f+"/viewers", nil), 404, apierr.NotFound)
	wantStatus(t, fx.keyDo("POST", "/api/v1/nodes/"+f+"/restore", nil), 200)
	if l := viewersList(t, fx.ownerDo, f, ""); !slices.Equal(viewersOf(l), []string{fx.bob.ID}) {
		t.Fatalf("after the restore the list is %v, want bob", viewersOf(l))
	}
}

// TestViewersIsRateLimited: the route has its own two limits, per caller and per account, and a refused request is
// answered 429.
func TestViewersIsRateLimited(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	f := fx.deep.ID
	viewersSwitch(t, fx, true)
	fx.s.limits["viewers"] = newLimiter(2)
	for i := 0; i < 2; i++ {
		wantStatus(t, fx.ownerDo("GET", "/api/v1/nodes/"+f+"/viewers", nil), 200)
	}
	wantError(t, fx.ownerDo("GET", "/api/v1/nodes/"+f+"/viewers", nil), http.StatusTooManyRequests, apierr.RateLimited)
	fx.s.limits["viewers"] = newLimiter(100)
	fx.s.limits["viewers.account"] = newLimiter(1)
	wantStatus(t, fx.keyDo("GET", "/api/v1/nodes/"+f+"/viewers", nil), 200)
	wantError(t, fx.keyDo("GET", "/api/v1/nodes/"+f+"/viewers", nil), http.StatusTooManyRequests, apierr.RateLimited)
}

// TestViewersSwitchIsTheOwnersOrTheKeys: the owner and the key set the switch, and a manager of a file does not.
func TestViewersSwitchIsTheOwnersOrTheKeys(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	rec := fx.ownerDo("PATCH", "/api/v1/accounts/"+fx.acc.ID+"/sharing-policy", map[string]any{"who_viewed": true})
	wantStatus(t, rec, 200)
	if got := decode[sharingPolicyJSON](t, rec).WhoViewed; !got {
		t.Fatal("the owner turned the switch on and the answer says it is off")
	}
	if rec := fx.annDo("PATCH", "/api/v1/accounts/"+fx.acc.ID+"/sharing-policy", map[string]any{"who_viewed": false}); rec.Code == http.StatusOK {
		t.Fatal("an editor of a file changed the account's who-viewed switch")
	}
	if rec := fx.keyDo("GET", "/api/v1/accounts/"+fx.acc.ID+"/sharing-policy", nil); decode[sharingPolicyJSON](t, rec).WhoViewed != true {
		t.Fatal("the key reads the switch as off after the owner turned it on")
	}
	// The change is audited under account.sharing_policy, with the switch named in its detail.
	found := false
	for _, a := range decode[listResponse[auditJSON]](t, fx.keyDo("GET", "/api/v1/accounts/"+fx.acc.ID+"/audit", nil)).Items {
		if a.Action == "account.sharing_policy" && a.Details["who_viewed"] == true {
			found = true
		}
	}
	if !found {
		t.Error("the switch was turned on and no account.sharing_policy entry records who_viewed: true")
	}
}

// TestViewersSwitchStartsAPeriod: a view made while the switch was off is not listed once it is on. Setting it on again
// keeps the running period; off and on starts a new one.
func TestViewersSwitchStartsAPeriod(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	f := fx.deep.ID
	viewersRead(t, fx, fx.bobSess, f)
	time.Sleep(5 * time.Millisecond)
	viewersSwitch(t, fx, true)
	if l := viewersList(t, fx.ownerDo, f, ""); len(l.Items) != 0 {
		t.Fatalf("a view from before the switch went on is listed: %v", viewersOf(l))
	}
	time.Sleep(5 * time.Millisecond)
	viewersRead(t, fx, fx.annSess, f)
	if l := viewersList(t, fx.ownerDo, f, ""); !slices.Equal(viewersOf(l), []string{fx.ann.ID}) {
		t.Fatalf("the list is %v, want ann only", viewersOf(l))
	}
	viewersSwitch(t, fx, true)
	if l := viewersList(t, fx.ownerDo, f, ""); !slices.Equal(viewersOf(l), []string{fx.ann.ID}) {
		t.Fatalf("setting the switch on again changed the list to %v", viewersOf(l))
	}
	viewersSwitch(t, fx, false)
	time.Sleep(5 * time.Millisecond)
	viewersSwitch(t, fx, true)
	if l := viewersList(t, fx.ownerDo, f, ""); len(l.Items) != 0 {
		t.Fatalf("off and on again still lists the last period: %v", viewersOf(l))
	}
}

// TestViewersWindowThroughTheRoute: a view older than the 30 days is not listed, though the recorder wrote it, and a view
// inside the window is.
func TestViewersWindowThroughTheRoute(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	f := fx.deep.ID
	viewersSwitch(t, fx, true)
	old := store.ViewRecord{AccountID: fx.acc.ID, UserID: fx.bob.ID, NodeID: f, At: time.Now().Add(-31 * 24 * time.Hour)}
	if err := fx.s.store.WriteViews(context.Background(), []store.ViewRecord{old}); err != nil {
		t.Fatal(err)
	}
	if l := viewersList(t, fx.ownerDo, f, ""); len(l.Items) != 0 {
		t.Fatalf("a view of 31 days ago is listed: %v", viewersOf(l))
	}
	viewersRead(t, fx, fx.annSess, f)
	if l := viewersList(t, fx.ownerDo, f, ""); !slices.Equal(viewersOf(l), []string{fx.ann.ID}) {
		t.Fatalf("the list is %v, want ann only", viewersOf(l))
	}
}
