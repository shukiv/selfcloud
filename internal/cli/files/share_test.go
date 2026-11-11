// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/cli/profile"
)

// getShare runs get with no --url: a share link names its own server.
func getShare(t *testing.T, args ...string) result {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Get(args, &out, &errOut)
	return result{code: code, stdout: out.String(), stderr: errOut.String()}
}

// withProfile saves a current profile, home, at url with token, beside SELFCLOUD_TOKEN, so a test can show that a
// share link's server gets neither.
func withProfile(t *testing.T, url, token string) {
	t.Helper()
	err := profile.Open(os.Getenv("SELFCLOUD_PROFILES_DIR")).Save(profile.File{Current: "home",
		Profiles: map[string]profile.Profile{"home": {URL: url, Kind: profile.KindAccount, Token: token}}})
	if err != nil {
		t.Fatal(err)
	}
}

// visitorCredentials returns the bearer values the record saw that are visitor credentials.
func visitorCredentials(calls []seen) []string {
	var out []string
	for _, c := range calls {
		if v, ok := strings.CutPrefix(c.auth, "Bearer "); ok && strings.HasPrefix(v, "scl_") {
			out = append(out, v)
		}
	}
	return out
}

// TestGetShareLinkSendsNoCredentialOfTheProfile: a share link is opened with no sign-in. Its server is checked as its
// own host, and gets neither the profile's key nor SELFCLOUD_TOKEN: the open carries no Authorization at all, and the
// file's reads carry only the visitor credential the open gave.
func TestGetShareLinkSendsNoCredentialOfTheProfile(t *testing.T) {
	l := newLive(t, nil, nil)
	l.isolate()
	profileServer := newFake(t)
	withProfile(t, profileServer.srv.URL, l.key)
	data := randomBytes(t, 2*mib+17)
	nodeID := l.upload("/", "shared.bin", data, "fail")
	link, _ := l.shareLink(nodeID, "", nil)
	dest := t.TempDir()
	res := getShare(t, link, dest, "--piece-size", "1MiB")
	l.wantCode(res, 0)
	got, err := os.ReadFile(filepath.Join(dest, "shared.bin"))
	if err != nil || sha(got) != sha(data) {
		t.Fatalf("the shared file differs: %v", err)
	}
	profileServer.wantNoCalls()
	calls := l.rec.all()
	if len(calls) == 0 || calls[0].method != http.MethodPost || calls[0].path != "/api/v1/share/open" || calls[0].auth != "" {
		t.Fatalf("the first call is not an open with no Authorization: %+v", calls)
	}
	for _, c := range calls[1:] {
		if !strings.HasPrefix(c.auth, "Bearer scl_") {
			t.Errorf("%s %s carried %q, not a visitor credential", c.method, c.path, c.auth)
		}
	}
	for _, c := range calls {
		if strings.Contains(c.auth, l.key) {
			t.Errorf("%s %s carried the account key", c.method, c.path)
		}
	}
}

// TestGetShareLinkWithAPassword: the password comes from standard input with --password-stdin, never from a terminal,
// where it would be shown as it is typed. A link that needs one says how to give it.
func TestGetShareLinkWithAPassword(t *testing.T) {
	l := newLive(t, nil, nil)
	l.isolate()
	data := randomBytes(t, 1000)
	nodeID := l.upload("/", "locked.bin", data, "fail")
	link, _ := l.shareLink(nodeID, "correct horse battery", nil)

	res := getShare(t, link, t.TempDir())
	l.wantCode(res, 3)
	if !strings.Contains(res.stderr, "--password-stdin") {
		t.Errorf("a link that needs its password does not say how to give it: %q", res.stderr)
	}

	before := len(l.rec.all())
	typeAtTerminal("correct horse battery\n")
	res = getShare(t, link, t.TempDir(), "--password-stdin")
	l.wantCode(res, 2)
	if len(l.rec.all()) != before {
		t.Error("a request was sent after a terminal was refused for the password")
	}

	Stdin, IsTerminal = strings.NewReader("wrong horse\n"), func(io.Reader) bool { return false }
	res = getShare(t, link, t.TempDir(), "--password-stdin")
	l.wantCode(res, 3)
	if !strings.Contains(res.stderr, "password") {
		t.Errorf("a wrong password: %q", res.stderr)
	}

	Stdin = strings.NewReader("correct horse battery\n")
	dest := t.TempDir()
	res = getShare(t, link, dest, "--password-stdin")
	l.wantCode(res, 0)
	if got, err := os.ReadFile(filepath.Join(dest, "locked.bin")); err != nil || sha(got) != sha(data) {
		t.Fatalf("the file behind the password differs: %v", err)
	}
}

// TestShareSecretsStayHidden is the secrets test of design-cli section 6 for share links: on the paths that fail and on
// the one that works, the link's secret, its password, the visitor credentials and the account key appear in no output,
// no file under the profiles directory, and not in the server's log.
func TestShareSecretsStayHidden(t *testing.T) {
	l := newLive(t, nil, nil)
	l.isolate()
	withProfile(t, l.url, l.key)
	const password = "Tr0ub4dor&3 secret"
	nodeID := l.upload("/", "s.bin", randomBytes(t, 3000), "fail")
	link, linkID := l.shareLink(nodeID, password, nil)
	_, secret, _ := strings.Cut(link, "#")
	dirLink, _ := l.shareLink(l.mkdir("/secret-dir"), "", nil)
	_, dirSecret, _ := strings.Cut(dirLink, "#")

	var outputs []string
	runWith := func(stdin string, args ...string) {
		Stdin, IsTerminal = strings.NewReader(stdin), func(io.Reader) bool { return false }
		res := getShare(t, args...)
		outputs = append(outputs, res.stdout, res.stderr)
		jres := getShare(t, append(args, "--json")...)
		outputs = append(outputs, jres.stdout, jres.stderr)
	}
	runWith("", link, t.TempDir())                                     // no password
	runWith("wrong\n", link, t.TempDir(), "--password-stdin")          // a wrong one
	runWith(password+"\n", link, t.TempDir(), "--password-stdin")      // the right one
	runWith("", dirLink, t.TempDir())                                  // a directory
	runWith("", strings.Replace(link, "/s#", "/x#", 1), t.TempDir())   // a malformed address
	runWith("", "http://127.0.0.1:1/s#"+secret, t.TempDir())           // an unreachable host
	runWith("", strings.Replace(link, "http://", "ftp://", 1), "dest") // another scheme
	l.jsonCall(http.MethodDelete, "/api/v1/share-links/"+linkID, nil)
	runWith(password+"\n", link, t.TempDir(), "--password-stdin") // a dead link

	secrets := map[string]string{"the link's secret": secret, "the directory link's secret": dirSecret,
		"the password": password, "the account key": l.key}
	creds := visitorCredentials(l.rec.all())
	if len(creds) == 0 {
		t.Fatal("no visitor credential was seen; the test proves nothing")
	}
	for i, c := range creds {
		secrets["visitor credential "+string(rune('a'+i%26))] = c
	}
	var files []string
	_ = filepath.WalkDir(os.Getenv("SELFCLOUD_PROFILES_DIR"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if b, rerr := os.ReadFile(p); rerr == nil {
				files = append(files, string(b))
			}
		}
		return nil
	})
	for what, s := range secrets {
		for _, o := range outputs {
			if strings.Contains(o, s) {
				t.Errorf("%s was printed: %q", what, o)
			}
		}
		for _, f := range files {
			if what != "the account key" && strings.Contains(f, s) {
				t.Errorf("%s is in a file under the profiles directory", what)
			}
		}
		if strings.Contains(l.logs.String(), s) {
			t.Errorf("%s is in the server's log", what)
		}
	}
}

// mkdir makes a directory at path in the folder and returns its node id.
func (l *live) mkdir(path string) string {
	l.t.Helper()
	v, err := decodeNode(l.jsonCall(http.MethodPost, "/api/v1/folders/"+l.folder+"/directories", map[string]any{"path": path}))
	if err != nil {
		l.t.Fatal(err)
	}
	return v.ID
}

// TestGetShareLinkRefusesADirectoryAndADropLink: get takes a link to a file. A link to a directory, and a link that only
// takes uploads, are refused with a message, and nothing is written.
func TestGetShareLinkRefusesADirectoryAndADropLink(t *testing.T) {
	l := newLive(t, nil, nil)
	l.isolate()
	dir := l.mkdir("/shared")
	dirLink, _ := l.shareLink(dir, "", nil)
	dest := t.TempDir()
	res := getShare(t, dirLink, dest)
	l.wantCode(res, 2)
	if !strings.Contains(res.stderr, "directory") {
		t.Errorf("stderr %q does not say the link is to a directory", res.stderr)
	}
	expires := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	dropLink, _ := l.shareLink(dir, "", map[string]any{"upload_only": true, "expires_at": expires, "title": "Send"})
	res = getShare(t, dropLink, dest)
	l.wantCode(res, 2)
	if !strings.Contains(res.stderr, "upload") {
		t.Errorf("stderr %q does not say the link takes uploads only", res.stderr)
	}
	if got := dirEntries(t, dest); len(got) != 0 {
		t.Errorf("the destination holds %v", got)
	}
}

// TestGetShareRenewsTheVisitorCredential: a visitor credential lives fifteen minutes, and a large file takes longer, so
// get renews the credential before a range when little of its life is left, and goes on with the new one.
func TestGetShareRenewsTheVisitorCredential(t *testing.T) {
	old := shareRenewBefore
	t.Cleanup(func() { shareRenewBefore = old })
	shareRenewBefore = 16 * time.Minute // more than a credential's life: every range renews first
	l := newLive(t, nil, nil)
	l.isolate()
	data := randomBytes(t, 3*mib)
	nodeID := l.upload("/", "long.bin", data, "fail")
	link, _ := l.shareLink(nodeID, "", nil)
	dest := t.TempDir()
	res := getShare(t, link, dest, "--piece-size", "1MiB")
	l.wantCode(res, 0)
	if got, err := os.ReadFile(filepath.Join(dest, "long.bin")); err != nil || sha(got) != sha(data) {
		t.Fatalf("the file differs: %v", err)
	}
	calls := l.rec.all()
	if n := countCalls(calls, "POST /api/v1/share/renew"); n < 2 {
		t.Fatalf("%d renewals, want one before each range", n)
	}
	var lastRenewed string
	for i, c := range calls {
		if c.method == http.MethodPost && c.path == "/api/v1/share/renew" {
			lastRenewed = ""
			continue
		}
		if i > 0 && strings.HasSuffix(c.path, "/content") {
			if c.auth == lastRenewed {
				t.Errorf("two ranges carried the same credential across a renewal")
			}
			lastRenewed = c.auth
		}
	}
}

// TestShareURLRules: a share link is https (or loopback http), ends in /s, and carries its secret in the fragment.
// Anything else is refused before any request, and a credential flag does not apply to it.
func TestShareURLRules(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	base := strings.Replace(f.srv.URL, "127.0.0.1", "localhost", 1)
	good := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" // 43 base64url characters, the secret's shape
	for _, bad := range []string{
		base + "/s",                   // no secret
		base + "/s#short",             // not a secret's shape
		base + "/s?x=1#" + good,       // a query
		base + "/files#" + good,       // not a share page
		"http://192.0.2.10/s#" + good, // plain http that is not loopback
		"ftp://" + f.srv.Listener.Addr().String() + "/s#" + good,
		"https://user:pw@example.test/s#" + good, // a user name in it
	} {
		res := getShare(t, bad, t.TempDir())
		wantCode(t, res, 2)
		if strings.Contains(res.stderr, good) {
			t.Errorf("%q: the refusal repeats the secret", bad)
		}
	}
	link := f.srv.URL + "/s#" + good
	tokenFile := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(tokenFile, []byte(testAccountKey), 0o600); err != nil {
		t.Fatal(err)
	}
	wantCode(t, getShare(t, link, t.TempDir(), "--token-file", tokenFile), 2)
	wantCode(t, getShare(t, link, t.TempDir(), "--folder", testFolder), 2)
	wantCode(t, getShare(t, link, t.TempDir(), "--url", f.srv.URL), 2)
	f.wantNoCalls()
	wantCode(t, getShare(t, testNode, t.TempDir(), "--password-stdin"), 2)
}
