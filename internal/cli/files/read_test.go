// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
)

// lsFolder is the children route of the test folder, asked for a path, with the query the command sends.
func lsFolderCall(path, cursor string) string {
	q := url.Values{"path": {path}}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	return "GET " + folderPath() + "?" + q.Encode()
}

func TestLsPathListsTheFolderChildren(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", folderPath(), page("", node(testOther, "my file.txt", "file")))
	res := f.run(Ls, "--folder", testFolder, "/docs/my file.txt")
	wantCode(t, res, 0)
	f.wantCalls(lsFolderCall("/docs/my file.txt", ""))
	// Columns are padded by text/tabwriter: each is as wide as its longest cell plus two spaces; the last is not padded.
	want := table([]string{"ID", "KIND", "SIZE", "MODIFIED", "NAME"},
		[]string{testOther, "file", "0", "2026-11-02T10:00:00Z", "my file.txt"})
	if res.stdout != want {
		t.Fatalf("stdout:\n%q\nwant:\n%q", res.stdout, want)
	}
}

func TestLsRootNeedsOnlyTheFolder(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", folderPath(), page(""))
	wantCode(t, f.run(Ls, "--folder", testFolder), 0)
	f.wantCalls(lsFolderCall("/", ""))
	f2 := newFake(t)
	f2.on("GET", folderPath(), page(""))
	wantCode(t, f2.run(Ls, "--folder", testFolder, "/"), 0)
	f2.wantCalls(lsFolderCall("/", ""))
}

func TestLsNodeListsTheNodeChildren(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", nodePath(testNode, "/children"), page("", node(testOther, "a.txt", "file")))
	res := f.run(Ls, testNode)
	wantCode(t, res, 0)
	f.wantCalls("GET " + nodePath(testNode, "/children"))
	if !strings.Contains(res.stdout, testOther) || !strings.Contains(res.stdout, "a.txt") {
		t.Fatalf("stdout %q lacks the child", res.stdout)
	}
}

func TestLsAllFollowsTheCursor(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", folderPath(), page("c1", node(testOther, "one", "file")), page("", node(testDest, "two", "file")))
	res := f.run(Ls, "--folder", testFolder, "--all", "/")
	wantCode(t, res, 0)
	f.wantCalls(lsFolderCall("/", ""), lsFolderCall("/", "c1"))
	if !strings.Contains(res.stdout, "one") || !strings.Contains(res.stdout, "two") {
		t.Fatalf("--all printed %q, want both pages", res.stdout)
	}
}

func TestLsWithoutAllNotesThatMoreExist(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", folderPath(), page("c1", node(testOther, "one", "file")))
	res := f.run(Ls, "--folder", testFolder, "/")
	wantCode(t, res, 0)
	f.wantCalls(lsFolderCall("/", ""))
	if strings.Contains(res.stdout, "next_cursor") || !strings.Contains(res.stderr, "--all") {
		t.Fatalf("stdout %q, stderr %q: want the first page and a note about --all on stderr", res.stdout, res.stderr)
	}
}

func TestLsJSONIsTheItemsAsTheAPIWritesThem(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", folderPath(), page("", node(testOther, "a", "file")))
	res := f.run(Ls, "--folder", testFolder, "--json", "/")
	wantCode(t, res, 0)
	if !strings.HasPrefix(res.stdout, "[") || !strings.Contains(res.stdout, `"folder_id": "`+testFolder+`"`) {
		t.Fatalf("--json stdout %q is not the list of items", res.stdout)
	}
}

func TestLsRefusesAPathWithoutAFolder(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.run(Ls, "/docs"), 2)
	f.wantNoCalls()
}

// TestFilesRefuseHostileNamesBeforeAnyCall: a path is checked segment by segment against the API's name rules before
// the first request, so a refused name costs no call and writes nothing.
func TestFilesRefuseHostileNamesBeforeAnyCall(t *testing.T) {
	isolate(t, testAccountKey)
	hostile := []string{
		"/..", "/a/../b", "/./x", "/a//b", "/a/\x00b", "/a/​b", "/ lead", "/trail ", "/a\\b", "/a/\nb",
		"docs", "x/../y", "/" + strings.Repeat("n", 256),
	}
	for _, p := range hostile {
		f := newFake(t)
		res := f.run(Ls, "--folder", testFolder, p)
		if res.code != 2 {
			t.Errorf("ls %q: exit %d, want 2 (stderr %q)", p, res.code, res.stderr)
		}
		f.wantNoCalls()
	}
}

// TestNamesThatLookLikeIDsAreNames: a path always starts with a slash, so a name such as nod_... or a leading dash is a
// name, not an id or a flag.
func TestNamesThatLookLikeIDsAreNames(t *testing.T) {
	isolate(t, testAccountKey)
	idName := "/" + id.Derive("nod", "named-like-an-id")
	f := newFake(t)
	f.on("GET", folderPath(), page("", node(testOther, "x", "file")))
	wantCode(t, f.run(Ls, "--folder", testFolder, idName), 0)
	f.wantCalls(lsFolderCall(idName, ""))
	f2 := newFake(t)
	f2.on("GET", folderPath(), page(""))
	wantCode(t, f2.run(Ls, "--folder", testFolder, "/-dash name"), 0)
	f2.wantCalls(lsFolderCall("/-dash name", ""))
}

func TestNamesWithUnicodeAreSentAsGiven(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", folderPath(), page(""))
	wantCode(t, f.run(Ls, "--folder", testFolder, "/תיקייה/שלום ü"), 0)
	f.wantCalls(lsFolderCall("/תיקייה/שלום ü", ""))
}

func TestLsRefusesMalformedIDs(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.run(Ls, "nod_short"), 2)
	wantCode(t, f.run(Ls, testFolder), 2) // a folder id is not a node
	wantCode(t, f.run(Ls, "--folder", testNode, "/"), 2)
	f.wantNoCalls()
}

func TestLsNotFoundIsExit4(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", folderPath(), apiErr(http.StatusNotFound, "not_found", "no such path"))
	wantCode(t, f.run(Ls, "--folder", testFolder, "/gone"), 4)
}

func TestLsForbiddenIsExit3(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", nodePath(testNode, "/children"), apiErr(http.StatusForbidden, "forbidden", "no access"))
	wantCode(t, f.run(Ls, testNode), 3)
}

func TestLsAcceptsADeviceToken(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("GET", folderPath(), page(""))
	wantCode(t, f.run(Ls, "--folder", testFolder, "/"), 0)
	f.wantCalls(lsFolderCall("/", ""))
}

func TestStatPrintsTheNode(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", nodePath(testNode, ""), ok(node(testNode, "report.pdf", "file")))
	res := f.run(Stat, testNode)
	wantCode(t, res, 0)
	f.wantCalls("GET " + nodePath(testNode, ""))
	want := "id=" + testNode + "\n" +
		"folder_id=" + testFolder + "\n" +
		"parent_id=" + testDest + "\n" +
		"name=report.pdf\n" +
		"kind=file\n" +
		"size=0\n" +
		"mime_type=\n" +
		"modified_at=2026-11-02T10:00:00Z\n" +
		"role=manager\n"
	if res.stdout != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", res.stdout, want)
	}
}

func TestStatJSONIsTheAPIDocument(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", nodePath(testNode, ""), ok(node(testNode, "a", "dir")))
	res := f.run(Stat, testNode, "--json")
	wantCode(t, res, 0)
	if !strings.Contains(res.stdout, `"kind": "dir"`) || !strings.Contains(res.stdout, `"folder_id"`) {
		t.Fatalf("--json stdout %q", res.stdout)
	}
}

func TestStatRefusesAPathAndAFolderID(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.run(Stat, "/docs/a.txt", "--folder", testFolder), 2)
	wantCode(t, f.run(Stat, testFolder), 2)
	f.wantNoCalls()
}

func TestStatNotFoundIsExit4(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.run(Stat, testNode), 4)
}
