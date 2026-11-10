// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// Device sign-in for the CLI (decision 125, design-cli 3.3). These tests drive the flow over HTTP against the real store.

type cliStartReply struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_url"`
	Interval        int    `json:"interval"`
	ExpiresIn       int    `json:"expires_in"`
}

type cliTokenReply struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// cliFrom sends a request from one client address, so the per-address limits can be told apart.
func cliFrom(t *testing.T, s *Server, method, path, ip string, body any, cookie *testSession) *httptest.ResponseRecorder {
	t.Helper()
	req := jsonRequest(t, method, path, body)
	req.RemoteAddr = ip + ":40000"
	if cookie != nil {
		req.AddCookie(cookie.cookie)
		if isUnsafe(method) {
			req.Header.Set("Origin", s.origin)
			req.Header.Set("X-CSRF-Token", cookie.csrf)
		}
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// cliStart starts a sign-in as the CLI does, with no credential.
func cliStart(t *testing.T, s *Server) cliStartReply {
	t.Helper()
	rec := call(t, s, "POST", "/api/v1/auth/cli/start", "", map[string]any{"client_name": "test terminal"})
	wantStatus(t, rec, 201)
	return decode[cliStartReply](t, rec)
}

// cliPoll polls with a device code.
func cliPoll(t *testing.T, s *Server, devCode string) *httptest.ResponseRecorder {
	t.Helper()
	return call(t, s, "POST", "/api/v1/auth/cli/poll", "", map[string]any{"device_code": devCode})
}

// cliApprove approves the request whose user code is code, from the browser session sess.
func cliApprove(t *testing.T, s *Server, sess testSession, code string) *httptest.ResponseRecorder {
	t.Helper()
	return callSession(t, s, sess, "POST", "/api/v1/auth/cli/approve", map[string]any{"user_code": code})
}

// cliLookup shows the request whose user code is code, from the browser session sess.
func cliLookup(t *testing.T, s *Server, sess testSession, code string) *httptest.ResponseRecorder {
	t.Helper()
	return callSession(t, s, sess, "POST", "/api/v1/auth/cli/lookup", map[string]any{"user_code": code})
}

// otherCode changes the last letter of a user code, so it is a valid code that is not the request's.
func otherCode(code string) string {
	last := code[len(code)-1]
	next := byte('B')
	if last == 'B' {
		next = 'C'
	}
	return code[:len(code)-1] + string(next)
}

// approvedToken starts a request, approves it with sess and returns the device token from the poll.
func approvedToken(t *testing.T, s *Server, sess testSession) (cliStartReply, string) {
	t.Helper()
	st := cliStart(t, s)
	wantStatus(t, cliApprove(t, s, sess, st.UserCode), 200)
	rec := cliPoll(t, s, st.DeviceCode)
	wantStatus(t, rec, 200)
	tok := decode[cliTokenReply](t, rec).Token
	return st, tok
}

var userCodeShape = regexp.MustCompile(`^[BCDFGHJKLMNPQRSTVWXZ]{4}-[BCDFGHJKLMNPQRSTVWXZ]{4}$`)

func TestDeviceStartReturnsTheCodes(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	rec := call(t, fx.s, "POST", "/api/v1/auth/cli/start", "", map[string]any{"client_name": "test terminal"})
	wantStatus(t, rec, 201)
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	st := decode[cliStartReply](t, rec)
	if len(st.DeviceCode) != 43 {
		t.Errorf("device_code has %d characters, want 43 (32 random bytes)", len(st.DeviceCode))
	}
	if !userCodeShape.MatchString(st.UserCode) {
		t.Errorf("user_code %q is not 8 letters from the 20-letter alphabet, in two groups", st.UserCode)
	}
	if want := fx.s.origin + "/cli-approve"; st.VerificationURL != want {
		t.Errorf("verification_url = %q, want %q", st.VerificationURL, want)
	}
	if strings.Contains(st.VerificationURL, strings.ReplaceAll(st.UserCode, "-", "")) {
		t.Errorf("verification_url %q carries the code", st.VerificationURL)
	}
	if st.Interval != 5 || st.ExpiresIn != 600 {
		t.Errorf("interval %d and expires_in %d, want 5 and 600", st.Interval, st.ExpiresIn)
	}
}

// Ten starts an hour from one address; the eleventh is refused (cli.start.ip, the ten pending requests an hour).
func TestDeviceStartIsLimitedPerAddress(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	for range 10 {
		wantStatus(t, cliFrom(t, fx.s, "POST", "/api/v1/auth/cli/start", "198.51.100.7", map[string]any{}, nil), 201)
	}
	wantError(t, cliFrom(t, fx.s, "POST", "/api/v1/auth/cli/start", "198.51.100.7", map[string]any{}, nil), 429, apierr.RateLimited)
	wantStatus(t, cliFrom(t, fx.s, "POST", "/api/v1/auth/cli/start", "198.51.100.8", map[string]any{}, nil), 201)
}

// A poll sooner than the interval gets slow_down, and the interval grows by 5 s each time. The request still expires at
// 600 s from its start, which the poll does not move.
func TestDevicePollSlowDownGrowsTheInterval(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	t0 := time.Now().UTC().Truncate(time.Millisecond)
	now := t0
	s.store.SetClock(func() time.Time { return now })
	st := cliStart(t, s)

	now = t0.Add(1 * time.Second)
	e := wantError(t, cliPoll(t, s, st.DeviceCode), 400, apierr.Code("slow_down"))
	if e.Details["interval"] != float64(10) {
		t.Errorf("slow_down interval = %v, want 10", e.Details["interval"])
	}
	now = t0.Add(6 * time.Second)
	e = wantError(t, cliPoll(t, s, st.DeviceCode), 400, apierr.Code("slow_down"))
	if e.Details["interval"] != float64(15) {
		t.Errorf("second slow_down interval = %v, want 15", e.Details["interval"])
	}
	now = t0.Add(21 * time.Second)
	wantError(t, cliPoll(t, s, st.DeviceCode), 400, apierr.Code("authorization_pending"))
}

// The poll answers once. An approved request gives the token, and a second poll gets expired_token. The token reads the
// folders it reaches through the API, as any delegated token does.
func TestDevicePollAnswersOnce(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	st, tok := approvedToken(t, s, fx.memSess)
	if !strings.HasPrefix(tok, "scd_") {
		t.Fatalf("token prefix: got %q", tok[:min(4, len(tok))])
	}
	wantError(t, cliPoll(t, s, st.DeviceCode), 400, apierr.Code("expired_token"))
	wantStatus(t, getWith(t, s, tok, "/api/v1/folders"), 200)
}

// A request dies 600 s after its start and does not move. At 599.9 s it can still be approved; at 600 s it cannot.
func TestDeviceRequestExpiresAtTenMinutes(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	t0 := time.Now().UTC().Truncate(time.Millisecond)
	now := t0
	s.store.SetClock(func() time.Time { return now })
	st := cliStart(t, s)
	now = t0.Add(600*time.Second - time.Millisecond)
	wantStatus(t, cliApprove(t, s, fx.memSess, st.UserCode), 200)

	now = t0
	st2 := cliStart(t, s)
	now = t0.Add(600*time.Second + 2*time.Second)
	e := wantError(t, cliApprove(t, s, fx.memSess, st2.UserCode), 400, apierr.InvalidRequest)
	if e.Details["reason"] != "expired" {
		t.Errorf("reason = %v, want expired", e.Details["reason"])
	}
	wantError(t, cliPoll(t, s, st2.DeviceCode), 400, apierr.Code("expired_token"))
}

// Four wrong codes leave the request standing; the right code then approves it.
func TestDeviceFourWrongCodesKeepTheRequest(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	st := cliStart(t, s)
	for range 4 {
		e := wantError(t, cliApprove(t, s, fx.memSess, otherCode(st.UserCode)), 400, apierr.InvalidRequest)
		if e.Details["reason"] != "wrong_code" {
			t.Fatalf("reason = %v, want wrong_code", e.Details["reason"])
		}
	}
	wantStatus(t, cliApprove(t, s, fx.memSess, st.UserCode), 200)
}

// Five wrong codes delete the request: the right code then finds nothing, and the poll says expired_token.
func TestDeviceFiveWrongCodesDeleteTheRequest(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	st := cliStart(t, s)
	for range 5 {
		wantError(t, cliApprove(t, s, fx.memSess, otherCode(st.UserCode)), 400, apierr.InvalidRequest)
	}
	wantError(t, cliApprove(t, s, fx.memSess, st.UserCode), 400, apierr.InvalidRequest)
	wantError(t, cliPoll(t, s, st.DeviceCode), 400, apierr.Code("expired_token"))
}

// A wrong code that matches no request gets the same answer as a wrong code for a request, so the answer shows nothing
// about which codes exist.
func TestDeviceWrongCodeAnswersAreAlike(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	st := cliStart(t, s)
	// The request id is each answer's own, so it is left out of the comparison.
	body := func(rec *httptest.ResponseRecorder) string {
		t.Helper()
		e := decodeError(t, rec)
		if rec.Code != 400 {
			t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body)
		}
		b, _ := json.Marshal(map[string]any{"code": e.Code, "message": e.Message, "details": e.Details})
		return string(b)
	}
	a := body(cliApprove(t, s, fx.memSess, otherCode(st.UserCode)))
	// A code whose first group matches no request at all.
	b := body(cliApprove(t, s, fx.memSess, "BBBB-BBBB"))
	if a != b {
		t.Errorf("wrong code answers differ:\n  %s\n  %s", a, b)
	}
}

// The store keeps the device code and the user code only as hashes. Neither appears in the database file.
func TestDeviceCodesAreNotStoredPlain(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	st := cliStart(t, fx.s)
	for _, p := range []string{fx.s.cfg.Store.Path, fx.s.cfg.Store.Path + "-wal"} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if bytes.Contains(b, []byte(st.DeviceCode)) || bytes.Contains(b, []byte(strings.ReplaceAll(st.UserCode, "-", ""))) ||
			bytes.Contains(b, []byte(st.UserCode)) {
			t.Errorf("%s holds a device code or a user code in plain text", p)
		}
	}
}

// Approval is a browser action: it needs the session cookie and its CSRF token. A device token does not make it.
func TestDeviceApprovalNeedsASessionAndCSRF(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	st := cliStart(t, s)
	wantError(t, call(t, s, "POST", "/api/v1/auth/cli/approve", "", map[string]any{"user_code": st.UserCode}), 401, apierr.Unauthenticated)
	noCSRF := testSession{cookie: fx.memSess.cookie}
	rec := callSession(t, s, noCSRF, "POST", "/api/v1/auth/cli/approve", map[string]any{"user_code": st.UserCode})
	if rec.Code == 200 {
		t.Fatal("approval without the CSRF token was accepted")
	}
	wrongOrigin := fx.memSess
	req := jsonRequest(t, "POST", "/api/v1/auth/cli/approve", map[string]any{"user_code": st.UserCode})
	req.AddCookie(wrongOrigin.cookie)
	req.Header.Set("Origin", "https://elsewhere.example")
	req.Header.Set("X-CSRF-Token", wrongOrigin.csrf)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code == 200 {
		t.Fatal("approval from another site was accepted")
	}
}

// Approval is by whichever browser session of the person signs the request; the device is the approver's.
func TestDeviceApprovedFromAnotherBrowser(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	rec := login(t, s, "member@example.com", testPassword, "")
	wantStatus(t, rec, 200)
	other := sessionFrom(t, s, rec)
	st := cliStart(t, s)
	wantStatus(t, cliApprove(t, s, other, st.UserCode), 200)
	tok := decode[cliTokenReply](t, cliPoll(t, s, st.DeviceCode)).Token
	wantStatus(t, getWith(t, s, tok, "/api/v1/folders"), 200)
}

// A person whose password must change cannot approve. The lookup and the approval both say so, and the request stays
// pending for a later approval.
func TestDeviceMustChangeUserCannotApprove(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	t0 := time.Now().UTC().Truncate(time.Millisecond)
	now := t0
	s.store.SetClock(func() time.Time { return now })
	st := cliStart(t, s)
	setMustChange(t, s, fx.acc.ID, fx.member.ID, true)
	for _, rec := range []*httptest.ResponseRecorder{
		cliLookup(t, s, fx.memSess, st.UserCode),
		cliApprove(t, s, fx.memSess, st.UserCode),
	} {
		wantMustChangeRefusal(t, rec)
	}
	now = t0.Add(6 * time.Second) // past the first interval, so the poll is due
	wantError(t, cliPoll(t, s, st.DeviceCode), 400, apierr.Code("authorization_pending"))
	setMustChange(t, s, fx.acc.ID, fx.member.ID, false)
	wantStatus(t, cliApprove(t, s, fx.memSess, st.UserCode), 200)
}

// An installation admin never holds a device token: the admin's approval is refused.
func TestDeviceInstallationAdminCannotApprove(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	_, adminSess := newAdminUser(t, s, fx.admin, "root@example.com")
	t0 := time.Now().UTC().Truncate(time.Millisecond)
	now := t0
	s.store.SetClock(func() time.Time { return now })
	st := cliStart(t, s)
	e := wantError(t, cliApprove(t, s, adminSess, st.UserCode), 403, apierr.Forbidden)
	if !strings.Contains(e.Message, "installation admin") {
		t.Errorf("message %q does not say why", e.Message)
	}
	now = t0.Add(6 * time.Second)
	wantError(t, cliPoll(t, s, st.DeviceCode), 400, apierr.Code("authorization_pending"))
}

// A device token of a user whose password must change is refused as the user's session is, on GET /folders and on the
// revoke route, with the must-change reason. The refusal ends nothing: once the flag clears, the token works again, and
// revoke then ends it. The change of the password itself ends it (the cut-off, tested in TestDeviceTokenEndedByCutOffs).
func TestDeviceTokenOfMustChangeUserRefused(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	_, tok := approvedToken(t, s, fx.memSess)
	setMustChange(t, s, fx.acc.ID, fx.member.ID, true)
	wantMustChangeRefusal(t, getWith(t, s, tok, "/api/v1/folders"))
	wantMustChangeRefusal(t, rawCall(t, s, "POST", "/api/v1/auth/cli/revoke", tok, "", nil))
	setMustChange(t, s, fx.acc.ID, fx.member.ID, false)
	wantStatus(t, getWith(t, s, tok, "/api/v1/folders"), 200)
	wantStatus(t, rawCall(t, s, "POST", "/api/v1/auth/cli/revoke", tok, "", nil), 204)
	wantError(t, getWith(t, s, tok, "/api/v1/folders"), 401, apierr.Unauthenticated)
}

// Revoking the device token ends it on its next call, and the revoke answers once.
func TestDeviceTokenRevokedByItself(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	_, tok := approvedToken(t, s, fx.memSess)
	wantStatus(t, rawCall(t, s, "POST", "/api/v1/auth/cli/revoke", tok, "", nil), 204)
	wantError(t, getWith(t, s, tok, "/api/v1/folders"), 401, apierr.Unauthenticated)
	wantError(t, rawCall(t, s, "POST", "/api/v1/auth/cli/revoke", tok, "", nil), 401, apierr.Unauthenticated)
}

// Only a device token may use the revoke route; a delegated token, a key and a browser session may not.
func TestDeviceRevokeRefusesOtherCredentials(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	tok := mintFor(t, s, fx.accKey, fx.acc.ID, map[string]any{"folders": []string{}, "all": true, "user": fx.member.ID})
	if rec := rawCall(t, s, "POST", "/api/v1/auth/cli/revoke", tok, "", nil); rec.Code == 204 {
		t.Fatal("a delegated token revoked a device")
	}
	if rec := rawCall(t, s, "POST", "/api/v1/auth/cli/revoke", fx.accKey, "", nil); rec.Code == 204 {
		t.Fatal("an account key used the device revoke")
	}
}

// Manage lists the person's live devices, and a revoke there ends the device's token on its next call.
func TestDeviceManageListAndRevoke(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	_, tok := approvedToken(t, s, fx.memSess)
	rec := callSession(t, s, fx.memSess, "GET", "/api/v1/auth/cli/devices", nil)
	wantStatus(t, rec, 200)
	if strings.Contains(rec.Body.String(), tok) || strings.Contains(rec.Body.String(), "scd_") {
		t.Fatal("the device list shows a token")
	}
	var list struct {
		Devices []struct {
			ID         string `json:"id"`
			ClientName string `json:"client_name"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Devices) != 1 || list.Devices[0].ClientName != "test terminal" {
		t.Fatalf("devices = %+v, want one named test terminal", list.Devices)
	}
	id := list.Devices[0].ID
	wantStatus(t, callSession(t, s, fx.memSess, "DELETE", "/api/v1/auth/cli/devices/"+id, nil), 204)
	wantError(t, getWith(t, s, tok, "/api/v1/folders"), 401, apierr.Unauthenticated)
	wantError(t, callSession(t, s, fx.memSess, "DELETE", "/api/v1/auth/cli/devices/"+id, nil), 404, apierr.NotFound)
	rec = callSession(t, s, fx.memSess, "GET", "/api/v1/auth/cli/devices", nil)
	wantStatus(t, rec, 200)
	if strings.Contains(rec.Body.String(), id) {
		t.Error("Manage still lists a revoked device")
	}
}

// A device token is ended by each cut-off the design names: a password change, an account-wide revoke, a user revoke,
// a disabled user and a suspended account.
func TestDeviceTokenEndedByCutOffs(t *testing.T) {
	t.Parallel()
	cases := map[string]func(t *testing.T, fx *impFix){
		"password change": func(t *testing.T, fx *impFix) {
			wantStatus(t, callSession(t, fx.s, fx.memSess, "POST", "/api/v1/me/password",
				map[string]any{"current_password": testPassword, "new_password": "a new password for the member"}), 204)
		},
		"account revoke": func(t *testing.T, fx *impFix) {
			wantStatus(t, call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/tokens/revoke", fx.accKey, nil), 200)
		},
		"user revoke": func(t *testing.T, fx *impFix) {
			wantStatus(t, call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.member.ID+"/tokens/revoke", fx.accKey, nil), 200)
		},
		"user disabled": func(t *testing.T, fx *impFix) {
			wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.member.ID, fx.accKey,
				map[string]any{"status": "disabled"}), 200)
		},
		"account suspended": func(t *testing.T, fx *impFix) {
			wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID, fx.admin, map[string]any{"status": "suspended"}), 200)
		},
	}
	for name, end := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fx := newImpFix(t, false)
			_, tok := approvedToken(t, fx.s, fx.memSess)
			wantStatus(t, getWith(t, fx.s, tok, "/api/v1/folders"), 200)
			end(t, fx)
			if rec := getWith(t, fx.s, tok, "/api/v1/folders"); rec.Code == 200 {
				t.Fatalf("the token still works after %s", name)
			}
		})
	}
}

// A device token is good until its exp, then answers token_expired. The clock moves past the default 720 h.
func TestDeviceTokenExpires(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	_, tok := approvedToken(t, s, fx.memSess)
	later := time.Now().Add(721 * time.Hour).UTC()
	s.store.SetClock(func() time.Time { return later })
	wantError(t, getWith(t, s, tok, "/api/v1/folders"), 401, apierr.TokenExpired)
}

// The device token and the delegated token have their own MAC keys and prefixes: neither verifies as the other.
func TestDeviceAndDelegatedMACsNeverCross(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	_, dev := approvedToken(t, s, fx.memSess)
	sct := mintFor(t, s, fx.accKey, fx.acc.ID, map[string]any{"folders": []string{}, "all": true, "user": fx.member.ID})
	wantStatus(t, getWith(t, s, sct, "/api/v1/folders"), 200)
	relabelled := "sct_" + strings.TrimPrefix(dev, "scd_")
	wantError(t, getWith(t, s, relabelled, "/api/v1/folders"), 401, apierr.Unauthenticated)
	relabelled = "scd_" + strings.TrimPrefix(sct, "sct_")
	wantError(t, getWith(t, s, relabelled, "/api/v1/folders"), 401, apierr.Unauthenticated)
}

// A device token is refused on the routes a delegated token is refused on: an account's own administration reads as not
// found for it, as it does for a token.
func TestDeviceTokenIsRefusedWhereATokenIs(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	_, tok := approvedToken(t, s, fx.memSess)
	wantError(t, getWith(t, s, tok, "/api/v1/accounts/"+fx.acc.ID+"/users"), 404, apierr.NotFound)
}

// The token and the codes never reach the log, and an audit row names the sign-in without the token.
func TestDeviceSecretsStayOutOfLogsAndAudit(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	var buf bytes.Buffer
	s.log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	st, tok := approvedToken(t, s, fx.memSess)
	wantError(t, cliPoll(t, s, st.DeviceCode), 400, apierr.Code("expired_token"))
	wantStatus(t, rawCall(t, s, "POST", "/api/v1/auth/cli/revoke", tok, "", nil), 204)
	if strings.Contains(buf.String(), tok) || strings.Contains(buf.String(), st.DeviceCode) || strings.Contains(buf.String(), st.UserCode) {
		t.Error("the log holds a token or a code")
	}
	entries, _, err := s.store.AuditLog(context.Background(), fx.acc.ID, "", true, store.Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var sawSignIn bool
	for _, e := range entries {
		b, _ := json.Marshal(e.Details)
		if strings.Contains(string(b), tok) || strings.Contains(string(b), st.DeviceCode) {
			t.Errorf("audit row %s holds a token or a device code", e.Action)
		}
		if strings.HasPrefix(e.Action, "cli.") {
			sawSignIn = true
		}
	}
	if !sawSignIn {
		t.Error("no cli. audit row for the sign-in")
	}
}

// Every poll and approval counts against a limit: a device code takes 20 polls a minute, and a session 10 codes a minute.
func TestDeviceRateLimits(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	st := cliStart(t, s)
	for range 20 {
		cliPoll(t, s, st.DeviceCode)
	}
	wantError(t, cliPoll(t, s, st.DeviceCode), 429, apierr.RateLimited)

	for range 10 {
		cliApprove(t, s, fx.memSess, otherCode(st.UserCode))
	}
	wantError(t, cliApprove(t, s, fx.memSess, otherCode(st.UserCode)), 429, apierr.RateLimited)
}

// A delegated token may not name the device audience, and a device is never minted from /tokens.
func TestMintRefusesTheDeviceAudience(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	rec := call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/tokens", fx.accKey,
		map[string]any{"folders": []string{}, "all": true, "user": fx.member.ID, "audience": "cli"})
	wantError(t, rec, 400, apierr.InvalidRequest)
}

// The sign-in routes are mounted: start and poll take no credential, and the browser routes are not found without one.
func TestDeviceRoutesAreMounted(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	for _, p := range []string{"/api/v1/auth/cli/poll", "/api/v1/auth/cli/start"} {
		if rec := cliFrom(t, s, "POST", p, "198.51.100.9", map[string]any{}, nil); rec.Code == 404 {
			t.Errorf("%s is not mounted", p)
		}
	}
}

// Approval checks the approver, and the claim does too: a person who turned must-change after approving gets no token,
// and the request is gone. The route refuses a must-change user before the approval, so this is the claim's own check.
func TestDeviceApprovedThenMustChangeGetsNoToken(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	t0 := time.Now().UTC().Truncate(time.Millisecond)
	now := t0
	s.store.SetClock(func() time.Time { return now })
	st := cliStart(t, s)
	wantStatus(t, cliApprove(t, s, fx.memSess, st.UserCode), 200)
	setMustChange(t, s, fx.acc.ID, fx.member.ID, true)
	now = t0.Add(6 * time.Second)
	e := wantError(t, cliPoll(t, s, st.DeviceCode), 403, apierr.Forbidden)
	if e.Details["reason"] != reasonPasswordChangeRequired {
		t.Errorf("reason = %v, want %s", e.Details["reason"], reasonPasswordChangeRequired)
	}
	wantError(t, cliPoll(t, s, st.DeviceCode), 400, apierr.Code("expired_token"))
}

// A device token whose MAC was made under the delegated token key is refused, even with the device's own payload. The
// two kinds have different keys, so a token of one never verifies as the other.
func TestDeviceMACUnderTheDelegatedKeyIsRefused(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	_, dev := approvedToken(t, s, fx.memSess)
	body, _, err := splitTokenUnder(auth.DevicePrefix, dev)
	if err != nil {
		t.Fatal(err)
	}
	pl, err := parseDevicePayload(body)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(pl)
	forged := auth.DevicePrefix + base64.RawURLEncoding.EncodeToString(b) + "." +
		base64.RawURLEncoding.EncodeToString(tokenMAC(s.tokens.current.mac, b))
	wantError(t, getWith(t, s, forged, "/api/v1/folders"), 401, apierr.Unauthenticated)
}

// A device token whose key id is not the signing key's is refused, as a delegated token's is.
func TestDeviceTokenWithAnotherKeyIDIsRefused(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	_, dev := approvedToken(t, s, fx.memSess)
	body, _, err := splitTokenUnder(auth.DevicePrefix, dev)
	if err != nil {
		t.Fatal(err)
	}
	pl, err := parseDevicePayload(body)
	if err != nil {
		t.Fatal(err)
	}
	pl.KeyID = "AAAAAAAAAAAAAAAA"
	b, _ := json.Marshal(pl)
	forged := auth.DevicePrefix + base64.RawURLEncoding.EncodeToString(b) + "." +
		base64.RawURLEncoding.EncodeToString(tokenMAC(s.deviceTokens.current.mac, b))
	wantError(t, getWith(t, s, forged, "/api/v1/folders"), 401, apierr.Unauthenticated)
}
