// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/output"
	"github.com/shukiv/selfcloud/internal/cli/profile"
)

// personHint is told to an account key that the star routes refuse. They belong to a signed-in person.
const personHint = "this needs a person's sign-in (selfcloud login); an account key has none"

// Star is `selfcloud star`: stars each node.
func Star(args []string, stdout, stderr io.Writer) int {
	return starUnstar("star", http.MethodPut, args, stdout, stderr)
}

// Unstar is `selfcloud unstar`: removes the star from each node.
func Unstar(args []string, stdout, stderr io.Writer) int {
	return starUnstar("unstar", http.MethodDelete, args, stdout, stderr)
}

// starUnstar sends one call per node and reports each result. An account key gets the person hint after the API's refusal,
// in plain output only.
func starUnstar(name, method string, args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet(name)
	var g conn.Global
	g.Register(set)
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	ids, err := nodeIDs(pos)
	if err != nil {
		return fail(stderr, g, err)
	}
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	outs := runEach(c, ids, func(id string) request {
		return request{method: method, path: nodeRoute(id, "/star")}
	})
	code = report(stdout, stderr, g.JSON, outs)
	if !g.JSON && c.Kind == profile.KindAccount {
		for _, o := range outs {
			if isForbidden(o.err) {
				_, _ = io.WriteString(stderr, "selfcloud: hint: "+personHint+"\n")
				break
			}
		}
	}
	return code
}

// listOptions are the flags of the star and recent lists.
type listOptions struct {
	all    bool
	kind   string
	after  string
	before string
}

// register adds the list flags to set.
func (o *listOptions) register(set *flag.FlagSet) {
	set.BoolVar(&o.all, "all", false, "follow the cursor and print every page")
	set.StringVar(&o.kind, "type", "", "only items of this `kind`")
	set.StringVar(&o.after, "modified-after", "", "only items modified at or after this RFC 3339 `time`")
	set.StringVar(&o.before, "modified-before", "", "only items modified before this RFC 3339 `time`")
}

// query builds the list's query. A time that is not RFC 3339, or that is before 1970, is a usage error, so nothing is sent.
func (o *listOptions) query() (url.Values, error) {
	q := url.Values{}
	if o.kind != "" {
		q.Set("type", o.kind)
	}
	if o.after != "" {
		ms, err := epochMilli(o.after)
		if err != nil {
			return nil, err
		}
		q.Set("modified_after", ms)
	}
	if o.before != "" {
		ms, err := epochMilli(o.before)
		if err != nil {
			return nil, err
		}
		q.Set("modified_before", ms)
	}
	return q, nil
}

// epochMilli turns an RFC 3339 time into milliseconds since the epoch.
func epochMilli(s string) (string, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return "", exitcode.Usagef("a time is written as RFC 3339, such as 2026-11-01T00:00:00Z")
	}
	ms := t.UnixMilli()
	if ms < 0 {
		return "", exitcode.Usagef("a time before 1970 is not supported")
	}
	return strconv.FormatInt(ms, 10), nil
}

// Starred is `selfcloud starred`: the person's starred nodes, newest star first.
func Starred(args []string, stdout, stderr io.Writer) int {
	return listCommand("starred", "/api/v1/starred", []string{"ID", "KIND", "SIZE", "STARRED", "NAME"},
		func(raw json.RawMessage) ([]string, error) {
			var v starredView
			if err := json.Unmarshal(raw, &v); err != nil {
				return nil, errors.New("the server's answer is not a node")
			}
			return []string{v.ID, v.Kind, strconv.FormatInt(v.Size, 10), v.StarredAt, v.Name}, nil
		}, args, stdout, stderr)
}

// Recent is `selfcloud recent`: the nodes the person viewed or edited last, newest first.
func Recent(args []string, stdout, stderr io.Writer) int {
	return listCommand("recent", "/api/v1/recent", []string{"ID", "KIND", "SIZE", "LAST", "NAME"},
		func(raw json.RawMessage) ([]string, error) {
			var v recentView
			if err := json.Unmarshal(raw, &v); err != nil {
				return nil, errors.New("the server's answer is not a node")
			}
			return []string{v.ID, v.Kind, strconv.FormatInt(v.Size, 10), v.LastAt, v.Name}, nil
		}, args, stdout, stderr)
}

// starredView is a node with its star's time. --json prints the API's own document.
type starredView struct {
	nodeView
	StarredAt string `json:"starred_at"`
}

// recentView is a node with the time it was last viewed or edited. --json prints the API's own document.
type recentView struct {
	nodeView
	LastAt string `json:"last_at"`
}

// listCommand reads one page of a list, or every page with --all, and prints it.
func listCommand(name, path string, headers []string, row func(json.RawMessage) ([]string, error),
	args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet(name)
	var g conn.Global
	g.Register(set)
	var o listOptions
	o.register(set)
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) != 0 {
		return fail(stderr, g, exitcode.Usagef("%s takes no arguments", name))
	}
	q, err := o.query()
	if err != nil {
		return fail(stderr, g, err)
	}
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	items, next, err := listPages(c, request{method: http.MethodGet, path: path, query: q}, o.all)
	if err != nil {
		code := fail(stderr, g, err)
		if !g.JSON && c.Kind == profile.KindAccount && isForbidden(err) {
			_, _ = io.WriteString(stderr, "selfcloud: hint: "+personHint+"\n")
		}
		return code
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
	rows := make([][]string, 0, len(items))
	for _, raw := range items {
		r, err := row(raw)
		if err != nil {
			return fail(stderr, g, err)
		}
		rows = append(rows, r)
	}
	if err := output.Table(stdout, headers, rows); err != nil {
		return fail(stderr, g, err)
	}
	return exitcode.OK
}

// isForbidden reports whether err is the API's 403.
func isForbidden(err error) bool {
	var ae *client.APIError
	return errors.As(err, &ae) && ae.Status == http.StatusForbidden
}
