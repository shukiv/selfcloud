// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// lastSignInOf reads one user's last_sign_in_at through the API: nil when the user never signed in.
func lastSignInOf(t *testing.T, s *Server, key, accID, usr string) *time.Time {
	t.Helper()
	return decode[userJSON](t, call(t, s, "GET", "/api/v1/accounts/"+accID+"/users/"+usr, key, nil)).LastSignInAt
}

// TestUsersCarryTheirLastSignIn checks last_sign_in_at on a user: none for a user who never signed in, the time of the
// user's own sign-in once there is one, unchanged by an admin's sign-in-as, and moved on by the next sign-in. The admins
// list carries it too.
func TestUsersCarryTheirLastSignIn(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)

	pending := decode[userResponse](t, call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users", fx.accKey,
		map[string]any{"email": "pending@example.com", "verification": "link"}))
	if got := lastSignInOf(t, fx.s, fx.accKey, fx.acc.ID, pending.ID); got != nil {
		t.Errorf("a user who never signed in has last_sign_in_at %v, want null", got)
	}
	rec := call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/users/"+pending.ID, fx.accKey, nil)
	if !strings.Contains(rec.Body.String(), `"last_sign_in_at":null`) {
		t.Errorf("a user who never signed in does not say last_sign_in_at null: %s", rec.Body.String())
	}

	// The member's verification signed it in.
	first := lastSignInOf(t, fx.s, fx.accKey, fx.acc.ID, fx.member.ID)
	if first == nil {
		t.Fatal("the member signed in by its verification, and has no last_sign_in_at")
	}

	// A sign-in-as is the admin's, not the member's.
	time.Sleep(3 * time.Millisecond)
	wantStatus(t, fx.signInAs(t, fx.member.ID), 200)
	if got := lastSignInOf(t, fx.s, fx.accKey, fx.acc.ID, fx.member.ID); got == nil || !got.Equal(*first) {
		t.Errorf("after a sign-in-as, last_sign_in_at = %v, want it unchanged at %v", got, first)
	}

	// The member's next sign-in moves it on.
	time.Sleep(3 * time.Millisecond)
	wantStatus(t, login(t, fx.s, "member@example.com", testPassword, ""), 200)
	if got := lastSignInOf(t, fx.s, fx.accKey, fx.acc.ID, fx.member.ID); got == nil || !got.After(*first) {
		t.Errorf("after a sign-in, last_sign_in_at = %v, want later than %v", got, first)
	}

	// The admins list carries it: the admin signed in by its verification.
	admins := decode[listResponse[userJSON]](t, call(t, fx.s, "GET", "/api/v1/admins", fx.admin, nil))
	found := false
	for _, a := range admins.Items {
		if a.ID == fx.adminUser.ID {
			found = true
			if a.LastSignInAt == nil {
				t.Errorf("the admin signed in, and the admins list has no last_sign_in_at for it")
			}
		}
	}
	if !found {
		t.Fatal("the admin is not on the admins list")
	}
}

// TestListAccountsCountsUsers checks user_count on each account of both lists, sorted and plain, and that a single
// account's read leaves it out.
func TestListAccountsCountsUsers(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	other, _ := newAccount(t, fx.s, fx.admin, "Empty")
	want := map[string]int64{fx.acc.ID: 1, other.ID: 0}
	for _, q := range []string{"?sort=name", ""} {
		page := decode[listResponse[accountJSON]](t, call(t, fx.s, "GET", "/api/v1/accounts"+q, fx.admin, nil))
		if len(page.Items) != 2 {
			t.Fatalf("%q: %d accounts, want 2", q, len(page.Items))
		}
		for _, a := range page.Items {
			if a.UserCount == nil || *a.UserCount != want[a.ID] {
				t.Errorf("%q: %s has user_count %s, want %d", q, a.Name, userCountJSON(a.UserCount), want[a.ID])
			}
		}
	}
	rec := call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID, fx.admin, nil)
	wantStatus(t, rec, 200)
	if strings.Contains(rec.Body.String(), "user_count") {
		t.Errorf("a single account's read has user_count: %s", rec.Body.String())
	}
}

// userCountJSON is an account's user_count as a test message shows it: the number, or "absent".
func userCountJSON(n *int64) string {
	if n == nil {
		return "absent"
	}
	return strconv.FormatInt(*n, 10)
}
