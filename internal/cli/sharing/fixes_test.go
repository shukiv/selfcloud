// SPDX-License-Identifier: AGPL-3.0-or-later

package sharing

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// signedToken has the shape the server signs: base64url(payload), a dot, and a 43-character MAC.
var signedToken = "eyJub2QiOiJ4In0." + testSecret

func passwordFileWith(t *testing.T, password string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(p, []byte(password+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestAnEchoedPasswordIsHiddenWhateverTheOutputLayerDoesToIt: the output layer escapes & < > " and \ under --json and turns
// a tab into a space, so a password with such a character must be hidden before it is encoded.
func TestAnEchoedPasswordIsHiddenWhateverTheOutputLayerDoesToIt(t *testing.T) {
	passwords := []string{
		"a&b<c>d-long-enough",
		"tab\there-long-enough",
		`q"u\o-te-long-enough`,
		"bidi‮flip-long-enough",
		"nul​width-long-enough",
	}
	for _, pw := range passwords {
		for _, asJSON := range []bool{false, true} {
			isolate(t, testKey)
			f := newFake(t)
			f.on("POST", sharePath(testNode), apiErr(400, "invalid_request", "password "+pw+" is too weak"))
			args := []string{testNode, "--audience", "anyone", "--password-file", passwordFileWith(t, pw)}
			if asJSON {
				args = append(args, "--json")
			}
			res := f.run(LinkCreate, args...)
			wantCode(t, res, 2)
			all := res.stdout + res.stderr
			// the password, and each form the output layer could give it, must be absent
			for _, form := range secretForms(pw) {
				if strings.Contains(all, form) {
					t.Fatalf("json=%v: %q left the form %q in %q", asJSON, pw, form, all)
				}
			}
			for _, part := range []string{"long-enough", "te-long"} {
				if strings.Contains(all, part) {
					t.Fatalf("json=%v: %q left %q in %q", asJSON, pw, part, all)
				}
			}
			if !strings.Contains(all, "[hidden]") {
				t.Fatalf("json=%v: %q: %q should say the password is hidden", asJSON, pw, all)
			}
		}
	}
}

func TestSecretFormsHoldTheEncodedForms(t *testing.T) {
	forms := secretForms("a&b<c>\"\\\t")
	want := []string{"a&b<c>\"\\\t", `a&b<c>\"\\\t`, `a&b<c>\"\\\t`, "a&b<c>\"\\ "}
	for _, w := range want {
		found := false
		for _, f := range forms {
			found = found || f == w
		}
		if !found {
			t.Errorf("secretForms lacks %q; has %q", w, forms)
		}
	}
}

func TestAServerErrorOfSeveralTargetsHidesThePasswordToo(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("PATCH", "/api/v1/share-links/"+testLink, apiErr(400, "invalid_request", "password a&b<c>d-long-enough is too weak"))
	res := f.run(LinkUpdate, testLink, "--password-file", passwordFileWith(t, "a&b<c>d-long-enough"), "--json")
	wantCode(t, res, 2)
	if strings.Contains(res.stderr, "long-enough") || !strings.Contains(res.stderr, "[hidden]") {
		t.Fatalf("stderr %q", res.stderr)
	}
}

// TestLinkUpdateHidesThePassword: kills a link update that forgets to hide the password it read.
func TestLinkUpdateHidesThePassword(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("PATCH", "/api/v1/share-links/"+testLink, apiErr(400, "invalid_request", "the password "+testPassword+" is too short"))
	res := f.run(LinkUpdate, testLink, "--password-file", passwordFile(t))
	wantCode(t, res, 2)
	if !strings.Contains(res.stderr, "the password [hidden] is too short") {
		t.Fatalf("stderr %q", res.stderr)
	}
}

// TestLinkUpdateJSONKeepsTheKnownFieldsOnly: kills a link update that prints the server's document as it is.
func TestLinkUpdateJSONKeepsTheKnownFieldsOnly(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("PATCH", "/api/v1/share-links/"+testLink, ok(listedLink(testLink)))
	res := f.run(LinkUpdate, testLink, "--role", "viewer", "--json")
	wantCode(t, res, 0)
	for _, s := range []string{testSecret, "secret_hash", `"url"`, `"token"`, `"password":`, "drive.example"} {
		if strings.Contains(res.stdout+res.stderr, s) {
			t.Fatalf("update --json printed %q: %q", s, res.stdout)
		}
	}
	if !strings.Contains(res.stdout, `"password_set": true`) {
		t.Fatalf("stdout %q lost a known field", res.stdout)
	}
}

func dirtyCreated(audience string) reply {
	link := linkDoc(testLink, audience, "viewer")
	link["password"] = testPassword
	link["secret_hash"] = "deadbeef"
	link["secret"] = testSecret
	link["token"] = "t0ken-value"
	return created(map[string]any{"link": link, "url": shareURL, "secret": testSecret})
}

func TestCreateAndResetJSONKeepTheKnownFieldsAndTheURL(t *testing.T) {
	for _, name := range []string{"create", "reset"} {
		isolate(t, testKey)
		f := newFake(t)
		var res result
		if name == "create" {
			f.on("POST", sharePath(testNode), dirtyCreated("organisation"))
			res = f.run(LinkCreate, testNode, "--json")
		} else {
			f.on("POST", "/api/v1/share-links/"+testLink+"/reset", dirtyCreated("organisation"))
			res = f.run(LinkReset, testLink, "--json")
		}
		wantCode(t, res, 0)
		for _, s := range []string{"secret_hash", "deadbeef", `"password":`, `"token"`, "t0ken-value", `"secret"`} {
			if strings.Contains(res.stdout, s) {
				t.Fatalf("%s --json printed %q: %q", name, s, res.stdout)
			}
		}
		if strings.Count(res.stdout, testSecret) != 1 || !strings.Contains(res.stdout, `"url": "`+shareURL+`"`) ||
			!strings.Contains(res.stdout, `"link": {`) {
			t.Fatalf("%s --json: stdout %q", name, res.stdout)
		}
	}
}

func TestSignedJSONKeepsUrlDispositionAndEnd(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	address := "https://drive.example/api/v1/links/" + signedToken
	f.on("POST", nodeRoute(testNode, "/links"), created(map[string]any{"url": address, "disposition": "attachment",
		"expires_at": "2026-11-12T11:00:00Z", "secret_hash": "deadbeef", "password": testPassword}))
	res := f.run(LinkCreate, testNode, "--signed", "--json")
	wantCode(t, res, 0)
	if strings.Contains(res.stdout, "deadbeef") || strings.Contains(res.stdout, "secret_hash") ||
		strings.Count(res.stdout, address) != 1 || !strings.Contains(res.stdout, `"disposition": "attachment"`) {
		t.Fatalf("stdout %q", res.stdout)
	}
}

// TestASuccessfulCreateLeavesNoTraceOfTheSecretOnStderr: stderr holds no address, no fragment and no scrubbed mark, so a
// leak that the scrubber would hide is still found.
func TestASuccessfulCreateLeavesNoTraceOfTheSecretOnStderr(t *testing.T) {
	signed := "https://drive.example/api/v1/links/" + signedToken
	cases := map[string]func(f *fake) result{
		"create": func(f *fake) result {
			f.on("POST", sharePath(testNode), createdLink("organisation", "viewer"))
			return f.run(LinkCreate, testNode)
		},
		"reset": func(f *fake) result {
			f.on("POST", "/api/v1/share-links/"+testLink+"/reset", createdLink("organisation", "viewer"))
			return f.run(LinkReset, testLink)
		},
		"signed": func(f *fake) result {
			f.on("POST", nodeRoute(testNode, "/links"), created(map[string]any{"url": signed, "disposition": "attachment",
				"expires_at": "2026-11-12T11:00:00Z"}))
			return f.run(LinkCreate, testNode, "--signed")
		},
	}
	for name, do := range cases {
		isolate(t, testKey)
		f := newFake(t)
		res := do(f)
		wantCode(t, res, 0)
		for _, s := range []string{"/s#", "/api/v1/links/", "[hidden]", testSecret} {
			if strings.Contains(res.stderr, s) {
				t.Fatalf("%s: stderr %q holds %q", name, res.stderr, s)
			}
		}
	}
}

func TestScrubTextTouchesOnlyTheShapesTheServerMakes(t *testing.T) {
	for _, path := range []string{
		"/repo/internal/api/v1/links/handler.go",
		"/repo/api/v1/links/handler.go",
		"/notes/s#release-notes-2026-final.md",
		"/notes/s#" + testSecret[:30],
		"/notes/s#" + testSecret + "extra-chars-past-43",
	} {
		if got := scrubText("item " + path + " done"); got != "item "+path+" done" {
			t.Errorf("scrubText changed %q to %q", path, got)
		}
	}
	got := scrubText("at https://h.example/s#" + testSecret + " and https://h.example/api/v1/links/" + signedToken + ".")
	if strings.Contains(got, testSecret) || !strings.Contains(got, "/s#[hidden] and") || !strings.Contains(got, "/api/v1/links/[hidden].") {
		t.Fatalf("scrubText = %q", got)
	}
}

func TestLinkLsLeavesPathsThatLookLikeSecretsAlone(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		isolate(t, testKey)
		f := newFake(t)
		d := linkDoc(testLink, "organisation", "viewer")
		d["item_path"] = "/repo/internal/api/v1/links/handler.go"
		e := linkDoc(testLink2, "organisation", "viewer")
		e["item_path"] = "/notes/s#release-notes-2026-final.md"
		f.on("GET", accountRoute("/share-links"), listOf("", d, e))
		args := []string{"--account", testAccount}
		if asJSON {
			args = append(args, "--json")
		}
		res := f.run(LinkLs, args...)
		wantCode(t, res, 0)
		for _, p := range []string{"/repo/internal/api/v1/links/handler.go", "/notes/s#release-notes-2026-final.md"} {
			if !strings.Contains(res.stdout, p) {
				t.Fatalf("json=%v: %q was changed: %q", asJSON, p, res.stdout)
			}
		}
	}
}

// TestAPasswordFileThatIsNotARegularFileIsRefused: /proc/self/fd/N of a pipe reads like standard input, which the terminal
// rule would otherwise guard; a file that is not regular is refused whatever it holds.
func TestAPasswordFileThatIsNotARegularFileIsRefused(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs /proc/self/fd")
	}
	isolate(t, testKey)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := w.WriteString(testPassword + "\n"); err != nil {
		t.Fatal(err)
	}
	w.Close()
	f := newFake(t)
	f.on("POST", sharePath(testNode), createdLink("anyone", "viewer"))
	res := f.run(LinkCreate, testNode, "--audience", "anyone", "--password-file", "/proc/self/fd/"+strconv.Itoa(int(r.Fd())))
	wantCode(t, res, 2)
	f.wantNoCalls()
}

func TestPublicCountAsksForPublicLinksOnly(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	list := accountRoute("/share-links")
	f.on("GET", list, listOf("", linkDoc(testLink, "anyone", "viewer")))
	typeAtTerminal("no\n")
	res := f.run(LinkRm, "--public", "--account", testAccount)
	wantCode(t, res, 2)
	f.wantCalls("GET " + list + "?audience=anyone")
	if !strings.Contains(res.stderr, "1 live link") || !strings.Contains(res.stderr, "expired") {
		t.Fatalf("the question %q should count the live links and say that expired ones go too", res.stderr)
	}
}

func TestLinkRmQuestionReadsAsAList(t *testing.T) {
	isolate(t, testKey)
	typeAtTerminal("no\n")
	f := newFake(t)
	res := f.run(LinkRm, testLink)
	wantCode(t, res, 2)
	if strings.Contains(res.stderr, "[") || !strings.Contains(res.stderr, "whose address stops") {
		t.Fatalf("question %q", res.stderr)
	}
	typeAtTerminal("no\n")
	res = f.run(LinkRm, testLink, testLink2)
	wantCode(t, res, 2)
	if strings.Contains(res.stderr, "[") || !strings.Contains(res.stderr, testLink+", "+testLink2) ||
		!strings.Contains(res.stderr, "whose addresses stop") {
		t.Fatalf("question %q", res.stderr)
	}
}

func TestLinkUpdateCanClearATitleAndAMessage(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("PATCH", "/api/v1/share-links/"+testLink, ok(linkDoc(testLink, "anyone", "")))
	wantCode(t, f.run(LinkUpdate, testLink, "--title=", "--message="), 0)
	if got := f.body(0); got["title"] != "" || got["message"] != "" || len(got) != 2 {
		t.Fatalf("body %v", got)
	}
}

// TestTransferWithANameThatCannotBeShownSafelyAsksForYes: a name with a bidi override or a control character is not printed
// as the phrase to type; the command refuses the question and sends nothing.
func TestTransferWithANameThatCannotBeShownSafelyAsksForYes(t *testing.T) {
	for _, name := range []string{"evil\x1b[2J‮name.txt", "flip‮name.txt", "zero​width.txt"} {
		isolate(t, testKey)
		f := newFake(t)
		f.on("GET", nodeRoute(testNode, ""), ok(map[string]any{"id": testNode, "name": name, "kind": "file"}))
		f.on("POST", nodeRoute(testNode, "/transfer-ownership"), ok(map[string]any{"id": testNode, "owner_id": testUser}))
		typeAtTerminal(name + "\n")
		res := f.run(TransferOwnership, testNode, "--to", testUser)
		wantCode(t, res, 2)
		if !strings.Contains(res.stderr, "--yes") {
			t.Fatalf("stderr %q should say to give --yes", res.stderr)
		}
		for _, bad := range []string{"\x1b", "‮", "​"} {
			if strings.Contains(res.stdout+res.stderr, bad) {
				t.Fatalf("stderr %q holds an unsafe character", res.stderr)
			}
		}
		for _, c := range f.requests() {
			if strings.HasPrefix(c, "POST") {
				t.Fatalf("transferred: %v", f.requests())
			}
		}
		// with --yes it goes on
		wantCode(t, f.run(TransferOwnership, testNode, "--to", testUser, "--yes"), 0)
	}
}

// TestRevokeWithAnUnsafeNameShowsTheIdAndStillAsks: revoke types the subject's id, so a name that cannot be shown is not a
// reason to refuse; the question names the node by its id.
func TestRevokeWithAnUnsafeNameShowsTheIdAndStillAsks(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("GET", nodeRoute(testNode, ""), ok(map[string]any{"id": testNode, "name": "flip‮name.txt", "kind": "file"}))
	f.on("DELETE", nodeRoute(testNode, "/permissions/"+testUser), done(204))
	typeAtTerminal(testUser + "\n")
	res := f.run(ShareRevoke, testNode, testUser)
	wantCode(t, res, 0)
	if strings.Contains(res.stderr, "‮") || !strings.Contains(res.stderr, testNode) {
		t.Fatalf("stderr %q", res.stderr)
	}
}
