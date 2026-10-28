//go:build linux

package fsutil

import "golang.org/x/sys/unix"

// Type returns the file system that holds path.
func Type(path string) (Info, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return Info{}, err
	}
	// Magic numbers fit in 32 bits; the field is signed and 64-bit on some platforms.
	magic := uint32(st.Type)
	name, network := classify(magic)
	return Info{Name: name, Network: network, Magic: magic}, nil
}

// FreeBytes returns the bytes available to an unprivileged user on the file system that
// holds path.
func FreeBytes(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}

func classify(magic uint32) (name string, network bool) {
	switch magic {
	case unix.NFS_SUPER_MAGIC:
		return "nfs", true
	case unix.SMB_SUPER_MAGIC:
		return "smb", true
	case unix.SMB2_SUPER_MAGIC:
		return "smb2", true
	case unix.CIFS_SUPER_MAGIC:
		return "cifs", true
	case unix.CEPH_SUPER_MAGIC:
		return "ceph", true
	case unix.V9FS_MAGIC:
		return "9p", true
	case unix.AFS_SUPER_MAGIC, unix.AFS_FS_MAGIC:
		return "afs", true
	case unix.CODA_SUPER_MAGIC:
		return "coda", true
	case unix.FUSE_SUPER_MAGIC:
		return "fuse", true
	}
	return "local", false
}
