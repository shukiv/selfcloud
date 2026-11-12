// SPDX-License-Identifier: AGPL-3.0-or-later

package ops

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

const (
	importsRoute = "/api/v1/imports"
	userHintText = "imports belong to a signed-in person; an account key or an admin key cannot use them"
)

func impRoute(imp, suffix string) string { return importsRoute + "/" + imp + suffix }
func archiveRoute() string               { return "/api/v1/accounts/" + testAcc + "/imports/archive" }

// importDoc returns an import as the API answers it.
func importDoc(extra map[string]any) map[string]any {
	d := map[string]any{
		"id": testImport, "source": "archive", "state": "running", "folder": testFolder, "parent": testNode, "on_conflict": "rename",
		"format": "zip", "entries": 12, "declared_bytes": 4096, "files": 10, "bytes": 2048, "skipped": 1, "job": testJob,
		"created_at": "2026-11-01T10:00:00Z", "updated_at": "2026-11-01T10:05:00Z", "finished_at": nil,
	}
	for k, v := range extra {
		d[k] = v
	}
	return d
}

func itemDoc(seq int, extra map[string]any) map[string]any {
	d := map[string]any{"seq": seq, "name": "a/b.txt", "kind": "file", "state": "done", "node": testNode, "bytes": 5, "error_code": nil}
	for k, v := range extra {
		d[k] = v
	}
	return d
}

type importCommand struct {
	name string
	run  func([]string, io.Writer, io.Writer) int
	args []string
}

// importCommands are the commands that call a route of the signed-in person's imports.
func importCommands() []importCommand {
	return []importCommand{
		{"import sources", ImportSources, nil},
		{"import start", ImportStart, []string{"--folder", testFolder, "--upload", testUpload}},
		{"import show", ImportShow, []string{testImport}},
		{"import items", ImportItems, []string{testImport}},
		{"import pause", ImportPause, []string{testImport}},
		{"import resume", ImportResume, []string{testImport}},
		{"import cancel", ImportCancel, []string{testImport, "--yes"}},
	}
}

// TestImportCommandsSendOneRequestWithTheCredential.
func TestImportCommandsSendOneRequestWithTheCredential(t *testing.T) {
	for _, c := range importCommands() {
		t.Run(c.name, func(t *testing.T) {
			isolate(t, testDeviceTok)
			f := newFake(t)
			res := f.run(c.run, c.args...)
			if res.code == 2 {
				t.Fatalf("exit 2: %q", res.stderr)
			}
			if len(f.requests()) != 1 {
				t.Fatalf("calls %v, want one", f.requests())
			}
			f.wantBearer(testDeviceTok)
		})
	}
}

// TestAnImportCommandNeedsNoAccount: an import belongs to the person, so no import command but the archive switch names
// an account, and an account in the environment is not sent.
func TestAnImportCommandNeedsNoAccount(t *testing.T) {
	isolate(t, testDeviceTok)
	t.Setenv("SELFCLOUD_ACCOUNT", "acc_junk")
	f := newFake(t)
	f.on("GET", importsRoute+"/sources", doc(200, map[string]any{"sources": []map[string]any{{"source": "archive", "enabled": true}}}))
	wantCode(t, f.run(ImportSources), 0)
	f.wantCalls("GET " + importsRoute + "/sources")
}

// TestImportSourcesListsTheSources.
func TestImportSourcesListsTheSources(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("GET", importsRoute+"/sources", doc(200, map[string]any{"sources": []map[string]any{{"source": "archive", "enabled": false}}}))
	res := f.run(ImportSources)
	wantCode(t, res, 0)
	if res.stdout != table([]string{"SOURCE", "ENABLED"}, []string{"archive", "false"}) {
		t.Fatalf("stdout %q", res.stdout)
	}
	res = f.run(ImportSources, "--json")
	wantCode(t, res, 0)
	var got struct {
		Sources []struct {
			Source  string `json:"source"`
			Enabled bool   `json:"enabled"`
		} `json:"sources"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &got); err != nil || len(got.Sources) != 1 || got.Sources[0].Source != "archive" {
		t.Fatalf("--json: %v, %q", err, res.stdout)
	}
	wantCode(t, f.run(ImportSources, "extra"), 2)
}

// TestImportStartSendsTheArchiveAndTheTarget: the body holds what was given, the defaults are the server's, and the
// answer is printed as an import.
func TestImportStartSendsTheArchiveAndTheTarget(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("POST", importsRoute, doc(202, importDoc(map[string]any{"state": "queued"})))
	res := f.run(ImportStart, "--folder", testFolder, "--upload", testUpload)
	wantCode(t, res, 0)
	f.wantCalls("POST " + importsRoute)
	if got := f.rawBody(0); got != `{"folder":"`+testFolder+`","upload":"`+testUpload+`"}` {
		t.Fatalf("body %s", got)
	}
	for _, want := range []string{"id=" + testImport + "\n", "state=queued\n", "folder=" + testFolder + "\n", "entries=12\n", "declared_bytes=4096\n"} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("stdout lacks %q: %q", want, res.stdout)
		}
	}

	f = newFake(t)
	f.on("POST", importsRoute, doc(202, importDoc(nil)))
	wantCode(t, f.run(ImportStart, "--folder", testFolder, "--upload", testUpload, "--parent", testNode, "--on-conflict", "replace",
		"--source", "archive"), 0)
	body := f.bodyOf(0)
	if body["parent"] != testNode || body["on_conflict"] != "replace" || body["source"] != "archive" {
		t.Fatalf("body %v", body)
	}
}

// TestImportStartRefusesBadInputBeforeAnyRequest.
func TestImportStartRefusesBadInputBeforeAnyRequest(t *testing.T) {
	cases := map[string][]string{
		"no folder":        {"--upload", testUpload},
		"no upload":        {"--folder", testFolder},
		"folder is a node": {"--folder", testNode, "--upload", testUpload},
		"upload is a job":  {"--folder", testFolder, "--upload", testJob},
		"bad parent":       {"--folder", testFolder, "--upload", testUpload, "--parent", testFolder},
		"bad conflict":     {"--folder", testFolder, "--upload", testUpload, "--on-conflict", "overwrite"},
		"empty source":     {"--folder", testFolder, "--upload", testUpload, "--source", ""},
		"extra argument":   {"--folder", testFolder, "--upload", testUpload, "more"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			isolate(t, testDeviceTok)
			f := newFake(t)
			wantCode(t, f.run(ImportStart, args...), 2)
			f.wantNoCalls()
		})
	}
}

// TestImportShowAndTheStateChangesPrintTheImport: show, pause and resume print the import, and refuse a bad id first.
func TestImportShowAndTheStateChangesPrintTheImport(t *testing.T) {
	cases := []struct {
		name   string
		run    func([]string, io.Writer, io.Writer) int
		method string
		route  string
		status int
	}{
		{"show", ImportShow, "GET", impRoute(testImport, ""), 200},
		{"pause", ImportPause, "POST", impRoute(testImport, "/pause"), 200},
		{"resume", ImportResume, "POST", impRoute(testImport, "/resume"), 200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			isolate(t, testDeviceTok)
			f := newFake(t)
			f.on(c.method, c.route, doc(c.status, importDoc(map[string]any{"state": "paused", "pause_reason": "user"})))
			res := f.run(c.run, testImport)
			wantCode(t, res, 0)
			f.wantCalls(c.method + " " + c.route)
			for _, want := range []string{"id=" + testImport + "\n", "state=paused\n", "pause_reason=user\n", "bytes=2048\n", "files=10\n", "job=" + testJob + "\n"} {
				if !strings.Contains(res.stdout, want) {
					t.Errorf("stdout lacks %q: %q", want, res.stdout)
				}
			}
			res = f.run(c.run, testImport, "--json")
			wantCode(t, res, 0)
			var got map[string]any
			if err := json.Unmarshal([]byte(res.stdout), &got); err != nil || got["id"] != testImport || got["state"] != "paused" {
				t.Fatalf("--json: %v, %q", err, res.stdout)
			}
			wantCode(t, f.run(c.run), 2)
			wantCode(t, f.run(c.run, "imp_short"), 2)
			wantCode(t, f.run(c.run, testUpload), 2)
			f.set(c.method, c.route, apiErrWith(409, "conflict", "the import is running; this action does not apply", "state"))
			wantCode(t, f.run(c.run, testImport), 3)
			f.set(c.method, c.route, apiErr(404, "not_found", "no such import"))
			wantCode(t, f.run(c.run, testImport), 4)
		})
	}
}

// TestImportShowOmitsWhatTheAPIOmits: a pause reason or an error code that the API leaves out is an empty value in the
// plain output and no key in JSON.
func TestImportShowOmitsWhatTheAPIOmits(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("GET", impRoute(testImport, ""), doc(200, importDoc(nil)))
	res := f.run(ImportShow, testImport, "--json")
	wantCode(t, res, 0)
	if strings.Contains(res.stdout, "pause_reason") || strings.Contains(res.stdout, "error_code") {
		t.Fatalf("stdout %q", res.stdout)
	}
	res = f.run(ImportShow, testImport)
	if !strings.Contains(res.stdout, "error_code=\n") || !strings.Contains(res.stdout, "finished_at=\n") {
		t.Fatalf("stdout %q", res.stdout)
	}
}

// TestImportItemsPagesBySequence: an item list is read after a sequence number, and --all follows next until it is null.
func TestImportItemsPagesBySequence(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	route := impRoute(testImport, "/items")
	f.on("GET", route,
		doc(200, map[string]any{"items": []map[string]any{itemDoc(1, nil), itemDoc(2, map[string]any{"state": "failed", "node": nil, "error_code": "name_invalid"})}, "next": 2}),
		doc(200, map[string]any{"items": []map[string]any{itemDoc(3, nil)}, "next": nil}))
	res := f.run(ImportItems, testImport)
	wantCode(t, res, 0)
	f.wantCalls("GET " + route)
	want := table([]string{"SEQ", "NAME", "KIND", "STATE", "BYTES", "NODE", "ERROR"},
		[]string{"1", "a/b.txt", "file", "done", "5", testNode, ""},
		[]string{"2", "a/b.txt", "file", "failed", "5", "", "name_invalid"})
	if res.stdout != want {
		t.Fatalf("stdout:\n%q\nwant\n%q", res.stdout, want)
	}
	if !strings.Contains(res.stderr, "more items exist") {
		t.Fatalf("stderr %q", res.stderr)
	}

	f = newFake(t)
	f.on("GET", route,
		doc(200, map[string]any{"items": []map[string]any{itemDoc(1, nil), itemDoc(2, nil)}, "next": 2}),
		doc(200, map[string]any{"items": []map[string]any{itemDoc(3, nil)}, "next": nil}))
	res = f.run(ImportItems, testImport, "--all", "--limit", "2", "--json")
	wantCode(t, res, 0)
	f.wantCalls("GET "+route+"?limit=2", "GET "+route+"?after=2&limit=2")
	var got struct {
		Items []map[string]any `json:"items"`
		Next  *int             `json:"next"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &got); err != nil || len(got.Items) != 3 || got.Next != nil {
		t.Fatalf("--json: %v, %q", err, res.stdout)
	}

	f = newFake(t)
	f.on("GET", route, doc(200, map[string]any{"items": []map[string]any{}, "next": nil}))
	wantCode(t, f.run(ImportItems, testImport, "--after", "7"), 0)
	f.wantCalls("GET " + route + "?after=7")
}

// TestImportItemsStopsWhenNextDoesNotMove: a server whose next never advances does not make --all loop for ever.
func TestImportItemsStopsWhenNextDoesNotMove(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	route := impRoute(testImport, "/items")
	f.on("GET", route, doc(200, map[string]any{"items": []map[string]any{itemDoc(5, nil)}, "next": 5}))
	res := f.run(ImportItems, testImport, "--all", "--after", "5")
	wantCode(t, res, 1)
	if len(f.requests()) > 2 {
		t.Fatalf("calls %v", f.requests())
	}
}

// TestImportItemsRefusesBadFlagsBeforeAnyRequest.
func TestImportItemsRefusesBadFlagsBeforeAnyRequest(t *testing.T) {
	for _, args := range [][]string{
		{testImport, "--limit", "0"}, {testImport, "--limit", "501"}, {testImport, "--limit", "x"},
		{testImport, "--after", "-1"}, {testImport, "--after", "x"}, {}, {"imp_x"},
	} {
		isolate(t, testDeviceTok)
		f := newFake(t)
		wantCode(t, f.run(ImportItems, args...), 2)
		f.wantNoCalls()
	}
}

// TestImportCancelAsksFirst: at a terminal the person types the import's id; --yes skips; no terminal and no --yes refuses.
func TestImportCancelAsksFirst(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("POST", impRoute(testImport, "/cancel"), doc(200, importDoc(map[string]any{"state": "cancelled"})))
	wantCode(t, f.run(ImportCancel, testImport), 2)
	f.wantNoCalls()
	typeAtTerminal("no\n")
	res := f.run(ImportCancel, testImport)
	wantCode(t, res, 2)
	f.wantNoCalls()
	if !strings.Contains(res.stderr, "Type "+testImport) {
		t.Fatalf("stderr %q", res.stderr)
	}
	typeAtTerminal(testImport + "\n")
	res = f.run(ImportCancel, testImport)
	wantCode(t, res, 0)
	f.wantCalls("POST " + impRoute(testImport, "/cancel"))
	if !strings.Contains(res.stdout, "state=cancelled\n") {
		t.Fatalf("stdout %q", res.stdout)
	}
	wantCode(t, f.run(ImportCancel, "imp_short", "--yes"), 2)
}

// TestTheDeviceRefusalIsShownAsTheServerSaysIt: starting, resuming and cancelling are the browser's for a device token; the
// refusal is exit 3 and its words are the server's.
func TestTheDeviceRefusalIsShownAsTheServerSaysIt(t *testing.T) {
	for _, c := range importCommands() {
		if c.name != "import start" && c.name != "import resume" && c.name != "import cancel" {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			isolate(t, testDeviceTok)
			f := newFake(t)
			for _, r := range []string{importsRoute, impRoute(testImport, "/resume"), impRoute(testImport, "/cancel")} {
				f.set("POST", r, apiErrWith(403, "forbidden", "a terminal's sign-in does not make this; use the browser", "device_token"))
			}
			res := f.run(c.run, c.args...)
			wantCode(t, res, 3)
			if !strings.Contains(res.stderr, "use the browser") || strings.Contains(res.stderr, "hint") {
				t.Fatalf("stderr %q", res.stderr)
			}
		})
	}
}

// TestAKeyIsToldImportsAreForASignedInPerson: the import routes refuse an account key and an admin key with a 403; plain
// output says why. Not under --json, not for a device token, not for another status.
func TestAKeyIsToldImportsAreForASignedInPerson(t *testing.T) {
	for _, tok := range []string{testAccountKey, testAdminKey} {
		for _, c := range importCommands() {
			t.Run(c.name, func(t *testing.T) {
				isolate(t, tok)
				f := newFake(t)
				for _, r := range []string{importsRoute + "/sources", impRoute(testImport, ""), impRoute(testImport, "/items")} {
					f.set("GET", r, apiErr(403, "forbidden", "imports are for a signed-in user"))
				}
				for _, r := range []string{importsRoute, impRoute(testImport, "/pause"), impRoute(testImport, "/resume"), impRoute(testImport, "/cancel")} {
					f.set("POST", r, apiErr(403, "forbidden", "imports are for a signed-in user"))
				}
				res := f.run(c.run, c.args...)
				wantCode(t, res, 3)
				if !strings.Contains(res.stderr, userHintText) {
					t.Fatalf("stderr %q lacks the hint", res.stderr)
				}
				res = f.run(c.run, append(append([]string{}, c.args...), "--json")...)
				wantCode(t, res, 3)
				if strings.Contains(res.stderr, "hint") {
					t.Fatalf("--json stderr %q", res.stderr)
				}
			})
		}
	}
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.set("GET", impRoute(testImport, ""), apiErr(403, "forbidden", "nope"))
	if res := f.run(ImportShow, testImport); strings.Contains(res.stderr, "hint") {
		t.Fatalf("a device token got the hint: %q", res.stderr)
	}
	isolate(t, testAccountKey)
	f = newFake(t)
	if res := f.run(ImportShow, testImport); res.code != 4 || strings.Contains(res.stderr, "hint") {
		t.Fatalf("a 404 for a key: exit %d, stderr %q", res.code, res.stderr)
	}
}

// TestImportArchiveSetSwitchesTheSource: on and off set enabled; the answer is printed; the account is checked first.
func TestImportArchiveSetSwitchesTheSource(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PUT", archiveRoute(), doc(200, map[string]any{"source": "archive", "enabled": true}), doc(200, map[string]any{"source": "archive", "enabled": false}))
	res := f.runAcc(ImportArchiveSet, "on")
	wantCode(t, res, 0)
	if got := f.rawBody(0); got != `{"enabled":true}` {
		t.Fatalf("body %s", got)
	}
	if res.stdout != "source=archive\nenabled=true\n" {
		t.Fatalf("stdout %q", res.stdout)
	}
	res = f.runAcc(ImportArchiveSet, "off", "--json")
	wantCode(t, res, 0)
	if got := f.rawBody(1); got != `{"enabled":false}` {
		t.Fatalf("body %s", got)
	}
	if !strings.Contains(res.stdout, `"enabled": false`) {
		t.Fatalf("stdout %q", res.stdout)
	}
	f.wantBearer(testAccountKey)
}

// TestImportArchiveSetChecksBeforeAnyRequest.
func TestImportArchiveSetChecksBeforeAnyRequest(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.run(ImportArchiveSet, "on"), 2)
	for _, bad := range []string{"acc_short", testFolder, testAcc + "/x"} {
		wantCode(t, f.run(ImportArchiveSet, "on", "--account", bad), 2)
	}
	for _, args := range [][]string{{}, {"maybe"}, {"ON"}, {"on", "off"}, {"true"}} {
		wantCode(t, f.runAcc(ImportArchiveSet, args...), 2)
	}
	f.wantNoCalls()
}

// TestImportArchiveSetTellsADeviceTheAccountNeedsAKey: the route is under the account, which a device token cannot reach.
func TestImportArchiveSetTellsADeviceTheAccountNeedsAKey(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	res := f.runAcc(ImportArchiveSet, "on")
	wantCode(t, res, 4)
	if !strings.Contains(res.stderr, accountHintText) {
		t.Fatalf("stderr %q", res.stderr)
	}
	isolate(t, testAccountKey)
	f = newFake(t)
	f.set("PUT", archiveRoute(), apiErr(403, "forbidden", "nope"))
	res = f.runAcc(ImportArchiveSet, "on")
	wantCode(t, res, 3)
	if strings.Contains(res.stderr, "hint") {
		t.Fatalf("the switch is an account key's, and got a hint: %q", res.stderr)
	}
}
