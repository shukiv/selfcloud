// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !linux

package fsutil

import "os"

func datasync(f *os.File) error { return f.Sync() }
