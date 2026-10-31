// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"strings"
	"testing"
)

// TestZipCallerMatrix: the zip routes refuse the callers they must refuse. A visitor of a link that anyone can open
// is refused by the route table, as on every route that does not admit links (reason link_route). The admin key
// never gets a zip: admins manage tenants and read no files. The account key and the makers are in
// TestZipIsForItsMakerAndCheckedEveryCall.
func TestZipCallerMatrix(t *testing.T) {
	fx := newAnyoneFixture(t)
	job := createZip(t, fx.sharingFixture, nil, fx.a.ID)
	if j := runZip(t, fx.sharingFixture, job.ID); j.Status != "succeeded" {
		t.Fatalf("job %s: %s", j.Status, j.Error)
	}
	_, secret := fx.makeAnyone(t, fx.b.ID, nil)
	visitor := byCred(t, fx.s, fx.visitAnyone(t, secret, ""))
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/zips/" + job.Target},
		{"DELETE", "/api/v1/zips/" + job.Target},
	} {
		rec := visitor(c.method, c.path, nil)
		wantStatus(t, rec, 403)
		if !strings.Contains(rec.Body.String(), "link_route") {
			t.Errorf("%s %s: refusal %s does not name link_route", c.method, c.path, rec.Body.String())
		}
	}
	rec := visitor("POST", "/api/v1/zips", map[string]any{"nodes": []string{fx.b.ID}})
	wantStatus(t, rec, 403)
	if !strings.Contains(rec.Body.String(), "link_route") {
		t.Errorf("POST /api/v1/zips: refusal %s does not name link_route", rec.Body.String())
	}
	admin := adminKey(t, fx.s)
	if rec := call(t, fx.s, "POST", "/api/v1/zips", admin, map[string]any{"nodes": []string{fx.a.ID}}); rec.Code == 202 {
		t.Errorf("admin key queued a zip of a file tree")
	}
	if rec := call(t, fx.s, "GET", "/api/v1/zips/"+job.Target, admin, nil); rec.Code == 200 {
		t.Errorf("admin key downloaded a zip")
	}
}

// TestZipRestrictedAfterRequestFailsTheJob: a viewer's zip is queued, then the tree becomes no_download before the job
// runs. The job checks the caller's guard at its start, and refuses to make the zip, as a copy's batch does.
func TestZipRestrictedAfterRequestFailsTheJob(t *testing.T) {
	fx := zipFixture(t)
	fx.setPerm(t, fx.a.ID, fx.ann.ID, "viewer")
	job := createZip(t, fx, &fx.annSess, fx.a.ID)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+fx.b.ID, fx.key, map[string]any{"no_download": true}), 200)
	j := runZip(t, fx, job.ID)
	if j.Status != "failed" || !strings.Contains(j.Error, "download") {
		t.Fatalf("job %s %q, want failed on the no-download rule", j.Status, j.Error)
	}
}
