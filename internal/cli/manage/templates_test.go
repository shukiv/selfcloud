// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
)

var templateFolderNode = id.Derive("nod", "cli11g-template-folder")

func templatesFolderPath() string { return "/api/v1/accounts/" + testAcc + "/templates" }

func TestTemplateFolderShowPrintsNone(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", templatesFolderPath(), doc(http.StatusOK, map[string]any{"node_id": nil}))
	res := f.runAcc(TemplateFolderShow)
	wantCode(t, res, 0)
	f.wantCalls("GET " + templatesFolderPath())
	f.wantBearer(testAccountKey)
	if want := "node_id=\n"; res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
}

func TestTemplateFolderShowPrintsTheFolder(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", templatesFolderPath(), doc(http.StatusOK, map[string]any{"node_id": templateFolderNode}))
	res := f.runAcc(TemplateFolderShow)
	wantCode(t, res, 0)
	if want := "node_id=" + templateFolderNode + "\n"; res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
}

func TestTemplateFolderShowJSON(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", templatesFolderPath(), doc(http.StatusOK,
		map[string]any{"node_id": nil, "token": "sck_secret_value", "hash": "hh"}))
	res := f.runAcc(TemplateFolderShow, "--json")
	wantCode(t, res, 0)
	var got map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &got); err != nil {
		t.Fatalf("stdout %q: %v", res.stdout, err)
	}
	if want := map[string]any{"node_id": nil}; !reflect.DeepEqual(got, want) {
		t.Fatalf("stdout %q: got %v, want %v", res.stdout, got, want)
	}
}

func TestTemplateFolderSetJSON(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PUT", templatesFolderPath(), doc(http.StatusOK,
		map[string]any{"node_id": templateFolderNode, "token": "sck_secret_value", "hash": "hh"}))
	res := f.runAcc(TemplateFolderSet, templateFolderNode, "--json")
	wantCode(t, res, 0)
	var got map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &got); err != nil {
		t.Fatalf("stdout %q: %v", res.stdout, err)
	}
	if want := map[string]any{"node_id": templateFolderNode}; !reflect.DeepEqual(got, want) {
		t.Fatalf("stdout %q: got %v, want %v", res.stdout, got, want)
	}
	if strings.Contains(res.stdout, "sck_secret_value") || strings.Contains(res.stdout, "hash") {
		t.Fatalf("stdout %q: a field the view does not name was printed", res.stdout)
	}
}

func TestTemplateFolderSetSendsTheFolder(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PUT", templatesFolderPath(), doc(http.StatusOK, map[string]any{"node_id": templateFolderNode}))
	res := f.runAcc(TemplateFolderSet, templateFolderNode)
	wantCode(t, res, 0)
	f.wantCalls("PUT " + templatesFolderPath())
	f.wantBearer(testAccountKey)
	if got, want := f.bodyOf(0), map[string]any{"node_id": templateFolderNode}; !reflect.DeepEqual(got, want) {
		t.Fatalf("body %v, want %v", got, want)
	}
	if want := "node_id=" + templateFolderNode + "\n"; res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
}

func TestTemplateFolderSetClearSendsNull(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PUT", templatesFolderPath(), doc(http.StatusOK, map[string]any{"node_id": nil}))
	res := f.runAcc(TemplateFolderSet, "--clear")
	wantCode(t, res, 0)
	f.wantCalls("PUT " + templatesFolderPath())
	if want := `{"node_id":null}`; f.rawBody(0) != want {
		t.Fatalf("body %s, want %s", f.rawBody(0), want)
	}
	if want := "node_id=\n"; res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
}

func TestTemplateFolderUsageErrorsCostNoCall(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	for _, args := range [][]string{
		{},
		{"--clear", templateFolderNode},
		{"--clear", templateFolderNode, templateFolderNode},
		{"other"},
		{testAccountKey},
	} {
		wantCode(t, f.runAcc(TemplateFolderSet, args...), 2)
	}
	f.wantNoCalls()
}

func TestTemplateFolderDeviceTokenGetsTheHint(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("GET", templatesFolderPath(), apiErr(http.StatusNotFound, "not_found", "no such account"))
	res := f.runAcc(TemplateFolderShow)
	wantCode(t, res, 4)
	if !strings.Contains(res.stderr, "no such account") {
		t.Fatalf("stderr %q: want the server's message", res.stderr)
	}
	if !strings.HasSuffix(res.stderr, "selfcloud: hint: this needs an account key; a signed-in device cannot manage an account\n") {
		t.Fatalf("stderr %q: want the account hint after the error", res.stderr)
	}
}

func TestTemplateFolderHintIsNotPrintedUnderJSON(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("GET", templatesFolderPath(), apiErr(http.StatusNotFound, "not_found", "no such account"))
	res := f.runAcc(TemplateFolderShow, "--json")
	wantCode(t, res, 4)
	if strings.Contains(res.stderr, "hint:") {
		t.Fatalf("stderr %q: want no hint under --json", res.stderr)
	}
}
