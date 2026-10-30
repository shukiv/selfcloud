// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/sweep"
)

// sweeper returns the housekeeping of s with the given blob grace period.
func sweeper(s *Server, grace time.Duration) *sweep.Sweeper {
	return sweep.New(sweep.Options{Store: s.store, Blobs: s.blobs, DeleteGrace: grace, Interval: time.Hour})
}

// blobFiles counts the blob files on disk.
func blobFiles(t *testing.T, s *Server) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(filepath.Join(s.cfg.DataDir, "blobs"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			n++
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestDeletedContentIsCollectedAfterGrace checks that a deleted file's contents stay on disk for the
// grace period, are removed after it, and that a second pass finds nothing to do.
func TestDeletedContentIsCollectedAfterGrace(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	a := putNode(t, s, key, f.ID, "/a.txt", "one")
	// A permanent delete: a plain delete would only move the file to the trash, where it stays.
	wantStatus(t, call(t, s, "DELETE", "/api/v1/nodes/"+a.ID+"?permanent=true", key, nil), 204)

	sw := sweeper(s, 24*time.Hour)
	now := store.Now()
	res, err := sw.Once(context.Background(), now)
	if err != nil || res.Orphaned != 1 || res.Removed != 0 {
		t.Fatalf("first pass = %+v, %v", res, err)
	}
	if n := blobFiles(t, s); n != 1 {
		t.Fatalf("files during grace = %d, want 1", n)
	}
	if _, err := sw.Once(context.Background(), now.Add(23*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n := blobFiles(t, s); n != 1 {
		t.Fatalf("files before grace ends = %d, want 1", n)
	}
	res, err = sw.Once(context.Background(), now.Add(25*time.Hour))
	if err != nil || res.Removed != 1 || res.Failed != 0 {
		t.Fatalf("pass after grace = %+v, %v", res, err)
	}
	if n := blobFiles(t, s); n != 0 {
		t.Fatalf("files after grace = %d, want 0", n)
	}
	again, err := sw.Once(context.Background(), now.Add(25*time.Hour))
	if err != nil || again.Orphaned != 0 || again.Removed != 0 {
		t.Errorf("repeat pass = %+v, %v", again, err)
	}
}

// TestCopyKeepsContentAlive checks that deleting the original of a copy does not orphan the shared
// contents: the copy still refers to them.
func TestCopyKeepsContentAlive(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	orig := putNode(t, s, key, f.ID, "/o.txt", "shared")
	cp := decode[nodeJSON](t, call(t, s, "POST", "/api/v1/nodes/"+orig.ID+"/copy", key,
		map[string]any{"on_conflict": "rename"}))
	wantStatus(t, call(t, s, "DELETE", "/api/v1/nodes/"+orig.ID, key, nil), 204)

	sw := sweeper(s, 0)
	res, err := sw.Once(context.Background(), store.Now().Add(48*time.Hour))
	if err != nil || res.Orphaned != 0 || res.Removed != 0 {
		t.Fatalf("pass = %+v, %v", res, err)
	}
	if got := rawCall(t, s, "GET", "/api/v1/nodes/"+cp.ID+"/content", key, "", nil); got.Code != 200 ||
		got.Body.String() != "shared" {
		t.Fatalf("copy after sweep: %d %q", got.Code, got.Body.String())
	}
}

// TestFolderDeleteAndReplaceLeaveContentToCollector checks that deleting a folder leaves its blobs to the
// collector, and that replacing a file's contents keeps the old content as a version: its blob is still
// referenced, so the collector leaves it alone.
func TestFolderDeleteAndReplaceLeaveContentToCollector(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	g := newFolder(t, s, key, acc.ID, "Other")
	putNode(t, s, key, f.ID, "/x.txt", "x")
	mkdirs(t, s, key, f.ID, "/d")
	putNode(t, s, key, f.ID, "/d/y.txt", "y")
	keep := putNode(t, s, key, g.ID, "/k.txt", "k")
	doc := putNode(t, s, key, g.ID, "/doc.txt", "v1")
	wantStatus(t, rawCall(t, s, "PUT", "/api/v1/nodes/"+doc.ID+"/content", key, "v2",
		map[string]string{"If-Match": doc.ETag}), 200)
	if n := blobFiles(t, s); n != 5 {
		t.Fatalf("files before = %d, want 5", n)
	}
	wantStatus(t, call(t, s, "DELETE", "/api/v1/accounts/"+acc.ID+"/folders/"+f.ID, key, nil), 204)
	if n := blobFiles(t, s); n != 5 {
		t.Fatalf("folder delete removed files at once: %d", n)
	}

	sw := sweeper(s, 0)
	res, err := sw.Once(context.Background(), store.Now())
	// The folder's two files are orphaned. The replaced content v1 is a version, so it is not.
	if err != nil || res.Orphaned != 2 || res.Removed != 2 {
		t.Fatalf("pass = %+v, %v", res, err)
	}
	if n := blobFiles(t, s); n != 3 {
		t.Fatalf("files after pass = %d, want 3 (keep.txt, the version v1, and v2)", n)
	}
	if got := rawCall(t, s, "GET", "/api/v1/nodes/"+keep.ID+"/content", key, "", nil); got.Body.String() != "k" {
		t.Errorf("surviving file = %q", got.Body.String())
	}
	if got := rawCall(t, s, "GET", "/api/v1/nodes/"+doc.ID+"/content", key, "", nil); got.Body.String() != "v2" {
		t.Errorf("replaced file = %q", got.Body.String())
	}
}

// TestHousekeepingEndsSessionsAndTokens checks that ended sessions and spent or expired tokens are
// removed, and that live ones are kept.
func TestHousekeepingEndsSessionsAndTokens(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")

	sw := sweeper(s, time.Hour)
	// The verification was used, so it goes at once; the session is still live.
	res, err := sw.Once(context.Background(), store.Now())
	if err != nil || res.Verifications < 1 || res.Sessions != 0 {
		t.Fatalf("now = %+v, %v", res, err)
	}
	// A year on, the session has ended too.
	res, err = sw.Once(context.Background(), store.Now().Add(365*24*time.Hour))
	if err != nil || res.Sessions < 1 {
		t.Fatalf("a year on = %+v, %v", res, err)
	}
}

// TestHousekeepingRunsBesideWrites runs collection passes while files are written and replaced, with
// no grace period, so anything wrongly unreferenced would be removed. Every file must still read.
func TestHousekeepingRunsBesideWrites(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	sw := sweeper(s, 0)

	errc := make(chan error, 1)
	stop := make(chan struct{})
	go func() {
		defer close(errc)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := sw.Once(context.Background(), store.Now()); err != nil {
				errc <- err
				return
			}
		}
	}()
	var files []nodeJSON
	for i := 0; i < 30; i++ {
		files = append(files, putNode(t, s, key, f.ID, "/f"+strings.Repeat("x", i%3)+string(rune('a'+i%26))+".txt",
			strings.Repeat("c", i+1)))
	}
	close(stop)
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if _, err := sw.Once(context.Background(), store.Now()); err != nil {
		t.Fatal(err)
	}
	for i, n := range files {
		got := rawCall(t, s, "GET", "/api/v1/nodes/"+n.ID+"/content", key, "", nil)
		if got.Code != 200 || got.Body.String() != strings.Repeat("c", i+1) {
			t.Fatalf("file %d after housekeeping: %d", i, got.Code)
		}
	}
}

// TestAccountDeleteRemovesItsFiles checks that the account-delete job takes the account's contents
// off disk, not only its rows.
func TestAccountDeleteRemovesItsFiles(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	other, otherKey := newAccount(t, s, admin, "Other")
	f := newFolder(t, s, key, acc.ID, "Docs")
	putNode(t, s, key, f.ID, "/a.txt", "a")
	g := newFolder(t, s, otherKey, other.ID, "Docs")
	putNode(t, s, otherKey, g.ID, "/b.txt", "b")
	if n := blobFiles(t, s); n != 2 {
		t.Fatalf("files = %d, want 2", n)
	}
	if err := s.runAccountDelete(context.Background(), &store.Job{ID: "job_test", Target: acc.ID}); err != nil {
		t.Fatal(err)
	}
	if n := blobFiles(t, s); n != 1 {
		t.Fatalf("files after account delete = %d, want 1", n)
	}
}

// TestHousekeepingRemovesStaleUploads checks that a pass removes abandoned temporary upload files and
// reports them.
func TestHousekeepingRemovesStaleUploads(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, _ := newAccount(t, s, admin, "Acme")
	dir := filepath.Join(s.cfg.DataDir, "uploads", acc.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "tmp-stale")
	if err := os.WriteFile(stale, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(stale, past, past); err != nil {
		t.Fatal(err)
	}
	res, err := sweeper(s, time.Hour).Once(context.Background(), store.Now())
	if err != nil || res.TempRemoved != 1 {
		t.Fatalf("pass = %+v, %v", res, err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Error("stale upload survived the pass")
	}
}

// TestDefaultDeleteGraceIsSevenDays checks the default that design section 12 gives.
func TestDefaultDeleteGraceIsSevenDays(t *testing.T) {
	t.Parallel()
	if got := config.Defaults().Blobs.DeleteGrace.D(); got != 7*24*time.Hour {
		t.Errorf("default delete_grace = %v, want 168h", got)
	}
}

// TestTruncateBytesKeepsCharacters checks that a cut never splits a character.
func TestTruncateBytesKeepsCharacters(t *testing.T) {
	t.Parallel()
	if got := truncateBytes("ééééé", 5); got != "éé" {
		t.Errorf("truncate = %q, want éé", got)
	}
	if got := truncateBytes("abc", 10); got != "abc" {
		t.Errorf("short string changed: %q", got)
	}
}
