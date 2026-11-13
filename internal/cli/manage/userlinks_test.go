// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func userLinkPath(suffix string) string {
	return "/api/v1/accounts/" + testAcc + "/users/" + testUser + suffix
}

const testLink = "https://cloud.example.test/sign-in#sck_link_value"

func verified(method string, extra map[string]any) map[string]any {
	v := map[string]any{"method": method}
	for k, val := range extra {
		v[k] = val
	}
	return map[string]any{"id": testUser, "email": "person@example.test", "status": "pending", "verification": v}
}

func TestUserVerifyEmail(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", userLinkPath("/verification"), doc(http.StatusOK, verified("email", map[string]any{"sent": true})))
	res := f.runAcc(UserVerify, testUser, "--method", "email")
	wantCode(t, res, 0)
	f.wantCalls("POST " + userLinkPath("/verification"))
	f.wantBearer(testAccountKey)
	if body := f.bodyOf(0); len(body) != 1 || body["method"] != "email" {
		t.Fatalf("body %v, want only the method", body)
	}
	for _, want := range []string{"verification=email", "sent=true"} {
		if !hasLine(res.stdout, want) {
			t.Errorf("stdout %q lacks %q", res.stdout, want)
		}
	}
	if res.stderr != "" {
		t.Errorf("stderr %q, want nothing", res.stderr)
	}
}

func TestUserVerifyLinkShowsTheLinkOnceAndNeverTheToken(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", userLinkPath("/verification"), doc(http.StatusOK, verified("link", map[string]any{
		"url": testLink, "token": "sck_secret_value", "expires_at": "2026-11-13T12:00:00Z",
	})))
	res := f.runAcc(UserVerify, testUser, "--method", "link")
	wantCode(t, res, 0)
	for _, want := range []string{"verification=link", "link=" + testLink, "link_expires_at=2026-11-13T12:00:00Z"} {
		if !hasLine(res.stdout, want) {
			t.Errorf("stdout %q lacks %q", res.stdout, want)
		}
	}
	if !strings.Contains(res.stderr, "the link is shown only now") {
		t.Errorf("stderr %q lacks the note", res.stderr)
	}
	if strings.Contains(res.stderr, testLink) {
		t.Errorf("the link was written to stderr: %q", res.stderr)
	}
	if got := strings.Count(res.stdout, testLink); got != 1 {
		t.Errorf("the link was printed %d times, want once: %q", got, res.stdout)
	}
	if strings.Contains(res.stdout+res.stderr, "sck_secret_value") {
		t.Fatalf("the token was printed: stdout %q, stderr %q", res.stdout, res.stderr)
	}
}

func TestUserVerifyJSONDropsTheTokenAndTheUser(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", userLinkPath("/verification"), doc(http.StatusOK, verified("link", map[string]any{
		"url": testLink, "token": "sck_secret_value", "expires_at": "2026-11-13T12:00:00Z",
	})))
	res := f.runAcc(UserVerify, testUser, "--method", "link", "--json")
	wantCode(t, res, 0)
	for _, want := range []string{`"method": "link"`, `"url": "` + testLink + `"`, `"expires_at": "2026-11-13T12:00:00Z"`} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("stdout %q lacks %q", res.stdout, want)
		}
	}
	for _, unwanted := range []string{"sck_secret_value", `"token"`, `"email"`, `"status"`} {
		if strings.Contains(res.stdout+res.stderr, unwanted) {
			t.Errorf("output contains %q: stdout %q, stderr %q", unwanted, res.stdout, res.stderr)
		}
	}
}

func TestUserVerifyNone(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", userLinkPath("/verification"), doc(http.StatusOK, map[string]any{
		"id": testUser, "email": "person@example.test", "status": "active",
	}))
	res := f.runAcc(UserVerify, testUser, "--method", "none")
	wantCode(t, res, 0)
	if res.stdout != "verification=none\n" {
		t.Fatalf("stdout %q, want verification=none only", res.stdout)
	}
	if res.stderr != "" {
		t.Fatalf("stderr %q, want nothing", res.stderr)
	}
	res = f.runAcc(UserVerify, testUser, "--method", "none", "--json")
	wantCode(t, res, 0)
	if !strings.Contains(res.stdout, `"method": "none"`) || strings.Contains(res.stdout, `"url"`) {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestUserResetPassword(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", userLinkPath("/password-reset"), doc(http.StatusOK, map[string]any{
		"method": "link", "url": testLink, "token": "sck_secret_value", "expires_at": "2026-11-13T12:00:00Z",
	}))
	res := f.runAcc(UserResetPassword, testUser, "--method", "link")
	wantCode(t, res, 0)
	f.wantCalls("POST " + userLinkPath("/password-reset"))
	if body := f.bodyOf(0); len(body) != 1 || body["method"] != "link" {
		t.Fatalf("body %v, want only the method", body)
	}
	for _, want := range []string{"verification=link", "link=" + testLink, "link_expires_at=2026-11-13T12:00:00Z"} {
		if !hasLine(res.stdout, want) {
			t.Errorf("stdout %q lacks %q", res.stdout, want)
		}
	}
	if strings.Contains(res.stderr, testLink) {
		t.Errorf("the link was written to stderr: %q", res.stderr)
	}
	if got := strings.Count(res.stdout, testLink); got != 1 {
		t.Errorf("the link was printed %d times, want once: %q", got, res.stdout)
	}
	if strings.Contains(res.stdout+res.stderr, "sck_secret_value") {
		t.Fatalf("the token was printed: stdout %q, stderr %q", res.stdout, res.stderr)
	}
	res = f.runAcc(UserResetPassword, testUser, "--method", "link", "--json")
	wantCode(t, res, 0)
	for _, want := range []string{`"method": "link"`, `"url": "` + testLink + `"`, `"expires_at": "2026-11-13T12:00:00Z"`} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("stdout %q lacks %q", res.stdout, want)
		}
	}
	if strings.Contains(res.stdout+res.stderr, "sck_secret_value") || strings.Contains(res.stdout, `"token"`) {
		t.Fatalf("the token was printed: stdout %q, stderr %q", res.stdout, res.stderr)
	}
	f.set("POST", userLinkPath("/password-reset"), doc(http.StatusOK, map[string]any{"method": "email", "sent": false}))
	res = f.runAcc(UserResetPassword, testUser, "--method", "email")
	wantCode(t, res, 0)
	for _, want := range []string{"verification=email", "sent=false"} {
		if !hasLine(res.stdout, want) {
			t.Errorf("stdout %q lacks %q", res.stdout, want)
		}
	}
}

func TestUserSignInLink(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", userLinkPath("/sign-in-links"), doc(http.StatusCreated, map[string]any{
		"url": testLink, "token": "sck_secret_value", "expires_at": "2026-11-13T12:00:00Z",
	}))
	res := f.runAcc(UserSignInLink, testUser)
	wantCode(t, res, 0)
	f.wantCalls("POST " + userLinkPath("/sign-in-links"))
	if raw := f.rawBody(0); raw != "{}" {
		t.Fatalf("body %q, want {}", raw)
	}
	for _, want := range []string{"link=" + testLink, "expires_at=2026-11-13T12:00:00Z"} {
		if !hasLine(res.stdout, want) {
			t.Errorf("stdout %q lacks %q", res.stdout, want)
		}
	}
	if !strings.Contains(res.stderr, "the link is shown only now") {
		t.Errorf("stderr %q lacks the note", res.stderr)
	}
	if strings.Contains(res.stderr, testLink) {
		t.Errorf("the link was written to stderr: %q", res.stderr)
	}
	if got := strings.Count(res.stdout, testLink); got != 1 {
		t.Errorf("the link was printed %d times, want once: %q", got, res.stdout)
	}
	if strings.Contains(res.stdout+res.stderr, "sck_secret_value") {
		t.Fatalf("the token was printed: stdout %q, stderr %q", res.stdout, res.stderr)
	}
	res = f.runAcc(UserSignInLink, testUser, "--next", "/files/x", "--json")
	wantCode(t, res, 0)
	if f.bodyOf(1)["next"] != "/files/x" {
		t.Fatalf("next sent %v", f.bodyOf(1)["next"])
	}
	if !strings.Contains(res.stdout, `"url": "`+testLink+`"`) || !strings.Contains(res.stdout, `"expires_at"`) {
		t.Fatalf("stdout %q", res.stdout)
	}
	if strings.Contains(res.stdout+res.stderr, "sck_secret_value") || strings.Contains(res.stdout, `"token"`) {
		t.Fatalf("the token was printed: stdout %q, stderr %q", res.stdout, res.stderr)
	}
}

func TestUserRevokeTokensConfirms(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", userLinkPath("/tokens/revoke"), doc(http.StatusOK, map[string]any{
		"tokens_not_before": "2026-11-13T12:00:00Z",
	}))
	res := f.runAcc(UserRevokeTokens, testUser)
	wantCode(t, res, 2)
	f.wantNoCalls()
	typeAtTerminal(testUser + "\n")
	res = f.runAcc(UserRevokeTokens, testUser)
	wantCode(t, res, 0)
	f.wantCalls("POST " + userLinkPath("/tokens/revoke"))
	if res.stdout != "tokens_not_before=2026-11-13T12:00:00Z\n" {
		t.Fatalf("stdout %q", res.stdout)
	}
	if !strings.Contains(res.stderr, testUser) || !strings.Contains(res.stderr, "end every sign-in") {
		t.Fatalf("question %q", res.stderr)
	}
}

func TestUserRevokeTokensYesAndWrongPhrase(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	typeAtTerminal("no\n")
	res := f.runAcc(UserRevokeTokens, testUser)
	wantCode(t, res, 2)
	f.wantNoCalls()
	f.on("POST", userLinkPath("/tokens/revoke"), doc(http.StatusOK, map[string]any{
		"tokens_not_before": "2026-11-13T12:00:00Z",
	}))
	res = f.runAcc(UserRevokeTokens, testUser, "--yes")
	wantCode(t, res, 0)
	if res.stderr != "" {
		t.Fatalf("stderr %q, want nothing with --yes", res.stderr)
	}
	res = f.runAcc(UserRevokeTokens, testUser, "--yes", "--json")
	wantCode(t, res, 0)
	if !strings.Contains(res.stdout, `"tokens_not_before"`) {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestUserLinksRefuseBeforeAnyRequest(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	cases := []struct {
		fn   func([]string, io.Writer, io.Writer) int
		args []string
	}{
		{UserVerify, []string{testUser}},
		{UserVerify, []string{testUser, "--method", "sms"}},
		{UserVerify, []string{"not-an-id", "--method", "email"}},
		{UserVerify, []string{"--method", "email"}},
		{UserResetPassword, []string{testUser}},
		{UserResetPassword, []string{testUser, "--method", "none"}},
		{UserResetPassword, []string{"not-an-id", "--method", "email"}},
		{UserSignInLink, []string{"not-an-id"}},
		{UserSignInLink, []string{}},
		{UserRevokeTokens, []string{"not-an-id", "--yes"}},
		{UserRevokeTokens, []string{"--yes"}},
	}
	for _, tc := range cases {
		wantCode(t, f.runAcc(tc.fn, tc.args...), 2)
	}
	wantCode(t, f.run(UserVerify, testUser, "--method", "email"), 2)
	wantCode(t, f.run(UserVerify, testUser, "--method", "email", "--account", "not-an-account"), 2)
	wantCode(t, f.run(UserSignInLink, testUser), 2)
	wantCode(t, f.run(UserRevokeTokens, testUser, "--yes"), 2)
	f.wantNoCalls()
}

func TestUserLinksHideTheDetailsOfAnError(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	errReply := reply{status: http.StatusForbidden, body: jsonOf(map[string]any{
		"error": map[string]any{
			"code": "forbidden", "message": "the server refused it",
			"details": map[string]any{"reason": "device_token", "token": "sck_secret_value"},
		},
	})}
	for _, tc := range []struct {
		fn   func([]string, io.Writer, io.Writer) int
		path string
		args []string
	}{
		{UserVerify, "/verification", []string{testUser, "--method", "link"}},
		{UserResetPassword, "/password-reset", []string{testUser, "--method", "link"}},
		{UserSignInLink, "/sign-in-links", []string{testUser}},
	} {
		f.set("POST", userLinkPath(tc.path), errReply)
		res := f.runAcc(tc.fn, tc.args...)
		wantCode(t, res, 3)
		if !strings.Contains(res.stderr, "the server refused it") {
			t.Fatalf("%s: stderr %q lacks the server's message", tc.path, res.stderr)
		}
		if strings.Contains(res.stdout+res.stderr, "sck_secret_value") || strings.Contains(res.stdout+res.stderr, "device_token") {
			t.Fatalf("%s: an error's details were printed: stdout %q, stderr %q", tc.path, res.stdout, res.stderr)
		}
	}
}

func TestUserLinksKeepTheServerErrorAndHideItsDetails(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", userLinkPath("/sign-in-links"), reply{status: http.StatusForbidden, body: jsonOf(map[string]any{
		"error": map[string]any{
			"code": "forbidden", "message": "only the account's own key may create a sign-in link",
			"details": map[string]any{"reason": "device_token", "token": "sck_secret_value"},
		},
	})})
	res := f.runAcc(UserSignInLink, testUser)
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "only the account's own key") {
		t.Fatalf("stderr %q lacks the server's message", res.stderr)
	}
	if strings.Contains(res.stdout+res.stderr, "sck_secret_value") || strings.Contains(res.stdout+res.stderr, "device_token") {
		t.Fatalf("the error's details were printed: stdout %q, stderr %q", res.stdout, res.stderr)
	}
	for _, tc := range []struct {
		reply reply
		code  int
	}{
		{apiErr(http.StatusNotFound, "not_found", "no such user"), 4},
		{apiErr(http.StatusConflict, "conflict", "the user is already verified"), 3},
		{apiErr(http.StatusForbidden, "forbidden", "only an API key may use this method; use email"), 3},
		{apiErr(http.StatusBadRequest, "invalid_request", "verification must be email, link or none"), 2},
	} {
		f.set("POST", userLinkPath("/verification"), tc.reply)
		wantCode(t, f.runAcc(UserVerify, testUser, "--method", "link"), tc.code)
	}
}

func TestUserVerifyRejectsAnAnswerWithoutAVerification(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", userLinkPath("/verification"), reply{status: http.StatusOK, body: `{"id":` + jsonOf(testUser) + `,"verification":"nope"}`})
	res := f.runAcc(UserVerify, testUser, "--method", "email")
	wantCode(t, res, 1)
	if !strings.Contains(res.stderr, "not a user and its verification") {
		t.Fatalf("stderr %q", res.stderr)
	}
}
