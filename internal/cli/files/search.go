// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/output"
	"github.com/shukiv/selfcloud/internal/config"
)

// Search is `selfcloud search`: the nodes whose name matches a query, or whose text does, and whose filters match.
func Search(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("search")
	var g conn.Global
	g.Register(set)
	var in, kind, typ, after, before, sizeMin, sizeMax, scope string
	var all bool
	var limit int
	var exts, labels []string
	set.StringVar(&in, "in", "", "search the `field`: name (default) or content")
	set.StringVar(&kind, "kind", "", "only a `kind`: file, dir or shortcut")
	set.StringVar(&typ, "type", "", "only a file group such as `image`, or folder or shortcut")
	set.Func("ext", "only an extension, such as `pdf`; repeat, or join with commas", func(v string) error {
		exts = append(exts, v)
		return nil
	})
	set.StringVar(&after, "modified-after", "", "only items modified at or after this RFC 3339 `time`")
	set.StringVar(&before, "modified-before", "", "only items modified before this RFC 3339 `time`")
	set.StringVar(&sizeMin, "size-min", "", "only files of at least this `size`, such as 64MiB")
	set.StringVar(&sizeMax, "size-max", "", "only files of at most this `size`, such as 2GiB")
	set.Func("label", "only a label value, as `field_id:value`; repeat up to 4 times", func(v string) error {
		labels = append(labels, v)
		return nil
	})
	set.StringVar(&scope, "scope", "", "only below this directory `node` (nod_...)")
	set.IntVar(&limit, "limit", 0, "how many results one page holds, 1 to 50")
	set.BoolVar(&all, "all", false, "follow the cursor and print every page")
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) > 1 {
		return fail(stderr, g, exitcode.Usagef("search takes at most one query"))
	}
	q := ""
	if len(pos) == 1 {
		q = pos[0]
	}
	switch in {
	case "", "name", "content":
	default:
		return fail(stderr, g, exitcode.Usagef("--in is name or content"))
	}
	if in == "content" && q == "" {
		return fail(stderr, g, exitcode.Usagef("--in content needs a query"))
	}
	switch kind {
	case "", "file", "dir", "shortcut":
	default:
		return fail(stderr, g, exitcode.Usagef("--kind is file, dir or shortcut"))
	}
	if len(labels) > 4 {
		return fail(stderr, g, exitcode.Usagef("search takes at most 4 --label filters"))
	}
	for _, label := range labels {
		if field, value, found := strings.Cut(label, ":"); !found || field == "" || value == "" {
			return fail(stderr, g, exitcode.Usagef("a label filter is written FIELD:VALUE"))
		}
	}
	nExts := 0
	for _, v := range exts {
		nExts += len(strings.Split(v, ","))
	}
	if nExts > 8 {
		return fail(stderr, g, exitcode.Usagef("search takes at most 8 --ext values"))
	}
	limitSet := false
	set.Visit(func(f *flag.Flag) {
		if f.Name == "limit" {
			limitSet = true
		}
	})
	if limitSet && (limit < 1 || limit > 50) {
		return fail(stderr, g, exitcode.Usagef("--limit is between 1 and 50"))
	}
	if scope != "" {
		if _, err := nodeArg(scope); err != nil {
			return fail(stderr, g, err)
		}
	}
	query := url.Values{}
	if q != "" {
		query.Set("q", q)
	}
	if in != "" {
		query.Set("in", in)
	}
	if kind != "" {
		query.Set("kind", kind)
	}
	if typ != "" {
		query.Set("type", typ)
	}
	if len(exts) > 0 {
		query.Set("ext", strings.Join(exts, ","))
	}
	if after != "" {
		ms, err := epochMilli(after)
		if err != nil {
			return fail(stderr, g, err)
		}
		query.Set("modified_after", ms)
	}
	if before != "" {
		ms, err := epochMilli(before)
		if err != nil {
			return fail(stderr, g, err)
		}
		query.Set("modified_before", ms)
	}
	if sizeMin != "" {
		n, err := searchSize("--size-min", sizeMin)
		if err != nil {
			return fail(stderr, g, err)
		}
		query.Set("size_min", n)
	}
	if sizeMax != "" {
		n, err := searchSize("--size-max", sizeMax)
		if err != nil {
			return fail(stderr, g, err)
		}
		query.Set("size_max", n)
	}
	for _, label := range labels {
		query.Add("label", label)
	}
	if scope != "" {
		query.Set("scope", scope)
	}
	if limitSet {
		query.Set("limit", strconv.Itoa(limit))
	}
	if q == "" && !searchHasFilter(query) {
		return fail(stderr, g, exitcode.Usagef("search needs a query or a filter"))
	}
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	items, next, err := listPages(c, request{method: http.MethodGet, path: "/api/v1/search", query: query}, all)
	if err != nil {
		return fail(stderr, g, err)
	}
	if next != "" {
		_, _ = io.WriteString(stderr, "selfcloud: more items exist; --all prints every page\n")
	}
	if g.JSON {
		if err := output.JSON(stdout, items); err != nil {
			return fail(stderr, g, err)
		}
		return exitcode.OK
	}
	content := in == "content"
	headers := []string{"ID", "KIND", "SIZE", "MODIFIED", "PLACE", "NAME"}
	if content {
		headers = append(headers, "MATCH")
	}
	rows := make([][]string, 0, len(items))
	for _, raw := range items {
		var v searchView
		if err := json.Unmarshal(raw, &v); err != nil {
			return fail(stderr, g, errors.New("the server's answer is not a search result"))
		}
		row := []string{v.ID, v.Kind, strconv.FormatInt(v.Size, 10), v.ModifiedAt, v.Place, v.Name}
		if content {
			row = append(row, searchMatch(v.Snippet))
		}
		rows = append(rows, row)
	}
	if err := output.Table(stdout, headers, rows); err != nil {
		return fail(stderr, g, err)
	}
	return exitcode.OK
}

// searchHasFilter reports whether the query names a filter, so that a search may run without a query.
func searchHasFilter(q url.Values) bool {
	for _, key := range []string{"kind", "type", "ext", "modified_after", "modified_before", "size_min", "size_max",
		"label", "scope"} {
		if q.Get(key) != "" {
			return true
		}
	}
	return false
}

// searchSize reads a size with the server's units, and returns its number of bytes.
func searchSize(flagName, value string) (string, error) {
	var b config.ByteSize
	if err := b.UnmarshalText([]byte(value)); err != nil {
		return "", exitcode.Usagef("%s is not a size, such as 64MiB", flagName)
	}
	return strconv.FormatInt(int64(b), 10), nil
}

// searchView is the part of a search result that the plain output prints.
type searchView struct {
	nodeView
	Place   string         `json:"place"`
	Snippet *searchSnippet `json:"snippet"`
}

// searchSnippet is a content match's text and ranges.
type searchSnippet struct {
	Text   string   `json:"text"`
	Ranges [][2]int `json:"ranges"`
}

// searchMatch makes a snippet one safe line, at most 80 characters long.
func searchMatch(s *searchSnippet) string {
	if s == nil {
		return ""
	}
	line := strings.Join(strings.Fields(output.Clean(s.Text)), " ")
	r := []rune(line)
	if len(r) > 80 {
		line = string(r[:80])
	}
	return line
}

// tagPollCap is the longest wait between two asks for tags.
const tagPollCap = 5 * time.Second

// tagMaxAsks is how many times one tags read is asked for before its pending state is shown.
const tagMaxAsks = 6

// defaultTagPollWait is the wait before the try-th ask for tags, when they are still pending. A server's Retry-After is
// honoured up to five seconds; without one the wait is two seconds.
func defaultTagPollWait(_ int, retry time.Duration) time.Duration {
	if retry <= 0 {
		retry = 2 * time.Second
	}
	return min(retry, tagPollCap)
}

// tagPollWait is the wait a command makes. Tests replace it.
var tagPollWait = defaultTagPollWait

// tagView is one node's tags, as the plain and JSON outputs show them.
type tagView struct {
	State      string `json:"state"`
	Title      string `json:"title,omitempty"`
	Artist     string `json:"artist,omitempty"`
	Album      string `json:"album,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

// TagLs is `selfcloud tag ls`: the audio tags of one or more nodes.
func TagLs(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("tag ls")
	var g conn.Global
	g.Register(set)
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	ids, err := tagNodeIDs(pos)
	if err != nil {
		return fail(stderr, g, err)
	}
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	views := map[string]tagView{}
	if len(ids) == 1 {
		v, shown, err := tagAskOne(c, ids[0])
		if err != nil {
			return fail(stderr, g, err)
		}
		if shown {
			views[ids[0]] = v
		}
	} else {
		views, err = tagAskMany(c, ids)
		if err != nil {
			return fail(stderr, g, err)
		}
	}
	for _, nodeID := range ids {
		if _, shown := views[nodeID]; !shown {
			fmt.Fprintf(stderr, "selfcloud: no tags for %s (not an audio file, or not reachable)\n", nodeID)
		}
	}
	if g.JSON {
		if err := output.JSON(stdout, views); err != nil {
			return fail(stderr, g, err)
		}
	} else {
		rows := make([][]string, 0, len(ids))
		for _, nodeID := range ids {
			if v, shown := views[nodeID]; shown {
				rows = append(rows, []string{nodeID, v.State, v.Title, v.Artist, v.Album, tagDuration(v.DurationMS)})
			}
		}
		if err := output.Table(stdout, []string{"NODE", "STATE", "TITLE", "ARTIST", "ALBUM", "DURATION"}, rows); err != nil {
			return fail(stderr, g, err)
		}
	}
	switch {
	case len(views) == 0:
		return exitcode.NotFound
	case len(views) < len(ids):
		return exitcode.Partial
	default:
		return exitcode.OK
	}
}

// tagNodeIDs checks each node id, drops an exact repeat, and keeps the first spelling. A node id that is not valid is a
// usage error, and so are no id and more than 100 distinct ids.
func tagNodeIDs(args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, exitcode.Usagef("tag ls takes at least one node id")
	}
	seen := make(map[string]bool, len(args))
	ids := make([]string, 0, len(args))
	for _, arg := range args {
		nodeID, err := nodeArg(arg)
		if err != nil {
			return nil, err
		}
		if seen[nodeID] {
			continue
		}
		seen[nodeID] = true
		ids = append(ids, nodeID)
	}
	if len(ids) > 100 {
		return nil, exitcode.Usagef("tag ls takes at most 100 node ids")
	}
	return ids, nil
}

// tagAskOne reads one node's tags, asking again while the server answers pending. shown is false when the node has no
// tags answer at all (a 404).
func tagAskOne(c *conn.Conn, nodeID string) (v tagView, shown bool, err error) {
	for ask := 1; ask <= tagMaxAsks; ask++ {
		resp, err := send(c, request{method: http.MethodGet, path: nodeRoute(nodeID, "/tags")})
		if err != nil {
			var ae *client.APIError
			if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
				return tagView{}, false, nil
			}
			return tagView{}, false, err
		}
		if err := json.Unmarshal(resp.Body, &v); err != nil {
			return tagView{}, false, errors.New("the server's answer is not a tags answer")
		}
		if v.State != "pending" || ask == tagMaxAsks {
			return v, true, nil
		}
		time.Sleep(tagPollWait(ask, tagRetryAfter(resp.Header)))
	}
	return v, true, nil
}

// tagAskMany asks for the tags of several nodes in one batch, then asks again only for those still pending.
func tagAskMany(c *conn.Conn, ids []string) (map[string]tagView, error) {
	views := map[string]tagView{}
	pending := append([]string(nil), ids...)
	for ask := 1; ask <= tagMaxAsks && len(pending) > 0; ask++ {
		if ask > 1 {
			time.Sleep(tagPollWait(ask, 2*time.Second))
		}
		q := url.Values{"ids": {strings.Join(pending, ",")}}
		resp, err := send(c, request{method: http.MethodGet, path: "/api/v1/media/tags", query: q})
		if err != nil {
			return nil, err
		}
		var doc struct {
			Items map[string]tagView `json:"items"`
		}
		if err := json.Unmarshal(resp.Body, &doc); err != nil {
			return nil, errors.New("the server's answer is not a tags answer")
		}
		next := make([]string, 0, len(pending))
		for _, nodeID := range pending {
			v, ok := doc.Items[nodeID]
			if !ok {
				continue
			}
			views[nodeID] = v
			if v.State == "pending" && ask < tagMaxAsks {
				next = append(next, nodeID)
			}
		}
		pending = next
	}
	return views, nil
}

// tagRetryAfter reads a server's Retry-After in seconds. An absent or unusable value is zero.
func tagRetryAfter(h http.Header) time.Duration {
	secs, err := strconv.Atoi(strings.TrimSpace(h.Get("Retry-After")))
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

// tagDuration writes a duration as minutes and zero-padded seconds. An absent or zero duration is blank.
func tagDuration(ms int64) string {
	if ms <= 0 {
		return ""
	}
	secs := ms / 1000
	return fmt.Sprintf("%d:%02d", secs/60, secs%60)
}
