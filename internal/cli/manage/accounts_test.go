// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
)

const (
	testAccountCreated = "2026-11-12T08:00:00Z"
	testAccountUpdated = "2026-11-12T09:00:00Z"
)

func accountDoc(id, name string) map[string]any {
	return map[string]any{
		"id": id, "external_id": nil, "name": name, "status": "active",
		"quota_bytes": 0, "used_bytes": 0, "used_files": 0,
		"created_at": testAccountCreated, "updated_at": testAccountUpdated,
	}
}

func accountWithExternal(id, name, ext string) map[string]any {
	m := accountDoc(id, name)
	if ext != "" {
		m["external_id"] = ext
	}
	return m
}

func TestAccountCreateSendsOnlyTheName(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", accountsRoute(), doc(http.StatusCreated, accountDoc(testAcc, "Sales")))
	res := f.run(AccountCreate, "  Sales  ")
	wantCode(t, res, 0)
	f.wantCalls("POST " + accountsRoute())
	f.wantBearer(testAccountKey)
	if body := f.bodyOf(0); len(body) != 1 || body["name"] != "Sales" {
		t.Fatalf("body %v, want only the trimmed name", body)
	}
	for _, want := range []string{"id=" + testAcc, "name=Sales", "status=active", "quota_bytes=0"} {
		if !hasLine(res.stdout, want) {
			t.Errorf("stdout %q lacks %q", res.stdout, want)
		}
	}
}

func TestAccountCreateExternalIDQuotaAndOwnerLink(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	owner := map[string]any{
		"id": testUser, "email": "owner@example.com", "display_name": "Owner", "status": "pending",
		"verification": map[string]any{
			"method": "link", "url": "https://example.test/verify/abc", "token": "secret-token",
			"expires_at": "2026-11-13T08:00:00Z",
		},
	}
	body := accountWithExternal(testAcc, "Sales", "crm-3")
	body["owner"] = owner
	body["token"] = "sck_secret_value"
	body["hash"] = "hh"
	f.on("POST", accountsRoute(), doc(http.StatusCreated, body))
	res := f.run(AccountCreate, "--external-id", "crm-3", "--quota", "1MiB",
		"--owner-email", "owner@example.com", "--owner-verification", "link",
		"--owner-name", "Owner", "--owner-locale", "pt-BR", "Sales")
	wantCode(t, res, 0)
	got := f.bodyOf(0)
	if got["name"] != "Sales" || got["external_id"] != "crm-3" || got["quota_bytes"] != float64(1<<20) {
		t.Fatalf("body %v", got)
	}
	o, _ := got["owner"].(map[string]any)
	if o["email"] != "owner@example.com" || o["verification"] != "link" ||
		o["display_name"] != "Owner" || o["locale"] != "pt-BR" {
		t.Fatalf("owner %v", o)
	}
	for _, want := range []string{"owner_id=" + testUser, "owner_email=owner@example.com",
		"owner_status=pending", "verification=link", "link=https://example.test/verify/abc",
		"link_expires_at=2026-11-13T08:00:00Z"} {
		if !hasLine(res.stdout, want) {
			t.Errorf("stdout %q lacks %q", res.stdout, want)
		}
	}
	if strings.Contains(res.stdout+res.stderr, "secret-token") ||
		strings.Contains(res.stdout+res.stderr, "sck_secret_value") || strings.Contains(res.stdout+res.stderr, "hh") {
		t.Fatalf("an answer-only field was printed: stdout %q stderr %q", res.stdout, res.stderr)
	}
	if !strings.Contains(res.stderr, "the sign-in link is shown only now") {
		t.Fatalf("stderr %q lacks the link note", res.stderr)
	}
}

func TestAccountCreateOwnerEmailAndJSONOmitsToken(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	body := accountDoc(testAcc, "Sales")
	body["owner"] = map[string]any{
		"id": testUser, "email": "owner@example.com", "display_name": "Owner", "status": "pending",
		"verification": map[string]any{"method": "email", "sent": true, "token": "secret-token"},
	}
	f.on("POST", accountsRoute(), doc(http.StatusCreated, body))
	res := f.run(AccountCreate, "--owner-email", "owner@example.com", "--owner-verification", "email",
		"--json", "Sales")
	wantCode(t, res, 0)
	if strings.Contains(res.stdout, "link=") || strings.Contains(res.stderr, "sign-in link") {
		t.Fatalf("the email method has no link: stdout %q stderr %q", res.stdout, res.stderr)
	}
	if !strings.Contains(res.stdout, `"verification"`) || !strings.Contains(res.stdout, `"sent": true`) {
		t.Fatalf("stdout %q is not the allow-listed object", res.stdout)
	}
	if strings.Contains(res.stdout, "secret-token") {
		t.Fatalf("the token was printed: %q", res.stdout)
	}
}

func TestAccountCreateLinkJSONShowsURLAndNoNote(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	body := accountDoc(testAcc, "Sales")
	body["owner"] = map[string]any{
		"id": testUser, "email": "owner@example.com", "display_name": "Owner", "status": "pending",
		"verification": map[string]any{
			"method": "link", "url": "https://example.test/verify/abc", "token": "secret-token",
			"expires_at": "2026-11-13T08:00:00Z",
		},
	}
	f.on("POST", accountsRoute(), doc(http.StatusCreated, body))
	res := f.run(AccountCreate, "--owner-email", "owner@example.com", "--owner-verification", "link",
		"--json", "Sales")
	wantCode(t, res, 0)
	if !strings.Contains(res.stdout, `"url": "https://example.test/verify/abc"`) {
		t.Fatalf("stdout %q lacks the url", res.stdout)
	}
	if strings.Contains(res.stdout+res.stderr, "secret-token") {
		t.Fatalf("the token was printed: stdout %q stderr %q", res.stdout, res.stderr)
	}
	if strings.Contains(res.stderr, "sign-in link") {
		t.Fatalf("stderr %q has the plain note under --json", res.stderr)
	}
}

func TestAccountCreateRepeatNotesIt(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", accountsRoute(), doc(http.StatusOK, accountDoc(testAcc, "Sales")))
	res := f.run(AccountCreate, "--external-id", "crm-3", "Sales")
	wantCode(t, res, 0)
	if !strings.Contains(res.stderr, "already exists; nothing was changed") {
		t.Fatalf("stderr %q", res.stderr)
	}
}

func TestAccountCreateGivenEmptyExternalIDIsSent(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", accountsRoute(), doc(http.StatusCreated, accountDoc(testAcc, "Sales")))
	res := f.run(AccountCreate, "--external-id", "", "Sales")
	wantCode(t, res, 0)
	if body := f.bodyOf(0); body["external_id"] != "" {
		t.Fatalf("body %v, want an empty external_id", body)
	}
}

func TestAccountCreateRefusals(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	for _, args := range [][]string{
		{}, {" "}, {"a", "b"},
		{"--owner-name", "Owner", "a"},
		{"--owner-email", "o@example.com", "a"},
		{"--owner-verification", "email", "a"},
		{"--owner-email", "", "--owner-verification", "email", "a"},
		{"--owner-verification", "", "a"},
		{"--owner-email", "o@example.com", "--owner-verification", "sms", "a"},
		{"--owner-email", "o@example.com", "--owner-verification", "email", "--owner-name", "O", "--owner-locale", "en", "--", ""},
		{"--quota", "not-a-size", "a"},
		{"--quota", "-1", "a"},
	} {
		wantCode(t, f.run(AccountCreate, args...), 2)
	}
	f.wantNoCalls()
}

func TestAccountLsSendsFilters(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	other := id.Derive("acc", "other")
	sales := accountDoc(testAcc, "Sales")
	sales["user_count"] = float64(3)
	sales["token"] = "sck_secret_value"
	sales["hash"] = "hh"
	f.on("GET", accountsRoute(), page("", sales, accountDoc(other, "Support")))
	res := f.run(AccountLs, "--status", "active", "--external-id", "crm-3", "--search", "sal")
	wantCode(t, res, 0)
	f.wantCalls("GET " + accountsRoute() + "?external_id=crm-3&q=sal&status=active")
	want := table([]string{"ID", "NAME", "STATUS", "QUOTA", "USED", "FILES", "CREATED"},
		[]string{testAcc, "Sales", "active", "0", "0", "0", testAccountCreated},
		[]string{other, "Support", "active", "0", "0", "0", testAccountCreated})
	if res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
	if strings.Contains(res.stdout+res.stderr, "sck_secret_value") || strings.Contains(res.stdout+res.stderr, "hh") {
		t.Fatalf("an answer-only field was printed: %q", res.stdout)
	}
	res = f.run(AccountLs, "--json")
	wantCode(t, res, 0)
	if !strings.HasPrefix(res.stdout, "[") || !strings.Contains(res.stdout, `"name": "Sales"`) {
		t.Fatalf("stdout %q", res.stdout)
	}
	for _, notWant := range []string{"user_count", "sck_secret_value", "hh"} {
		if strings.Contains(res.stdout, notWant) {
			t.Fatalf("stdout %q contains %q", res.stdout, notWant)
		}
	}
}

func TestAccountLsFiltersAndPages(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	other := id.Derive("acc", "other")
	f.on("GET", accountsRoute(), page("c2", accountDoc(testAcc, "Sales")),
		page("c2", accountDoc(testAcc, "Sales")), page("", accountDoc(other, "Support")))
	res := f.run(AccountLs)
	wantCode(t, res, 0)
	if !strings.Contains(res.stderr, "--all") || strings.Contains(res.stdout, "Support") {
		t.Fatalf("first page only, with a note: stdout %q stderr %q", res.stdout, res.stderr)
	}
	res = f.run(AccountLs, "--all")
	wantCode(t, res, 0)
	f.wantCalls("GET "+accountsRoute(), "GET "+accountsRoute(), "GET "+accountsRoute()+"?cursor=c2")
	if !strings.Contains(res.stdout, "Support") {
		t.Fatalf("stdout %q", res.stdout)
	}
	wantCode(t, f.run(AccountLs, "--status", "gone"), 2)
	wantCode(t, f.run(AccountLs, "extra"), 2)
	if len(f.requests()) != 3 {
		t.Fatalf("a refused argument sent a request: %v", f.requests())
	}
}

func TestAccountShow(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	body := accountDoc(testAcc, "Sales")
	body["token"] = "sck_secret_value"
	body["hash"] = "hh"
	body["user_count"] = float64(7)
	f.on("GET", accountRoute(testAcc), doc(http.StatusOK, body))
	res := f.runAcc(AccountShow)
	wantCode(t, res, 0)
	f.wantCalls("GET " + accountRoute(testAcc))
	for _, want := range []string{"id=" + testAcc, "name=Sales", "status=active"} {
		if !hasLine(res.stdout, want) {
			t.Errorf("stdout %q lacks %q", res.stdout, want)
		}
	}
	if strings.Contains(res.stdout+res.stderr, "sck_secret_value") || strings.Contains(res.stdout+res.stderr, "hh") {
		t.Fatalf("an answer-only field was printed: %q", res.stdout)
	}
	res = f.runAcc(AccountShow, "--json")
	wantCode(t, res, 0)
	if !strings.Contains(res.stdout, `"name": "Sales"`) {
		t.Fatalf("stdout %q is not the account view", res.stdout)
	}
	for _, notWant := range []string{"sck_secret_value", "hh", "user_count"} {
		if strings.Contains(res.stdout, notWant) {
			t.Fatalf("stdout %q contains %q", res.stdout, notWant)
		}
	}
	wantCode(t, f.runAcc(AccountShow, "extra"), 2)
	if len(f.requests()) != 2 {
		t.Fatalf("a refused argument sent a request: %v", f.requests())
	}
}

func TestAccountUpdateSendsOnlyGivenFields(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	updated := accountDoc(testAcc, "Renamed")
	updated["token"] = "sck_secret_value"
	updated["hash"] = "hh"
	f.on("PATCH", accountRoute(testAcc), doc(http.StatusOK, updated))
	res := f.runAcc(AccountUpdate, "--name", "  Renamed/Team  ")
	wantCode(t, res, 0)
	f.wantCalls("PATCH " + accountRoute(testAcc))
	if body := f.bodyOf(0); len(body) != 1 || body["name"] != "Renamed/Team" {
		t.Fatalf("body %v, want only the trimmed name", body)
	}
	if !hasLine(res.stdout, "name=Renamed") {
		t.Fatalf("stdout %q", res.stdout)
	}
	res = f.runAcc(AccountUpdate, "--name", "Renamed", "--json")
	wantCode(t, res, 0)
	if !strings.Contains(res.stdout, `"name": "Renamed"`) {
		t.Fatalf("stdout %q is not the account view", res.stdout)
	}
	for _, notWant := range []string{"sck_secret_value", "hh"} {
		if strings.Contains(res.stdout, notWant) {
			t.Fatalf("stdout %q contains %q", res.stdout, notWant)
		}
	}

	f = newFake(t)
	f.on("PATCH", accountRoute(testAcc), doc(http.StatusOK, accountDoc(testAcc, "Sales")))
	res = f.runAcc(AccountUpdate, "--external-id", "", "--quota", "0", "--status", "active")
	wantCode(t, res, 0)
	f.wantCalls("PATCH " + accountRoute(testAcc))
	body := f.bodyOf(0)
	if len(body) != 3 || body["external_id"] != "" || body["quota_bytes"] != float64(0) || body["status"] != "active" {
		t.Fatalf("body %v", body)
	}
}

func TestAccountUpdateSuspendAsksAndRefusesWithoutTerminal(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accountRoute(testAcc), doc(http.StatusOK, accountDoc(testAcc, "Sales")))
	f.on("PATCH", accountRoute(testAcc), doc(http.StatusOK, accountDoc(testAcc, "Sales")))
	wantCode(t, f.runAcc(AccountUpdate, "--status", "suspended"), 2)
	f.wantNoCalls()

	typeAtTerminal("Sales\n")
	res := f.runAcc(AccountUpdate, "--status", "suspended")
	wantCode(t, res, 0)
	f.wantCalls("GET "+accountRoute(testAcc), "PATCH "+accountRoute(testAcc))
	if body := f.bodyOf(1); len(body) != 1 || body["status"] != "suspended" {
		t.Fatalf("body %v", body)
	}

	isolate(t, testAccountKey)
	f = newFake(t)
	f.on("PATCH", accountRoute(testAcc), doc(http.StatusOK, accountDoc(testAcc, "Sales")))
	res = f.runAcc(AccountUpdate, "--status", "suspended", "--yes")
	wantCode(t, res, 0)
	f.wantCalls("PATCH " + accountRoute(testAcc))
}

func TestAccountUpdateRefusals(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	for _, args := range [][]string{
		{}, {"--yes"}, {"--name", "   "}, {"--status", "gone"}, {"--quota", "bad"}, {"--quota", "-1"},
		{"extra"}, {"--name", "ok", "--status", "gone"},
	} {
		wantCode(t, f.runAcc(AccountUpdate, args...), 2)
	}
	wantCode(t, f.run(AccountUpdate, "--name", "ok"), 2)
	f.wantNoCalls()
}

func TestAccountRmWithYes(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	job := "job_" + strings.Repeat("a", 26)
	f.on("DELETE", accountRoute(testAcc), doc(http.StatusAccepted,
		map[string]any{"id": job, "kind": "account.delete", "target": testAcc, "status": "queued",
			"token": "sck_secret_value", "hash": "hh"}))
	res := f.runAcc(AccountRm, "--yes")
	wantCode(t, res, 0)
	f.wantCalls("DELETE " + accountRoute(testAcc))
	if !hasLine(res.stdout, "job_id="+job) || !hasLine(res.stdout, "status=queued") {
		t.Fatalf("stdout %q", res.stdout)
	}
	if strings.Contains(res.stdout+res.stderr, "sck_secret_value") || strings.Contains(res.stdout+res.stderr, "hh") {
		t.Fatalf("an answer-only field was printed: %q", res.stdout)
	}
	if !strings.Contains(res.stderr, "removed in the background") ||
		!strings.Contains(res.stderr, "selfcloud job show "+job) {
		t.Fatalf("stderr %q", res.stderr)
	}
	res = f.runAcc(AccountRm, "--yes", "--json")
	wantCode(t, res, 0)
	if !strings.Contains(res.stdout, `"job_id": "`+job+`"`) {
		t.Fatalf("stdout %q is not the job view", res.stdout)
	}
	for _, notWant := range []string{"sck_secret_value", "hh", `"kind"`, `"target"`} {
		if strings.Contains(res.stdout, notWant) {
			t.Fatalf("stdout %q contains %q", res.stdout, notWant)
		}
	}
}

func TestAccountRmWithoutYesAndNoTerminalRefuses(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.runAcc(AccountRm), 2)
	f.wantNoCalls()
}

func TestAccountRmAtATerminalTypesTheName(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accountRoute(testAcc), doc(http.StatusOK, accountDoc(testAcc, "Sales")))
	job := "job_" + strings.Repeat("b", 26)
	f.on("DELETE", accountRoute(testAcc), doc(http.StatusAccepted,
		map[string]any{"id": job, "status": "queued"}))
	typeAtTerminal("sales\n")
	wantCode(t, f.runAcc(AccountRm), 2)
	for _, c := range f.requests() {
		if strings.HasPrefix(c, "DELETE") {
			t.Fatalf("calls %v: a refused answer removed the account", f.requests())
		}
	}
	typeAtTerminal("Sales\n")
	res := f.runAcc(AccountRm)
	wantCode(t, res, 0)
	f.wantCalls("GET "+accountRoute(testAcc), "GET "+accountRoute(testAcc), "DELETE "+accountRoute(testAcc))
}

func TestAccountUsagePrintsEveryField(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accountRoute(testAcc)+"/usage", doc(http.StatusOK, map[string]any{
		"quota_bytes": nil, "used_bytes": 10, "reserved_bytes": 2, "available_bytes": nil,
		"files": 3, "folders": 1, "used_files": 3, "users_active": 2, "users_pending": 1,
		"search_text_bytes": 7, "search_text_cap": 100, "search_text_skipped": 4,
		"token": "sck_secret_value", "hash": "hh",
	}))
	res := f.runAcc(AccountUsage)
	wantCode(t, res, 0)
	f.wantCalls("GET " + accountRoute(testAcc) + "/usage")
	want := "quota_bytes=\nused_bytes=10\nreserved_bytes=2\navailable_bytes=\nfiles=3\nfolders=1\n" +
		"used_files=3\nusers_active=2\nusers_pending=1\nsearch_text_bytes=7\nsearch_text_cap=100\n" +
		"search_text_skipped=4\n"
	if res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
	res = f.runAcc(AccountUsage, "--json")
	wantCode(t, res, 0)
	if !strings.Contains(res.stdout, `"quota_bytes": null`) || !strings.Contains(res.stdout, `"used_bytes": 10`) {
		t.Fatalf("stdout %q is not the usage view", res.stdout)
	}
	for _, notWant := range []string{"sck_secret_value", "hh"} {
		if strings.Contains(res.stdout, notWant) {
			t.Fatalf("stdout %q contains %q", res.stdout, notWant)
		}
	}
}

func TestAccountAuditSendsFiltersAndPrintsRows(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	entry := func(action string) map[string]any {
		return map[string]any{
			"id": "1", "time": "2026-11-12T08:00:00Z",
			"actor":  map[string]any{"kind": "user", "id": testUser},
			"action": action, "target": testFolder, "ip": "127.0.0.1", "outcome": "ok",
			"user_agent": "cli", "details": map[string]any{"secret": "not shown"},
			"token": "sck_secret_value", "hash": "hh",
		}
	}
	f.on("GET", accountRoute(testAcc)+"/audit", page("", entry("file.read"), entry("file.write")))
	res := f.runAcc(AccountAudit, "--action", "file.read", "--actor", testUser)
	wantCode(t, res, 0)
	f.wantCalls("GET " + accountRoute(testAcc) + "/audit?action=file.read&actor=" + testUser)
	want := table([]string{"TIME", "ACTOR", "ACTION", "TARGET", "OUTCOME", "IP"},
		[]string{"2026-11-12T08:00:00Z", "user:" + testUser, "file.read", testFolder, "ok", "127.0.0.1"},
		[]string{"2026-11-12T08:00:00Z", "user:" + testUser, "file.write", testFolder, "ok", "127.0.0.1"})
	if res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
	if strings.Contains(res.stdout, "not shown") {
		t.Fatalf("the details were printed: %q", res.stdout)
	}
	res = f.runAcc(AccountAudit, "--json")
	wantCode(t, res, 0)
	if !strings.HasPrefix(res.stdout, "[") || !strings.Contains(res.stdout, `"action": "file.read"`) {
		t.Fatalf("stdout %q", res.stdout)
	}
	for _, want := range []string{`"id": "1"`, `"user_agent": "cli"`} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("stdout %q lacks %q", res.stdout, want)
		}
	}
	for _, notWant := range []string{"details", "not shown", "sck_secret_value", "hh"} {
		if strings.Contains(res.stdout, notWant) {
			t.Fatalf("stdout %q contains %q", res.stdout, notWant)
		}
	}
}

func TestAccountAuditPagesAndRefusals(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	entry := func(action string) map[string]any {
		return map[string]any{"id": "1", "time": "2026-11-12T08:00:00Z",
			"actor": map[string]any{"kind": "system", "id": "job_1"}, "action": action,
			"target": testFolder, "ip": "", "outcome": "ok"}
	}
	f.on("GET", accountRoute(testAcc)+"/audit", page("c2", entry("a")), page("c2", entry("a")),
		page("", entry("b")))
	res := f.runAcc(AccountAudit)
	wantCode(t, res, 0)
	if !strings.Contains(res.stderr, "--all") {
		t.Fatalf("stderr %q", res.stderr)
	}
	wantCode(t, f.runAcc(AccountAudit, "--all"), 0)
	f.wantCalls("GET "+accountRoute(testAcc)+"/audit", "GET "+accountRoute(testAcc)+"/audit",
		"GET "+accountRoute(testAcc)+"/audit?cursor=c2")
	long := strings.Repeat("a", 101)
	wantCode(t, f.runAcc(AccountAudit, "--action", long), 2)
	wantCode(t, f.runAcc(AccountAudit, "--actor", long), 2)
	wantCode(t, f.runAcc(AccountAudit, "extra"), 2)
	if len(f.requests()) != 3 {
		t.Fatalf("a refused argument sent a request: %v", f.requests())
	}
}

func TestAccountAuditCountsCharactersNotBytes(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	entry := map[string]any{"id": "1", "time": "2026-11-12T08:00:00Z",
		"actor": map[string]any{"kind": "system", "id": "job_1"}, "action": "a",
		"target": testFolder, "ip": "", "outcome": "ok"}
	f.on("GET", accountRoute(testAcc)+"/audit", page("", entry))
	hundred := strings.Repeat("я", 100)
	wantCode(t, f.runAcc(AccountAudit, "--action", hundred), 0)
	wantCode(t, f.runAcc(AccountAudit, "--actor", hundred), 0)
	wantCode(t, f.runAcc(AccountAudit, "--action", hundred+"я"), 2)
	wantCode(t, f.runAcc(AccountAudit, "--actor", hundred+"я"), 2)
	if len(f.requests()) != 2 {
		t.Fatalf("a refused argument sent a request: %v", f.requests())
	}
}

func TestAccountErrorAnswers(t *testing.T) {
	cases := []struct {
		name    string
		run     func([]string, io.Writer, io.Writer) int
		account bool
		args    []string
		method  string
		route   string
		status  int
		want    int
		hint    bool
	}{
		{"create conflict", AccountCreate, false, []string{"Sales"}, "POST", accountsRoute(),
			http.StatusConflict, 3, false},
		{"ls forbidden", AccountLs, false, nil, "GET", accountsRoute(), http.StatusForbidden, 3, true},
		{"show not found", AccountShow, true, nil, "GET", accountRoute(testAcc), http.StatusNotFound, 4, false},
		{"update forbidden", AccountUpdate, true, []string{"--name", "New"}, "PATCH", accountRoute(testAcc),
			http.StatusForbidden, 3, true},
		{"rm conflict", AccountRm, true, []string{"--yes"}, "DELETE", accountRoute(testAcc),
			http.StatusConflict, 3, false},
		{"usage forbidden", AccountUsage, true, nil, "GET", accountRoute(testAcc) + "/usage",
			http.StatusForbidden, 3, false},
		{"audit not found", AccountAudit, true, nil, "GET", accountRoute(testAcc) + "/audit",
			http.StatusNotFound, 4, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t, testAccountKey)
			f := newFake(t)
			code := "forbidden"
			if tc.status == http.StatusNotFound {
				code = "not_found"
			} else if tc.status == http.StatusConflict {
				code = "conflict"
			}
			f.on(tc.method, tc.route, apiErr(tc.status, code, "the server said no"))
			var res result
			if tc.account {
				res = f.runAcc(tc.run, tc.args...)
			} else {
				res = f.run(tc.run, tc.args...)
			}
			wantCode(t, res, tc.want)
			if got := strings.Contains(res.stderr, "this needs an installation admin key"); got != tc.hint {
				t.Fatalf("hint %v, want %v; stderr %q", got, tc.hint, res.stderr)
			}
		})
	}
}

func TestAccountErrorAnswerDoesNotPrintSecretFields(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", accountsRoute(), reply{status: http.StatusConflict, body: jsonOf(map[string]any{
		"error": map[string]any{"code": "conflict", "message": "the server said no",
			"details": map[string]any{"token": "sck_secret_value", "hash": "hh"}},
	})})
	res := f.run(AccountCreate, "Sales")
	wantCode(t, res, 3)
	for _, notWant := range []string{"sck_secret_value", "hh"} {
		if strings.Contains(res.stdout+res.stderr, notWant) {
			t.Fatalf("stdout %q stderr %q contains %q", res.stdout, res.stderr, notWant)
		}
	}
}

func TestAccountAdminRouteNeedsAnAdminKeyHint(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", accountsRoute(), apiErr(http.StatusForbidden, "forbidden", "an installation admin is required"))
	res := f.run(AccountCreate, "Sales")
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "this needs an installation admin key") {
		t.Fatalf("stderr %q lacks the admin hint", res.stderr)
	}
	res = f.run(AccountCreate, "Sales", "--json")
	wantCode(t, res, 3)
	if strings.Contains(res.stderr, "hint") || !strings.HasPrefix(res.stderr, `{"error"`) {
		t.Fatalf("--json stderr %q: want only the JSON error", res.stderr)
	}
	isolate(t, "sc_admin_TESTKEY_0123456789abcdefghijklmnopqrstuvwxyz")
	res = f.run(AccountCreate, "Sales")
	wantCode(t, res, 3)
	if strings.Contains(res.stderr, "this needs an installation admin key") {
		t.Fatalf("an admin key got the hint: stderr %q", res.stderr)
	}
}
