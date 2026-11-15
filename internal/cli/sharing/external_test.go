// SPDX-License-Identifier: AGPL-3.0-or-later

package sharing

import (
	"net/http"
	"strings"
	"testing"
)

// The external-sharing commands (slice xs4, decision 187): share grant, ls and revoke with --external, and shared
// --external. A grant names an address of another account, viewer only; the address is checked before any request and
// escaped into one path segment.

const testAddress = "guest@other.example"

func extGrantDoc(email string) map[string]any {
	return map[string]any{"email": email, "role": "viewer", "expires_at": nil,
		"created_at": "2026-11-01T10:00:00Z", "created_by": testUser2, "created_by_name": "Dana"}
}

func TestShareGrantExternal(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	path := nodeRoute(testNode, "/external-permissions")
	f.on("POST", path, ok(extGrantDoc(testAddress)))
	res := f.run(ShareGrant, testNode, testAddress, "--external")
	wantCode(t, res, 0)
	f.wantCalls("POST " + path)
	if got := f.body(0); got["email"] != testAddress || got["role"] != "viewer" || len(got) != 2 {
		t.Fatalf("body %v, want only the email and the viewer role", got)
	}
	want := "email=" + testAddress + "\nrole=viewer\ncreated_at=2026-11-01T10:00:00Z\n"
	if res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
}

func TestShareGrantExternalWithAnEnd(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	path := nodeRoute(testNode, "/external-permissions")
	doc := extGrantDoc(testAddress)
	doc["expires_at"] = "2026-12-01T00:00:00Z"
	f.on("POST", path, ok(doc))
	res := f.run(ShareGrant, testNode, testAddress, "--external", "--role", "viewer", "--expires", "2026-12-01T00:00:00Z")
	wantCode(t, res, 0)
	if got := f.body(0); got["email"] != testAddress || got["role"] != "viewer" || got["expires_at"] != "2026-12-01T00:00:00Z" || len(got) != 3 {
		t.Fatalf("body %v, want the email, the role and the end", got)
	}
	if !strings.Contains(res.stdout, "expires_at=2026-12-01T00:00:00Z\n") {
		t.Fatalf("stdout %q", res.stdout)
	}
	res = f.run(ShareGrant, testNode, testAddress, "--external", "--expires-in", "7d")
	wantCode(t, res, 0)
	if s, _ := f.body(1)["expires_at"].(string); !strings.HasSuffix(s, "Z") || len(s) != len("2026-12-01T00:00:00Z") {
		t.Fatalf("expires_at %v", f.body(1)["expires_at"])
	}
}

func TestShareGrantExternalRefusedCombinationsCostNoCall(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	for _, args := range [][]string{
		{testNode, testAddress, "--external", "--role", "editor"},
		{testNode, testAddress, "--external", "--role", "manager"},
		{testNode, testAddress, "--external", "--folder", testFolder, "--account", testAccount},
		{testNode, testAddress, "--external", "--level", "read"},
		{testNode, testAddress, "--external", "--message", "a note"},
		{testNode, testAddress, "--external", "--no-notify"},
		{testNode, "--external"},                           // no address
		{testNode, testAddress, testAddress, "--external"}, // two addresses
	} {
		wantCode(t, f.run(ShareGrant, args...), 2)
	}
	f.wantNoCalls()
}

func TestShareGrantExternalBadAddressesCostNoCall(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	for _, address := range []string{
		"",
		strings.Repeat("a", 250) + "@b.cd", // 255 bytes
		"a b@x.org",
		"a\tb@x.org",
		"a\nb@x.org",
		"no-at-sign",
	} {
		wantCode(t, f.run(ShareGrant, testNode, address, "--external"), 2)
	}
	f.wantNoCalls()
	// 254 bytes is the longest address the API takes; it goes through as given.
	longest := strings.Repeat("a", 249) + "@b.cd"
	f.on("POST", nodeRoute(testNode, "/external-permissions"), ok(extGrantDoc(longest)))
	wantCode(t, f.run(ShareGrant, testNode, longest, "--external"), 0)
	if got := f.body(0)["email"]; got != longest {
		t.Fatalf("email %v, want the address as given", got)
	}
}

func TestShareLsExternal(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	path := nodeRoute(testNode, "/external-permissions")
	ended := extGrantDoc("ended@other.example")
	ended["expires_at"] = "2026-12-01T00:00:00Z"
	f.on("GET", path, listOf("", extGrantDoc(testAddress), ended))
	res := f.run(ShareLs, testNode, "--external")
	wantCode(t, res, 0)
	f.wantCalls("GET " + path)
	want := table([]string{"EMAIL", "ROLE", "EXPIRES", "CREATED", "BY"},
		[]string{testAddress, "viewer", "", "2026-11-01T10:00:00Z", "Dana"},
		[]string{"ended@other.example", "viewer", "2026-12-01T00:00:00Z", "2026-11-01T10:00:00Z", "Dana"})
	if res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
	res = f.run(ShareLs, testNode, "--external", "--json")
	wantCode(t, res, 0)
	if !strings.HasPrefix(res.stdout, "[") || !strings.Contains(res.stdout, `"email": "`+testAddress+`"`) {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestShareLsExternalRefusesAFolder(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	wantCode(t, f.run(ShareLs, "--folder", testFolder, "--account", testAccount, "--external"), 2)
	f.wantNoCalls()
}

func TestShareRevokeExternalAsksFirst(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	path := nodeRoute(testNode, "/external-permissions/"+testAddress)
	f.on("DELETE", path, done(http.StatusNoContent))
	wantCode(t, f.run(ShareRevoke, testNode, testAddress, "--external"), 2)
	f.wantNoCalls()
	res := f.run(ShareRevoke, testNode, testAddress, "--external", "--yes")
	wantCode(t, res, 0)
	f.wantCalls("DELETE " + path)
	if want := table([]string{"TARGET", "STATUS", "DETAIL"}, []string{testAddress, "done", ""}); res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
}

func TestShareRevokeExternalAtATerminalNamesTheNodeAndTheAddress(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	path := nodeRoute(testNode, "/external-permissions/"+testAddress)
	f.on("GET", nodeRoute(testNode, ""), ok(map[string]any{"id": testNode, "name": "budget.xlsx", "kind": "file"}))
	f.on("DELETE", path, done(http.StatusNoContent))
	typeAtTerminal("nope\n")
	res := f.run(ShareRevoke, testNode, testAddress, "--external")
	wantCode(t, res, 2)
	for _, want := range []string{"budget.xlsx", testAddress} {
		if !strings.Contains(res.stderr, want) {
			t.Fatalf("the question %q does not name %q", res.stderr, want)
		}
	}
	for _, c := range f.requests() {
		if strings.HasPrefix(c, "DELETE") {
			t.Fatalf("a refused answer revoked: %v", f.requests())
		}
	}
	typeAtTerminal(testAddress + "\n")
	wantCode(t, f.run(ShareRevoke, testNode, testAddress, "--external"), 0)
	calls := f.requests()
	if calls[len(calls)-1] != "DELETE "+path {
		t.Fatalf("calls %v: the delete must be last", calls)
	}
}

// TestShareRevokeExternalEscapesTheAddress: the address goes into one path segment, escaped: a slash becomes %2F and a
// plus stays, so the address can never add a segment or a query.
func TestShareRevokeExternalEscapesTheAddress(t *testing.T) {
	for _, c := range []struct{ address, escaped string }{
		{"a/b@x.org", "a%2Fb@x.org"},
		{"a+b@x.org", "a+b@x.org"},
		{"a?b@x.org", "a%3Fb@x.org"},
	} {
		isolate(t, testKey)
		f := newFake(t)
		path := nodeRoute(testNode, "/external-permissions/"+c.escaped)
		f.on("DELETE", nodeRoute(testNode, "/external-permissions/"+c.address), done(http.StatusNoContent))
		wantCode(t, f.run(ShareRevoke, testNode, c.address, "--external", "--yes"), 0)
		f.wantRawCalls("DELETE " + path)
	}
}

func TestShareRevokeExternalNotFoundKeepsItsExitCode(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("DELETE", nodeRoute(testNode, "/external-permissions/"+testAddress), apiErr(404, "not_found", "no such permission"))
	res := f.run(ShareRevoke, testNode, testAddress, "--external", "--yes")
	wantCode(t, res, 4)
	if !strings.Contains(res.stderr, "no such permission") {
		t.Fatalf("stderr %q", res.stderr)
	}
}

func TestShareRevokeExternalArgumentErrorsCostNoCall(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	for _, args := range [][]string{
		{testNode, "--external", "--yes"},                           // no address
		{testNode, testAddress, testAddress, "--external", "--yes"}, // two addresses
		{testNode, testAddress, "--external", "--folder", testFolder, "--yes"},
		{testNode, "", "--external", "--yes"},
		{testNode, "a b@x.org", "--external", "--yes"},
		{testNode, "no-at-sign", "--external", "--yes"},
	} {
		wantCode(t, f.run(ShareRevoke, args...), 2)
	}
	f.wantNoCalls()
}

func extSharedNode(nodeID, name string) map[string]any {
	return map[string]any{"id": nodeID, "name": name, "kind": "file", "role": "viewer",
		"account_name": "Other account", "shared_by_name": "Dana", "expires_at": nil}
}

func TestSharedExternal(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("GET", "/api/v1/shared/external", ok(map[string]any{
		"items": []map[string]any{extSharedNode(testNode, "plan.pdf")}, "email_proved": true}))
	res := f.run(Shared, "--external")
	wantCode(t, res, 0)
	f.wantCalls("GET /api/v1/shared/external")
	want := table([]string{"ID", "KIND", "ROLE", "NAME", "ACCOUNT", "BY", "EXPIRES"},
		[]string{testNode, "file", "viewer", "plan.pdf", "Other account", "Dana", ""})
	if res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
	if res.stderr != "" {
		t.Fatalf("stderr %q, want none when the address is proved", res.stderr)
	}
}

func TestSharedExternalJSONPrintsTheWholeAnswer(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("GET", "/api/v1/shared/external", ok(map[string]any{
		"items": []map[string]any{extSharedNode(testNode, "plan.pdf")}, "email_proved": true}))
	res := f.run(Shared, "--external", "--json")
	wantCode(t, res, 0)
	if !strings.Contains(res.stdout, `"email_proved": true`) || !strings.Contains(res.stdout, `"items"`) ||
		!strings.Contains(res.stdout, `"id": "`+testNode+`"`) {
		t.Fatalf("stdout %q is not the whole answer document", res.stdout)
	}
}

func TestSharedExternalNoticeWhenTheAddressIsNotProved(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("GET", "/api/v1/shared/external", ok(map[string]any{"items": []map[string]any{}, "email_proved": false}))
	res := f.run(Shared, "--external")
	wantCode(t, res, 0)
	notice := "your email address is not proved; prove it in the web interface to see what other accounts shared with you"
	if !strings.Contains(res.stderr, notice) {
		t.Fatalf("stderr %q does not carry the notice", res.stderr)
	}
	if strings.Count(res.stderr, "\n") != 1 {
		t.Fatalf("stderr %q, want one line", res.stderr)
	}
	// under --json the notice is absent: the document's own email_proved says it
	res = f.run(Shared, "--external", "--json")
	wantCode(t, res, 0)
	if strings.Contains(res.stderr, notice) || !strings.Contains(res.stdout, `"email_proved": false`) {
		t.Fatalf("json: stdout %q, stderr %q", res.stdout, res.stderr)
	}
}

func TestSharedExternalRefusedFlagsCostNoCall(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	for _, args := range [][]string{
		{"--external", "--recent"},
		{"--external", "--all"},
		{"--external", "extra"},
	} {
		wantCode(t, f.run(Shared, args...), 2)
	}
	f.wantNoCalls()
}

// A terminal's device token reads the list as a session does (decision 203): the table is printed and no line is added
// for a proved address; --json prints the document unchanged.
func TestSharedExternalDeviceTokenReadsTheList(t *testing.T) {
	isolate(t, testDeviceTk)
	f := newFake(t)
	f.on("GET", "/api/v1/shared/external", ok(map[string]any{
		"items": []map[string]any{extSharedNode(testNode, "plan.pdf")}, "email_proved": true}))
	res := f.run(Shared, "--external")
	wantCode(t, res, 0)
	want := table([]string{"ID", "KIND", "ROLE", "NAME", "ACCOUNT", "BY", "EXPIRES"},
		[]string{testNode, "file", "viewer", "plan.pdf", "Other account", "Dana", ""})
	if res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
	if res.stderr != "" {
		t.Fatalf("stderr %q, want none for a proved device token", res.stderr)
	}
	res = f.run(Shared, "--external", "--json")
	wantCode(t, res, 0)
	if res.stderr != "" || !strings.Contains(res.stdout, `"email_proved": true`) {
		t.Fatalf("json: stdout %q, stderr %q", res.stdout, res.stderr)
	}
}

// An unproved address gets the unproved notice even with a device token, and no other line.
func TestSharedExternalUnprovedDeviceTokenGetsTheNotice(t *testing.T) {
	isolate(t, testDeviceTk)
	f := newFake(t)
	f.on("GET", "/api/v1/shared/external", ok(map[string]any{"items": []map[string]any{}, "email_proved": false}))
	res := f.run(Shared, "--external")
	wantCode(t, res, 0)
	if !strings.Contains(res.stderr, "your email address is not proved") {
		t.Fatalf("stderr %q does not carry the unproved notice", res.stderr)
	}
	if strings.Count(res.stderr, "\n") != 1 {
		t.Fatalf("stderr %q, want one line", res.stderr)
	}
}
