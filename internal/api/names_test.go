// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http/httptest"
	"testing"
)

// nameOf reads one field of a node reply as a generic map, so the test states the JSON it wants.
func nameOf(t *testing.T, got map[string]any, field, want string) {
	t.Helper()
	v, ok := got[field]
	if want == "" {
		if ok {
			t.Errorf("%s = %v; want no name", field, v)
		}
		return
	}
	if v != want {
		t.Errorf("%s = %v; want %q", field, v, want)
	}
}

// childOf finds the child with the given ID in a children reply.
func childOf(t *testing.T, rec *httptest.ResponseRecorder, id string) map[string]any {
	t.Helper()
	wantStatus(t, rec, 200)
	page := decode[struct {
		Items []map[string]any `json:"items"`
	}](t, rec)
	for _, it := range page.Items {
		if it["id"] == id {
			return it
		}
	}
	t.Fatalf("child %s not listed: %s", id, rec.Body)
	return nil
}

// TestDetailsNamesGoOnlyToTheAccountsOwnPeople: Details shows who created and owns a node by display name. A name goes
// to the account's own people and keys who reach the node, in a stat and in a listing. A link visitor, a caller from
// outside the account, and a deleted user get no name.
func TestDetailsNamesGoOnlyToTheAccountsOwnPeople(t *testing.T) {
	t.Parallel()
	fx := newAnyoneFixture(t)
	const annName = "Ann Example"
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.ann.ID, fx.key,
		map[string]any{"display_name": annName}), 200)
	root := fx.rootOf(t, fx.fld.ID)
	fx.setPerm(t, root, fx.ann.ID, "contributor")
	fx.setPerm(t, root, fx.bob.ID, "viewer")
	file := nodeOf(t, rawSession(t, fx.s, fx.annSess, "POST", "/api/v1/folders/"+fx.fld.ID+"/files?path=/by-ann.txt",
		"hi", nil))
	stat := "/api/v1/nodes/" + file.ID

	keyStat := nodeReply(t, call(t, fx.s, "GET", stat, fx.key, nil))
	nameOf(t, keyStat, "created_by_name", annName)
	nameOf(t, keyStat, "owner_name", annName)

	memberStat := nodeReply(t, callSession(t, fx.s, fx.bobSess, "GET", stat, nil))
	nameOf(t, memberStat, "created_by_name", annName)
	nameOf(t, memberStat, "owner_name", annName)

	listed := childOf(t, callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/nodes/"+root+"/children", nil), file.ID)
	nameOf(t, listed, "created_by_name", annName)
	nameOf(t, listed, "owner_name", annName)

	// A link visitor sees the node, and no name of anyone.
	_, secret := fx.makeAnyone(t, file.ID, nil)
	visitor := fx.visitAnyone(t, secret, "")
	visited := nodeReply(t, fx.visitor(t, visitor, "GET", stat))
	nameOf(t, visited, "created_by_name", "")
	nameOf(t, visited, "owner_name", "")

	// A caller from another account does not reach the node, so it learns nothing.
	_, otherKey := newAccount(t, fx.s, fx.admin, "Other")
	wantStatus(t, call(t, fx.s, "GET", stat, otherKey, nil), 404)

	// A deleted user is no name at all. Its files pass to bob, and the node still names ann as creator.
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.ann.ID+"?transfer_to="+fx.bob.ID,
		fx.key, nil), 204)
	gone := nodeReply(t, call(t, fx.s, "GET", stat, fx.key, nil))
	nameOf(t, gone, "created_by_name", "")
	nameOf(t, gone, "owner_name", "")
}

// nodeReply is a node reply as a generic map, after a 200.
func nodeReply(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	wantStatus(t, rec, 200)
	return decode[map[string]any](t, rec)
}
