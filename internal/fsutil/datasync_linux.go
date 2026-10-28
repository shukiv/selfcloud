// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package fsutil

import (
	"os"

	"golang.org/x/sys/unix"
)

func datasync(f *os.File) error {
	for {
		err := unix.Fdatasync(int(f.Fd()))
		if err != unix.EINTR {
			return err
		}
	}
}
