// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// accountCommand is a command that names an account, with the arguments that make it valid apart from the account.
type accountCommand struct {
	name string
	run  func([]string, io.Writer, io.Writer) int
	args []string
}

// accountCommands are the commands that call a route under /accounts/{acc}.
func accountCommands() []accountCommand {
	return []accountCommand{
		{"folder create", FolderCreate, []string{"Team"}},
		{"folder ls", FolderLs, nil},
		{"folder show", FolderShow, []string{testFolder}},
		{"folder rename", FolderRename, []string{testFolder, "New"}},
		{"folder rm", FolderRm, []string{testFolder, "--yes"}},
		{"group create", GroupCreate, []string{"G"}},
		{"group ls", GroupLs, nil},
		{"group show", GroupShow, []string{testGroup}},
		{"group rename", GroupRename, []string{testGroup, "New"}},
		{"group rm", GroupRm, []string{testGroup, "--yes"}},
		{"group members", GroupMembers, []string{testGroup}},
		{"group add-member", GroupAddMember, []string{testGroup, testUser}},
		{"group remove-member", GroupRemoveMember, []string{testGroup, testUser}},
		{"user grants", UserGrants, []string{testUser}},
		{"account show", AccountShow, nil},
		{"account update", AccountUpdate, []string{"--name", "New"}},
		{"account rm", AccountRm, []string{"--yes"}},
		{"account usage", AccountUsage, nil},
		{"account audit", AccountAudit, nil},
	}
}

// TestEveryAccountCommandChecksItsAccountBeforeAnyRequest: with no account, a flag that is not an account id, or a
// variable that is not one, each command is refused with exit 2 and sends nothing.
func TestEveryAccountCommandChecksItsAccountBeforeAnyRequest(t *testing.T) {
	for _, c := range accountCommands() {
		t.Run(c.name, func(t *testing.T) {
			isolate(t, testAccountKey)
			f := newFake(t)
			wantCode(t, f.run(c.run, c.args...), 2)
			for _, bad := range []string{"acc_short", testFolder, strings.Repeat("a", 40), testAcc + "/x"} {
				wantCode(t, f.run(c.run, append(append([]string{}, c.args...), "--account", bad)...), 2)
			}
			t.Setenv("SELFCLOUD_ACCOUNT", "acc_short")
			wantCode(t, f.run(c.run, c.args...), 2)
			f.wantNoCalls()
		})
	}
}

const accountHint = "this needs an account key; a signed-in device cannot manage an account"

// TestADeviceTokenIsToldAnAccountNeedsAKey: the server answers a device token not found on every route of an account, and
// the person is told why.
func TestADeviceTokenIsToldAnAccountNeedsAKey(t *testing.T) {
	for _, c := range accountCommands() {
		t.Run(c.name, func(t *testing.T) {
			isolate(t, testDeviceTok)
			f := newFake(t) // every route answers 404 no such thing
			res := f.runAcc(c.run, c.args...)
			wantCode(t, res, 4)
			if !strings.Contains(res.stderr, accountHint) {
				t.Fatalf("stderr %q lacks the hint", res.stderr)
			}
		})
	}
}

// TestTheAccountHintIsForADeviceTokenInPlainOutputOnly: a key is not told it, nor is --json, nor is any other failure.
func TestTheAccountHintIsForADeviceTokenInPlainOutputOnly(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	res := f.runAcc(FolderShow, testFolder)
	wantCode(t, res, 4)
	if strings.Contains(res.stderr, "hint") {
		t.Fatalf("a key got a hint: %q", res.stderr)
	}

	isolate(t, testDeviceTok)
	f = newFake(t)
	res = f.runAcc(FolderShow, testFolder, "--json")
	wantCode(t, res, 4)
	if strings.Contains(res.stderr, "hint") || !strings.HasPrefix(res.stderr, `{"error"`) {
		t.Fatalf("--json stderr %q: want only the JSON error", res.stderr)
	}

	// Another failure of the device token (forbidden, conflict) is not this one.
	f.set("POST", accFolders(), apiErr(http.StatusConflict, "conflict", "taken"))
	res = f.runAcc(FolderCreate, "x")
	wantCode(t, res, 3)
	if strings.Contains(res.stderr, "hint") {
		t.Fatalf("a conflict got the hint: %q", res.stderr)
	}

	// The routes that name no account are not told it: their 404 is a real one.
	f.set("PUT", hiddenPath(), apiErr(http.StatusNotFound, "not_found", "no such folder"))
	res = f.run(FolderHide, testFolder)
	wantCode(t, res, 4)
	if strings.Contains(res.stderr, "hint") {
		t.Fatalf("hide got the account hint: %q", res.stderr)
	}
	f.set("GET", "/api/v1/folders", apiErr(http.StatusNotFound, "not_found", "no such thing"))
	res = f.run(FolderLs, "--mine")
	wantCode(t, res, 4)
	if strings.Contains(res.stderr, "hint") {
		t.Fatalf("folder ls --mine got the account hint: %q", res.stderr)
	}
}
