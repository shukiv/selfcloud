// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
)

// What a terminal's device token may do with team spaces and groups. The command-line reference (docs/cli.md, "Folders and
// groups") and decision 145 rest on these answers, so a change to any of them must fail here first.
func TestDeviceTokenOnFoldersAndGroups(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	owner := ownerSessionOf(t, fx)
	_, tok := approvedToken(t, s, owner)

	rec := call(t, s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/folders", fx.accKey, map[string]any{"name": "Team"})
	wantStatus(t, rec, 201)
	fld := decode[folderJSON](t, rec).ID
	hidden := "/api/v1/folders/" + fld + "/hidden"
	listed := func(path string) []string {
		rec := getWith(t, s, tok, path)
		wantStatus(t, rec, 200)
		var ids []string
		for _, it := range decode[listResponse[folderAccessJSON]](t, rec).Items {
			ids = append(ids, it.ID)
		}
		return ids
	}
	contains := func(ids []string, id string) bool {
		for _, v := range ids {
			if v == id {
				return true
			}
		}
		return false
	}

	// Hiding and showing a space again are open to a device token.
	if !contains(listed("/api/v1/folders"), fld) {
		t.Fatal("the owner's device does not list the space before it is hidden")
	}
	wantStatus(t, rawCall(t, s, "PUT", hidden, tok, "", nil), 204)
	if contains(listed("/api/v1/folders"), fld) {
		t.Error("a hidden space is still in the plain list")
	}
	// The list of hidden spaces is answered only to a person's own session: for a token it is empty.
	if ids := listed("/api/v1/folders?hidden=true"); len(ids) != 0 {
		t.Errorf("the hidden list for a token holds %v, want it empty", ids)
	}
	wantStatus(t, rawCall(t, s, "DELETE", hidden, tok, "", nil), 204)
	if !contains(listed("/api/v1/folders"), fld) {
		t.Error("the space is not back in the list after show-again")
	}

	// A space's settings are refused to every token, with a plain forbidden and no reason.
	rec = rawCall(t, s, "PATCH", "/api/v1/folders/"+fld+"/settings", tok, `{"allow_links":false}`,
		map[string]string{"Content-Type": "application/json"})
	e := wantError(t, rec, 403, apierr.Forbidden)
	if reason, ok := e.Details["reason"]; ok {
		t.Errorf("the settings refusal carries the reason %v, want none", reason)
	}

	// An account's own administration reads as not found for a token, even the owner's.
	acc := "/api/v1/accounts/" + fx.acc.ID
	for _, p := range []struct{ method, path string }{
		{"GET", acc + "/folders"},
		{"GET", acc + "/folders/" + fld},
		{"GET", acc + "/groups"},
		{"GET", acc + "/users/" + fx.member.ID + "/grants"},
	} {
		wantError(t, rawCall(t, s, p.method, p.path, tok, "", nil), 404, apierr.NotFound)
	}
	wantError(t, call(t, s, "POST", acc+"/groups", tok, map[string]any{"name": "G"}), 404, apierr.NotFound)
	wantError(t, call(t, s, "POST", acc+"/folders", tok, map[string]any{"name": "Other"}), 404, apierr.NotFound)
	// The same owner's session does manage the account.
	wantStatus(t, callSession(t, s, owner, "GET", acc+"/folders", nil), 200)
}
