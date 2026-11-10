// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"errors"
	"io"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/profile"
)

// ShortcutAdd is `selfcloud shortcut add DIR TARGET [--name NAME]`: a shortcut to TARGET, in the directory DIR. Both are
// node ids; the shortcut takes the target's name unless --name gives another.
func ShortcutAdd(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("shortcut add")
	var g conn.Global
	g.Register(set)
	var name string
	set.StringVar(&name, "name", "", "the shortcut's name (default: the target's name)")
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) != 2 {
		return fail(stderr, g, exitcode.Usagef("shortcut add takes a directory's node id and a target's node id"))
	}
	dir, err := nodeArg(pos[0])
	if err != nil {
		return fail(stderr, g, err)
	}
	target, err := nodeArg(pos[1])
	if err != nil {
		return fail(stderr, g, err)
	}
	body := map[string]any{"target": target}
	if name != "" {
		if err := checkName(name); err != nil {
			return fail(stderr, g, err)
		}
		body["name"] = name
	}
	r, err := jsonRequest("POST", nodeRoute(dir, "/shortcuts"), body)
	if err != nil {
		return fail(stderr, g, err)
	}
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	resp, err := send(c, r)
	if err != nil {
		return fail(stderr, g, err)
	}
	if err := printNode(stdout, resp.Body, g.JSON); err != nil {
		return fail(stderr, g, err)
	}
	return exitcode.OK
}

// Unlock is `selfcloud unlock NODE`: breaks the editing lock on a file. The API refuses a device sign-in this, with a
// message and no reason; the command adds the one fact a terminal needs, that the browser can do it.
func Unlock(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("unlock")
	var g conn.Global
	g.Register(set)
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return fail(stderr, g, exitcode.Usagef("unlock takes one node id"))
	}
	nodeID, err := nodeArg(pos[0])
	if err != nil {
		return fail(stderr, g, err)
	}
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	if _, err := send(c, request{method: "POST", path: nodeRoute(nodeID, "/unlock")}); err != nil {
		var apiErr *client.APIError
		// The reason-less lock refusal is the one a terminal cannot get past; a refusal that names its reason is the API's own.
		if c.Kind == profile.KindDevice && errors.As(err, &apiErr) && apiErr.Status == 403 && apiErr.Reason == "" {
			err = &exitcode.Error{Exit: exitcode.Refused, Name: "forbidden",
				Message: apiErr.Message + "; a terminal's sign-in cannot break a lock, so use the browser"}
		}
		return report(stdout, stderr, g.JSON, []outcome{failedOutcome(nodeID, err)})
	}
	return report(stdout, stderr, g.JSON, []outcome{doneOutcome(nodeID)})
}
