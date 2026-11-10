// SPDX-License-Identifier: AGPL-3.0-or-later

package web

// pickEncoding chooses the encoding of an asset's answer from the request's Accept-Encoding and the variants the build holds.
// Red stub: the rules are written in the tests first.
func pickEncoding(accept string, hasBr, hasGzip bool) string {
	_, _, _ = accept, hasBr, hasGzip
	return ""
}
