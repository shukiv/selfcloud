// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
)

func accUsers() string        { return "/api/v1/accounts/" + testAcc + "/users" }
func accUser(u string) string { return accUsers() + "/" + u }

func userDoc(userID, email, name string) map[string]any {
	return map[string]any{
		"id": userID, "external_id": nil, "email": email, "display_name": name, "locale": "en",
		"role": "member", "status": "active", "verified_at": "2026-11-01T10:00:00Z",
		"quota_bytes": nil, "created_at": "2026-11-01T10:00:00Z", "updated_at": "2026-11-02T10:00:00Z",
		"pending_email": nil, "last_sign_in_at": nil,
	}
}

func userDetailDoc(userID, email, name string) map[string]any {
	d := userDoc(userID, email, name)
	d["effective_quota_bytes"] = nil
	return d
}

func verification(method string, fields map[string]any) map[string]any {
	v := map[string]any{"method": method}
	for k, val := range fields {
		v[k] = val
	}
	return v
}

func TestUserAddSendsOnlyGivenFields(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	created := userDoc(testUser, "ann@example.test", "Ann")
	created["verification"] = verification("email", map[string]any{"sent": true})
	f.on("POST", accUsers(), doc(http.StatusCreated, created))
	res := f.runAcc(UserAdd, "--verification", "email", "--name", "Ann", "--locale", "pt-BR", "ann@example.test")
	wantCode(t, res, 0)
	f.wantCalls("POST " + accUsers())
	f.wantBearer(testAccountKey)
	body := f.bodyOf(0)
	if len(body) != 4 || body["email"] != "ann@example.test" || body["verification"] != "email" ||
		body["display_name"] != "Ann" || body["locale"] != "pt-BR" {
		t.Fatalf("body %v, want email, verification, display_name and locale only", body)
	}
	for _, want := range []string{"id=" + testUser, "email=ann@example.test", "display_name=Ann", "role=member",
		"status=active", "verification=email", "sent=true"} {
		if !hasLine(res.stdout, want) {
			t.Errorf("stdout %q lacks the line %q", res.stdout, want)
		}
	}
}

func TestUserAddNoneAndRepeat(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", accUsers(), doc(http.StatusCreated, userDoc(testUser, "ann@example.test", "Ann")))
	res := f.runAcc(UserAdd, "--verification", "none", "ann@example.test")
	wantCode(t, res, 0)
	if !hasLine(res.stdout, "verification=none") || strings.Contains(res.stdout, "sent=") ||
		strings.Contains(res.stdout, "link=") {
		t.Fatalf("stdout %q, want verification=none and nothing sent", res.stdout)
	}
	if strings.Contains(res.stderr, "already exists") {
		t.Fatalf("stderr %q", res.stderr)
	}
	f.set("POST", accUsers(), doc(http.StatusOK, userDoc(testUser, "ann@example.test", "Ann")))
	res = f.runAcc(UserAdd, "--verification", "none", "--external-id", "crm-1", "ann@example.test")
	wantCode(t, res, 0)
	if !strings.Contains(res.stderr, "this person already exists; nothing was changed") {
		t.Fatalf("stderr %q", res.stderr)
	}
	if strings.Contains(res.stdout, "verification=") {
		t.Fatalf("stdout %q; a repeat changed nothing", res.stdout)
	}
}

func TestUserAddLinkIsShownOnceAndTheTokenNever(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	created := userDoc(testUser, "ann@example.test", "Ann")
	created["verification"] = verification("link", map[string]any{
		"url": "https://cloud.example.test/sign-in#v=1", "token": "vs_secret", "expires_at": "2026-11-03T10:00:00Z",
	})
	f.on("POST", accUsers(), doc(http.StatusCreated, created))
	res := f.runAcc(UserAdd, "--verification", "link", "ann@example.test")
	wantCode(t, res, 0)
	if strings.Contains(res.stdout+res.stderr, "vs_secret") {
		t.Fatalf("the token was printed: stdout %q stderr %q", res.stdout, res.stderr)
	}
	if !hasLine(res.stdout, "verification=link") || !hasLine(res.stdout, "link=https://cloud.example.test/sign-in#v=1") ||
		!hasLine(res.stdout, "link_expires_at=2026-11-03T10:00:00Z") {
		t.Fatalf("stdout %q", res.stdout)
	}
	if !strings.Contains(res.stderr, "the sign-in link is shown only now") {
		t.Fatalf("stderr %q", res.stderr)
	}
	res = f.runAcc(UserAdd, "--verification", "link", "ann@example.test", "--json")
	wantCode(t, res, 0)
	if strings.Contains(res.stdout, "vs_secret") || !strings.Contains(res.stdout, `"url": "https://cloud.example.test/sign-in#v=1"`) {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestUserAddRefusals(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.runAcc(UserAdd, "ann@example.test"), 2)                               // no --verification
	wantCode(t, f.runAcc(UserAdd, "--verification", "sms", "ann@example.test"), 2)      // bad method
	wantCode(t, f.runAcc(UserAdd, "--verification", "none"), 2)                         // no email
	wantCode(t, f.runAcc(UserAdd, "--verification", "none", "a@b.test", "c@d.test"), 2) // two emails
	wantCode(t, f.run(UserAdd, "--verification", "none", "ann@example.test"), 2)        // no account
	f.wantNoCalls()
}

func TestUserLs(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	other := id.Derive("usr", "b")
	f.on("GET", accUsers(), page("", userDoc(testUser, "ann@example.test", "Ann"),
		userDoc(other, "bob@example.test", "Bob")))
	res := f.runAcc(UserLs)
	wantCode(t, res, 0)
	f.wantCalls("GET " + accUsers())
	want := table([]string{"ID", "EMAIL", "NAME", "ROLE", "STATUS", "VERIFIED"},
		[]string{testUser, "ann@example.test", "Ann", "member", "active", "2026-11-01T10:00:00Z"},
		[]string{other, "bob@example.test", "Bob", "member", "active", "2026-11-01T10:00:00Z"})
	if res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
	res = f.runAcc(UserLs, "--json")
	wantCode(t, res, 0)
	var items []map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &items); err != nil || len(items) != 2 {
		t.Fatalf("stdout %q: %v", res.stdout, err)
	}
	if len(items[0]) != 13 {
		t.Fatalf("the view has %d fields, want 13: %v", len(items[0]), items[0])
	}
	for _, k := range []string{"id", "external_id", "email", "display_name", "locale", "role", "status",
		"verified_at", "quota_bytes", "pending_email", "last_sign_in_at", "created_at", "updated_at"} {
		if _, ok := items[0][k]; !ok {
			t.Errorf("the view lacks %q", k)
		}
	}
}

func TestUserLsFilters(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accUsers(), page("", userDoc(testUser, "ann@example.test", "Ann")))
	res := f.runAcc(UserLs, "--email", "ann@example.test", "--external-id", "crm-1")
	wantCode(t, res, 0)
	f.wantCalls("GET " + accUsers() + "?email=ann%40example.test&external_id=crm-1")
}

func TestUserLsAll(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accUsers(), page("c2", userDoc(testUser, "ann@example.test", "Ann")),
		page("", userDoc(id.Derive("usr", "b"), "bob@example.test", "Bob")))
	res := f.runAcc(UserLs)
	wantCode(t, res, 0)
	f.wantCalls("GET " + accUsers())
	if !strings.Contains(res.stderr, "--all") || strings.Contains(res.stdout, "Bob") {
		t.Fatalf("first page only, with a note: stdout %q stderr %q", res.stdout, res.stderr)
	}
	f = newFake(t)
	f.on("GET", accUsers(), page("c2", userDoc(testUser, "ann@example.test", "Ann")),
		page("", userDoc(id.Derive("usr", "b"), "bob@example.test", "Bob")))
	res = f.runAcc(UserLs, "--all")
	wantCode(t, res, 0)
	f.wantCalls("GET "+accUsers(), "GET "+accUsers()+"?cursor=c2")
	if !strings.Contains(res.stdout, "Bob") {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestUserShow(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	d := userDetailDoc(testUser, "ann@example.test", "Ann")
	d["quota_bytes"] = 1073741824
	d["effective_quota_bytes"] = 1073741824
	d["token"] = "sck_secret_value"
	d["hash"] = "hh"
	f.on("GET", accUser(testUser), doc(http.StatusOK, d))
	res := f.runAcc(UserShow, testUser)
	wantCode(t, res, 0)
	f.wantCalls("GET " + accUser(testUser))
	for _, want := range []string{"id=" + testUser, "email=ann@example.test", "display_name=Ann", "locale=en",
		"role=member", "status=active", "verified_at=2026-11-01T10:00:00Z", "quota_bytes=1073741824",
		"pending_email=", "last_sign_in_at=", "effective_quota_bytes=1073741824"} {
		if !hasLine(res.stdout, want) {
			t.Errorf("stdout %q lacks %q", res.stdout, want)
		}
	}
	res = f.runAcc(UserShow, testUser, "--json")
	wantCode(t, res, 0)
	if strings.Contains(res.stdout, "sck_secret_value") || strings.Contains(res.stdout, `"hash"`) {
		t.Fatalf("a field the view does not know was printed: %q", res.stdout)
	}
}

func TestUserUpdateSendsOnlyGivenFlags(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	d := userDetailDoc(testUser, "ann@example.test", "Ann")
	d["display_name"] = "Annie"
	f.on("PATCH", accUser(testUser), doc(http.StatusOK, d))
	res := f.runAcc(UserUpdate, testUser, "--name", "Annie")
	wantCode(t, res, 0)
	f.wantCalls("PATCH " + accUser(testUser))
	if body := f.bodyOf(0); len(body) != 1 || body["display_name"] != "Annie" {
		t.Fatalf("body %v, want only display_name", body)
	}
	if !hasLine(res.stdout, "display_name=Annie") || !hasLine(res.stdout, "effective_quota_bytes=") {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestUserUpdateQuotaAndExternalID(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	d := userDetailDoc(testUser, "ann@example.test", "Ann")
	f.on("PATCH", accUser(testUser), doc(http.StatusOK, d))
	wantCode(t, f.runAcc(UserUpdate, testUser, "--quota", "default"), 0)
	if raw := f.rawBody(0); !strings.Contains(raw, `"quota_bytes":null`) {
		t.Fatalf("body %q, want quota_bytes null", raw)
	}
	wantCode(t, f.runAcc(UserUpdate, testUser, "--quota", "0"), 0)
	if got := f.bodyOf(1)["quota_bytes"]; got != float64(0) {
		t.Fatalf("quota_bytes %v, want 0", got)
	}
	wantCode(t, f.runAcc(UserUpdate, testUser, "--quota", "10GiB"), 0)
	if got := f.bodyOf(2)["quota_bytes"]; got != float64(10737418240) {
		t.Fatalf("quota_bytes %v, want 10GiB", got)
	}
	wantCode(t, f.runAcc(UserUpdate, testUser, "--external-id", ""), 0)
	body := f.bodyOf(3)
	if v, ok := body["external_id"]; !ok || v != "" {
		t.Fatalf("external_id %v (present %v), want \"\"", v, ok)
	}
	wantCode(t, f.runAcc(UserUpdate, testUser, "--quota", "many"), 2)
	wantCode(t, f.runAcc(UserUpdate, testUser), 2)
}

func TestUserUpdateNegativeQuotaRefused(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.runAcc(UserUpdate, testUser, "--quota", "-1GiB"), 2)
	wantCode(t, f.runAcc(UserUpdate, testUser, "--quota", "-1"), 2)
	f.wantNoCalls()
}

func TestUserUpdateStatusDisabledAsks(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	d := userDetailDoc(testUser, "ann@example.test", "Ann")
	d["status"] = "disabled"
	f.on("PATCH", accUser(testUser), doc(http.StatusOK, d))
	wantCode(t, f.runAcc(UserUpdate, testUser, "--status", "disabled"), 2) // no terminal
	f.wantNoCalls()
	typeAtTerminal("\n")
	wantCode(t, f.runAcc(UserUpdate, testUser, "--status", "disabled"), 2) // the wrong answer
	f.wantNoCalls()
	typeAtTerminal(testUser + "\n")
	wantCode(t, f.runAcc(UserUpdate, testUser, "--status", "disabled"), 0)
	if got := f.bodyOf(0)["status"]; got != "disabled" {
		t.Fatalf("status %v", got)
	}
	wantCode(t, f.runAcc(UserUpdate, testUser, "--status", "active", "--yes"), 0)
	if got := f.bodyOf(1)["status"]; got != "active" {
		t.Fatalf("status %v", got)
	}
	wantCode(t, f.runAcc(UserUpdate, testUser, "--status", "asleep"), 2)
}

func TestUserUpdateEmailNeedsVerification(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.runAcc(UserUpdate, testUser, "--email", "new@example.test"), 2)
	wantCode(t, f.runAcc(UserUpdate, testUser, "--verification", "email"), 2)
	wantCode(t, f.runAcc(UserUpdate, testUser, "--email", "new@example.test", "--verification", "none"), 2)
	f.wantNoCalls()
	d := userDetailDoc(testUser, "ann@example.test", "Ann")
	d["pending_email"] = "new@example.test"
	d["verification"] = verification("link", map[string]any{
		"url": "https://cloud.example.test/verify#v=1", "token": "vs_secret",
	})
	f.on("PATCH", accUser(testUser), doc(http.StatusOK, d))
	res := f.runAcc(UserUpdate, testUser, "--email", "new@example.test", "--verification", "link")
	wantCode(t, res, 0)
	body := f.bodyOf(0)
	if body["email"] != "new@example.test" || body["verification"] != "link" {
		t.Fatalf("body %v", body)
	}
	if strings.Contains(res.stdout+res.stderr, "vs_secret") {
		t.Fatalf("the token was printed: %q", res.stdout+res.stderr)
	}
	if !strings.Contains(res.stderr, "the sign-in link is shown only now") ||
		!hasLine(res.stdout, "link=https://cloud.example.test/verify#v=1") {
		t.Fatalf("stdout %q stderr %q", res.stdout, res.stderr)
	}
}

func TestUserRm(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	other := id.Derive("usr", "other")
	f.on("DELETE", accUser(testUser), done(http.StatusNoContent))
	res := f.runAcc(UserRm, testUser, "--yes")
	wantCode(t, res, 0)
	f.wantCalls("DELETE " + accUser(testUser))
	if !hasLine(res.stdout, "target="+testUser) || !hasLine(res.stdout, "status=done") {
		t.Fatalf("stdout %q", res.stdout)
	}
	wantCode(t, f.runAcc(UserRm, testUser, "--transfer-to", other, "--yes"), 0)
	f.wantCalls("DELETE "+accUser(testUser), "DELETE "+accUser(testUser)+"?transfer_to="+other)
	wantCode(t, f.runAcc(UserRm, testUser, "--delete-files", "--yes"), 0)
	f.wantCalls("DELETE "+accUser(testUser), "DELETE "+accUser(testUser)+"?transfer_to="+other,
		"DELETE "+accUser(testUser)+"?delete_files=true")
}

func TestUserRmRefusals(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	other := id.Derive("usr", "other")
	wantCode(t, f.runAcc(UserRm, testUser, "--transfer-to", other, "--delete-files", "--yes"), 2)
	wantCode(t, f.runAcc(UserRm, testUser, "--transfer-to", testUser, "--yes"), 2)
	wantCode(t, f.runAcc(UserRm, testUser, "--transfer-to", "nope", "--yes"), 2)
	wantCode(t, f.runAcc(UserRm, testUser), 2) // no terminal, no --yes
	f.wantNoCalls()
	f.on("DELETE", accUser(testUser), apiErr(http.StatusConflict, "conflict",
		"the person's My files is not empty; give transfer_to or delete_files=true"))
	res := f.runAcc(UserRm, testUser, "--yes")
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "selfcloud: hint: give --transfer-to USER or --delete-files") {
		t.Fatalf("stderr %q", res.stderr)
	}
	res = f.runAcc(UserRm, testUser, "--yes", "--json")
	wantCode(t, res, 3)
	if strings.Contains(res.stderr, "selfcloud: hint:") {
		t.Fatalf("a hint in --json: %q", res.stderr)
	}
}

func TestUserAddConflict(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", accUsers(), apiErr(http.StatusConflict, "conflict",
		"a user with this email already exists in the account"))
	res := f.runAcc(UserAdd, "--verification", "none", "ann@example.test")
	wantCode(t, res, 3)
	f.wantCalls("POST " + accUsers())
}

func TestUserShowErrors(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accUser(testUser),
		apiErr(http.StatusNotFound, "not_found", "no such user"),
		apiErr(http.StatusForbidden, "forbidden", "not allowed"))
	wantCode(t, f.runAcc(UserShow, testUser), 4)
	res := f.runAcc(UserShow, testUser)
	wantCode(t, res, 3)
	if strings.Contains(res.stderr, "selfcloud: hint:") {
		t.Fatalf("an account route got an admin hint: %q", res.stderr)
	}
}

func TestUserUpdateErrors(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PATCH", accUser(testUser),
		apiErr(http.StatusNotFound, "not_found", "no such user"),
		apiErr(http.StatusConflict, "conflict", "the account needs an active owner"))
	wantCode(t, f.runAcc(UserUpdate, testUser, "--name", "Ann"), 4)
	wantCode(t, f.runAcc(UserUpdate, testUser, "--name", "Ann"), 3)
}

func TestUserUpdateEmailMethodSendsNoLink(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	d := userDetailDoc(testUser, "ann@example.test", "Ann")
	d["pending_email"] = "new@example.test"
	d["verification"] = verification("email", map[string]any{"sent": true, "token": "vs_secret"})
	f.on("PATCH", accUser(testUser), doc(http.StatusOK, d))
	res := f.runAcc(UserUpdate, testUser, "--email", "new@example.test", "--verification", "email")
	wantCode(t, res, 0)
	if !hasLine(res.stdout, "verification=email") || !hasLine(res.stdout, "sent=true") {
		t.Fatalf("stdout %q", res.stdout)
	}
	if strings.Contains(res.stdout+res.stderr, "link=") || strings.Contains(res.stdout+res.stderr, "vs_secret") ||
		strings.Contains(res.stderr, "shown only now") {
		t.Fatalf("a mail verification printed a link: stdout %q stderr %q", res.stdout, res.stderr)
	}
}

func TestUserRmConfirmation(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("DELETE", accUser(testUser), done(http.StatusNoContent))
	wantCode(t, f.runAcc(UserRm, testUser), 2) // no terminal
	f.wantNoCalls()
	typeAtTerminal("someone-else\n")
	wantCode(t, f.runAcc(UserRm, testUser), 2) // the wrong answer
	f.wantNoCalls()
	typeAtTerminal(testUser + "\n")
	wantCode(t, f.runAcc(UserRm, testUser), 0)
	f.wantCalls("DELETE " + accUser(testUser))
}

func TestUserBadIDsAndUsageMakeNoRequests(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.runAcc(UserShow, "not-a-user"), 2)
	wantCode(t, f.runAcc(UserUpdate, "not-a-user", "--name", "Ann"), 2)
	wantCode(t, f.runAcc(UserRm, "not-a-user", "--yes"), 2)
	wantCode(t, f.runAcc(UserLs, "unexpected"), 2)
	wantCode(t, f.runAcc(UserUpdate, testUser), 2) // no change
	wantCode(t, f.run(UserShow, testUser), 2)      // no account
	f.wantNoCalls()
}

func TestUserErrorNeverPrintsASecretField(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	body := jsonOf(map[string]any{
		"error": map[string]string{"code": "conflict", "message": "the person's My files is not empty"},
		"token": "sck_secret_value", "hash": "hh",
	})
	f.on("DELETE", accUser(testUser),
		reply{status: http.StatusConflict, body: body}, reply{status: http.StatusConflict, body: body})
	for _, args := range [][]string{{testUser, "--yes"}, {testUser, "--yes", "--json"}} {
		res := f.runAcc(UserRm, args...)
		wantCode(t, res, 3)
		if strings.Contains(res.stdout+res.stderr, "sck_secret_value") {
			t.Fatalf("a secret field was printed: stdout %q stderr %q", res.stdout, res.stderr)
		}
	}
}
