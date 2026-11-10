// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// trashedNode is a node in the trash, as GET /trash answers it.
func trashedNode(nodeID, name string) map[string]any {
	n := node(nodeID, name, "file")
	n["trashed_at"] = "2026-11-03T08:00:00Z"
	n["trashed_by"] = "usr_TESTUSER"
	n["original_parent_id"] = testDest
	n["purge_at"] = "2026-12-03T08:00:00Z"
	return n
}

func TestRmMovesOneNodeToTheTrash(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("DELETE", nodePath(testNode, ""), done(http.StatusNoContent))
	res := f.run(Rm, testNode, "--yes")
	wantCode(t, res, 0)
	f.wantCalls("DELETE " + nodePath(testNode, ""))
	if want := table([]string{"TARGET", "STATUS", "DETAIL"}, []string{testNode, "done", ""}); res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
}

func TestRmWithoutYesAndNotATerminalRefuses(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.run(Rm, testNode), 2)
	f.wantNoCalls()
}

// TestRmAtATerminalTypesTheID: a terminal asks for the node's ID typed back, since a name would cost a read first.
func TestRmAtATerminalTypesTheID(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("DELETE", nodePath(testNode, ""), done(http.StatusNoContent))
	typeAtTerminal("\n")
	wantCode(t, f.run(Rm, testNode), 2)
	typeAtTerminal(testOther + "\n")
	wantCode(t, f.run(Rm, testNode), 2)
	f.wantNoCalls()
	typeAtTerminal(testNode + "\n")
	wantCode(t, f.run(Rm, testNode), 0)
	f.wantCalls("DELETE " + nodePath(testNode, ""))
}

func TestRmSeveralTargetsReportsEachAndExits7WhenSomeFail(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("DELETE", nodePath(testNode, ""), done(http.StatusNoContent))
	f.on("DELETE", nodePath(testOther, ""), apiErr(http.StatusNotFound, "not_found", "no such node"))
	res := f.run(Rm, testNode, testOther, "--yes")
	wantCode(t, res, 7)
	f.wantCalls("DELETE "+nodePath(testNode, ""), "DELETE "+nodePath(testOther, ""))
	if !hasRow(res.stdout, testNode, "done") || !hasRow(res.stdout, testOther, "failed") ||
		!strings.Contains(res.stdout, "no such node") {
		t.Fatalf("stdout %q: want a row for each target", res.stdout)
	}
}

func TestRmSeveralTargetsExit1WhenAllFail(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("DELETE", nodePath(testNode, ""), apiErr(http.StatusNotFound, "not_found", "no such node"))
	f.on("DELETE", nodePath(testOther, ""), apiErr(http.StatusForbidden, "forbidden", "no access"))
	wantCode(t, f.run(Rm, testNode, testOther, "--yes"), 1)
}

// TestRmValidatesEveryTargetBeforeAnyCall: one bad target stops the whole command, so nothing is deleted.
func TestRmValidatesEveryTargetBeforeAnyCall(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.run(Rm, testNode, "/docs/a.txt", "--yes"), 2)
	wantCode(t, f.run(Rm, testNode, "nod_short", "--yes"), 2)
	wantCode(t, f.run(Rm, testFolder, "--yes"), 2)
	f.wantNoCalls()
}

func TestRmForbiddenIsExit3(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("DELETE", nodePath(testNode, ""), apiErr(http.StatusForbidden, "forbidden", "a viewer may not remove this"))
	res := f.run(Rm, testNode, "--yes")
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "a viewer may not remove this") {
		t.Fatalf("stderr %q", res.stderr)
	}
}

func TestRmJSONListsEachTarget(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("DELETE", nodePath(testNode, ""), done(http.StatusNoContent))
	res := f.run(Rm, testNode, "--yes", "--json")
	wantCode(t, res, 0)
	if !strings.Contains(res.stdout, `"target": "`+testNode+`"`) || !strings.Contains(res.stdout, `"status": "done"`) {
		t.Fatalf("--json stdout %q", res.stdout)
	}
}

func TestRmDeviceTokenIsAllowed(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("DELETE", nodePath(testNode, ""), done(http.StatusNoContent))
	wantCode(t, f.run(Rm, testNode, "--yes"), 0)
}

func TestTrashLsListsTheTrash(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", "/api/v1/trash", page("", trashedNode(testNode, "old.txt")))
	res := f.run(TrashLs)
	wantCode(t, res, 0)
	f.wantCalls("GET /api/v1/trash")
	want := table([]string{"ID", "KIND", "TRASHED", "NAME"},
		[]string{testNode, "file", "2026-11-03T08:00:00Z", "old.txt"})
	if res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
}

func TestTrashLsFolderFilterAndCursor(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", "/api/v1/trash", page("c1", trashedNode(testNode, "a")), page("", trashedNode(testOther, "b")))
	res := f.run(TrashLs, "--folder", testFolder, "--all")
	wantCode(t, res, 0)
	q := url.Values{"folder": {testFolder}}
	f.wantCalls("GET /api/v1/trash?"+q.Encode(), "GET /api/v1/trash?"+url.Values{"folder": {testFolder}, "cursor": {"c1"}}.Encode())
	if !strings.Contains(res.stdout, testNode) || !strings.Contains(res.stdout, testOther) {
		t.Fatalf("--all printed %q, want both pages", res.stdout)
	}
}

func TestTrashLsRefusesABadFolder(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.run(TrashLs, "--folder", "fld_x"), 2)
	f.wantNoCalls()
}

func TestTrashRestoreRestoresEachTarget(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", nodePath(testNode, "/restore"), ok(node(testNode, "old.txt", "file")))
	f.on("POST", nodePath(testOther, "/restore"), apiErr(http.StatusConflict, "name_taken", "a node has this name"))
	res := f.run(TrashRestore, testNode, testOther)
	wantCode(t, res, 7)
	f.wantCalls("POST "+nodePath(testNode, "/restore"), "POST "+nodePath(testOther, "/restore"))
	if !hasRow(res.stdout, testNode, "done") || !strings.Contains(res.stdout, "a node has this name") {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestTrashRestoreOneTargetDoneIsExit0(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", nodePath(testNode, "/restore"), ok(node(testNode, "old.txt", "file")))
	wantCode(t, f.run(TrashRestore, testNode), 0)
}

func TestTrashRestoreRefusesAPathBeforeAnyCall(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.run(TrashRestore, testNode, "/x"), 2)
	f.wantNoCalls()
}

func TestTrashEmptyLoopsUntilNoMore(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", "/api/v1/trash/empty",
		reply{status: http.StatusOK, body: `{"purged":2,"more":true}`},
		reply{status: http.StatusOK, body: `{"purged":1,"more":false}`})
	typeAtTerminal("empty\n")
	res := f.run(TrashEmpty)
	wantCode(t, res, 0)
	f.wantCalls("POST /api/v1/trash/empty", "POST /api/v1/trash/empty")
	if !strings.Contains(res.stdout, "purged=3") {
		t.Fatalf("stdout %q, want purged=3", res.stdout)
	}
}

func TestTrashEmptyNeedsTheConfirmation(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", "/api/v1/trash/empty", reply{status: http.StatusOK, body: `{"purged":0,"more":false}`})
	wantCode(t, f.run(TrashEmpty), 2)
	typeAtTerminal("yes\n")
	wantCode(t, f.run(TrashEmpty), 2)
	f.wantNoCalls()
	wantCode(t, f.run(TrashEmpty, "--yes", "--folder", testFolder), 0)
	f.wantCalls("POST /api/v1/trash/empty?" + url.Values{"folder": {testFolder}}.Encode())
}

func TestTrashRmPurgesOneNode(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("DELETE", "/api/v1/trash/"+testNode, done(http.StatusNoContent))
	wantCode(t, f.run(TrashRm, testNode), 2)
	f.wantNoCalls()
	typeAtTerminal(testNode + "\n")
	res := f.run(TrashRm, testNode)
	wantCode(t, res, 0)
	f.wantCalls("DELETE /api/v1/trash/" + testNode)
}

func TestShortcutAddMakesAShortcutInADirectory(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", nodePath(testDest, "/shortcuts"), created(node(testOther, "link", "shortcut")))
	res := f.run(ShortcutAdd, testDest, testNode, "--name", "link")
	wantCode(t, res, 0)
	f.wantCalls("POST " + nodePath(testDest, "/shortcuts"))
	body := f.wantBody(0)
	if body["target"] != testNode || body["name"] != "link" {
		t.Fatalf("body %v", body)
	}
	if !strings.Contains(res.stdout, "id="+testOther+"\n") {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestShortcutAddWithoutNameKeepsTheTargetsName(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", nodePath(testDest, "/shortcuts"), created(node(testOther, "a.txt", "shortcut")))
	wantCode(t, f.run(ShortcutAdd, testDest, testNode), 0)
	if body := f.wantBody(0); body["name"] != nil {
		t.Fatalf("body %v: a name was sent with none given", body)
	}
}

func TestShortcutAddRefusesPathsAndBadNames(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.run(ShortcutAdd, "/docs", testNode), 2)
	wantCode(t, f.run(ShortcutAdd, testDest, "/a.txt"), 2)
	wantCode(t, f.run(ShortcutAdd, testDest, testNode, "--name", "a/b"), 2)
	wantCode(t, f.run(ShortcutAdd, testDest), 2)
	f.wantNoCalls()
}

func TestUnlockBreaksALock(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", nodePath(testNode, "/unlock"), done(http.StatusNoContent))
	res := f.run(Unlock, testNode)
	wantCode(t, res, 0)
	f.wantCalls("POST " + nodePath(testNode, "/unlock"))
	if !hasRow(res.stdout, testNode, "done") {
		t.Fatalf("stdout %q", res.stdout)
	}
}

// TestUnlockRefusesADeviceWithTheBrowserHint: the API refuses a device token on lock break with a message and no reason,
// so the CLI adds the one fact a terminal needs: it cannot do this, the browser can.
func TestUnlockRefusesADeviceWithTheBrowserHint(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("POST", nodePath(testNode, "/unlock"), apiErr(http.StatusForbidden, "forbidden", "only a user or the account key may break a lock"))
	res := f.run(Unlock, testNode)
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "only a user or the account key may break a lock") || !strings.Contains(res.stderr, "browser") {
		t.Fatalf("stderr %q: want the API's message and the browser hint", res.stderr)
	}
}

func TestUnlockAccountKeyGetsNoHint(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", nodePath(testNode, "/unlock"), apiErr(http.StatusForbidden, "forbidden", "only a user or the account key may break a lock"))
	res := f.run(Unlock, testNode)
	wantCode(t, res, 3)
	if strings.Contains(res.stderr, "browser") {
		t.Fatalf("stderr %q: an account key gets no browser hint", res.stderr)
	}
}
