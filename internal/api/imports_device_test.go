// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"testing"
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
