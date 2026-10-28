// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package blob

import "syscall"

// nofollow makes open fail on a symbolic link, so a planted link cannot redirect a read.
const nofollow = syscall.O_NOFOLLOW
