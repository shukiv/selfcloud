// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// starredItem is a node in GET /starred: a node document plus starred_at, place and starred.
func starredItem(nodeID, name, starredAt string) map[string]any {
	n := node(nodeID, name, "file")
	n["size"] = 240
	n["starred_at"] = starredAt
	n["place"] = "/Docs"
	n["starred"] = true
	return n
}

// recentItem is a node in GET /recent: a node document plus viewed_at, edited_at, last_at and place.
func recentItem(nodeID, name, lastAt string) map[string]any {
	n := node(nodeID, name, "file")
	n["size"] = 240
	n["viewed_at"] = "2026-11-02T12:00:00Z"
	n["edited_at"] = nil
	n["last_at"] = lastAt
	n["place"] = "/Docs"
	return n
}

func TestStarStarsEachNode(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("PUT", nodePath(testNode, "/star"), done(http.StatusNoContent))
	res := f.run(Star, testNode)
	wantCode(t, res, 0)
	f.wantCalls("PUT " + nodePath(testNode, "/star"))
	if len(f.bodies) != 1 || f.bodies[0] != "" {
		t.Fatalf("bodies %q: want no body", f.bodies)
	}
	if want := table([]string{"TARGET", "STATUS", "DETAIL"}, []string{testNode, "done", ""}); res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
}

func TestStarSeveralTargetsReportsEachAndExits7WhenSomeFail(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("PUT", nodePath(testNode, "/star"), done(http.StatusNoContent))
	f.on("PUT", nodePath(testOther, "/star"), apiErr(http.StatusNotFound, "not_found", "no such node"))
	res := f.run(Star, testNode, testOther)
	wantCode(t, res, 7)
	f.wantCalls("PUT "+nodePath(testNode, "/star"), "PUT "+nodePath(testOther, "/star"))
	if !hasRow(res.stdout, testNode, "done") || !hasRow(res.stdout, testOther, "failed") ||
		!strings.Contains(res.stdout, "no such node") {
		t.Fatalf("stdout %q: want a row for each target", res.stdout)
	}
}

func TestStarConflictShowsTheLimitMessage(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("PUT", nodePath(testNode, "/star"),
		apiErr(http.StatusConflict, "conflict", "you may star at most 5000 items; remove some stars first"))
	res := f.run(Star, testNode)
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "you may star at most 5000 items; remove some stars first") {
		t.Fatalf("stderr %q", res.stderr)
	}
}

func TestUnstarRemovesTheStar(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("DELETE", nodePath(testNode, "/star"), done(http.StatusNoContent))
	res := f.run(Unstar, testNode)
	wantCode(t, res, 0)
	f.wantCalls("DELETE " + nodePath(testNode, "/star"))
	if want := table([]string{"TARGET", "STATUS", "DETAIL"}, []string{testNode, "done", ""}); res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
}

func TestStarValidatesEveryTargetBeforeAnyCall(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	wantCode(t, f.run(Star), 2)
	wantCode(t, f.run(Star, testNode, "/docs/a.txt"), 2)
	wantCode(t, f.run(Star, "nod_short"), 2)
	wantCode(t, f.run(Unstar, testFolder), 2)
	f.wantNoCalls()
}

func TestStarAccountKeyGetsThePersonHint(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PUT", nodePath(testNode, "/star"), apiErr(http.StatusForbidden, "forbidden", "only a user has stars; an account key has none"))
	res := f.run(Star, testNode)
	wantCode(t, res, 3)
	if !strings.HasSuffix(res.stderr, "selfcloud: hint: this needs a person's sign-in (selfcloud login); an account key has none\n") {
		t.Fatalf("stderr %q: want the person hint after the error", res.stderr)
	}
}

func TestStarredPlainListsTheFirstPageAndNotesMore(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("GET", "/api/v1/starred", page("c2", starredItem(testNode, "notes.txt", "2026-11-03T09:00:00Z")))
	res := f.run(Starred)
	wantCode(t, res, 0)
	f.wantCalls("GET /api/v1/starred")
	if len(f.bodies) != 1 || f.bodies[0] != "" {
		t.Fatalf("bodies %q: want no body", f.bodies)
	}
	if want := "selfcloud: more items exist; --all prints every page\n"; res.stderr != want {
		t.Fatalf("stderr %q, want %q", res.stderr, want)
	}
	want := table([]string{"ID", "KIND", "SIZE", "STARRED", "NAME"},
		[]string{testNode, "file", "240", "2026-11-03T09:00:00Z", "notes.txt"})
	if res.stdout != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", res.stdout, want)
	}
}

func TestStarJSONPrintsEachTarget(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("PUT", nodePath(testNode, "/star"), done(http.StatusNoContent))
	res := f.run(Star, testNode, "--json")
	wantCode(t, res, 0)
	var outs []map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &outs); err != nil || len(outs) != 1 ||
		outs[0]["target"] != testNode || outs[0]["status"] != "done" {
		t.Fatalf("stdout %q: %v", res.stdout, err)
	}
}

func TestStarredPlainPrintsOnlyItsOwnFields(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	item := starredItem(testNode, "notes.txt", "2026-11-03T09:00:00Z")
	item["token"] = "sck_secret_value"
	f.on("GET", "/api/v1/starred", page("", item))
	res := f.run(Starred, "--all")
	wantCode(t, res, 0)
	if strings.Contains(res.stdout, "sck_secret_value") {
		t.Fatalf("stdout %q: a field the table does not name was printed", res.stdout)
	}
}

func TestRecentPlainPrintsLastAt(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("GET", "/api/v1/recent", page("", recentItem(testNode, "notes.txt", "2026-11-04T09:00:00Z")))
	res := f.run(Recent)
	wantCode(t, res, 0)
	f.wantCalls("GET /api/v1/recent")
	if res.stderr != "" {
		t.Fatalf("stderr %q: want no note for a last page", res.stderr)
	}
	want := table([]string{"ID", "KIND", "SIZE", "LAST", "NAME"},
		[]string{testNode, "file", "240", "2026-11-04T09:00:00Z", "notes.txt"})
	if res.stdout != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", res.stdout, want)
	}
}

func TestStarredAndRecentSendTheFilters(t *testing.T) {
	query := "modified_after=1793491200000&modified_before=1793577600000&type=file"
	for _, tc := range []struct {
		command string
		run     func([]string, *fake) result
		path    string
	}{
		{"starred", func(args []string, f *fake) result { return f.run(Starred, args...) }, "/api/v1/starred"},
		{"recent", func(args []string, f *fake) result { return f.run(Recent, args...) }, "/api/v1/recent"},
	} {
		t.Run(tc.command, func(t *testing.T) {
			isolate(t, testDeviceTok)
			f := newFake(t)
			f.on("GET", tc.path, page("", starredItem(testNode, "notes.txt", "2026-11-03T09:00:00Z")))
			res := tc.run([]string{"--type", "file",
				"--modified-after", "2026-11-01T00:00:00Z",
				"--modified-before", "2026-11-02T00:00:00Z"}, f)
			wantCode(t, res, 0)
			f.wantCalls("GET " + tc.path + "?" + query)
		})
	}
}

func TestStarredJSONIsTheItemsAsTheAPIWritesThem(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("GET", "/api/v1/starred", page("", starredItem(testNode, "notes.txt", "2026-11-03T09:00:00Z")))
	res := f.run(Starred, "--json")
	wantCode(t, res, 0)
	var items []map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &items); err != nil || len(items) != 1 ||
		items[0]["starred_at"] != "2026-11-03T09:00:00Z" || items[0]["place"] != "/Docs" ||
		items[0]["id"] != testNode {
		t.Fatalf("stdout %q: %v", res.stdout, err)
	}
}

func TestRecentJSONIsTheItemsAsTheAPIWritesThem(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("GET", "/api/v1/recent", page("", recentItem(testNode, "notes.txt", "2026-11-04T09:00:00Z")))
	res := f.run(Recent, "--json")
	wantCode(t, res, 0)
	var items []map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &items); err != nil || len(items) != 1 ||
		items[0]["last_at"] != "2026-11-04T09:00:00Z" || items[0]["viewed_at"] != "2026-11-02T12:00:00Z" ||
		items[0]["edited_at"] != nil {
		t.Fatalf("stdout %q: %v", res.stdout, err)
	}
}

func TestStarredAllFollowsAnEmptyPage(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("GET", "/api/v1/starred", page("c2"), page("",
		starredItem(testNode, "one.txt", "2026-11-03T09:00:00Z"),
		starredItem(testOther, "two.txt", "2026-11-02T09:00:00Z")))
	res := f.run(Starred, "--all")
	wantCode(t, res, 0)
	f.wantCalls("GET /api/v1/starred", "GET /api/v1/starred?cursor=c2")
	if res.stderr != "" {
		t.Fatalf("stderr %q: --all must not print the note", res.stderr)
	}
	if !strings.Contains(res.stdout, "one.txt") || !strings.Contains(res.stdout, "two.txt") {
		t.Fatalf("stdout %q: want both items", res.stdout)
	}
}

func TestStarredAndRecentRefuseABadTimeBeforeAnyCall(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*fake, ...string) result
	}{
		{"starred", func(f *fake, args ...string) result { return f.run(Starred, args...) }},
		{"recent", func(f *fake, args ...string) result { return f.run(Recent, args...) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t, testDeviceTok)
			f := newFake(t)
			wantCode(t, tc.run(f, "--modified-after", "yesterday"), 2)
			wantCode(t, tc.run(f, "--modified-before", "1969-12-31T23:59:59Z"), 2)
			wantCode(t, tc.run(f, "--type", "file", testNode), 2)
			f.wantNoCalls()
		})
	}
}

func TestStarredAccountKeyHintIsPlainOnly(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", "/api/v1/starred", apiErr(http.StatusForbidden, "forbidden", "only a user has stars; an account key has none"))
	res := f.run(Starred)
	wantCode(t, res, 3)
	if !strings.HasSuffix(res.stderr, "selfcloud: hint: this needs a person's sign-in (selfcloud login); an account key has none\n") {
		t.Fatalf("stderr %q: want the person hint", res.stderr)
	}
	jres := f.run(Starred, "--json")
	wantCode(t, jres, 3)
	if strings.Contains(jres.stderr, "hint:") || !strings.Contains(jres.stderr, `"code":"forbidden"`) {
		t.Fatalf("--json stderr %q: want only the error envelope", jres.stderr)
	}
}

func TestStarredDeviceTokenRefusalHasNoHint(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("GET", "/api/v1/starred", apiErr(http.StatusForbidden, "forbidden", "a read-only token cannot read stars"))
	res := f.run(Starred)
	wantCode(t, res, 3)
	if strings.Contains(res.stderr, "hint:") {
		t.Fatalf("stderr %q: a device token gets no person hint", res.stderr)
	}
}
