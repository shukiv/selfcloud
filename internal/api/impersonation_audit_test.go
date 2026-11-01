// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"testing"
)

// TestRefusedSignInAsIsItsOwnRow: a wrong admin password is a refused attempt, not a sign-in. It is recorded as
// admin.password_refused with its way, and no impersonation.start row is written for it (the owner's audit log showed
// both as a sign-in). The right password writes one start row, and going back writes one end row, each naming the admin
// for the viewers (decision 41).
func TestRefusedSignInAsIsItsOwnRow(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	rec := fx.signInAs(t, fx.member.ID) // right password, to have a sign-in to compare against
	wantStatus(t, rec, 200)
	par := parked(t, fx.s, rec)
	imp := sessionFrom(t, fx.s, rec)
	wantStatus(t, goBack(t, fx.s, imp, par), 200)

	wantError(t, callSession(t, fx.s, fx.adminSess, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.member.ID+"/sign-in",
		map[string]any{"admin_password": "wrong password here"}), 403, "forbidden")

	items := fx.auditOf(t, fx.accKey)
	refused := findAudit(items, "admin.password_refused")
	if refused == nil || refused.Details["way"] != waySignInAs || refused.Outcome != "failed" {
		t.Fatalf("no refused sign-in row with way %s: %+v", waySignInAs, refused)
	}
	if refused.Target != fx.member.ID || refused.Actor.ID != fx.adminUser.ID {
		t.Errorf("the refused row = %+v, want the admin, targeting the member", refused)
	}
	starts := 0
	for _, e := range items {
		if e.Action == "impersonation.start" {
			starts++
			if e.Actor.ID != fx.adminUser.ID || e.Details["actor_admin"] != true || e.Details["actor_name"] != fx.adminUser.Email {
				t.Errorf("impersonation.start names %v, want the admin %s", e.Details, fx.adminUser.Email)
			}
		}
	}
	if starts != 1 {
		t.Errorf("impersonation.start rows = %d, want one for one right password", starts)
	}
	end := findAudit(items, "impersonation.end")
	if end == nil || end.Details["reason"] != "returned" || end.Details["actor_name"] != fx.adminUser.Email {
		t.Errorf("impersonation.end = %+v, want reason returned, named for the admin", end)
	}
}

// TestAuditNamesADeletedAdmin: a row an admin made is still named for the viewers after the admin is removed, with no
// name, so the viewers say "an installation admin" and never a raw ID (decision 41).
func TestAuditNamesADeletedAdmin(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	imp, _ := fx.signedInAsMember(t)
	_ = imp
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/admins/"+fx.adminUser.ID, fx.admin, nil), 204)

	end := findAudit(fx.auditOf(t, fx.accKey), "impersonation.end")
	if end == nil {
		t.Fatal("removing the admin wrote no impersonation.end row")
	}
	if end.Details["actor_admin"] != true {
		t.Errorf("the end row is not marked as an admin's: %v", end.Details)
	}
	if name, ok := end.Details["actor_name"]; !ok || name != "" {
		t.Errorf("actor_name = %v for a removed admin, want an empty name", end.Details["actor_name"])
	}
}
