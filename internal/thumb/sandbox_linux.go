// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package thumb

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// errNoLandlock is returned when the kernel has no Landlock. The child then runs with no_new_privs and its limits only.
var errNoLandlock = errors.New("landlock is not available")

// LandlockABI returns the Landlock ABI version of the running kernel, or 0 when it has none. It restricts nothing, so
// the server can call it to report what the decoder will run under.
func LandlockABI() int {
	abi, _, errno := syscall.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return 0
	}
	return int(abi)
}

// fsRights returns the file-system rights a ruleset of the given ABI can handle. The kernel refuses a bit it does
// not know, so only the bits of this ABI are set. Every handled right is denied, since no rule allows any.
func fsRights(abi int) uint64 {
	r := uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_CHAR | unix.LANDLOCK_ACCESS_FS_MAKE_DIR | unix.LANDLOCK_ACCESS_FS_MAKE_REG |
		unix.LANDLOCK_ACCESS_FS_MAKE_SOCK | unix.LANDLOCK_ACCESS_FS_MAKE_FIFO | unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM)
	if abi >= 2 {
		r |= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi >= 3 {
		r |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	if abi >= 5 {
		r |= unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	}
	return r
}

// landlockAttr is struct landlock_ruleset_attr: the rights handled by the ruleset. From ABI 4 it has the network
// rights too; the size passed to the kernel says whether it is there.
type landlockAttr struct {
	fs  uint64
	net uint64
}

// setNoNewPrivs sets no_new_privs on the calling thread. No exec of a set-id file can then gain privileges, and
// Landlock may be applied. It is a refusal when it fails: the child does not decode without it. Threads the runtime
// starts later inherit it; the decoder runs on one locked thread (harden), so that is the one that matters.
func setNoNewPrivs() error {
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("no_new_privs: %w", err)
	}
	return nil
}

// restrictFiles applies a Landlock ruleset to the calling thread: it handles every file-system right and, from ABI 4,
// the TCP bind and connect rights, and it has no rule. Afterwards the thread can open no file and connect to no
// address. It returns the ABI applied, or errNoLandlock when the kernel has none. Descriptors already open keep
// working: Landlock checks a path when it is opened, so this runs after the input and output descriptors are open.
func restrictFiles() (int, error) {
	abi := LandlockABI()
	if abi == 0 {
		return 0, errNoLandlock
	}
	attr := landlockAttr{fs: fsRights(abi)}
	size := uintptr(8)
	if abi >= 4 {
		attr.net = unix.LANDLOCK_ACCESS_NET_BIND_TCP | unix.LANDLOCK_ACCESS_NET_CONNECT_TCP
		size = 16
	}
	fd, _, errno := syscall.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), size, 0)
	runtime.KeepAlive(&attr)
	if errno != 0 {
		return 0, fmt.Errorf("landlock ruleset: %w", errno)
	}
	defer syscall.Close(int(fd))
	if _, _, errno := syscall.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, fd, 0, 0); errno != 0 {
		return 0, fmt.Errorf("landlock restrict: %w", errno)
	}
	return abi, nil
}
