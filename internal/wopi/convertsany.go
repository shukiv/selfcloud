// SPDX-License-Identifier: AGPL-3.0-or-later

package wopi

// ConvertsAny reports whether the document lists any conversion action on the discovery host. A document that lists none
// means the server has no conversion to offer, which is an unavailable converter rather than an unsupported type
// (decision 37).
func (doc *Document) ConvertsAny() bool {
	if doc == nil {
		return false
	}
	for _, modes := range doc.actions {
		if _, ok := modes["convert-to"]; ok {
			return true
		}
	}
	return false
}
