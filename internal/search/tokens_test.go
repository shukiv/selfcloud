// SPDX-License-Identifier: AGPL-3.0-or-later

package search

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/names"
)

// tokenSamples are the texts the splitter is checked on: punctuation, emoji, niqqud, a combining mark, Arabic diacritics,
// and mixed scripts. Each is folded first, as a stored text and a query term are.
var tokenSamples = []string{
	"a..", "-a-", "x.a", "a.b", "...", "a.bc", "x.ab", "Résumé-2026", "hello,world", "ab😀cd", "😀😀",
	"שָׁלוֹם", "café", "مَرْحَبًا", "א.ב", "report 2026 דוח", "  spaced   out  ",
}

// TestTokensAgreeWithFTS5 (review, decision 46): the splitter gives the tokens the word table's tokenizer gives, for every
// sample. The tokenizer's own tokens are read from an fts5vocab table over a unicode61 table with the same options as the
// stored text.
func TestTokensAgreeWithFTS5(t *testing.T) {
	db := fts(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "CREATE VIRTUAL TABLE tok_check USING fts5(text, tokenize = 'unicode61 remove_diacritics 2')"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "CREATE VIRTUAL TABLE tok_vocab USING fts5vocab(tok_check, 'instance')"); err != nil {
		t.Fatal(err)
	}
	for _, raw := range tokenSamples {
		folded := names.SearchForm(raw)
		if _, err := db.ExecContext(ctx, "DELETE FROM tok_check"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, "INSERT INTO tok_check (text) VALUES (?)", folded); err != nil {
			t.Fatal(err)
		}
		rows, err := db.QueryContext(ctx, "SELECT term FROM tok_vocab ORDER BY term")
		if err != nil {
			t.Fatal(err)
		}
		var fromFTS []string
		for rows.Next() {
			var term string
			if err := rows.Scan(&term); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			fromFTS = append(fromFTS, term)
		}
		rows.Close()
		mine := Tokens(folded)
		sort.Strings(mine)
		if fromFTS == nil {
			fromFTS = []string{}
		}
		if mine == nil {
			mine = []string{}
		}
		if !reflect.DeepEqual(mine, fromFTS) {
			t.Errorf("%q (folded %q): the splitter gives %q, the tokenizer gives %q", raw, folded, mine, fromFTS)
		}
	}
}

// TestRefusedTermsNeverReachTheIndex (review, decision 46): the parser keeps the raw length rule only. The word index
// refuses a term whose one token is a single letter, and a term with no token, in CheckTerms; a name search whose terms
// are all three runes or more takes the trigram index, which reads them literally, and is not checked.
func TestRefusedTermsNeverReachTheIndex(t *testing.T) {
	t.Parallel()
	lim := Limits{MaxQueryBytes: 1000, MaxTerms: 10}
	for _, raw := range []string{"a..", "-a-", "...", "a.", "😀😀", "--"} {
		if _, err := ParseTerms(raw, lim); err != nil {
			t.Errorf("term %q (three runes or two) is refused by the parser: %v", raw, err)
		}
	}
	if _, err := ParseTerms("a", lim); err == nil {
		t.Error("a one-rune term was accepted by the parser")
	}
	// Content: any term whose only token is one letter, or that has no token, is refused.
	for _, raw := range []string{"a..", "-a-", "...", "a.", "😀😀", "--"} {
		terms, _ := ParseTerms(raw, lim)
		if err := CheckTerms(terms, true); err == nil {
			t.Errorf("content: term %q was accepted", raw)
		}
	}
	// Names on the word index (a term under three runes in the query): the same refusal. "a..", "..." have three runes, so
	// alone they take the trigram index; with a short term in the query they take the word index, see the next test.
	for _, raw := range []string{"a.", "--"} {
		terms, _ := ParseTerms(raw, lim)
		if err := CheckTerms(terms, false); err == nil {
			t.Errorf("name word index: term %q was accepted", raw)
		}
	}
	// A short term sends every term of the query to the word index, so "a.." is refused beside "ab" as well.
	if terms, _ := ParseTerms("a.. ab", lim); CheckTerms(terms, false) == nil {
		t.Error("name query: a.. beside a short term was accepted on the word index")
	}
	// Names on the trigram index (every term three runes or more): read literally, so not refused here.
	for _, raw := range []string{"-a-", "...", "1.2", "a-b"} {
		terms, _ := ParseTerms(raw, lim)
		if err := CheckTerms(terms, false); err != nil {
			t.Errorf("name trigram: term %q refused: %v", raw, err)
		}
	}
	// Two tokens are a phrase, for content and for the word index alike.
	for _, raw := range []string{"1.2", "a.b", "x.a"} {
		terms, _ := ParseTerms(raw, lim)
		if err := CheckTerms(terms, true); err != nil {
			t.Errorf("content: the phrase %q refused: %v", raw, err)
		}
	}
}

// TestAcceptedTermsMatchStrings: the MATCH expression the content search sends, for each term the parser accepts. A
// phrase of the tokens; the prefix only when the last token has wordPrefixMinRunes runes or more.
func TestAcceptedTermsMatchStrings(t *testing.T) {
	t.Parallel()
	lim := Limits{MaxQueryBytes: 1000, MaxTerms: 10}
	const head = `account_id:"acc_a" AND `
	for _, c := range []struct {
		raw, want string
	}{
		{"x.ab", head + `text:"x ab"`},        // two tokens: a phrase, the last token has two runes: no prefix
		{"1.2", head + `text:"1 2"`},          // a version number: a phrase of two tokens, exact
		{"a.b", head + `text:"a b"`},          // two one-letter tokens: a phrase, exact, no prefix
		{"x.a", head + `text:"x a"`},          // likewise
		{"x.abc", head + `text:"x abc"*`},     // the last token has three runes: a prefix
		{"abc.", head + `text:"abc"*`},        // trailing punctuation is not a token
		{"שָׁלוֹם", head + `text:"שלום"*`},    // niqqud is folded away, then four runes: a prefix
		{"café", head + `text:"cafe"*`},      // a combining mark is folded away
		{"مَرْحَبًا", head + `text:"مرحبا"*`}, // Arabic diacritics are folded away
		{"😀ab", head + `text:"ab"`},           // an emoji separates; the word is two runes: exact
	} {
		terms, err := ParseTerms(c.raw, lim)
		if err != nil {
			t.Fatalf("term %q refused: %v", c.raw, err)
		}
		got, err := MatchContent(terms, "acc_a")
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("term %q: MATCH %q, want %q", c.raw, got, c.want)
		}
	}
}

// TestShortTokenDoesNotExpandInTheRealTable (review, decision 46): run against a real FTS5 table, the expression the
// splitter gives for a term of one letter with punctuation matches only the word that is that letter. The words that begin
// with it do not match.
func TestShortTokenDoesNotExpandInTheRealTable(t *testing.T) {
	db := fts(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "CREATE VIRTUAL TABLE expand_check USING fts5(text, account_id, tokenize = 'unicode61 remove_diacritics 2')"); err != nil {
		t.Fatal(err)
	}
	for i, text := range []string{"apple", "a", "abc", "a b", "bab"} {
		if _, err := db.ExecContext(ctx, "INSERT INTO expand_check (rowid, text, account_id) VALUES (?, ?, 'acc_a')", i+1, text); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{"a..", "a.", "-a-"} {
		// The parser refuses these; the index must not expand them either, if they reach it.
		expr, err := MatchContent([]string{names.SearchForm(raw)}, "acc_a")
		if err != nil {
			t.Fatal(err)
		}
		rows, err := db.QueryContext(ctx, "SELECT text FROM expand_check WHERE expand_check MATCH ? ORDER BY rowid", expr)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			got = append(got, s)
		}
		rows.Close()
		if strings.Join(got, "|") != "a|a b" {
			t.Errorf("%q: MATCH %q found %q, want the word a only (\"a\", \"a b\")", raw, expr, got)
		}
	}
}
