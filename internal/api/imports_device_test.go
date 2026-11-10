// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/shukiv/selfcloud/internal/store"
)

// deviceImportBody is one archive import of one file into the fixture's folder, as the web interface starts it.
func deviceImportBody(t *testing.T, fx *importFixture) map[string]any {
	t.Helper()
	return map[string]any{"source": "archive", "folder": fx.folder,
		"upload": fx.archive(t, "one.zip", zipArchive(t, [][2]string{{"a.txt", "1"}}))}
}

// A device token starts no import, and resumes none: an import job runs with its stored actor and does not re-check the
// token that started it, so a device's import would keep writing after the device is revoked (review re-check 1). The
// refusal is the device refusal, before the handler runs.
func TestDeviceTokenRefusedStartingAnImport(t *testing.T) {
	t.Parallel()
	fx := newImportFixture(t)
	_, dtok := approvedToken(t, fx.s, fx.sess)
	wantDeviceRefused(t, call(t, fx.s, "POST", "/api/v1/imports", dtok, deviceImportBody(t, fx)))
	wantDeviceRefused(t, call(t, fx.s, "POST", "/api/v1/imports/imp_none/resume", dtok, map[string]any{}))
}

// A delegated token still starts an import, as it did before the device refusal existed.
func TestDelegatedTokenStillStartsAnImport(t *testing.T) {
	t.Parallel()
	fx := newImportFixture(t)
	dtok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"user": fx.user.ID,
		"folders": []string{fx.folder}, "permissions": "write"})
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/imports", dtok, deviceImportBody(t, fx)), http.StatusAccepted)
}

// A device token cancels no import. The usual sequence is a pause, then a cancel: once the paused job has ended, no
// import job is unfinished, so a cancel would queue a cleanup job under the stored actor (review re-check 2). The
// refusal is the device refusal, the import stays paused, and no job is queued.
func TestDeviceTokenRefusedCancellingAnImport(t *testing.T) {
	t.Parallel()
	fx := newImportFixture(t)
	imp := fx.startOK(t, fx.archive(t, "c.zip", zipArchive(t, photosZip)), "")
	wantStatus(t, callSession(t, fx.s, fx.sess, "POST", "/api/v1/imports/"+imp.ID+"/pause", nil), http.StatusOK)
	if err := fx.runJob(t); err != nil {
		t.Fatalf("a paused import's job ends: %v", err)
	}
	_, dtok := approvedToken(t, fx.s, fx.sess)
	wantDeviceRefused(t, call(t, fx.s, "POST", "/api/v1/imports/"+imp.ID+"/cancel", dtok, nil))
	if got := fx.get(t, imp.ID); got.State != store.ImportPaused {
		t.Fatalf("after a device cancel: state %s, want paused", got.State)
	}
	if _, err := fx.s.store.ClaimJobOf(context.Background(), jobImport); err == nil {
		t.Fatal("a device cancel queued an import job")
	}
}

// A device token still pauses an import: a pause queues nothing and only stops work, so it stays open to a device.
func TestDeviceTokenMayPauseAnImport(t *testing.T) {
	t.Parallel()
	fx := newImportFixture(t)
	imp := fx.startOK(t, fx.archive(t, "p.zip", zipArchive(t, photosZip)), "")
	_, dtok := approvedToken(t, fx.s, fx.sess)
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/imports/"+imp.ID+"/pause", dtok, nil), http.StatusOK)
	if err := fx.runJob(t); err != nil {
		t.Fatalf("a paused import's job ends: %v", err)
	}
	if got := fx.get(t, imp.ID); got.State != store.ImportPaused {
		t.Fatalf("after a device pause: state %s, want paused", got.State)
	}
}
