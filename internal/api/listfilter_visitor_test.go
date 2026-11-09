// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"strings"
	"testing"
)

// TestVisitorShortcutFilterGivesAnEmptyList: a link visitor's folder listing with ?type=shortcut is an empty list, not
// an error and not the shortcut. A shortcut inside a link is not for a visitor at all (decision 24), so the filter that
// names the kind finds nothing the visitor may see.
func TestVisitorShortcutFilterGivesAnEmptyList(t *testing.T) {
	t.Parallel()
	fx := newAnyoneFixture(t)
	sc := makeShortcut(t, fx.sharingFixture, fx.c.ID, fx.deep.ID)
	scID := sc["id"].(string)
	_, secret := fx.makeAnyone(t, fx.c.ID, nil)
	tok := fx.visitAnyone(t, secret, "")

	rec := call(t, fx.s, "GET", "/api/v1/nodes/"+fx.c.ID+"/children?type=shortcut", tok, nil)
	wantStatus(t, rec, 200)
	if items := decode[listResponse[nodeJSON]](t, rec).Items; len(items) != 0 {
		t.Errorf("a visitor's type=shortcut listing has %d item(s), want none", len(items))
	}
	if body := rec.Body.String(); strings.Contains(body, scID) || strings.Contains(body, fx.deep.ID) {
		t.Errorf("a visitor's type=shortcut listing names the shortcut or its target: %s", body)
	}
}
