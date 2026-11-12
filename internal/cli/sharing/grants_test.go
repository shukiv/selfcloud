// SPDX-License-Identifier: AGPL-3.0-or-later

package sharing

import (
	"net/http"
	"strings"
	"testing"
)

func perm(subject, kind, role string) map[string]any {
	return map[string]any{"subject_kind": kind, "subject_id": subject, "role": role, "inherited_from": nil,
		"expires_at": nil, "created_at": "2026-11-01T10:00:00Z", "created_by": testUser2, "subject_name": "Dana"}
}

func TestShareLsPrintsTheNodesPermissions(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	inherited := perm(testGroup, "group", "viewer")
	inherited["inherited_from"] = testOther
	inherited["expires_at"] = "2026-12-01T00:00:00Z"
	f.on("GET", nodeRoute(testNode, "/permissions"), listOf("", perm(testUser, "user", "editor"), inherited))
	res := f.run(ShareLs, testNode)
	wantCode(t, res, 0)
	f.wantCalls("GET " + nodeRoute(testNode, "/permissions"))
	want := table([]string{"SUBJECT", "KIND", "ROLE", "FROM", "EXPIRES", "NAME"},
		[]string{testUser, "user", "editor", "", "", "Dana"},
		[]string{testGroup, "group", "viewer", testOther, "2026-12-01T00:00:00Z", "Dana"})
	if res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
}

func TestShareLsJSONIsTheListAsTheAPIWroteIt(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("GET", nodeRoute(testNode, "/permissions"), listOf("", perm(testUser, "user", "editor")))
	res := f.run(ShareLs, testNode, "--json")
	wantCode(t, res, 0)
	if !strings.HasPrefix(res.stdout, "[") || !strings.Contains(res.stdout, `"role": "editor"`) {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestShareLsOfATeamFolderReadsItsGrants(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("GET", accountRoute("/folders/"+testFolder+"/grants"), listOf("",
		map[string]any{"user_id": testUser, "level": "write", "created_at": "2026-11-01T10:00:00Z", "created_by": testUser2}))
	res := f.run(ShareLs, "--folder", testFolder, "--account", testAccount)
	wantCode(t, res, 0)
	f.wantCalls("GET " + accountRoute("/folders/"+testFolder+"/grants"))
	want := table([]string{"USER", "LEVEL", "CREATED"}, []string{testUser, "write", "2026-11-01T10:00:00Z"})
	if res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
}

func TestShareLsTakesTheAccountFromTheEnvironment(t *testing.T) {
	isolate(t, testKey)
	t.Setenv("SELFCLOUD_ACCOUNT", testAccount)
	f := newFake(t)
	f.on("GET", accountRoute("/folders/"+testFolder+"/grants"), listOf(""))
	wantCode(t, f.run(ShareLs, "--folder", testFolder), 0)
}

func TestShareLsArgumentErrorsCostNoCall(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	for _, args := range [][]string{
		{},                                 // neither a node nor a folder
		{testNode, "--folder", testFolder}, // both
		{"/docs"},                          // a path is not a node
		{"nod_short"},                      // a bad id
		{"--folder", testFolder},           // no account
		{"--folder", testFolder, "--account", "acc_x"},
		{"--folder", testNode, "--account", testAccount},
		{testNode, testOther},
	} {
		wantCode(t, f.run(ShareLs, args...), 2)
	}
	f.wantNoCalls()
}

func TestShareLsErrors(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("GET", nodeRoute(testNode, "/permissions"), apiErr(404, "not_found", "no such node"))
	f.on("GET", nodeRoute(testOther, "/permissions"), apiErr(403, "forbidden", "a delegated token cannot read permissions"))
	wantCode(t, f.run(ShareLs, testNode), 4)
	res := f.run(ShareLs, testOther)
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "a delegated token cannot read permissions") {
		t.Fatalf("stderr %q does not say why", res.stderr)
	}
}

// TestShareGrantTakesEveryRoleTheAPINames: the five roles go to the server as given; the server decides what the caller may
// grant. A role it does not name is refused before any call.
func TestShareGrantTakesEveryRoleTheAPINames(t *testing.T) {
	for _, role := range []string{"viewer", "commenter", "contributor", "editor", "manager"} {
		isolate(t, testKey)
		f := newFake(t)
		path := nodeRoute(testNode, "/permissions/"+testUser)
		f.on("PUT", path, ok(map[string]any{"subject_kind": "user", "subject_id": testUser, "role": role, "notified": true}))
		res := f.run(ShareGrant, testNode, testUser, "--role", role)
		wantCode(t, res, 0)
		f.wantCalls("PUT " + path)
		if got := f.body(0); got["role"] != role || len(got) != 1 {
			t.Fatalf("%s: body %v, want only the role", role, got)
		}
		want := "subject_kind=user\nsubject_id=" + testUser + "\nrole=" + role + "\nnotified=true\n"
		if res.stdout != want {
			t.Fatalf("%s: stdout %q, want %q", role, res.stdout, want)
		}
	}
	isolate(t, testKey)
	f := newFake(t)
	for _, bad := range []string{"owner", "Viewer", "", "admin"} {
		wantCode(t, f.run(ShareGrant, testNode, testUser, "--role", bad), 2)
	}
	f.wantNoCalls()
}

func TestShareGrantToAGroupWithAnEndAndANote(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	path := nodeRoute(testNode, "/permissions/"+testGroup)
	f.on("PUT", path, ok(map[string]any{"subject_kind": "group", "subject_id": testGroup, "role": "viewer",
		"expires_at": "2026-12-01T00:00:00Z", "notified": false, "reason": "not_requested"}))
	res := f.run(ShareGrant, testNode, testGroup, "--role", "viewer", "--expires", "2026-12-01T00:00:00Z",
		"--message", "for the audit", "--no-notify")
	wantCode(t, res, 0)
	got := f.body(0)
	if got["role"] != "viewer" || got["expires_at"] != "2026-12-01T00:00:00Z" || got["message"] != "for the audit" ||
		got["notify"] != false {
		t.Fatalf("body %v", got)
	}
	if !strings.Contains(res.stdout, "reason=not_requested\n") || !strings.Contains(res.stdout, "expires_at=2026-12-01T00:00:00Z\n") {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestShareGrantExpiresIn(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("PUT", nodeRoute(testNode, "/permissions/"+testUser), ok(map[string]any{"role": "viewer"}))
	wantCode(t, f.run(ShareGrant, testNode, testUser, "--role", "viewer", "--expires-in", "7d"), 0)
	if s, _ := f.body(0)["expires_at"].(string); !strings.HasSuffix(s, "Z") || len(s) != len("2026-12-01T00:00:00Z") {
		t.Fatalf("expires_at %v", f.body(0)["expires_at"])
	}
}

func TestShareGrantOfATeamFolderUsesALevel(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	path := accountRoute("/folders/" + testFolder + "/grants/" + testUser)
	f.on("PUT", path, ok(map[string]any{"folder_id": testFolder, "user_id": testUser, "level": "read",
		"created_at": "2026-11-01T10:00:00Z", "created_by": testUser2}))
	res := f.run(ShareGrant, "--folder", testFolder, "--account", testAccount, testUser, "--level", "read")
	wantCode(t, res, 0)
	f.wantCalls("PUT " + path)
	if got := f.body(0); got["level"] != "read" || len(got) != 1 {
		t.Fatalf("body %v", got)
	}
	want := "folder_id=" + testFolder + "\nuser_id=" + testUser + "\nlevel=read\ncreated_at=2026-11-01T10:00:00Z\n"
	if res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
}

func TestShareGrantArgumentErrorsCostNoCall(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	for _, args := range [][]string{
		{testNode, testUser},                        // no role
		{testNode, "--role", "viewer"},              // no subject
		{testNode, testFolder, "--role", "viewer"},  // a folder is no subject
		{testNode, "usr_short", "--role", "viewer"}, // bad id
		{testNode, testUser, "--role", "viewer", "--level", "read"},
		{testNode, testUser, "--role", "viewer", "--expires", "x"},
		{testNode, testUser, "--role", "viewer", "--expires", "2026-12-01T00:00:00Z", "--expires-in", "1d"},
		{testNode, testUser, "--role", "viewer", "--message", strings.Repeat("é", 501)},
		{"--folder", testFolder, "--account", testAccount, testUser}, // no level
		{"--folder", testFolder, "--account", testAccount, testUser, "--level", "admin"},
		{"--folder", testFolder, "--account", testAccount, testUser, "--level", "read", "--role", "viewer"},
		{"--folder", testFolder, "--account", testAccount, testGroup, "--level", "read"}, // a group has no grant
		{"--folder", testFolder, "--account", testAccount, testUser, "--level", "read", "--expires-in", "1d"},
		{"--folder", testFolder, testUser, "--level", "read"}, // no account
		{"--folder", testFolder, "--account", testAccount, testNode, "--level", "read"},
	} {
		wantCode(t, f.run(ShareGrant, args...), 2)
	}
	f.wantNoCalls()
}

// TestShareGrantRefusalsByRole: the server's refusals for what a role may share come back with its message and exit 3, and
// the invalid and conflicting ones with their own codes. The CLI adds no role rule of its own beyond the role's name.
func TestShareGrantRefusalsByRole(t *testing.T) {
	cases := []struct {
		name   string
		reply  reply
		code   int
		inText string
	}{
		{"a viewer may not share", apiErr(403, "forbidden", "you need the manager role"), 3, "you need the manager role"},
		{"a commenter may not share", apiErr(403, "forbidden", "you need the manager role"), 3, "manager role"},
		{"an editor may not grant above its role", apiErr(403, "forbidden", "you may not grant a role above your own"), 3, "above your own"},
		{"an editor may not change a manager's row", apiErr(403, "forbidden", "you may not change a permission above your own role"), 3, "above your own role"},
		{"members only", apiErr(409, "conflict", "the team space is members only"), 3, "members only"},
		{"no such user", apiErr(404, "not_found", "no such user"), 4, "no such user"},
		{"the end is in the past", apiErr(400, "invalid_request", "expires_at must be in the future"), 2, "future"},
		{"a device token", apiErrReason(403, "forbidden", "a terminal's sign-in does not make this; use the browser", "device_token"), 3, "use the browser"},
	}
	for _, c := range cases {
		isolate(t, testKey)
		f := newFake(t)
		f.on("PUT", nodeRoute(testNode, "/permissions/"+testUser), c.reply)
		res := f.run(ShareGrant, testNode, testUser, "--role", "editor")
		wantCode(t, res, c.code)
		if !strings.Contains(res.stderr, c.inText) {
			t.Errorf("%s: stderr %q does not say %q", c.name, res.stderr, c.inText)
		}
		if res.stdout != "" {
			t.Errorf("%s: stdout %q, want none", c.name, res.stdout)
		}
	}
}

func TestShareRevokeAsksFirst(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	path := nodeRoute(testNode, "/permissions/"+testUser)
	f.on("DELETE", path, done(http.StatusNoContent))
	wantCode(t, f.run(ShareRevoke, testNode, testUser), 2)
	f.wantNoCalls()
	res := f.run(ShareRevoke, testNode, testUser, "--yes")
	wantCode(t, res, 0)
	f.wantCalls("DELETE " + path)
	if want := table([]string{"TARGET", "STATUS", "DETAIL"}, []string{testUser, "done", ""}); res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
}

func TestShareRevokeAtATerminalNamesTheNodeAndTheSubject(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	path := nodeRoute(testNode, "/permissions/"+testUser)
	f.on("GET", nodeRoute(testNode, ""), ok(map[string]any{"id": testNode, "name": "budget.xlsx", "kind": "file"}))
	f.on("DELETE", path, done(http.StatusNoContent))
	typeAtTerminal("nope\n")
	res := f.run(ShareRevoke, testNode, testUser)
	wantCode(t, res, 2)
	for _, want := range []string{"budget.xlsx", testUser} {
		if !strings.Contains(res.stderr, want) {
			t.Fatalf("the question %q does not name %q", res.stderr, want)
		}
	}
	for _, c := range f.requests() {
		if strings.HasPrefix(c, "DELETE") {
			t.Fatalf("a refused answer revoked: %v", f.requests())
		}
	}
	typeAtTerminal(testUser + "\n")
	wantCode(t, f.run(ShareRevoke, testNode, testUser), 0)
	calls := f.requests()
	if calls[len(calls)-1] != "DELETE "+path {
		t.Fatalf("calls %v: the delete must be last", calls)
	}
}

func TestShareRevokeSeveralSubjects(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("DELETE", nodeRoute(testNode, "/permissions/"+testUser), done(204))
	f.on("DELETE", nodeRoute(testNode, "/permissions/"+testGroup), apiErr(404, "not_found", "no such permission"))
	res := f.run(ShareRevoke, testNode, testUser, testGroup, "--yes")
	wantCode(t, res, 7)
	if !strings.Contains(res.stdout, "no such permission") {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestShareRevokeOfATeamFolderGrant(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	path := accountRoute("/folders/" + testFolder + "/grants/" + testUser)
	f.on("DELETE", path, done(204))
	wantCode(t, f.run(ShareRevoke, "--folder", testFolder, "--account", testAccount, testUser, "--yes"), 0)
	f.wantCalls("DELETE " + path)
}

func TestShareRevokeArgumentErrorsCostNoCall(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	for _, args := range [][]string{
		{testNode},
		{testNode, "x", "--yes"},
		{"--folder", testFolder, "--account", testAccount, "--yes"},
		{"--folder", testFolder, "--account", testAccount, testGroup, "--yes"},
		{"--folder", testFolder, testUser, "--yes"},
		{testNode, testUser, "--folder", testFolder, "--yes"},
	} {
		wantCode(t, f.run(ShareRevoke, args...), 2)
	}
	f.wantNoCalls()
}

func TestTransferOwnership(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("GET", nodeRoute(testNode, ""), ok(map[string]any{"id": testNode, "name": "report.pdf", "kind": "file"}))
	f.on("POST", nodeRoute(testNode, "/transfer-ownership"), ok(map[string]any{"id": testNode, "name": "report.pdf",
		"kind": "file", "owner_id": testUser}))
	wantCode(t, f.run(TransferOwnership, testNode, "--to", testUser), 2) // no terminal, no --yes
	f.wantNoCalls()
	res := f.run(TransferOwnership, testNode, "--to", testUser, "--yes")
	wantCode(t, res, 0)
	f.wantCalls("POST " + nodeRoute(testNode, "/transfer-ownership"))
	if got := f.body(0); got["to"] != testUser || len(got) != 1 {
		t.Fatalf("body %v", got)
	}
	if want := "id=" + testNode + "\nname=report.pdf\nkind=file\nowner_id=" + testUser + "\n"; res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
}

func TestTransferOwnershipAtATerminalTypesTheNodeName(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("GET", nodeRoute(testNode, ""), ok(map[string]any{"id": testNode, "name": "report.pdf", "kind": "file"}))
	f.on("POST", nodeRoute(testNode, "/transfer-ownership"), ok(map[string]any{"id": testNode, "owner_id": testUser}))
	typeAtTerminal("wrong\n")
	res := f.run(TransferOwnership, testNode, "--to", testUser)
	wantCode(t, res, 2)
	for _, want := range []string{"report.pdf", testUser} {
		if !strings.Contains(res.stderr, want) {
			t.Fatalf("the question %q does not name %q", res.stderr, want)
		}
	}
	for _, c := range f.requests() {
		if strings.HasPrefix(c, "POST") {
			t.Fatalf("a refused answer transferred: %v", f.requests())
		}
	}
	typeAtTerminal("report.pdf\n")
	wantCode(t, f.run(TransferOwnership, testNode, "--to", testUser), 0)
}

func TestTransferOwnershipErrors(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("POST", nodeRoute(testNode, "/transfer-ownership"), apiErr(403, "forbidden", "only the owner of the item, or an account key, can transfer it"))
	f.on("POST", nodeRoute(testOther, "/transfer-ownership"), apiErr(409, "conflict", "the new owner has no My files to receive the item"))
	wantCode(t, f.run(TransferOwnership, testNode, "--to", testUser, "--yes"), 3)
	wantCode(t, f.run(TransferOwnership, testOther, "--to", testUser, "--yes"), 3)
	wantCode(t, f.run(TransferOwnership, testNode, "--yes"), 2)
	wantCode(t, f.run(TransferOwnership, testNode, "--to", testGroup, "--yes"), 2)
	wantCode(t, f.run(TransferOwnership, "--to", testUser, "--yes"), 2)
}
