//go:build !linux

package fsutil

// Type cannot tell file systems apart on this platform and reports a local one.
func Type(path string) (Info, error) {
	return Info{Name: "unknown"}, nil
}

// FreeBytes is not supported on this platform.
func FreeBytes(path string) (uint64, error) {
	return 0, ErrUnsupported
}
