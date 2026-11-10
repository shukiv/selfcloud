// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestMkdirMakesOneDirectory(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", "/api/v1/folders/"+testFolder+"/directories", created(node(testOther, "photos 2026", "dir")))
	res := f.run(Mkdir, "--folder", testFolder, "/photos 2026")
	wantCode(t, res, 0)
	f.wantCalls("POST /api/v1/folders/" + testFolder + "/directories")
	body := f.wantBody(0)
	if body["path"] != "/photos 2026" || body["parents"] != false {
		t.Fatalf("body %v, want the path and parents false", body)
	}
	if !strings.Contains(res.stdout, "id="+testOther+"\n") || !strings.Contains(res.stdout, "name=photos 2026\n") {
		t.Fatalf("stdout %q lacks the new directory", res.stdout)
	}
}

func TestMkdirParentsIsSentAsParents(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", "/api/v1/folders/"+testFolder+"/directories", ok(node(testOther, "b", "dir")))
	wantCode(t, f.run(Mkdir, "--folder", testFolder, "--parents", "/a/b"), 0)
	if body := f.wantBody(0); body["parents"] != true || body["path"] != "/a/b" {
		t.Fatalf("body %v", body)
	}
}

func TestMkdirExistingNameIsExit3(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", "/api/v1/folders/"+testFolder+"/directories", apiErr(http.StatusConflict, "name_taken", "a node has this name"))
	wantCode(t, f.run(Mkdir, "--folder", testFolder, "/x"), 3)
}

func TestMkdirJSONIsTheNodeDocument(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", "/api/v1/folders/"+testFolder+"/directories", created(node(testOther, "x", "dir")))
	res := f.run(Mkdir, "--folder", testFolder, "--json", "/x")
	wantCode(t, res, 0)
	if !strings.Contains(res.stdout, `"kind": "dir"`) {
		t.Fatalf("--json stdout %q", res.stdout)
	}
}

func TestMkdirRefusesTheRootAndMissingFolder(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.run(Mkdir, "--folder", testFolder, "/"), 2)
	res := f.run(Mkdir, "/x") // no folder
	wantCode(t, res, 2)
	if !strings.Contains(res.stderr, "--folder") {
		t.Fatalf("stderr %q: a path without --folder must say so", res.stderr)
	}
	wantCode(t, f.run(Mkdir, "--folder", testFolder), 2)
	f.wantNoCalls()
}

// TestMkdirAndTouchRefuseHostileNames: the same name rules as ls, before any call.
func TestMkdirAndTouchRefuseHostileNames(t *testing.T) {
	isolate(t, testAccountKey)
	for _, p := range []string{"/..", "/a/../b", "/a//b", "/a/\x00b", "/ x", "x", "docs/x", "dir", "/a\\b", "/a/‮b"} {
		f := newFake(t)
		if res := f.run(Mkdir, "--folder", testFolder, p); res.code != 2 {
			t.Errorf("mkdir %q: exit %d, want 2", p, res.code)
		}
		f.wantNoCalls()
		f2 := newFake(t)
		if res := f2.run(Touch, "--folder", testFolder, p); res.code != 2 {
			t.Errorf("touch %q: exit %d, want 2", p, res.code)
		}
		f2.wantNoCalls()
	}
}

// TestMkdirSendsTheHostileButValidNameUnchanged: a name that only looks odd (CON, a name with a dot-dot inside, a name
// that is an id) is a valid name for the server and is sent as it is.
func TestMkdirSendsValidOddNamesUnchanged(t *testing.T) {
	isolate(t, testAccountKey)
	for _, name := range []string{"CON", "a..b", "nod_" + strings.Repeat("A", 26), "-rf", "x.", "é"} {
		f := newFake(t)
		f.on("POST", "/api/v1/folders/"+testFolder+"/directories", created(node(testOther, name, "dir")))
		if res := f.run(Mkdir, "--folder", testFolder, "/"+name); res.code != 0 {
			t.Errorf("mkdir /%s: exit %d, stderr %q", name, res.code, res.stderr)
			continue
		}
		if body := f.wantBody(0); body["path"] != "/"+name {
			t.Errorf("mkdir sent %v for /%s", body["path"], name)
		}
	}
}

func TestTouchCreatesAnEmptyFile(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	q := url.Values{"path": {"/docs/empty.txt"}}
	f.on("POST", "/api/v1/folders/"+testFolder+"/files", created(node(testOther, "empty.txt", "file")))
	res := f.run(Touch, "--folder", testFolder, "/docs/empty.txt")
	wantCode(t, res, 0)
	f.wantCalls("POST /api/v1/folders/" + testFolder + "/files?" + q.Encode())
	if got := f.bodies[0]; got != "" {
		t.Fatalf("touch sent a body %q; want none", got)
	}
}

func TestTouchParentsAddsTheFlag(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", "/api/v1/folders/"+testFolder+"/files", created(node(testOther, "x", "file")))
	wantCode(t, f.run(Touch, "--folder", testFolder, "--parents", "/a/x"), 0)
	if !strings.Contains(f.requests()[0], "parents=true") {
		t.Fatalf("calls %v: want parents=true", f.requests())
	}
}

func TestTouchExistingFileIsExit3(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", "/api/v1/folders/"+testFolder+"/files", apiErr(http.StatusConflict, "name_taken", "a node has this name"))
	wantCode(t, f.run(Touch, "--folder", testFolder, "/x"), 3)
}
