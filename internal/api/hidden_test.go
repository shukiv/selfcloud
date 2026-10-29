// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"strings"
	"testing"
)

// TestEmptyHiddenListIsAnArray: a user with no hidden team spaces gets an empty items array, not null, so the
// interface can read the list without a special case.
func TestEmptyHiddenListIsAnArray(t *testing.T) {
	fx := newSharingFixture(t)
	rec := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/folders?hidden=true", nil)
	wantStatus(t, rec, 200)
	if !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Fatalf("empty hidden listing = %s, want items to be []", rec.Body)
	}
}
