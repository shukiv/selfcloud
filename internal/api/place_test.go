// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http/httptest"
	"testing"
	"time"
)

// placesOf reads a list answer and returns the "place" of each item by its name, read by its JSON name so the test does
// not depend on the Go field. An item with no place has "".
func placesOf(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	wantStatus(t, rec, 200)
	body := decode[struct {
		Items []map[string]any `json:"items"`
	}](t, rec)
	out := map[string]string{}
	for _, it := range body.Items {
		name, _ := it["name"].(string)
		place, _ := it["place"].(string)
		out[name] = place
	}
	return out
}

// wantPlaces fails unless every named item has the wanted place.
func wantPlaces(t *testing.T, got map[string]string, want map[string]string) {
	t.Helper()
	for name, place := range want {
		g, ok := got[name]
		if !ok {
			t.Errorf("%s is not in the answer (got %v)", name, got)
			continue
		}
		if g != place {
			t.Errorf("place of %s = %q, want %q", name, g, place)
		}
	}
}

// TestStarredPlaceNamesTheReachableParent: a viewer on b reaches deep, so deep's place is b. It does not reach a, the
// parent of b, so b's place is "".
func TestStarredPlaceNamesTheReachableParent(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/star", nil), 204)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+fx.b.ID+"/star", nil), 204)

	got := placesOf(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/starred", nil))
	wantPlaces(t, got, map[string]string{"file.txt": "b", "b": ""})
}

// TestRecentPlaceNamesTheReachableParent: the same rule on the recent list. A session's GET of the content is a view
// (a HEAD is not, as TestRecentViewsAndEdits shows).
func TestRecentPlaceNamesTheReachableParent(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.deep.ID+"/content", nil), 200)
	flushViews(t, fx.s)

	got := placesOf(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/recent", nil))
	wantPlaces(t, got, map[string]string{"file.txt": "b"})
}

// TestRecentPlaceIsEmptyForAnUnreachableParent: ann reaches the file on its own grant and not the folder above it.
func TestRecentPlaceIsEmptyForAnUnreachableParent(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.deep.ID, fx.ann.ID, "viewer")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.deep.ID+"/content", nil), 200)
	flushViews(t, fx.s)

	got := placesOf(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/recent", nil))
	wantPlaces(t, got, map[string]string{"file.txt": ""})
}

// TestSharedPlaceNamesTheReachableParent: ann holds grants on b and deep. deep's parent b is reached; b's parent a is not.
func TestSharedPlaceNamesTheReachableParent(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	fx.setPerm(t, fx.deep.ID, fx.ann.ID, "viewer")

	got := placesOf(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/shared", nil))
	wantPlaces(t, got, map[string]string{"file.txt": "b", "b": ""})
}

// TestSharedPlaceIsEmptyBelowAnUnreachableFolder: ann holds a grant on the file only. The file's parent b is not reached,
// so the answer names no parent, and no name of b or of anything above it leaks.
func TestSharedPlaceIsEmptyBelowAnUnreachableFolder(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.deep.ID, fx.ann.ID, "viewer")

	got := placesOf(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/shared", nil))
	wantPlaces(t, got, map[string]string{"file.txt": ""})
}

// TestTrashPlaceIsTheOriginalParent: the trashed file's place is the folder it was in.
func TestTrashPlaceIsTheOriginalParent(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.deep.ID, fx.key, nil), 204)

	got := placesOf(t, call(t, fx.s, "GET", "/api/v1/trash", fx.key, nil))
	wantPlaces(t, got, map[string]string{"file.txt": "b"})
}

// TestTrashPlaceIsEmptyWhenTheParentIsInTrash: the file was trashed, then its parent. The parent is in the trash, so the
// caller does not reach it through the normal walk and the file's place is "". The parent's own place is its parent a.
func TestTrashPlaceIsEmptyWhenTheParentIsInTrash(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.deep.ID, fx.key, nil), 204)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.b.ID, fx.key, nil), 204)

	got := placesOf(t, call(t, fx.s, "GET", "/api/v1/trash", fx.key, nil))
	wantPlaces(t, got, map[string]string{"file.txt": "", "b": "a"})
}

// TestTrashPlaceIsEmptyForAnUnreachableParent: ann may trash the file on its own grant, and reaches no parent of it. Her
// trash listing names no parent.
func TestTrashPlaceIsEmptyForAnUnreachableParent(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.deep.ID, fx.ann.ID, "editor")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/nodes/"+fx.deep.ID, nil), 204)

	got := placesOf(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/trash", nil))
	wantPlaces(t, got, map[string]string{"file.txt": ""})
}

// TestTrashPlaceIsEmptyAtTheTop: a file in the folder's root has the root as its parent, and the root is not a place.
func TestTrashPlaceIsEmptyAtTheTop(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	top := putNode(t, fx.s, fx.key, fx.fld.ID, "/top.txt", "t")
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+top.ID, fx.key, nil), 204)

	got := placesOf(t, call(t, fx.s, "GET", "/api/v1/trash", fx.key, nil))
	wantPlaces(t, got, map[string]string{"top.txt": ""})
}

// searchPlaces runs a search as the caller's credential and returns the place of each hit.
func searchPlacesAs(t *testing.T, fx *searchFixture, cred string, query string) map[string]string {
	t.Helper()
	return placesOf(t, call(t, fx.s, "GET", "/api/v1/search?"+query, cred, nil))
}

// TestSearchPlaceIsTheParentNameAndEmptyAtTheTop: a hit in a folder names the folder; a hit in the root names nothing.
func TestSearchPlaceIsTheParentNameAndEmptyAtTheTop(t *testing.T) {
	t.Parallel()
	fx := newSearchFixture(t)
	mkdirs(t, fx.s, fx.key, fx.fld.ID, "/docs")
	putNode(t, fx.s, fx.key, fx.fld.ID, "/docs/report.txt", "r")
	putNode(t, fx.s, fx.key, fx.fld.ID, "/toplevel-report.txt", "t")

	got := searchPlacesAs(t, fx, fx.key, "q=report")
	wantPlaces(t, got, map[string]string{"report.txt": "docs", "toplevel-report.txt": ""})
}

// TestSearchPlaceFollowsTheTokenNode: a token whose node is the folder sub reaches sub and what is below it, not docs
// above it. The folder sub is a hit with its parent docs outside the token's node, so its place is "". A file in sub
// names sub.
func TestSearchPlaceFollowsTheTokenNode(t *testing.T) {
	t.Parallel()
	fx := newSearchFixture(t)
	mkdirs(t, fx.s, fx.key, fx.fld.ID, "/docs")
	sub := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/docs/sub")
	putNode(t, fx.s, fx.key, fx.fld.ID, "/docs/sub/report.txt", "r")
	tok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}, "node": sub.ID,
		"user": fx.owner.ID})
	got := searchPlacesAs(t, fx, tok, "q=sub")
	wantPlaces(t, got, map[string]string{"sub": ""})
	got = searchPlacesAs(t, fx, tok, "q=report")
	wantPlaces(t, got, map[string]string{"report.txt": "sub"})
}

// TestSearchPlaceIsEmptyForAnUnreachableParent: a user with a grant on the file only finds it, and is told no parent.
func TestSearchPlaceIsEmptyForAnUnreachableParent(t *testing.T) {
	t.Parallel()
	fx := newSearchFixture(t)
	mkdirs(t, fx.s, fx.key, fx.fld.ID, "/docs")
	rep := putNode(t, fx.s, fx.key, fx.fld.ID, "/docs/report.txt", "r")
	ann, annSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "ann@example.com")
	wantStatus(t, call(t, fx.s, "PUT", "/api/v1/nodes/"+rep.ID+"/permissions/"+ann.ID, fx.key,
		map[string]any{"role": "viewer"}), 200)

	got := placesOf(t, callSession(t, fx.s, annSess, "GET", "/api/v1/search?q=report", nil))
	wantPlaces(t, got, map[string]string{"report.txt": ""})
}

// TestSharedAtIsTheGrantTimeOnEveryOrder: shared_at is set on the default order too, and carries the same time as the
// recent order gives the same item.
func TestSharedAtIsTheGrantTimeOnEveryOrder(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	before := time.Now()
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	after := time.Now()

	def := sharedAtOf(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/shared", nil))
	rec := sharedAtOf(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/shared?order=recent", nil))
	wantSharedAtWithin(t, "b", def["b"], before, after)
	if def["b"] != rec["b"] {
		t.Errorf("shared_at on b = %q in the default order, %q in the recent order", def["b"], rec["b"])
	}
}

// TestSharedAtOfAGroupGrant: a grant to a group the caller is in sets shared_at too, at the time of that grant.
func TestSharedAtOfAGroupGrant(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	grp := fx.group(t, "Team A")
	fx.inGroup(t, grp, fx.ann.ID, true)
	before := time.Now()
	wantStatus(t, call(t, fx.s, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/permissions/"+grp, fx.key,
		map[string]any{"role": "viewer"}), 200)
	after := time.Now()

	got := sharedAtOf(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/shared", nil))
	wantSharedAtWithin(t, "file.txt", got["file.txt"], before, after)
}

// TestSharedAtRisesWithALaterVisit: a grant, then a visit to an organisation link on the same node. The default order's
// shared_at is the visit's time, the later of the two, as the recent order's is.
func TestSharedAtRisesWithALaterVisit(t *testing.T) {
	t.Parallel()
	fx := newLinkFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	time.Sleep(3 * time.Millisecond)
	from := time.Now()
	_, url := fx.makeLink(t, fx.b.ID, "viewer")
	fx.openedAs(t, fx.annSess, secretOf(t, url))
	to := time.Now()

	def := sharedAtOf(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/shared", nil))
	rec := sharedAtOf(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/shared?order=recent", nil))
	wantSharedAtWithin(t, "b", def["b"], from, to)
	if def["b"] != rec["b"] {
		t.Errorf("shared_at on b = %q in the default order, %q in the recent order", def["b"], rec["b"])
	}
}

// wantSharedAtWithin fails unless text is a time from from to to, both as the test saw them: the time of the call that
// made the change, not the epoch, a zero value or an earlier change.
func wantSharedAtWithin(t *testing.T, name, text string, from, to time.Time) {
	t.Helper()
	if text == "" {
		t.Fatalf("%s carries no shared_at", name)
	}
	at, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		t.Fatalf("shared_at of %s = %q: %v", name, text, err)
	}
	// Store times are whole milliseconds, so the lower bound is cut to one.
	if at.Before(from.Truncate(time.Millisecond)) || at.After(to) {
		t.Errorf("shared_at of %s = %v, want a time from %v to %v", name, at, from, to)
	}
}

// sharedAtOf returns the shared_at of each item of a shared answer, by name ("" when absent).
func sharedAtOf(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	wantStatus(t, rec, 200)
	body := decode[struct {
		Items []map[string]any `json:"items"`
	}](t, rec)
	out := map[string]string{}
	for _, it := range body.Items {
		name, _ := it["name"].(string)
		at, _ := it["shared_at"].(string)
		out[name] = at
	}
	return out
}
