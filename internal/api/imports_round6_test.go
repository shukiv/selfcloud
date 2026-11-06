// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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
