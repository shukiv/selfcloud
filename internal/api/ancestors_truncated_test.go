// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// deepTree makes /d1/d2/.../dN in the folder and a file f.txt in dN, as cred. It returns the file and the IDs of d1 to dN
// in that order: dirs[i] is d(i+1). The file has N+1 ancestors: dN up to d1, then the folder's root.
func deepTree(t *testing.T, s *Server, cred, accID, fld string, depth int) (nodeJSON, []string) {
	t.Helper()
	segs := make([]string, depth)
	for i := range segs {
		segs[i] = fmt.Sprintf("d%d", i+1)
	}
	p := "/" + strings.Join(segs, "/")
	deepest := mkdirs(t, s, cred, fld, p)
	file := putNode(t, s, cred, fld, p+"/f.txt", "deep")
	dirs := make([]string, depth)
	id := deepest.ID
	for i := depth - 1; i >= 0; i-- {
		dirs[i] = id
		n, err := s.store.Node(context.Background(), accID, id)
		if err != nil {
			t.Fatal(err)
		}
		id = n.ParentID
	}
	return file, dirs
}

// chainOf decodes a 200 answer of GET /nodes/{nod}/ancestors into the IDs of its items, topmost first, and its truncated
// flag. The answer must carry exactly these two fields, so nothing else in it can tell the caller more.
func chainOf(t *testing.T, rec *httptest.ResponseRecorder) ([]string, bool) {
	t.Helper()
	wantStatus(t, rec, 200)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode ancestors: %v in %s", err, rec.Body.String())
	}
	fields := make([]string, 0, len(raw))
	for k := range raw {
		fields = append(fields, k)
	}
	slices.Sort(fields)
	if !reflect.DeepEqual(fields, []string{"ancestors", "truncated"}) {
		t.Fatalf("the answer's fields are %v, want exactly [ancestors truncated]", fields)
	}
	var truncated bool
	if err := json.Unmarshal(raw["truncated"], &truncated); err != nil {
		t.Fatalf("truncated is %s, not a boolean: %v", raw["truncated"], err)
	}
	return ancestorIDs(t, rec), truncated
}

// wantChain checks an answer's items, by ID and topmost first, and its truncated flag.
func wantChain(t *testing.T, who string, rec *httptest.ResponseRecorder, want []string, wantTruncated bool) {
	t.Helper()
	ids, truncated := chainOf(t, rec)
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("%s: the ancestors are %d items %v, want %d items %v", who, len(ids), ids, len(want), want)
	}
	if truncated != wantTruncated {
		t.Errorf("%s: truncated is %v, want %v", who, truncated, wantTruncated)
	}
}

// wantItemsAreStat checks that each item of an answer is the stat answer the same caller gets for that node.
func wantItemsAreStat(t *testing.T, who string, rec *httptest.ResponseRecorder, stat func(id string) *httptest.ResponseRecorder) {
	t.Helper()
	for _, item := range ancestorItems(t, rec) {
		var n nodeJSON
		if err := json.Unmarshal(item, &n); err != nil {
			t.Fatal(err)
		}
		got := stat(n.ID)
		wantStatus(t, got, 200)
		if !sameValue(t, item, got.Body.Bytes()) {
			t.Errorf("%s: item %s is not the stat answer\nitem: %s\nstat: %s", who, n.Name, item, got.Body.String())
		}
	}
}

// TestAncestorsOfAShallowNodeAreNotTruncated: a chain that ends at the folder's root well below the cap is not truncated.
func TestAncestorsOfAShallowNodeAreNotTruncated(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	root := fx.rootOf(t, fx.fld.ID)
	wantChain(t, "the owner", call(t, fx.s, "GET", "/api/v1/nodes/"+fx.deep.ID+"/ancestors", fx.key, nil),
		[]string{root, fx.a.ID, fx.b.ID}, false)
}

// TestAncestorsAreTruncatedWhenTheParentAboveTheCapIsReached: a file 64 directories down has 65 ancestors, the folder's root
// the 65th. The answer is the 64 nearest, d1 to d64, and truncated, because the owner reaches the root above them. A file
// 70 directories down answers d7 to d70, truncated, and d6 above them is reached.
func TestAncestorsAreTruncatedWhenTheParentAboveTheCapIsReached(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	file, dirs := deepTree(t, fx.s, fx.key, fx.acc.ID, fx.fld.ID, 64)
	wantChain(t, "65 ancestors", call(t, fx.s, "GET", "/api/v1/nodes/"+file.ID+"/ancestors", fx.key, nil), dirs, true)

	other := newFolder(t, fx.s, fx.key, fx.acc.ID, "Deeper")
	file70, dirs70 := deepTree(t, fx.s, fx.key, fx.acc.ID, other.ID, 70)
	wantChain(t, "71 ancestors", call(t, fx.s, "GET", "/api/v1/nodes/"+file70.ID+"/ancestors", fx.key, nil), dirs70[6:], true)
}

// TestAncestorsAreNotTruncatedAtExactly64: a file 63 directories down has 64 ancestors, the folder's root the topmost. The
// answer is all of them, the root first, and not truncated: the walk ends at the root, at the cap and not past it.
func TestAncestorsAreNotTruncatedAtExactly64(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	file, dirs := deepTree(t, fx.s, fx.key, fx.acc.ID, fx.fld.ID, 63)
	want := append([]string{fx.rootOf(t, fx.fld.ID)}, dirs...)
	wantChain(t, "64 ancestors", call(t, fx.s, "GET", "/api/v1/nodes/"+file.ID+"/ancestors", fx.key, nil), want, false)
}

// TestAncestorsAreNotTruncatedAboveAGrantAtTheCap: Ann is given d7, the 64th ancestor of a file 70 directories down. d6, the
// 65th, exists and Ann does not reach it. Her answer is d7 to d70 and not truncated: the answer of a chain that ends at d7.
// Nothing in it names d6 but d7's own parent_id, which her stat of d7 gives too. Once she is given d6, her answer is
// truncated.
func TestAncestorsAreNotTruncatedAboveAGrantAtTheCap(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	file, dirs := deepTree(t, fx.s, fx.key, fx.acc.ID, fx.fld.ID, 70)
	d6, d7 := dirs[5], dirs[6]
	fx.setPerm(t, d7, fx.ann.ID, "viewer")
	rec := ancestorsCall(t, fx.s, fx.annSess, file.ID)
	wantChain(t, "a grant on the 64th", rec, dirs[6:], false)
	wantItemsAreStat(t, "a grant on the 64th", rec, func(id string) *httptest.ResponseRecorder {
		return callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+id, nil)
	})
	if n := strings.Count(rec.Body.String(), d6); n != 1 {
		t.Errorf("the answer names d6 %d times, want once (d7's parent_id): %s", n, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"name":"d6"`) {
		t.Errorf("the answer names d6, which Ann does not reach")
	}

	fx.setPerm(t, d6, fx.ann.ID, "viewer")
	wantChain(t, "a grant on the 65th", ancestorsCall(t, fx.s, fx.annSess, file.ID), dirs[6:], true)
}

// TestAncestorsTruncatedForAnAnyoneLinkVisitor: a visitor of a link on d7, the 64th ancestor, gets d7 to d70, not
// truncated, and d7 with no parent: the link's node is the top of what the visitor sees, and nothing says a folder is
// above it. A visitor of a link on d6 reaches the 65th, so the answer is truncated.
func TestAncestorsTruncatedForAnAnyoneLinkVisitor(t *testing.T) {
	t.Parallel()
	fx := newAnyoneFixture(t)
	file, dirs := deepTree(t, fx.s, fx.key, fx.acc.ID, fx.fld.ID, 70)
	d6, d7 := dirs[5], dirs[6]

	_, at64 := fx.makeAnyone(t, d7, nil)
	tok := fx.visitAnyone(t, at64, "")
	rec := call(t, fx.s, "GET", "/api/v1/nodes/"+file.ID+"/ancestors", tok, nil)
	wantChain(t, "a link on the 64th", rec, dirs[6:], false)
	wantItemsAreStat(t, "a link on the 64th", rec, func(id string) *httptest.ResponseRecorder {
		return call(t, fx.s, "GET", "/api/v1/nodes/"+id, tok, nil)
	})
	if strings.Contains(rec.Body.String(), d6) {
		t.Errorf("a visitor's answer names d6, above the link's node: %s", rec.Body.String())
	}

	_, at65 := fx.makeAnyone(t, d6, nil)
	tok65 := fx.visitAnyone(t, at65, "")
	wantChain(t, "a link on the 65th", call(t, fx.s, "GET", "/api/v1/nodes/"+file.ID+"/ancestors", tok65, nil), dirs[6:], true)
}

// TestAncestorsTruncatedUnderAReadTokensNodeScope: Ann is an editor on d6, so she reaches the 65th ancestor. A read token of
// hers confined to d7 does not: its answer is d7 to d70, not truncated, each item at the token's viewer cap. A read token of
// hers with no node scope reaches d6, and its answer is truncated.
func TestAncestorsTruncatedUnderAReadTokensNodeScope(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	file, dirs := deepTree(t, fx.s, fx.key, fx.acc.ID, fx.fld.ID, 70)
	d6, d7 := dirs[5], dirs[6]
	fx.setPerm(t, d6, fx.ann.ID, "editor")

	confined := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"all": true, "user": fx.ann.ID, "node": d7})
	rec := getWith(t, fx.s, confined, "/api/v1/nodes/"+file.ID+"/ancestors")
	wantChain(t, "a token confined to the 64th", rec, dirs[6:], false)
	for _, item := range ancestorItems(t, rec) {
		if role := roleOf(t, item); role != "viewer" {
			t.Errorf("an item's role under a read token = %q, want viewer", role)
		}
	}
	wantItemsAreStat(t, "a token confined to the 64th", rec, func(id string) *httptest.ResponseRecorder {
		return getWith(t, fx.s, confined, "/api/v1/nodes/"+id)
	})

	whole := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"all": true, "user": fx.ann.ID})
	wantChain(t, "a token with no node scope", getWith(t, fx.s, whole, "/api/v1/nodes/"+file.ID+"/ancestors"), dirs[6:], true)
}

// TestAncestorsTruncatedForAnOrganisationLinkVisitor: a member who opened an organisation link on d7, the 64th ancestor,
// gets d7 to d70, not truncated. A member who opened one on d6 reaches the 65th, so the answer is truncated.
func TestAncestorsTruncatedForAnOrganisationLinkVisitor(t *testing.T) {
	t.Parallel()
	fx := newLinkFixture(t)
	file, dirs := deepTree(t, fx.s, fx.key, fx.acc.ID, fx.fld.ID, 70)
	d6, d7 := dirs[5], dirs[6]

	_, url64 := fx.makeLink(t, d7, "viewer")
	fx.openedAs(t, fx.annSess, secretOf(t, url64))
	rec := ancestorsCall(t, fx.s, fx.annSess, file.ID)
	wantChain(t, "a link on the 64th", rec, dirs[6:], false)
	wantItemsAreStat(t, "a link on the 64th", rec, func(id string) *httptest.ResponseRecorder {
		return callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+id, nil)
	})
	if strings.Contains(rec.Body.String(), `"name":"d6"`) {
		t.Errorf("the link visitor's answer names d6")
	}

	_, url65 := fx.makeLink(t, d6, "viewer")
	fx.openedAs(t, fx.bobSess, secretOf(t, url65))
	wantChain(t, "a link on the 65th", ancestorsCall(t, fx.s, fx.bobSess, file.ID), dirs[6:], true)
}

// TestAncestorsTruncatedForAnImpersonatedPerson: an installation admin signed in as a member given d7, the 64th ancestor,
// gets the member's answer: d7 to d70, not truncated. Once the member is given d6, the answer is truncated.
func TestAncestorsTruncatedForAnImpersonatedPerson(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	f := newFolder(t, fx.s, fx.accKey, fx.acc.ID, "Team")
	file, dirs := deepTree(t, fx.s, fx.accKey, fx.acc.ID, f.ID, 70)
	d6, d7 := dirs[5], dirs[6]
	grant := func(nodeID string) {
		wantStatus(t, call(t, fx.s, "PUT", "/api/v1/nodes/"+nodeID+"/permissions/"+fx.member.ID, fx.accKey,
			map[string]any{"role": "viewer"}), 200)
	}

	grant(d7)
	impSess, _ := fx.signedInAsMember(t)
	rec := callSession(t, fx.s, impSess, "GET", "/api/v1/nodes/"+file.ID+"/ancestors", nil)
	wantChain(t, "a grant on the 64th", rec, dirs[6:], false)
	if strings.Contains(rec.Body.String(), `"name":"d6"`) {
		t.Errorf("the impersonated person's answer names d6")
	}

	grant(d6)
	wantChain(t, "a grant on the 65th", callSession(t, fx.s, impSess, "GET", "/api/v1/nodes/"+file.ID+"/ancestors", nil), dirs[6:], true)
}
