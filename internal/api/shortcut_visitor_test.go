// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
)

// TestVisitorStatOfShortcutIsNotFound: a link visitor's stat of a shortcut inside the link gives the same answer as a
// stat of an item outside it, not_found. A shortcut's name and kind are not for a visitor at all (decision 24).
func TestVisitorStatOfShortcutIsNotFound(t *testing.T) {
	t.Parallel()
	fx := newAnyoneFixture(t)
	sc := makeShortcut(t, fx.sharingFixture, fx.c.ID, fx.deep.ID)
	scID := sc["id"].(string)
	outside := putNode(t, fx.s, fx.key, fx.fld.ID, "/a/elsewhere.txt", "outside")
	_, secret := fx.makeAnyone(t, fx.c.ID, nil)
	tok := fx.visitAnyone(t, secret, "")

	inside := call(t, fx.s, "GET", "/api/v1/nodes/"+scID, tok, nil)
	elsewhere := call(t, fx.s, "GET", "/api/v1/nodes/"+outside.ID, tok, nil)
	gotInside := wantError(t, inside, 404, apierr.NotFound)
	gotElsewhere := wantError(t, elsewhere, 404, apierr.NotFound)
	if !sameRefusal(gotInside, gotElsewhere) {
		t.Errorf("a visitor's stat of a shortcut answers %q, an item outside the link answers %q",
			gotInside.Message, gotElsewhere.Message)
	}
	if strings.Contains(inside.Body.String(), scID) || strings.Contains(inside.Body.String(), fx.deep.ID) {
		t.Errorf("a visitor's refusal names the shortcut or its target: %s", inside.Body.String())
	}
}
