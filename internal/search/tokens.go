// SPDX-License-Identifier: AGPL-3.0-or-later

package search

import (
	"strings"
	"unicode"
)

// Tokens splits folded text the way the word table's tokenizer (unicode61, remove_diacritics 2) does: a token is a maximal
// run of token characters, and every other character separates tokens. Token characters are letters, numbers, marks,
// and private-use characters; everything else (punctuation, symbols such as emoji, spaces) separates. The text is taken as
// it is folded (names.SearchForm), so the marks have gone before the split; a mark that is left is still a token character.
// TestTokensAgreeWithFTS5 checks this against the tokenizer itself, table by table.
func Tokens(text string) []string {
	var out []string
	var b strings.Builder
	for _, r := range text {
		if isTokenChar(r) {
			b.WriteRune(r)
			continue
		}
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

func isTokenChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) || unicode.Is(unicode.Co, r)
}

// runeLen is the number of runes in s.
func runeLen(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
