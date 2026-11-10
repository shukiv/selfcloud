// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"encoding/json"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/output"
)

// connect resolves the connection. Every file command needs a credential, so a missing one is refused before any call.
func connect(g conn.Global, stderr io.Writer) (*conn.Conn, error) {
	return conn.Connect(g, stderr, true)
}

// Ls is `selfcloud ls`: the children of a path in a folder, or of a directory node.
func Ls(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("ls")
	var g conn.Global
	g.Register(set)
	var folder string
	var all bool
	set.StringVar(&folder, "folder", "", "the `id` (fld_...) of the folder a path is walked from")
	set.BoolVar(&all, "all", false, "follow the cursor and print every page")
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) > 1 {
		return fail(stderr, g, exitcode.Usagef("ls takes one target: a path, or a directory's node id"))
	}
	var r request
	if len(pos) == 1 && !strings.HasPrefix(pos[0], "/") {
		if folder != "" {
			return fail(stderr, g, exitcode.Usagef("--folder goes with a path; a node id names its own folder"))
		}
		nodeID, err := nodeArg(pos[0])
		if err != nil {
			return fail(stderr, g, err)
		}
		r = request{method: "GET", path: nodeRoute(nodeID, "/children")}
	} else {
		fid, err := requireFolder(folder)
		if err != nil {
			return fail(stderr, g, err)
		}
		p := "/"
		if len(pos) == 1 {
			if p, err = remotePath(pos[0]); err != nil {
				return fail(stderr, g, err)
			}
		}
		r = request{method: "GET", path: folderRoute(fid, "/children"), query: url.Values{"path": {p}}}
	}
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	items, next, err := listPages(c, r, all)
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
	rows, err := nodeRows(items, func(v nodeView) []string {
		return []string{v.ID, v.Kind, strconv.FormatInt(v.Size, 10), v.ModifiedAt, v.Name}
	})
	if err != nil {
		return fail(stderr, g, err)
	}
	if err := output.Table(stdout, []string{"ID", "KIND", "SIZE", "MODIFIED", "NAME"}, rows); err != nil {
		return fail(stderr, g, err)
	}
	return exitcode.OK
}

// nodeRows turns each node in a list into a row.
func nodeRows(items []json.RawMessage, row func(nodeView) []string) ([][]string, error) {
	rows := make([][]string, 0, len(items))
	for _, raw := range items {
		v, err := decodeNode(raw)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row(v))
	}
	return rows, nil
}

// Stat is `selfcloud stat`: one node, by its id.
func Stat(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("stat")
	var g conn.Global
	g.Register(set)
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return fail(stderr, g, exitcode.Usagef("stat takes one node id"))
	}
	nodeID, err := nodeArg(pos[0])
	if err != nil {
		return fail(stderr, g, err)
	}
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	resp, err := send(c, request{method: "GET", path: nodeRoute(nodeID, "")})
	if err != nil {
		return fail(stderr, g, err)
	}
	if err := printNode(stdout, resp.Body, g.JSON); err != nil {
		return fail(stderr, g, err)
	}
	return exitcode.OK
}
