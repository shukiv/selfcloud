// SPDX-License-Identifier: AGPL-3.0-or-later

package fsck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// An import's archive is named by its import_jobs row, with no blobs row. Its file is not an orphan.
// Mutant: drop the archive clause from BlobNamed; the archive-only blob becomes an orphan.
func TestImportArchiveBlobIsNotAnOrphan(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	bid, sha := f.commit([]byte("an archive still being imported"))
	f.importJob(bid, sha)
	f.age(f.path(bid), 2*time.Hour)
	if r := f.run(Options{VerifyContent: true}); len(r.Findings) != 0 {
		t.Fatalf("an import archive was reported: %+v", r.Findings)
	}
}

// An import's archive with no file and no blobs row is missing. Mutant: drop the archive clause from
// UnrecordedBlobsAfter; the missing archive is no longer listed.
func TestMissingImportArchiveIsFound(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	bid, sha := f.commit([]byte("the archive went missing"))
	f.importJob(bid, sha)
	f.aside(f.path(bid))
	got := find(f.run(Options{}), KindMissingBlob)
	if len(got) != 1 || got[0].ID != bid {
		t.Fatalf("missing findings = %+v, want one for the archive %q", got, bid)
	}
}

// A zip file no zips row names is an orphan once it is old enough.
func TestOrphanZipIsFound(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	zid := id.New("zip")
	p := f.zipPath(zid)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("no row names me"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.age(p, 2*time.Hour)
	got := find(f.run(Options{}), KindOrphanZip)
	if len(got) != 1 || got[0].ID != zid {
		t.Fatalf("orphan zip findings = %+v, want one for %q", got, zid)
	}
}

// A ready zip whose file is gone is a missing zip, found after the recheck.
func TestMissingZipIsFound(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	zid := f.readyZip([]byte("PK zip contents"))
	f.aside(f.zipPath(zid))
	got := find(f.run(Options{}), KindMissingZip)
	if len(got) != 1 || got[0].ID != zid {
		t.Fatalf("missing zip findings = %+v, want one for %q", got, zid)
	}
}

// A symbolic link in the blob tree is reported as stray and is not followed: its target is not judged. The row
// says the size of the real content, which the link's own size does not match, so following the link (or judging
// the link as a file) would report a size mismatch.
// Mutant: judge a non-regular entry as a blob; the link's size is then compared with the row and reported.
func TestSymlinkInBlobTreeIsStrayAndNotFollowed(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	content := []byte("the real content, longer than the link")
	target := filepath.Join(f.dir, "aside", "real.bin")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, content, 0o600); err != nil {
		t.Fatal(err)
	}
	bid := id.New("blb")
	p := f.path(bid)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
	f.row(bid, int64(len(content)), sum(content))
	r := f.run(Options{VerifyContent: true})
	strays := find(r, KindStray)
	if len(strays) != 1 || !strings.HasSuffix(strays[0].Path, bid) {
		t.Fatalf("stray findings = %+v, want the link %q", strays, bid)
	}
	if got := find(r, KindSizeBlob); len(got) != 0 {
		t.Fatalf("the link was judged as a blob: %+v", got)
	}
	if got := find(r, KindHashBlob); len(got) != 0 {
		t.Fatalf("the link's target was read: %+v", got)
	}
}

// A blob file under an account directory other than its row's account is stray, not a match for the row.
// Mutant: drop the account check in judgeBlob; the file's size and hash agree with the row, so nothing is reported.
func TestBlobUnderWrongAccountIsStray(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	content := []byte("filed under someone else")
	bid := id.New("blb")
	other := id.New("acc")
	p := filepath.Join(f.dir, "blobs", other, bid[4:6], bid[6:8], bid)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, content, 0o600); err != nil {
		t.Fatal(err)
	}
	f.row(bid, int64(len(content)), sum(content))
	f.age(p, 2*time.Hour)
	got := find(f.run(Options{VerifyContent: true}), KindStray)
	if len(got) != 1 || got[0].Account != other || got[0].ID != bid {
		t.Fatalf("stray findings = %+v, want the file under %q for %q", got, other, bid)
	}
}

// A blob row in orphan state whose file is still present is the sweep's before it removes the file. It is not a
// finding while the file matches the row.
// Mutant: BlobNamed counts only live blob rows; the orphan-state row then no longer names the file, and it is reported.
func TestOrphanStateRowWithFilePresentIsNotAFinding(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	content := []byte("the sweep has not removed me yet")
	bid, sha := f.commit(content)
	deleteAfter := time.Now().Add(time.Hour)
	if err := f.st.Write(f.ctx, func(tx *store.Tx) error {
		return tx.InsertBlob(f.ctx, &store.Blob{ID: bid, AccountID: f.acc, Size: int64(len(content)), SHA256: sha,
			State: store.BlobOrphan, DeleteAfter: &deleteAfter, CreatedAt: time.Now()})
	}); err != nil {
		t.Fatal(err)
	}
	f.age(f.path(bid), 2*time.Hour)
	if r := f.run(Options{VerifyContent: true}); len(r.Findings) != 0 {
		t.Fatalf("an orphan-state row with its file present was reported: %+v", r.Findings)
	}
}

// moveOrphan checks the age of each file again against a fresh snapshot. A file that becomes recent after the walk
// (a writer touched it) is not moved, and its finding says so.
// Mutant: remove the age check in moveOrphan; the recent file is moved.
func TestRepairRechecksMinAgeBeforeEachMove(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	b1, _ := f.commit([]byte("first old orphan"))
	b2, _ := f.commit([]byte("second old orphan"))
	p1, p2 := f.path(b1), f.path(b2)
	f.age(p1, 2*time.Hour)
	f.age(p2, 2*time.Hour)
	r := f.run(Options{Repair: true, afterMove: func() {
		// After the first move, a writer touches the file still in place. The error for the moved file is expected.
		now := time.Now()
		_ = os.Chtimes(p1, now, now)
		_ = os.Chtimes(p2, now, now)
	}})
	got := find(r, KindOrphanBlob)
	if len(got) != 2 {
		t.Fatalf("orphan findings = %d, want 2: %+v", len(got), r.Findings)
	}
	var moved, recent int
	for _, x := range got {
		switch {
		case x.Outcome == "not moved: too recent":
			recent++
		case strings.HasPrefix(x.Outcome, "moved"):
			moved++
		}
	}
	if moved != 1 || recent != 1 {
		t.Fatalf("outcomes = %+v, want one moved and one not moved: too recent", got)
	}
	inPlace := 0
	for _, p := range []string{p1, p2} {
		if _, err := os.Lstat(p); err == nil {
			inPlace++
		}
	}
	if inPlace != 1 {
		t.Fatalf("%d files still in place, want the recent one only", inPlace)
	}
}

// importJob names blob bid as the archive of an import, with no blobs row, the way an import holds its upload.
func (f *fixture) importJob(bid, sha string) {
	f.t.Helper()
	now := time.Now()
	im := &store.Import{ID: id.New("imp"), JobID: id.New("job"), AccountID: f.acc, UserID: "usr_test",
		Source: "archive", FolderID: f.folder, OnConflict: "rename", State: "running", Phase: "scan",
		ArchiveBlobID: bid, ArchiveSHA256: sha, ArchiveFormat: "zip", Actor: "{}", CreatedAt: now, UpdatedAt: now}
	if err := f.st.Write(f.ctx, func(tx *store.Tx) error { return tx.InsertImport(f.ctx, im) }); err != nil {
		f.t.Fatal(err)
	}
}

// zipPath is where a stored zip is kept: derived/zips/<account>/<zip>.zip.
func (f *fixture) zipPath(zid string) string {
	return filepath.Join(f.dir, "derived", "zips", f.acc, zid+".zip")
}

// readyZip records a ready zip with a file of the given contents and returns its ID.
func (f *fixture) readyZip(contents []byte) string {
	f.t.Helper()
	zid := id.New("zip")
	now := time.Now()
	if err := f.st.Write(f.ctx, func(tx *store.Tx) error {
		if err := tx.InsertZip(f.ctx, &store.Zip{ID: zid, AccountID: f.acc, CreatedBy: "usr_test",
			CreatedKind: "user", CreatedAt: now}); err != nil {
			return err
		}
		_, err := tx.CompleteZip(f.ctx, zid, int64(len(contents)), sum(contents), now.Add(time.Hour))
		return err
	}); err != nil {
		f.t.Fatal(err)
	}
	p := f.zipPath(zid)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, contents, 0o600); err != nil {
		f.t.Fatal(err)
	}
	return zid
}
