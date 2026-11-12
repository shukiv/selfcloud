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
			if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
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
			if b, _ := os.ReadFile(victim); string(b) != "keep" {
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

// TestGetStartsAgainInANewPartFile: when the file changes during a download, the part file is removed and made anew,
// never emptied in place, so a name that another program gave the part file meanwhile keeps the bytes it had.
func TestGetStartsAgainInANewPartFile(t *testing.T) {
	isolate(t, testAccountKey)
	nodeID := id.Derive("nod", "cli6-new-part")
	v1, v2 := bytes.Repeat([]byte("1"), 3*mib/2), bytes.Repeat([]byte("2"), 3*mib/2)
	dest, keep := t.TempDir(), filepath.Join(t.TempDir(), "kept")
	var mu sync.Mutex
	var linkErr error
	cur, etag, contents := v1, `"v1"`, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/api/v1/nodes/" + nodeID:
			writeNode(w, nodeID, "relinked.bin", cur, etag)
		case "/api/v1/nodes/" + nodeID + "/content":
			if contents++; contents == 2 {
				linkErr = os.Link(filepath.Join(dest, "relinked.bin.part"), keep)
				cur, etag = v2, `"v2"`
			}
			w.Header().Set("ETag", etag)
			http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(cur))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	res := getAt(srv.URL, nodeID, dest, "--piece-size", "1MiB")
	mu.Lock()
	defer mu.Unlock()
	if linkErr != nil {
		t.Fatal(linkErr)
	}
	wantCode(t, res, 0)
	if b, _ := os.ReadFile(keep); !bytes.Equal(b, v1[:mib]) {
		t.Errorf("the other name of the old part file holds %d bytes, not the %d it had", len(b), mib)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "relinked.bin")); !bytes.Equal(b, v2) {
		t.Error("the result is not the new version")
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

// TestGetRefusesAPartFileReplacedDuringTheDownload: when another file is renamed over the part file while the bytes
// arrive, the download's bytes are gone and the file at the name is not the one it checked. get stops (exit 1), keeps
// the part file, and names no file.
func TestGetRefusesAPartFileReplacedDuringTheDownload(t *testing.T) {
	isolate(t, testAccountKey)
	nodeID := id.Derive("nod", "cli6-swapped-part")
	data := bytes.Repeat([]byte("D"), 3*mib/2)
	dest := t.TempDir()
	other := filepath.Join(t.TempDir(), "other")
	if err := os.WriteFile(other, []byte("someone else's"), 0o600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var swapErr error
	contents := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/api/v1/nodes/" + nodeID:
			writeNode(w, nodeID, "swapped.bin", data, fakeETag)
		case "/api/v1/nodes/" + nodeID + "/content":
			if contents++; contents == 2 {
				swapErr = os.Rename(other, filepath.Join(dest, "swapped.bin.part"))
			}
			w.Header().Set("ETag", fakeETag)
			http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	res := getAt(srv.URL, nodeID, dest, "--piece-size", "1MiB")
	mu.Lock()
	defer mu.Unlock()
	if swapErr != nil {
		t.Fatal(swapErr)
	}
	wantCode(t, res, 1)
	if !strings.Contains(res.stderr, "replaced during the download") {
		t.Errorf("stderr %q does not say the part file was replaced", res.stderr)
	}
	if _, err := os.Lstat(filepath.Join(dest, "swapped.bin")); err == nil {
		t.Error("the file was named")
	}
	if _, err := os.Lstat(filepath.Join(dest, "swapped.bin.part")); err != nil {
		t.Errorf("the part file was removed: %v", err)
	}
}
