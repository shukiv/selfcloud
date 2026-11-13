// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
)

const testAdminKey = "sc_admin_TESTKEY_0123456789abcdefghijklmnopqrstuvwxyz"

func adminUser(usr, email, name, status string) map[string]any {
	return map[string]any{
		"id": usr, "external_id": nil, "email": email, "display_name": name, "locale": "en", "role": "admin",
		"status": status, "verified_at": nil, "quota_bytes": nil, "created_at": "2026-11-13T09:00:00Z",
		"updated_at": "2026-11-13T09:00:00Z", "pending_email": nil, "last_sign_in_at": nil,
	}
}

func adminCreated(usr, email, name string, verification map[string]any) map[string]any {
	v := adminUser(usr, email, name, "pending")
	v["verification"] = verification
	return v
}

func TestAdminLs(t *testing.T) {
	isolate(t, testAdminKey)
	f := newFake(t)
	other := id.Derive("usr", "other")
	f.on("GET", adminsPath, page("", adminUser(testUser, "ann@example.com", "Ann", "active"),
		adminUser(other, "bo@example.com", "Bo", "pending")))
	res := f.run(AdminLs)
	wantCode(t, res, 0)
	f.wantCalls("GET " + adminsPath)
	f.wantBearer(testAdminKey)
	want := table([]string{"ID", "EMAIL", "NAME", "STATUS", "VERIFIED", "LAST SIGN-IN", "CREATED"},
		[]string{testUser, "ann@example.com", "Ann", "active", "", "", "2026-11-13T09:00:00Z"},
		[]string{other, "bo@example.com", "Bo", "pending", "", "", "2026-11-13T09:00:00Z"})
	if res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
}

func TestAdminLsJSONIsTheAllowList(t *testing.T) {
	isolate(t, testAdminKey)
	f := newFake(t)
	f.on("GET", adminsPath, page("", adminUser(testUser, "ann@example.com", "Ann", "active")))
	res := f.run(AdminLs, "--json")
	wantCode(t, res, 0)
	for _, key := range []string{`"id"`, `"email"`, `"display_name"`, `"status"`, `"verified_at"`,
		`"last_sign_in_at"`, `"created_at"`} {
		if !strings.Contains(res.stdout, key) {
			t.Errorf("stdout %q lacks %s", res.stdout, key)
		}
	}
	for _, key := range []string{`"external_id"`, `"locale"`, `"role"`, `"quota_bytes"`, `"pending_email"`,
		`"updated_at"`} {
		if strings.Contains(res.stdout, key) {
			t.Errorf("stdout %q shows the field %s", res.stdout, key)
		}
	}
}

func TestAdminLsPagesAndAll(t *testing.T) {
	isolate(t, testAdminKey)
	f := newFake(t)
	other := id.Derive("usr", "other")
	f.on("GET", adminsPath,
		page("c2", adminUser(testUser, "ann@example.com", "Ann", "active")),
		page("c2", adminUser(testUser, "ann@example.com", "Ann", "active")),
		page("", adminUser(other, "bo@example.com", "Bo", "pending")))
	res := f.run(AdminLs)
	wantCode(t, res, 0)
	if !strings.Contains(res.stderr, "--all") || strings.Contains(res.stdout, "bo@example.com") {
		t.Fatalf("first page only, with a note: stdout %q stderr %q", res.stdout, res.stderr)
	}
	res = f.run(AdminLs, "--all")
	wantCode(t, res, 0)
	f.wantCalls("GET "+adminsPath, "GET "+adminsPath, "GET "+adminsPath+"?cursor=c2")
	if !strings.Contains(res.stdout, "bo@example.com") {
		t.Fatalf("stdout %q", res.stdout)
	}
	wantCode(t, f.run(AdminLs, "extra"), 2)
}

func TestAdminLsRefusalHintsAtTheAdminKey(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", adminsPath, apiErr(http.StatusForbidden, "forbidden", "installation admins only"))
	res := f.run(AdminLs)
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "installation admin key") {
		t.Fatalf("stderr %q", res.stderr)
	}
	f.set("GET", adminsPath, apiErr(http.StatusForbidden, "forbidden", "installation admins only"))
	res = f.run(AdminLs, "--json")
	wantCode(t, res, 3)
	if strings.Contains(res.stderr, "installation admin key") {
		t.Fatalf("a hint under --json: %q", res.stderr)
	}
}

func TestAdminAddWithALinkShowsItOnce(t *testing.T) {
	isolate(t, testAdminKey)
	f := newFake(t)
	link := "https://selfcloud.example/set-password#secret"
	f.on("POST", adminsPath, doc(http.StatusCreated, adminCreated(testUser, "ann@example.com", "Ann",
		map[string]any{"method": "link", "url": link, "token": "sck_secret_value",
			"expires_at": "2026-11-13T10:00:00Z"})))
	res := f.run(AdminAdd, "--verification", "link", "--name", "Ann", "ann@example.com")
	wantCode(t, res, 0)
	f.wantCalls("POST " + adminsPath)
	f.wantBearer(testAdminKey)
	body := f.bodyOf(0)
	if len(body) != 3 || body["email"] != "ann@example.com" || body["verification"] != "link" || body["display_name"] != "Ann" {
		t.Fatalf("body %v", body)
	}
	for _, want := range []string{"id=" + testUser, "email=ann@example.com", "display_name=Ann",
		"status=pending", "verification=link", "link=" + link, "link_expires_at=2026-11-13T10:00:00Z"} {
		if !hasLine(res.stdout, want) {
			t.Errorf("stdout %q lacks %q", res.stdout, want)
		}
	}
	if strings.Contains(res.stdout+res.stderr, "sck_secret_value") {
		t.Fatalf("a token was printed: stdout %q stderr %q", res.stdout, res.stderr)
	}
	if !strings.Contains(res.stderr, "shown only now") {
		t.Fatalf("stderr %q lacks the note", res.stderr)
	}
}

func TestAdminAddWithEmailSendsTheCode(t *testing.T) {
	isolate(t, testAdminKey)
	f := newFake(t)
	sent := true
	f.on("POST", adminsPath, doc(http.StatusCreated, adminCreated(testUser, "ann@example.com", "",
		map[string]any{"method": "email", "sent": sent})))
	res := f.run(AdminAdd, "--verification", "email", "ann@example.com")
	wantCode(t, res, 0)
	if body := f.bodyOf(0); len(body) != 2 || body["email"] != "ann@example.com" || body["verification"] != "email" {
		t.Fatalf("body %v, want only the email and the method", body)
	}
	for _, want := range []string{"verification=email", "sent=true"} {
		if !hasLine(res.stdout, want) {
			t.Errorf("stdout %q lacks %q", res.stdout, want)
		}
	}
	if strings.Contains(res.stdout, "link=") || strings.Contains(res.stderr, "shown only now") {
		t.Fatalf("an emailed admin has no link: stdout %q stderr %q", res.stdout, res.stderr)
	}
}

func TestAdminAddJSONAndEmptyName(t *testing.T) {
	isolate(t, testAdminKey)
	f := newFake(t)
	f.on("POST", adminsPath, doc(http.StatusCreated, adminCreated(testUser, "ann@example.com", "",
		map[string]any{"method": "link", "url": "https://selfcloud.example/x", "token": "sck_secret_value",
			"expires_at": "2026-11-13T10:00:00Z"})))
	res := f.run(AdminAdd, "--verification", "link", "--name=", "ann@example.com", "--json")
	wantCode(t, res, 0)
	if _, ok := f.bodyOf(0)["display_name"]; !ok {
		t.Fatalf("--name= was not sent: %v", f.bodyOf(0))
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &v); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"id", "email", "display_name", "status", "verification"} {
		if _, ok := v[key]; !ok {
			t.Errorf("the document lacks %s: %s", key, res.stdout)
		}
	}
	for _, key := range []string{"external_id", "locale", "role", "verified_at", "created_at"} {
		if _, ok := v[key]; ok {
			t.Errorf("the document shows %s: %s", key, res.stdout)
		}
	}
	verification, ok := v["verification"].(map[string]any)
	if !ok {
		t.Fatalf("verification is not an object: %s", res.stdout)
	}
	for _, key := range []string{"method", "url", "expires_at", "sent"} {
		if _, ok := verification[key]; !ok {
			t.Errorf("verification lacks %s: %s", key, res.stdout)
		}
	}
	if _, ok := verification["token"]; ok {
		t.Fatalf("the verification shows the token: %s", res.stdout)
	}
	if strings.Contains(res.stdout+res.stderr, "sck_secret_value") {
		t.Fatalf("a token was printed: stdout %q stderr %q", res.stdout, res.stderr)
	}
}

func TestAdminAddRefusals(t *testing.T) {
	isolate(t, testAdminKey)
	f := newFake(t)
	for _, args := range [][]string{
		{},
		{"ann@example.com"},
		{"--verification", "none", "ann@example.com"},
		{"--verification", "", "ann@example.com"},
		{"--verification", "link", ""},
		{"--verification", "link", "--", " "},
		{"--verification", "link", "a@example.com", "b@example.com"},
	} {
		wantCode(t, f.run(AdminAdd, args...), 2)
	}
	f.wantNoCalls()
	f.on("POST", adminsPath, apiErr(http.StatusConflict, "conflict", "an admin with this email already exists"))
	res := f.run(AdminAdd, "--verification", "email", "ann@example.com")
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "already exists") {
		t.Fatalf("stderr %q", res.stderr)
	}
}

func TestAdminAddRefusalHintsAtTheAdminKey(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", adminsPath, apiErr(http.StatusForbidden, "forbidden", "installation admins only"))
	res := f.run(AdminAdd, "--verification", "email", "ann@example.com")
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "installation admin key") {
		t.Fatalf("stderr %q", res.stderr)
	}
}

func TestAdminRmWithYes(t *testing.T) {
	isolate(t, testAdminKey)
	f := newFake(t)
	f.on("DELETE", adminsPath+"/"+testUser, done(http.StatusNoContent))
	res := f.run(AdminRm, testUser, "--yes")
	wantCode(t, res, 0)
	f.wantCalls("DELETE " + adminsPath + "/" + testUser)
	f.wantBearer(testAdminKey)
	if !hasLine(res.stdout, "target="+testUser) || !hasLine(res.stdout, "status=done") {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestAdminRmWithoutYesAndNoTerminalRefuses(t *testing.T) {
	isolate(t, testAdminKey)
	f := newFake(t)
	wantCode(t, f.run(AdminRm, testUser), 2)
	f.wantNoCalls()
}

func TestAdminRmAtATerminalTypesTheID(t *testing.T) {
	isolate(t, testAdminKey)
	f := newFake(t)
	f.on("DELETE", adminsPath+"/"+testUser, done(http.StatusNoContent))
	for _, typed := range []string{"\n", "usr_wrong\n"} {
		typeAtTerminal(typed)
		wantCode(t, f.run(AdminRm, testUser), 2)
	}
	typeAtTerminal(testUser + "\n")
	wantCode(t, f.run(AdminRm, testUser), 0)
	f.wantCalls("DELETE " + adminsPath + "/" + testUser)
}

func TestAdminRmRefusals(t *testing.T) {
	isolate(t, testAdminKey)
	f := newFake(t)
	wantCode(t, f.run(AdminRm, testFolder, "--yes"), 2)
	wantCode(t, f.run(AdminRm, "--yes"), 2)
	wantCode(t, f.run(AdminRm, "ann@example.com", "--yes"), 2)
	f.wantNoCalls()
	f.on("DELETE", adminsPath+"/"+testUser, apiErr(http.StatusConflict, "conflict",
		"this is the last active admin and no admin key without an expiry is active"))
	res := f.run(AdminRm, testUser, "--yes")
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "last active admin") {
		t.Fatalf("stderr %q", res.stderr)
	}
	f.set("DELETE", adminsPath+"/"+testUser, apiErr(http.StatusNotFound, "not_found", "no such admin"))
	wantCode(t, f.run(AdminRm, testUser, "--yes"), 4)
}

func TestAdminRmRefusalHintsAtTheAdminKey(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("DELETE", adminsPath+"/"+testUser, apiErr(http.StatusForbidden, "forbidden", "installation admins only"))
	res := f.run(AdminRm, testUser, "--yes")
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "installation admin key") {
		t.Fatalf("stderr %q", res.stderr)
	}
}

func TestAdminRmJSON(t *testing.T) {
	isolate(t, testAdminKey)
	f := newFake(t)
	f.on("DELETE", adminsPath+"/"+testUser, done(http.StatusNoContent))
	res := f.run(AdminRm, testUser, "--yes", "--json")
	wantCode(t, res, 0)
	if !strings.Contains(res.stdout, `"target": "`+testUser+`"`) || !strings.Contains(res.stdout, `"status": "done"`) {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestAdminLsNeverPrintsSecrets(t *testing.T) {
	isolate(t, testAdminKey)
	f := newFake(t)
	item := adminUser(testUser, "ann@example.com", "Ann", "active")
	item["token"] = "sck_secret_value"
	item["hash"] = "hh"
	f.on("GET", adminsPath, page("", item))
	for _, args := range [][]string{nil, {"--json"}} {
		res := f.run(AdminLs, args...)
		wantCode(t, res, 0)
		out := res.stdout + res.stderr
		for _, secret := range []string{"sck_secret_value", `"token"`, `"hash"`, "hh"} {
			if strings.Contains(out, secret) {
				t.Fatalf("args %v printed %q: %q", args, secret, out)
			}
		}
	}
}

func TestAdminErrorBodiesNeverPrintSecrets(t *testing.T) {
	isolate(t, testAdminKey)
	f := newFake(t)
	body := `{"error":{"code":"conflict","message":"the change was refused"},"token":"sck_secret_value","hash":"hh"}`
	f.on("GET", adminsPath, reply{status: http.StatusConflict, body: body})
	f.on("POST", adminsPath, reply{status: http.StatusConflict, body: body})
	f.on("DELETE", adminsPath+"/"+testUser, reply{status: http.StatusConflict, body: body})
	for _, res := range []result{
		f.run(AdminLs),
		f.run(AdminAdd, "--verification", "email", "ann@example.com"),
		f.run(AdminRm, testUser, "--yes"),
	} {
		wantCode(t, res, 3)
		if strings.Contains(res.stdout+res.stderr, "sck_secret_value") || strings.Contains(res.stdout+res.stderr, "hh") {
			t.Fatalf("an error answer printed a secret: stdout %q stderr %q", res.stdout, res.stderr)
		}
	}
}

func TestAdminRefusalsWithAnAdminKeyHaveNoHint(t *testing.T) {
	isolate(t, testAdminKey)
	f := newFake(t)
	forbidden := apiErr(http.StatusForbidden, "forbidden", "installation admins only")

	f.set("GET", adminsPath, forbidden)
	res := f.run(AdminLs)
	wantCode(t, res, 3)
	if strings.Contains(res.stderr, "installation admin key") {
		t.Fatalf("an admin key got the hint: %q", res.stderr)
	}
	f.set("POST", adminsPath, forbidden)
	res = f.run(AdminAdd, "--verification", "email", "ann@example.com")
	wantCode(t, res, 3)
	if strings.Contains(res.stderr, "installation admin key") {
		t.Fatalf("an admin key got the hint: %q", res.stderr)
	}
	f.set("DELETE", adminsPath+"/"+testUser, forbidden)
	res = f.run(AdminRm, testUser, "--yes")
	wantCode(t, res, 3)
	if strings.Contains(res.stderr, "installation admin key") {
		t.Fatalf("an admin key got the hint: %q", res.stderr)
	}
}

func TestAdminNotFoundFromEachRoute(t *testing.T) {
	isolate(t, testAdminKey)
	f := newFake(t)
	f.set("GET", adminsPath, apiErr(http.StatusNotFound, "not_found", "no such thing"))
	wantCode(t, f.run(AdminLs), 4)
	f.set("POST", adminsPath, apiErr(http.StatusNotFound, "not_found", "no such thing"))
	wantCode(t, f.run(AdminAdd, "--verification", "email", "ann@example.com"), 4)
	f.set("DELETE", adminsPath+"/"+testUser, apiErr(http.StatusNotFound, "not_found", "no such admin"))
	wantCode(t, f.run(AdminRm, testUser, "--yes"), 4)
}

func TestAdminAddEmailSentFalse(t *testing.T) {
	isolate(t, testAdminKey)
	f := newFake(t)
	f.on("POST", adminsPath, doc(http.StatusCreated, adminCreated(testUser, "ann@example.com", "",
		map[string]any{"method": "email", "sent": false})))
	res := f.run(AdminAdd, "--verification", "email", "ann@example.com")
	wantCode(t, res, 0)
	if !hasLine(res.stdout, "sent=false") {
		t.Fatalf("stdout %q", res.stdout)
	}
}
