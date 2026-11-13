// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"testing"
)

// fu2 (decision 74): a directory made under a parent named by its node ID. The path form names the parent by
// its path, so a folder renamed or moved in another session a moment earlier can get the new folder at its old path.
// The parent form names the node, which keeps its ID whatever its name, and it keeps every refusal of a write.

// A parent renamed before the call still receives the new folder, and the path it had is not made again.
func TestMkdirByParentFollowsARename(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	dirs := "/api/v1/folders/" + fx.fld.ID + "/directories"
	// Another session renames /a to /b a moment before this call.
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+fx.a.ID, fx.key, map[string]any{"name": "b"}), http.StatusOK)

	rec := call(t, fx.s, "POST", dirs, fx.key, map[string]any{"parent": fx.a.ID, "name": "new"})
	wantStatus(t, rec, http.StatusCreated)
	if m := decodeMap(t, rec); m["parent_id"] != fx.a.ID || m["name"] != "new" {
		t.Errorf("made directory = %v, want name new under the renamed parent %s", m, fx.a.ID)
	}
	// The old path names nothing now, so the path form is refused, and nothing is made there.
	wantError(t, call(t, fx.s, "POST", dirs, fx.key, map[string]any{"path": "/a/other"}), http.StatusNotFound, "not_found")
}

// The path form is unchanged: a path under the root is made as before.
func TestMkdirPathFormIsUnchanged(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	dirs := "/api/v1/folders/" + fx.fld.ID + "/directories"
	wantStatus(t, call(t, fx.s, "POST", dirs, fx.key, map[string]any{"path": "/z", "parents": false}), http.StatusCreated)
	wantError(t, call(t, fx.s, "POST", dirs, fx.key, map[string]any{"path": "/z"}), http.StatusConflict, "conflict")
}

// The parent form refuses what every write refuses: a parent in the trash, a parent of another account, a parent in
// another folder than the one named in the URL, a file, and a name that is not one element.
func TestMkdirByParentRefusals(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	dirs := "/api/v1/folders/" + fx.fld.ID + "/directories"
	other, otherKey := newAccount(t, fx.s, fx.admin, "Beta")
	otherFld := newFolder(t, fx.s, otherKey, other.ID, "Team")

	wantError(t, call(t, fx.s, "POST", "/api/v1/folders/"+otherFld.ID+"/directories", otherKey,
		map[string]any{"parent": fx.a.ID, "name": "x"}), http.StatusNotFound, "not_found")
	// A parent of this account, named in another folder's URL, is not_found: the URL's folder is the scope.
	second := newFolder(t, fx.s, fx.key, fx.acc.ID, "Second")
	wantError(t, call(t, fx.s, "POST", "/api/v1/folders/"+second.ID+"/directories", fx.key,
		map[string]any{"parent": fx.a.ID, "name": "x"}), http.StatusNotFound, "not_found")
	wantError(t, call(t, fx.s, "POST", dirs, fx.key,
		map[string]any{"parent": fx.deep.ID, "name": "x"}), http.StatusBadRequest, "invalid_request")
	wantError(t, call(t, fx.s, "POST", dirs, fx.key,
		map[string]any{"parent": fx.a.ID, "name": "x/y"}), http.StatusBadRequest, "invalid_path")
	wantError(t, call(t, fx.s, "POST", dirs, fx.key,
		map[string]any{"parent": fx.a.ID, "path": "/x"}), http.StatusBadRequest, "invalid_request")

	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.a.ID, fx.key, nil), http.StatusNoContent)
	wantError(t, call(t, fx.s, "POST", dirs, fx.key, map[string]any{"parent": fx.a.ID, "name": "x"}),
		http.StatusNotFound, "not_found")
}
