// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package files

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/shukiv/selfcloud/internal/cli/exitcode"
)

// withoutHardLinks makes the hard link of a finished download fail the way vfat, exFAT and some network mounts do.
func withoutHardLinks(t *testing.T) {
	t.Helper()
	old := linkPart
	t.Cleanup(func() { linkPart = old })
	linkPart = func(_ *os.Root, from, to string) error {
		return &os.LinkError{Op: "link", Old: from, New: to, Err: syscall.EPERM}
	}
}

// noReplaceRenameVia replaces the rename that never replaces, for the length of the test.
func noReplaceRenameVia(t *testing.T, fn func(root *os.Root, from, to string) error) {
	t.Helper()
	old := renameNoReplace
	t.Cleanup(func() { renameNoReplace = old })
	renameNoReplace = fn
}

// needRealNoReplace skips a test on a file system where the kernel's rename that never replaces is not available.
func needRealNoReplace(t *testing.T, dir string) {
	t.Helper()
	root := openRoot(t, dir)
	for _, n := range []string{"probe.a", "probe.b"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	err := renameNoReplace(root, "probe.a", "probe.b")
	if errors.Is(err, os.ErrExist) {
		return
	}
	if err == nil {
		t.Fatal("the rename that never replaces replaced a file")
	}
	t.Skipf("this file system or kernel has no rename that never replaces: %v", err)
}

// newFinishedPart makes a part file holding data, ready for finish.
func newFinishedPart(t *testing.T, dir, name, data string) *partFile {
	t.Helper()
	pf, err := newPart(openRoot(t, dir), name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pf.copyRange(strings.NewReader(data), int64(len(data))); err != nil {
		t.Fatal(err)
	}
	return pf
}

// TestFinishWithoutHardLinksTakesAFreeName: with no hard links, a name that is free is taken by a rename that never
// replaces, and the part file's name is gone.
func TestFinishWithoutHardLinksTakesAFreeName(t *testing.T) {
	dir := t.TempDir()
	needRealNoReplace(t, dir)
	withoutHardLinks(t)
	pf := newFinishedPart(t, dir, "f.bin", "the bytes")
	if err := pf.finish(false); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "f.bin")); err != nil || string(b) != "the bytes" {
		t.Errorf("the name holds %q, %v", b, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "f.bin.part")); err == nil {
		t.Errorf("the part file is still there")
	}
}

// TestFinishWithoutHardLinksKeepsAFileThatAppears: a file that takes the name after the download began, and even one
// that takes it at the last moment before the rename, is kept. The download stays as its part file, and the refusal says
// so, as it does when a hard link finds the name taken.
func TestFinishWithoutHardLinksKeepsAFileThatAppears(t *testing.T) {
	for _, when := range []string{"before finish", "just before the rename"} {
		t.Run(when, func(t *testing.T) {
			dir := t.TempDir()
			needRealNoReplace(t, dir)
			withoutHardLinks(t)
			pf := newFinishedPart(t, dir, "f.bin", "the download")
			final := filepath.Join(dir, "f.bin")
			put := func() {
				if err := os.WriteFile(final, []byte("someone else's"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if when == "before finish" {
				put()
			} else {
				real := renameNoReplace
				noReplaceRenameVia(t, func(root *os.Root, from, to string) error {
					put()
					return real(root, from, to)
				})
			}
			err := pf.finish(false)
			code, _, msg := exitcode.Of(err)
			if code != exitcode.Usage || !strings.Contains(msg, "f.bin.part") || !strings.Contains(msg, "--overwrite") {
				t.Fatalf("finish: exit %d, %q; want a usage refusal that names the part file and --overwrite", code, msg)
			}
			if b, _ := os.ReadFile(final); string(b) != "someone else's" {
				t.Errorf("the name holds %q, want the file that appeared", b)
			}
			if b, _ := os.ReadFile(final + ".part"); string(b) != "the download" {
				t.Errorf("the part file holds %q, want the download kept", b)
			}
		})
	}
}

// TestFinishWithoutHardLinksRefusesWhenNoReplaceIsUnsupported: when the file system gives no hard links and no rename
// that never replaces, nothing is renamed, however free the name looks. The refusal is a usage error that names the part
// file and the way out, and the part file stays.
func TestFinishWithoutHardLinksRefusesWhenNoReplaceIsUnsupported(t *testing.T) {
	for _, errno := range []syscall.Errno{syscall.EINVAL, syscall.ENOSYS, syscall.ENOTSUP} {
		t.Run(errno.Error(), func(t *testing.T) {
			dir := t.TempDir()
			withoutHardLinks(t)
			noReplaceRenameVia(t, func(*os.Root, string, string) error {
				return &os.LinkError{Op: "rename", Err: errno}
			})
			pf := newFinishedPart(t, dir, "f.bin", "the download")
			err := pf.finish(false)
			code, _, msg := exitcode.Of(err)
			if code != exitcode.Usage || !strings.Contains(msg, "f.bin.part") || !strings.Contains(msg, "--overwrite") {
				t.Fatalf("finish: exit %d, %q; want a usage refusal that names the part file and --overwrite", code, msg)
			}
			if _, err := os.Lstat(filepath.Join(dir, "f.bin")); err == nil {
				t.Errorf("the download was given its name without a rename that never replaces")
			}
			if b, _ := os.ReadFile(filepath.Join(dir, "f.bin.part")); string(b) != "the download" {
				t.Errorf("the part file holds %q, want the download kept", b)
			}
		})
	}
}

// TestFinishWithoutHardLinksReportsOtherFailures: a failure that is not "taken" and not "unsupported" is a failure of
// the write, and the part file stays.
func TestFinishWithoutHardLinksReportsOtherFailures(t *testing.T) {
	dir := t.TempDir()
	withoutHardLinks(t)
	noReplaceRenameVia(t, func(*os.Root, string, string) error { return &os.LinkError{Op: "rename", Err: syscall.EIO} })
	pf := newFinishedPart(t, dir, "f.bin", "the download")
	err := pf.finish(false)
	if err == nil || !errors.Is(err, syscall.EIO) {
		t.Fatalf("finish: %v, want the input/output error", err)
	}
	if code, _, _ := exitcode.Of(err); code == exitcode.Usage {
		t.Errorf("an I/O failure is not a usage refusal")
	}
	if _, err := os.Lstat(filepath.Join(dir, "f.bin.part")); err != nil {
		t.Errorf("the part file: %v", err)
	}
}

// TestFinishWithoutHardLinksNeverReplacesALink: a symbolic link at the name is not replaced and not followed.
func TestFinishWithoutHardLinksNeverReplacesALink(t *testing.T) {
	dir := t.TempDir()
	needRealNoReplace(t, dir)
	withoutHardLinks(t)
	if err := os.Symlink("elsewhere", filepath.Join(dir, "f.bin")); err != nil {
		t.Fatal(err)
	}
	pf := newFinishedPart(t, dir, "f.bin", "the download")
	if code, _, _ := exitcode.Of(pf.finish(false)); code != exitcode.Usage {
		t.Fatalf("finish over a link: exit %d, want %d", code, exitcode.Usage)
	}
	if got, err := os.Readlink(filepath.Join(dir, "f.bin")); err != nil || got != "elsewhere" {
		t.Errorf("the link is %q, %v; want it untouched", got, err)
	}
}
