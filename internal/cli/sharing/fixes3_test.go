// SPDX-License-Identifier: AGPL-3.0-or-later

package sharing

import (
	"strings"
	"testing"
)

// TestAPasswordThatIsAWordOfTheOutputChangesNeitherTheAddressNorTheKeys: the password is hidden in the values the server
// sent, never in the address line or in a key, so the marks reveal nothing and the address stays whole.
func TestAPasswordThatIsAWordOfTheOutputChangesNeitherTheAddressNorTheKeys(t *testing.T) {
	for _, pw := range []string{"drive.example", "https://", "password"} {
		for _, asJSON := range []bool{false, true} {
			isolate(t, testKey)
			f := newFake(t)
			link := linkDoc(testLink, "anyone", "viewer")
			link["message"] = "the secret is " + pw
			f.on("POST", sharePath(testNode), created(map[string]any{"link": link, "url": shareURL}))
			args := []string{testNode, "--audience", "anyone", "--password-file", passwordFileWith(t, pw)}
			if asJSON {
				args = append(args, "--json")
			}
			res := f.run(LinkCreate, args...)
			wantCode(t, res, 0)
			if !strings.Contains(res.stdout, shareURL) {
				t.Fatalf("pw %q json=%v: the address was changed: %q", pw, asJSON, res.stdout)
			}
			if strings.Contains(res.stdout, "[hidden]_set") || strings.Contains(res.stdout, `[hidden]":`) {
				t.Fatalf("pw %q json=%v: a key was changed: %q", pw, asJSON, res.stdout)
			}
			if asJSON {
				if !strings.Contains(res.stdout, `"password_set": true`) || !strings.Contains(res.stdout, `"message": "the secret is [hidden]"`) {
					t.Fatalf("pw %q: stdout %q", pw, res.stdout)
				}
			} else if !strings.Contains(res.stdout, "password_set=true\n") {
				t.Fatalf("pw %q: stdout %q", pw, res.stdout)
			}
		}
	}
}

func TestUpdateKeepsKeysWhenThePasswordIsAWordOfTheOutput(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		isolate(t, testKey)
		f := newFake(t)
		link := linkDoc(testLink, "anyone", "viewer")
		link["title"] = "password"
		f.on("PATCH", "/api/v1/share-links/"+testLink, ok(link))
		args := []string{testLink, "--password-file", passwordFileWith(t, "password")}
		if asJSON {
			args = append(args, "--json")
		}
		res := f.run(LinkUpdate, args...)
		wantCode(t, res, 0)
		if strings.Contains(res.stdout, "[hidden]_set") || strings.Contains(res.stdout, `[hidden]":`) {
			t.Fatalf("json=%v: a key was changed: %q", asJSON, res.stdout)
		}
		if !strings.Contains(res.stdout, "password_set") {
			t.Fatalf("json=%v: stdout %q", asJSON, res.stdout)
		}
		if asJSON && !strings.Contains(res.stdout, `"title": "[hidden]"`) {
			t.Fatalf("stdout %q should hide the value", res.stdout)
		}
	}
}

// TestAnAnswerWithNoNameButAParentIsNotAFolderRoot: only a node with no name and no parent is a root.
func TestAnAnswerWithNoNameButAParentIsNotAFolderRoot(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("GET", nodeRoute(testNode, ""), ok(map[string]any{"id": testNode, "name": "", "parent_id": testOther, "kind": "file"}))
	f.on("DELETE", nodeRoute(testNode, "/permissions/"+testUser), done(204))
	typeAtTerminal(testUser + "\n")
	res := f.run(ShareRevoke, testNode, testUser)
	wantCode(t, res, 1)
	for _, c := range f.requests() {
		if strings.HasPrefix(c, "DELETE") {
			t.Fatalf("revoked on a nameless node with a parent: %v", f.requests())
		}
	}
}

// A known key that holds an object, an array or a number equal to a numeric password is not printed.
func TestAKnownKeyWithNonScalarValueHoldingThePasswordIsNotPrinted(t *testing.T) {
	const pw = "12345678"
	for _, asJSON := range []bool{false, true} {
		isolate(t, testKey)
		f := newFake(t)
		link := linkDoc(testLink, "anyone", "viewer")
		link["title"] = map[string]any{"x": pw}
		link["message"] = []any{pw}
		link["expires_at"] = 12345678
		f.on("PATCH", "/api/v1/share-links/"+testLink, ok(link))
		args := []string{testLink, "--password-file", passwordFileWith(t, pw)}
		if asJSON {
			args = append(args, "--json")
		}
		res := f.run(LinkUpdate, args...)
		wantCode(t, res, 0)
		if strings.Contains(res.stdout, pw) || strings.Contains(res.stderr, pw) {
			t.Fatalf("json=%v: the password was printed: %q", asJSON, res.stdout)
		}
		if !strings.Contains(res.stdout, "password_set") {
			t.Fatalf("json=%v: keys lost: %q", asJSON, res.stdout)
		}
	}
}

// A string that equals the password only after the output layer cleans it is hidden.
func TestAStringThatEqualsThePasswordOnlyAfterCleaningIsHidden(t *testing.T) {
	const pw = "aaaa bbbb cccc"
	for _, echo := range []string{"aaaa\tbbbb\tcccc", "aaaa bbbb\u200bcccc"} {
		for _, asJSON := range []bool{false, true} {
			isolate(t, testKey)
			f := newFake(t)
			link := linkDoc(testLink, "anyone", "viewer")
			link["title"] = echo
			f.on("PATCH", "/api/v1/share-links/"+testLink, ok(link))
			args := []string{testLink, "--password-file", passwordFileWith(t, pw)}
			if asJSON {
				args = append(args, "--json")
			}
			res := f.run(LinkUpdate, args...)
			wantCode(t, res, 0)
			if strings.Contains(res.stdout, "aaaa") {
				t.Fatalf("echo %q json=%v: the password got through: %q", echo, asJSON, res.stdout)
			}
		}
	}
}
