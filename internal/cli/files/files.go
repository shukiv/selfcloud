// SPDX-License-Identifier: AGPL-3.0-or-later

// Package files is the command-line client's file commands (design-cli.md section 8, slice cli5): ls, stat, mkdir, touch,
// mv, cp, rm, trash ls|restore|empty|rm, shortcut add and unlock. A node is named by its nod_ id. A path starts with a
// slash and is walked by the server from the folder named by --folder (fld_ id). Each name in a path is checked with the
// API's name rules before the first request, so a refused name costs no call. The commands call the routes that
// docs/cli-parity.tsv names for them, and no others.
//
// Destructive commands ask first: at a terminal the person types the phrase the command shows; without one the command
// refuses (exit 2) unless --yes is given. Nothing is sent before the answer.
package files

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/output"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/names"
)

// stdin is what a confirmation reads. Tests replace it.
var stdin io.Reader = os.Stdin

// isTerminal reports whether r is a terminal or another character device, where what is typed is shown. Tests replace it.
var isTerminal = func(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// parse reads a command's arguments. ok is false when the command must return code at once: after -h, or after a usage
// error that has already been reported.
func parse(set *flag.FlagSet, g *conn.Global, args []string, stdout, stderr io.Writer) (pos []string, code int, ok bool) {
	pos, err := conn.Parse(set, args, stdout)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, exitcode.OK, false
		}
		return nil, output.Fail(stderr, g.JSON, err), false
	}
	return pos, exitcode.OK, true
}

// fail reports err and returns its exit code.
func fail(stderr io.Writer, g conn.Global, err error) int {
	return output.Fail(stderr, g.JSON, err)
}

// nodeArg checks a node id.
func nodeArg(arg string) (string, error) {
	if !id.Valid("nod", arg) {
		return "", exitcode.Usagef("a node is named by its nod_ id")
	}
	return arg, nil
}

// folderArg checks a folder id.
func folderArg(arg string) (string, error) {
	if !id.Valid("fld", arg) {
		return "", exitcode.Usagef("a folder is named by its fld_ id")
	}
	return arg, nil
}

// requireFolder is folderArg for a command whose path needs a folder: --folder is required.
func requireFolder(flagValue string) (string, error) {
	if flagValue == "" {
		return "", exitcode.Usagef("a path needs --folder, the id of the folder it is in (fld_...)")
	}
	return folderArg(flagValue)
}

// remotePath checks a path and returns it as the API reads it: a slash, then the names joined by slashes. "/" is the
// folder's root. A path with an empty name, a name "." or "..", a name with a slash, a backslash or a control character,
// or a name the API would refuse for its length or its spaces, is refused here.
func remotePath(arg string) (string, error) {
	if !strings.HasPrefix(arg, "/") {
		return "", exitcode.Usagef("a path starts with /")
	}
	rest := strings.TrimSuffix(arg[1:], "/")
	if rest == "" {
		return "/", nil
	}
	for _, seg := range strings.Split(rest, "/") {
		if _, _, err := names.Clean(seg); err != nil {
			return "", exitcode.Usagef("a name in the path is not valid: %s", err)
		}
	}
	return "/" + rest, nil
}

// checkName checks a name given with --name, with the API's rules.
func checkName(name string) error {
	if _, _, err := names.Clean(name); err != nil {
		return exitcode.Usagef("the name is not valid: %s", err)
	}
	return nil
}

// nodeRoute is the route of a node, with a suffix such as "/move" or "/children".
func nodeRoute(nodeID, suffix string) string { return "/api/v1/nodes/" + nodeID + suffix }

// folderRoute is the route of a folder, with a suffix such as "/files".
func folderRoute(folderID, suffix string) string { return "/api/v1/folders/" + folderID + suffix }

// trashRoute is the route of one trashed node.
func trashRoute(nodeID string) string { return "/api/v1/trash/" + nodeID }

// request is one call to the API.
type request struct {
	method      string
	path        string
	query       url.Values
	body        []byte
	contentType string
}

// jsonRequest builds a call whose body is the JSON encoding of doc.
func jsonRequest(method, path string, doc any) (request, error) {
	b, err := json.Marshal(doc)
	if err != nil {
		return request{}, err
	}
	return request{method: method, path: path, body: b, contentType: "application/json"}, nil
}

// send makes one call. The client repeats only GET and HEAD, and only on a 429 or 503.
func send(c *conn.Conn, r request) (*client.Response, error) {
	return c.Client.Do(context.Background(), client.Request{
		Method:      r.method,
		Path:        r.path,
		Query:       r.query,
		Accept:      "application/json",
		ContentType: r.contentType,
		Body:        r.body,
	})
}

// nodeView is the part of a node document that the plain output prints. --json prints the document as the API wrote it.
type nodeView struct {
	ID         string  `json:"id"`
	FolderID   string  `json:"folder_id"`
	ParentID   *string `json:"parent_id"`
	Name       string  `json:"name"`
	Kind       string  `json:"kind"`
	Size       int64   `json:"size"`
	MimeType   string  `json:"mime_type"`
	ModifiedAt string  `json:"modified_at"`
	Role       string  `json:"role"`
	TrashedAt  string  `json:"trashed_at"`
}

// decodeNode reads one node document.
func decodeNode(body []byte) (nodeView, error) {
	var v nodeView
	if err := json.Unmarshal(body, &v); err != nil {
		return v, errors.New("the server's answer is not a node")
	}
	return v, nil
}

// printNode writes one node: key=value lines, or the API's document under --json.
func printNode(stdout io.Writer, body []byte, asJSON bool) error {
	if asJSON {
		return output.Indented(stdout, body)
	}
	v, err := decodeNode(body)
	if err != nil {
		return err
	}
	parent := ""
	if v.ParentID != nil {
		parent = *v.ParentID
	}
	return output.KeyValues(stdout, []output.KV{
		{Key: "id", Value: v.ID},
		{Key: "folder_id", Value: v.FolderID},
		{Key: "parent_id", Value: parent},
		{Key: "name", Value: v.Name},
		{Key: "kind", Value: v.Kind},
		{Key: "size", Value: strconv.FormatInt(v.Size, 10)},
		{Key: "mime_type", Value: v.MimeType},
		{Key: "modified_at", Value: v.ModifiedAt},
		{Key: "role", Value: v.Role},
	})
}

// listPages reads a list. Without all it reads the first page; with all it follows the cursor the API gives until the
// list ends. next is the cursor of a page that was not followed, or "".
func listPages(c *conn.Conn, r request, all bool) (items []json.RawMessage, next string, err error) {
	items = []json.RawMessage{}
	cursor := ""
	for {
		q := url.Values{}
		for k, v := range r.query {
			q[k] = v
		}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		r.query = q
		resp, err := send(c, r)
		if err != nil {
			return nil, "", err
		}
		var page struct {
			Items      []json.RawMessage `json:"items"`
			NextCursor string            `json:"next_cursor"`
		}
		if err := json.Unmarshal(resp.Body, &page); err != nil {
			return nil, "", errors.New("the server's answer is not a list")
		}
		items = append(items, page.Items...)
		if !all || page.NextCursor == "" {
			return items, page.NextCursor, nil
		}
		if page.NextCursor == cursor {
			return nil, "", errors.New("the server gave the same cursor twice; the listing stopped")
		}
		cursor = page.NextCursor
	}
}

// outcome is one target's result in a command that acts on several targets.
type outcome struct {
	Target string     `json:"target"`
	Status string     `json:"status"`
	Error  *errorBody `json:"error,omitempty"`
	err    error
}

// errorBody is a failed target's error, as the JSON output names it.
type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func doneOutcome(target string) outcome {
	return outcome{Target: target, Status: "done"}
}

func failedOutcome(target string, err error) outcome {
	_, name, msg := exitcode.Of(err)
	return outcome{Target: target, Status: "failed", Error: &errorBody{Code: name, Message: msg}, err: err}
}

// runEach makes the call for each target, in order. Every target is tried, so a failure does not stop the others.
func runEach(c *conn.Conn, targets []string, call func(target string) request) []outcome {
	outs := make([]outcome, 0, len(targets))
	for _, t := range targets {
		if _, err := send(c, call(t)); err != nil {
			outs = append(outs, failedOutcome(t, err))
			continue
		}
		outs = append(outs, doneOutcome(t))
	}
	return outs
}

// report writes the results of a command that acts on several targets, and returns its exit code. One target keeps the
// exit code of its own error. Otherwise the code is 0 when every target is done, 7 when some are and some are not, and 1
// when none is (design-cli 3.11).
func report(stdout, stderr io.Writer, asJSON bool, outs []outcome) int {
	if len(outs) == 1 && outs[0].err != nil {
		return output.Fail(stderr, asJSON, outs[0].err)
	}
	var err error
	if asJSON {
		err = output.JSON(stdout, outs)
	} else {
		rows := make([][]string, 0, len(outs))
		for _, o := range outs {
			detail := ""
			if o.Error != nil {
				detail = o.Error.Message
			}
			rows = append(rows, []string{o.Target, o.Status, detail})
		}
		err = output.Table(stdout, []string{"TARGET", "STATUS", "DETAIL"}, rows)
	}
	if err != nil {
		return output.Fail(stderr, asJSON, err)
	}
	failed := 0
	for _, o := range outs {
		if o.err != nil {
			failed++
		}
	}
	switch {
	case failed == 0:
		return exitcode.OK
	case failed == len(outs):
		return exitcode.Failed
	default:
		return exitcode.Partial
	}
}

// phraseFor is what a person types to confirm: the one target's id, or yes for several.
func phraseFor(targets []string) string {
	if len(targets) == 1 {
		return targets[0]
	}
	return "yes"
}

// confirm asks before a destructive change. With yes it asks nothing. Otherwise standard input must be a terminal, where
// the person types phrase and presses Enter; without a terminal the command refuses (exit 2), so nothing is sent.
func confirm(yes bool, phrase, what string, stderr io.Writer) error {
	return confirmWith(yes, func() (string, error) { return phrase, nil }, what, stderr)
}

// confirmWith is confirm for a phrase that may need a read, such as a node's name. The read happens only at a terminal,
// after the question is known to be needed; an error from it stops the command before anything is sent.
func confirmWith(yes bool, phrase func() (string, error), what string, stderr io.Writer) error {
	if yes {
		return nil
	}
	if !isTerminal(stdin) {
		return exitcode.Usagef("%s; give --yes to confirm when standard input is not a terminal", what)
	}
	p, err := phrase()
	if err != nil {
		return err
	}
	return askPhrase(p, what, stderr)
}

// askPhrase shows the question and reads the answer, which must be phrase exactly.
func askPhrase(phrase, what string, stderr io.Writer) error {
	fmt.Fprintf(stderr, "%s. Type %s and press Enter to go on: ", what, phrase)
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return exitcode.Usagef("the answer could not be read; nothing was changed")
	}
	if strings.TrimRight(line, "\r\n") != phrase {
		return exitcode.Usagef("not confirmed; nothing was changed")
	}
	return nil
}
