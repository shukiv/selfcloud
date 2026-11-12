// SPDX-License-Identifier: AGPL-3.0-or-later

package sharing

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var shareURL = "https://drive.example/s#" + testSecret

func linkDoc(linkID, audience, role string) map[string]any {
	return map[string]any{"id": linkID, "node_id": testNode, "audience": audience, "role": role, "expires_at": "2026-12-01T00:00:00Z",
		"created_at": "2026-11-01T10:00:00Z", "inherited_from": nil, "upload_only": false, "title": "", "message": "",
		"ask_name": false, "notify": false, "password_set": audience == "anyone"}
}

func createdLink(audience, role string) reply {
	return created(map[string]any{"link": linkDoc(testLink, audience, role), "url": shareURL})
}

func passwordFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(p, []byte(testPassword+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func sharePath(nodeID string) string { return nodeRoute(nodeID, "/share-links") }

func TestLinkCreatePrintsTheLinkOnce(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("POST", sharePath(testNode), createdLink("organisation", "viewer"))
	res := f.run(LinkCreate, testNode)
	wantCode(t, res, 0)
	f.wantCalls("POST " + sharePath(testNode))
	if got := f.body(0); got["audience"] != "organisation" || len(got) != 1 {
		t.Fatalf("body %v: the audience is organisation unless the person says otherwise, and nothing else is sent", got)
	}
	want := "id=" + testLink + "\nnode_id=" + testNode + "\naudience=organisation\nrole=viewer\nexpires_at=2026-12-01T00:00:00Z\n" +
		"url=" + shareURL + "\n"
	if res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
	if n := strings.Count(res.stdout, testSecret); n != 1 {
		t.Fatalf("the secret is in stdout %d times, want once", n)
	}
	if strings.Contains(res.stderr, testSecret) {
		t.Fatalf("the secret reached stderr: %q", res.stderr)
	}
}

func TestLinkCreateJSONHoldsTheURLOnce(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("POST", sharePath(testNode), createdLink("organisation", "viewer"))
	res := f.run(LinkCreate, testNode, "--json")
	wantCode(t, res, 0)
	if n := strings.Count(res.stdout, testSecret); n != 1 || !strings.Contains(res.stdout, `"url": "`+shareURL+`"`) {
		t.Fatalf("stdout %q: want the API's document, with the URL once", res.stdout)
	}
	if strings.Contains(res.stderr, testSecret) {
		t.Fatalf("the secret reached stderr: %q", res.stderr)
	}
}

func TestLinkCreateWithAPasswordFromAFile(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("POST", sharePath(testNode), createdLink("anyone", "viewer"))
	res := f.run(LinkCreate, testNode, "--audience", "anyone", "--expires-in", "30d", "--password-file", passwordFile(t))
	wantCode(t, res, 0)
	got := f.body(0)
	if got["audience"] != "anyone" || got["password"] != testPassword || got["expires_at"] == nil {
		t.Fatalf("body %v", got)
	}
	if !strings.Contains(res.stdout, "password_set=true\n") {
		t.Fatalf("stdout %q should say a password is set, not what it is", res.stdout)
	}
}

func TestLinkCreateWithAPasswordFromStandardInput(t *testing.T) {
	isolate(t, testKey)
	pipeIn(testPassword + "\n")
	f := newFake(t)
	f.on("POST", sharePath(testNode), createdLink("anyone", "viewer"))
	wantCode(t, f.run(LinkCreate, testNode, "--audience", "anyone", "--password-file", "-"), 0)
	if f.body(0)["password"] != testPassword {
		t.Fatalf("body %v", f.body(0))
	}
}

func TestLinkCreateRefusesAPasswordFromATerminal(t *testing.T) {
	isolate(t, testKey)
	typeAtTerminal(testPassword + "\n")
	f := newFake(t)
	wantCode(t, f.run(LinkCreate, testNode, "--audience", "anyone", "--password-file", "-"), 2)
	f.wantNoCalls()
}

// TestThereIsNoPasswordFlag: a password given on the command line is refused, and the refusal does not repeat it.
func TestThereIsNoPasswordFlag(t *testing.T) {
	for _, args := range [][]string{
		{testNode, "--audience", "anyone", "--password=" + testPassword},
		{testNode, "--audience", "anyone", "--password", testPassword},
		{testNode, "--audience", "anyone", "-password", testPassword},
	} {
		isolate(t, testKey)
		f := newFake(t)
		res := f.run(LinkCreate, args...)
		wantCode(t, res, 2)
		f.wantNoCalls()
	}
	isolate(t, testKey)
	f := newFake(t)
	wantCode(t, f.run(LinkUpdate, testLink, "--password="+testPassword), 2)
	f.wantNoCalls()
}

// TestAServerErrorNeverRepeatsThePasswordOrTheSecret: a hostile or careless answer that echoes what it was sent is scrubbed.
func TestAServerErrorNeverRepeatsThePasswordOrTheSecret(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("POST", sharePath(testNode), apiErr(400, "invalid_request", "password "+testPassword+" is too weak; link "+shareURL))
	res := f.run(LinkCreate, testNode, "--audience", "anyone", "--password-file", passwordFile(t))
	wantCode(t, res, 2)
	if strings.Contains(res.stderr, testSecret) {
		t.Fatalf("stderr %q repeats a secret", res.stderr)
	}
	if !strings.Contains(res.stderr, "is too weak") {
		t.Fatalf("stderr %q lost the reason", res.stderr)
	}
	isolate(t, testKey)
	f = newFake(t)
	f.on("POST", sharePath(testNode), apiErr(400, "invalid_request", "password "+testPassword+" is too weak"))
	res = f.run(LinkCreate, testNode, "--audience", "anyone", "--password-file", passwordFile(t), "--json")
	wantCode(t, res, 2)
	if !strings.Contains(res.stderr, "[hidden]") {
		t.Fatalf("stderr %q: the password should be hidden", res.stderr)
	}
}

func TestLinkCreateRoles(t *testing.T) {
	for _, role := range []string{"viewer", "commenter", "contributor", "editor"} {
		isolate(t, testKey)
		f := newFake(t)
		f.on("POST", sharePath(testNode), createdLink("organisation", role))
		wantCode(t, f.run(LinkCreate, testNode, "--role", role), 0)
		if f.body(0)["role"] != role {
			t.Fatalf("%s: body %v", role, f.body(0))
		}
	}
	isolate(t, testKey)
	f := newFake(t)
	for _, bad := range []string{"manager", "owner", "Viewer"} {
		wantCode(t, f.run(LinkCreate, testNode, "--role", bad), 2)
	}
	f.wantNoCalls()
}

func TestLinkCreateRefusalsByRoleAndPolicy(t *testing.T) {
	cases := []struct {
		name  string
		reply reply
		code  int
		say   string
	}{
		{"a viewer may not share", apiErr(403, "forbidden", "you need the manager role"), 3, "manager role"},
		{"an editor may not give a role above its own", apiErr(403, "forbidden", "you may not give a link a role above your own"), 3, "above your own"},
		{"an anyone link needs a manager", apiErrReason(403, "forbidden", "only a manager of the item may make or change a link that anyone can open", "manager_required"), 3, "only a manager"},
		{"public links are off", apiErrReason(403, "forbidden", "links that anyone can open are turned off for this account", "public_links_off"), 3, "turned off"},
		{"a second link", apiErrReason(409, "conflict", "this item already has a link; change that one instead", "exists"), 3, "already has a link"},
		{"anyone gives viewer only", apiErr(400, "invalid_request", "a link that anyone can open gives the viewer role only"), 2, "viewer role only"},
		{"a device token", apiErrReason(403, "forbidden", "a terminal's sign-in does not make this; use the browser", "device_token"), 3, "use the browser"},
		{"no such node", apiErr(404, "not_found", "no such node"), 4, "no such node"},
	}
	for _, c := range cases {
		isolate(t, testKey)
		f := newFake(t)
		f.on("POST", sharePath(testNode), c.reply)
		res := f.run(LinkCreate, testNode, "--audience", "anyone", "--role", "editor")
		wantCode(t, res, c.code)
		if !strings.Contains(res.stderr, c.say) || res.stdout != "" {
			t.Errorf("%s: stdout %q stderr %q", c.name, res.stdout, res.stderr)
		}
	}
}

func TestLinkCreateADropLink(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	doc := linkDoc(testLink, "anyone", "")
	doc["upload_only"] = true
	f.on("POST", sharePath(testNode), created(map[string]any{"link": doc, "url": shareURL}))
	res := f.run(LinkCreate, testNode, "--drop", "--title", "Send files", "--message", "Thanks", "--ask-name", "--notify",
		"--expires-in", "7d")
	wantCode(t, res, 0)
	got := f.body(0)
	if got["upload_only"] != true || got["audience"] != "anyone" || got["title"] != "Send files" || got["message"] != "Thanks" ||
		got["ask_name"] != true || got["notify"] != true || got["role"] != nil {
		t.Fatalf("body %v", got)
	}
	isolate(t, testKey)
	f = newFake(t)
	for _, args := range [][]string{
		{testNode, "--title", "x"},
		{testNode, "--ask-name"},
		{testNode, "--notify"},
		{testNode, "--drop", "--role", "viewer"},
		{testNode, "--drop", "--audience", "organisation"},
	} {
		wantCode(t, f.run(LinkCreate, args...), 2)
	}
	f.wantNoCalls()
}

func TestLinkCreateASignedDownloadLink(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	signed := "https://drive.example/api/v1/links/" + testSecret + ".payload.mac"
	f.on("POST", nodeRoute(testNode, "/links"), created(map[string]any{"url": signed, "disposition": "attachment",
		"expires_at": "2026-11-12T11:00:00Z"}))
	res := f.run(LinkCreate, testNode, "--signed", "--ttl", "1h")
	wantCode(t, res, 0)
	f.wantCalls("POST " + nodeRoute(testNode, "/links"))
	if got := f.body(0); got["disposition"] != "attachment" || got["ttl"] != float64(3600) {
		t.Fatalf("body %v", got)
	}
	want := "disposition=attachment\nexpires_at=2026-11-12T11:00:00Z\nurl=" + signed + "\n"
	if res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
	if strings.Contains(res.stderr, testSecret) {
		t.Fatalf("stderr %q", res.stderr)
	}
	f.on("POST", nodeRoute(testOther, "/links"), created(map[string]any{"url": signed, "disposition": "inline", "expires_at": "x"}))
	wantCode(t, f.run(LinkCreate, testOther, "--signed", "--inline"), 0)
	if f.body(1)["disposition"] != "inline" || f.body(1)["ttl"] != nil {
		t.Fatalf("body %v", f.body(1))
	}
	// the download rule and a device token come back as the API words them
	f.on("POST", nodeRoute(testFolder, "/links"), apiErr(403, "download_restricted", "this item is marked no download"))
	for _, args := range [][]string{
		{testNode, "--signed", "--audience", "anyone"},
		{testNode, "--signed", "--password-file", "-"},
		{testNode, "--signed", "--drop"},
		{testNode, "--ttl", "1h"},
		{testNode, "--inline"},
		{testNode, "--signed", "--ttl", "0s"},
		{testNode, "--signed", "--ttl", "x"},
	} {
		wantCode(t, f.run(LinkCreate, args...), 2)
	}
}

func TestLinkCreateArgumentErrorsCostNoCall(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	for _, args := range [][]string{
		{},
		{"/docs"},
		{testNode, testOther},
		{testNode, "--audience", "everyone"},
		{testNode, "--expires", "soon"},
		{testNode, "--expires", "2026-12-01T00:00:00Z", "--expires-in", "1d"},
		{testNode, "--password-file", filepath.Join(t.TempDir(), "missing")},
		{testNode, "--title", strings.Repeat("x", 10), "--drop", "--role", "viewer"},
	} {
		wantCode(t, f.run(LinkCreate, args...), 2)
	}
	f.wantNoCalls()
}

func listedLink(linkID string) map[string]any {
	d := linkDoc(linkID, "anyone", "viewer")
	d["secret"] = testSecret
	d["url"] = shareURL
	d["password"] = testPassword
	d["secret_hash"] = "abc"
	d["token"] = testSecret
	return d
}

func TestLinkLsNeverPrintsASecretEvenIfTheServerSendsOne(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		isolate(t, testKey)
		f := newFake(t)
		f.on("GET", sharePath(testNode), listOf("", listedLink(testLink)))
		args := []string{testNode}
		if asJSON {
			args = append(args, "--json")
		}
		res := f.run(LinkLs, args...)
		wantCode(t, res, 0)
		for _, s := range []string{testSecret, "secret_hash", "https://drive.example", `"url"`, `"token"`, `"password":`} {
			if strings.Contains(res.stdout+res.stderr, s) {
				t.Fatalf("json=%v: the listing printed %q: %q", asJSON, s, res.stdout)
			}
		}
		if !strings.Contains(res.stdout, testLink) {
			t.Fatalf("json=%v: stdout %q lost the link", asJSON, res.stdout)
		}
	}
}

func TestLinkLsOfANodeTable(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	drop := linkDoc(testLink2, "anyone", "")
	drop["upload_only"] = true
	drop["paused"] = true
	f.on("GET", sharePath(testNode), listOf("", linkDoc(testLink, "organisation", "editor"), drop))
	res := f.run(LinkLs, testNode)
	wantCode(t, res, 0)
	want := table([]string{"ID", "NODE", "AUDIENCE", "ROLE", "KIND", "EXPIRES", "PASSWORD", "PAUSED", "ITEM"},
		[]string{testLink, testNode, "organisation", "editor", "access", "2026-12-01T00:00:00Z", "false", "", ""},
		[]string{testLink2, testNode, "anyone", "", "drop", "2026-12-01T00:00:00Z", "true", "true", ""})
	if res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
}

func TestLinkLsOfTheAccount(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	d := listedLink(testLink)
	d["item_path"] = "/Team/plan.pdf"
	d["item_name"] = "plan.pdf"
	f.on("GET", accountRoute("/share-links"), listOf("c2", d))
	f.on("GET", accountRoute("/share-links"), listOf("", listedLink(testLink2)))
	res := f.run(LinkLs, "--account", testAccount, "--audience", "anyone", "--kind", "access", "--all")
	wantCode(t, res, 0)
	f.wantCalls("GET "+accountRoute("/share-links")+"?audience=anyone&kind=access",
		"GET "+accountRoute("/share-links")+"?audience=anyone&cursor=c2&kind=access")
	if !strings.Contains(res.stdout, "/Team/plan.pdf") || strings.Contains(res.stdout, testSecret) {
		t.Fatalf("stdout %q", res.stdout)
	}
	isolate(t, testKey)
	f = newFake(t)
	f.on("GET", accountRoute("/share-links"), listOf("c2", d))
	res = f.run(LinkLs, "--account", testAccount)
	wantCode(t, res, 0)
	if !strings.Contains(res.stderr, "--all") {
		t.Fatalf("stderr %q should say more pages exist", res.stderr)
	}
}

func TestLinkLsArgumentErrorsCostNoCall(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	for _, args := range [][]string{
		{},
		{testNode, "--account", testAccount},
		{testNode, "--audience", "anyone"},
		{"--account", "acc_x"},
		{"--account", testAccount, "--audience", "all"},
		{"--account", testAccount, "--kind", "other"},
		{"--account", testAccount, testNode},
	} {
		wantCode(t, f.run(LinkLs, args...), 2)
	}
	f.wantNoCalls()
}

func TestLinkUpdateSendsOnlyWhatWasGiven(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	path := "/api/v1/share-links/" + testLink
	f.on("PATCH", path, ok(listedLink(testLink)))
	res := f.run(LinkUpdate, testLink, "--role", "editor")
	wantCode(t, res, 0)
	f.wantCalls("PATCH " + path)
	if got := f.body(0); got["role"] != "editor" || len(got) != 1 {
		t.Fatalf("body %v", got)
	}
	for _, s := range []string{testSecret, "https://drive.example", testPassword} {
		if strings.Contains(res.stdout+res.stderr, s) {
			t.Fatalf("update printed %q: %q", s, res.stdout)
		}
	}
}

func TestLinkUpdateEveryField(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	path := "/api/v1/share-links/" + testLink
	f.on("PATCH", path, ok(linkDoc(testLink, "anyone", "viewer")))
	res := f.run(LinkUpdate, testLink, "--audience", "anyone", "--role", "viewer", "--expires-in", "2d",
		"--password-file", passwordFile(t), "--title", "T", "--message", "M", "--ask-name=false", "--notify")
	wantCode(t, res, 0)
	got := f.body(0)
	if got["audience"] != "anyone" || got["role"] != "viewer" || got["password"] != testPassword || got["title"] != "T" ||
		got["message"] != "M" || got["ask_name"] != false || got["notify"] != true || got["expires_at"] == nil {
		t.Fatalf("body %v", got)
	}
}

func TestLinkUpdateRemovesAPasswordAndAnExpiry(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("PATCH", "/api/v1/share-links/"+testLink, ok(linkDoc(testLink, "organisation", "viewer")))
	wantCode(t, f.run(LinkUpdate, testLink, "--no-password", "--no-expiry"), 0)
	raw := f.rawBody(0)
	if !strings.Contains(raw, `"password":""`) || !strings.Contains(raw, `"expires_at":null`) {
		t.Fatalf("body %s: a removed password is an empty string and a removed end is null", raw)
	}
}

func TestLinkUpdateArgumentErrorsCostNoCall(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	for _, args := range [][]string{
		{testLink},
		{testNode, "--role", "viewer"},
		{testLink, "--role", "manager"},
		{testLink, "--audience", "all"},
		{testLink, "--no-expiry", "--expires-in", "1d"},
		{testLink, "--no-expiry", "--expires", "2026-12-01T00:00:00Z"},
		{testLink, "--no-password", "--password-file", "-"},
		{testLink, testLink2, "--role", "viewer"},
		{testLink, "--title", strings.Repeat("x", 101)},
		{testLink, "--message", strings.Repeat("x", 1001)},
	} {
		wantCode(t, f.run(LinkUpdate, args...), 2)
	}
	f.wantNoCalls()
}

func TestLinkUpdateRefusals(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	path := "/api/v1/share-links/" + testLink
	f.on("PATCH", path, apiErrReason(403, "forbidden", "a terminal's sign-in does not make this; use the browser", "device_token"))
	res := f.run(LinkUpdate, testLink, "--role", "viewer")
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "use the browser") {
		t.Fatalf("stderr %q", res.stderr)
	}
	f.on("PATCH", "/api/v1/share-links/"+testLink2, apiErr(404, "not_found", "no such link"))
	wantCode(t, f.run(LinkUpdate, testLink2, "--role", "viewer"), 4)
}

func TestLinkResetPrintsTheNewLinkOnce(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	path := "/api/v1/share-links/" + testLink + "/reset"
	f.on("POST", path, createdLink("anyone", "viewer"))
	res := f.run(LinkReset, testLink)
	wantCode(t, res, 0)
	f.wantCalls("POST " + path)
	if strings.Count(res.stdout, testSecret) != 1 || !strings.HasSuffix(res.stdout, "url="+shareURL+"\n") {
		t.Fatalf("stdout %q", res.stdout)
	}
	if strings.Contains(res.stderr, testSecret) {
		t.Fatalf("stderr %q", res.stderr)
	}
	wantCode(t, f.run(LinkReset), 2)
	wantCode(t, f.run(LinkReset, testNode), 2)
	wantCode(t, f.run(LinkReset, testLink, testLink2), 2)
	f.on("POST", "/api/v1/share-links/"+testLink2+"/reset", apiErrReason(403, "forbidden", "a terminal's sign-in does not make this; use the browser", "device_token"))
	wantCode(t, f.run(LinkReset, testLink2), 3)
}

func TestLinkRmAsksFirst(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	path := "/api/v1/share-links/" + testLink
	f.on("DELETE", path, done(http.StatusNoContent))
	wantCode(t, f.run(LinkRm, testLink), 2)
	f.wantNoCalls()
	typeAtTerminal("wrong\n")
	res := f.run(LinkRm, testLink)
	wantCode(t, res, 2)
	if !strings.Contains(res.stderr, testLink) {
		t.Fatalf("the question %q does not name the link", res.stderr)
	}
	f.wantNoCalls()
	typeAtTerminal(testLink + "\n")
	wantCode(t, f.run(LinkRm, testLink), 0)
	f.wantCalls("DELETE " + path)
	res = f.run(LinkRm, testLink, "--yes")
	wantCode(t, res, 0)
	if want := table([]string{"TARGET", "STATUS", "DETAIL"}, []string{testLink, "done", ""}); res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
}

func TestLinkRmSeveral(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("DELETE", "/api/v1/share-links/"+testLink, done(204))
	f.on("DELETE", "/api/v1/share-links/"+testLink2, apiErr(404, "not_found", "no such link"))
	wantCode(t, f.run(LinkRm, testLink, testLink2, "--yes"), 7)
	wantCode(t, f.run(LinkRm, testLink, "nope", "--yes"), 2)
}

func TestLinkRmPublicCountsFirstAndAsks(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	list := accountRoute("/share-links")
	f.on("GET", list, listOf("c2", linkDoc(testLink, "anyone", "viewer")))
	f.on("GET", list, listOf("", linkDoc(testLink2, "anyone", "viewer")))
	del := accountRoute("/share-links/delete-public")
	f.on("POST", del, ok(map[string]int{"deleted": 2}))
	// no terminal, no --yes: refused, nothing read or sent
	wantCode(t, f.run(LinkRm, "--public", "--account", testAccount), 2)
	f.wantNoCalls()
	// a wrong answer: the question says how many, and nothing is deleted
	typeAtTerminal("no\n")
	res := f.run(LinkRm, "--public", "--account", testAccount)
	wantCode(t, res, 2)
	if !strings.Contains(res.stderr, "2 live links") {
		t.Fatalf("the question %q does not say how many links", res.stderr)
	}
	for _, c := range f.requests() {
		if strings.HasPrefix(c, "POST") {
			t.Fatalf("a refused answer deleted: %v", f.requests())
		}
	}
	typeAtTerminal("yes\n")
	res = f.run(LinkRm, "--public", "--account", testAccount)
	wantCode(t, res, 0)
	if res.stdout != "deleted=2\n" {
		t.Fatalf("stdout %q", res.stdout)
	}
	calls := f.requests()
	if calls[len(calls)-1] != "POST "+del {
		t.Fatalf("calls %v", calls)
	}
}

func TestLinkRmPublicWithYesReadsNothing(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	del := accountRoute("/share-links/delete-public")
	f.on("POST", del, ok(map[string]int{"deleted": 0}))
	wantCode(t, f.run(LinkRm, "--public", "--account", testAccount, "--yes", "--json"), 0)
	f.wantCalls("POST " + del)
}

func TestLinkRmArgumentErrorsCostNoCall(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	for _, args := range [][]string{
		{"--yes"},
		{"--public", "--yes"},
		{"--public", "--account", "acc_x", "--yes"},
		{"--public", "--account", testAccount, testLink, "--yes"},
		{"--account", testAccount, testLink, "--yes"},
	} {
		wantCode(t, f.run(LinkRm, args...), 2)
	}
	f.wantNoCalls()
}
