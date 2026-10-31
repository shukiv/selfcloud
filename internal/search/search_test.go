// SPDX-License-Identifier: AGPL-3.0-or-later

package search

import (
	"database/sql"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite" // the FTS5 check runs the expression on the same engine the store uses
)

var lim = Limits{MaxQueryBytes: 200, MaxTerms: 8}

func TestParseTermsRefusesWhatTheLimitsRefuse(t *testing.T) {
	long := strings.Repeat("a", 65)
	cases := map[string]string{
		"too many bytes": strings.Repeat("ab ", 70),
		"nine terms":     "ab ab ab ab ab ab ab ab ab",
		"term over 64":   long,
		"one letter":     "a",
		"control":        "ab\x01cd",
		"not UTF-8":      "ab\xff",
		"only a mark":    "́́",
	}
	for name, raw := range cases {
		if _, err := ParseTerms(raw, lim); err == nil {
			t.Errorf("%s: ParseTerms(%q) accepted it", name, raw)
		}
	}
	terms, err := ParseTerms("  Report\tשָׁלוֹם  ", lim)
	if err != nil || len(terms) != 2 || terms[0] != "report" || terms[1] != "שלום" {
		t.Fatalf("ParseTerms = %q, %v; want the folded terms report and שלום", terms, err)
	}
}

func TestChooseUsesTheWordIndexForShortTerms(t *testing.T) {
	if got := Choose([]string{"report", "שלום"}); got != Substring {
		t.Errorf("long terms: index %v, want the trigram table", got)
	}
	if got := Choose([]string{"report", "ab"}); got != Words {
		t.Errorf("a two-character term: index %v, want the word table", got)
	}
	if got := Choose([]string{"דו"}); got != Words {
		t.Errorf("two Hebrew letters: index %v, want the word table", got)
	}
}

// openFTS returns an engine with the two name tables of the migration and one row, shared by the fuzz target.
var (
	ftsOnce sync.Once
	ftsDB   *sql.DB
	ftsErr  error
)

func fts(t testing.TB) *sql.DB {
	t.Helper()
	ftsOnce.Do(func() {
		ftsDB, ftsErr = sql.Open("sqlite", ":memory:")
		if ftsErr != nil {
			return
		}
		ftsDB.SetMaxOpenConns(1)
		for _, q := range []string{
			`CREATE VIRTUAL TABLE search_words USING fts5 (text, account_id, tokenize = 'unicode61 remove_diacritics 2')`,
			`CREATE VIRTUAL TABLE search_trigram USING fts5 (text, account_id, tokenize = 'trigram')`,
			`INSERT INTO search_words (rowid, text, account_id) VALUES (1, 'report 2026.pdf', 'acc_fuzz')`,
			`INSERT INTO search_trigram (rowid, text, account_id) VALUES (1, 'report 2026.pdf', 'acc_fuzz')`,
		} {
			if _, ftsErr = ftsDB.Exec(q); ftsErr != nil {
				return
			}
		}
	})
	if ftsErr != nil {
		t.Fatal(ftsErr)
	}
	return ftsDB
}

// checkExpression fails when the expression is not plain quoted phrases joined by AND. Outside the quotes only the column
// filters, AND, and the prefix star may appear.
func checkExpression(t *testing.T, expr string) {
	t.Helper()
	var outside strings.Builder
	inQuote := false
	for i := 0; i < len(expr); i++ {
		c := expr[i]
		switch {
		case inQuote && c == '"' && i+1 < len(expr) && expr[i+1] == '"':
			i++ // a doubled quote is a literal inside a phrase
		case c == '"':
			inQuote = !inQuote
			if !inQuote {
				outside.WriteByte(' ') // a phrase ends: its place in the text is a gap
			}
		case inQuote:
		default:
			outside.WriteByte(c)
		}
	}
	if inQuote {
		t.Fatalf("expression leaves a quote open: %q", expr)
	}
	rest := outside.String()
	for _, ok := range []string{"account_id:", "text:", "AND", "*"} {
		rest = strings.ReplaceAll(rest, ok, " ")
	}
	if strings.TrimSpace(rest) != "" {
		t.Fatalf("expression has syntax outside its quotes (%q): %q", strings.TrimSpace(rest), expr)
	}
}

// runMatch runs the expression on the engine; a syntax error fails the test.
func runMatch(t *testing.T, table, expr string) {
	t.Helper()
	rows, err := fts(t).Query("SELECT rowid FROM "+table+" WHERE "+table+" MATCH ?", expr)
	if err != nil {
		t.Fatalf("FTS5 refuses %q: %v", expr, err)
	}
	rows.Close()
}

// TestMatchQuotesHostileInput: each input that a raw query would read as syntax is a plain phrase here, and FTS5 accepts it.
func TestMatchQuotesHostileInput(t *testing.T) {
	for _, raw := range []string{"report OR", `"unclosed`, "NEAR(report resume)", `a" OR "b`, "report*", "-report",
		"^report", "report:x", "a AND b", "(report", `""`, "report NOT x"} {
		terms, err := ParseTerms(raw, lim)
		if err != nil {
			continue // the limits may refuse it; that is also safe
		}
		for _, idx := range []Index{Words, Substring} {
			if idx == Substring && Choose(terms) != Substring {
				continue
			}
			expr, err := Match(idx, terms, "acc_fuzz")
			if err != nil {
				t.Fatalf("Match(%q): %v", raw, err)
			}
			checkExpression(t, expr)
			table := "search_words"
			if idx == Substring {
				table = "search_trigram"
			}
			runMatch(t, table, expr)
		}
	}
}

// FuzzMatch: whatever a caller types, the expression Match builds has no syntax outside its phrases, and FTS5 accepts it.
func FuzzMatch(f *testing.F) {
	for _, seed := range []string{"report", "report OR", `"unclosed`, "NEAR(report resume)", `a" OR "b`, "report*",
		"בית", "تقرير", "-x", "^a", "a:b", "x AND y", "(", ")", "\x00", "a\"\"b", "ab cd ef"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		terms, err := ParseTerms(raw, lim)
		if err != nil || len(terms) == 0 {
			return // refused, or nothing left to search for: no expression is built
		}
		for _, idx := range []Index{Words, Substring} {
			if idx == Substring && Choose(terms) != Substring {
				continue
			}
			expr, err := Match(idx, terms, "acc_fuzz")
			if err != nil {
				t.Fatalf("Match: %v", err)
			}
			checkExpression(t, expr)
			table := "search_words"
			if idx == Substring {
				table = "search_trigram"
			}
			runMatch(t, table, expr)
		}
	})
}

// TestFingerprintBindsTheRequest: the fingerprint changes with any filter as typed, a label value included, and not with
// the limit or with the typed label tests, which follow from the filters.
func TestFingerprintBindsTheRequest(t *testing.T) {
	base := func() *Request {
		return &Request{AccountID: "acc_a", Terms: []string{"report"}, ModifiedFrom: -1, ModifiedTo: -1, SizeMin: -1, SizeMax: -1}
	}
	a := base()
	a.LabelFilters = [][2]string{{"fld_x", "abc"}}
	b := base()
	b.LabelFilters = [][2]string{{"fld_x", "xyz"}}
	if a.Fingerprint() == b.Fingerprint() {
		t.Fatal("a changed label value keeps the fingerprint")
	}
	c := base()
	c.LabelFilters, c.Limit = a.LabelFilters, 7
	if a.Fingerprint() != c.Fingerprint() {
		t.Fatal("the limit changes the fingerprint")
	}
	a.Labels = []LabelTest{{FieldID: "fld_x"}}
	if a.Fingerprint() != c.Fingerprint() {
		t.Fatal("the typed label tests change the fingerprint")
	}
}

// TestCandidateSQLBindsEveryValue: the query text holds no request value; every value is an argument.
func TestCandidateSQLBindsEveryValue(t *testing.T) {
	r := &Request{AccountID: "acc_a", Terms: []string{"x'y"}, Kind: "file",
		Exts: []string{"pdf"}, ModifiedFrom: 1, ModifiedTo: -1, SizeMin: -1, SizeMax: 9,
		Labels: []LabelTest{{FieldID: "lbf_a", column: "value_text", op: "prefix", args: []any{"abc", "abc\xff"}}}}
	query, args, err := r.candidateSQL(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"x'y", "pdf", "abc", "file"} {
		if strings.Contains(query, v) {
			t.Errorf("query text holds the value %q: %s", v, query)
		}
	}
	if len(args) == 0 {
		t.Fatal("no arguments")
	}
}
