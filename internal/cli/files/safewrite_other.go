// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !unix

package files

import "io/fs"

// partProblem says why an existing part file must not be written. The owner and the links of a file are not known on
// this platform, so only the file type is checked, by the opening itself.
func partProblem(fs.FileInfo) string { return "" }

// removeAllowed says a removal that failed may have changed something, since the permission rules of unix are not known
// on this platform, so the failure is told as a failed write.
func removeAllowed(err error) error { return err }
