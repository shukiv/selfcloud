// SPDX-License-Identifier: AGPL-3.0-or-later

package search

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/store"
)

// Content search (slice q3, decision 46). A content candidate is a node whose current blob has a ready content row that
// matches every term. The candidate order is (content row, node rowid): the full-text match yields content rows in rowid
// order, and each row's nodes come from the blob index in rowid order, so the query needs no sort of the match set. The
// access check is the caller's (api), as for names: a candidate is kept only when the caller reaches the node.

// snippetTokens is how many tokens a snippet window holds, as FTS5 counts them.
const snippetTokens = 12

// Snippet markers. Control characters are dropped from extracted text when it is read (ReadText), so these two bytes
// never occur in a text and cannot be confused with it.
const (
	markStart = '\x01'
	markEnd   = '\x02'
)

// snippetMax is the longest snippet text returned, in bytes.
const snippetMax = 400

// snippetRawMax is the raw FTS snippet above which only a window around its first match is parsed. FTS returns a whole
// token, so one huge word would otherwise be copied in full for every hit.
const snippetRawMax = 64 << 10

// Snippet is the text around a content match, in search form (lower case, marks removed), and the byte ranges of the
// matched words in it: [start, end) pairs.
type Snippet struct {
	Text   string
	Ranges [][2]int
}

// ContentSQL returns the content candidate query and its arguments, for the plan test that checks it uses the indexes.
func (r *Request) ContentSQL(after Pos, limit int) (string, []any, error) {
	return r.contentSQL(after, limit)
}

// contentSQL builds the content candidate read after the position after, at most limit rows. Every value is a bound
// argument. The filters that a name search applies to its search row are applied to the node, through search_names for
// the extension, and through node_labels for label values.
func (r *Request) contentSQL(after Pos, limit int) (string, []any, error) {
	match, err := MatchContent(r.Terms, r.AccountID)
	if err != nil {
		return "", nil, err
	}
	var args []any
	var b strings.Builder
	// The order rests on INDEXED BY nodes_blob, the part the tests prove: it pins how nodes is reached, through the blob index.
	// The plan this is written for has the full-text table as the outer loop, then its text rows, then their nodes; the tests
	// check that plan's first step and the pages, not the whole loop order. Once the partial index on nodes existed
	// (bisected to 8b247ba), the planner put nodes outside the match, the pages followed the nodes' order, and hits repeated
	// or were skipped. The CROSS JOIN keywords are not proven to matter: an ordinary-JOIN mutant with the INDEXED BY passes
	// the paging and plan tests (one run on fa53d965: all three tests pass).
	b.WriteString(`SELECT st.id, n.rowid, n.id FROM search_text_fts
		CROSS JOIN search_texts st ON st.id = search_text_fts.rowid
		CROSS JOIN nodes n INDEXED BY nodes_blob ON n.blob_id = st.blob_id AND n.account_id = st.account_id AND n.kind = 'file'
		JOIN folders f ON f.id = n.folder_id`)
	if len(r.Exts) > 0 {
		b.WriteString(" JOIN search_names sn ON sn.node_id = n.id")
	}
	b.WriteString(" WHERE search_text_fts MATCH ? AND st.account_id = ? AND f.staged = 0 AND n.trashed_at IS NULL")
	args = append(args, match, r.AccountID)
	// The position is on the full-text rowid (search_text_fts.rowid = search_texts.id), so the lower bound is a constraint on
	// the full-text scan itself. On st.id it was a filter after the scan, and every batch re-ran MATCH from the start, which
	// made a search over many matches cost about the square of the matches (decision 46).
	b.WriteString(" AND search_text_fts.rowid >= ? AND (search_text_fts.rowid > ? OR n.rowid > ?)")
	args = append(args, after.A, after.A, after.B)
	if r.Kind != "" {
		b.WriteString(" AND n.kind = ?")
		args = append(args, r.Kind)
	}
	// The type group (decision 110), as the name query applies it: a file group keeps that group's files.
	if r.Group != "" {
		b.WriteString(" AND n.type_group = ?")
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
		b.WriteString(" AND EXISTS (SELECT 1 FROM node_labels nl WHERE nl.node_id = n.id AND nl.field_id = ? AND " +
			t.clause() + ")")
		args = append(args, t.FieldID)
		args = append(args, t.args...)
	}
	// No ORDER BY. SQLite does not treat a full-text MATCH as ordered by rowid, so an ORDER BY here sorts the whole match set
	// on every page, which costs as much as the matches, not the limit. The plan the query gets is the one the candidate
	// order depends on: the full-text table outermost, its content rows in rowid order, then each row's nodes in rowid order
	// through the blob index. TestContentQueryPlanSortsNoMatchSet pins that plan, so a change of plan fails there.
	b.WriteString(" LIMIT ?")
	args = append(args, limit)
	return b.String(), args, nil
}

// contentCandidates reads the content candidates after after, on q.
func (r *Request) contentCandidates(ctx context.Context, q store.Querier, after Pos, limit int) ([]Candidate, error) {
	query, args, err := r.contentSQL(after, limit)
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
		if err := rows.Scan(&c.TextID, &c.RowID, &c.NodeID); err != nil {
			return nil, err
		}
		c.At = Pos{A: c.TextID, B: c.RowID}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SnippetOf returns the snippet of a content match: the window of the text around its first matched words, with the
// matched ranges. It is read only for a hit the caller may read (the api calls it after the access check). A text row
// whose match does not highlight in FTS returns its opening words, with no ranges.
func (r *Request) SnippetOf(ctx context.Context, q store.Querier, textID int64) (Snippet, error) {
	match, err := MatchContent(r.Terms, r.AccountID)
	if err != nil {
		return Snippet{}, err
	}
	var raw string
	err = q.QueryRowContext(ctx, `SELECT snippet(search_text_fts, 0, ?, ?, '', ?) FROM search_text_fts
		WHERE search_text_fts MATCH ? AND rowid = ?`,
		string(markStart), string(markEnd), snippetTokens, match, textID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Snippet{}, nil
	}
	if err != nil {
		return Snippet{}, err
	}
	return parseSnippet(raw), nil
}

// parseSnippet turns FTS's marked text into a snippet. A huge raw text is first cut to a window around its first mark.
func parseSnippet(raw string) Snippet {
	if len(raw) > snippetRawMax {
		i := strings.IndexByte(raw, markStart)
		if i < 0 {
			i = 0
		}
		lo := max(0, i-snippetMax/2)
		hi := min(len(raw), lo+snippetRawMax)
		for lo > 0 && !utf8.RuneStart(raw[lo]) {
			lo--
		}
		for hi < len(raw) && !utf8.RuneStart(raw[hi]) {
			hi--
		}
		raw = raw[lo:hi]
	}
	var b strings.Builder
	var ranges [][2]int
	open := -1
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case markStart:
			open = b.Len()
		case markEnd:
			if open >= 0 {
				ranges = append(ranges, [2]int{open, b.Len()})
				open = -1
			}
		default:
			b.WriteByte(raw[i])
		}
	}
	return clipSnippet(b.String(), ranges)
}

// clipSnippet keeps a window of snippetMax bytes that holds the first range, and moves the ranges into it. Its ends fall on
// rune boundaries.
func clipSnippet(text string, ranges [][2]int) Snippet {
	if len(text) <= snippetMax {
		return Snippet{Text: text, Ranges: ranges}
	}
	first := 0
	if len(ranges) > 0 {
		first = ranges[0][0]
	}
	lo := max(0, first-snippetMax/3)
	for lo > 0 && !utf8.RuneStart(text[lo]) {
		lo--
	}
	hi := min(len(text), lo+snippetMax)
	for hi < len(text) && !utf8.RuneStart(text[hi]) {
		hi--
	}
	out := Snippet{Text: text[lo:hi]}
	for _, rg := range ranges {
		s, e := max(rg[0], lo), min(rg[1], hi)
		if s < e {
			out.Ranges = append(out.Ranges, [2]int{s - lo, e - lo})
		}
	}
	return out
}
