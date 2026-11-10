// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/id"
)

// ancestorsBody is the answer of GET /nodes/{nod}/ancestors. Items stay raw, so a test can compare each one with the
// stat answer for the same node, byte for byte in its JSON value.
type ancestorsBody struct {
	Ancestors []json.RawMessage `json:"ancestors"`
}

// ancestorIDs decodes a 200 answer into the IDs of its items, topmost first.
func ancestorIDs(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	wantStatus(t, rec, 200)
	var body struct {
		Ancestors []nodeJSON `json:"ancestors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode ancestors: %v in %s", err, rec.Body.String())
	}
	ids := make([]string, 0, len(body.Ancestors))
	for _, n := range body.Ancestors {
		ids = append(ids, n.ID)
	}
	return ids
}

// sameValue reports whether two JSON documents hold the same value, whatever their key order or spacing.
func sameValue(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("decode %s: %v", a, err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return reflect.DeepEqual(x, y)
}

// ancestorsCall is GET /nodes/{nod}/ancestors as a session user.
func ancestorsCall(t *testing.T, s *Server, sess testSession, nodeID string) *httptest.ResponseRecorder {
	t.Helper()
	return callSession(t, s, sess, "GET", "/api/v1/nodes/"+nodeID+"/ancestors", nil)
}

// TestAncestorsOwnerChainIsTopmostFirst: the owner's chain of a file is the folder root, then each directory down to
// the parent. The node itself is not in it, and a root has none, as an empty array (not null).
func TestAncestorsOwnerChainIsTopmostFirst(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	root := fx.rootOf(t, fx.fld.ID)
	want := []string{root, fx.a.ID, fx.b.ID}
	got := ancestorIDs(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.deep.ID+"/ancestors", fx.key, nil))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ancestors of the file = %v, want %v (folder root, a, b: topmost first, parent last)", got, want)
	}

	rec := call(t, fx.s, "GET", "/api/v1/nodes/"+root+"/ancestors", fx.key, nil)
	wantStatus(t, rec, 200)
	if !strings.Contains(rec.Body.String(), `"ancestors":[]`) {
		t.Errorf("the folder root's answer = %s, want an empty array", rec.Body.String())
	}
}

// TestAncestorsItemsAreTheStatAnswers: each item is byte for byte the stat answer for its node, for the caller it is
// given to, with the caller's role and the enrichments stat makes (a star is one of them).
func TestAncestorsItemsAreTheStatAnswers(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+fx.b.ID+"/star", nil), 204)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.b.ID, nil), 200)

	for _, caller := range []struct {
		name string
		call func(path string) *httptest.ResponseRecorder
	}{
		{"owner", func(p string) *httptest.ResponseRecorder { return call(t, fx.s, "GET", p, fx.key, nil) }},
		{"person with a grant", func(p string) *httptest.ResponseRecorder { return callSession(t, fx.s, fx.annSess, "GET", p, nil) }},
	} {
		rec := caller.call("/api/v1/nodes/" + fx.deep.ID + "/ancestors")
		wantStatus(t, rec, 200)
		var body ancestorsBody
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		for _, item := range body.Ancestors {
			var n nodeJSON
			if err := json.Unmarshal(item, &n); err != nil {
				t.Fatal(err)
			}
			stat := caller.call("/api/v1/nodes/" + n.ID)
			wantStatus(t, stat, 200)
			if !sameValue(t, item, stat.Body.Bytes()) {
				t.Errorf("%s: item %s is not the stat answer\nitem: %s\nstat: %s", caller.name, n.ID, item, stat.Body.String())
			}
		}
		if len(body.Ancestors) == 0 {
			t.Errorf("%s: no ancestors read, the comparison checked nothing", caller.name)
		}
	}
}

// TestAncestorsStopAtAGrantBelowTheRoot: a person given a directory gets the chain from that directory down. The
// directories above the grant, and the folder root, are not returned and not hinted at.
func TestAncestorsStopAtAGrantBelowTheRoot(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	rec := ancestorsCall(t, fx.s, fx.annSess, fx.deep.ID)
	if got := ancestorIDs(t, rec); !reflect.DeepEqual(got, []string{fx.b.ID}) {
		t.Errorf("ancestors for the granted person = %v, want [b] only", got)
	}
	root := fx.rootOf(t, fx.fld.ID)
	for _, hidden := range []string{root, fx.a.ID} {
		if strings.Contains(rec.Body.String(), hidden) {
			t.Errorf("the answer names %s, which the person does not reach: %s", hidden, rec.Body.String())
		}
	}
	// The granted directory's own chain stops at the grant too: its parent is not reached.
	if got := ancestorIDs(t, ancestorsCall(t, fx.s, fx.annSess, fx.b.ID)); len(got) != 0 {
		t.Errorf("ancestors of the granted directory = %v, want none", got)
	}
	// A person with no grant reaches nothing, so the node itself is not found.
	wantError(t, ancestorsCall(t, fx.s, fx.bobSess, fx.deep.ID), 404, apierr.NotFound)
}

// TestAncestorsOfAnUnreachableNodeAreNotFoundLikeStat: a node the caller cannot reach, a malformed ID and an unknown
// ID all answer exactly as stat answers them.
func TestAncestorsOfAnUnreachableNodeAreNotFoundLikeStat(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	unknown := id.New("nod")
	for _, nodeID := range []string{fx.sib.ID, "nod_not-a-valid-id", unknown} {
		ancestors := wantError(t, ancestorsCall(t, fx.s, fx.annSess, nodeID), 404, apierr.NotFound)
		stat := wantError(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+nodeID, nil), 404, apierr.NotFound)
		if !sameRefusal(ancestors, stat) {
			t.Errorf("%s: ancestors refuse with %q, stat with %q", nodeID, ancestors.Message, stat.Message)
		}
		if strings.Contains(ancestorsCall(t, fx.s, fx.annSess, nodeID).Body.String(), fx.a.ID) {
			t.Errorf("%s: the refusal names a node above", nodeID)
		}
	}
}

// TestAncestorsEndAtTheLinkRoot: a visitor of an anyone link gets the chain up to the link's node, and no further. The
// link's node has no parent in the answer, as its stat does.
func TestAncestorsEndAtTheLinkRoot(t *testing.T) {
	t.Parallel()
	fx := newAnyoneFixture(t)
	_, secret := fx.makeAnyone(t, fx.b.ID, nil)
	tok := fx.visitAnyone(t, secret, "")

	rec := call(t, fx.s, "GET", "/api/v1/nodes/"+fx.deep.ID+"/ancestors", tok, nil)
	if got := ancestorIDs(t, rec); !reflect.DeepEqual(got, []string{fx.b.ID}) {
		t.Errorf("a visitor's ancestors = %v, want [the link's node]", got)
	}
	if strings.Contains(rec.Body.String(), fx.a.ID) || strings.Contains(rec.Body.String(), fx.fld.ID) {
		t.Errorf("a visitor's answer names a node above the link: %s", rec.Body.String())
	}
	stat := call(t, fx.s, "GET", "/api/v1/nodes/"+fx.b.ID, tok, nil)
	var body ancestorsBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Ancestors) != 1 {
		t.Fatalf("ancestors body = %s (%v)", rec.Body.String(), err)
	}
	if !sameValue(t, body.Ancestors[0], stat.Body.Bytes()) {
		t.Errorf("a visitor's item is not the link node's stat\nitem: %s\nstat: %s", body.Ancestors[0], stat.Body.String())
	}
	if got := ancestorIDs(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.b.ID+"/ancestors", tok, nil)); len(got) != 0 {
		t.Errorf("a visitor's ancestors of the link's node = %v, want none", got)
	}
}

// TestAncestorsEndAtTheTokensNode: a delegated token confined to a directory lists that directory and nothing above it.
func TestAncestorsEndAtTheTokensNode(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	tok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"all": true, "user": fx.ann.ID, "node": fx.b.ID})
	if got := ancestorIDs(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.deep.ID+"/ancestors", tok, nil)); !reflect.DeepEqual(got, []string{fx.b.ID}) {
		t.Errorf("a confined token's ancestors = %v, want [b]", got)
	}
	wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.sib.ID+"/ancestors", tok, nil), 404, apierr.NotFound)
}

// TestAncestorsOfAFolderListToken: a token that names folders reaches only those folders. A node in another folder is
// not found, and the answer for one in a listed folder is the folder's chain.
func TestAncestorsOfAFolderListToken(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	other := newFolder(t, fx.s, fx.key, fx.acc.ID, "Other")
	otherFile := putNode(t, fx.s, fx.key, other.ID, "/y.txt", "other")
	tok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}})
	root := fx.rootOf(t, fx.fld.ID)
	if got := ancestorIDs(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.deep.ID+"/ancestors", tok, nil)); !reflect.DeepEqual(got, []string{root, fx.a.ID, fx.b.ID}) {
		t.Errorf("a listed folder's ancestors = %v, want the folder's chain", got)
	}
	wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+otherFile.ID+"/ancestors", tok, nil), 404, apierr.NotFound)
}

// TestAncestorsEndAtATrashedDirectory: once a directory is in the trash, nothing below it is reached, so the node
// below is not found, and the same goes for its ancestors' answer.
func TestAncestorsEndAtATrashedDirectory(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.a.ID, fx.key, nil), 204)
	ancestors := wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.deep.ID+"/ancestors", fx.key, nil), 404, apierr.NotFound)
	stat := wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.deep.ID, fx.key, nil), 404, apierr.NotFound)
	if !sameRefusal(ancestors, stat) {
		t.Errorf("ancestors below a trashed directory refuse with %q, stat with %q", ancestors.Message, stat.Message)
	}
}

// TestAncestorsCapIs64Nearest: beyond 64 ancestors the answer is the 64 nearest, topmost first, as the web walk gave.
func TestAncestorsCapIs64Nearest(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	var segs []string
	for i := 1; i <= 70; i++ {
		segs = append(segs, fmt.Sprintf("d%d", i))
	}
	deepDir := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/"+strings.Join(segs, "/"))
	file := putNode(t, fx.s, fx.key, fx.fld.ID, "/"+strings.Join(segs, "/")+"/f.txt", "deep")
	if deepDir.Name != "d70" {
		t.Fatalf("the deep directory is %q", deepDir.Name)
	}
	rec := call(t, fx.s, "GET", "/api/v1/nodes/"+file.ID+"/ancestors", fx.key, nil)
	wantStatus(t, rec, 200)
	var body struct {
		Ancestors []nodeJSON `json:"ancestors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Ancestors) != 64 {
		t.Fatalf("got %d ancestors, want 64", len(body.Ancestors))
	}
	if first, last := body.Ancestors[0].Name, body.Ancestors[63].Name; first != "d7" || last != "d70" {
		t.Errorf("the 64 items run from %q to %q, want d7 to d70 (the 64 nearest, topmost first)", first, last)
	}
}

// TestAncestorsOfANodeOfAnotherAccountAreNotFound: an account key of another account reaches nothing here.
func TestAncestorsOfANodeOfAnotherAccountAreNotFound(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	admin := fx.admin
	_, otherKey := newAccount(t, fx.s, admin, "Other")
	wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.deep.ID+"/ancestors", otherKey, nil), 404, apierr.NotFound)
}

// TestAncestorsAnswerNoRoleForAnUnreachableFolderAndTheCallerWithNoCredential: no credential is refused as stat refuses.
func TestAncestorsAnswerNoCredentialLikeStat(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	bare := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/nodes/"+fx.deep.ID+"/ancestors", nil)
	fx.s.Handler().ServeHTTP(bare, req)
	statBare := httptest.NewRecorder()
	fx.s.Handler().ServeHTTP(statBare, httptest.NewRequest("GET", "/api/v1/nodes/"+fx.deep.ID, nil))
	if bare.Code != statBare.Code || bare.Code == 200 {
		t.Errorf("no credential: ancestors %d, stat %d; want the same refusal", bare.Code, statBare.Code)
	}
}
