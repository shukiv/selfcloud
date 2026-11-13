// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
)

// listDeviceIDs reads the ids of the devices the person in sess sees on Signed-in devices.
func listDeviceIDs(t *testing.T, s *Server, sess testSession) []string {
	t.Helper()
	var list struct {
		Devices []struct {
			ID string `json:"id"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(callSession(t, s, sess, "GET", "/api/v1/auth/cli/devices", nil).Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(list.Devices))
	for _, d := range list.Devices {
		ids = append(ids, d.ID)
	}
	return ids
}

// A person may end only their own device. Another user's device in the same account, and a device of another account,
// both answer 404, the same as an unknown id, so the answer does not show that the device exists. The owner's
// own device keeps working afterwards.
func TestDeviceRevokeOfAnotherUsersDeviceIsNotFound(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	_, tok := approvedToken(t, s, fx.memSess)
	ids := listDeviceIDs(t, s, fx.memSess)
	if len(ids) != 1 {
		t.Fatalf("devices = %v, want one", ids)
	}
	_, bossSess := verifiedUser(t, s, fx.accKey, fx.acc.ID, "boss@example.com")
	wantError(t, callSession(t, s, bossSess, "DELETE", "/api/v1/auth/cli/devices/"+ids[0], nil), 404, apierr.NotFound)
	wantStatus(t, getWith(t, s, tok, "/api/v1/folders"), 200)
}

func TestDeviceRevokeOfAnotherAccountsDeviceIsNotFound(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	_, tok := approvedToken(t, s, fx.memSess)
	ids := listDeviceIDs(t, s, fx.memSess)
	if len(ids) != 1 {
		t.Fatalf("devices = %v, want one", ids)
	}
	beta, betaKey := newAccount(t, s, fx.admin, "Beta")
	_, betaSess := verifiedUser(t, s, betaKey, beta.ID, "ann@example.com")
	wantError(t, callSession(t, s, betaSess, "DELETE", "/api/v1/auth/cli/devices/"+ids[0], nil), 404, apierr.NotFound)
	wantStatus(t, getWith(t, s, tok, "/api/v1/folders"), 200)
}

// The request's end is fixed at 600 s from its start. A poll before the end does not move it: a sign-in approved at 650 s
// is refused as expired, and its poll gets expired_token (the sliding mutant accepts the approval).
func TestDeviceApprovalAfterTheFixedEndIsRefused(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	t0 := time.Now().UTC().Truncate(time.Millisecond)
	now := t0
	s.store.SetClock(func() time.Time { return now })
	st := cliStart(t, s)

	now = t0.Add(100 * time.Second)
	wantError(t, cliPoll(t, s, st.DeviceCode), 400, apierr.AuthorizationPending)

	now = t0.Add(650 * time.Second)
	e := wantError(t, cliApprove(t, s, fx.memSess, st.UserCode), 400, apierr.InvalidRequest)
	if e.Details["reason"] != "expired" {
		t.Errorf("reason = %v, want expired", e.Details["reason"])
	}
	wantError(t, cliPoll(t, s, st.DeviceCode), 400, apierr.ExpiredToken)
}

// Ending a device is a change, so it needs the session's CSRF token as every session change does: a cookie alone is refused,
// and the device keeps working.
func TestDeviceRevokeNeedsCSRF(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	_, tok := approvedToken(t, s, fx.memSess)
	ids := listDeviceIDs(t, s, fx.memSess)
	if len(ids) != 1 {
		t.Fatalf("devices = %v, want one", ids)
	}
	req := jsonRequest(t, "DELETE", "/api/v1/auth/cli/devices/"+ids[0], nil)
	req.AddCookie(fx.memSess.cookie)
	req.Header.Set("Origin", s.origin)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("revoke without the CSRF token answered %d, want 403: %s", rec.Code, rec.Body.String())
	}
	wantStatus(t, getWith(t, s, tok, "/api/v1/folders"), 200)
}
