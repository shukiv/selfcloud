// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package fsutil

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		magic   uint32
		name    string
		network bool
	}{
		{unix.NFS_SUPER_MAGIC, "nfs", true},
		{unix.CIFS_SUPER_MAGIC, "cifs", true},
		{unix.SMB2_SUPER_MAGIC, "smb2", true},
		{unix.FUSE_SUPER_MAGIC, "fuse", true},
		{unix.V9FS_MAGIC, "9p", true},
		{unix.EXT4_SUPER_MAGIC, "local", false},
		{unix.XFS_SUPER_MAGIC, "local", false},
		{unix.BTRFS_SUPER_MAGIC, "local", false},
		{unix.TMPFS_MAGIC, "local", false},
	} {
		name, network := classify(tc.magic)
		if name != tc.name || network != tc.network {
			t.Errorf("classify(%#x) = %q, %v; want %q, %v", tc.magic, name, network, tc.name, tc.network)
		}
	}
}

func TestTypeAndFreeBytes(t *testing.T) {
	dir := t.TempDir()
	if _, err := Type(dir); err != nil {
		t.Fatal(err)
	}
	free, err := FreeBytes(dir)
	if err != nil {
		t.Fatal(err)
	}
	if free == 0 {
		t.Error("FreeBytes reported 0 for the test directory")
	}
}
