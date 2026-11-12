// SPDX-License-Identifier: AGPL-3.0-or-later

package sharing

import (
	"strings"
	"testing"
)

// A folder's root node has the empty name.
func rootNode() map[string]any {
	return map[string]any{"id": testNode, "name": "", "parent_id": nil, "kind": "dir", "folder_id": testFolder}
}

func TestRevokeOnAFolderRootAsksWithTheNodeId(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("GET", nodeRoute(testNode, ""), ok(rootNode()))
	f.on("DELETE", nodeRoute(testNode, "/permissions/"+testUser), done(204))
	typeAtTerminal("wrong\n")
	res := f.run(ShareRevoke, testNode, testUser)
	wantCode(t, res, 2)
	if !strings.Contains(res.stderr, testNode) || !strings.Contains(res.stderr, "root") {
		t.Fatalf("the question %q should name the node by its id and say it is a root", res.stderr)
	}
	for _, c := range f.requests() {
		if strings.HasPrefix(c, "DELETE") {
			t.Fatalf("a wrong answer revoked: %v", f.requests())
		}
	}
	typeAtTerminal(testUser + "\n")
	res = f.run(ShareRevoke, testNode, testUser)
	wantCode(t, res, 0)
	n := 0
	for _, c := range f.requests() {
		if strings.HasPrefix(c, "DELETE") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("calls %v, want one DELETE", f.requests())
	}
}

func TestTransferOfARootLeavesTheRefusalToTheAPI(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("GET", nodeRoute(testNode, ""), ok(rootNode()))
	f.on("POST", nodeRoute(testNode, "/transfer-ownership"),
		apiErr(400, "invalid_request", "a folder's root cannot be transferred; transfer its contents instead"))
	typeAtTerminal(testNode + "\n")
	res := f.run(TransferOwnership, testNode, "--to", testUser)
	wantCode(t, res, 2)
	if !strings.Contains(res.stderr, "root cannot be transferred") {
		t.Fatalf("stderr %q", res.stderr)
	}
}

func TestAPasswordOutsideTheServersLimitsIsRefusedBeforeAnyRequest(t *testing.T) {
	for _, pw := range []string{"e", "abc", "1234567", strings.Repeat("x", 257), strings.Repeat("é", 257)} {
		isolate(t, testKey)
		f := newFake(t)
		res := f.run(LinkCreate, testNode, "--audience", "anyone", "--password-file", passwordFileWith(t, pw))
		wantCode(t, res, 2)
		f.wantNoCalls()
		if len(pw) >= 8 && strings.Contains(res.stderr, pw) {
			t.Fatalf("stderr %q repeats the password", res.stderr)
		}
		wantCode(t, f.run(LinkUpdate, testLink, "--password-file", passwordFileWith(t, pw)), 2)
		f.wantNoCalls()
	}
	for _, pw := range []string{"12345678", strings.Repeat("x", 256), strings.Repeat("é", 256)} {
		if got, err := readPassword(passwordFileWith(t, pw)); err != nil || got != pw {
			t.Errorf("a password of %d characters was refused: %v", len([]rune(pw)), err)
		}
	}
}

// TestAShortSecretIsNeverHidden: a short text would garble every line, and the marks would show what it is.
func TestAShortSecretIsNeverHidden(t *testing.T) {
	var out strings.Builder
	r, _, _ := begin("x", nil, &out, &out, nil)
	r.hide("e")
	r.hide("1234567")
	if got := r.outS.secrets; len(got) != 0 {
		t.Fatalf("short secrets registered: %q", got)
	}
	r.hide("12345678")
	if len(r.outS.secrets) == 0 {
		t.Fatal("an 8-character secret must be registered")
	}
}

func TestAnEchoedPasswordInAPrintedFieldIsHiddenOnStdout(t *testing.T) {
	const pw = "a&b‮\ttag\U000e0001-long-enough"
	isolate(t, testKey)
	f := newFake(t)
	path := "/api/v1/share-links/" + testLink
	// JSON: the password is echoed into the title
	d := linkDoc(testLink, "anyone", "viewer")
	d["title"] = "your password is " + pw
	f.on("PATCH", path, ok(d))
	res := f.run(LinkUpdate, testLink, "--password-file", passwordFileWith(t, pw), "--json")
	wantCode(t, res, 0)
	for _, form := range secretForms(pw) {
		if strings.Contains(res.stdout, form) {
			t.Fatalf("--json: stdout holds the form %q: %q", form, res.stdout)
		}
	}
	for _, part := range []string{"long-enough", "tag"} {
		if strings.Contains(res.stdout, part) {
			t.Fatalf("--json: stdout holds %q: %q", part, res.stdout)
		}
	}
	if !strings.Contains(res.stdout, "[hidden]") {
		t.Fatalf("--json: stdout %q", res.stdout)
	}
	// plain: the password, with its tab, is echoed into a field that plain output prints
	isolate(t, testKey)
	f = newFake(t)
	d = linkDoc(testLink, "anyone", "viewer")
	d["role"] = pw
	f.on("PATCH", path, ok(d))
	res = f.run(LinkUpdate, testLink, "--password-file", passwordFileWith(t, pw))
	wantCode(t, res, 0)
	if strings.Contains(res.stdout, "long-enough") || !strings.Contains(res.stdout, "role=[hidden]") ||
		!strings.Contains(res.stdout, "password_set=true") {
		t.Fatalf("plain: stdout %q", res.stdout)
	}
}

// TestCreateAndResetJSONHideAPasswordTheServerEchoesButNotTheURL: the address goes through the unscrubbed-by-shape stream,
// and a password echoed into a known field still does not.
func TestCreateAndResetJSONHideAPasswordTheServerEchoes(t *testing.T) {
	for _, name := range []string{"create", "reset"} {
		isolate(t, testKey)
		f := newFake(t)
		link := linkDoc(testLink, "anyone", "viewer")
		link["message"] = "your password is " + testPassword
		reply := created(map[string]any{"link": link, "url": shareURL})
		var res result
		if name == "create" {
			f.on("POST", sharePath(testNode), reply)
			res = f.run(LinkCreate, testNode, "--audience", "anyone", "--password-file", passwordFile(t), "--json")
		} else {
			f.on("POST", "/api/v1/share-links/"+testLink+"/reset", reply)
			res = f.run(LinkReset, testLink, "--json")
		}
		// reset reads no password, so nothing is registered there: only create is asked to hide it
		if name == "create" {
			if strings.Contains(res.stdout, testPassword) {
				t.Fatalf("%s: stdout %q holds the password", name, res.stdout)
			}
		}
		if res.code != 0 || strings.Count(res.stdout, testSecret) != 1 {
			t.Fatalf("%s: exit %d, stdout %q", name, res.code, res.stdout)
		}
	}
}

func TestScrubTextHidesAdjacentSecrets(t *testing.T) {
	got := scrubText("https://h/s#" + testSecret + "/s#" + testSecret + " end")
	if strings.Contains(got, testSecret) {
		t.Fatalf("scrubText = %q", got)
	}
}

func TestSecretFormsHoldTheSurrogatePairOfAnAstralFormatRune(t *testing.T) {
	forms := secretForms("tag\U000e0001-long-enough")
	for _, f := range forms {
		if f == "tag\\udb40\\udc01-long-enough" {
			return
		}
	}
	t.Fatalf("no surrogate-pair form in %q", forms)
}
