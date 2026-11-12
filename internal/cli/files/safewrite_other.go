// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !unix

package files

import "io/fs"

// partProblem refuses every existing part file on a platform where its owner and link count cannot be read: an old part
// file is never continued there, and --overwrite makes a new one in its place.
func partProblem(fs.FileInfo) string {
	return "cannot be checked on this platform"
}
