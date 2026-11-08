// SPDX-License-Identifier: AGPL-3.0-or-later

package fsck

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// F1. The manifest is opened before the first rename. Here the quarantine folder for today is read-only, the folders the
// blob would move into already exist, so the rename could succeed and only the manifest cannot be created. No file
// may move. Mutant: open the manifest after the rename; the blob is then moved with no line.
func TestManifestFailureMovesNothing(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not stop writes here")
	}
	f := newFixture(t)
	bid, _ := f.commit([]byte("an orphan whose manifest cannot be made"))
	f.age(f.path(bid), 2*time.Hour)
	day := time.Now().UTC().Format("2006-01-02")
	qAbs := filepath.Join(f.dir, "quarantine", day)
	dstDir := filepath.Join(qAbs, "blobs", f.acc, bid[4:6], bid[6:8])
	if err := os.MkdirAll(dstDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(qAbs, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(qAbs, 0o700) })
	_, err := Run(f.ctx, Options{DataDir: f.dir, DBPath: f.dbPath, MinAge: time.Hour, Repair: true})
	if err == nil {
		t.Fatal("a repair whose manifest cannot be created returned no error")
	}
	if _, serr := os.Lstat(f.path(bid)); serr != nil {
		t.Fatalf("the blob was moved although its manifest could not be created: %v", serr)
	}
	if _, serr := os.Lstat(filepath.Join(dstDir, bid)); serr == nil {
		t.Fatal("the blob reached quarantine with no manifest line")
	}
}
