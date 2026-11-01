// SPDX-License-Identifier: AGPL-3.0-or-later

package names

import (
	"strconv"
	"strings"
	"sync"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// Compare orders two names for a listing sorted by name. It is the root collation with case, accents and width
// ignored, and each run of ASCII digits is compared by its value, so "file 2" comes before "file 10". Punctuation,
// digits and letters keep the root collation's order between them. Two names that differ only in case, accents or
// leading zeros compare equal; the listing breaks that tie by ID.
//
// This is the order the web interface used for names (Intl.Collator with numeric and base sensitivity, root locale).
// It is not the order of the he or ar locales: those put Hebrew or Arabic names before Latin ones, where the root order
// puts Latin first. Inside one script the orders agree. The order is stored in the indexes of the listing, so it must
// not change: a change needs a new collation name and a migration that rebuilds the indexes.
func Compare(a, b string) int {
	c := collators.Get().(*collate.Collator)
	defer collators.Put(c)
	return c.CompareString(numericKey(a), numericKey(b))
}

// numericKey rewrites each run of ASCII digits as a length prefix and the digits, without leading zeros, so that the
// collator's order of the rewritten text is the numeric order: a shorter number comes first, and equal lengths compare
// digit by digit. The prefix says how many digits the length has (1 to 3), so the prefixes themselves sort correctly
// for lengths up to 999.
func numericKey(s string) string {
	if strings.IndexFunc(s, func(r rune) bool { return r >= '0' && r <= '9' }) < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	for i := 0; i < len(s); {
		if !isDigit(s[i]) {
			b.WriteByte(s[i])
			i++
			continue
		}
		j := i
		for j < len(s) && isDigit(s[j]) {
			j++
		}
		run := trimZeros(s[i:j])
		n := strconv.Itoa(len(run))
		b.WriteString(strconv.Itoa(len(n)))
		b.WriteString(n)
		b.WriteString(run)
		i = j
	}
	return b.String()
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// trimZeros drops leading zeros from a run of digits, keeping one digit.
func trimZeros(s string) string {
	for len(s) > 1 && s[0] == '0' {
		s = s[1:]
	}
	return s
}

// collators holds root collators. A collator is not safe for concurrent use, and SQLite calls the collation from
// several connections at once.
var collators = sync.Pool{New: func() any { return collate.New(language.Und, collate.Loose) }}
