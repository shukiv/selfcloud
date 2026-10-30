// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"fmt"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/id"
)

// sameRefusal reports whether two refusals carry the same code and message. The request ID is left out: it differs
// on every request.
func sameRefusal(a, b errorDetail) bool {
	return a.Code == b.Code && a.Message == b.Message
}

// decodeMap decodes a JSON object as a map, so a test can read the shortcut and appearance fields directly.
func decodeMap(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	return decode[map[string]any](t, rec)
}

// makeShortcut makes a shortcut to target inside dirID as the account key, and returns it.
func makeShortcut(t *testing.T, fx *sharingFixture, dirID, target string) map[string]any {
	t.Helper()
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+dirID+"/shortcuts", fx.key, map[string]any{"target": target})
	wantStatus(t, rec, 201)
	return decodeMap(t, rec)
}

// targetOf returns the target object of a shortcut node, or nil when the node carries none.
func targetOf(n map[string]any) map[string]any {
	t, _ := n["target"].(map[string]any)
	return t
}

// TestShortcutIsItsOwnKindAndGrantsNothing: a shortcut is a node of kind shortcut with size 0. Opening it grants
// the caller nothing on its target, and its content and children routes refuse it.
func TestShortcutIsItsOwnKindAndGrantsNothing(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	sc := makeShortcut(t, fx, fx.c.ID, fx.deep.ID)
	if sc["kind"] != "shortcut" || sc["size"] != float64(0) {
		t.Fatalf("shortcut = kind %v size %v, want shortcut and 0", sc["kind"], sc["size"])
	}
	if tg := targetOf(sc); tg == nil || tg["id"] != fx.deep.ID || tg["kind"] != "file" {
		t.Fatalf("target of the shortcut to the caller = %v, want the file", sc["target"])
	}
	// Ann reaches the shortcut's directory but not its target: the shortcut grants nothing.
	fx.setPerm(t, fx.c.ID, fx.ann.ID, "viewer")
	wantStatus(t, fx.stat(t, fx.annSess, sc["id"].(string)), 200)
	wantError(t, fx.stat(t, fx.annSess, fx.deep.ID), 404, apierr.NotFound)
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.deep.ID+"/content", nil), 404, apierr.NotFound)
	// The content and children routes refuse a shortcut, for everyone.
	scID := sc["id"].(string)
	wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+scID+"/content", fx.key, nil), 400, apierr.InvalidRequest)
	kids := decodeError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+scID+"/children", fx.key, nil))
	if kids.Code != apierr.InvalidRequest || !strings.Contains(kids.Message, "shortcut") {
		t.Errorf("children of a shortcut = %s %q, want invalid_request naming the shortcut", kids.Code, kids.Message)
	}
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+scID+"/content", nil), 400, apierr.InvalidRequest)
}

// TestUnreachableTargetRevealsNothing: a viewer with no reach on the target sees the shortcut as unavailable, and
// the target's ID appears in no answer: not the stat, not the listing, not a refused create.
func TestUnreachableTargetRevealsNothing(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	sc := makeShortcut(t, fx, fx.c.ID, fx.deep.ID)
	scID := sc["id"].(string)
	fx.setPerm(t, fx.c.ID, fx.ann.ID, "viewer")

	stat := fx.stat(t, fx.annSess, scID)
	wantStatus(t, stat, 200)
	got := decodeMap(t, stat)
	if got["unavailable"] != true || got["target"] != nil {
		t.Errorf("stat for ann = unavailable %v, target %v; want unavailable with no target", got["unavailable"], got["target"])
	}
	if strings.Contains(stat.Body.String(), fx.deep.ID) {
		t.Error("the stat of an unavailable shortcut names the target's ID")
	}
	list := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.c.ID+"/children", nil)
	wantStatus(t, list, 200)
	if !strings.Contains(list.Body.String(), scID) || strings.Contains(list.Body.String(), fx.deep.ID) {
		t.Error("the listing for ann must show the shortcut and never the target's ID")
	}

	// A contributor who cannot reach the target gets the same answer for a missing target as for an unreachable one.
	fx.setPerm(t, fx.c.ID, fx.ann.ID, "contributor")
	unreachable := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.c.ID+"/shortcuts",
		map[string]any{"target": fx.deep.ID})
	missing := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.c.ID+"/shortcuts",
		map[string]any{"target": id.New("nod")})
	wantError(t, unreachable, 404, apierr.NotFound)
	wantError(t, missing, 404, apierr.NotFound)
	if !sameRefusal(decodeError(t, unreachable), decodeError(t, missing)) {
		t.Errorf("refusals differ: %q for an unreachable target, %q for a missing one",
			unreachable.Body.String(), missing.Body.String())
	}
	if strings.Contains(unreachable.Body.String(), fx.deep.ID) {
		t.Error("the refusal names the target's ID")
	}
}

// TestShortcutCreateNeedsContributorAndAValidTarget: the creator needs contributor on the directory, the target ID
// must be well formed, and a target must be a file or a directory, never a shortcut or a folder's root.
func TestShortcutCreateNeedsContributorAndAValidTarget(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.c.ID, fx.ann.ID, "viewer")
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.c.ID+"/shortcuts",
		map[string]any{"target": fx.deep.ID}), 403, apierr.Forbidden)

	bad := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.c.ID+"/shortcuts", fx.key, map[string]any{"target": "nod_not-an-id"})
	wantError(t, bad, 400, apierr.InvalidRequest)
	first := makeShortcut(t, fx, fx.c.ID, fx.deep.ID)
	wantError(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.c.ID+"/shortcuts", fx.key,
		map[string]any{"target": first["id"]}), 400, apierr.InvalidRequest)
	root := fx.rootOf(t, fx.fld.ID)
	wantError(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.c.ID+"/shortcuts", fx.key,
		map[string]any{"target": root}), 400, apierr.InvalidRequest)
	wantError(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.deep.ID+"/shortcuts", fx.key,
		map[string]any{"target": fx.sib.ID}), 400, apierr.InvalidRequest)
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.c.ID+"/shortcuts", fx.key,
		map[string]any{"target": fx.b.ID, "name": "Bee"}), 201)
}

// TestShortcutIsNoParent: a shortcut takes no children, so nothing is made, uploaded or moved into it.
func TestShortcutIsNoParent(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	sc := makeShortcut(t, fx, fx.c.ID, fx.deep.ID) // named "file.txt" after its target
	scID := sc["id"].(string)
	mk := call(t, fx.s, "POST", "/api/v1/folders/"+fx.fld.ID+"/directories", fx.key,
		map[string]any{"path": "/a/c/file.txt/x", "parents": true})
	if mk.Code < 400 || mk.Code >= 500 {
		t.Errorf("mkdir under a shortcut = %d, want a 4xx refusal", mk.Code)
	}
	up := rawCall(t, fx.s, "POST", "/api/v1/folders/"+fx.fld.ID+"/files?path=/a/c/file.txt/y.txt", fx.key, "y", nil)
	if up.Code < 400 || up.Code >= 500 {
		t.Errorf("upload under a shortcut = %d, want a 4xx refusal", up.Code)
	}
	wantError(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.b.ID+"/move", fx.key, map[string]any{"parent": scID}),
		400, apierr.InvalidRequest)
	wantError(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.b.ID+"/copy", fx.key, map[string]any{"parent": scID}),
		400, apierr.InvalidRequest)
}

// TestShortcutMoveCopyTrashRestorePurge: a shortcut moves, copies, trashes, restores and purges like a file, and keeps
// its target. A copy of a tree copies its shortcuts, never their targets.
func TestShortcutMoveCopyTrashRestorePurge(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	sc := makeShortcut(t, fx, fx.c.ID, fx.deep.ID)
	scID := sc["id"].(string)

	moved := call(t, fx.s, "POST", "/api/v1/nodes/"+scID+"/move", fx.key, map[string]any{"parent": fx.a.ID})
	wantStatus(t, moved, 200)
	if m := decodeMap(t, moved); m["kind"] != "shortcut" || m["parent_id"] != fx.a.ID || targetOf(m)["id"] != fx.deep.ID {
		t.Errorf("moved shortcut = %v, want kind shortcut in /a with its target", m)
	}

	cp := call(t, fx.s, "POST", "/api/v1/nodes/"+scID+"/copy", fx.key, map[string]any{"parent": fx.c.ID, "name": "copy-of"})
	wantStatus(t, cp, 201)
	if c := decodeMap(t, cp); c["kind"] != "shortcut" || c["id"] == scID || targetOf(c)["id"] != fx.deep.ID {
		t.Errorf("copy of a shortcut = %v, want a new shortcut to the same target", c)
	}

	// A copy of the whole of /a carries its shortcut, still pointing at the same target.
	tree := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.a.ID+"/copy", fx.key, map[string]any{"name": "a-copy"})
	wantStatus(t, tree, 201)
	treeID := decodeMap(t, tree)["id"].(string)
	kids := decodeMap(t, call(t, fx.s, "GET", "/api/v1/nodes/"+treeID+"/children", fx.key, nil))
	found := false
	for _, it := range kids["items"].([]any) {
		m := it.(map[string]any)
		if m["name"] == "file.txt" && m["kind"] == "shortcut" && targetOf(m)["id"] == fx.deep.ID {
			found = true
		}
	}
	if !found {
		t.Error("the copied tree lacks its shortcut, or the copy re-pointed it")
	}

	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+scID, fx.key, nil), 204)
	wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+scID, fx.key, nil), 404, apierr.NotFound)
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+scID+"/restore", fx.key, nil), 200)
	if r := decodeMap(t, call(t, fx.s, "GET", "/api/v1/nodes/"+scID, fx.key, nil)); r["kind"] != "shortcut" {
		t.Errorf("restored shortcut = %v", r)
	}
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+scID, fx.key, nil), 204)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/trash/"+scID, fx.key, nil), 204)
	wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+scID, fx.key, nil), 404, apierr.NotFound)
	// Purging the shortcut leaves its target alone.
	wantStatus(t, fx.stat(t, fx.annSess, fx.deep.ID), 404)
	wantStatus(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.deep.ID, fx.key, nil), 200)
}

// TestShortcutSharing: a permission on a shortcut shows the shortcut to the person it names, and grants no reach to
// the target.
func TestShortcutSharing(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	sc := makeShortcut(t, fx, fx.c.ID, fx.deep.ID)
	scID := sc["id"].(string)
	fx.setPerm(t, scID, fx.ann.ID, "viewer")
	got := decodeMap(t, fx.stat(t, fx.annSess, scID))
	if got["unavailable"] != true || got["target"] != nil {
		t.Errorf("ann on the shortcut = %v, want unavailable", got)
	}
	wantError(t, fx.stat(t, fx.annSess, fx.deep.ID), 404, apierr.NotFound)
	// Ann does not see the directory the shortcut sits in.
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.c.ID+"/children", nil), 404, apierr.NotFound)
}

// TestTargetStateChangesWhatTheShortcutShows: a target trashed, restored, moved out of reach, or purged changes what
// the shortcut shows, and the target's ID is never shown to someone who cannot reach it.
func TestTargetStateChangesWhatTheShortcutShows(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	sc := makeShortcut(t, fx, fx.c.ID, fx.deep.ID)
	scID := sc["id"].(string)
	fx.setPerm(t, fx.c.ID, fx.ann.ID, "viewer")
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	ann := func() map[string]any { return decodeMap(t, fx.stat(t, fx.annSess, scID)) }
	if g := ann(); targetOf(g)["id"] != fx.deep.ID {
		t.Fatalf("ann sees %v before any change, want the target", g["target"])
	}

	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.deep.ID, fx.key, nil), 204)
	if g := ann(); g["unavailable"] != true || g["target"] != nil {
		t.Errorf("ann after the target is trashed = %v, want unavailable", g)
	}
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.deep.ID+"/restore", fx.key, nil), 200)
	if g := ann(); targetOf(g)["id"] != fx.deep.ID {
		t.Errorf("ann after the restore = %v, want the target again", g)
	}

	// Moved to /a, out of ann's reach: unavailable for ann, still resolved for the key.
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.deep.ID+"/move", fx.key, map[string]any{"parent": fx.a.ID}), 200)
	if g := ann(); g["unavailable"] != true || strings.Contains(decodeStr(t, fx, scID), fx.deep.ID) {
		t.Errorf("ann after the target moved out of reach = %v", g)
	}
	if g := decodeMap(t, call(t, fx.s, "GET", "/api/v1/nodes/"+scID, fx.key, nil)); targetOf(g)["id"] != fx.deep.ID {
		t.Errorf("key after the target moved = %v, want the target", g)
	}

	// Purged: the shortcut is broken, with no target for anyone.
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.deep.ID+"?permanent=true", fx.key, nil), 204)
	g := decodeMap(t, call(t, fx.s, "GET", "/api/v1/nodes/"+scID, fx.key, nil))
	if g["unavailable"] != true || g["target"] != nil {
		t.Errorf("key after the target was purged = %v, want unavailable with no target", g)
	}
}

// decodeStr returns a stat body as text, for checks that look for an ID in it.
func decodeStr(t *testing.T, fx *sharingFixture, nodeID string) string {
	t.Helper()
	return fx.stat(t, fx.annSess, nodeID).Body.String()
}

// TestShortcutCrossAccountTargetRefused: a target in another account is refused exactly as a missing one is.
func TestShortcutCrossAccountTargetRefused(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	acc2, key2 := newAccount(t, fx.s, fx.admin, "Other")
	fld2 := newFolder(t, fx.s, key2, acc2.ID, "T")
	dir2 := mkdirs(t, fx.s, key2, fld2.ID, "/d")
	other := putNode(t, fx.s, key2, fld2.ID, "/d/x.txt", "x")
	foreign := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.c.ID+"/shortcuts", fx.key, map[string]any{"target": other.ID})
	missing := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.c.ID+"/shortcuts", fx.key, map[string]any{"target": id.New("nod")})
	wantError(t, foreign, 404, apierr.NotFound)
	if !sameRefusal(decodeError(t, foreign), decodeError(t, missing)) || strings.Contains(foreign.Body.String(), other.ID) {
		t.Errorf("a foreign target answers %q; a missing one %q", foreign.Body.String(), missing.Body.String())
	}
	wantError(t, call(t, fx.s, "POST", "/api/v1/nodes/"+dir2.ID+"/shortcuts", key2, map[string]any{"target": fx.deep.ID}),
		404, apierr.NotFound)
}

// TestShortcutLeavesQuotaAlone: shortcuts take no bytes and no files of the account or of any folder.
func TestShortcutLeavesQuotaAlone(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	before := usageOf(t, fx.s, fx.key, fx.acc.ID)
	big := putNode(t, fx.s, fx.key, fx.fld.ID, "/big.bin", strings.Repeat("x", 4096))
	mid := usageOf(t, fx.s, fx.key, fx.acc.ID)
	sc := makeShortcut(t, fx, fx.c.ID, big.ID)
	makeShortcut(t, fx, fx.c.ID, fx.b.ID)
	after := usageOf(t, fx.s, fx.key, fx.acc.ID)
	if !reflect.DeepEqual(after, mid) {
		t.Errorf("usage after two shortcuts = %+v, want the same as before them (%+v)", after, mid)
	}
	if reflect.DeepEqual(before, mid) {
		t.Fatal("the test file did not change usage; the comparison proves nothing")
	}
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+sc["id"].(string)+"/copy", fx.key, map[string]any{"parent": fx.a.ID}), 201)
	if now := usageOf(t, fx.s, fx.key, fx.acc.ID); !reflect.DeepEqual(now, mid) {
		t.Errorf("usage after copying a shortcut = %+v, want %+v", now, mid)
	}

	// A directory holding a shortcut and a file: purging it takes back the file's bytes and the file, and only those.
	// The shortcut counts as nothing in the counters the purge reads.
	dir := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/holder")
	putNode(t, fx.s, fx.key, fx.fld.ID, "/holder/f.bin", strings.Repeat("y", 100))
	makeShortcut(t, fx, dir.ID, fx.sib.ID)
	withDir := usageOf(t, fx.s, fx.key, fx.acc.ID)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+dir.ID+"?permanent=true", fx.key, nil), 204)
	if purged := usageOf(t, fx.s, fx.key, fx.acc.ID); purged.Files != mid.Files || purged.UsedBytes != mid.UsedBytes {
		t.Errorf("usage after purging a directory with a shortcut = %+v, want the usage before it (%+v); with it %+v",
			purged, mid, withDir)
	}
}

// TestColourAndDescriptionAreTheItemsAndTheirFormatIsChecked: editors set a folder's colour from the palette and its
// description; every reader sees them; viewers cannot set them; a colour belongs to a directory only.
func TestColourAndDescriptionAreTheItemsAndTheirFormatIsChecked(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.a.ID, fx.ann.ID, "viewer")
	patch := func(node string, body map[string]any) *httptest.ResponseRecorder {
		return call(t, fx.s, "PATCH", "/api/v1/nodes/"+node, fx.key, body)
	}
	wantStatus(t, patch(fx.a.ID, map[string]any{"color": "teal", "description": "Plans\x00 for\nQ3"}), 200)
	got := decodeMap(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.a.ID, fx.key, nil))
	if got["color"] != "teal" || got["description"] != "Plans for\nQ3" {
		t.Errorf("appearance = color %v, description %q; want teal and the text with the control character gone",
			got["color"], got["description"])
	}
	seen := decodeMap(t, fx.stat(t, fx.annSess, fx.a.ID))
	if seen["color"] != "teal" || seen["description"] != "Plans for\nQ3" {
		t.Errorf("a viewer sees %v, %v; want the same", seen["color"], seen["description"])
	}
	wantError(t, callSession(t, fx.s, fx.annSess, "PATCH", "/api/v1/nodes/"+fx.a.ID, map[string]any{"color": "red"}),
		403, apierr.Forbidden)

	wantError(t, patch(fx.a.ID, map[string]any{"color": "magenta"}), 400, apierr.InvalidRequest)
	wantError(t, patch(fx.deep.ID, map[string]any{"color": "red"}), 400, apierr.InvalidRequest)
	sc := makeShortcut(t, fx, fx.c.ID, fx.deep.ID)
	wantError(t, patch(sc["id"].(string), map[string]any{"color": "red"}), 400, apierr.InvalidRequest)
	wantError(t, patch(fx.a.ID, map[string]any{"description": strings.Repeat("é", 1001)}), 400, apierr.InvalidRequest)
	wantStatus(t, patch(fx.a.ID, map[string]any{"description": strings.Repeat("é", 1000)}), 200)

	// Null clears the colour; the description is cleared by an empty text.
	cleared := decodeMap(t, patch(fx.a.ID, map[string]any{"color": nil, "description": ""}))
	if _, has := cleared["color"]; has || cleared["description"] != nil {
		t.Errorf("after clearing = %v, want no colour and no description", cleared)
	}

	// A copy keeps the colour.
	wantStatus(t, patch(fx.a.ID, map[string]any{"color": "blue"}), 200)
	cp := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.a.ID+"/copy", fx.key, map[string]any{"name": "a-copy"})
	wantStatus(t, cp, 201)
	if c := decodeMap(t, cp); c["color"] != "blue" {
		t.Errorf("copy colour = %v, want blue", c["color"])
	}
}

// TestShortcutGuardsAgainstKindsThatAreNotDirectories: helper for the kind checks used by other tests.
func TestShortcutTargetFormatIsChecked(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	for _, bad := range []any{"", 7, "nod_x y", "fld_" + strings.Repeat("a", 40)} {
		rec := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.c.ID+"/shortcuts", fx.key, map[string]any{"target": bad})
		if rec.Code != 400 {
			t.Errorf("target %v = %d, want a refusal", bad, rec.Code)
		}
	}
	wantError(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.c.ID+"/shortcuts", fx.key, map[string]any{}),
		400, apierr.InvalidRequest)
}

// TestVisitorLearnsNothingAboutAShortcut: a link visitor sees a shortcut's name at most. The listing leaves shortcuts
// out, a stat carries no target and no unavailable mark, and a visitor cannot make one. Colour and description are
// seen by every reader, the visitor included.
func TestVisitorLearnsNothingAboutAShortcut(t *testing.T) {
	t.Parallel()
	fx := newAnyoneFixture(t)
	sc := makeShortcut(t, fx.sharingFixture, fx.c.ID, fx.deep.ID)
	scID := sc["id"].(string)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+fx.c.ID, fx.key,
		map[string]any{"color": "green", "description": "Shared notes"}), 200)
	_, secret := fx.makeAnyone(t, fx.c.ID, nil)
	tok := fx.visitAnyone(t, secret, "")

	list := call(t, fx.s, "GET", "/api/v1/nodes/"+fx.c.ID+"/children", tok, nil)
	wantStatus(t, list, 200)
	if strings.Contains(list.Body.String(), scID) || strings.Contains(list.Body.String(), fx.deep.ID) {
		t.Error("a visitor's listing names the shortcut or its target")
	}

	stat := call(t, fx.s, "GET", "/api/v1/nodes/"+scID, tok, nil)
	if stat.Code == 200 {
		got := decodeMap(t, stat)
		if _, has := got["target"]; has {
			t.Error("a visitor's stat carries a target")
		}
		if _, has := got["unavailable"]; has {
			t.Error("a visitor's stat carries an unavailable mark")
		}
	}
	if strings.Contains(stat.Body.String(), fx.deep.ID) {
		t.Error("a visitor's stat names the target")
	}

	// Colour and description are the owner's notes: a link visitor gets neither, on a stat or in a listing.
	dir := decodeMap(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.c.ID, tok, nil))
	if _, has := dir["color"]; has {
		t.Errorf("a visitor's stat carries a colour: %v", dir["color"])
	}
	if _, has := dir["description"]; has {
		t.Errorf("a visitor's stat carries a description: %v", dir["description"])
	}
	sub := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/a/c/sub")
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+sub.ID, fx.key,
		map[string]any{"color": "red", "description": "private notes"}), 200)
	for _, it := range decodeMap(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.c.ID+"/children", tok, nil))["items"].([]any) {
		m := it.(map[string]any)
		if _, has := m["color"]; has {
			t.Errorf("a visitor's listing carries a colour on %v", m["name"])
		}
		if _, has := m["description"]; has {
			t.Errorf("a visitor's listing carries a description on %v", m["name"])
		}
	}

	denied := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.c.ID+"/shortcuts", tok, map[string]any{"target": fx.sib.ID})
	if denied.Code == 201 || strings.Contains(denied.Body.String(), fx.deep.ID) {
		t.Errorf("a visitor made a shortcut or saw a target: %d %s", denied.Code, denied.Body.String())
	}

	// A shortcut has no link of its own: it is not shared by link, its target is.
	wantError(t, call(t, fx.s, "POST", "/api/v1/nodes/"+scID+"/share-links", fx.key,
		map[string]any{"audience": "anyone"}), 400, apierr.InvalidRequest)
}

// TestOwnershipTransferOfAShortcut: the owner's transfer answer resolves the target for a caller who reaches it, and
// the new owner, who does not reach the target, sees the shortcut unavailable with no target ID.
func TestOwnershipTransferOfAShortcut(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	sc := makeShortcut(t, fx, fx.c.ID, fx.deep.ID)
	scID := sc["id"].(string)
	fx.setPerm(t, fx.c.ID, fx.ann.ID, "viewer")

	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+scID+"/transfer-ownership", fx.key, map[string]any{"to": fx.ann.ID})
	wantStatus(t, rec, 200)
	got := decodeMap(t, rec)
	if got["owner_id"] != fx.ann.ID || got["kind"] != "shortcut" {
		t.Fatalf("transfer answer = owner %v kind %v, want ann and shortcut", got["owner_id"], got["kind"])
	}
	if targetOf(got)["id"] != fx.deep.ID {
		t.Errorf("transfer answer for the key = %v, want the target resolved", got["target"])
	}
	seen := fx.stat(t, fx.annSess, scID)
	wantStatus(t, seen, 200)
	if g := decodeMap(t, seen); g["unavailable"] != true || g["target"] != nil || strings.Contains(seen.Body.String(), fx.deep.ID) {
		t.Errorf("new owner's view = %v, want unavailable with no target ID", g)
	}
}

// TestTokenAndShortcuts: a delegated token reaches shortcuts within its scope only. A token cannot make a shortcut
// outside its folders, cannot make one to a target outside them, and reads a shortcut whose target is outside them
// as unavailable, with no target ID. A read-only token cannot make one.
func TestTokenAndShortcuts(t *testing.T) {
	t.Parallel()
	m := newMemberFixture(t) // ann: write on f (fileF), read on g (fileG)
	s, key, accID := m.s, m.key, m.acc.ID
	dirF := mkdirs(t, s, key, m.f.ID, "/dir")
	dirG := mkdirs(t, s, key, m.g.ID, "/dirG")
	tokF := mintFor(t, s, key, accID, map[string]any{"folders": []string{m.f.ID}, "user": m.ann.ID, "permissions": "write"})
	tokR := mintFor(t, s, key, accID, map[string]any{"folders": []string{m.f.ID}, "user": m.ann.ID, "permissions": "read"})
	jsonBody := map[string]string{"Content-Type": "application/json"}

	// A target outside the token's folders is refused as a missing one, and nothing about it is shown.
	outside := rawCall(t, s, "POST", "/api/v1/nodes/"+dirF.ID+"/shortcuts", tokF, `{"target":"`+m.fileG.ID+`"}`, jsonBody)
	missing := rawCall(t, s, "POST", "/api/v1/nodes/"+dirF.ID+"/shortcuts", tokF, `{"target":"`+id.New("nod")+`"}`, jsonBody)
	wantError(t, outside, 404, apierr.NotFound)
	if !sameRefusal(decodeError(t, outside), decodeError(t, missing)) || strings.Contains(outside.Body.String(), m.fileG.ID) {
		t.Errorf("a target outside the token's folders answers %q; a missing one %q", outside.Body.String(), missing.Body.String())
	}

	// A directory outside the token's folders is not reached at all.
	wantError(t, rawCall(t, s, "POST", "/api/v1/nodes/"+dirG.ID+"/shortcuts", tokF, `{"target":"`+m.fileF.ID+`"}`, jsonBody),
		404, apierr.NotFound)

	// A target inside the token's folders is made, and the token sees it.
	made := rawCall(t, s, "POST", "/api/v1/nodes/"+dirF.ID+"/shortcuts", tokF, `{"target":"`+m.fileF.ID+`"}`, jsonBody)
	wantStatus(t, made, 201)
	madeNode := decodeMap(t, made)
	if targetOf(madeNode)["id"] != m.fileF.ID {
		t.Errorf("in-scope shortcut for the token = %v, want its target", madeNode["target"])
	}

	// A read-only token cannot make one.
	wantError(t, rawCall(t, s, "POST", "/api/v1/nodes/"+dirF.ID+"/shortcuts", tokR, `{"target":"`+m.fileF.ID+`"}`, jsonBody),
		403, apierr.Forbidden)

	// A shortcut in scope whose target is outside it: the token sees unavailable, with no target ID. Ann's own session,
	// which reaches the target through its read grant, sees the target.
	scID := makeShortcut(t, &sharingFixture{s: s, acc: m.acc, key: key}, dirF.ID, m.fileG.ID)["id"].(string)
	tokStat := call(t, s, "GET", "/api/v1/nodes/"+scID, tokF, nil)
	wantStatus(t, tokStat, 200)
	if g := decodeMap(t, tokStat); g["unavailable"] != true || g["target"] != nil || strings.Contains(tokStat.Body.String(), m.fileG.ID) {
		t.Errorf("token's view of a shortcut to an out-of-scope target = %v, want unavailable with no target ID", g)
	}
	kids := call(t, s, "GET", "/api/v1/nodes/"+dirF.ID+"/children", tokF, nil)
	wantStatus(t, kids, 200)
	if strings.Contains(kids.Body.String(), m.fileG.ID) {
		t.Error("the token's listing names the out-of-scope target")
	}
	sessSeen := decodeMap(t, callSession(t, s, m.sess, "GET", "/api/v1/nodes/"+scID, nil))
	if targetOf(sessSeen)["id"] != m.fileG.ID {
		t.Errorf("ann's session view = %v, want the target it reaches through its read grant", sessSeen["target"])
	}

	// The read-only token reads the shortcut it can reach, with its target.
	readSeen := decodeMap(t, call(t, s, "GET", "/api/v1/nodes/"+madeNode["id"].(string), tokR, nil))
	if targetOf(readSeen)["id"] != m.fileF.ID {
		t.Errorf("read token's view = %v, want the in-scope target", readSeen["target"])
	}
}

// TestTrashedShortcutResolvesItsTarget: a trashed shortcut in the trash listing and its restore answer show the target
// to a caller who reaches it, and never its ID to one who does not.
func TestTrashedShortcutResolvesItsTarget(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	sc := makeShortcut(t, fx, fx.c.ID, fx.deep.ID)
	scID := sc["id"].(string)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+scID, fx.key, nil), 204)

	var found map[string]any
	for _, it := range decodeMap(t, call(t, fx.s, "GET", "/api/v1/trash", fx.key, nil))["items"].([]any) {
		if m := it.(map[string]any); m["id"] == scID {
			found = m
		}
	}
	if found == nil {
		t.Fatal("the key's trash listing lacks the shortcut")
	}
	if targetOf(found)["id"] != fx.deep.ID {
		t.Errorf("trash listing for the key = %v, want the target resolved", found["target"])
	}
	// Ann does not reach the target, so the trash listing she sees, if any, names nothing of it.
	annTrash := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/trash", nil)
	if strings.Contains(annTrash.Body.String(), fx.deep.ID) {
		t.Error("ann's trash listing names the target's ID")
	}

	restored := call(t, fx.s, "POST", "/api/v1/nodes/"+scID+"/restore", fx.key, nil)
	wantStatus(t, restored, 200)
	if targetOf(decodeMap(t, restored))["id"] != fx.deep.ID {
		t.Errorf("restore answer for the key = %v, want the target resolved", restored.Body.String())
	}
}

// TestShortcutAuditHidesTarget: the audit entry of a shortcut's creation names the shortcut and its directory, never
// its target: the target's ID is shown to no one who does not reach it, and the log is read by owners and keys.
func TestShortcutAuditHidesTarget(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	sc := makeShortcut(t, fx, fx.c.ID, fx.deep.ID)
	rec := call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/audit", fx.key, nil)
	wantStatus(t, rec, 200)
	found := false
	for _, it := range decodeMap(t, rec)["items"].([]any) {
		m := it.(map[string]any)
		if m["action"] != "node.shortcut" || m["target"] != sc["id"] {
			continue
		}
		found = true
		if strings.Contains(fmt.Sprint(m["details"]), fx.deep.ID) {
			t.Errorf("the node.shortcut audit entry names the target: %v", m["details"])
		}
	}
	if !found {
		t.Fatal("no node.shortcut audit entry for the shortcut")
	}
}

// TestBidiControlsAreStrippedButMarksKept: a description loses the bidirectional embedding and override controls
// and the isolates, which can reorder the text a reader sees. The joiners and direction marks that Hebrew, Arabic and
// Persian need are kept.
func TestBidiControlsAreStrippedButMarksKept(t *testing.T) {
	t.Parallel()
	for r := rune(0x202A); r <= 0x202E; r++ {
		if got, err := cleanDescription("a" + string(r) + "b"); err != nil || got != "ab" {
			t.Errorf("U+%04X kept: %q, %v", r, got, err)
		}
	}
	for r := rune(0x2066); r <= 0x2069; r++ {
		if got, err := cleanDescription("a" + string(r) + "b"); err != nil || got != "ab" {
			t.Errorf("U+%04X kept: %q, %v", r, got, err)
		}
	}
	const kept = "‍‎‏‌" // ZWJ, LRM, RLM, ZWNJ
	if got, err := cleanDescription("שלום" + kept + "مرحبا"); err != nil || got != "שלום"+kept+"مرحبا" {
		t.Errorf("joiners and direction marks were removed: %q, %v", got, err)
	}
}

// TestCopyOfAShortcutIsNotAnEdit: copying a shortcut makes a shortcut, and the copy is not recorded in the copier's
// Recent list: a shortcut has no contents, so there is no edit to record. The documentation says so.
func TestCopyOfAShortcutIsNotAnEdit(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	sc := makeShortcut(t, fx, fx.c.ID, fx.deep.ID)
	fx.setPerm(t, fx.c.ID, fx.ann.ID, "contributor")
	rec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+sc["id"].(string)+"/copy", map[string]any{"name": "copy-of"})
	wantStatus(t, rec, 201)
	copyID := decodeMap(t, rec)["id"].(string)
	if contains(recentIDs(t, fx, fx.annSess), copyID) {
		t.Error("the copy of a shortcut is in the copier's Recent list")
	}
}

// TestColourEmptyStringClears: "" clears a colour as null does, so a client that sends an empty string gets what it
// asked for. The API documentation says so.
func TestColourEmptyStringClears(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+fx.a.ID, fx.key, map[string]any{"color": "teal"}), 200)
	rec := call(t, fx.s, "PATCH", "/api/v1/nodes/"+fx.a.ID, fx.key, map[string]any{"color": ""})
	wantStatus(t, rec, 200)
	if _, has := decodeMap(t, rec)["color"]; has {
		t.Errorf("colour after an empty string = %v, want none", decodeMap(t, rec)["color"])
	}
}

// TestNodeScopedTokenAndShortcuts: a token confined to one directory makes a shortcut there to a target inside it, and
// refuses a target or a directory outside it. It reads a shortcut whose target lies outside as unavailable, with no
// target ID, and lists it the same way.
func TestNodeScopedTokenAndShortcuts(t *testing.T) {
	t.Parallel()
	m := newMemberFixture(t) // ann: write on f (fileF), read on g (fileG)
	s, key, accID := m.s, m.key, m.acc.ID
	dirF := mkdirs(t, s, key, m.f.ID, "/dir")
	inside := putNode(t, s, key, m.f.ID, "/dir/inside.txt", "in")
	outDir := mkdirs(t, s, key, m.f.ID, "/out")
	tok := mintFor(t, s, key, accID, map[string]any{"folders": []string{m.f.ID}, "user": m.ann.ID,
		"permissions": "write", "node": dirF.ID})
	jsonBody := map[string]string{"Content-Type": "application/json"}

	// The shortcut is named, since its default name would clash with the target beside it.
	made := rawCall(t, s, "POST", "/api/v1/nodes/"+dirF.ID+"/shortcuts", tok,
		`{"target":"`+inside.ID+`","name":"link.txt"}`, jsonBody)
	wantStatus(t, made, 201)
	if targetOf(decodeMap(t, made))["id"] != inside.ID {
		t.Errorf("in-scope shortcut for the node-scoped token = %v, want its target", made.Body.String())
	}

	// A target outside the directory the token is confined to is refused as a missing one.
	outside := rawCall(t, s, "POST", "/api/v1/nodes/"+dirF.ID+"/shortcuts", tok, `{"target":"`+m.fileF.ID+`"}`, jsonBody)
	wantError(t, outside, 404, apierr.NotFound)
	if strings.Contains(outside.Body.String(), m.fileF.ID) {
		t.Error("a refusal names a target outside the token's directory")
	}
	// A directory outside it is not reached, so no shortcut is made there.
	wantError(t, rawCall(t, s, "POST", "/api/v1/nodes/"+outDir.ID+"/shortcuts", tok, `{"target":"`+inside.ID+`"}`, jsonBody),
		404, apierr.NotFound)

	// A shortcut made by the key, in the token's directory, to a target outside it: the token sees it unavailable.
	scID := makeShortcut(t, &sharingFixture{s: s, acc: m.acc, key: key}, dirF.ID, m.fileF.ID)["id"].(string)
	stat := call(t, s, "GET", "/api/v1/nodes/"+scID, tok, nil)
	wantStatus(t, stat, 200)
	if g := decodeMap(t, stat); g["unavailable"] != true || g["target"] != nil || strings.Contains(stat.Body.String(), m.fileF.ID) {
		t.Errorf("node-scoped token's view = %v, want unavailable with no target ID", g)
	}
	kids := call(t, s, "GET", "/api/v1/nodes/"+dirF.ID+"/children", tok, nil)
	wantStatus(t, kids, 200)
	if strings.Contains(kids.Body.String(), m.fileF.ID) {
		t.Error("the node-scoped token's listing names the out-of-scope target")
	}
}

// TestTokenMovesShortcutAcrossFolders: a shortcut moves to another folder under a token that reaches both, and the
// move is refused under a token that reaches only the folder it leaves. The target stays the same, and it is shown
// only where the caller reaches it.
func TestTokenMovesShortcutAcrossFolders(t *testing.T) {
	t.Parallel()
	m := newMemberFixture(t) // ann: write on f (fileF), read on g
	s, key, accID := m.s, m.key, m.acc.ID
	h := newFolder(t, s, key, accID, "H")
	m.grant(t, h.ID, "write")
	dirF := mkdirs(t, s, key, m.f.ID, "/dir")
	dirH := mkdirs(t, s, key, h.ID, "/dirH")
	scID := makeShortcut(t, &sharingFixture{s: s, acc: m.acc, key: key}, dirF.ID, m.fileF.ID)["id"].(string)
	tokF := mintFor(t, s, key, accID, map[string]any{"folders": []string{m.f.ID}, "user": m.ann.ID, "permissions": "write"})
	tokFH := mintFor(t, s, key, accID, map[string]any{"folders": []string{m.f.ID, h.ID}, "user": m.ann.ID, "permissions": "write"})
	move := func(tok string) *httptest.ResponseRecorder {
		return call(t, s, "POST", "/api/v1/nodes/"+scID+"/move", tok, map[string]any{"folder": h.ID, "parent": dirH.ID})
	}

	wantError(t, move(tokF), 404, apierr.NotFound)
	moved := move(tokFH)
	wantStatus(t, moved, 200)
	got := decodeMap(t, moved)
	if got["folder_id"] != h.ID || got["parent_id"] != dirH.ID || targetOf(got)["id"] != m.fileF.ID {
		t.Errorf("moved shortcut = folder %v parent %v target %v; want the new folder, its new parent and the target",
			got["folder_id"], got["parent_id"], got["target"])
	}
	// The token that reached only the old folder no longer reaches the shortcut at all.
	wantError(t, call(t, s, "GET", "/api/v1/nodes/"+scID, tokF, nil), 404, apierr.NotFound)
	// The token reaching both sees the target, since the target stays in folder f.
	if g := decodeMap(t, call(t, s, "GET", "/api/v1/nodes/"+scID, tokFH, nil)); targetOf(g)["id"] != m.fileF.ID {
		t.Errorf("token reaching both, after the move = %v, want the target", g["target"])
	}
}
