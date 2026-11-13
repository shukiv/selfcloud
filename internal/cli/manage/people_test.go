// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func directoryReply(items ...map[string]any) reply {
	if items == nil {
		items = []map[string]any{}
	}
	return reply{status: http.StatusOK, body: jsonOf(map[string]any{"items": items, "more": false})}
}

func TestPeopleFindListsTheDirectory(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on(http.MethodGet, "/api/v1/directory", directoryReply(
		map[string]any{"id": testUser, "kind": "user", "name": "Ada", "email": "ada@example.test", "extra": "hidden"},
		map[string]any{"id": testGroup, "kind": "group", "name": "Team"},
	))
	res := f.run(PeopleFind, "ad")
	wantCode(t, res, 0)
	f.wantCalls("GET /api/v1/directory?kind=all&q=ad")
	want := table([]string{"ID", "KIND", "NAME", "EMAIL"},
		[]string{testUser, "user", "Ada", "ada@example.test"},
		[]string{testGroup, "group", "Team", ""})
	if res.stdout != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", res.stdout, want)
	}
}

func TestPeopleFindSendsAnEmptyQuery(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on(http.MethodGet, "/api/v1/directory", directoryReply())
	res := f.run(PeopleFind, "--kind", "group")
	wantCode(t, res, 0)
	f.wantCalls("GET /api/v1/directory?kind=group&q=")
	want := table([]string{"ID", "KIND", "NAME", "EMAIL"})
	if res.stdout != want {
		t.Fatalf("stdout:\n%s\nwant the header only", res.stdout)
	}
}

func TestPeopleFindMore(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on(http.MethodGet, "/api/v1/directory", reply{status: http.StatusOK, body: jsonOf(map[string]any{
		"items": []map[string]any{{"id": testUser, "kind": "user", "name": "Ada", "email": "ada@example.test"}},
		"more":  true,
	})})
	res := f.run(PeopleFind)
	wantCode(t, res, 0)
	if !strings.Contains(res.stderr, "more matched; narrow the query (at most 20 are shown)") {
		t.Fatalf("stderr %q; want the more note", res.stderr)
	}
}

func TestPeopleFindJSONUsesAView(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on(http.MethodGet, "/api/v1/directory", reply{status: http.StatusOK, body: jsonOf(map[string]any{
		"items": []map[string]any{{
			"id": testUser, "kind": "user", "name": "Ada", "email": "ada@example.test", "extra": "hidden",
		}},
		"more": false,
	})})
	res := f.run(PeopleFind, "--json")
	wantCode(t, res, 0)
	var page struct {
		Items []map[string]any `json:"items"`
		More  bool             `json:"more"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &page); err != nil {
		t.Fatalf("stdout %q is not a JSON object: %v", res.stdout, err)
	}
	if len(page.Items) != 1 || page.Items[0]["email"] != "ada@example.test" {
		t.Fatalf("page %v; want the directory entry", page)
	}
	if _, ok := page.Items[0]["extra"]; ok {
		t.Fatalf("page %v; want only printed fields", page)
	}
}

func TestPeopleFindDeviceForbiddenHint(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on(http.MethodGet, "/api/v1/directory", apiErr(http.StatusForbidden, "forbidden", "not with this credential"))
	res := f.run(PeopleFind)
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "selfcloud: hint: this needs an account key; a signed-in device cannot use it") {
		t.Fatalf("stderr %q; want the account-key hint", res.stderr)
	}
}

func TestPeopleFindAccountForbiddenHasNoHint(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on(http.MethodGet, "/api/v1/directory", apiErr(http.StatusForbidden, "forbidden", "not with this credential"))
	res := f.run(PeopleFind)
	wantCode(t, res, 3)
	if strings.Contains(res.stderr, "this needs an account key") {
		t.Fatalf("stderr %q; an account key must not get the device hint", res.stderr)
	}
}

func TestPeopleFindUsage(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"bad kind", []string{"--kind", "team"}},
		{"two queries", []string{"one", "two"}},
		{"long query", []string{strings.Repeat("é", 101)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolate(t, testAccountKey)
			f := newFake(t)
			res := f.run(PeopleFind, tt.args...)
			wantCode(t, res, 2)
			f.wantNoCalls()
			if !strings.HasPrefix(res.stderr, "selfcloud: ") {
				t.Fatalf("stderr %q; want a usage message", res.stderr)
			}
		})
	}
}
