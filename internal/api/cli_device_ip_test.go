// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The device list shows the address and the user agent of the terminal that started the sign-in, as the approval page
// showed them, and not the address of the person who approved it (decision 125).
func TestDeviceListShowsTheStartAddress(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	const startIP = "198.51.100.23"
	const approverIP = "203.0.113.9"
	const agent = "selfcloud/test-terminal"

	// The terminal starts from startIP with its own user agent.
	req := jsonRequest(t, "POST", "/api/v1/auth/cli/start", map[string]any{"client_name": "test terminal"})
	req.RemoteAddr = startIP + ":40000"
	req.Header.Set("User-Agent", agent)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	wantStatus(t, rec, http.StatusCreated)
	st := decode[cliStartReply](t, rec)

	// The approval page shows the start address and the agent.
	look := decode[struct {
		ClientIP  string `json:"client_ip"`
		UserAgent string `json:"user_agent"`
	}](t, cliLookup(t, s, fx.memSess, st.UserCode))
	if look.ClientIP != startIP || look.UserAgent != agent {
		t.Fatalf("approve page shows %q %q, want %q %q", look.ClientIP, look.UserAgent, startIP, agent)
	}

	// The person approves from another address; the device keeps the terminal's address, not the approver's.
	wantStatus(t, cliFrom(t, s, "POST", "/api/v1/auth/cli/approve", approverIP,
		map[string]any{"user_code": st.UserCode}, &fx.memSess), 200)
	prec := cliPoll(t, s, st.DeviceCode)
	wantStatus(t, prec, 200)
	if tok := decode[struct {
		Token string `json:"token"`
	}](t, prec).Token; tok == "" {
		t.Fatal("no token after approval")
	}

	var list struct {
		Devices []struct {
			ClientName string `json:"client_name"`
			ClientIP   string `json:"client_ip"`
			UserAgent  string `json:"user_agent"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(callSession(t, s, fx.memSess, "GET", "/api/v1/auth/cli/devices", nil).Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Devices) != 1 {
		t.Fatalf("devices = %+v, want one", list.Devices)
	}
	d := list.Devices[0]
	if d.ClientIP != look.ClientIP {
		t.Errorf("list client_ip = %q, approve page showed %q", d.ClientIP, look.ClientIP)
	}
	if d.UserAgent != look.UserAgent {
		t.Errorf("list user_agent = %q, approve page showed %q", d.UserAgent, look.UserAgent)
	}
}
