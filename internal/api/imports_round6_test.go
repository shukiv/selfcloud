// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/jobs"
)

// Another user of the account cannot hold someone else's archive: a request for an upload the caller did not make answers
// not_found (404), the same as a missing upload, and does not wait for the owner's slot (round 6, F14 ordering).
func TestAnotherUsersImportRequestDoesNotHoldTheArchive(t *testing.T) {
	fx := newImportFixture(t)
	fx.s.uploadSlotWait = 20 * time.Millisecond
	upl := fx.archive(t, "a.zip", zipArchive(t, [][2]string{{"a.txt", "1"}}))
	// The owner's PATCH or commit holds the slot.
	release, err := fx.s.takeUploadSlot(httptest.NewRecorder(), upl)
	if err != nil {
		t.Fatalf("taking the owner's slot: %v", err)
	}
	defer release()
	_, bobSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "bob@example.com")
	body := map[string]any{"source": "archive", "upload": upl, "folder": fx.folder}
	rec := callSession(t, fx.s, bobSess, "POST", "/api/v1/imports", body)
	wantStatus(t, rec, http.StatusNotFound)
}

// An entry an import has staged is not listed: a directory the import reused holds the staged entries as children by
// parent_id, but they are in the staging folder until the publish, so the directory's listing must not show them
// (round 6, the staged-node finding from the node-write guard).
func TestStagedEntriesAreNotListedUnderAReusedDirectory(t *testing.T) {
	fx := newImportFixture(t)
	data := zipArchive(t, [][2]string{{"t/a.txt", "1"}})
	target := mkdirIn(t, fx, fx.folder, "", "D")
	_ = importInto(t, fx, target.ID, data)
	reused := childrenOfNode(t, fx, target.ID)["t"]
	a := childrenOfNode(t, fx, reused.ID)["a.txt"]
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+a.ID, fx.key, nil), http.StatusNoContent)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/trash/"+a.ID, fx.key, nil), http.StatusNoContent)
	yielded := false
	fx.s.importFault = func(stage string) error {
		if stage == "publish" && !yielded {
			yielded = true
			return jobs.ErrYield
		}
		return nil
	}
	body := map[string]any{"source": "archive", "folder": fx.folder, "parent": target.ID, "upload": fx.archive(t, "a.zip", data)}
	wantStatus(t, callSession(t, fx.s, fx.sess, "POST", "/api/v1/imports", body), http.StatusAccepted)
	// The run stages a.txt under the reused directory, then yields at the publish.
	_ = fx.runJob(t)
	fx.s.importFault = nil
	if got := childrenOfNode(t, fx, reused.ID); len(got) != 0 {
		t.Fatalf("the reused directory lists %d staged entries, want none", len(got))
	}
}
