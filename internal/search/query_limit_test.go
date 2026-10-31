// SPDX-License-Identifier: AGPL-3.0-or-later

package search

import (
	"strings"
	"testing"
)

// TestParseTermsRefusesALongQuery: a query over MaxQueryBytes is refused even when every term is within the other limits.
func TestParseTermsRefusesALongQuery(t *testing.T) {
	lim := Limits{MaxQueryBytes: 200, MaxTerms: 1000}
	raw := strings.Repeat("ab ", 100) // 300 bytes, 100 valid terms
	if _, err := ParseTerms(raw, lim); err == nil {
		t.Fatal("a 300-byte query passed a 200-byte limit")
	}
	if _, err := ParseTerms(strings.Repeat("ab ", 66), lim); err != nil {
		t.Fatalf("a 198-byte query was refused: %v", err)
	}
}
