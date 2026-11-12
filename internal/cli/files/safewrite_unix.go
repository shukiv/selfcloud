// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package files

import (
	"io/fs"
	"os"
	"syscall"
)

// currentUID is the user a part file must belong to. Tests make it another, since they cannot chown.
var currentUID = os.Geteuid

// partProblem says why an existing part file must not be written: another user owns it, or it has another name (a hard
// link), so writing it would change another file. "" when neither.
func partProblem(fi fs.FileInfo) string {
	st, ok := fi.Sys().(*syscall.Stat_t)
	switch {
	case !ok:
		return "cannot be checked"
	case int64(st.Uid) != int64(currentUID()):
		return "belongs to another user"
	case st.Nlink != 1:
		return "has another name (a hard link), so writing it would change another file"
	}
	return ""
}
