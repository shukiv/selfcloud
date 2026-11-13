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
	"strings"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/output"
)

// docTypes are the document types a new document may have.
var docTypes = map[string]bool{
	"docx": true, "xlsx": true, "pptx": true, "odt": true, "ods": true, "odp": true, "odg": true, "txt": true,
}

// templateKinds are the kinds a template listing may take.
var templateKinds = map[string]bool{"document": true, "spreadsheet": true, "presentation": true}

// docOfficeHint is told after a 404 to a document type that needs the office service.
const docOfficeHint = "office documents may be off on this server; --type txt needs no office"

// DocNew is `selfcloud doc new`: a new document in a directory, from a type or from a template.
func DocNew(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("doc new")
	var g conn.Global
	g.Register(set)
	var typ, template, name string
	set.StringVar(&typ, "type", "", "the `type` of the new document: docx, xlsx, pptx, odt, ods, odp, odg or txt")
	set.StringVar(&template, "template", "", "a `template`: builtin:NAME, or the nod_ id of a template file")
	set.StringVar(&name, "name", "", "the new document's `name`")
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return fail(stderr, g, exitcode.Usagef("doc new takes one directory: its nod_ id"))
	}
	dir, err := nodeArg(pos[0])
	if err != nil {
		return fail(stderr, g, err)
	}
	given := map[string]bool{}
	set.Visit(func(f *flag.Flag) { given[f.Name] = true })
	if !given["type"] && !given["template"] {
		return fail(stderr, g, exitcode.Usagef("give --type or --template, one of the two"))
	}
	if given["type"] && given["template"] {
		return fail(stderr, g, exitcode.Usagef("give --type or --template, not both"))
	}
	body := map[string]any{}
	if given["type"] {
		if !docTypes[typ] {
			return fail(stderr, g, exitcode.Usagef("a document type is docx, xlsx, pptx, odt, ods, odp, odg or txt"))
		}
		body["type"] = typ
	}
	if given["template"] {
		if rest, ok := strings.CutPrefix(template, "builtin:"); ok {
			if rest == "" {
				return fail(stderr, g, exitcode.Usagef("a built-in template is builtin:NAME"))
			}
		} else if _, err := nodeArg(template); err != nil {
			return fail(stderr, g, exitcode.Usagef("a template is builtin:NAME, or the nod_ id of a template file"))
		}
		body["template"] = template
	}
	if given["name"] {
		if err := checkName(name); err != nil {
			return fail(stderr, g, err)
		}
		body["name"] = name
	}
	r, err := jsonRequest(http.MethodPost, nodeRoute(dir, "/documents"), body)
	if err != nil {
		return fail(stderr, g, err)
	}
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	resp, err := send(c, r)
	if err != nil {
		code := fail(stderr, g, err)
		if !g.JSON && typ != "txt" && docIsNotFound(err) {
			_, _ = io.WriteString(stderr, "selfcloud: hint: "+docOfficeHint+"\n")
		}
		return code
	}
	if err := printNode(stdout, resp.Body, g.JSON); err != nil {
		return fail(stderr, g, err)
	}
	return exitcode.OK
}

// docIsNotFound reports whether err is the API's 404.
func docIsNotFound(err error) bool {
	var ae *client.APIError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}

// templateItem is one template as the commands print it. The view names every field the API's item has and no other, so
// a field the server adds is never printed. --json prints these, not the API's document.
type templateItem struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Source       string `json:"source"`
	NodeID       string `json:"node_id,omitempty"`
	Category     string `json:"category,omitempty"`
	HasThumbnail *bool  `json:"has_thumbnail,omitempty"`
}

// templatePage is one page of GET /api/v1/templates.
type templatePage struct {
	Items      []json.RawMessage `json:"items"`
	NextCursor string            `json:"next_cursor"`
}

// TemplateLs is `selfcloud template ls`: the built-in and account templates of one kind.
func TemplateLs(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("template ls")
	var g conn.Global
	g.Register(set)
	var kind string
	var limit int
	var all bool
	set.StringVar(&kind, "kind", "", "the template `kind`: document, spreadsheet or presentation")
	set.IntVar(&limit, "limit", 0, "the number of templates in a page (1 to 100)")
	set.BoolVar(&all, "all", false, "follow the cursor and print every page")
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) != 0 {
		return fail(stderr, g, exitcode.Usagef("template ls takes no arguments"))
	}
	if kind == "" {
		return fail(stderr, g, exitcode.Usagef("give --kind: document, spreadsheet or presentation"))
	}
	if !templateKinds[kind] {
		return fail(stderr, g, exitcode.Usagef("a template kind is document, spreadsheet or presentation"))
	}
	limitSet := false
	set.Visit(func(f *flag.Flag) {
		if f.Name == "limit" {
			limitSet = true
		}
	})
	if limitSet && (limit < 1 || limit > 100) {
		return fail(stderr, g, exitcode.Usagef("--limit is a number from 1 to 100"))
	}
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	items, next, err := templateList(c, kind, limit, limitSet, all)
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
	rows := make([][]string, 0, len(items))
	for _, it := range items {
		rows = append(rows, []string{it.ID, it.Kind, it.Source, it.Category, it.Name})
	}
	if err := output.Table(stdout, []string{"ID", "KIND", "SOURCE", "CATEGORY", "NAME"}, rows); err != nil {
		return fail(stderr, g, err)
	}
	return exitcode.OK
}

// templateList reads one page of templates, or every page with --all. The route's cursor is named after, not cursor.
// next is the cursor of a page that was not followed, or "".
func templateList(c *conn.Conn, kind string, limit int, limitSet, all bool) (items []templateItem, next string, err error) {
	items = []templateItem{}
	cursor := ""
	seen := map[string]bool{}
	for {
		q := url.Values{"kind": {kind}}
		if limitSet {
			q.Set("limit", strconv.Itoa(limit))
		}
		if cursor != "" {
			q.Set("after", cursor)
		}
		resp, err := send(c, request{method: http.MethodGet, path: "/api/v1/templates", query: q})
		if err != nil {
			return nil, "", err
		}
		var p templatePage
		if err := json.Unmarshal(resp.Body, &p); err != nil {
			return nil, "", errors.New("the server's answer is not a template list")
		}
		for _, raw := range p.Items {
			var it templateItem
			if err := json.Unmarshal(raw, &it); err != nil {
				return nil, "", errors.New("the server's answer holds an item that is not a template")
			}
			items = append(items, it)
		}
		if p.NextCursor == "" {
			return items, "", nil
		}
		if !all {
			return items, p.NextCursor, nil
		}
		if p.NextCursor == cursor || seen[p.NextCursor] {
			return nil, "", errors.New("the server repeated a cursor; stopping")
		}
		seen[p.NextCursor] = true
		cursor = p.NextCursor
	}
}
