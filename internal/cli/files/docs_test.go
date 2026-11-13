// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// docNode is a node document for a new document.
func docNode(name string) map[string]any {
	n := node(testNode, name, "file")
	n["size"] = 1200
	return n
}

// templateFile is an account template in GET /api/v1/templates.
func templateFile(name string) map[string]any {
	return map[string]any{"id": testOther, "name": name, "kind": "document", "source": "account",
		"node_id": testOther, "has_thumbnail": false}
}

// builtinTemplate is a built-in template in GET /api/v1/templates.
func builtinTemplate(name string) map[string]any {
	return map[string]any{"id": "builtin:letter", "name": name, "kind": "document", "source": "builtin",
		"category": "letter", "has_thumbnail": true}
}

func TestDocNewBlankSendsTypeAndPrintsTheNode(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("POST", nodePath(testDest, "/documents"), created(docNode("Report.docx")))
	res := f.run(DocNew, testDest, "--type", "docx")
	wantCode(t, res, 0)
	f.wantCalls("POST " + nodePath(testDest, "/documents"))
	if got, want := f.wantBody(0), map[string]any{"type": "docx"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("body %v, want %v", got, want)
	}
	if !strings.Contains(res.stdout, "id="+testNode+"\n") || !strings.Contains(res.stdout, "name=Report.docx\n") {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestDocNewFromBuiltinTemplateSendsTheTemplateAndName(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("POST", nodePath(testDest, "/documents"), created(docNode("Mine.docx")))
	res := f.run(DocNew, testDest, "--template", "builtin:letter", "--name", "Mine")
	wantCode(t, res, 0)
	f.wantCalls("POST " + nodePath(testDest, "/documents"))
	if got, want := f.wantBody(0), map[string]any{"template": "builtin:letter", "name": "Mine"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("body %v, want %v", got, want)
	}
}

func TestDocNewFromFileTemplateSendsOnlyTheTemplate(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("POST", nodePath(testDest, "/documents"), created(docNode("Copy.docx")))
	res := f.run(DocNew, testDest, "--template", testOther)
	wantCode(t, res, 0)
	if got, want := f.wantBody(0), map[string]any{"template": testOther}; !reflect.DeepEqual(got, want) {
		t.Fatalf("body %v, want %v", got, want)
	}
}

func TestDocNewFileDirectoryKeepsTheServersMessage(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("POST", nodePath(testDest, "/documents"),
		apiErr(http.StatusBadRequest, "invalid", "a document goes in a folder"))
	res := f.run(DocNew, testDest, "--type", "txt")
	if res.code == 0 {
		t.Fatalf("exit 0, want a refusal; stdout %q", res.stdout)
	}
	f.wantCalls("POST " + nodePath(testDest, "/documents"))
	if !strings.Contains(res.stderr, "a document goes in a folder") {
		t.Fatalf("stderr %q: want the server's message", res.stderr)
	}
}

func TestDocNewUsageErrorsCostNoCall(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	for _, args := range [][]string{
		{},
		{testDest},
		{testDest, "--type", "txt", "--template", "builtin:letter"},
		{testDest, "--type", "doc"},
		{testDest, "--template", "other"},
		{testDest, "--template", "builtin:"},
		{testDest, "--type="},
		{testDest, "--type", "txt", "--name", "bad/name"},
		{testDest, "--type", "txt", "--name="},
		{testDest, "--type", "txt", "--name", ".."},
		{testFolder, "--type", "txt"},
		{testDest, testOther, "--type", "txt"},
	} {
		wantCode(t, f.run(DocNew, args...), 2)
	}
	f.wantNoCalls()
}

func TestDocNewOfficeOffGetsTheHint(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("POST", nodePath(testDest, "/documents"), apiErr(http.StatusNotFound, "not_found", "no such node"))
	res := f.run(DocNew, testDest, "--type", "docx")
	wantCode(t, res, 4)
	if !strings.Contains(res.stderr, "no such node") {
		t.Fatalf("stderr %q: want the server's message", res.stderr)
	}
	if !strings.HasSuffix(res.stderr, "selfcloud: hint: office documents may be off on this server; --type txt needs no office\n") {
		t.Fatalf("stderr %q: want the office hint after the error", res.stderr)
	}
}

func TestDocNewTextHasNoOfficeHint(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("POST", nodePath(testDest, "/documents"), apiErr(http.StatusNotFound, "not_found", "no such node"))
	res := f.run(DocNew, testDest, "--type", "txt")
	wantCode(t, res, 4)
	if strings.Contains(res.stderr, "hint:") {
		t.Fatalf("stderr %q: want no hint", res.stderr)
	}
}

func TestDocNewJSONHasNoOfficeHint(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("POST", nodePath(testDest, "/documents"), apiErr(http.StatusNotFound, "not_found", "no such node"))
	res := f.run(DocNew, testDest, "--type", "docx", "--json")
	wantCode(t, res, 4)
	if strings.Contains(res.stderr, "hint:") {
		t.Fatalf("stderr %q: want no hint under --json", res.stderr)
	}
}

func TestDocNewPlainPrintsOnlyItsOwnFields(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	n := docNode("Report.docx")
	n["token"] = "sck_secret_value"
	n["hash"] = "hh"
	f.on("POST", nodePath(testDest, "/documents"), created(n))
	res := f.run(DocNew, testDest, "--type", "docx")
	wantCode(t, res, 0)
	if strings.Contains(res.stdout, "sck_secret_value") || strings.Contains(res.stdout, "hash") {
		t.Fatalf("stdout %q: a field the output does not name was printed", res.stdout)
	}
}

func TestDocNewJSONPrintsTheAPIsDocument(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	n := docNode("Report.docx")
	f.on("POST", nodePath(testDest, "/documents"), created(n))
	res := f.run(DocNew, testDest, "--type", "docx", "--json")
	wantCode(t, res, 0)
	var got map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &got); err != nil || got["id"] != testNode {
		t.Fatalf("stdout %q: %v", res.stdout, err)
	}
}

func TestTemplateLsPlainListsTheFirstPageAndNotesMore(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("GET", "/api/v1/templates", page("c2", builtinTemplate("letter.docx"), templateFile("mine.docx")))
	res := f.run(TemplateLs, "--kind", "document")
	wantCode(t, res, 0)
	f.wantCalls("GET /api/v1/templates?kind=document")
	if want := "selfcloud: more items exist; --all prints every page\n"; res.stderr != want {
		t.Fatalf("stderr %q, want %q", res.stderr, want)
	}
	want := table([]string{"ID", "KIND", "SOURCE", "CATEGORY", "NAME"},
		[]string{"builtin:letter", "document", "builtin", "letter", "letter.docx"},
		[]string{testOther, "document", "account", "", "mine.docx"})
	if res.stdout != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", res.stdout, want)
	}
}

func TestTemplateLsAllFollowsAfterAndNeverCursor(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("GET", "/api/v1/templates",
		page("c2", builtinTemplate("letter.docx")),
		page("", templateFile("mine.docx")))
	res := f.run(TemplateLs, "--kind", "document", "--all")
	wantCode(t, res, 0)
	f.wantCalls("GET /api/v1/templates?kind=document", "GET /api/v1/templates?after=c2&kind=document")
	if res.stderr != "" {
		t.Fatalf("stderr %q: want no note for the last page", res.stderr)
	}
	want := table([]string{"ID", "KIND", "SOURCE", "CATEGORY", "NAME"},
		[]string{"builtin:letter", "document", "builtin", "letter", "letter.docx"},
		[]string{testOther, "document", "account", "", "mine.docx"})
	if res.stdout != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", res.stdout, want)
	}
}

func TestTemplateLsFollowsAShortPageWithACursor(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("GET", "/api/v1/templates",
		page("scan-2", templateFile("one.docx")),
		page("", templateFile("two.docx")))
	res := f.run(TemplateLs, "--kind", "document", "--all")
	wantCode(t, res, 0)
	f.wantCalls("GET /api/v1/templates?kind=document", "GET /api/v1/templates?after=scan-2&kind=document")
	if !strings.Contains(res.stdout, "one.docx") || !strings.Contains(res.stdout, "two.docx") {
		t.Fatalf("stdout %q: want both pages", res.stdout)
	}
}

func TestTemplateLsSendsOnlyTheGivenFlags(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("GET", "/api/v1/templates", page("", templateFile("mine.docx")))
	res := f.run(TemplateLs, "--kind", "spreadsheet", "--limit", "2")
	wantCode(t, res, 0)
	f.wantCalls("GET /api/v1/templates?kind=spreadsheet&limit=2")
}

func TestTemplateLsUsageErrorsCostNoCall(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	for _, args := range [][]string{
		{},
		{"--kind", "document", "extra"},
		{"--kind", "other"},
		{"--kind", "document", "--limit", "0"},
		{"--kind", "document", "--limit", "101"},
		{"--kind", "document", "--limit", "two"},
	} {
		wantCode(t, f.run(TemplateLs, args...), 2)
	}
	f.wantNoCalls()
}

func TestTemplateLsJSONPrintsOnlyTheKnownFields(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	builtin := builtinTemplate("letter.docx")
	builtin["token"] = "sck_secret_value"
	account := templateFile("mine.docx")
	account["hash"] = "hh"
	f.on("GET", "/api/v1/templates", page("", builtin, account))
	res := f.run(TemplateLs, "--kind", "document", "--json")
	wantCode(t, res, 0)
	var items []map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &items); err != nil {
		t.Fatalf("stdout %q: %v", res.stdout, err)
	}
	if len(items) != 2 {
		t.Fatalf("items %v", items)
	}
	for i, allowed := range []map[string]bool{
		{"id": true, "name": true, "kind": true, "source": true, "category": true, "has_thumbnail": true},
		{"id": true, "name": true, "kind": true, "source": true, "node_id": true, "has_thumbnail": true},
	} {
		for k := range items[i] {
			if !allowed[k] {
				t.Errorf("item %d holds %q, which the view does not name", i, k)
			}
		}
	}
	if items[0]["has_thumbnail"] != true || items[1]["has_thumbnail"] != false {
		t.Fatalf("items %v: want the thumbnails as the API wrote them", items)
	}
}
