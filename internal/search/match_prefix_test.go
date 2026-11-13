// SPDX-License-Identifier: AGPL-3.0-or-later

package search

import (
	"strings"
	"testing"
)

// TestShortContentTermDoesNotExpand (decision 46): a content term of two runes matches whole words only, so a
// short search does not match most of a large tenant's words. Two runes of Hebrew or Arabic are still a whole word. A term
// of three runes expands to a prefix, as before.
func TestShortContentTermDoesNotExpand(t *testing.T) {
	t.Parallel()
	// The whole match is compared, since a prefix match also contains the exact term as a substring.
	const head = `account_id:"acc_a" AND `
	for _, c := range []struct {
		term, want string
	}{
		{"ab", `text:"ab"`},        // two runes: exact
		{"שלום", `text:"שלום"*`},   // four runes of Hebrew: a prefix
		{"לא", `text:"לא"`},        // two runes of Hebrew: exact
		{"مرحبا", `text:"مرحبا"*`}, // five runes of Arabic: a prefix
		{"مر", `text:"مر"`},        // two runes of Arabic: exact
		{"abc", `text:"abc"*`},     // three runes: a prefix
	} {
		got, err := MatchContent([]string{c.term}, "acc_a")
		if err != nil {
			t.Fatal(err)
		}
		if got != head+c.want {
			t.Errorf("term %q: match %q, want %q", c.term, got, head+c.want)
		}
	}
}

// TestNameWordSearchKeepsItsPrefix: name search (Match) is not the content search: a short word is a word prefix there
// (decision 40), as before.
func TestNameWordSearchKeepsItsPrefix(t *testing.T) {
	t.Parallel()
	got, err := Match(Words, []string{"ab"}, "acc_a")
	if err != nil {
		t.Fatal(err)
	}
	if want := `account_id:"acc_a" AND text:"ab"*`; got != want {
		t.Errorf("name word search: %q, want %q", got, want)
	}
}

// TestSubstringIndexIsNotChangedByThePrefixRule: the trigram index (names) builds its own terms; a term there is never a
// prefix, whatever its length.
func TestSubstringIndexIsNotChangedByThePrefixRule(t *testing.T) {
	t.Parallel()
	got, err := Match(Substring, []string{"abc"}, "acc_a")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, `"abc"*`) {
		t.Errorf("a trigram term became a prefix: %q", got)
	}
}
