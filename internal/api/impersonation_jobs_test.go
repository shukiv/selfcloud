// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"testing"

	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/store"
)

// TestImpersonatedJobsKeepTheMark: a zip, a large copy and a move made while an admin is signed in as a user carry the
// admin's mark into their audit rows (decision 41), including a job that runs after the sign-in-as has ended (F5).
func TestImpersonatedJobsKeepTheMark(t *testing.T) {
	t.Parallel()
	fx := zipFixture(t, func(c *config.Config) { c.Admin.TenantAccess = true })
	fx.s.copySyncNodes = 3 // the tree under /a is larger, so the copy is a job
	root, rootSess := newAdminUser(t, fx.s, fx.admin, "root@example.com")
	fx.grant(t, fx.fldRoot(t), fx.ann.ID, "editor")
	rec := callSession(t, fx.s, rootSess, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.ann.ID+"/sign-in",
		map[string]any{"admin_password": testPassword})
	wantStatus(t, rec, 200)
	imp := sessionFrom(t, fx.s, rec)
	par := parked(t, fx.s, rec)

	zip := createZip(t, fx, &imp, fx.a.ID)
	copyRes := callSession(t, fx.s, imp, "POST", "/api/v1/nodes/"+fx.a.ID+"/copy",
		map[string]any{"folder": fx.fld.ID, "name": "a copy"})
	wantStatus(t, copyRes, 202)
	copyJob := decode[jobJSON](t, copyRes)
	moveRes := callSession(t, fx.s, imp, "POST", "/api/v1/nodes/"+fx.deep.ID+"/move",
		map[string]any{"folder": fx.fld.ID, "parent": fx.c.ID})
	wantStatus(t, moveRes, 200)

	// The sign-in-as ends before the jobs run.
	wantStatus(t, goBack(t, fx.s, imp, par), 200)
	if err := fx.s.jobs.RunPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, j := range []string{zip.ID, copyJob.ID} {
		if st, err := fx.s.store.Job(context.Background(), j); err != nil || st.Status != store.JobSucceeded {
			t.Fatalf("job %s: %v", j, err)
		}
	}
	items := decode[listResponse[auditJSON]](t, call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/audit",
		fx.key, nil)).Items
	for _, action := range []string{"node.zip", "node.copy", "node.move"} {
		e := findAudit(items, action)
		if e == nil {
			t.Errorf("%s: no audit row", action)
			continue
		}
		if e.Details["impersonated_by"] != root.ID {
			t.Errorf("%s: impersonated_by = %v, want the admin %s", action, e.Details["impersonated_by"], root.ID)
		}
	}
}

// fldRoot is the root node of the team folder the zip fixture makes, the node its permissions are about.
func (fx *sharingFixture) fldRoot(t *testing.T) string {
	t.Helper()
	return decode[folderJSON](t, call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/folders/"+fx.fld.ID, fx.key, nil)).RootNodeID
}
