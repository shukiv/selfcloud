// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !unix

package fsck

// nofollow is not available here; the paths the hash pass reads are built from server-made IDs only.
const nofollow = 0
