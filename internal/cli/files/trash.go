// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strconv"

	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/output"
)

// Rm is `selfcloud rm`: moves one or more nodes to the Trash. Asks first (--yes skips the question).
func Rm(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("rm")
	var g conn.Global
	g.Register(set)
	var yes bool
	set.BoolVar(&yes, "yes", false, "do not ask first (needed when standard input is not a terminal)")
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
	// One target is confirmed by its name, read first (design 3.7); several by yes.
	phrase := func() (string, error) {
		if len(ids) != 1 {
			return phraseFor(ids), nil
		}
		return nodeName(c, ids[0])
	}
	if err := confirmWith(yes, phrase, "move the node or nodes to the Trash", stderr); err != nil {
		return fail(stderr, g, err)
	}
	return report(stdout, stderr, g.JSON, runEach(c, ids, func(id string) request {
		return request{method: "DELETE", path: nodeRoute(id, "")}
	}))
}

// nodeName reads a node's name: the phrase a person types back before a removal.
func nodeName(c *conn.Conn, nodeID string) (string, error) {
	resp, err := send(c, request{method: "GET", path: nodeRoute(nodeID, "")})
	if err != nil {
		return "", err
	}
	v, err := decodeNode(resp.Body)
	if err != nil {
		return "", err
	}
	return v.Name, nil
}

// trashedName reads a trashed node's name from the trash listing, the one place a trashed node can be read. A node that
// the listing does not hold is not found, and nothing is purged.
func trashedName(c *conn.Conn, nodeID string) (string, error) {
	cursor := ""
	for {
		q := url.Values{}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		resp, err := send(c, request{method: "GET", path: "/api/v1/trash", query: q})
		if err != nil {
			return "", err
		}
		var page struct {
			Items      []json.RawMessage `json:"items"`
			NextCursor string            `json:"next_cursor"`
		}
		if err := json.Unmarshal(resp.Body, &page); err != nil {
			return "", errors.New("the server's answer is not a list")
		}
		for _, raw := range page.Items {
			v, err := decodeNode(raw)
			if err != nil {
				return "", err
			}
			if v.ID == nodeID {
				return v.Name, nil
			}
		}
		if page.NextCursor == "" || page.NextCursor == cursor {
			return "", exitcode.NotFoundf("the node is not in the Trash")
		}
		cursor = page.NextCursor
	}
}

// TrashLs is `selfcloud trash ls`: the trashed nodes, of every folder or of the one given.
func TrashLs(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("trash ls")
	var g conn.Global
	g.Register(set)
	var folder string
	var all bool
	set.StringVar(&folder, "folder", "", "only the trash of this folder, `id` (fld_...)")
	set.BoolVar(&all, "all", false, "follow the cursor and print every page")
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) != 0 {
		return fail(stderr, g, exitcode.Usagef("trash ls takes no arguments"))
	}
	q := url.Values{}
	if folder != "" {
		fid, err := folderArg(folder)
		if err != nil {
			return fail(stderr, g, err)
		}
		q.Set("folder", fid)
	}
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	items, next, err := listPages(c, request{method: "GET", path: "/api/v1/trash", query: q}, all)
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
		return []string{v.ID, v.Kind, v.TrashedAt, v.Name}
	})
	if err != nil {
		return fail(stderr, g, err)
	}
	if err := output.Table(stdout, []string{"ID", "KIND", "TRASHED", "NAME"}, rows); err != nil {
		return fail(stderr, g, err)
	}
	return exitcode.OK
}

// TrashRestore is `selfcloud trash restore`: puts one or more trashed nodes back where they were.
func TrashRestore(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("trash restore")
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
	return runCommand(g, stdout, stderr, ids, func(id string) request {
		return request{method: "POST", path: nodeRoute(id, "/restore")}
	})
}

// TrashEmpty is `selfcloud trash empty`: purges the trash of the folder given, or of every folder the person may empty.
// The API purges a bounded number at a time and says when more are left, so the command goes on until it is told no more
// exist. Asks first (--yes skips the question).
func TrashEmpty(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("trash empty")
	var g conn.Global
	g.Register(set)
	var folder string
	var yes bool
	set.StringVar(&folder, "folder", "", "only the trash of this folder, `id` (fld_...)")
	set.BoolVar(&yes, "yes", false, "do not ask first (needed when standard input is not a terminal)")
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) != 0 {
		return fail(stderr, g, exitcode.Usagef("trash empty takes no arguments"))
	}
	q := url.Values{}
	if folder != "" {
		fid, err := folderArg(folder)
		if err != nil {
			return fail(stderr, g, err)
		}
		q.Set("folder", fid)
	}
	if err := confirm(yes, "empty", "purge the trash for good; it cannot be restored", stderr); err != nil {
		return fail(stderr, g, err)
	}
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	purged := 0
	for {
		resp, err := send(c, request{method: "POST", path: "/api/v1/trash/empty", query: q})
		if err != nil {
			return fail(stderr, g, err)
		}
		var ans struct {
			Purged int  `json:"purged"`
			More   bool `json:"more"`
		}
		if err := json.Unmarshal(resp.Body, &ans); err != nil {
			return fail(stderr, g, errors.New("the server's answer is not an empty-trash reply"))
		}
		purged += ans.Purged
		if !ans.More {
			break
		}
		if ans.Purged == 0 {
			return fail(stderr, g, errors.New("the server did not purge anything but said more remain; the trash is not empty"))
		}
	}
	if g.JSON {
		if err := output.JSON(stdout, map[string]int{"purged": purged}); err != nil {
			return fail(stderr, g, err)
		}
		return exitcode.OK
	}
	if err := output.KeyValues(stdout, []output.KV{{Key: "purged", Value: strconv.Itoa(purged)}}); err != nil {
		return fail(stderr, g, err)
	}
	return exitcode.OK
}

// TrashRm is `selfcloud trash rm`: purges one or more trashed nodes for good. Asks first (--yes skips the question).
func TrashRm(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("trash rm")
	var g conn.Global
	g.Register(set)
	var yes bool
	set.BoolVar(&yes, "yes", false, "do not ask first (needed when standard input is not a terminal)")
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
	// One target is confirmed by its name, read from the trash listing first (design 3.7); several by yes.
	phrase := func() (string, error) {
		if len(ids) != 1 {
			return phraseFor(ids), nil
		}
		return trashedName(c, ids[0])
	}
	if err := confirmWith(yes, phrase, "purge the node or nodes for good; they cannot be restored", stderr); err != nil {
		return fail(stderr, g, err)
	}
	return report(stdout, stderr, g.JSON, runEach(c, ids, func(id string) request {
		return request{method: "DELETE", path: trashRoute(id)}
	}))
}

// nodeIDs checks that every argument is a node id. It is one check for all of them, before anything is sent.
func nodeIDs(pos []string) ([]string, error) {
	if len(pos) == 0 {
		return nil, exitcode.Usagef("give one or more node ids (nod_...)")
	}
	for _, arg := range pos {
		if _, err := nodeArg(arg); err != nil {
			return nil, err
		}
	}
	return pos, nil
}

// runCommand makes the call for each id, then reports the results and returns the exit code.
func runCommand(g conn.Global, stdout, stderr io.Writer, ids []string, call func(id string) request) int {
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	return report(stdout, stderr, g.JSON, runEach(c, ids, call))
}
