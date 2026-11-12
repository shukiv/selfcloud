// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
)

func hasLine(out, line string) bool {
	for _, l := range strings.Split(out, "\n") {
		if l == line {
			return true
		}
	}
	return false
}

func TestFolderCreateSendsTheNameAndPrintsTheFolder(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", accFolders(), doc(http.StatusCreated, folder(testFolder, "Team A")))
	res := f.runAcc(FolderCreate, "Team A")
	wantCode(t, res, 0)
	f.wantCalls("POST " + accFolders())
	f.wantBearer(testAccountKey)
	if body := f.bodyOf(0); len(body) != 1 || body["name"] != "Team A" {
		t.Fatalf("body %v, want only the name", body)
	}
	for _, want := range []string{"id=" + testFolder, "name=Team A", "kind=team", "allow_links=true"} {
		if !hasLine(res.stdout, want) {
			t.Errorf("stdout %q lacks the line %q", res.stdout, want)
		}
	}
}

func TestFolderCreateJSONPrintsTheDocument(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", accFolders(), doc(http.StatusCreated, folder(testFolder, "Team A")))
	res := f.runAcc(FolderCreate, "Team A", "--json")
	wantCode(t, res, 0)
	if !strings.Contains(res.stdout, `"id": "`+testFolder+`"`) || !strings.Contains(res.stdout, `"root_node_id"`) {
		t.Fatalf("stdout %q is not the API's document", res.stdout)
	}
}

func TestFolderCreateExternalID(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", accFolders(), doc(http.StatusCreated, folder(testFolder, "Team A")))
	wantCode(t, f.runAcc(FolderCreate, "Team A", "--external-id", "crm-17"), 0)
	if got := f.bodyOf(0)["external_id"]; got != "crm-17" {
		t.Fatalf("external_id %v, want crm-17", got)
	}
}

// TestFolderCreateHostileNames: names with spaces, Unicode, a leading dash and the look of an id are sent as they are.
func TestFolderCreateHostileNames(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"two words and  spaces", []string{"two words and  spaces"}},
		{"Ünïcödé ✓ 文件", []string{"Ünïcödé ✓ 文件"}},
		{"-leading-dash", []string{"--", "-leading-dash"}},
		{"--double", []string{"--", "--double"}},
		{testFolder, []string{testFolder}},
		{testAcc, []string{testAcc}},
		{"-h", []string{"--", "-h"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t, testAccountKey)
			f := newFake(t)
			f.on("POST", accFolders(), doc(http.StatusCreated, folder(testFolder, tc.name)))
			wantCode(t, f.runAcc(FolderCreate, tc.args...), 0)
			f.wantCalls("POST " + accFolders())
			if got := f.bodyOf(0)["name"]; got != tc.name {
				t.Fatalf("name sent %q, want %q", got, tc.name)
			}
		})
	}
}

// TestFolderCreateRefusesBadNamesBeforeAnyCall: a name the API would refuse is refused with exit 2 and no request.
func TestFolderCreateRefusesBadNamesBeforeAnyCall(t *testing.T) {
	for _, name := range []string{"", "   ", "a/b", "a\\b", ".", "..", "tab\there", "nul\x00", strings.Repeat("x", 300)} {
		isolate(t, testAccountKey)
		f := newFake(t)
		wantCode(t, f.runAcc(FolderCreate, "--", name), 2)
		f.wantNoCalls()
	}
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.runAcc(FolderCreate), 2)           // no name
	wantCode(t, f.runAcc(FolderCreate, "a", "b"), 2) // two names
	f.wantNoCalls()
}

func TestFolderCreateErrors(t *testing.T) {
	cases := []struct {
		reply reply
		code  int
		text  string
	}{
		{apiErr(http.StatusConflict, "conflict", "a folder with this name exists"), 3, "a folder with this name exists"},
		{apiErr(http.StatusForbidden, "forbidden", "only an owner may"), 3, "only an owner may"},
		{apiErr(http.StatusNotFound, "not_found", "no such account"), 4, "no such account"},
		{apiErr(http.StatusUnauthorized, "unauthorized", "bad key"), 3, "bad key"},
	}
	for _, tc := range cases {
		isolate(t, testAccountKey)
		f := newFake(t)
		f.on("POST", accFolders(), tc.reply)
		res := f.runAcc(FolderCreate, "x")
		wantCode(t, res, tc.code)
		if !strings.Contains(res.stderr, tc.text) {
			t.Errorf("stderr %q lacks %q", res.stderr, tc.text)
		}
	}
}

func TestFolderCreateErrorJSON(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", accFolders(), apiErr(http.StatusConflict, "conflict", "taken"))
	res := f.runAcc(FolderCreate, "x", "--json")
	wantCode(t, res, 3)
	if want := `{"error":{"code":"conflict","message":"taken (HTTP 409)"}}`; strings.TrimSpace(res.stderr) != want {
		t.Fatalf("stderr %q, want %q", res.stderr, want)
	}
}

// TestAccountIsRequiredAndChecked: an account-scoped command needs an acc_ id from --account or SELFCLOUD_ACCOUNT, and
// refuses anything else before a request.
func TestAccountIsRequiredAndChecked(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.run(FolderCreate, "x"), 2) // none
	for _, bad := range []string{"", "acc_short", "acc_" + strings.Repeat("!", 26), testFolder, testUser,
		strings.Repeat("a", 40), testAcc + "/x", testAcc + "?x=1", "../" + testAcc, testAcc + "\n"} {
		wantCode(t, f.run(FolderCreate, "x", "--account", bad), 2)
	}
	t.Setenv("SELFCLOUD_ACCOUNT", "acc_short")
	wantCode(t, f.run(FolderCreate, "x"), 2)
	f.wantNoCalls()
}

func TestAccountFromTheEnvironmentAndTheFlagWins(t *testing.T) {
	isolate(t, testAccountKey)
	other := id.Derive("acc", "another")
	f := newFake(t)
	f.on("POST", accFolders(), doc(http.StatusCreated, folder(testFolder, "x")))
	f.on("POST", "/api/v1/accounts/"+other+"/folders", doc(http.StatusCreated, folder(testFolder, "x")))
	t.Setenv("SELFCLOUD_ACCOUNT", testAcc)
	wantCode(t, f.run(FolderCreate, "x"), 0)
	wantCode(t, f.run(FolderCreate, "x", "--account", other), 0)
	f.wantCalls("POST "+accFolders(), "POST /api/v1/accounts/"+other+"/folders")
}

func TestFolderLsAccountPrintsATable(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accFolders(), page("", folder(testFolder, "Team A"), folder(id.Derive("fld", "b"), "Team B")))
	res := f.runAcc(FolderLs)
	wantCode(t, res, 0)
	f.wantCalls("GET " + accFolders())
	want := table([]string{"ID", "NAME", "KIND"}, []string{testFolder, "Team A", "team"},
		[]string{id.Derive("fld", "b"), "Team B", "team"})
	if res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
}

func TestFolderLsJSONIsTheListOfItems(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accFolders(), page("", folder(testFolder, "Team A")))
	res := f.runAcc(FolderLs, "--json")
	wantCode(t, res, 0)
	if !strings.HasPrefix(res.stdout, "[") || !strings.Contains(res.stdout, `"name": "Team A"`) {
		t.Fatalf("stdout %q is not a JSON list of the API's items", res.stdout)
	}
}

func TestFolderLsPagesAndAll(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accFolders(), page("c2", folder(testFolder, "Team A")), page("c2", folder(testFolder, "Team A")),
		page("", folder(id.Derive("fld", "b"), "Team B")))
	res := f.runAcc(FolderLs)
	wantCode(t, res, 0)
	f.wantCalls("GET " + accFolders())
	if !strings.Contains(res.stderr, "--all") || strings.Contains(res.stdout, "Team B") {
		t.Fatalf("first page only, with a note on stderr: stdout %q stderr %q", res.stdout, res.stderr)
	}
	res = f.runAcc(FolderLs, "--all")
	wantCode(t, res, 0)
	f.wantCalls("GET "+accFolders(), "GET "+accFolders(), "GET "+accFolders()+"?cursor=c2")
	if !strings.Contains(res.stdout, "Team B") {
		t.Fatalf("--all lacks the second page: %q", res.stdout)
	}
}

func TestFolderLsExternalIDFilter(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accFolders(), page(""))
	wantCode(t, f.runAcc(FolderLs, "--external-id", "crm 17&x"), 0)
	f.wantCalls("GET " + accFolders() + "?external_id=crm+17%26x")
}

func TestFolderLsMine(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	mine := folder(testFolder, "Team A")
	mine["role"] = "editor"
	mine["hidden"] = true
	f.on("GET", "/api/v1/folders", page("", mine))
	res := f.run(FolderLs, "--mine")
	wantCode(t, res, 0)
	f.wantCalls("GET /api/v1/folders")
	f.wantBearer(testDeviceTok)
	want := table([]string{"ID", "NAME", "KIND", "ROLE", "HIDDEN"}, []string{testFolder, "Team A", "team", "editor", "true"})
	if res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
}

func TestFolderLsMineHiddenAsksForOnlyTheHiddenOnes(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("GET", "/api/v1/folders", page(""))
	wantCode(t, f.run(FolderLs, "--mine", "--hidden"), 0)
	f.wantCalls("GET /api/v1/folders?hidden=true")
}

func TestFolderLsModeRules(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.run(FolderLs), 2)                                 // neither
	wantCode(t, f.runAcc(FolderLs, "--mine"), 2)                    // both
	wantCode(t, f.runAcc(FolderLs, "--hidden"), 2)                  // hidden is for --mine
	wantCode(t, f.run(FolderLs, "--mine", "--external-id", "x"), 2) // external id is for --account
	wantCode(t, f.run(FolderLs, "--mine", "extra"), 2)              // no arguments
	f.wantNoCalls()
	t.Setenv("SELFCLOUD_ACCOUNT", testAcc) // the environment alone does not turn --mine into an account listing
	f.on("GET", "/api/v1/folders", page(""))
	wantCode(t, f.run(FolderLs, "--mine"), 0)
	f.wantCalls("GET /api/v1/folders")
}

func TestFolderShow(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accFolder(testFolder), doc(http.StatusOK, folder(testFolder, "Team A")))
	res := f.runAcc(FolderShow, testFolder)
	wantCode(t, res, 0)
	f.wantCalls("GET " + accFolder(testFolder))
	if !hasLine(res.stdout, "name=Team A") {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestFolderShowErrorsAndArguments(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	res := f.runAcc(FolderShow, testFolder) // the fake answers 404
	wantCode(t, res, 4)
	if !strings.Contains(res.stderr, "no such thing") {
		t.Fatalf("stderr %q", res.stderr)
	}
	f.on("GET", accFolder(testFolder), apiErr(http.StatusForbidden, "forbidden", "not yours"))
	wantCode(t, f.runAcc(FolderShow, testFolder), 3)
	n := len(f.requests())
	wantCode(t, f.runAcc(FolderShow, testGroup), 2) // a group id
	wantCode(t, f.runAcc(FolderShow, "Team A"), 2)  // a name
	wantCode(t, f.runAcc(FolderShow), 2)            // nothing
	wantCode(t, f.runAcc(FolderShow, testFolder, testFolder), 2)
	if len(f.requests()) != n {
		t.Fatalf("a refused argument sent a request: %v", f.requests())
	}
}

func TestFolderRename(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PATCH", accFolder(testFolder), doc(http.StatusOK, folder(testFolder, "-New name")))
	res := f.runAcc(FolderRename, testFolder, "--", "-New name")
	wantCode(t, res, 0)
	f.wantCalls("PATCH " + accFolder(testFolder))
	if body := f.bodyOf(0); len(body) != 1 || body["name"] != "-New name" {
		t.Fatalf("body %v", body)
	}
	if !hasLine(res.stdout, "name=-New name") {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestFolderRenameRefusals(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.runAcc(FolderRename, testFolder, "a/b"), 2)
	wantCode(t, f.runAcc(FolderRename, testFolder, ""), 2)
	wantCode(t, f.runAcc(FolderRename, testFolder), 2)
	wantCode(t, f.runAcc(FolderRename, "New", testFolder), 2) // arguments swapped
	f.wantNoCalls()
	f.on("PATCH", accFolder(testFolder), apiErr(http.StatusConflict, "conflict", "name in use"))
	wantCode(t, f.runAcc(FolderRename, testFolder, "x"), 3)
}

func TestFolderRmWithYes(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("DELETE", accFolder(testFolder), done(http.StatusNoContent))
	res := f.runAcc(FolderRm, testFolder, "--yes")
	wantCode(t, res, 0)
	f.wantCalls("DELETE " + accFolder(testFolder))
	if !hasLine(res.stdout, "target="+testFolder) || !hasLine(res.stdout, "status=done") {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestFolderRmJSON(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("DELETE", accFolder(testFolder), done(http.StatusNoContent))
	res := f.runAcc(FolderRm, testFolder, "--yes", "--json")
	wantCode(t, res, 0)
	if !strings.Contains(res.stdout, `"target": "`+testFolder+`"`) || !strings.Contains(res.stdout, `"status": "done"`) {
		t.Fatalf("stdout %q", res.stdout)
	}
}

// TestFolderRmWithoutYesAndNoTerminalRefuses: nothing at all is sent, not even the read of the name.
func TestFolderRmWithoutYesAndNoTerminalRefuses(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.runAcc(FolderRm, testFolder), 2)
	f.wantNoCalls()
}

func TestFolderRmAtATerminalTypesTheName(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accFolder(testFolder), doc(http.StatusOK, folder(testFolder, "Team A")))
	f.on("DELETE", accFolder(testFolder), done(http.StatusNoContent))
	typeAtTerminal("\n")
	wantCode(t, f.runAcc(FolderRm, testFolder), 2)
	typeAtTerminal(testFolder + "\n") // the id is not the name
	wantCode(t, f.runAcc(FolderRm, testFolder), 2)
	typeAtTerminal("team a\n") // case matters
	wantCode(t, f.runAcc(FolderRm, testFolder), 2)
	for _, c := range f.requests() {
		if strings.HasPrefix(c, "DELETE") {
			t.Fatalf("calls %v: a refused answer deleted the folder", f.requests())
		}
	}
	typeAtTerminal("Team A\n")
	wantCode(t, f.runAcc(FolderRm, testFolder), 0)
	calls := f.requests()
	if calls[len(calls)-1] != "DELETE "+accFolder(testFolder) {
		t.Fatalf("calls %v: the last must be the delete", calls)
	}
}

func TestFolderRmNameReadFailureDeletesNothing(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("DELETE", accFolder(testFolder), done(http.StatusNoContent))
	typeAtTerminal("Team A\n")
	wantCode(t, f.runAcc(FolderRm, testFolder), 4)
	for _, c := range f.requests() {
		if strings.HasPrefix(c, "DELETE") {
			t.Fatalf("calls %v: nothing may be deleted when the name could not be read", f.requests())
		}
	}
}

func TestFolderRmErrors(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.runAcc(FolderRm, testFolder, "--yes"), 4) // 404
	f.on("DELETE", accFolder(testFolder), apiErr(http.StatusForbidden, "forbidden", "owners only"))
	wantCode(t, f.runAcc(FolderRm, testFolder, "--yes"), 3)
	n := len(f.requests())
	wantCode(t, f.runAcc(FolderRm, "Team A", "--yes"), 2)
	wantCode(t, f.runAcc(FolderRm, "--yes"), 2)
	wantCode(t, f.runAcc(FolderRm, testFolder, testFolder, "--yes"), 2)
	if len(f.requests()) != n {
		t.Fatalf("a refused argument sent a request: %v", f.requests())
	}
}

func settingsPath() string { return "/api/v1/folders/" + testFolder + "/settings" }

func TestFolderSettingsSetSendsOnlyTheGivenSettings(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	updated := folder(testFolder, "Team A")
	updated["members_only"] = true
	updated["allow_links"] = false
	f.on("PATCH", settingsPath(), doc(http.StatusOK, updated))
	res := f.run(FolderSettingsSet, testFolder, "--members-only=true", "--allow-links=false")
	wantCode(t, res, 0)
	f.wantCalls("PATCH " + settingsPath())
	body := f.bodyOf(0)
	if len(body) != 2 || body["members_only"] != true || body["allow_links"] != false {
		t.Fatalf("body %v, want members_only true and allow_links false only", body)
	}
	if !hasLine(res.stdout, "members_only=true") || !hasLine(res.stdout, "allow_links=false") {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestFolderSettingsSetEachSetting(t *testing.T) {
	for flagName, key := range map[string]string{
		"members-only": "members_only", "sharing-managers-only": "sharing_managers_only",
		"no-download-for-viewers": "no_download_for_viewers", "allow-links": "allow_links",
	} {
		for _, v := range []string{"true", "false"} {
			isolate(t, testAccountKey)
			f := newFake(t)
			f.on("PATCH", settingsPath(), doc(http.StatusOK, folder(testFolder, "x")))
			wantCode(t, f.run(FolderSettingsSet, testFolder, "--"+flagName+"="+v), 0)
			if body := f.bodyOf(0); len(body) != 1 || body[key] != (v == "true") {
				t.Fatalf("--%s=%s: body %v", flagName, v, body)
			}
		}
	}
}

func TestFolderSettingsSetRefusals(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.run(FolderSettingsSet, testFolder), 2)                         // no setting
	wantCode(t, f.run(FolderSettingsSet, testFolder, "--members-only=maybe"), 2) // not a boolean
	wantCode(t, f.run(FolderSettingsSet, testFolder, "--members-only="), 2)      // empty
	wantCode(t, f.run(FolderSettingsSet, "--members-only=true"), 2)              // no folder
	wantCode(t, f.run(FolderSettingsSet, testGroup, "--members-only=true"), 2)   // not a folder id
	wantCode(t, f.run(FolderSettingsSet, testFolder, testFolder, "--allow-links=true"), 2)
	f.wantNoCalls()
}

// TestFolderSettingsSetTokenRefusalSaysTheWebDoesIt: every token is refused with a plain forbidden. The server's message
// is reported, and a device token's user is told the web interface changes a space's settings.
func TestFolderSettingsSetTokenRefusalSaysTheWebDoesIt(t *testing.T) {
	const msg = "a delegated token cannot change a space's settings"
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("PATCH", settingsPath(), apiErr(http.StatusForbidden, "forbidden", msg))
	res := f.run(FolderSettingsSet, testFolder, "--allow-links=true")
	wantCode(t, res, 3)
	f.wantBearer(testDeviceTok)
	if !strings.Contains(res.stderr, msg) || !strings.Contains(res.stderr, "web interface") {
		t.Fatalf("stderr %q: want the server's message and the hint", res.stderr)
	}
	// Under --json stderr is one JSON line, with no hint.
	res = f.run(FolderSettingsSet, testFolder, "--allow-links=true", "--json")
	wantCode(t, res, 3)
	if want := `{"error":{"code":"forbidden","message":"` + msg + ` (HTTP 403)"}}`; strings.TrimSpace(res.stderr) != want {
		t.Fatalf("stderr %q, want %q", res.stderr, want)
	}
}

// TestFolderSettingsSetOtherRefusalsHaveNoHint: a key that is refused, or a device token refused for another reason,
// is not told the web does it.
func TestFolderSettingsSetOtherRefusalsHaveNoHint(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PATCH", settingsPath(), apiErr(http.StatusForbidden, "forbidden", "managers only"))
	res := f.run(FolderSettingsSet, testFolder, "--allow-links=true")
	wantCode(t, res, 3)
	if strings.Contains(res.stderr, "web interface") {
		t.Fatalf("stderr %q has a hint for a key", res.stderr)
	}
	isolate(t, testDeviceTok)
	f = newFake(t)
	f.on("PATCH", settingsPath(), apiErr(http.StatusNotFound, "not_found", "no such folder"))
	res = f.run(FolderSettingsSet, testFolder, "--allow-links=true")
	wantCode(t, res, 4)
	if strings.Contains(res.stderr, "web interface") {
		t.Fatalf("stderr %q has a hint for a not-found answer", res.stderr)
	}
}

func TestFolderSettingsSetPersonalFolderIsRefusedByTheServer(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PATCH", settingsPath(), apiErr(http.StatusBadRequest, "invalid_request", "a personal folder has no team-space settings"))
	res := f.run(FolderSettingsSet, testFolder, "--allow-links=true")
	wantCode(t, res, 2)
	if !strings.Contains(res.stderr, "personal folder") {
		t.Fatalf("stderr %q", res.stderr)
	}
}

func hiddenPath() string { return "/api/v1/folders/" + testFolder + "/hidden" }

func TestFolderHideAndShowAgain(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("PUT", hiddenPath(), done(http.StatusNoContent))
	f.on("DELETE", hiddenPath(), done(http.StatusNoContent))
	res := f.run(FolderHide, testFolder)
	wantCode(t, res, 0)
	if !hasLine(res.stdout, "target="+testFolder) || !hasLine(res.stdout, "status=done") {
		t.Fatalf("stdout %q", res.stdout)
	}
	wantCode(t, f.run(FolderShowAgain, testFolder), 0)
	f.wantCalls("PUT "+hiddenPath(), "DELETE "+hiddenPath())
	f.wantBearer(testDeviceTok) // a terminal's sign-in may do this; the command sends it
	if f.rawBody(0) != "" || f.rawBody(1) != "" {
		t.Fatalf("a hide has no body: %q %q", f.rawBody(0), f.rawBody(1))
	}
}

func TestFolderHideErrors(t *testing.T) {
	for _, fn := range []func([]string, io.Writer, io.Writer) int{FolderHide, FolderShowAgain} {
		isolate(t, testAccountKey)
		f := newFake(t)
		wantCode(t, f.run(fn, testFolder), 4) // 404
		f.on("PUT", hiddenPath(), apiErr(http.StatusForbidden, "forbidden", "only a user can hide a team space from its own list"))
		f.on("DELETE", hiddenPath(), apiErr(http.StatusForbidden, "forbidden", "only a user can hide a team space from its own list"))
		res := f.run(fn, testFolder)
		wantCode(t, res, 3)
		if !strings.Contains(res.stderr, "only a user can hide") {
			t.Fatalf("stderr %q", res.stderr)
		}
		f.on("PUT", hiddenPath(), apiErr(http.StatusBadRequest, "invalid_request", "a personal folder cannot be hidden"))
		n := len(f.requests())
		wantCode(t, f.run(fn, "Team A"), 2)
		wantCode(t, f.run(fn), 2)
		wantCode(t, f.run(fn, testFolder, testFolder), 2)
		if len(f.requests()) != n {
			t.Fatalf("a refused argument sent a request: %v", f.requests())
		}
	}
}

func TestFolderCommandsRejectUnknownFlagsWithoutEchoingThem(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	res := f.runAcc(FolderLs, "--no-such-flag")
	wantCode(t, res, 2)
	if strings.Contains(res.stderr, "no-such-flag") {
		t.Fatalf("stderr %q echoes the flag", res.stderr)
	}
	f.wantNoCalls()
}

// TestSettingsHintNeedsAPlainForbidden: a device token refused for another reason, such as a password that must change,
// is not told the web interface changes settings.
func TestSettingsHintNeedsAPlainForbidden(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("PATCH", settingsPath(), apiErrWith(http.StatusForbidden, "forbidden", "change your password first", "password_change_required"))
	res := f.run(FolderSettingsSet, testFolder, "--allow-links=true")
	wantCode(t, res, 3)
	if strings.Contains(res.stderr, "hint") || !strings.Contains(res.stderr, "change your password first") {
		t.Fatalf("stderr %q", res.stderr)
	}
}

// TestABareSettingFlagMeansTrue: --members-only alone is true, and a value after a space is not its value.
func TestABareSettingFlagMeansTrue(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PATCH", settingsPath(), doc(http.StatusOK, folder(testFolder, "x")))
	wantCode(t, f.run(FolderSettingsSet, testFolder, "--members-only"), 0)
	if body := f.bodyOf(0); len(body) != 1 || body["members_only"] != true {
		t.Fatalf("body %v, want members_only true", body)
	}
	n := len(f.requests())
	wantCode(t, f.run(FolderSettingsSet, testFolder, "--members-only", "false"), 2)
	if len(f.requests()) != n {
		t.Fatalf("a stray argument sent a request: %v", f.requests())
	}
}

func TestFolderShowPrintsItsCounts(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	d := folder(testFolder, "Team A")
	d["files"], d["bytes"] = 12, 3400
	f.on("GET", accFolder(testFolder), doc(http.StatusOK, d))
	res := f.runAcc(FolderShow, testFolder)
	wantCode(t, res, 0)
	if !hasLine(res.stdout, "files=12") || !hasLine(res.stdout, "bytes=3400") {
		t.Fatalf("stdout %q", res.stdout)
	}
	f.set("GET", accFolder(testFolder), doc(http.StatusOK, folder(testFolder, "Team A")))
	res = f.runAcc(FolderShow, testFolder)
	if strings.Contains(res.stdout, "files=") {
		t.Fatalf("a document with no counts printed them: %q", res.stdout)
	}
}

// TestRemovalRefusesANameItCannotShowSafely: a name with a control character would reach the terminal in the question, and
// could not be typed back, so the person is told to use --yes and nothing is sent.
func TestRemovalRefusesANameItCannotShowSafely(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accFolder(testFolder), doc(http.StatusOK, folder(testFolder, "evil\x1b[31m")))
	f.on("GET", accGroup(testGroup), doc(http.StatusOK, group(testGroup, "bell\x07")))
	f.on("DELETE", accFolder(testFolder), done(http.StatusNoContent))
	f.on("DELETE", accGroup(testGroup), done(http.StatusNoContent))
	for _, run := range []func() result{
		func() result { return f.runAcc(FolderRm, testFolder) },
		func() result { return f.runAcc(GroupRm, testGroup) },
	} {
		typeAtTerminal("\n")
		res := run()
		wantCode(t, res, 2)
		if !strings.Contains(res.stderr, "--yes") || strings.ContainsAny(res.stderr, "\x1b\x07") {
			t.Fatalf("stderr %q: want the --yes advice and no control character", res.stderr)
		}
	}
	for _, c := range f.requests() {
		if strings.HasPrefix(c, "DELETE") {
			t.Fatalf("calls %v: nothing may be deleted", f.requests())
		}
	}
}
