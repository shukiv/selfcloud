// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package files

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

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
