// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"io"
	"strings"

	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
)

// Mv is `selfcloud mv`: moves a node into a directory (a node id, or a path with --folder), under its own name or under
// --name. Without a destination, --name renames the node in place.
func Mv(args []string, stdout, stderr io.Writer) int {
	return transfer("mv", args, stdout, stderr)
}

// Cp is `selfcloud cp`: copies a node into a directory, under its own name or under --name.
func Cp(args []string, stdout, stderr io.Writer) int {
	return transfer("cp", args, stdout, stderr)
}

// transfer is mv or cp. The destination is a directory: a node id, or a path walked in --folder. A name already there is a
// conflict (exit 3) unless --replace is given, which asks first.
func transfer(verb string, args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet(verb)
	var g conn.Global
	g.Register(set)
	var folder, name string
	var replace, yes bool
	set.StringVar(&folder, "folder", "", "the `id` (fld_...) a destination path is walked from")
	set.StringVar(&name, "name", "", "the name the node has at the destination")
	set.BoolVar(&replace, "replace", false, "replace a node of the same name at the destination (asks first)")
	set.BoolVar(&yes, "yes", false, "do not ask before --replace (needed when standard input is not a terminal)")
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) < 1 || len(pos) > 2 {
		return fail(stderr, g, exitcode.Usagef("%s takes a node id and a destination", verb))
	}
	src, err := nodeArg(pos[0])
	if err != nil {
		return fail(stderr, g, err)
	}
	if name != "" {
		if err := checkName(name); err != nil {
			return fail(stderr, g, err)
		}
	}

	var r request
	if len(pos) == 1 {
		if verb == "cp" || name == "" {
			return fail(stderr, g, exitcode.Usagef("%s needs a destination%s", verb, renameHint(verb)))
		}
		if folder != "" || replace || yes {
			return fail(stderr, g, exitcode.Usagef("--folder, --replace and --yes go with a destination"))
		}
		var err error
		r, err = jsonRequest("PATCH", nodeRoute(src, ""), map[string]string{"name": name})
		if err != nil {
			return fail(stderr, g, err)
		}
	} else {
		body, err := destination(pos[1], folder)
		if err != nil {
			return fail(stderr, g, err)
		}
		if name != "" {
			body["name"] = name
		}
		if replace {
			if err := confirm(yes, "replace", "replace the node already at the destination", stderr); err != nil {
				return fail(stderr, g, err)
			}
			body["on_conflict"] = "replace"
		}
		suffix := "/move"
		if verb == "cp" {
			suffix = "/copy"
		}
		if r, err = jsonRequest("POST", nodeRoute(src, suffix), body); err != nil {
			return fail(stderr, g, err)
		}
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

// renameHint names the other form of the command, for the usage error of a missing destination.
func renameHint(verb string) string {
	if verb == "mv" {
		return ", or --name to rename the node in place"
	}
	return ""
}

// destination is the body that names a directory: its node id, or a path, each with the folder it is in when --folder
// gives one. The API takes a node id only with the folder that holds the node (resolveDestination), so the folder is
// sent with it; without --folder the source's folder is used, as the API does. A path is checked with the API's name
// rules.
func destination(dest, folder string) (map[string]any, error) {
	if !strings.HasPrefix(dest, "/") {
		parentID, err := nodeArg(dest)
		if err != nil {
			return nil, err
		}
		body := map[string]any{"parent": parentID}
		if folder != "" {
			fid, err := folderArg(folder)
			if err != nil {
				return nil, err
			}
			body["folder"] = fid
		}
		return body, nil
	}
	p, err := remotePath(dest)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"path": p}
	if folder != "" {
		fid, err := folderArg(folder)
		if err != nil {
			return nil, err
		}
		body["folder"] = fid
	}
	return body, nil
}
