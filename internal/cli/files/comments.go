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
	"os"
	"strconv"
	"strings"

	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/output"
	"github.com/shukiv/selfcloud/internal/cli/profile"
	"github.com/shukiv/selfcloud/internal/id"
)

// commentView is a comment as the commands print it. --json prints these fields only, so a field the API adds later is
// not passed through by an older client.
type commentView struct {
	ID             string           `json:"id"`
	ParentID       string           `json:"parent_id,omitempty"`
	Deleted        bool             `json:"deleted,omitempty"`
	Body           string           `json:"body,omitempty"`
	Mine           bool             `json:"mine,omitempty"`
	AuthorName     string           `json:"author_name,omitempty"`
	AuthorGone     bool             `json:"author_gone,omitempty"`
	CreatedAt      string           `json:"created_at"`
	EditedAt       *string          `json:"edited_at,omitempty"`
	Mentions       []commentMention `json:"mentions"`
	Resolved       bool             `json:"resolved"`
	ResolvedAt     *string          `json:"resolved_at,omitempty"`
	ResolvedByName string           `json:"resolved_by_name,omitempty"`
	ResolvedByGone bool             `json:"resolved_by_gone,omitempty"`
	ReplyCount     int              `json:"reply_count,omitempty"`
	Replies        []commentView    `json:"replies,omitempty"`
	RepliesMore    bool             `json:"replies_more,omitempty"`
}

// commentMention is one person named in a comment.
type commentMention struct {
	Name string `json:"name"`
}

// CommentLs is `selfcloud comment ls`: a node's top-level comments, or one comment's replies.
func CommentLs(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("comment ls")
	var g conn.Global
	g.Register(set)
	var all bool
	var replies string
	set.BoolVar(&all, "all", false, "print every page")
	set.StringVar(&replies, "replies", "", "list the replies to the comment `id` (cmt_...)")
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return fail(stderr, g, exitcode.Usagef("comment ls takes one node id"))
	}
	nodeID, err := nodeArg(pos[0])
	if err != nil {
		return fail(stderr, g, err)
	}
	q := url.Values{}
	if replies != "" {
		if !id.Valid("cmt", replies) {
			return fail(stderr, g, exitcode.Usagef("a comment is named by its cmt_ id"))
		}
		q.Set("parent", replies)
	}
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	items, next, err := listPages(c, request{method: http.MethodGet, path: nodeRoute(nodeID, "/comments"), query: q}, all)
	if err != nil {
		return fail(stderr, g, err)
	}
	if next != "" {
		_, _ = io.WriteString(stderr, "selfcloud: more items exist; --all prints every page\n")
	}
	views := make([]commentView, 0, len(items))
	for _, raw := range items {
		var v commentView
		if err := json.Unmarshal(raw, &v); err != nil {
			return fail(stderr, g, errors.New("the server's answer is not a comment"))
		}
		views = append(views, v)
	}
	if g.JSON {
		if err := output.JSON(stdout, views); err != nil {
			return fail(stderr, g, err)
		}
		return exitcode.OK
	}
	rows := make([][]string, 0, len(views)+1)
	headers := []string{"ID", "AUTHOR", "CREATED", "REPLIES", "RESOLVED", "TEXT"}
	for _, v := range views {
		rows = append(rows, commentCells(v))
	}
	if err := output.Table(stdout, headers, rows); err != nil {
		return fail(stderr, g, err)
	}
	return exitcode.OK
}

// commentCells is one comment's row in the table.
func commentCells(v commentView) []string {
	author := v.AuthorName
	if v.AuthorGone {
		author = "(gone)"
	}
	replies := ""
	if v.ReplyCount > 0 {
		replies = strconv.Itoa(v.ReplyCount)
	}
	if v.RepliesMore {
		replies += "+"
	}
	resolved := ""
	if v.Resolved {
		resolved = "yes"
	}
	text := commentText(v.Body)
	if v.Deleted {
		text = "(deleted)"
	}
	return []string{v.ID, author, v.CreatedAt, replies, resolved, text}
}

// commentText is a comment's first line, cut at 80 characters. A text with more lines or more characters ends in "…".
func commentText(body string) string {
	first, _, more := strings.Cut(body, "\n")
	first = strings.TrimSuffix(first, "\r")
	if r := []rune(first); len(r) > 80 {
		first, more = string(r[:80]), true
	}
	if more {
		return first + "…"
	}
	return first
}

// CommentAdd is `selfcloud comment add`: a comment on a node, or a reply to a top-level comment.
func CommentAdd(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("comment add")
	var g conn.Global
	g.Register(set)
	var text, textFile, replyTo string
	set.StringVar(&text, "text", "", "the comment's `text`")
	set.StringVar(&textFile, "text-file", "", "read the text from `file`, or - for standard input")
	set.StringVar(&replyTo, "reply-to", "", "reply to the top-level comment `id` (cmt_...)")
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return fail(stderr, g, exitcode.Usagef("comment add takes one node id"))
	}
	nodeID, err := nodeArg(pos[0])
	if err != nil {
		return fail(stderr, g, err)
	}
	given := map[string]bool{}
	set.Visit(func(f *flag.Flag) { given[f.Name] = true })
	if given["text"] == given["text-file"] {
		return fail(stderr, g, exitcode.Usagef("give exactly one of --text and --text-file"))
	}
	if replyTo != "" && !id.Valid("cmt", replyTo) {
		return fail(stderr, g, exitcode.Usagef("a comment is named by its cmt_ id"))
	}
	if given["text-file"] {
		body, err := readCommentText(textFile)
		if err != nil {
			return fail(stderr, g, err)
		}
		text = body
	}
	if strings.TrimSpace(text) == "" {
		return fail(stderr, g, exitcode.Usagef("a comment needs some text"))
	}
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	in := map[string]any{"body": text}
	if replyTo != "" {
		in["parent_id"] = replyTo
	}
	req, err := jsonRequest(http.MethodPost, nodeRoute(nodeID, "/comments"), in)
	if err != nil {
		return fail(stderr, g, err)
	}
	resp, err := send(c, req)
	if err != nil {
		code := fail(stderr, g, err)
		if !g.JSON && c.Kind == profile.KindAccount && isForbidden(err) {
			_, _ = io.WriteString(stderr, "selfcloud: hint: "+personHint+"\n")
		}
		return code
	}
	var v commentView
	if err := json.Unmarshal(resp.Body, &v); err != nil {
		return fail(stderr, g, errors.New("the server's answer is not a comment"))
	}
	if g.JSON {
		if err := output.JSON(stdout, v); err != nil {
			return fail(stderr, g, err)
		}
		return exitcode.OK
	}
	err = output.KeyValues(stdout, []output.KV{
		{Key: "id", Value: v.ID},
		{Key: "parent_id", Value: v.ParentID},
		{Key: "created_at", Value: v.CreatedAt},
		{Key: "author_name", Value: v.AuthorName},
		{Key: "body", Value: v.Body},
	})
	if err != nil {
		return fail(stderr, g, err)
	}
	return exitcode.OK
}

// readCommentText reads the text from a file, or from standard input for "-".
func readCommentText(path string) (string, error) {
	if path == "-" {
		b, err := io.ReadAll(Stdin)
		if err != nil {
			return "", fmt.Errorf("read standard input: %w", err)
		}
		return string(b), nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read the text file: %w", err)
	}
	return string(b), nil
}

// activityView is an activity entry as the commands print it. Its details keep only the keys the API documents.
type activityView struct {
	ID      string         `json:"id"`
	Time    string         `json:"time"`
	Kind    string         `json:"kind"`
	Action  string         `json:"action"`
	Actor   activityActor  `json:"actor"`
	Details map[string]any `json:"details,omitempty"`
}

// activityActor is who made the change.
type activityActor struct {
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
	Gone bool   `json:"gone,omitempty"`
	Mine bool   `json:"mine,omitempty"`
}

// activityDetailKeys is the documented detail keys, in the order the table prints them.
var activityDetailKeys = []string{
	"kind", "via", "reply", "role", "subject_kind", "subject_name", "subject_gone", "field_name", "field_gone",
}

// activityDetails keeps the documented detail keys only.
func activityDetails(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for _, k := range activityDetailKeys {
		if v, ok := in[k]; ok {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// activityCells is one activity entry's row in the table.
func activityCells(v activityView) []string {
	actor := v.Actor.Name
	if actor == "" {
		actor = v.Actor.Kind
	}
	var parts []string
	for _, k := range activityDetailKeys {
		if v, ok := v.Details[k]; ok {
			parts = append(parts, k+"="+fmt.Sprint(v))
		}
	}
	return []string{v.Time, actor, v.Action, strings.Join(parts, " ")}
}

// Activity is `selfcloud activity`: a node's audit trail, newest first.
func Activity(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("activity")
	var g conn.Global
	g.Register(set)
	var all bool
	var kind string
	set.BoolVar(&all, "all", false, "print every page")
	set.StringVar(&kind, "kind", "", "keep one `kind`: content, comment, access or label")
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return fail(stderr, g, exitcode.Usagef("activity takes one node id"))
	}
	nodeID, err := nodeArg(pos[0])
	if err != nil {
		return fail(stderr, g, err)
	}
	switch kind {
	case "", "content", "comment", "access", "label":
	default:
		return fail(stderr, g, exitcode.Usagef("--kind is content, comment, access or label"))
	}
	q := url.Values{}
	if kind != "" {
		q.Set("kind", kind)
	}
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	items, next, err := listPages(c, request{method: http.MethodGet, path: nodeRoute(nodeID, "/activity"), query: q}, all)
	if err != nil {
		return fail(stderr, g, err)
	}
	if next != "" {
		_, _ = io.WriteString(stderr, "selfcloud: more items exist; --all prints every page\n")
	}
	views := make([]activityView, 0, len(items))
	for _, raw := range items {
		var v activityView
		if err := json.Unmarshal(raw, &v); err != nil {
			return fail(stderr, g, errors.New("the server's answer is not an activity entry"))
		}
		v.Details = activityDetails(v.Details)
		views = append(views, v)
	}
	if g.JSON {
		if err := output.JSON(stdout, views); err != nil {
			return fail(stderr, g, err)
		}
		return exitcode.OK
	}
	rows := make([][]string, 0, len(views)+1)
	headers := []string{"TIME", "ACTOR", "ACTION", "DETAILS"}
	for _, v := range views {
		rows = append(rows, activityCells(v))
	}
	if err := output.Table(stdout, headers, rows); err != nil {
		return fail(stderr, g, err)
	}
	return exitcode.OK
}
