// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"net/http"
	"strings"
	"testing"
)

func TestUserSessionsEnd(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", userLinkPath("/sessions/end"), doc(http.StatusOK, map[string]any{"ended": 2}))
	res := f.runAcc(UserSessionsEnd, testUser)
	wantCode(t, res, 0)
	f.wantCalls("POST " + userLinkPath("/sessions/end"))
	f.wantBearer(testAccountKey)
	if raw := f.rawBody(0); raw != "{}" {
		t.Fatalf("body %q, want {}", raw)
	}
	if res.stdout != "ended 2 session(s)\n" {
		t.Fatalf("stdout %q", res.stdout)
	}
	if res.stderr != "" {
		t.Errorf("stderr %q, want nothing", res.stderr)
	}
}

func TestUserSessionsEndRef(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", userLinkPath("/sessions/end"), doc(http.StatusOK, map[string]any{"ended": 1}))
	res := f.runAcc(UserSessionsEnd, testUser, "--ref", "app-one_2")
	wantCode(t, res, 0)
	if body := f.bodyOf(0); len(body) != 1 || body["session_ref"] != "app-one_2" {
		t.Fatalf("body %v, want only session_ref", body)
	}
	if res.stdout != "ended 1 session(s)\n" {
		t.Fatalf("stdout %q", res.stdout)
	}
	// An empty ref means all, and sends no field.
	res = f.runAcc(UserSessionsEnd, testUser, "--ref", "")
	wantCode(t, res, 0)
	if raw := f.rawBody(1); raw != "{}" {
		t.Fatalf("body %q, want {}", raw)
	}
}

func TestUserSessionsEndJSON(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", userLinkPath("/sessions/end"), doc(http.StatusOK, map[string]any{"ended": 0}))
	res := f.runAcc(UserSessionsEnd, testUser, "--json")
	wantCode(t, res, 0)
	if !strings.Contains(res.stdout, `"ended": 0`) {
		t.Fatalf("stdout %q, want the server's document", res.stdout)
	}
	if res.stderr != "" {
		t.Errorf("stderr %q, want nothing", res.stderr)
	}
}

// TestUserSessionsEndNoQuestion: ending sessions signs the person out but deletes nothing, so the command never asks.
func TestUserSessionsEndNoQuestion(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", userLinkPath("/sessions/end"), doc(http.StatusOK, map[string]any{"ended": 3}))
	typeAtTerminal("wrong answer\n")
	res := f.runAcc(UserSessionsEnd, testUser)
	wantCode(t, res, 0)
	f.wantCalls("POST " + userLinkPath("/sessions/end"))
	if res.stdout != "ended 3 session(s)\n" {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestUserSessionsEndBadRefRefusedBeforeAnyRequest(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	for _, bad := range []string{strings.Repeat("a", 129), "a b", "a/b", "é", "a.b"} {
		res := f.runAcc(UserSessionsEnd, testUser, "--ref", bad)
		wantCode(t, res, 2)
	}
	res := f.runAcc(UserSessionsEnd)
	wantCode(t, res, 2)
	res = f.runAcc(UserSessionsEnd, testUser, "extra")
	wantCode(t, res, 2)
	res = f.runAcc(UserSessionsEnd, "not-an-id")
	wantCode(t, res, 2)
	f.wantNoCalls()
}

func TestUserSessionsEndServerRefusals(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", userLinkPath("/sessions/end"),
		apiErr(http.StatusNotFound, "not_found", "no such user"),
		apiErr(http.StatusForbidden, "forbidden", "only the account's own key may end a user's sessions"))
	res := f.runAcc(UserSessionsEnd, testUser)
	wantCode(t, res, 4)
	res = f.runAcc(UserSessionsEnd, testUser)
	wantCode(t, res, 3)
}
