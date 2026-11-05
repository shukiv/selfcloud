// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
)

// TestAdminSessionLinkRules checks what an admin session may do with verification methods. It may
// ask for a link to set up a user who has never verified, but never "none". A link for a password
// reset is refused while admin.tenant_access is off, with its reason; when it is on, the link is
// allowed (TestResetLinkWhenAllowedClearsTheFlag). Owner and member sessions may only use email.
// A password reset as a link from an admin session is refused while admin.tenant_access is off, and says why (sec1,
// decision 84, the same gate as the owner's link).
func TestAdminSessionResetLinkNeedsTenantAccess(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	_, adminSess := newAdminUser(t, s, admin, "ann@example.com")
	acc, key := newAccount(t, s, admin, "Acme")
	active, _ := verifiedUser(t, s, key, acc.ID, "active@example.com")
	e := wantError(t, callSession(t, s, adminSess, "POST", "/api/v1/accounts/"+acc.ID+"/users/"+active.ID+"/password-reset",
		map[string]any{"method": "link"}), 403, apierr.Forbidden)
	if e.Details["reason"] != reasonTenantAccessOff {
		t.Errorf("reset link refused with reason %v, want %s", e.Details["reason"], reasonTenantAccessOff)
	}
}

func TestAdminSessionLinkRules(t *testing.T) {
	t.Parallel()
	// The admin's link for a pending owner needs admin.tenant_access (sec1, decision 84), so the test turns it on.
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.Admin.TenantAccess = true })
	admin := adminKey(t, s)
	_, adminSess := newAdminUser(t, s, admin, "ann@example.com")
	users := func(acc string) string { return "/api/v1/accounts/" + acc + "/users" }

	// A new tenant with an owner, made by the admin session with a link: the owner gets a token.
	created := callSession(t, s, adminSess, "POST", "/api/v1/accounts", map[string]any{"name": "Acme",
		"admin_password": testPassword, "owner": map[string]any{"email": "owner@example.com", "verification": "link"}})
	wantStatus(t, created, 201)
	acc := decode[accountResponse](t, created)
	if acc.Owner == nil || acc.Owner.Verification == nil || acc.Owner.Verification.Token == "" ||
		acc.Owner.Verification.URL == "" {
		t.Fatalf("owner = %+v", acc.Owner)
	}

	// The owner is pending. Resending it a link from the admin session works, since it is pending.
	resend := callSession(t, s, adminSess, "POST", users(acc.ID)+"/"+acc.Owner.ID+"/verification",
		map[string]any{"method": "link", "admin_password": testPassword})
	wantStatus(t, resend, 200)
	if got := decode[userResponse](t, resend); got.Verification == nil || got.Verification.Token == "" {
		t.Errorf("resend to pending gave no token: %+v", got)
	}

	// none is refused for an admin session, both on create and on resend.
	wantError(t, callSession(t, s, adminSess, "POST", users(acc.ID),
		map[string]any{"email": "new@example.com", "verification": "none"}), 403, apierr.Forbidden)
	wantError(t, callSession(t, s, adminSess, "POST", users(acc.ID)+"/"+acc.Owner.ID+"/verification",
		map[string]any{"method": "none"}), 403, apierr.Forbidden)

	// A password reset as a link is refused while admin.tenant_access is off: see TestAdminSessionResetLinkNeedsTenantAccess.
	active, _ := verifiedUser(t, s, adminKeyAccount(t, s, admin, acc.ID), acc.ID, "active@example.com")

	// Email still works for a reset, and the answer carries no token.
	reset := callSession(t, s, adminSess, "POST", users(acc.ID)+"/"+active.ID+"/password-reset",
		map[string]any{"method": "email"})
	wantStatus(t, reset, 200)
	if got := decode[struct {
		Method string `json:"method"`
		Token  string `json:"token"`
	}](t, reset); got.Token != "" {
		t.Errorf("email reset carried a token")
	}

	// A new admin made by an admin session is made by email only.
	wantError(t, callSession(t, s, adminSess, "POST", "/api/v1/admins",
		map[string]any{"email": "second@example.com", "verification": "link"}), 403, apierr.Forbidden)
	wantStatus(t, callSession(t, s, adminSess, "POST", "/api/v1/admins",
		map[string]any{"email": "second@example.com", "verification": "email"}), 201)

	// A pending member is invited by its owner, so an admin session gets email only for it; an
	// owner made by the admin session may still get a link, with the admin's password (sec1, F4).
	wantError(t, callSession(t, s, adminSess, "POST", users(acc.ID),
		map[string]any{"email": "member@example.com", "verification": "link"}), 403, apierr.Forbidden)
	wantStatus(t, callSession(t, s, adminSess, "POST", users(acc.ID),
		map[string]any{"email": "owner2@example.com", "role": "owner", "verification": "link", "admin_password": testPassword}), 201)
}

// adminKeyAccount returns an account key for acc, made with the admin key.
func adminKeyAccount(t *testing.T, s *Server, admin, acc string) string {
	t.Helper()
	rec := call(t, s, "POST", "/api/v1/accounts/"+acc+"/keys", admin, map[string]any{"name": "test"})
	wantStatus(t, rec, 201)
	return decode[createdKey](t, rec).Key
}
