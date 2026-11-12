// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !unix

package files

import "io/fs"

// partProblem refuses every existing part file on this platform: the owner and the links of a file are not known here,
// so the safe-write rule cannot be checked and a part file is never continued.
func partProblem(fs.FileInfo) string { return "cannot be checked on this platform" }

// removeAllowed says a removal that failed may have changed something, since the permission rules of unix are not known
// on this platform, so the failure is told as a failed write.
func removeAllowed(err error) error { return err }
