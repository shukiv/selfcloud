// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// fileProbe is a real account with one folder on the real API. The fixture commands run against url without the
// recorder; the command under test runs against recURL, which is the same server behind the recorder.
type fileProbe struct {
	t      *testing.T
	url    string
	recURL string
	rec    *recorder
	st     *store.Store
	folder string
	acc    string
}

// newFileProbe makes an account with `admin create-account` in a private data directory, makes a team folder in its
// store, and serves that store. The account key becomes the token of the commands it runs.
func newFileProbe(t *testing.T) *fileProbe {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("SELFCLOUD_DATA_DIR", dir)
	code, out, errOut := runCmd(t, "admin", "create-account", "-name", "probe")
	if code != 0 {
		t.Fatalf("create-account: exit %d, stderr %q", code, errOut)
	}
	accountID := valueOf(t, out, "account_id=")
	key := valueOf(t, out, "account_key=")

	srv, st, s := serveDir(t, dir)
	f := &store.Folder{ID: id.New("fld"), AccountID: accountID, Name: "probe", NameKey: "probe"}
	ctx := context.Background()
	if err := st.Write(ctx, func(tx *store.Tx) error { return tx.InsertFolder(ctx, f, "probe") }); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{next: s.Handler()}
	recorded := httptest.NewServer(rec)
	t.Cleanup(recorded.Close)
	t.Setenv("SELFCLOUD_TOKEN", key)
	return &fileProbe{t: t, url: srv.URL, recURL: recorded.URL, rec: rec, st: st, folder: f.ID, acc: accountID}
}

// lock holds node nodeID for an editor the way an office session does, so the node is locked until the break.
func (p *fileProbe) lock(nodeID string) {
	p.t.Helper()
	ctx := context.Background()
	now := time.Now()
	l := &store.WOPILock{NodeID: nodeID, AccountID: p.acc, FolderID: p.folder, LockID: id.New("lck"),
		Holder: "probe-editor", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := p.st.Write(ctx, func(tx *store.Tx) error {
		_, err := tx.PutLock(ctx, l)
		return err
	}); err != nil {
		p.t.Fatal(err)
	}
}

// reset forgets the calls recorded so far.
func (p *fileProbe) reset() {
	p.rec.mu.Lock()
	p.rec.calls = nil
	p.rec.mu.Unlock()
}

// recorded returns the calls recorded since reset, as "METHOD /path".
func (p *fileProbe) recorded() []string {
	p.rec.mu.Lock()
	defer p.rec.mu.Unlock()
	return append([]string(nil), p.rec.calls...)
}

// do runs a fixture command against the real server, outside the recording, and returns its standard output.
func (p *fileProbe) do(args ...string) string {
	p.t.Helper()
	var out, errOut bytes.Buffer
	full := append(append([]string{}, args...), "--url", p.url)
	if code := run(full, &out, &errOut); code != 0 {
		p.t.Fatalf("fixture %v: exit %d, stderr %q", args, code, errOut.String())
	}
	return out.String()
}

// touch makes an empty file at path in the probe folder and returns its node id.
func (p *fileProbe) touch(path string) string {
	return valueOf(p.t, p.do("touch", "--folder", p.folder, path), "id=")
}

// mkdir makes a directory at path in the probe folder and returns its node id.
func (p *fileProbe) mkdir(path string) string {
	return valueOf(p.t, p.do("mkdir", "--folder", p.folder, path), "id=")
}

// trashed makes a file at path and moves it to the Trash, and returns its node id.
func (p *fileProbe) trashed(path string) string {
	id := p.touch(path)
	p.do("rm", id, "--yes")
	return id
}

// valueOf returns the rest of the first line of out that starts with key. It fails the test when no line does.
func valueOf(t *testing.T, out, key string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(line, key); ok {
			return v
		}
	}
	t.Fatalf("no %s line in %q", key, out)
	return ""
}

// fileProbes gives, for each file command, the arguments that make it call its route on the real server. The fixture
// nodes are made first, through the same commands and outside the recording.
var fileProbes = map[string]func(p *fileProbe) []string{
	"ls": func(p *fileProbe) []string { return []string{"--folder", p.folder, "/"} },
	"stat": func(p *fileProbe) []string {
		return []string{p.touch("/stat.txt")}
	},
	"mkdir": func(p *fileProbe) []string { return []string{"--folder", p.folder, "/probe-dir"} },
	"touch": func(p *fileProbe) []string { return []string{"--folder", p.folder, "/probe-touch.txt"} },
	"mv": func(p *fileProbe) []string {
		return []string{p.touch("/mv-src.txt"), p.mkdir("/mv-dest")}
	},
	"cp": func(p *fileProbe) []string {
		return []string{p.touch("/cp-src.txt"), p.mkdir("/cp-dest")}
	},
	"rm":       func(p *fileProbe) []string { return []string{p.touch("/rm.txt"), "--yes"} },
	"trash ls": func(p *fileProbe) []string { return []string{"--folder", p.folder} },
	"trash restore": func(p *fileProbe) []string {
		return []string{p.trashed("/restore.txt")}
	},
	"trash empty": func(p *fileProbe) []string { return []string{"--folder", p.folder, "--yes"} },
	"trash rm": func(p *fileProbe) []string {
		return []string{p.trashed("/trash-rm.txt"), "--yes"}
	},
	"shortcut add": func(p *fileProbe) []string {
		return []string{p.mkdir("/sc-dir"), p.touch("/sc-target.txt")}
	},
	"unlock": func(p *fileProbe) []string {
		nodeID := p.touch("/unlock.txt")
		p.lock(nodeID)
		return []string{nodeID}
	},
}

// idSegment matches a node, folder or other id in a path: a prefix, an underscore and 26 characters.
var idSegment = regexp.MustCompile(`^([a-z]+)_[0-9a-z]{26}$`)

// routeOf turns a recorded "METHOD /path" into the form the remote table uses: each id segment becomes its prefix in
// braces, so "GET /api/v1/nodes/nod_.../children" is "GET /api/v1/nodes/{nod}/children".
func routeOf(call string) string {
	method, path, _ := strings.Cut(call, " ")
	segs := strings.Split(path, "/")
	for i, s := range segs {
		if m := idSegment.FindStringSubmatch(s); m != nil {
			segs[i] = "{" + m[1] + "}"
		}
	}
	return method + " " + strings.Join(segs, "/")
}
