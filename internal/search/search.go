// SPDX-License-Identifier: AGPL-3.0-or-later

// Package search finds nodes by name (design section 9 and 14, slice q, decision 40). It holds the read side: parsing a
// query, building its full-text expression, the candidate read, the cursor, and the job that indexes the backlog. The
// access check is the api package's: a candidate is returned only when the caller reaches it, and the check reads in the
// same snapshot as the candidates (store.Read).
//
// User text never reaches MATCH as syntax. Every term is folded (names.SearchForm), checked, and quoted by Match, which
// is the only place that builds the expression. Every value goes in as a bound argument.
package search

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/jobs"
	"github.com/shukiv/selfcloud/internal/names"
	"github.com/shukiv/selfcloud/internal/store"
)

// maxTermBytes is the longest term a query may hold, in bytes, as typed.
const maxTermBytes = 64

// minTermRunes is the fewest characters a term may have. A one-letter term matches nearly every name, and its prefix
// scan costs about 20 ms at a million names, so it is refused.
const minTermRunes = 2

// trigramRunes is the fewest characters a trigram term can match. A shorter query is answered from the word index.
const trigramRunes = 3

// Limits are the query limits of the configuration.
type Limits struct {
	MaxQueryBytes int
	MaxTerms      int
}

// ParseTerms checks the text of a query and returns its folded terms. Anything it refuses is invalid_request, and names
// the limit.
func ParseTerms(raw string, lim Limits) ([]string, error) {
	if len(raw) > lim.MaxQueryBytes {
		return nil, apierr.Newf(apierr.InvalidRequest, "q may be at most %d bytes", lim.MaxQueryBytes)
	}
	if !utf8.ValidString(raw) {
		return nil, apierr.New(apierr.InvalidRequest, "q must be valid UTF-8")
	}
	for _, r := range raw {
		if unicode.IsControl(r) && !unicode.IsSpace(r) {
			return nil, apierr.New(apierr.InvalidRequest, "q may not contain control characters")
		}
	}
	fields := strings.FieldsFunc(raw, unicode.IsSpace)
	if len(fields) > lim.MaxTerms {
		return nil, apierr.Newf(apierr.InvalidRequest, "q may have at most %d terms", lim.MaxTerms)
	}
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(f) > maxTermBytes {
			return nil, apierr.Newf(apierr.InvalidRequest, "a term may be at most %d bytes", maxTermBytes)
		}
		t := names.SearchForm(f)
		if utf8.RuneCountInString(t) < minTermRunes {
			return nil, apierr.Newf(apierr.InvalidRequest, "a term needs at least %d characters", minTermRunes)
		}
		out = append(out, t)
	}
	return out, nil
}

// Index is the full-text table a query reads.
type Index int

const (
	// Words is the unicode61 table: each term matches the start of a word.
	Words Index = iota
	// Substring is the trigram table: each term matches anywhere in the name.
	Substring
)

// Choose returns the index for terms: the trigram table when every term is long enough for it, else the word table.
// Mixed lengths use the word table for all terms, so a short term still matches.
func Choose(terms []string) Index {
	for _, t := range terms {
		if utf8.RuneCountInString(t) < trigramRunes {
			return Words
		}
	}
	return Substring
}

// CheckTerms refuses the terms the word index cannot search as words (invalid_request). It applies to every content search,
// and to a name search whose terms take the word index (Choose). A term with no letter or digit, or whose only token has
// fewer than minTermRunes runes, is refused: as a prefix it would expand one letter across every tenant (review, decision
// 46). A term with two tokens or more is a phrase, as Match builds it. A name search whose terms are all three runes or
// longer takes the trigram index, which reads the name literally, and is not checked here (so "1.2" and "a-b" are found).
func CheckTerms(terms []string, content bool) error {
	if !content && Choose(terms) != Words {
		return nil
	}
	for _, t := range terms {
		toks := Tokens(t)
		if len(toks) == 0 || (len(toks) == 1 && runeLen(toks[0]) < minTermRunes) {
			return apierr.Newf(apierr.InvalidRequest, "a term needs at least %d letters or digits", minTermRunes)
		}
	}
	return nil
}

var accountPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Match builds the full-text expression for terms in index, limited to accountID. Each term is a quoted phrase, with
// any quote inside it doubled, so no term can be read as syntax. A word-index term is a prefix.
func Match(index Index, terms []string, accountID string) (string, error) {
	return match(index, terms, accountID, 0)
}

// MatchContent is the match of a content search. A term expands to a prefix only from wordPrefixMinRunes runes: a shorter
// prefix matches a large share of a tenant's words, so a short search would cost what the largest tenant's corpus costs
// (decision 46). A shorter term matches whole words only. Name search keeps Match, where a short word is a
// prefix (decision 40).
func MatchContent(terms []string, accountID string) (string, error) {
	return match(Words, terms, accountID, wordPrefixMinRunes)
}

// match builds the expression. A word term of at least minRunes runes is a prefix; the other word terms are exact.
func match(index Index, terms []string, accountID string, minRunes int) (string, error) {
	if len(terms) == 0 {
		return "", errors.New("search: no terms")
	}
	if !accountPattern.MatchString(accountID) {
		return "", errors.New("search: account ID is not in the ID format")
	}
	parts := make([]string, 0, len(terms)+1)
	parts = append(parts, "account_id:"+quote(accountID))
	for _, t := range terms {
		if index != Words {
			// The trigram table reads the name as it is: a substring, never a prefix.
			parts = append(parts, "text:"+quote(t))
			continue
		}
		// A word term is the tokens the tokenizer makes of it, as one phrase: "x.ab" is the words x and ab, next to each
		// other. The phrase has no prefix, unless its last token has at least minRunes runes: then the last token is a
		// prefix (review, decision 46). The check is on the tokens, so punctuation cannot shorten or lengthen a prefix.
		toks := Tokens(t)
		if len(toks) == 0 {
			return "", errors.New("search: a term has no tokens")
		}
		phrase := strings.Join(toks, " ")
		star := ""
		if runeLen(toks[len(toks)-1]) >= minRunes {
			star = "*"
		}
		parts = append(parts, "text:"+quote(phrase)+star)
	}
	return strings.Join(parts, " AND "), nil
}

// wordPrefixMinRunes is the shortest content term whose last token expands to a prefix (see MatchContent).
const wordPrefixMinRunes = 3

func quote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// LabelTest is one predicate on a label value. Its column is one of the typed columns of node_labels, chosen from the
// field's type, never from the caller.
type LabelTest struct {
	FieldID    string
	Restricted bool
	column     string // value_text, value_num, value_date, value_user or value_choice
	op         string // "prefix", "range" or "equal"
	args       []any
}

// NewLabelTest makes a test on one field. Its column comes from the field's type; its arguments are the bound values.
func NewLabelTest(f *store.LabelField, op string, args ...any) (LabelTest, error) {
	t := LabelTest{FieldID: f.ID, Restricted: f.Restricted, args: args}
	switch {
	case f.Type == "text" && op == "prefix":
		t.column, t.op = "value_text", "prefix"
	case f.Type == "number" && op == "range":
		t.column, t.op = "value_num", "range"
	case f.Type == "date" && op == "range":
		t.column, t.op = "value_date", "range"
	case f.Type == "user" && op == "equal":
		t.column, t.op = "value_user", "equal"
	case f.Type == "choice" && op == "equal":
		t.column, t.op = "value_choice", "equal"
	default:
		return LabelTest{}, apierr.Newf(apierr.InvalidRequest, "label field %s cannot be searched this way", f.ID)
	}
	return t, nil
}

// clause is the SQL condition on node_labels nl for the test. It is built from constants only.
func (t LabelTest) clause() string {
	switch t.op {
	case "prefix":
		return "nl." + t.column + " >= ? AND nl." + t.column + " < ?"
	case "range":
		return "nl." + t.column + " >= ? AND nl." + t.column + " <= ?"
	default:
		return "nl." + t.column + " = ?"
	}
}

// Pos is a point in a candidate read. A name search reads its rows in A alone (the search row's ID). A content search
// reads in (A, B): the content row's ID, then the node's rowid, since one text can belong to several nodes.
type Pos struct{ A, B int64 }

// Request is a parsed search, ready to read. Terms are folded; a request with no terms is a filter-only search.
type Request struct {
	AccountID string
	// In is "" (names) or InContent (the extracted text of files, decision 46). It is omitted from the fingerprint when
	// empty, so a name cursor keeps its fingerprint.
	In    string `json:",omitempty"`
	Terms []string
	Kind  string // "", file, dir or shortcut
	Exts  []string
	// Group is a file type group (decision 110): "" for none, else a group ID that keeps the files of that group. It is
	// omitted from the fingerprint when empty, so a search with no type keeps its fingerprint.
	Group string `json:",omitempty"`
	// ModifiedFrom and ModifiedTo bound the modified time in unix milliseconds: From inclusive, To exclusive. -1 is none.
	ModifiedFrom, ModifiedTo int64
	// SizeMin and SizeMax bound the size in bytes, both inclusive. -1 is none.
	SizeMin, SizeMax int64
	// LabelFilters are the label filters as the caller typed them: field ID and raw value. They are part of the
	// fingerprint, so a cursor is bound to the values it was made for.
	LabelFilters [][2]string
	// Labels are the typed tests, made from LabelFilters once the account's fields are read. They are not in the
	// fingerprint: the filters are, and the tests follow from them.
	Labels []LabelTest `json:"-"`
	// ScopeID is a directory the results must lie below, or "" for none.
	ScopeID string
	Limit   int
}

// InContent is the value of Request.In for a search over the extracted text of files.
const InContent = "content"

// Candidate is one row read from the index. A name candidate has RowID (its search row) and NodeID. A content candidate
// has TextID (its content row, which the snippet reads) and RowID, the node's rowid, and NodeID.
type Candidate struct {
	RowID  int64
	TextID int64
	NodeID string
	At     Pos // the position after this candidate
}

// ordinaryLabels returns the tests that run in the candidate SQL: the ones on fields that are not restricted.
func (r *Request) ordinaryLabels() []LabelTest {
	var out []LabelTest
	for _, t := range r.Labels {
		if !t.Restricted {
			out = append(out, t)
		}
	}
	return out
}

// candidateSQL builds the candidate read after rowid after, at most limit rows. Every value is an argument.
func (r *Request) candidateSQL(after int64, limit int) (string, []any, error) {
	var args []any
	var b strings.Builder
	if len(r.Terms) > 0 {
		idx := Choose(r.Terms)
		table := "search_words"
		if idx == Substring {
			table = "search_trigram"
		}
		match, err := Match(idx, r.Terms, r.AccountID)
		if err != nil {
			return "", nil, err
		}
		// The full-text table is the outer loop (CROSS JOIN keeps the planner from reordering), and the position is on its
		// rowid, which is sn.id. On sn.id the planner ran the match once per name row of the account, for every batch
		// (decision 46 measured the same shape for content; decision 54 review).
		b.WriteString("SELECT sn.id, sn.node_id FROM " + table + " CROSS JOIN search_names sn ON sn.id = " + table + ".rowid")
		b.WriteString(" JOIN nodes n ON n.id = sn.node_id JOIN folders f ON f.id = n.folder_id")
		b.WriteString(" WHERE " + table + " MATCH ? AND sn.account_id = ? AND " + table + ".rowid > ? AND f.staged = 0 AND n.trashed_at IS NULL")
		args = append(args, match, r.AccountID, after)
	} else {
		b.WriteString("SELECT sn.id, sn.node_id FROM search_names sn JOIN nodes n ON n.id = sn.node_id")
		b.WriteString(" JOIN folders f ON f.id = n.folder_id")
		b.WriteString(" WHERE sn.account_id = ? AND sn.id > ? AND f.staged = 0 AND n.trashed_at IS NULL")
		args = append(args, r.AccountID, after)
	}
	if r.Kind != "" {
		b.WriteString(" AND sn.kind = ?")
		args = append(args, r.Kind)
	}
	if r.Group != "" {
		b.WriteString(" AND n.kind = 'file' AND n.type_group = ?")
		args = append(args, r.Group)
	}
	if len(r.Exts) > 0 {
		marks := make([]string, len(r.Exts))
		for i, e := range r.Exts {
			marks[i] = "?"
			args = append(args, e)
		}
		b.WriteString(" AND sn.ext IN (" + strings.Join(marks, ", ") + ")")
	}
	if r.ModifiedFrom >= 0 {
		b.WriteString(" AND n.modified_at >= ?")
		args = append(args, r.ModifiedFrom)
	}
	if r.ModifiedTo >= 0 {
		b.WriteString(" AND n.modified_at < ?")
		args = append(args, r.ModifiedTo)
	}
	if r.SizeMin >= 0 {
		b.WriteString(" AND n.size >= ?")
		args = append(args, r.SizeMin)
	}
	if r.SizeMax >= 0 {
		b.WriteString(" AND n.size <= ?")
		args = append(args, r.SizeMax)
	}
	for _, t := range r.ordinaryLabels() {
		b.WriteString(" AND EXISTS (SELECT 1 FROM node_labels nl WHERE nl.node_id = sn.node_id AND nl.field_id = ? AND " +
			t.clause() + ")")
		args = append(args, t.FieldID)
		args = append(args, t.args...)
	}
	// A match yields its rows in rowid order, which is sn.id, so a term search needs no ORDER BY. With one, SQLite sorts
	// every match after the position before it returns the first batch: a batch then costs all the matches that follow
	// it. Without it the batch stops after its limit (the plan test checks there is no sort).
	if len(r.Terms) == 0 {
		b.WriteString(" ORDER BY sn.id")
	}
	b.WriteString(" LIMIT ?")
	args = append(args, limit)
	return b.String(), args, nil
}

// NameSQL returns the name candidate query and its arguments, for the plan test that checks it uses the indexes.
func (r *Request) NameSQL(after int64, limit int) (string, []any, error) {
	return r.candidateSQL(after, limit)
}

// Candidates reads up to limit candidates after the position after, in order, on q. The reach check is not made here.
// A name search orders by search row; a content search orders by content row, then by node (contentSQL).
func (r *Request) Candidates(ctx context.Context, q store.Querier, after Pos, limit int) ([]Candidate, error) {
	if r.In == InContent {
		return r.contentCandidates(ctx, q, after, limit)
	}
	query, args, err := r.candidateSQL(after.A, limit)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		var c Candidate
		if err := rows.Scan(&c.RowID, &c.NodeID); err != nil {
			return nil, err
		}
		c.At = Pos{A: c.RowID}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RestrictedHolds reports whether node nodeID has a value that the restricted test t matches. It is read on the same
// snapshot as the reach check, and only for a caller who is a manager of the node.
func RestrictedHolds(ctx context.Context, q store.Querier, nodeID string, t LabelTest) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, "SELECT 1 FROM node_labels nl WHERE nl.node_id = ? AND nl.field_id = ? AND "+t.clause(),
		append([]any{nodeID, t.FieldID}, t.args...)...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Fingerprint identifies a request without its limit, so a cursor is only valid for the request that made it. It is made
// from the request as typed, not from the typed label tests, so it does not depend on when the fields were read.
func (r *Request) Fingerprint() string {
	c := *r
	c.Limit = 0
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}

// RunIndexJob is the handler of the search job. It indexes one batch of the backlog and yields, so the jobs queued
// behind it run between batches. When the backlog is empty it returns nil. Each batch is its own write, so a crash
// loses at most the batch in progress, which runs again.
func RunIndexJob(ctx context.Context, st *store.Store) error {
	done, err := st.SearchIndexStep(ctx, store.SearchBatch)
	if err != nil {
		return fmt.Errorf("search index: %w", err)
	}
	if done {
		return nil
	}
	return jobs.ErrYield
}
