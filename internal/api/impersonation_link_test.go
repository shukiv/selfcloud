// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
)

// TestImpersonatedSignedLinkEndsWithTheSignIn: the viewer's preview link is a signed link, and it works while an admin
// is signed in as the user. Its life is capped at the sign-in-as's hour, so it never outlives that hour.
func TestImpersonatedSignedLinkEndsWithTheSignIn(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	imp, _ := fx.signedInAsMember(t)
	fld := newFolder(t, fx.s, fx.accKey, fx.acc.ID, "One")
	a := putNode(t, fx.s, fx.accKey, fld.ID, "/a.txt", "aaa")
	root := decode[folderJSON](t, call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/folders/"+fld.ID, fx.accKey, nil)).RootNodeID
	wantStatus(t, call(t, fx.s, "PUT", "/api/v1/nodes/"+root+"/permissions/"+fx.member.ID, fx.accKey,
		map[string]any{"role": "viewer", "notify": false}), 200)

	// The default life is accepted; a life past the sign-in-as's hour is refused with the cap.
	wantStatus(t, callSession(t, fx.s, imp, "POST", "/api/v1/nodes/"+a.ID+"/links", map[string]any{}), 201)
	e := wantError(t, callSession(t, fx.s, imp, "POST", "/api/v1/nodes/"+a.ID+"/links",
		map[string]any{"ttl": 3600}), 400, apierr.InvalidRequest)
	if max, _ := e.Details["max_ttl_seconds"].(float64); max <= 0 || max >= 3600 {
		t.Errorf("max_ttl_seconds = %v, want a cap under an hour", e.Details["max_ttl_seconds"])
	}
}
