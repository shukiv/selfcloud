// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"strings"
	"testing"
)

// zipAsContributor makes a zip of root as ann, who is a contributor and may zip it, and returns the zip's id.
func zipAsContributor(t *testing.T, fx *sharingFixture, root string) string {
	t.Helper()
	fx.setPerm(t, fx.a.ID, fx.ann.ID, "contributor")
	job := createZip(t, fx, &fx.annSess, root)
	if j := runZip(t, fx, job.ID); j.Status != "succeeded" {
		t.Fatalf("job %s: %s", j.Status, j.Error)
	}
	return job.Target
}

// TestZipGetRefusesAfterDemotion: a contributor zips a covered folder, the owner demotes her to viewer, and every
// GET of the zip is refused, a Range resume and a conditional GET included. The refusal is the create rule.
func TestZipGetRefusesAfterDemotion(t *testing.T) {
	fx := zipFixture(t)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+fx.a.ID, fx.key, map[string]any{"no_download": true}), 200)
	zipID := zipAsContributor(t, fx, fx.a.ID)
	first := getZip(t, fx, &fx.annSess, zipID, nil)
	wantStatus(t, first, 200)
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("the zip carries no ETag")
	}
	fx.setPerm(t, fx.a.ID, fx.ann.ID, "viewer")
	rec := getZip(t, fx, &fx.annSess, zipID, nil)
	wantStatus(t, rec, 403)
	if !strings.Contains(rec.Body.String(), "download_restricted") {
		t.Errorf("refusal %s does not name download_restricted", rec.Body.String())
	}
	wantStatus(t, getZip(t, fx, &fx.annSess, zipID, map[string]string{"Range": "bytes=10-"}), 403)
	wantStatus(t, getZip(t, fx, &fx.annSess, zipID, map[string]string{"If-None-Match": etag}), 403)
}

// TestZipGetRefusesCoveredFileInUncoveredFolder: the folder is not covered, but a file under it is, so a viewer's
// copy of the folder is refused: the subtree counts, as it does for a create.
func TestZipGetRefusesCoveredFileInUncoveredFolder(t *testing.T) {
	fx := zipFixture(t)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+fx.deep.ID, fx.key, map[string]any{"no_download": true}), 200)
	zipID := zipAsContributor(t, fx, fx.a.ID)
	wantStatus(t, getZip(t, fx, &fx.annSess, zipID, nil), 200)
	fx.setPerm(t, fx.a.ID, fx.ann.ID, "viewer")
	wantStatus(t, getZip(t, fx, &fx.annSess, zipID, nil), 403)
}

// TestZipGetAllowsUncovered: nothing under the folder is covered, so a demoted viewer may still fetch the zip.
func TestZipGetAllowsUncovered(t *testing.T) {
	fx := zipFixture(t)
	zipID := zipAsContributor(t, fx, fx.a.ID)
	wantStatus(t, getZip(t, fx, &fx.annSess, zipID, nil), 200)
	fx.setPerm(t, fx.a.ID, fx.ann.ID, "viewer")
	wantStatus(t, getZip(t, fx, &fx.annSess, zipID, nil), 200)
}

// TestZipGetKeepsAnswerForContributor: a contributor who zipped a covered folder keeps the zip, Range included.
func TestZipGetKeepsAnswerForContributor(t *testing.T) {
	fx := zipFixture(t)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+fx.a.ID, fx.key, map[string]any{"no_download": true}), 200)
	zipID := zipAsContributor(t, fx, fx.a.ID)
	wantStatus(t, getZip(t, fx, &fx.annSess, zipID, nil), 200)
	wantStatus(t, getZip(t, fx, &fx.annSess, zipID, map[string]string{"Range": "bytes=10-"}), 206)
}
