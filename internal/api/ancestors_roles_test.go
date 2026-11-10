// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
)

// ancestorItems decodes a 200 answer into its raw items, topmost first.
func ancestorItems(t *testing.T, rec *httptest.ResponseRecorder) []json.RawMessage {
	t.Helper()
	wantStatus(t, rec, 200)
	var body ancestorsBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode ancestors: %v in %s", err, rec.Body.String())
	}
	return body.Ancestors
}

// roleOf is the role of a raw node item.
func roleOf(t *testing.T, item json.RawMessage) string {
	t.Helper()
	var n nodeJSON
	if err := json.Unmarshal(item, &n); err != nil {
		t.Fatal(err)
	}
	return n.Role
}

// TestAncestorsItemsCarryTheTokensRoleCap: a read token caps the role of the items at viewer, though the token's user
// is an editor on the directory. The item is still the stat answer the same token gets for that node.
func TestAncestorsItemsCarryTheTokensRoleCap(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "editor")
	tok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"all": true, "user": fx.ann.ID})
	items := ancestorItems(t, getWith(t, fx.s, tok, "/api/v1/nodes/"+fx.deep.ID+"/ancestors"))
	if len(items) != 1 {
		t.Fatalf("got %d ancestors, want [b]", len(items))
	}
	if role := roleOf(t, items[0]); role != "viewer" {
		t.Errorf("the item's role under a read token = %q, want viewer", role)
	}
	stat := getWith(t, fx.s, tok, "/api/v1/nodes/"+fx.b.ID)
	wantStatus(t, stat, 200)
	if !sameValue(t, items[0], stat.Body.Bytes()) {
		t.Errorf("the item is not the token's stat answer\nitem: %s\nstat: %s", items[0], stat.Body.String())
	}
}

// TestAncestorsForAnOrganisationLinkVisitor: a signed-in member with no grant reaches a directory only through an
// organisation link on it. The member's chain is that directory, with the link's role, and nothing above it.
func TestAncestorsForAnOrganisationLinkVisitor(t *testing.T) {
	t.Parallel()
	fx := newLinkFixture(t)
	wantError(t, ancestorsCall(t, fx.s, fx.annSess, fx.deep.ID), 404, apierr.NotFound)

	// A member reaches a link's node once the member has opened the link, as the share page does.
	_, url := fx.makeLink(t, fx.b.ID, "viewer")
	fx.openedAs(t, fx.annSess, secretOf(t, url))
	rec := ancestorsCall(t, fx.s, fx.annSess, fx.deep.ID)
	if got := ancestorIDs(t, rec); !reflect.DeepEqual(got, []string{fx.b.ID}) {
		t.Fatalf("the visitor's ancestors = %v, want [b] (the link's directory only)", got)
	}
	items := ancestorItems(t, rec)
	if role := roleOf(t, items[0]); role != "viewer" {
		t.Errorf("the item's role for the link visitor = %q, want the link's viewer", role)
	}
	statOfB := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.b.ID, nil)
	wantStatus(t, statOfB, 200)
	if !sameValue(t, items[0], statOfB.Body.Bytes()) {
		t.Errorf("the link visitor's item is not the stat answer\nitem: %s\nstat: %s", items[0], statOfB.Body.String())
	}
	if got := ancestorIDs(t, ancestorsCall(t, fx.s, fx.annSess, fx.b.ID)); len(got) != 0 {
		t.Errorf("the visitor's ancestors of the linked directory = %v, want none", got)
	}
}

// TestAncestorsOfAnImpersonatedPerson: an installation admin signed in as a member gets the member's chain, and no
// admin's answer of its own: the admin's own read of a node is forbidden, as stat refuses it.
func TestAncestorsOfAnImpersonatedPerson(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	f := newFolder(t, fx.s, fx.accKey, fx.acc.ID, "Team")
	bDir := mkdirs(t, fx.s, fx.accKey, f.ID, "/a/b")
	file := putNode(t, fx.s, fx.accKey, f.ID, "/a/b/c.txt", "x")
	bID := bDir.ID
	wantStatus(t, call(t, fx.s, "PUT", "/api/v1/nodes/"+bID+"/permissions/"+fx.member.ID, fx.accKey,
		map[string]any{"role": "viewer"}), 200)

	impSess, _ := fx.signedInAsMember(t)
	items := ancestorItems(t, callSession(t, fx.s, impSess, "GET", "/api/v1/nodes/"+file.ID+"/ancestors", nil))
	if len(items) != 1 {
		t.Fatalf("the impersonated person's ancestors = %d, want [b] (the grant only)", len(items))
	}
	var got nodeJSON
	if err := json.Unmarshal(items[0], &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != bID {
		t.Errorf("the impersonated person's ancestor is %s, want the granted directory %s", got.ID, bID)
	}
	if got.Role != "viewer" {
		t.Errorf("the role on the granted directory = %q, want viewer", got.Role)
	}

	// The admin's own read of the same node is refused, as stat refuses it.
	adminRead := callSession(t, fx.s, fx.adminSess, "GET", "/api/v1/nodes/"+file.ID+"/ancestors", nil)
	if adminRead.Code == 200 {
		t.Errorf("an installation admin read the ancestors of a person's file: %s", adminRead.Body.String())
	}
}
