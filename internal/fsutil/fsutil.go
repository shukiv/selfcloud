// Package fsutil reports facts about the file system under a path.
package fsutil

import (
	"errors"
	"os"
)

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

// Datasync makes f's data durable, and the metadata needed to read it back, as fdatasync(2)
// does. Where that call is missing it falls back to a full Sync.
func Datasync(f *os.File) error { return datasync(f) }
