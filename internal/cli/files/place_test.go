// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"net/http"
	"strings"
	"testing"
)

func TestMvMovesIntoADirectoryByID(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", nodePath(testNode, "/move"), ok(node(testNode, "a.txt", "file")))
	res := f.run(Mv, testNode, testDest)
	wantCode(t, res, 0)
	f.wantCalls("POST " + nodePath(testNode, "/move"))
	body := f.wantBody(0)
	if body["parent"] != testDest || body["path"] != nil || body["folder"] != nil || body["on_conflict"] != nil {
		t.Fatalf("body %v: want only the parent", body)
	}
	if !strings.Contains(res.stdout, "id="+testNode+"\n") {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestMvToAPathSendsThePathAndTheFolder(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", nodePath(testNode, "/move"), ok(node(testNode, "a.txt", "file")))
	wantCode(t, f.run(Mv, testNode, "/archive/2026", "--folder", testFolder), 0)
	body := f.wantBody(0)
	if body["path"] != "/archive/2026" || body["folder"] != testFolder || body["parent"] != nil {
		t.Fatalf("body %v: want the path and the folder", body)
	}
}

func TestMvToAPathWithoutAFolderLeavesTheFolderToTheServer(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", nodePath(testNode, "/move"), ok(node(testNode, "a.txt", "file")))
	wantCode(t, f.run(Mv, testNode, "/archive"), 0)
	if body := f.wantBody(0); body["folder"] != nil {
		t.Fatalf("body %v: a folder was sent with none given", body)
	}
}

func TestMvRenameOnlyPatchesTheName(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PATCH", nodePath(testNode, ""), ok(node(testNode, "new name.txt", "file")))
	res := f.run(Mv, testNode, "--name", "new name.txt")
	wantCode(t, res, 0)
	f.wantCalls("PATCH " + nodePath(testNode, ""))
	if body := f.wantBody(0); body["name"] != "new name.txt" || len(body) != 1 {
		t.Fatalf("body %v: want only the name", body)
	}
}

func TestMvIntoADirectoryUnderANewName(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", nodePath(testNode, "/move"), ok(node(testNode, "renamed", "file")))
	wantCode(t, f.run(Mv, testNode, testDest, "--name", "renamed"), 0)
	body := f.wantBody(0)
	if body["parent"] != testDest || body["name"] != "renamed" {
		t.Fatalf("body %v: want the parent and the name", body)
	}
}

func TestMvNeedsADestinationOrAName(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.run(Mv, testNode), 2)
	wantCode(t, f.run(Mv, testNode, testDest, "/extra"), 2)
	f.wantNoCalls()
}

func TestMvRefusesAPathSourceAndABadFolder(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.run(Mv, "/a.txt", testDest), 2)
	wantCode(t, f.run(Mv, testNode, "/x", "--folder", testNode), 2)
	wantCode(t, f.run(Mv, testNode, "/x", "--folder", "fld_nope"), 2)
	f.wantNoCalls()
}

// TestMvRefusesHostileDestinationNames: the destination path is checked before any call.
func TestMvRefusesHostileDestinationNames(t *testing.T) {
	isolate(t, testAccountKey)
	for _, p := range []string{"/..", "/a/./b", "/a//b", "/a/\x00", "docs"} {
		f := newFake(t)
		if res := f.run(Mv, testNode, p, "--folder", testFolder); res.code != 2 {
			t.Errorf("mv to %q: exit %d, want 2", p, res.code)
		}
		f.wantNoCalls()
	}
}

func TestMvOverAnExistingNameIsExit3UnlessReplaced(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", nodePath(testNode, "/move"), apiErr(http.StatusConflict, "name_taken", "a node has this name"))
	wantCode(t, f.run(Mv, testNode, testDest), 3)
	if body := f.wantBody(0); body["on_conflict"] != nil {
		t.Fatalf("body %v: on_conflict sent without --replace", body)
	}
}

func TestMvReplaceNeedsTheConfirmation(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", nodePath(testNode, "/move"), ok(node(testNode, "a.txt", "file")))
	wantCode(t, f.run(Mv, testNode, testDest, "--replace"), 2) // not a terminal, no --yes
	f.wantNoCalls()

	typeAtTerminal("no\n")
	wantCode(t, f.run(Mv, testNode, testDest, "--replace"), 2)
	f.wantNoCalls()

	typeAtTerminal("replace\n")
	wantCode(t, f.run(Mv, testNode, testDest, "--replace"), 0)
	f.wantCalls("POST " + nodePath(testNode, "/move"))
	if body := f.wantBody(0); body["on_conflict"] != "replace" {
		t.Fatalf("body %v: want on_conflict replace", body)
	}
}

func TestMvReplaceWithYesSkipsTheQuestion(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", nodePath(testNode, "/move"), ok(node(testNode, "a.txt", "file")))
	wantCode(t, f.run(Mv, testNode, testDest, "--replace", "--yes"), 0)
	if body := f.wantBody(0); body["on_conflict"] != "replace" {
		t.Fatalf("body %v", body)
	}
}

func TestCpCopiesIntoADirectory(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", nodePath(testNode, "/copy"), created(node(testOther, "a.txt", "file")))
	res := f.run(Cp, testNode, testDest)
	wantCode(t, res, 0)
	f.wantCalls("POST " + nodePath(testNode, "/copy"))
	if body := f.wantBody(0); body["parent"] != testDest || body["on_conflict"] != nil {
		t.Fatalf("body %v", body)
	}
	if !strings.Contains(res.stdout, "id="+testOther+"\n") {
		t.Fatalf("stdout %q should show the copy", res.stdout)
	}
}

func TestCpToAPathWithNewNameAndFolder(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", nodePath(testNode, "/copy"), created(node(testOther, "b.txt", "file")))
	wantCode(t, f.run(Cp, testNode, "/copies", "--folder", testFolder, "--name", "b.txt"), 0)
	body := f.wantBody(0)
	if body["path"] != "/copies" || body["folder"] != testFolder || body["name"] != "b.txt" {
		t.Fatalf("body %v", body)
	}
}

func TestCpReplaceNeedsTheConfirmationAndSendsReplace(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", nodePath(testNode, "/copy"), created(node(testOther, "a.txt", "file")))
	wantCode(t, f.run(Cp, testNode, testDest, "--replace"), 2)
	f.wantNoCalls()
	wantCode(t, f.run(Cp, testNode, testDest, "--replace", "--yes"), 0)
	if body := f.wantBody(0); body["on_conflict"] != "replace" {
		t.Fatalf("body %v", body)
	}
}

func TestCpOverAnExistingNameIsExit3(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", nodePath(testNode, "/copy"), apiErr(http.StatusConflict, "name_taken", "a node has this name"))
	wantCode(t, f.run(Cp, testNode, testDest), 3)
}

func TestCpNeedsADestinationAndAnIDSource(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.run(Cp, testNode), 2)
	wantCode(t, f.run(Cp, "/a.txt", testDest), 2)
	wantCode(t, f.run(Cp, testNode, testDest, "--name", "x/y"), 2)
	f.wantNoCalls()
}

func TestCpDeviceTokenIsRefusedWithTheAPIMessage(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("POST", nodePath(testNode, "/copy"), apiErr(http.StatusForbidden, "forbidden", "a device may not copy here"))
	res := f.run(Cp, testNode, testDest)
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "a device may not copy here") {
		t.Fatalf("stderr %q does not carry the API's message", res.stderr)
	}
}
