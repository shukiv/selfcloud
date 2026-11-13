// SPDX-License-Identifier: AGPL-3.0-or-later

package ops

// The import commands. An import belongs to a signed-in person: every import route answers an account key or an admin key
// with 403 "imports are for a signed-in user", so these commands work with a device token. Starting, resuming and
// cancelling an import are refused to a device token (decision 125), so the browser does those. Only the archive switch is
// an account's, and it needs an account key or an admin key.

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/shukiv/selfcloud/internal/cli/cmdkit"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/cli/output"
)

const importsRoute = "/api/v1/imports"

// importView is an import as the commands print it.
type importView struct {
	ID            string  `json:"id"`
	Source        string  `json:"source"`
	State         string  `json:"state"`
	PauseReason   string  `json:"pause_reason,omitempty"`
	ErrorCode     string  `json:"error_code,omitempty"`
	Folder        string  `json:"folder"`
	Parent        string  `json:"parent"`
	OnConflict    string  `json:"on_conflict"`
	Format        string  `json:"format,omitempty"`
	Entries       int64   `json:"entries"`
	DeclaredBytes int64   `json:"declared_bytes"`
	Files         int64   `json:"files"`
	Bytes         int64   `json:"bytes"`
	Skipped       int64   `json:"skipped"`
	Job           string  `json:"job"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
	FinishedAt    *string `json:"finished_at"`
}

func i64(n int64) string { return strconv.FormatInt(n, 10) }

func (v importView) pairs() []output.KV {
	return []output.KV{
		{Key: "id", Value: v.ID},
		{Key: "source", Value: v.Source},
		{Key: "state", Value: v.State},
		{Key: "pause_reason", Value: v.PauseReason},
		{Key: "error_code", Value: v.ErrorCode},
		{Key: "folder", Value: v.Folder},
		{Key: "parent", Value: v.Parent},
		{Key: "on_conflict", Value: v.OnConflict},
		{Key: "format", Value: v.Format},
		{Key: "entries", Value: i64(v.Entries)},
		{Key: "declared_bytes", Value: i64(v.DeclaredBytes)},
		{Key: "files", Value: i64(v.Files)},
		{Key: "bytes", Value: i64(v.Bytes)},
		{Key: "skipped", Value: i64(v.Skipped)},
		{Key: "job", Value: v.Job},
		{Key: "created_at", Value: v.CreatedAt},
		{Key: "updated_at", Value: v.UpdatedAt},
		{Key: "finished_at", Value: cmdkit.Str(v.FinishedAt)},
	}
}

// itemView is one item of an import.
type itemView struct {
	Seq       int64   `json:"seq"`
	Name      string  `json:"name"`
	Kind      string  `json:"kind"`
	State     string  `json:"state"`
	Node      *string `json:"node"`
	Bytes     int64   `json:"bytes"`
	ErrorCode *string `json:"error_code"`
}

// userCommand starts a command whose routes are a signed-in person's.
func userCommand(name string, withAccount bool, stdout, stderr io.Writer) *command {
	c := begin(name, withAccount, stdout, stderr)
	c.UserRoutes = true
	return c
}

// printImport reads an import answer and prints it.
func (c *command) printImport(body []byte) error {
	var v importView
	if err := cmdkit.DecodeAs(body, &v, "an import"); err != nil {
		return err
	}
	if v.ID == "" {
		return cmdkit.ErrNotA("an import")
	}
	if c.G.JSON {
		return output.JSON(c.Stdout, v)
	}
	return output.KeyValues(c.Stdout, v.pairs())
}

// ImportSources is `selfcloud import sources`: the import sources, and whether the account has each one on.
func ImportSources(args []string, stdout, stderr io.Writer) int {
	c := userCommand("import sources", false, stdout, stderr)
	pos, code, ok := c.Parse(args)
	if !ok {
		return code
	}
	if err := cmdkit.NoArgs("import sources", pos); err != nil {
		return c.fail(err)
	}
	cn, err := c.Connect()
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodGet, importsRoute+"/sources", nil, nil)
	if err != nil {
		return c.fail(err)
	}
	type source struct {
		Source  string `json:"source"`
		Enabled bool   `json:"enabled"`
	}
	var v struct {
		Sources []source `json:"sources"`
	}
	if err = cmdkit.DecodeAs(resp.Body, &v, "a list of import sources"); err == nil {
		if v.Sources == nil {
			v.Sources = []source{}
		}
		if c.G.JSON {
			err = output.JSON(c.Stdout, v)
		} else {
			err = cmdkit.PrintList(c.Command, v.Sources, false, []string{"SOURCE", "ENABLED"}, func(s source) []string {
				return []string{s.Source, strconv.FormatBool(s.Enabled)}
			})
		}
	}
	if err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// oneImport parses a command that takes one import id.
func oneImport(c *command, name string, args []string) (imp string, code int, ok bool) {
	pos, code, ok := c.Parse(args)
	if !ok {
		return "", code, false
	}
	if len(pos) != 1 {
		return "", c.fail(exitcode.Usagef("%s takes one import id", name)), false
	}
	imp, err := cmdkit.IDArg("imp", "a import", pos[0])
	if err != nil {
		return "", c.fail(err), false
	}
	return imp, 0, true
}

// ImportShow is `selfcloud import show IMPORT`: its state and counts.
func ImportShow(args []string, stdout, stderr io.Writer) int {
	c := userCommand("import show", false, stdout, stderr)
	imp, code, ok := oneImport(c, "import show", args)
	if !ok {
		return code
	}
	cn, err := c.Connect()
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodGet, importsRoute+"/"+imp, nil, nil)
	if err == nil {
		err = c.printImport(resp.Body)
	}
	if err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// ImportItems is `selfcloud import items IMPORT [--after SEQ] [--limit N] [--all]`. The server pages by the sequence number
// of the last item, not by a cursor.
func ImportItems(args []string, stdout, stderr io.Writer) int {
	c := userCommand("import items", false, stdout, stderr)
	var all bool
	var after int64
	var limit int
	c.Set.BoolVar(&all, "all", false, "follow the pages and print every item")
	c.Set.Int64Var(&after, "after", 0, "start after the item with this sequence `number`")
	c.Set.IntVar(&limit, "limit", 0, "the most items in a page, 1 to 500 (default: the server's, 100)")
	imp, code, ok := oneImport(c, "import items", args)
	if !ok {
		return code
	}
	if after < 0 {
		return c.fail(exitcode.Usagef("--after is zero or more"))
	}
	if cmdkit.FlagGiven(c.Set, "limit") && (limit < 1 || limit > 500) {
		return c.fail(exitcode.Usagef("--limit is from 1 to 500"))
	}
	cn, err := c.Connect()
	if err != nil {
		return c.fail(err)
	}
	var items []itemView
	var next *int64
	for {
		q := url.Values{}
		if after > 0 {
			q.Set("after", i64(after))
		}
		if limit > 0 {
			q.Set("limit", strconv.Itoa(limit))
		}
		resp, err := files.Send(cn, http.MethodGet, importsRoute+"/"+imp+"/items", q, nil)
		var pg struct {
			Items []itemView `json:"items"`
			Next  *int64     `json:"next"`
		}
		if err == nil {
			err = cmdkit.DecodeAs(resp.Body, &pg, "a page of import items")
		}
		if err != nil {
			return c.fail(err)
		}
		items, next = append(items, pg.Items...), pg.Next
		if !all || next == nil {
			break
		}
		if *next <= after {
			return c.fail(errors.New("the server's next page does not move on; stopping"))
		}
		after = *next
	}
	asked := limit
	if asked < 1 {
		asked = defaultItemsPage
	}
	if err := printItems(c, items, next, asked); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// defaultItemsPage is how many items a page holds when --limit is not given.
const defaultItemsPage = 100

// printItems prints one page of items, or all of them. The API sets next to the last item's number on every page that has
// an item, so next alone does not say that more exist: only a page as long as the one asked for may have more, and then the
// note says where to read on.
func printItems(c *command, items []itemView, next *int64, asked int) error {
	if items == nil {
		items = []itemView{}
	}
	if next != nil && len(items) >= asked {
		_, _ = io.WriteString(c.Stderr, "selfcloud: more items may exist; --after "+i64(*next)+" reads on, or --all prints every page\n")
	}
	if c.G.JSON {
		return output.JSON(c.Stdout, map[string]any{"items": items, "next": next})
	}
	return cmdkit.PrintList(c.Command, items, false, []string{"SEQ", "NAME", "KIND", "STATE", "BYTES", "NODE", "ERROR"},
		func(it itemView) []string {
			return []string{i64(it.Seq), it.Name, it.Kind, it.State, i64(it.Bytes), cmdkit.Str(it.Node), cmdkit.Str(it.ErrorCode)}
		})
}

// ImportPause is `selfcloud import pause IMPORT`: stops a queued or running import at its next checkpoint. A device token
// may do it, since a pause only stops work. Starting, resuming and cancelling an import are the browser's.
func ImportPause(args []string, stdout, stderr io.Writer) int {
	c := userCommand("import pause", false, stdout, stderr)
	imp, code, ok := oneImport(c, "import pause", args)
	if !ok {
		return code
	}
	cn, err := c.Connect()
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodPost, importsRoute+"/"+imp+"/pause", nil, nil)
	if err == nil {
		err = c.printImport(resp.Body)
	}
	if err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// ImportArchiveSet is `selfcloud import archive set --account ACC on|off`: turns the archive source on or off for an
// account. It needs an account key or an admin key.
func ImportArchiveSet(args []string, stdout, stderr io.Writer) int {
	c := begin("import archive set", true, stdout, stderr)
	pos, code, ok := c.Parse(args)
	if !ok {
		return code
	}
	if len(pos) != 1 || (pos[0] != "on" && pos[0] != "off") {
		return c.fail(exitcode.Usagef("import archive set takes on or off"))
	}
	acc, err := c.AccountID()
	if err != nil {
		return c.fail(err)
	}
	cn, err := c.Connect()
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodPut, "/api/v1/accounts/"+acc+"/imports/archive", nil, map[string]bool{"enabled": pos[0] == "on"})
	var v struct {
		Source  string `json:"source"`
		Enabled bool   `json:"enabled"`
	}
	if err == nil {
		err = cmdkit.DecodeAs(resp.Body, &v, "an import source")
	}
	if err == nil {
		if c.G.JSON {
			err = output.JSON(c.Stdout, v)
		} else {
			err = output.KeyValues(c.Stdout, []output.KV{
				{Key: "source", Value: v.Source}, {Key: "enabled", Value: strconv.FormatBool(v.Enabled)}})
		}
	}
	if err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}
