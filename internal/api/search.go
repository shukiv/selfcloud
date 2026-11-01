// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/jobs"
	"github.com/shukiv/selfcloud/internal/names"
	"github.com/shukiv/selfcloud/internal/search"
	"github.com/shukiv/selfcloud/internal/store"
)

// Search bounds (design section 9, slice q). A page holds at most searchMaxLimit nodes; a request reads candidates in
// batches of searchBatch, and the whole request reads at most the configured scan budget.
const (
	searchDefaultLimit = 25
	searchMaxLimit     = 50
	searchBatch        = 100
	searchMaxFilters   = 8 // extensions
	searchMaxLabels    = 4
)

var (
	searchNodeID  = regexp.MustCompile(`^nod_[a-z0-9]{1,64}$`)
	searchIDPat   = regexp.MustCompile(`^[a-z]{2,6}_[a-z0-9]{1,64}$`)
	searchDatePat = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
)

// searchHit is one result: a node the caller reaches, and for a content search the snippet of its text that matched.
type searchHit struct {
	nodeJSON
	Snippet *snippetJSON `json:"snippet,omitempty"`
}

// snippetJSON is a content match's text (search form, plain text, never markup) and its [start, end) byte ranges.
type snippetJSON struct {
	Text   string   `json:"text"`
	Ranges [][2]int `json:"ranges"`
}

// searchParams is the request's parameters, checked but not yet resolved against the account's label fields.
type searchParams struct {
	q       string
	in      string // "" (names) or search.InContent
	kind    string
	exts    []string
	mFrom   int64
	mTo     int64
	sMin    int64
	sMax    int64
	labels  [][2]string // field ID, raw value
	scope   string
	limit   int
	cursor  string
	inNamed bool
}

// handleSearch finds the nodes whose name matches a query, and whose label values match the filters, that the caller
// reaches. The response is a page of nodes, with no total: a short page carries a cursor when the scan stopped before
// the page was full, and that is all it says about the matches it did not return (design section 4.3).
//
// Any signed-in user may search, and so may a delegated token, confined to its folders and node scope with its role
// capped. A share-link visitor is refused by the route. Trashed and staged nodes never match, and a restricted label
// matches only for a manager of the node; for anyone else the response is the same as if the value were not set.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := callerCheck(p); err != nil {
		return err
	}
	if !s.cfg.Search.Enabled {
		return apierr.New(apierr.NotFound, "search is not turned on for this server")
	}
	// A signed-in user, an account key and a token of either are allowed: a key reaches every folder of its own account,
	// and a token is confined to its folders and node scope, as on every other route (decision 40). A share-link visitor
	// never reaches this handler; the route refuses it.
	if p.Kind != auth.KindUser && p.Kind != auth.KindAccountKey {
		return apierr.New(apierr.Forbidden, "only a signed-in user, an account key or a delegated token may search")
	}
	ps, err := parseSearchParams(r.URL.Query())
	if err != nil {
		return err
	}
	lim := search.Limits{MaxQueryBytes: s.cfg.Search.MaxQueryBytes, MaxTerms: s.cfg.Search.MaxTerms}
	terms, err := search.ParseTerms(ps.q, lim)
	if err != nil {
		return err
	}
	if err := search.CheckTerms(terms, ps.in == search.InContent); err != nil {
		return err
	}
	if len(terms) == 0 && !ps.hasFilter() {
		return apierr.New(apierr.InvalidRequest, "q or a filter is required")
	}
	if ps.in == search.InContent {
		// A content search reads the text of files: it needs terms, and it finds files only (a folder has no text).
		if len(terms) == 0 {
			return apierr.New(apierr.InvalidRequest, "in=content needs q")
		}
		if ps.kind != "" && ps.kind != store.KindFile {
			return apierr.New(apierr.InvalidRequest, "in=content finds files only: kind must be file")
		}
	}
	if err := s.rateLimit("search", string(p.Kind)+":"+p.ID); err != nil {
		return err
	}
	// Per account as well as per caller: a tenant with many users or tokens would otherwise multiply the caller's limit
	// (review S5). The account's share is ten callers' worth.
	if err := s.rateLimit("search.account", p.AccountID); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.Search.Timeout.D())
	defer cancel()

	req := &search.Request{AccountID: p.AccountID, In: ps.in, Terms: terms, Kind: ps.kind, Exts: ps.exts,
		ModifiedFrom: ps.mFrom, ModifiedTo: ps.mTo, SizeMin: ps.sMin, SizeMax: ps.sMax,
		LabelFilters: ps.labels, ScopeID: ps.scope, Limit: ps.limit}
	var out listResponse[searchHit]
	err = s.store.Read(ctx, func(q store.Querier) error {
		// The label tests are read in the snapshot first. The cursor is opened after them, against the request as it was
		// typed (its fingerprint includes the raw filters), so a changed filter makes the cursor invalid.
		tests, err := resolveLabelTests(ctx, q, p.AccountID, ps.labels)
		if err != nil {
			return err
		}
		req.Labels = tests
		var start search.Pos
		if ps.cursor != "" {
			if start, err = s.openSearchCursor(p, req, ps.cursor); err != nil {
				return err
			}
		}
		out, err = s.searchIn(ctx, q, p, req, ps, start)
		return err
	})
	if errors.Is(err, context.DeadlineExceeded) {
		return apierr.New(apierr.Unavailable, "the search took too long; try a narrower query")
	}
	if err != nil {
		return err
	}
	if out.Items == nil {
		out.Items = []searchHit{}
	}
	ptrs := make([]*nodeJSON, len(out.Items))
	for i := range out.Items {
		ptrs[i] = &out.Items[i].nodeJSON
	}
	if len(ptrs) > 0 {
		if err := s.addPeopleNames(ctx, p, p.AccountID, ptrs...); err != nil {
			return err
		}
		if err := s.withTargets(ctx, p, ptrs...); err != nil {
			return err
		}
		if err := s.withThumbnails(ctx, p, p.AccountID, ptrs...); err != nil {
			return err
		}
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// searchIn runs one search on the snapshot q. It reads candidates in batches, checks each batch's access in the same
// snapshot, and stops when the page is full, the candidates end, or the scan budget is used up.
func (s *Server) searchIn(ctx context.Context, q store.Querier, p *auth.Principal, req *search.Request,
	ps searchParams, after search.Pos) (listResponse[searchHit], error) {
	var out listResponse[searchHit]
	within := ""
	if p.Token != nil {
		within = p.Token.Node
	}
	if req.ScopeID != "" {
		// The scope must be a directory the caller reaches, or it is not_found, as for any node the caller cannot see.
		rows, err := reachRowsIn(ctx, q, p, []string{req.ScopeID}, within)
		if err != nil {
			return out, err
		}
		if len(rows) != 1 || rows[0].NodeID != req.ScopeID || reachRole(p, rows[0]) == "" {
			return out, apierr.New(apierr.NotFound, "no such directory")
		}
		nodes, err := store.NodesIn(ctx, q, p.AccountID, []string{req.ScopeID})
		if err != nil {
			return out, err
		}
		if n := nodes[req.ScopeID]; n == nil || n.Kind != store.KindDir {
			return out, apierr.New(apierr.InvalidRequest, "scope must be a directory")
		}
		// Below the scope: the scope becomes the node the reach rows test "within" against. The token's own scope
		// contains it, as the check above established.
		within = req.ScopeID
	}
	// The only things that stop the walk are the items the caller may read and the end of the candidates. A candidate
	// the caller cannot read, or one the no-download or label rules drop, moves the walk on and decides nothing else: a
	// cursor, a short page or a count is never a sign of such a row (review S1). The work is bounded by the request's
	// deadline, which answers unavailable.
	limit := req.Limit
	exhausted := false
	for len(out.Items) < limit {
		cands, err := req.Candidates(ctx, q, after, searchBatch)
		if err != nil {
			return out, storeErr(err)
		}
		if len(cands) == 0 {
			exhausted = true
			break
		}
		ids := make([]string, len(cands))
		for i, c := range cands {
			ids[i] = c.NodeID
		}
		rows, err := reachRowsIn(ctx, q, p, ids, within)
		if err != nil {
			return out, err
		}
		byID := make(map[string]store.ReachRow, len(rows))
		for _, row := range rows {
			byID[row.NodeID] = row
		}
		nodes, err := store.NodesIn(ctx, q, p.AccountID, ids)
		if err != nil {
			return out, storeErr(err)
		}
		full := false
		for _, c := range cands {
			after = c.At
			row, ok := byID[c.NodeID]
			if !ok {
				continue
			}
			role := reachRole(p, row)
			if role == "" || nodes[c.NodeID] == nil {
				continue
			}
			if req.ScopeID != "" && !row.Within {
				continue
			}
			// A content match reads the node's text, so the no-download rule applies to it: a viewer or a commenter that
			// the rule binds gets no content hit from the node, and so no snippet (decision 46).
			if req.In == search.InContent && restrictedRole(role) {
				covered, err := store.NoDownloadAppliesIn(ctx, q, p.AccountID, c.NodeID)
				if err != nil {
					return out, storeErr(err)
				}
				if covered {
					continue
				}
			}
			keep, err := restrictedMatch(ctx, q, role, c.NodeID, req.Labels)
			if err != nil {
				return out, err
			}
			if !keep {
				continue
			}
			hit := searchHit{nodeJSON: nodeJSONFor(p, nodes[c.NodeID])}
			hit.Role = role
			if req.In == search.InContent {
				sn, err := req.SnippetOf(ctx, q, c.TextID)
				if err != nil {
					return out, storeErr(err)
				}
				ranges := sn.Ranges
				if ranges == nil {
					ranges = [][2]int{}
				}
				hit.Snippet = &snippetJSON{Text: sn.Text, Ranges: ranges}
			}
			out.Items = append(out.Items, hit)
			if len(out.Items) == limit {
				full = true
				break
			}
		}
		if full {
			break
		}
		if len(cands) < searchBatch {
			exhausted = true
			break
		}
	}
	if !exhausted && len(out.Items) == limit {
		next, err := s.sealSearchCursor(p, req, after)
		if err != nil {
			return out, err
		}
		out.NextCursor = next
	}
	return out, nil
}

// restrictedRole reports whether the no-download rule can bind a caller at role: a viewer or a commenter, as in
// downloadRestricted (restrict.go). Anyone above a commenter may download, so the rule never applies to them.
func restrictedRole(role string) bool {
	return store.RoleRank(role) <= store.RoleRank(store.RoleCommenter)
}

// restrictedMatch applies the restricted label tests. A restricted value counts only for a manager of the node: anyone
// else gets false for the test, the same answer as a node that has no value.
func restrictedMatch(ctx context.Context, q store.Querier, role, nodeID string, tests []search.LabelTest) (bool, error) {
	for _, t := range tests {
		if !t.Restricted {
			continue
		}
		if role != store.RoleManager {
			return false, nil
		}
		ok, err := search.RestrictedHolds(ctx, q, nodeID, t)
		if err != nil {
			return false, storeErr(err)
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// resolveLabelTests checks each label filter against its field and makes the typed test. An unknown field is
// invalid_request. A value is parsed by the field's type: a text prefix, a number or date range, or a user or choice ID.
func resolveLabelTests(ctx context.Context, q store.Querier, accountID string, raw [][2]string) ([]search.LabelTest, error) {
	var out []search.LabelTest
	for _, rv := range raw {
		f, err := store.LabelFieldIn(ctx, q, accountID, rv[0])
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, apierr.New(apierr.InvalidRequest, "label is not a field of this account")
			}
			return nil, storeErr(err)
		}
		t, err := labelTest(f, rv[1])
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// labelTest parses one filter value by the field's type. A text value is a prefix of the stored value, as typed. A number
// or a date is one value or a range "low..high", both ends inclusive. A user or a choice is the ID of one.
func labelTest(f *store.LabelField, v string) (search.LabelTest, error) {
	bad := func(msg string) error { return apierr.New(apierr.InvalidRequest, msg) }
	switch f.Type {
	case "text":
		if v == "" || len(v) > 500 || !utf8.ValidString(v) {
			return search.LabelTest{}, bad("a text label value must be 1 to 500 bytes of UTF-8")
		}
		// The bound is the prefix followed by 0xFF, which UTF-8 never contains (design section 4.5).
		return search.NewLabelTest(f, "prefix", v, v+"\xff")
	case "number":
		lo, hi, err := splitRange(v)
		if err != nil {
			return search.LabelTest{}, bad("a number label value is a number or low..high")
		}
		a, errA := strconv.ParseFloat(lo, 64)
		b, errB := strconv.ParseFloat(hi, 64)
		if errA != nil || errB != nil || math.IsNaN(a) || math.IsNaN(b) || math.IsInf(a, 0) || math.IsInf(b, 0) || a > b {
			return search.LabelTest{}, bad("a number label value is a number or low..high")
		}
		return search.NewLabelTest(f, "range", a, b)
	case "date":
		lo, hi, err := splitRange(v)
		if err != nil || !searchDatePat.MatchString(lo) || !searchDatePat.MatchString(hi) {
			return search.LabelTest{}, bad("a date label value is YYYY-MM-DD or low..high")
		}
		if _, err := time.Parse("2006-01-02", lo); err != nil {
			return search.LabelTest{}, bad("a date label value is YYYY-MM-DD or low..high")
		}
		if _, err := time.Parse("2006-01-02", hi); err != nil {
			return search.LabelTest{}, bad("a date label value is YYYY-MM-DD or low..high")
		}
		if lo > hi {
			return search.LabelTest{}, bad("a date range must run forward")
		}
		return search.NewLabelTest(f, "range", lo, hi)
	case "user":
		if !searchIDPat.MatchString(v) {
			return search.LabelTest{}, bad("a user label value is a user ID")
		}
		return search.NewLabelTest(f, "equal", v)
	case "choice":
		if !searchIDPat.MatchString(v) {
			return search.LabelTest{}, bad("a choice label value is a choice ID")
		}
		return search.NewLabelTest(f, "equal", v)
	}
	return search.LabelTest{}, bad("label field cannot be searched")
}

// splitRange splits "low..high" into its two ends, or returns the one value as both ends.
func splitRange(v string) (string, string, error) {
	if lo, hi, ok := strings.Cut(v, ".."); ok {
		if lo == "" || hi == "" {
			return "", "", apierr.New(apierr.InvalidRequest, "a range has two ends")
		}
		return lo, hi, nil
	}
	if v == "" {
		return "", "", apierr.New(apierr.InvalidRequest, "a value is empty")
	}
	return v, v, nil
}

// parseSearchParams reads and checks the request's parameters. Field IDs and values are checked against their types
// later, when the account's fields are read.
func parseSearchParams(v url.Values) (searchParams, error) {
	ps := searchParams{q: v.Get("q"), limit: searchDefaultLimit, cursor: v.Get("cursor"), mFrom: -1, mTo: -1,
		sMin: -1, sMax: -1}
	switch in := v.Get("in"); in {
	case "", "name":
	case "content":
		ps.in = search.InContent
	default:
		return ps, apierr.New(apierr.InvalidRequest, "in must be name or content")
	}
	switch k := v.Get("kind"); k {
	case "", store.KindFile, store.KindDir, store.KindShortcut:
		ps.kind = k
	default:
		return ps, apierr.New(apierr.InvalidRequest, "kind must be file, dir or shortcut")
	}
	if raw := v.Get("ext"); raw != "" {
		parts := strings.Split(raw, ",")
		if len(parts) > searchMaxFilters {
			return ps, apierr.Newf(apierr.InvalidRequest, "ext may name at most %d extensions", searchMaxFilters)
		}
		for _, e := range parts {
			e = names.SearchForm(strings.TrimPrefix(e, "."))
			if e == "" || utf8.RuneCountInString(e) > 16 || strings.ContainsAny(e, ". ,") {
				return ps, apierr.New(apierr.InvalidRequest, "ext must be a list of extensions without dots")
			}
			ps.exts = append(ps.exts, e)
		}
	}
	var err error
	if ps.mFrom, err = optionalMillis(v, "modified_after"); err != nil {
		return ps, err
	}
	if ps.mTo, err = optionalMillis(v, "modified_before"); err != nil {
		return ps, err
	}
	if ps.sMin, err = optionalMillis(v, "size_min"); err != nil {
		return ps, err
	}
	if ps.sMax, err = optionalMillis(v, "size_max"); err != nil {
		return ps, err
	}
	for _, raw := range v["label"] {
		field, value, ok := strings.Cut(raw, ":")
		if !ok || value == "" || !searchIDPat.MatchString(field) {
			return ps, apierr.New(apierr.InvalidRequest, "label must be field_id:value")
		}
		ps.labels = append(ps.labels, [2]string{field, value})
	}
	if len(ps.labels) > searchMaxLabels {
		return ps, apierr.Newf(apierr.InvalidRequest, "at most %d label filters", searchMaxLabels)
	}
	if sc := v.Get("scope"); sc != "" {
		if !searchNodeID.MatchString(sc) {
			return ps, apierr.New(apierr.InvalidRequest, "scope must be a node ID")
		}
		ps.scope = sc
	}
	if raw := v.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > searchMaxLimit {
			return ps, apierr.Newf(apierr.InvalidRequest, "limit must be between 1 and %d", searchMaxLimit)
		}
		ps.limit = n
	}
	return ps, nil
}

// optionalMillis reads a non-negative integer parameter, or -1 when it is absent.
func optionalMillis(v url.Values, key string) (int64, error) {
	raw := v.Get(key)
	if raw == "" {
		return -1, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, apierr.Newf(apierr.InvalidRequest, "%s must be a whole number, zero or more", key)
	}
	return n, nil
}

// hasFilter reports whether the request names any filter, so that it may run without a query.
func (ps searchParams) hasFilter() bool {
	return ps.kind != "" || len(ps.exts) > 0 || ps.mFrom >= 0 || ps.mTo >= 0 || ps.sMin >= 0 || ps.sMax >= 0 ||
		len(ps.labels) > 0 || ps.scope != ""
}

// Retry bounds of the search job. A failed batch waits searchRetryBase, and each further failure in a row doubles the
// wait, up to searchRetryMax. A success, or a process start, resets it.
const (
	searchRetryBase = time.Second
	searchRetryMax  = 5 * time.Minute
)

// searchBackoff returns the wait after the n-th failure in a row.
func searchBackoff(n int64) time.Duration {
	d := searchRetryBase << min(max(n-1, 0), 20)
	return min(d, searchRetryMax)
}

// runSearchIndex is the handler of the search job: one batch of the backlog, then a yield (search.RunIndexJob). A failed
// batch is retried after a wait, so a backlog cannot sit stuck behind one error; the next queued work brings it forward.
func (s *Server) runSearchIndex(ctx context.Context, _ *store.Job) error {
	err := search.RunIndexJob(ctx, s.store)
	if err == nil || errors.Is(err, jobs.ErrYield) {
		s.searchFailures.Store(0)
		return err
	}
	return jobs.RetryAfter(searchBackoff(s.searchFailures.Add(1)), err)
}
