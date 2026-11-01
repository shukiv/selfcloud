// SPDX-License-Identifier: AGPL-3.0-or-later

package names

import (
	"slices"
	"testing"
)

// TestCompareOrder pins the order of a fixture. The expected order is the one Intl.Collator gives with the root
// locale, numeric and base sensitivity (node: new Intl.Collator("und", {numeric: true, sensitivity: "base"})). The
// fixture has no two names that compare equal, so the order is fixed. Hebrew and Arabic letters follow the alphabet
// inside their script. Between scripts the root order puts Latin first; Intl with the he or ar locale puts the Hebrew
// or Arabic names first instead, and that difference is stated in the decision, not tested here.
func TestCompareOrder(t *testing.T) {
	fixture := []string{
		"file 10", "file 2", "File 1", "Résumé", "resume", "שלום", "ארכיון", "תיק", "בית", "אבא",
		"ם", "מ", "ק", "مستند", "صورة", "كتاب", "كاتب", "a-b", "a b", "ab", "B", "b", "1", "10", "2",
		"a1", "a10", "a2", "_x", "#tag", "zeta", "Ω", "Ζ",
	}
	want := []string{
		"_x", "#tag", "1", "2", "10", "a b", "a-b", "a1", "a2", "a10", "ab", "B", "b", "File 1", "file 2", "file 10",
		"Résumé", "resume", "zeta", "Ζ", "Ω", "אבא", "ארכיון", "בית", "ם", "מ", "ק", "שלום", "תיק",
		"صورة", "كاتب", "كتاب", "مستند",
	}
	got := slices.Clone(fixture)
	slices.SortStableFunc(got, Compare)
	if !slices.Equal(got, want) {
		t.Fatalf("order\n got: %q\nwant: %q", got, want)
	}
}

func TestCompareTiesAreEqual(t *testing.T) {
	for _, pair := range [][2]string{
		{"Résumé", "resume"},  // accents and case
		{"file 2", "file 02"}, // leading zeros
		{"File", "file"},
	} {
		if got := Compare(pair[0], pair[1]); got != 0 {
			t.Errorf("Compare(%q, %q) = %d, want 0", pair[0], pair[1], got)
		}
		if got := Compare(pair[1], pair[0]); got != 0 {
			t.Errorf("Compare(%q, %q) = %d, want 0", pair[1], pair[0], got)
		}
	}
}

func TestCompareIsAntisymmetric(t *testing.T) {
	names := []string{"a", "A", "a2", "a10", "a 10", "שלום", "ש", "", "1", "01", "Ω"}
	for _, a := range names {
		for _, b := range names {
			if x, y := Compare(a, b), Compare(b, a); x != -y {
				t.Errorf("Compare(%q, %q)=%d but Compare(%q, %q)=%d", a, b, x, b, a, y)
			}
		}
	}
}
