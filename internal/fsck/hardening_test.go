// SPDX-License-Identifier: AGPL-3.0-or-later

package fsck

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// hashFile reads the file it is given and never a symbolic link: a link planted where a blob or derived file
// belongs must not redirect the read. Mutant: open without the no-follow flag; the hash of the target is returned.
func TestHashFileRefusesASymlink(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges here")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("the target's contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if h, err := hashFile(link); err == nil {
		t.Fatalf("hashFile followed a symbolic link and returned %s", h)
	}
	if h, err := hashFile(target); err != nil || h == "" {
		t.Fatalf("hashFile of a regular file = %q, %v", h, err)
	}
}
