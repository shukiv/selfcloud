// SPDX-License-Identifier: AGPL-3.0-or-later

package names

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// goldenOrder is the order of goldenFixture in the root collation with numeric digits, case and accents ignored. It was
// written from node's Intl.Collator("und", {numeric: true, sensitivity: "base"}), not from Compare. The fixture has no two
// names that compare equal, so the order is strict. It covers Latin with accents, Greek, Cyrillic, Hebrew (with final
// forms), Arabic, digits and punctuation, and mixed case.
var goldenOrder = []string{
	"_x", "(draft)", "@home", "#tag", "~tmp", "1", "2", "10", "99", "a b", "a_b", "a-b", "ab", "Ælfred", "Ångström",
	"Ärger", "B", "éclair", "école", "file 1", "file 2", "File 3", "file 10", "file 100", "naïve", "Résumé", "x1", "x01x",
	"x9", "x10", "Zeta", "zeta2", "Zoë", "α", "β", "Ζ", "Ω", "арбуз", "Ёж", "Жук", "Щука", "Яблоко", "אבא", "ארכיון",
	"בית", "ך", "ם", "ק", "שלום", "תיק", "أحمد", "ابراهيم", "زيد", "صورة", "كاتب", "كتاب", "مستند",
}

// TestCompareGolden checks Compare against goldenOrder: sorting the names in any order gives this order, and each name
// compares before the next.
func TestCompareGolden(t *testing.T) {
	shuffled := slices.Clone(goldenOrder)
	slices.Reverse(shuffled)
	slices.SortStableFunc(shuffled, Compare)
	if !slices.Equal(shuffled, goldenOrder) {
		t.Fatalf("golden order\n got: %q\nwant: %q", shuffled, goldenOrder)
	}
	for i := 1; i < len(goldenOrder); i++ {
		if Compare(goldenOrder[i-1], goldenOrder[i]) >= 0 {
			t.Errorf("%q does not compare before %q", goldenOrder[i-1], goldenOrder[i])
		}
	}
}

// pinnedXText is the golang.org/x/text version the collation was built on. The name order is stored in the listing's
// indexes (migration 0035), so a different version can give a different order for the same names.
const pinnedXText = "v0.43.0"

// TestCollationDependencyPinned fails when golang.org/x/text changes version. The message says what that change needs: a
// REINDEX migration with a new collation name, and only then this pin moves.
func TestCollationDependencyPinned(t *testing.T) {
	b, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^\s*golang\.org/x/text\s+(\S+)`).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("go.mod has no golang.org/x/text requirement")
	}
	if m[1] != pinnedXText {
		t.Fatalf("golang.org/x/text is %s, pinned at %s. The name order is stored in the listing's indexes, so a new version "+
			"needs a REINDEX migration (a new collation name, with the indexes rebuilt on the old one's rows) before this pin "+
			"moves. Check goldenOrder under the new version first. Pinned at %s, go.mod says %s",
			m[1], pinnedXText, pinnedXText, m[1])
	}
	if strings.TrimSpace(m[1]) == "" {
		t.Fatal("empty version")
	}
}
