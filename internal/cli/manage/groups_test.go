// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"net/http"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
)

func memberPath(g, u string) string { return accGroup(g) + "/members/" + u }

func TestGroupCreateSendsTheNameAndPrintsTheGroup(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", accGroups(), doc(http.StatusCreated, group(testGroup, "Sales team")))
	res := f.runAcc(GroupCreate, "Sales team")
	wantCode(t, res, 0)
	f.wantCalls("POST " + accGroups())
	f.wantBearer(testAccountKey)
	if body := f.bodyOf(0); len(body) != 1 || body["name"] != "Sales team" {
		t.Fatalf("body %v, want only the name", body)
	}
	for _, want := range []string{"id=" + testGroup, "name=Sales team", "external_id="} {
		if !hasLine(res.stdout, want) {
			t.Errorf("stdout %q lacks the line %q", res.stdout, want)
		}
	}
}

// TestGroupCreateAgainIsAnAnswerNotAnError: the API answers 200 for a group that exists with the same name.
func TestGroupCreateAgainIsAnAnswerNotAnError(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", accGroups(), doc(http.StatusOK, group(testGroup, "Sales team")))
	wantCode(t, f.runAcc(GroupCreate, "Sales team"), 0)
}

func TestGroupCreateExternalIDAndJSON(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", accGroups(), doc(http.StatusCreated, group(testGroup, "g")))
	res := f.runAcc(GroupCreate, "g", "--external-id", "crm-3", "--json")
	wantCode(t, res, 0)
	if got := f.bodyOf(0)["external_id"]; got != "crm-3" {
		t.Fatalf("external_id %v", got)
	}
	if !strings.Contains(res.stdout, `"id": "`+testGroup+`"`) {
		t.Fatalf("stdout %q is not the API's document", res.stdout)
	}
}

func TestGroupCreateHostileNames(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"two words and  spaces", []string{"two words and  spaces"}},
		{"Ünïcödé ✓ 文件", []string{"Ünïcödé ✓ 文件"}},
		{"-leading-dash", []string{"--", "-leading-dash"}},
		{testGroup, []string{testGroup}},
		{testUser, []string{testUser}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t, testAccountKey)
			f := newFake(t)
			f.on("POST", accGroups(), doc(http.StatusCreated, group(testGroup, tc.name)))
			wantCode(t, f.runAcc(GroupCreate, tc.args...), 0)
			if got := f.bodyOf(0)["name"]; got != tc.name {
				t.Fatalf("name sent %q, want %q", got, tc.name)
			}
		})
	}
}

func TestGroupCreateRefusals(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	for _, name := range []string{"", " ", "a/b", "..", "tab\there"} {
		wantCode(t, f.runAcc(GroupCreate, "--", name), 2)
	}
	wantCode(t, f.runAcc(GroupCreate), 2)
	wantCode(t, f.runAcc(GroupCreate, "a", "b"), 2)
	wantCode(t, f.run(GroupCreate, "a"), 2) // no account
	f.wantNoCalls()
	f.on("POST", accGroups(), apiErr(http.StatusConflict, "conflict", "a group with this name exists"))
	res := f.runAcc(GroupCreate, "a")
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "a group with this name exists") {
		t.Fatalf("stderr %q", res.stderr)
	}
}

func TestGroupLs(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	other := id.Derive("grp", "b")
	f.on("GET", accGroups(), page("", group(testGroup, "Sales"), group(other, "Support")))
	res := f.runAcc(GroupLs)
	wantCode(t, res, 0)
	f.wantCalls("GET " + accGroups())
	want := table([]string{"ID", "NAME"}, []string{testGroup, "Sales"}, []string{other, "Support"})
	if res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
	res = f.runAcc(GroupLs, "--json")
	wantCode(t, res, 0)
	if !strings.HasPrefix(res.stdout, "[") || !strings.Contains(res.stdout, `"name": "Sales"`) {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestGroupLsPagesAndAll(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accGroups(), page("c2", group(testGroup, "Sales")), page("c2", group(testGroup, "Sales")),
		page("", group(id.Derive("grp", "b"), "Support")))
	res := f.runAcc(GroupLs)
	wantCode(t, res, 0)
	if !strings.Contains(res.stderr, "--all") || strings.Contains(res.stdout, "Support") {
		t.Fatalf("first page only, with a note: stdout %q stderr %q", res.stdout, res.stderr)
	}
	res = f.runAcc(GroupLs, "--all")
	wantCode(t, res, 0)
	f.wantCalls("GET "+accGroups(), "GET "+accGroups(), "GET "+accGroups()+"?cursor=c2")
	if !strings.Contains(res.stdout, "Support") {
		t.Fatalf("stdout %q", res.stdout)
	}
	wantCode(t, f.runAcc(GroupLs, "extra"), 2)
}

func TestGroupShowAndErrors(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accGroup(testGroup), doc(http.StatusOK, group(testGroup, "Sales")))
	res := f.runAcc(GroupShow, testGroup)
	wantCode(t, res, 0)
	f.wantCalls("GET " + accGroup(testGroup))
	if !hasLine(res.stdout, "name=Sales") || !hasLine(res.stdout, "id="+testGroup) {
		t.Fatalf("stdout %q", res.stdout)
	}
	n := len(f.requests())
	wantCode(t, f.runAcc(GroupShow, testFolder), 2)
	wantCode(t, f.runAcc(GroupShow, "Sales"), 2)
	wantCode(t, f.runAcc(GroupShow), 2)
	if len(f.requests()) != n {
		t.Fatalf("a refused argument sent a request: %v", f.requests())
	}
	f.on("GET", accGroup(id.Derive("grp", "none")), apiErr(http.StatusNotFound, "not_found", "no such group"))
	res = f.runAcc(GroupShow, id.Derive("grp", "none"))
	wantCode(t, res, 4)
	if !strings.Contains(res.stderr, "no such group") {
		t.Fatalf("stderr %q", res.stderr)
	}
}

func TestGroupRename(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PATCH", accGroup(testGroup), doc(http.StatusOK, group(testGroup, "-Renamed")))
	res := f.runAcc(GroupRename, testGroup, "--", "-Renamed")
	wantCode(t, res, 0)
	f.wantCalls("PATCH " + accGroup(testGroup))
	if body := f.bodyOf(0); len(body) != 1 || body["name"] != "-Renamed" {
		t.Fatalf("body %v", body)
	}
	wantCode(t, f.runAcc(GroupRename, testGroup, "a/b"), 2)
	wantCode(t, f.runAcc(GroupRename, testGroup), 2)
	wantCode(t, f.runAcc(GroupRename, "New", testGroup), 2)
	f.set("PATCH", accGroup(testGroup), apiErr(http.StatusConflict, "conflict", "name in use"))
	wantCode(t, f.runAcc(GroupRename, testGroup, "x"), 3)
}

func TestGroupRmWithYes(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("DELETE", accGroup(testGroup), done(http.StatusNoContent))
	res := f.runAcc(GroupRm, testGroup, "--yes")
	wantCode(t, res, 0)
	f.wantCalls("DELETE " + accGroup(testGroup))
	if !hasLine(res.stdout, "target="+testGroup) || !hasLine(res.stdout, "status=done") {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestGroupRmWithoutYesAndNoTerminalRefuses(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.runAcc(GroupRm, testGroup), 2)
	f.wantNoCalls()
}

func TestGroupRmAtATerminalTypesTheName(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accGroup(testGroup), doc(http.StatusOK, group(testGroup, "Sales")))
	f.on("DELETE", accGroup(testGroup), done(http.StatusNoContent))
	for _, typed := range []string{"\n", testGroup + "\n", "sales\n"} {
		typeAtTerminal(typed)
		wantCode(t, f.runAcc(GroupRm, testGroup), 2)
	}
	for _, c := range f.requests() {
		if strings.HasPrefix(c, "DELETE") {
			t.Fatalf("calls %v: a refused answer deleted the group", f.requests())
		}
	}
	typeAtTerminal("Sales\n")
	wantCode(t, f.runAcc(GroupRm, testGroup), 0)
	calls := f.requests()
	if calls[len(calls)-1] != "DELETE "+accGroup(testGroup) {
		t.Fatalf("calls %v: the last must be the delete", calls)
	}
}

func TestGroupRmNameReadFailureDeletesNothing(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("DELETE", accGroup(testGroup), done(http.StatusNoContent))
	typeAtTerminal("Sales\n")
	wantCode(t, f.runAcc(GroupRm, testGroup), 4)
	for _, c := range f.requests() {
		if strings.HasPrefix(c, "DELETE") {
			t.Fatalf("calls %v: nothing may be deleted when the name could not be read", f.requests())
		}
	}
}

func TestGroupRmErrors(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.runAcc(GroupRm, testGroup, "--yes"), 4)
	f.on("DELETE", accGroup(testGroup), apiErr(http.StatusForbidden, "forbidden", "owners only"))
	wantCode(t, f.runAcc(GroupRm, testGroup, "--yes"), 3)
	n := len(f.requests())
	wantCode(t, f.runAcc(GroupRm, testFolder, "--yes"), 2)
	wantCode(t, f.runAcc(GroupRm, "--yes"), 2)
	if len(f.requests()) != n {
		t.Fatalf("a refused argument sent a request: %v", f.requests())
	}
}

func TestGroupMembers(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	m := func(u string) map[string]any {
		return map[string]any{"user_id": u, "created_at": "2026-11-03T08:00:00Z"}
	}
	f.on("GET", accGroup(testGroup)+"/members", page("", m(testUser), m(id.Derive("usr", "b"))))
	res := f.runAcc(GroupMembers, testGroup)
	wantCode(t, res, 0)
	f.wantCalls("GET " + accGroup(testGroup) + "/members")
	want := table([]string{"USER_ID", "ADDED"}, []string{testUser, "2026-11-03T08:00:00Z"},
		[]string{id.Derive("usr", "b"), "2026-11-03T08:00:00Z"})
	if res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
	res = f.runAcc(GroupMembers, testGroup, "--json")
	wantCode(t, res, 0)
	if !strings.HasPrefix(res.stdout, "[") || !strings.Contains(res.stdout, `"user_id": "`+testUser+`"`) {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestGroupMembersPagesAndErrors(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	path := accGroup(testGroup) + "/members"
	m := func(u string) map[string]any {
		return map[string]any{"user_id": u, "created_at": "2026-11-03T08:00:00Z"}
	}
	f.on("GET", path, page("c2", m(testUser)), page("c2", m(testUser)), page("", m(id.Derive("usr", "b"))))
	res := f.runAcc(GroupMembers, testGroup)
	wantCode(t, res, 0)
	if !strings.Contains(res.stderr, "--all") {
		t.Fatalf("stderr %q", res.stderr)
	}
	wantCode(t, f.runAcc(GroupMembers, testGroup, "--all"), 0)
	f.wantCalls("GET "+path, "GET "+path, "GET "+path+"?cursor=c2")
	wantCode(t, f.runAcc(GroupMembers, testUser), 2)
	wantCode(t, f.runAcc(GroupMembers), 2)
	g2 := id.Derive("grp", "gone")
	wantCode(t, f.runAcc(GroupMembers, g2), 4)
}

func TestGroupAddMember(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PUT", memberPath(testGroup, testUser), done(http.StatusNoContent))
	res := f.runAcc(GroupAddMember, testGroup, testUser)
	wantCode(t, res, 0)
	f.wantCalls("PUT " + memberPath(testGroup, testUser))
	f.wantBearer(testAccountKey)
	if f.rawBody(0) != "" {
		t.Fatalf("an add has no body: %q", f.rawBody(0))
	}
	for _, want := range []string{"group=" + testGroup, "user=" + testUser, "status=done"} {
		if !hasLine(res.stdout, want) {
			t.Errorf("stdout %q lacks %q", res.stdout, want)
		}
	}
	res = f.runAcc(GroupAddMember, testGroup, testUser, "--json")
	wantCode(t, res, 0)
	if !strings.Contains(res.stdout, `"user": "`+testUser+`"`) || !strings.Contains(res.stdout, `"status": "done"`) {
		t.Fatalf("stdout %q", res.stdout)
	}
}

// TestGroupAddMemberUnknownUser: the server answers not found for a user it does not know, and the command says so.
func TestGroupAddMemberUnknownUser(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PUT", memberPath(testGroup, testUser), apiErr(http.StatusNotFound, "not_found", "no such user"))
	res := f.runAcc(GroupAddMember, testGroup, testUser)
	wantCode(t, res, 4)
	if !strings.Contains(res.stderr, "no such user") {
		t.Fatalf("stderr %q", res.stderr)
	}
}

func TestGroupMemberErrorsAndArguments(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PUT", memberPath(testGroup, testUser), apiErr(http.StatusBadRequest, "invalid_request", "an installation admin cannot be a group member"))
	wantCode(t, f.runAcc(GroupAddMember, testGroup, testUser), 2)
	f.on("PUT", memberPath(testGroup, id.Derive("usr", "x")), apiErr(http.StatusForbidden, "forbidden", "owners only"))
	wantCode(t, f.runAcc(GroupAddMember, testGroup, id.Derive("usr", "x")), 3)
	n := len(f.requests())
	for _, args := range [][]string{
		{testGroup}, {testUser}, {testUser, testGroup}, {testGroup, testFolder}, {testFolder, testUser},
		{testGroup, "someone@example.com"}, {testGroup, testUser, testUser}, {},
	} {
		wantCode(t, f.runAcc(GroupAddMember, args...), 2)
		wantCode(t, f.runAcc(GroupRemoveMember, args...), 2)
	}
	if len(f.requests()) != n {
		t.Fatalf("a refused argument sent a request: %v", f.requests())
	}
}

func TestGroupRemoveMember(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("DELETE", memberPath(testGroup, testUser), done(http.StatusNoContent))
	res := f.runAcc(GroupRemoveMember, testGroup, testUser)
	wantCode(t, res, 0)
	f.wantCalls("DELETE " + memberPath(testGroup, testUser))
	for _, want := range []string{"group=" + testGroup, "user=" + testUser, "status=done"} {
		if !hasLine(res.stdout, want) {
			t.Errorf("stdout %q lacks %q", res.stdout, want)
		}
	}
	f.set("DELETE", memberPath(testGroup, testUser), apiErr(http.StatusNotFound, "not_found", "no such member"))
	wantCode(t, f.runAcc(GroupRemoveMember, testGroup, testUser), 4)
}

func userGrantsPath() string { return "/api/v1/accounts/" + testAcc + "/users/" + testUser + "/grants" }

func TestUserGrants(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	g := func(fld, level string) map[string]any {
		return map[string]any{"folder_id": fld, "level": level, "created_at": "2026-11-04T09:00:00Z", "created_by": "usr_X"}
	}
	other := id.Derive("fld", "other")
	f.on("GET", userGrantsPath(), page("", g(testFolder, "write"), g(other, "read")))
	res := f.runAcc(UserGrants, testUser)
	wantCode(t, res, 0)
	f.wantCalls("GET " + userGrantsPath())
	want := table([]string{"FOLDER_ID", "LEVEL", "GRANTED"}, []string{testFolder, "write", "2026-11-04T09:00:00Z"},
		[]string{other, "read", "2026-11-04T09:00:00Z"})
	if res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
	res = f.runAcc(UserGrants, testUser, "--json")
	wantCode(t, res, 0)
	if !strings.HasPrefix(res.stdout, "[") || !strings.Contains(res.stdout, `"level": "write"`) {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestUserGrantsEmptyAndErrors(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", userGrantsPath(), page(""))
	res := f.runAcc(UserGrants, testUser)
	wantCode(t, res, 0)
	if res.stdout != table([]string{"FOLDER_ID", "LEVEL", "GRANTED"}) {
		t.Fatalf("an empty list is the header alone: %q", res.stdout)
	}
	n := len(f.requests())
	wantCode(t, f.runAcc(UserGrants, testFolder), 2)
	wantCode(t, f.runAcc(UserGrants, "ann@example.com"), 2)
	wantCode(t, f.runAcc(UserGrants), 2)
	wantCode(t, f.run(UserGrants, testUser), 2) // no account
	if len(f.requests()) != n {
		t.Fatalf("a refused argument sent a request: %v", f.requests())
	}
	f.set("GET", userGrantsPath(), apiErr(http.StatusNotFound, "not_found", "no such user"))
	wantCode(t, f.runAcc(UserGrants, testUser), 4)
	f.set("GET", userGrantsPath(), apiErr(http.StatusForbidden, "forbidden", "no"))
	wantCode(t, f.runAcc(UserGrants, testUser), 3)
}
