//go:build !unix

package blob

// nofollow is not available here; blob paths are still built from server-made IDs only.
const nofollow = 0
