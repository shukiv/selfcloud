// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
)

// signDeviceForTest signs a device payload under the server's device key, as the server would, so a test can change one
// field and keep the MAC valid. The server never does this.
func signDeviceForTest(t *testing.T, s *Server, pl devicePayload) string {
	t.Helper()
	b, err := json.Marshal(pl)
	if err != nil {
		t.Fatal(err)
	}
	return auth.DevicePrefix + base64.RawURLEncoding.EncodeToString(b) + "." +
		base64.RawURLEncoding.EncodeToString(tokenMAC(s.deviceTokens.current.mac, b))
}

// devicePayloadOf reads a device token's payload without checking its MAC, for a test that changes it.
func devicePayloadOf(t *testing.T, tok string) devicePayload {
	t.Helper()
	body, _, err := splitTokenUnder(auth.DevicePrefix, tok)
	if err != nil {
		t.Fatal(err)
	}
	pl, err := parseDevicePayload(body)
	if err != nil {
		t.Fatal(err)
	}
	return pl
}

// A device token that names another user of its account is refused: the row belongs to the user it was made for.
func TestDeviceOfAnotherUserIsRefused(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	_, tok := approvedToken(t, s, fx.memSess)
	boss, _ := verifiedUser(t, s, fx.accKey, fx.acc.ID, "boss@example.com")
	pl := devicePayloadOf(t, tok)
	pl.UserID = boss.ID
	wantError(t, getWith(t, s, signDeviceForTest(t, s, pl), "/api/v1/folders"), 401, apierr.Unauthenticated)
}

// A device token that names another account is refused: its row is in the account it was made for.
func TestDeviceOfAnotherAccountIsRefused(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	_, tok := approvedToken(t, s, fx.memSess)
	beta, betaKey := newAccount(t, s, fx.admin, "Beta")
	betaUser, _ := verifiedUser(t, s, betaKey, beta.ID, "ann@example.com")
	pl := devicePayloadOf(t, tok)
	pl.AccountID = beta.ID
	pl.UserID = betaUser.ID
	wantError(t, getWith(t, s, signDeviceForTest(t, s, pl), "/api/v1/folders"), 401, apierr.Unauthenticated)
}

// The approval limit applies per session as well as per address: one session's codes are limited whatever address they
// come from.
func TestDeviceApprovalLimitIsPerSession(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	st := cliStart(t, s)
	for i := range 10 {
		ip := fmt.Sprintf("198.51.100.%d", i+1)
		rec := cliFrom(t, s, "POST", "/api/v1/auth/cli/approve", ip, map[string]any{"user_code": otherCode(st.UserCode)}, &fx.memSess)
		if rec.Code == 429 {
			t.Fatalf("attempt %d was limited", i+1)
		}
	}
	wantError(t, cliFrom(t, s, "POST", "/api/v1/auth/cli/approve", "203.0.113.200",
		map[string]any{"user_code": otherCode(st.UserCode)}, &fx.memSess), 429, apierr.RateLimited)
}
