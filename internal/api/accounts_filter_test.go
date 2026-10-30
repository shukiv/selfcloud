// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/mail"
)

// TestListAccountsSearchAndStatus checks GET /accounts?q= (a name contains the text, or the external ID
// is exactly it, folding case) and ?status= (active or suspended), and that the text is matched
// literally: a % or _ in it is not a wildcard.
func TestListAccountsSearchAndStatus(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acme := decode[accountJSON](t, call(t, s, "POST", "/api/v1/accounts", admin,
		map[string]any{"name": "Acme Widgets"}))
	beta := decode[accountJSON](t, call(t, s, "POST", "/api/v1/accounts", admin,
		map[string]any{"name": "Beta Co", "external_id": "ext-beta"}))
	list := func(query string) []string {
		t.Helper()
		var ids []string
		for _, a := range decode[listResponse[accountJSON]](t, call(t, s, "GET", "/api/v1/accounts"+query, admin, nil)).Items {
			ids = append(ids, a.ID)
		}
		return ids
	}
	same := func(got []string, want ...string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	if got := list("?q=ACME"); !same(got, acme.ID) {
		t.Errorf("q=ACME = %v, want only %s", got, acme.ID)
	}
	if got := list("?q=ext-beta"); !same(got, beta.ID) {
		t.Errorf("q=ext-beta = %v, want only %s", got, beta.ID)
	}
	if got := list("?q=%25"); len(got) != 0 {
		t.Errorf("q=%% matched %v; it must be literal", got)
	}
	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+acme.ID, admin, map[string]any{"status": "suspended"}), 200)
	if got := list("?status=suspended"); !same(got, acme.ID) {
		t.Errorf("status=suspended = %v, want only %s", got, acme.ID)
	}
	if got := list("?status=active"); !same(got, beta.ID) {
		t.Errorf("status=active = %v, want only %s", got, beta.ID)
	}
	wantError(t, call(t, s, "GET", "/api/v1/accounts?status=deleting", admin, nil), 400, apierr.InvalidRequest)
}
