//go:build unix

package blob

import "syscall"

// nofollow makes open fail on a symbolic link, so a planted link cannot redirect a read.
const nofollow = syscall.O_NOFOLLOW
