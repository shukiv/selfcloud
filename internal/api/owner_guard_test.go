// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/mail"
)

// makeOwner creates a user with the owner role through the API, verifies it, and returns it.
func makeOwner(t *testing.T, s *Server, key, accID, email string) userResponse {
	t.Helper()
	rec := call(t, s, "POST", "/api/v1/accounts/"+accID+"/users", key,
		map[string]any{"email": email, "role": "owner", "verification": "link"})
	wantStatus(t, rec, 201)
	created := decode[userResponse](t, rec)
	wantStatus(t, call(t, s, "POST", "/api/v1/auth/verify", "",
		map[string]any{"token": created.Verification.Token, "password": testPassword}), 200)
	return created
}

// TestLastOwnerAndSelfAreKept checks that an account always keeps an active owner, and that an owner
// cannot remove its own user. Each refusal is a conflict whose details give the reason.
func TestLastOwnerAndSelfAreKept(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	users := "/api/v1/accounts/" + acc.ID + "/users/"
	only := makeOwner(t, s, key, acc.ID, "only@example.com")
	onlySess := sessionFrom(t, s, call(t, s, "POST", "/api/v1/auth/login", "",
		map[string]any{"email": "only@example.com", "password": testPassword}))

	// The only active owner cannot be demoted, disabled or removed, by any caller.
	for _, body := range []map[string]any{{"role": "member"}, {"status": "disabled"}} {
		e := wantError(t, call(t, s, "PATCH", users+only.ID, key, body), 409, apierr.Conflict)
		if e.Details["reason"] != "last_owner" {
			t.Errorf("%v: reason = %v, want last_owner", body, e.Details["reason"])
		}
	}
	e := wantError(t, call(t, s, "DELETE", users+only.ID, key, nil), 409, apierr.Conflict)
	if e.Details["reason"] != "last_owner" {
		t.Errorf("delete last owner: reason = %v, want last_owner", e.Details["reason"])
	}

	// An owner removing itself is refused, even when another owner exists.
	second := makeOwner(t, s, key, acc.ID, "second@example.com")
	e = wantError(t, callSession(t, s, onlySess, "DELETE", users+only.ID, nil), 409, apierr.Conflict)
	if e.Details["reason"] != "self" {
		t.Errorf("self delete: reason = %v, want self", e.Details["reason"])
	}

	// With a second active owner, the first may be demoted and removed by the second.
	wantStatus(t, call(t, s, "PATCH", users+only.ID, key, map[string]any{"role": "member"}), 200)
	wantStatus(t, call(t, s, "PATCH", users+second.ID, key, map[string]any{"role": "member"}), 409)
	wantStatus(t, call(t, s, "DELETE", users+only.ID, key, nil), 204)

	// A pending owner is not an active one: removing the only active owner is still refused.
	pending := call(t, s, "POST", "/api/v1/accounts/"+acc.ID+"/users", key,
		map[string]any{"email": "later@example.com", "role": "owner", "verification": "link"})
	wantStatus(t, pending, 201)
	later := decode[userResponse](t, pending)
	wantStatus(t, call(t, s, "DELETE", users+later.ID, key, nil), 204)
}

// TestRemovingPendingOwnerIsAllowed checks that a pending owner, who cannot sign in, may be removed
// even when it is the only owner that is not yet active, and that removing an active member is not
// affected by the guard.
func TestRemovingPendingOwnerIsAllowed(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	owner := makeOwner(t, s, key, acc.ID, "owner@example.com")
	pending := decode[userResponse](t, call(t, s, "POST", "/api/v1/accounts/"+acc.ID+"/users", key,
		map[string]any{"email": "pending@example.com", "role": "owner", "verification": "link"}))
	users := "/api/v1/accounts/" + acc.ID + "/users/"
	wantStatus(t, call(t, s, "DELETE", users+pending.ID, key, nil), 204)
	wantStatus(t, call(t, s, "PATCH", users+owner.ID, key, map[string]any{"role": "member"}), 409)
}
