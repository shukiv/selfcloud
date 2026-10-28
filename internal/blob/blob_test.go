package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"selfcloud/internal/id"
)

func newDir(t *testing.T) *Dir {
	t.Helper()
	d, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// write stores body as a blob of accountID and returns what Commit reports.
func write(t *testing.T, d *Dir, accountID, body string, limit int64) Committed {
	t.Helper()
	w, err := d.Create(context.Background(), accountID, limit)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Abort()
	if _, err := io.WriteString(w, body); err != nil {
		t.Fatal(err)
	}
	c, err := w.Commit()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCommitLayoutAndContents(t *testing.T) {
	d := newDir(t)
	acc := id.New("acc")
	c := write(t, d, acc, "hello world", 100)
	h := sha256.Sum256([]byte("hello world"))
	if c.Size != 11 || c.SHA256 != hex.EncodeToString(h[:]) || !id.Valid("blb", c.ID) {
		t.Fatalf("committed = %+v", c)
	}
	// The file sits at blobs/<account>/<b1>/<b2>/<id>, where b1 and b2 come from the ID.
	want := filepath.Join(d.dataDir, "blobs", acc, c.ID[4:6], c.ID[6:8], c.ID)
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("blob not at %s: %v", want, err)
	}
	f, err := d.Open(acc, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, _ := io.ReadAll(f)
	if string(got) != "hello world" {
		t.Errorf("contents = %q", got)
	}
	// No temporary file is left behind.
	entries, _ := os.ReadDir(filepath.Join(d.dataDir, "uploads", acc))
	if len(entries) != 0 {
		t.Errorf("uploads left %d files", len(entries))
	}
}

func TestLimitAbortsAndLeavesNothing(t *testing.T) {
	d := newDir(t)
	acc := id.New("acc")
	w, err := d.Create(context.Background(), acc, 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "abc"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "de"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("write past the limit = %v, want ErrTooLarge", err)
	}
	if err := w.Abort(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(d.dataDir, "uploads", acc))
	if len(entries) != 0 {
		t.Errorf("abort left %d files", len(entries))
	}
	if _, err := w.Commit(); err == nil {
		t.Error("commit after abort succeeded")
	}
	if _, err := w.Write([]byte("x")); err == nil {
		t.Error("write after abort succeeded")
	}
}

func TestOpenRefusesSymlinkAndBadIDs(t *testing.T) {
	d := newDir(t)
	acc := id.New("acc")
	c := write(t, d, acc, "secret", 100)
	path := filepath.Join(d.dataDir, "blobs", acc, c.ID[4:6], c.ID[6:8], c.ID)

	// A link planted where a blob should be is not followed.
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("elsewhere"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Open(acc, c.ID); err == nil {
		t.Fatal("opened a symbolic link")
	}

	// Anything but a server-made ID is refused before a path is built.
	for _, bad := range []string{"", "../../etc/passwd", "blb_../x", "blb_", "acc_" + strings.Repeat("a", 26), "blb_ABCDEFGHIJKLMNOPQRSTUVWXYZ"} {
		if _, err := d.Open(acc, bad); err == nil {
			t.Errorf("Open(%q) accepted", bad)
		}
		if err := d.Remove(acc, bad); err == nil {
			t.Errorf("Remove(%q) accepted", bad)
		}
	}
	for _, bad := range []string{"", "../x", "a/b", "A", strings.Repeat("a", 65)} {
		if _, err := d.Create(context.Background(), bad, 1); err == nil {
			t.Errorf("Create for account %q accepted", bad)
		}
	}
}

func TestRemoveIsRepeatable(t *testing.T) {
	d := newDir(t)
	acc := id.New("acc")
	c := write(t, d, acc, "x", 10)
	if err := d.Remove(acc, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.Remove(acc, c.ID); err != nil {
		t.Errorf("second remove = %v", err)
	}
	if _, err := d.Open(acc, c.ID); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("open after remove = %v", err)
	}
}

func TestAccountsAreSeparateAndRemovable(t *testing.T) {
	d := newDir(t)
	a, b := id.New("acc"), id.New("acc")
	ca := write(t, d, a, "a", 10)
	cb := write(t, d, b, "b", 10)
	if err := d.RemoveAccount(a); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Open(a, ca.ID); err == nil {
		t.Error("account A's blob survived its removal")
	}
	if f, err := d.Open(b, cb.ID); err != nil {
		t.Errorf("account B lost a blob: %v", err)
	} else {
		f.Close()
	}
	// A blob of one account cannot be opened under another account's ID.
	if _, err := d.Open(a, cb.ID); err == nil {
		t.Error("blob of B opened under A")
	}
}

// TestFailedSyncRemovesTheFile checks that when the directory sync after the rename fails, the
// committed name is removed too, and Commit reports the failure.
func TestFailedSyncRemovesTheFile(t *testing.T) {
	d := newDir(t)
	acc := id.New("acc")
	orig := syncDirFn
	syncDirFn = func(string) error { return errors.New("injected") }
	defer func() { syncDirFn = orig }()

	w, err := d.Create(context.Background(), acc, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Abort()
	io.WriteString(w, "contents")
	if _, err := w.Commit(); err == nil {
		t.Fatal("commit succeeded with a failing sync")
	}
	if n := countFiles(t, filepath.Join(d.dataDir, "blobs")); n != 0 {
		t.Errorf("blob files after a failed commit = %d", n)
	}
	if n := countFiles(t, filepath.Join(d.dataDir, "uploads")); n != 0 {
		t.Errorf("temporary files after a failed commit = %d", n)
	}
}

func countFiles(t *testing.T, root string) int {
	t.Helper()
	n := 0
	filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
		if err == nil && e.Type().IsRegular() {
			n++
		}
		return nil
	})
	return n
}

// TestRemoveStaleUploads checks that only temporary files older than the cut-off go, and nothing
// else in uploads/.
func TestRemoveStaleUploads(t *testing.T) {
	d := newDir(t)
	acc := id.New("acc")
	dir := filepath.Join(d.dataDir, "uploads", acc)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, "tmp-old")
	fresh := filepath.Join(dir, "tmp-fresh")
	other := filepath.Join(dir, "keep-me")
	for _, p := range []string{old, fresh, other} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(other, past, past); err != nil {
		t.Fatal(err)
	}
	n, err := d.RemoveStaleUploads(time.Now().Add(-24 * time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("removed %d, %v; want 1", n, err)
	}
	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Error("stale temporary file survived")
	}
	for _, p := range []string{fresh, other} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was removed: %v", filepath.Base(p), err)
		}
	}
	// With no uploads directory at all, there is nothing to do.
	if n, err := newDirEmpty(t).RemoveStaleUploads(time.Now()); err != nil || n != 0 {
		t.Errorf("empty data dir: %d, %v", n, err)
	}
}

func newDirEmpty(t *testing.T) *Dir {
	t.Helper()
	return &Dir{dataDir: t.TempDir()}
}
