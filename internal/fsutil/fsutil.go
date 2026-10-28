// Package fsutil reports facts about the file system under a path.
package fsutil

import "errors"

// ErrUnsupported is returned where the platform cannot answer.
var ErrUnsupported = errors.New("fsutil: not supported on this platform")

// Info describes the file system that holds a path.
type Info struct {
	Name string // "nfs", "cifs", "fuse", ... or "local"
	// Network is true for network file systems and for FUSE, which is often network-backed.
	// Neither can be trusted with the POSIX locks and fsync that SQLite relies on.
	Network bool
	Magic   uint32
}
