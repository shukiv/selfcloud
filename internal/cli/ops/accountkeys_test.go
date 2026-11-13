// SPDX-License-Identifier: AGPL-3.0-or-later

package ops

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

const testNewKey = "sc_acct_NEWSECRET0123456789abcdefghijklmnopqrstuvwxyz"

func keysRouteTest() string        { return "/api/v1/accounts/" + testAcc + "/keys" }
func keyRouteTest(k string) string { return keysRouteTest() + "/" + k }

// keyDoc returns a key document as the API answers a read, with fields added or replaced by extra.
func keyDoc(extra map[string]any) map[string]any {
	d := map[string]any{
		"id": testKey, "name": "ci", "prefix": "sc_acct_abcdefg", "created_at": "2026-11-01T10:00:00Z",
		"expires_at": nil, "revoked_at": nil, "last_used_at": nil,
	}
	for k, v := range extra {
		d[k] = v
	}
	return d
}

type keyCommand struct {
	name string
	run  func([]string, io.Writer, io.Writer) int
	args []string
}

func keyCommands() []keyCommand {
	return []keyCommand{
		{"account key create", AccountKeyCreate, []string{"ci"}},
		{"account key ls", AccountKeyLs, nil},
		{"account key revoke", AccountKeyRevoke, []string{testKey, "--yes"}},
	}
}

// TestEveryKeyCommandChecksItsAccountBeforeAnyRequest: with no account, a flag that is not an account id, or a variable
// that is not one, each command is refused with exit 2 and sends nothing.
func TestEveryKeyCommandChecksItsAccountBeforeAnyRequest(t *testing.T) {
	for _, c := range keyCommands() {
		t.Run(c.name, func(t *testing.T) {
			isolate(t, testAdminKey)
			f := newFake(t)
			wantCode(t, f.run(c.run, c.args...), 2)
			for _, bad := range []string{"acc_short", testFolder, testAcc + "/x", testAcc + "?x=1"} {
				wantCode(t, f.run(c.run, append(append([]string{}, c.args...), "--account", bad)...), 2)
			}
			t.Setenv("SELFCLOUD_ACCOUNT", "acc_short")
			wantCode(t, f.run(c.run, c.args...), 2)
			f.wantNoCalls()
		})
	}
}

// TestAccountKeyCreateSendsTheNameAndShowsTheKeyOnce: the body holds the name, plain output holds the six fields and the
// secret, and standard error says the secret is not shown again.
func TestAccountKeyCreateSendsTheNameAndShowsTheKeyOnce(t *testing.T) {
	isolate(t, testAdminKey)
	f := newFake(t)
	f.on("POST", keysRouteTest(), doc(201, keyDoc(map[string]any{"key": testNewKey})))
	res := f.runAcc(AccountKeyCreate, "ci")
	wantCode(t, res, 0)
	f.wantCalls("POST " + keysRouteTest())
	if got := f.rawBody(0); got != `{"name":"ci"}` {
		t.Fatalf("body %q, want {\"name\":\"ci\"}", got)
	}
	for _, want := range []string{"id=" + testKey, "name=ci", "prefix=sc_acct_abcdefg", "created_at=2026-11-01T10:00:00Z",
		"expires_at=\n", "key=" + testNewKey} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("stdout %q lacks %q", res.stdout, want)
		}
	}
	if !strings.Contains(res.stderr, "the key is shown only now") {
		t.Errorf("stderr %q lacks the note", res.stderr)
	}
}

// TestAccountKeyCreateSendsAnExpiry: --expires is sent as the RFC 3339 time it was given.
func TestAccountKeyCreateSendsAnExpiry(t *testing.T) {
	isolate(t, testAdminKey)
	f := newFake(t)
	f.on("POST", keysRouteTest(), doc(201, keyDoc(map[string]any{"key": testNewKey})))
	wantCode(t, f.runAcc(AccountKeyCreate, "--expires", "2030-01-01T00:00:00Z", "ci"), 0)
	if got := f.rawBody(0); got != `{"expires_at":"2030-01-01T00:00:00Z","name":"ci"}` {
		t.Fatalf("body %q", got)
	}
}

// TestAccountKeyCreateRefusesBadArgumentsBeforeAnyRequest: a missing, blank or extra name, and an expiry that is not a
// time or not in the future, cost no request.
func TestAccountKeyCreateRefusesBadArgumentsBeforeAnyRequest(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no name", nil},
		{"blank name", []string{"   "}},
		{"two names", []string{"a", "b"}},
		{"bad expiry", []string{"--expires", "tomorrow", "ci"}},
		{"past expiry", []string{"--expires", "2000-01-01T00:00:00Z", "ci"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			isolate(t, testAdminKey)
			f := newFake(t)
			wantCode(t, f.runAcc(AccountKeyCreate, c.args...), 2)
			f.wantNoCalls()
		})
	}
}

// TestAccountKeyCreatePrintsOnlyItsSixFields: an answer's hash, admin password and other fields are not shown, and the
// JSON document has exactly the six keys the command promises.
func TestAccountKeyCreatePrintsOnlyItsSixFields(t *testing.T) {
	isolate(t, testAdminKey)
	f := newFake(t)
	f.on("POST", keysRouteTest(), doc(201, keyDoc(map[string]any{
		"key": testNewKey, "hash": "HASHMARKER", "admin_password": "PASSWORDMARKER", "extra": "EXTRAMARKER",
	})))
	res := f.runAcc(AccountKeyCreate, "ci")
	wantCode(t, res, 0)
	for _, hidden := range []string{"HASHMARKER", "PASSWORDMARKER", "EXTRAMARKER", "revoked_at", "last_used_at"} {
		if strings.Contains(res.stdout, hidden) {
			t.Errorf("stdout %q holds %q", res.stdout, hidden)
		}
	}

	f = newFake(t)
	f.on("POST", keysRouteTest(), doc(201, keyDoc(map[string]any{"key": testNewKey})))
	res = f.runAcc(AccountKeyCreate, "ci", "--json")
	wantCode(t, res, 0)
	var doc map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &doc); err != nil {
		t.Fatalf("stdout %q is not JSON: %v", res.stdout, err)
	}
	want := []string{"id", "name", "prefix", "created_at", "expires_at", "key"}
	if len(doc) != len(want) {
		t.Fatalf("JSON keys %v, want %v", doc, want)
	}
	for _, k := range want {
		if _, ok := doc[k]; !ok {
			t.Errorf("JSON lacks %q", k)
		}
	}
	if doc["key"] != testNewKey {
		t.Errorf("key = %v, want %s", doc["key"], testNewKey)
	}
	if strings.Contains(res.stderr, "note") {
		t.Errorf("--json stderr %q holds the plain note", res.stderr)
	}
}

// TestAccountKeyLsPrintsTheSevenFieldsAndNoSecret: a key field a server sends beyond the view is not shown, plain or
// --json.
func TestAccountKeyLsPrintsTheSevenFieldsAndNoSecret(t *testing.T) {
	isolate(t, testAdminKey)
	f := newFake(t)
	f.on("GET", keysRouteTest(), page("", keyDoc(map[string]any{"key": "sck_secret_value"})))
	res := f.runAcc(AccountKeyLs)
	wantCode(t, res, 0)
	f.wantCalls("GET " + keysRouteTest())
	if !strings.Contains(res.stdout, "ID") || !strings.Contains(res.stdout, "LAST USED") {
		t.Errorf("stdout %q lacks the header", res.stdout)
	}
	if !strings.Contains(res.stdout, testKey) || !strings.Contains(res.stdout, "ci") {
		t.Errorf("stdout %q lacks the row", res.stdout)
	}
	if strings.Contains(res.stdout, "sck_secret_value") {
		t.Errorf("stdout %q holds a key secret", res.stdout)
	}

	f = newFake(t)
	f.on("GET", keysRouteTest(), page("", keyDoc(map[string]any{"key": "sck_secret_value"})))
	res = f.runAcc(AccountKeyLs, "--json")
	wantCode(t, res, 0)
	if strings.Contains(res.stdout, "sck_secret_value") {
		t.Errorf("--json stdout %q holds a key secret", res.stdout)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &items); err != nil {
		t.Fatalf("stdout %q is not JSON: %v", res.stdout, err)
	}
	want := []string{"id", "name", "prefix", "created_at", "expires_at", "revoked_at", "last_used_at"}
	if len(items) != 1 || len(items[0]) != len(want) {
		t.Fatalf("items %v, want one with keys %v", items, want)
	}
	for _, k := range want {
		if _, ok := items[0][k]; !ok {
			t.Errorf("item lacks %q", k)
		}
	}
}

// TestAccountKeyLsRefusesArguments shows a positional argument costs no request.
func TestAccountKeyLsRefusesArguments(t *testing.T) {
	isolate(t, testAdminKey)
	f := newFake(t)
	wantCode(t, f.runAcc(AccountKeyLs, testKey), 2)
	f.wantNoCalls()
}

// TestAccountKeyRevokeAsksByIDAndDeletes: --yes skips the question; at a terminal the id is typed; a different answer
// sends nothing.
func TestAccountKeyRevokeAsksByIDAndDeletes(t *testing.T) {
	isolate(t, testAdminKey)
	f := newFake(t)
	f.on("DELETE", keyRouteTest(testKey), done(204))
	res := f.runAcc(AccountKeyRevoke, testKey, "--yes")
	wantCode(t, res, 0)
	f.wantCalls("DELETE " + keyRouteTest(testKey))
	if !strings.Contains(res.stdout, "target="+testKey) || !strings.Contains(res.stdout, "status=done") {
		t.Errorf("stdout %q lacks the result", res.stdout)
	}

	isolate(t, testAdminKey)
	f = newFake(t)
	f.on("DELETE", keyRouteTest(testKey), done(204))
	typeAtTerminal(testKey + "\n")
	wantCode(t, f.runAcc(AccountKeyRevoke, testKey), 0)
	f.wantCalls("DELETE " + keyRouteTest(testKey))

	isolate(t, testAdminKey)
	f = newFake(t)
	typeAtTerminal("no\n")
	wantCode(t, f.runAcc(AccountKeyRevoke, testKey), 2)
	f.wantNoCalls()
}

// TestAccountKeyRevokeRefusesBadIDsBeforeAnyRequest: only a key_ id reaches the path.
func TestAccountKeyRevokeRefusesBadIDsBeforeAnyRequest(t *testing.T) {
	for _, bad := range []string{"key_../../x", testAcc, ""} {
		isolate(t, testAdminKey)
		f := newFake(t)
		wantCode(t, f.runAcc(AccountKeyRevoke, bad, "--yes"), 2)
		f.wantNoCalls()
	}
}

// TestAKeyAnswerThatIsNotTheDocumentFails: an answer that is not a key or a list of keys is an error naming what was
// expected, with nothing on standard output.
func TestAKeyAnswerThatIsNotTheDocumentFails(t *testing.T) {
	for _, c := range []struct {
		name   string
		method string
		run    func([]string, io.Writer, io.Writer) int
		args   []string
	}{
		{"create", "POST", AccountKeyCreate, []string{"ci"}},
		{"ls", "GET", AccountKeyLs, nil},
	} {
		for _, body := range []string{"[]", "not json"} {
			t.Run(c.name+" "+body, func(t *testing.T) {
				isolate(t, testAdminKey)
				f := newFake(t)
				f.on(c.method, keysRouteTest(), reply{status: 200, body: body})
				res := f.runAcc(c.run, c.args...)
				wantCode(t, res, 1)
				if res.stdout != "" {
					t.Errorf("stdout %q, want empty", res.stdout)
				}
				if !strings.Contains(res.stderr, "the server's answer is not") {
					t.Errorf("stderr %q does not name the expectation", res.stderr)
				}
			})
		}
	}
}

const adminHintText = "this needs an installation admin key"

// TestAnAdminRouteForbiddenNamesTheAdminKey: an account key or a device token is told which key the key routes need, and
// not under --json; an admin key that is refused is not told.
func TestAnAdminRouteForbiddenNamesTheAdminKey(t *testing.T) {
	for _, token := range []string{testAccountKey, testDeviceTok} {
		isolate(t, token)
		f := newFake(t)
		f.on("POST", keysRouteTest(), apiErr(403, "forbidden", "no"))
		res := f.runAcc(AccountKeyCreate, "ci")
		wantCode(t, res, 3)
		if !strings.Contains(res.stderr, adminHintText) {
			t.Errorf("%s: stderr %q lacks the hint", token, res.stderr)
		}

		f = newFake(t)
		f.on("POST", keysRouteTest(), apiErr(403, "forbidden", "no"))
		res = f.runAcc(AccountKeyCreate, "ci", "--json")
		wantCode(t, res, 3)
		if strings.Contains(res.stderr, "hint") || !strings.HasPrefix(res.stderr, `{"error"`) {
			t.Errorf("%s: --json stderr %q: want only the JSON error", token, res.stderr)
		}
	}

	isolate(t, testAdminKey)
	f := newFake(t)
	f.on("POST", keysRouteTest(), apiErr(403, "forbidden", "no"))
	res := f.runAcc(AccountKeyCreate, "ci")
	wantCode(t, res, 3)
	if strings.Contains(res.stderr, "hint") {
		t.Errorf("an admin key got a hint: %q", res.stderr)
	}
}
