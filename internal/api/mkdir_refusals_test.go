// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"testing"
)

// fu2 (decision 74). The parent form makes a directory, so it needs contributor on the parent, and a
// share-link visitor never writes through the directories route. Each refusal is pinned, and the contributor check
// fails when it is lowered (the mutation proof).

// A member with viewer on the parent is refused: no directory is made, and the answer is forbidden, as for every write.
func TestMkdirByParentRefusesAViewer(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.a.ID, fx.ann.ID, "viewer")
	dirs := "/api/v1/folders/" + fx.fld.ID + "/directories"
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", dirs, map[string]any{"parent": fx.a.ID, "name": "x"}),
		http.StatusForbidden, "forbidden")
	if n := sqlCount(t, fx.s, "SELECT COUNT(*) FROM nodes WHERE parent_id = ? AND name = 'x'", fx.a.ID); n != 0 {
		t.Errorf("a directory was made by a viewer: %d rows", n)
	}
}

// A share-link visitor cannot make a directory by the parent form: an anyone link gives the viewer role only (decision
// 24), and a viewer is refused by the contributor check, so no directory is made.
func TestMkdirByParentRefusesAShareLinkVisitor(t *testing.T) {
	t.Parallel()
	fx := newAnyoneFixture(t)
	_, secret := fx.makeAnyone(t, fx.a.ID, nil)
	tok := fx.visitAnyone(t, secret, "")
	rec := call(t, fx.s, "POST", "/api/v1/folders/"+fx.fld.ID+"/directories", tok,
		map[string]any{"parent": fx.a.ID, "name": "visitor-dir"})
	wantStatus(t, rec, http.StatusForbidden)
	if rec.Code == http.StatusCreated {
		t.Fatalf("a visitor made a directory: %d %s", rec.Code, rec.Body)
	}
	if n := sqlCount(t, fx.s, "SELECT COUNT(*) FROM nodes WHERE name = 'visitor-dir'"); n != 0 {
		t.Errorf("a directory was made by a visitor: %d rows", n)
	}
}
