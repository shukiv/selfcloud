//go:build !linux

package fsutil

import "os"

func datasync(f *os.File) error { return f.Sync() }
