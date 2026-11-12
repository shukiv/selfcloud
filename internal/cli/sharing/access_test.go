// SPDX-License-Identifier: AGPL-3.0-or-later

package sharing

import (
	"strings"
	"testing"
)

func request(reqID, status string) map[string]any {
	return map[string]any{"id": reqID, "node_id": testNode, "user_id": testUser, "user_name": "Dana", "role": "editor",
		"message": "please", "status": status, "created_at": "2026-11-01T10:00:00Z", "decided_at": nil, "decided_by": ""}
}

func TestAccessLsOfANode(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("GET", nodeRoute(testNode, "/access-requests"), listOf("c2", request(testReq, "pending")))
	f.on("GET", nodeRoute(testNode, "/access-requests"), listOf("", request(testReq2, "approved")))
	res := f.run(AccessLs, testNode, "--all")
	wantCode(t, res, 0)
	f.wantCalls("GET "+nodeRoute(testNode, "/access-requests"), "GET "+nodeRoute(testNode, "/access-requests")+"?cursor=c2")
	want := table([]string{"ID", "NODE", "USER", "NAME", "ROLE", "STATUS", "CREATED"},
		[]string{testReq, testNode, testUser, "Dana", "editor", "pending", "2026-11-01T10:00:00Z"},
		[]string{testReq2, testNode, testUser, "Dana", "editor", "approved", "2026-11-01T10:00:00Z"})
	if res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
}

func TestAccessLsPending(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("GET", "/api/v1/access-requests", listOf("", request(testReq, "pending")))
	res := f.run(AccessLs, "--pending", "--json")
	wantCode(t, res, 0)
	f.wantCalls("GET /api/v1/access-requests")
	if !strings.HasPrefix(res.stdout, "[") || !strings.Contains(res.stdout, testReq) {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestAccessLsNoticeWhenMoreExist(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("GET", nodeRoute(testNode, "/access-requests"), listOf("c2", request(testReq, "pending")))
	res := f.run(AccessLs, testNode)
	wantCode(t, res, 0)
	if !strings.Contains(res.stderr, "--all") {
		t.Fatalf("stderr %q", res.stderr)
	}
}

func TestAccessLsArgumentErrorsCostNoCall(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	for _, args := range [][]string{{}, {testNode, "--pending"}, {"--pending", testNode}, {"/docs"}, {testNode, testOther}} {
		wantCode(t, f.run(AccessLs, args...), 2)
	}
	f.wantNoCalls()
}

func TestAccessLsRefusals(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("GET", nodeRoute(testNode, "/access-requests"), apiErr(403, "forbidden", "you need the manager role"))
	f.on("GET", "/api/v1/access-requests", apiErr(403, "forbidden", "a delegated token cannot list access requests"))
	wantCode(t, f.run(AccessLs, testNode), 3)
	res := f.run(AccessLs, "--pending")
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "a delegated token cannot list access requests") {
		t.Fatalf("stderr %q", res.stderr)
	}
}

func TestAccessApproveAndDeny(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("POST", "/api/v1/access-requests/"+testReq+"/approve", ok(request(testReq, "approved")))
	f.on("POST", "/api/v1/access-requests/"+testReq+"/deny", ok(request(testReq, "denied")))
	res := f.run(AccessApprove, testReq)
	wantCode(t, res, 0)
	if want := table([]string{"TARGET", "STATUS", "DETAIL"}, []string{testReq, "done", ""}); res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
	if f.rawBody(0) != "" {
		t.Fatalf("approve with no --role sends no body, sent %q", f.rawBody(0))
	}
	wantCode(t, f.run(AccessDeny, testReq), 0)
	f.wantCalls("POST /api/v1/access-requests/"+testReq+"/approve", "POST /api/v1/access-requests/"+testReq+"/deny")
}

func TestAccessApproveAtAnotherRole(t *testing.T) {
	for _, role := range []string{"viewer", "commenter", "editor"} {
		isolate(t, testKey)
		f := newFake(t)
		f.on("POST", "/api/v1/access-requests/"+testReq+"/approve", ok(request(testReq, "approved")))
		wantCode(t, f.run(AccessApprove, testReq, "--role", role), 0)
		if got := f.body(0); got["role"] != role || len(got) != 1 {
			t.Fatalf("%s: body %v", role, got)
		}
	}
	isolate(t, testKey)
	f := newFake(t)
	for _, bad := range []string{"manager", "contributor", "owner"} {
		wantCode(t, f.run(AccessApprove, testReq, "--role", bad), 2)
	}
	f.wantNoCalls()
}

func TestAccessDecideSeveralAndRefusals(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("POST", "/api/v1/access-requests/"+testReq+"/approve", ok(request(testReq, "approved")))
	f.on("POST", "/api/v1/access-requests/"+testReq2+"/approve", apiErr(409, "conflict", "the request was already decided"))
	res := f.run(AccessApprove, testReq, testReq2)
	wantCode(t, res, 7)
	if !strings.Contains(res.stdout, "already decided") {
		t.Fatalf("stdout %q", res.stdout)
	}
	one := f.run(AccessApprove, testReq2)
	wantCode(t, one, 3)
	f.on("POST", "/api/v1/access-requests/"+testReq2+"/deny", apiErr(403, "forbidden", "you may not grant a role above your own"))
	wantCode(t, f.run(AccessDeny, testReq2), 3)
	f.on("POST", "/api/v1/access-requests/"+testUser2+"/deny", apiErr(404, "not_found", "no such access request"))
	wantCode(t, f.run(AccessDeny, testReq+"x"), 2)
}

func TestAccessDecideArgumentErrorsCostNoCall(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	for _, args := range [][]string{{}, {testNode}, {"arq_short"}, {testReq, "x"}} {
		wantCode(t, f.run(AccessApprove, args...), 2)
		wantCode(t, f.run(AccessDeny, args...), 2)
	}
	wantCode(t, f.run(AccessDeny, testReq, "--role", "viewer"), 2)
	f.wantNoCalls()
}

func sharedNode(nodeID, name string) map[string]any {
	return map[string]any{"id": nodeID, "name": name, "kind": "file", "role": "editor", "folder_id": testFolder,
		"shared_at": "2026-11-02T09:00:00Z", "place": "Reports"}
}

func TestShared(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("GET", "/api/v1/shared", listOf("c2", sharedNode(testNode, "plan.pdf")))
	f.on("GET", "/api/v1/shared", listOf("", sharedNode(testOther, "notes.txt")))
	res := f.run(Shared, "--all", "--recent")
	wantCode(t, res, 0)
	f.wantCalls("GET /api/v1/shared?order=recent", "GET /api/v1/shared?cursor=c2&order=recent")
	want := table([]string{"ID", "KIND", "ROLE", "NAME", "SHARED", "PLACE"},
		[]string{testNode, "file", "editor", "plan.pdf", "2026-11-02T09:00:00Z", "Reports"},
		[]string{testOther, "file", "editor", "notes.txt", "2026-11-02T09:00:00Z", "Reports"})
	if res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
}

func TestSharedRefusesAnAccountKey(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("GET", "/api/v1/shared", apiErr(403, "forbidden", "only a user has shared items; an account key has none"))
	res := f.run(Shared)
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "only a user has shared items") {
		t.Fatalf("stderr %q", res.stderr)
	}
	wantCode(t, f.run(Shared, "extra"), 2)
}
