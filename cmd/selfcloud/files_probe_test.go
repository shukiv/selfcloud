// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/api"
	"github.com/shukiv/selfcloud/internal/cli/files"
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
	srv    *api.Server
	jobs   bool
	folder string
	acc    string
	// answer is what a person at a terminal types at the confirmation of the command under test, if it asks.
	answer string
	// admin is an installation admin key. The settings commands take only that kind; the account key, which the other
	// probes use, is refused by them with a 403.
	admin string
}

// typeAt makes the confirmation read answer, as a person at a terminal types it. It returns the function that puts the
// input back.
func typeAt(answer string) func() {
	oldIn, oldTTY := files.Stdin, files.IsTerminal
	files.Stdin = strings.NewReader(answer)
	files.IsTerminal = func(io.Reader) bool { return true }
	return func() { files.Stdin, files.IsTerminal = oldIn, oldTTY }
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
	code, adminOut, errOut := runCmd(t, "admin", "create-key", "-name", "probe")
	if code != 0 {
		t.Fatalf("create-key: exit %d, stderr %q", code, errOut)
	}

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
	return &fileProbe{t: t, url: srv.URL, recURL: recorded.URL, rec: rec, st: st, srv: s, folder: f.ID, acc: accountID,
		admin: strings.TrimSpace(adminOut)}
}

// startJobs runs the server's job runner for the rest of the test, so a zip is made. The handlers alone never run jobs.
func (p *fileProbe) startJobs() {
	p.t.Helper()
	if p.jobs {
		return
	}
	p.jobs = true
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		p.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = p.srv.Serve(ctx, ln, nil)
	}()
	p.t.Cleanup(func() {
		cancel()
		<-done
	})
}

// zip makes a zip of a new file and waits until it is ready, outside the recording, and returns the zip's id.
func (p *fileProbe) zip(path string) string {
	p.startJobs()
	return valueOf(p.t, p.do("zip", "create", p.touch(path), "--wait", "--wait-timeout", "2m"), "zip=")
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
	// rm and trash rm ask for the name of the one node, as a person at a terminal types it, so the probe makes the
	// name reads it declares (reads in remote.go).
	"rm": func(p *fileProbe) []string {
		p.answer = "rm.txt\n"
		return []string{p.touch("/rm.txt")}
	},
	"trash ls": func(p *fileProbe) []string { return []string{"--folder", p.folder} },
	"trash restore": func(p *fileProbe) []string {
		return []string{p.trashed("/restore.txt")}
	},
	"trash empty": func(p *fileProbe) []string { return []string{"--folder", p.folder, "--yes"} },
	"trash rm": func(p *fileProbe) []string {
		p.answer = "trash-rm.txt\n"
		return []string{p.trashed("/trash-rm.txt")}
	},
	"shortcut add": func(p *fileProbe) []string {
		return []string{p.mkdir("/sc-dir"), p.touch("/sc-target.txt")}
	},
	"unlock": func(p *fileProbe) []string {
		nodeID := p.touch("/unlock.txt")
		p.lock(nodeID)
		return []string{nodeID}
	},
	"put": func(p *fileProbe) []string {
		return []string{p.local("put.bin", "bytes for put"), "--folder", p.folder, "/"}
	},
	// get writes into a directory of its own, never the test's working directory.
	"get": func(p *fileProbe) []string {
		nodeID := valueOf(p.t, p.do("put", p.local("get.bin", "bytes for get"), "--folder", p.folder, "/"), "id=")
		return []string{nodeID, p.t.TempDir()}
	},
	"upload cancel": func(p *fileProbe) []string { return []string{p.openUpload("cancel.bin", 10)} },
	// zip create waits for its zip, which the job runner makes, so the probe also reaches the poll route it declares as a read.
	"zip create": func(p *fileProbe) []string {
		p.startJobs()
		return []string{p.touch("/zip-create.txt"), "--wait", "--wait-timeout", "2m"}
	},
	// zip get waits for a real zip, then downloads it into a directory of its own.
	"zip get": func(p *fileProbe) []string { return []string{p.zip("/zip-get.txt"), p.t.TempDir()} },
	// zip rm asks for the zip's id, as a person at a terminal types it.
	"zip rm": func(p *fileProbe) []string {
		zipID := p.zip("/zip-rm.txt")
		p.answer = zipID + "\n"
		return []string{zipID}
	},
	// The version commands need a file with two contents: versionPair replaces the first with the second, which keeps
	// the first as a version.
	"version ls": func(p *fileProbe) []string {
		nodeID, _ := p.versionPair("/version-ls.txt")
		return []string{nodeID}
	},
	// version get writes into a directory of its own, as get does.
	"version get": func(p *fileProbe) []string {
		nodeID, verID := p.versionPair("/version-get.txt")
		return []string{nodeID, verID, p.t.TempDir()}
	},
	"version label": func(p *fileProbe) []string {
		nodeID, verID := p.versionPair("/version-label.txt")
		return []string{nodeID, verID, "--label", "probe"}
	},
	"version restore": func(p *fileProbe) []string {
		nodeID, verID := p.versionPair("/version-restore.txt")
		return []string{nodeID, verID}
	},
	"version rm": func(p *fileProbe) []string {
		nodeID, verID := p.versionPair("/version-rm.txt")
		return []string{nodeID, verID, "--yes"}
	},
}

// settingsProbes gives, for each settings command, the arguments that make it call its route on the real server. They run
// with the probe's admin key (useAdminKey). trash.retention is a duration setting; 48h is a valid value.
var settingsProbes = map[string]func(p *fileProbe) []string{
	"settings get":   func(p *fileProbe) []string { return nil },
	"settings set":   func(p *fileProbe) []string { return []string{"trash.retention=48h"} },
	"settings reset": func(p *fileProbe) []string { return []string{"trash.retention"} },
	"settings logo set": func(p *fileProbe) []string {
		return []string{p.picture("logo.png", 64, 32)}
	},
	"settings logo remove": func(p *fileProbe) []string { return nil },
	"settings icon set": func(p *fileProbe) []string {
		return []string{p.picture("icon.png", 512, 512)}
	},
	"settings icon remove": func(p *fileProbe) []string { return nil },
}

// useAdminKey makes the probe's admin key the credential of the commands that follow. It returns the function that puts
// the account key back.
func (p *fileProbe) useAdminKey() func() {
	previous := os.Getenv("SELFCLOUD_TOKEN")
	p.t.Setenv("SELFCLOUD_TOKEN", p.admin)
	return func() { p.t.Setenv("SELFCLOUD_TOKEN", previous) }
}

// picture writes a PNG of width by height pixels called name, in a directory of its own, and returns its path.
func (p *fileProbe) picture(name string, width, height int) string {
	p.t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		p.t.Fatal(err)
	}
	return p.local(name, buf.String())
}

// local writes a local file called name with data, in a directory of its own, and returns its path.
func (p *fileProbe) local(name, data string) string {
	p.t.Helper()
	path := filepath.Join(p.t.TempDir(), name)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		p.t.Fatal(err)
	}
	return path
}

// openUpload begins an upload of size bytes called name in the probe folder, outside the recording and with no byte
// sent, and returns its id.
func (p *fileProbe) openUpload(name string, size int) string {
	p.t.Helper()
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	req, err := http.NewRequest(http.MethodPost, p.url+"/api/v1/uploads", nil)
	if err != nil {
		p.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv("SELFCLOUD_TOKEN"))
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Upload-Length", strconv.Itoa(size))
	req.Header.Set("Upload-Metadata", "folder "+b64(p.folder)+",name "+b64(name)+",on_conflict "+b64("fail"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		p.t.Fatalf("open an upload: %d", resp.StatusCode)
	}
	return path.Base(resp.Header.Get("Location"))
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
			// The route names a webhook {hook}, though its ids start with whk.
			if m[1] == "whk" {
				segs[i] = "{hook}"
			}
		}
	}
	// A node's permission is set on a user or a group, and the route table names either {subject}.
	for i := 1; i < len(segs); i++ {
		if segs[i-1] == "permissions" && (segs[i] == "{usr}" || segs[i] == "{grp}") {
			segs[i] = "{subject}"
		}
	}
	return method + " " + strings.Join(segs, "/")
}
