// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
)

// recordPart records a part file of dest as one this client began for f's file, as a get that stopped leaves it.
func recordPart(t *testing.T, f *fileServer, dest, name string) {
	t.Helper()
	err := saveDownloadRecord(downloadRecord{Part: filepath.Join(dest, name+partSuffix), Server: f.srv.URL,
		Node: f.nodeID, ETag: fakeETag, Size: int64(len(f.data)), StartedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
}

// TestGetNeverWritesThroughAHardLinkedPart: a part file that is a hard link to another file is never emptied or
// written. Without --overwrite it is refused, whether or not a record of this client names it; with --overwrite it is
// replaced by a new part file, so the other file keeps its bytes.
func TestGetNeverWritesThroughAHardLinkedPart(t *testing.T) {
	for _, tc := range []struct {
		what      string
		recorded  bool
		overwrite bool
		want      int
	}{
		{"stray", false, false, 2},
		{"stray, --overwrite", false, true, 0},
		{"recorded", true, false, 2},
		{"recorded, --overwrite", true, true, 0},
	} {
		t.Run(tc.what, func(t *testing.T) {
			isolate(t, testAccountKey)
			f := newFileServer(t, "h.bin", []byte("server bytes"), sha([]byte("server bytes")))
			victim := filepath.Join(t.TempDir(), "victim.txt")
			if err := os.WriteFile(victim, []byte("precious data"), 0o600); err != nil {
				t.Fatal(err)
			}
			dest := t.TempDir()
			if err := os.Link(victim, filepath.Join(dest, "h.bin.part")); err != nil {
				t.Fatal(err)
			}
			if tc.recorded {
				recordPart(t, f, dest, "h.bin")
			}
			args := []string{f.nodeID, dest}
			if tc.overwrite {
				args = append(args, "--overwrite")
			}
			res := f.get(args...)
			wantCode(t, res, tc.want)
			if b, _ := os.ReadFile(victim); string(b) != "precious data" {
				t.Errorf("the linked file now holds %q", b)
			}
			if tc.want == 2 && !strings.Contains(res.stderr, "h.bin.part") {
				t.Errorf("the refusal does not name the part file: %q", res.stderr)
			}
			if tc.want == 0 {
				if b, _ := os.ReadFile(filepath.Join(dest, "h.bin")); string(b) != "server bytes" {
					t.Errorf("the file holds %q", b)
				}
			}
		})
	}
}

// TestOldPartRefusesAHardLink: a part file with more than one link is not reopened.
func TestOldPartRefusesAHardLink(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.bin.part"), []byte("held"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(dir, "f.bin.part"), filepath.Join(dir, "other")); err != nil {
		t.Fatal(err)
	}
	if pf, err := oldPart(openRoot(t, dir), "f.bin", true); err == nil {
		pf.close()
		t.Fatal("a part file with two links was reopened")
	}
}

// asAnotherUser makes the command see every file as another user's, since a test cannot chown.
func asAnotherUser(t *testing.T) {
	t.Helper()
	old := currentUID
	t.Cleanup(func() { currentUID = old })
	currentUID = func() int { return os.Geteuid() + 1 }
}

// TestGetRefusesAPartFileOfAnotherUser: a part file another user owns, such as one made ahead in a shared directory
// under the server's name, is never reopened, so the download never goes into it. Without --overwrite it is refused
// (exit 2), even with a record of this client; with --overwrite it is removed and made again.
func TestGetRefusesAPartFileOfAnotherUser(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFileServer(t, "o.bin", []byte("server bytes"), sha([]byte("server bytes")))
	dest := t.TempDir()
	part := filepath.Join(dest, "o.bin.part")
	if err := os.WriteFile(part, []byte("theirs"), 0o600); err != nil {
		t.Fatal(err)
	}
	recordPart(t, f, dest, "o.bin")
	asAnotherUser(t)
	res := f.get(f.nodeID, dest)
	wantCode(t, res, 2)
	if !strings.Contains(res.stderr, "o.bin.part") || !strings.Contains(res.stderr, "another user") {
		t.Errorf("the refusal does not name the part file and its owner: %q", res.stderr)
	}
	if b, _ := os.ReadFile(part); string(b) != "theirs" {
		t.Errorf("the other user's part file holds %q", b)
	}
	wantCode(t, f.get(f.nodeID, dest, "--overwrite"), 0)
	if b, _ := os.ReadFile(filepath.Join(dest, "o.bin")); string(b) != "server bytes" {
		t.Errorf("the file holds %q", b)
	}
}

// TestOldPartRefusesAnotherUsersFile: a part file another user owns is not reopened.
func TestOldPartRefusesAnotherUsersFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.bin.part"), []byte("held"), 0o600); err != nil {
		t.Fatal(err)
	}
	asAnotherUser(t)
	if pf, err := oldPart(openRoot(t, dir), "f.bin", true); err == nil {
		pf.close()
		t.Fatal("another user's part file was reopened")
	}
}

// TestGetRefusesAHiddenNameFromTheServer: a name from the server that starts with a dot would make a hidden file, such
// as a shell's start-up file in the home directory, so it is refused (exit 2) before anything is fetched, and the error
// names the file and says how to name the destination. A destination the person names may start with a dot.
func TestGetRefusesAHiddenNameFromTheServer(t *testing.T) {
	isolate(t, testAccountKey)
	for _, name := range []string{".bash_profile", ".git", ".", ".."} {
		f := newFileServer(t, name, []byte("export X=1\n"), sha([]byte("export X=1\n")))
		dest := t.TempDir()
		res := f.get(f.nodeID, dest)
		wantCode(t, res, 2)
		if !strings.Contains(res.stderr, "name of its own") {
			t.Errorf("%q: the refusal does not say how to name the destination: %q", name, res.stderr)
		}
		if len(name) > 2 && !strings.Contains(res.stderr, name) {
			t.Errorf("%q: the refusal does not name the file: %q", name, res.stderr)
		}
		if f.contentCalls() != 0 {
			t.Errorf("%q: the content was fetched", name)
		}
		if got := dirEntries(t, dest); len(got) != 0 {
			t.Errorf("%q: the destination holds %v", name, got)
		}
	}
	f := newFileServer(t, ".npmrc", []byte("registry=x\n"), sha([]byte("registry=x\n")))
	named := filepath.Join(t.TempDir(), "x", ".npmrc")
	if err := os.Mkdir(filepath.Dir(named), 0o700); err != nil {
		t.Fatal(err)
	}
	wantCode(t, f.get(f.nodeID, named), 0)
	if b, _ := os.ReadFile(named); string(b) != "registry=x\n" {
		t.Errorf("the named destination holds %q", b)
	}
}

// TestGetRestartsOnARangeOfAnotherVersion: when the file changes and a proxy drops If-Range, a range of the new version
// comes back as a 206 with the new ETag. get sees the other version, starts again on it, and the result is the new
// file, never the old start with the new end.
func TestGetRestartsOnARangeOfAnotherVersion(t *testing.T) {
	isolate(t, testAccountKey)
	nodeID := id.Derive("nod", "cli6-other-version")
	v1, v2 := bytes.Repeat([]byte("1"), 3*mib/2), bytes.Repeat([]byte("2"), 3*mib/2)
	var mu sync.Mutex
	cur, etag, contents := v1, `"v1"`, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/api/v1/nodes/" + nodeID:
			writeNode(w, nodeID, "versions.bin", cur, etag)
		case "/api/v1/nodes/" + nodeID + "/content":
			if contents++; contents == 2 {
				cur, etag = v2, `"v2"`
				r.Header.Del("If-Range")
			}
			w.Header().Set("ETag", etag)
			http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(cur))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	dest := t.TempDir()
	res := getAt(srv.URL, nodeID, dest, "--piece-size", "1MiB")
	wantCode(t, res, 0)
	if b, _ := os.ReadFile(filepath.Join(dest, "versions.bin")); !bytes.Equal(b, v2) {
		t.Error("the result is not the new version")
	}
	if !strings.Contains(res.stderr, "changed") {
		t.Errorf("stderr %q does not say the file changed", res.stderr)
	}
}

// TestGetRefusesARangeItDidNotAskFor: a 206 whose Content-Range is not the range asked for is not written, even when its
// bytes happen to be right: get stops (exit 1) and names no file.
func TestGetRefusesARangeItDidNotAskFor(t *testing.T) {
	isolate(t, testAccountKey)
	nodeID := id.Derive("nod", "cli6-shifted")
	data := randomBytes(t, 3*mib/2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/nodes/" + nodeID:
			writeNode(w, nodeID, "shifted.bin", data, fakeETag)
		case "/api/v1/nodes/" + nodeID + "/content":
			a, b := rangeStart(r.Header.Get("Range")), int64(0)
			if _, end, ok := strings.Cut(r.Header.Get("Range"), "-"); ok {
				b, _ = strconv.ParseInt(end, 10, 64)
			}
			w.Header().Set("ETag", fakeETag)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", a+1, b+1, len(data)))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(data[a : b+1])
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	dest := t.TempDir()
	res := getAt(srv.URL, nodeID, dest, "--piece-size", "1MiB")
	wantCode(t, res, 1)
	if !strings.Contains(res.stderr, "another range") {
		t.Errorf("stderr %q does not say the range was not the one asked for", res.stderr)
	}
	if _, err := os.Lstat(filepath.Join(dest, "shifted.bin")); err == nil {
		t.Error("the file was named")
	}
}

// TestGetPartFileIsPrivateUntilItIsChecked: while the bytes arrive, the part file is readable by its owner alone; the
// finished file takes the mode a new file gets under the umask.
func TestGetPartFileIsPrivateUntilItIsChecked(t *testing.T) {
	isolate(t, testAccountKey)
	old := syscall.Umask(0o027)
	t.Cleanup(func() { syscall.Umask(old) })
	f := newFileServer(t, "private.bin", []byte("private bytes"), sha([]byte("private bytes")))
	dest := t.TempDir()
	var mu sync.Mutex
	var partMode os.FileMode
	seen := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/content") {
			if fi, err := os.Lstat(filepath.Join(dest, "private.bin.part")); err == nil {
				mu.Lock()
				partMode = fi.Mode().Perm()
				mu.Unlock()
			}
		}
		f.handle(w, r)
	}))
	t.Cleanup(seen.Close)
	wantCode(t, getAt(seen.URL, f.nodeID, dest), 0)
	mu.Lock()
	defer mu.Unlock()
	if partMode != 0o600 {
		t.Errorf("the part file's mode during the download is %04o, want 0600", partMode)
	}
	fi, err := os.Stat(filepath.Join(dest, "private.bin"))
	if err != nil || fi.Mode().Perm() != 0o640 {
		t.Errorf("the finished file's mode is %v (%v), want 0640 under umask 027", fi.Mode().Perm(), err)
	}
}
