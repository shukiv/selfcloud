// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package files

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/shukiv/selfcloud/internal/cli/exitcode"
)

// renameNoReplace renames from to to inside root, and fails with an error that is fs.ErrExist when to is taken, whatever
// it is: the kernel decides, so a file that appears at the name at any moment is kept. The directory is the root's own,
// opened through it, so the call goes to the directory the root holds and no path is resolved again. Tests replace it.
var renameNoReplace = func(root *os.Root, from, to string) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	sc, err := dir.SyscallConn()
	if err != nil {
		return err
	}
	var rerr error
	if err := sc.Control(func(fd uintptr) {
		rerr = unix.Renameat2(int(fd), from, int(fd), to, unix.RENAME_NOREPLACE)
	}); err != nil {
		return err
	}
	if rerr != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: rerr}
	}
	return nil
}

// takeNameWithoutLinks gives a finished part file its name on a file system with no hard links, by a rename that never
// replaces a file. When the file system has no such rename either, nothing is renamed: a check and then a rename would
// leave a window where a file that appeared at the name is replaced. The part file is kept, and the refusal names the
// way out.
func (pf *partFile) takeNameWithoutLinks() error {
	if !pf.stillAtName() {
		return pf.replacedError()
	}
	err := renameNoReplace(pf.root, pf.part, pf.name)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrExist):
		return pf.appearedError()
	case errors.Is(err, syscall.EINVAL), errors.Is(err, syscall.ENOSYS), errors.Is(err, syscall.ENOTSUP):
		return exitcode.Usagef("this file system has no hard links and no rename that leaves an existing file alone, so "+
			"%s cannot be given its name without a risk of replacing a file that appears there; the download is kept "+
			"as %s; --overwrite replaces whatever is at the name, or write to another file system", pf.name, pf.part)
	}
	return fmt.Errorf("cannot give the download its name: %w", err)
}
