// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"io"
	"net/url"

	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
)

// Mkdir is `selfcloud mkdir`: one directory at a path, or with --parents the missing directories on the way too.
func Mkdir(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("mkdir")
	var g conn.Global
	g.Register(set)
	var folder string
	var parents bool
	set.StringVar(&folder, "folder", "", "the `id` (fld_...) of the folder the path is in")
	set.BoolVar(&parents, "parents", false, "make the missing directories on the way as well")
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return fail(stderr, g, exitcode.Usagef("mkdir takes one path"))
	}
	p, err := remotePath(pos[0])
	if err != nil {
		return fail(stderr, g, err)
	}
	if p == "/" {
		return fail(stderr, g, exitcode.Usagef("the path names the folder's root, which already exists"))
	}
	fid, err := requireFolder(folder)
	if err != nil {
		return fail(stderr, g, err)
	}
	r, err := jsonRequest("POST", folderRoute(fid, "/directories"), map[string]any{"path": p, "parents": parents})
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

// Touch is `selfcloud touch`: a new, empty file at a path. A name already in use is a conflict (exit 3); the API has no
// way to change a file's time, so touch never changes an existing file.
func Touch(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("touch")
	var g conn.Global
	g.Register(set)
	var folder string
	var parents bool
	set.StringVar(&folder, "folder", "", "the `id` (fld_...) of the folder the path is in")
	set.BoolVar(&parents, "parents", false, "make the missing directories on the way as well")
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return fail(stderr, g, exitcode.Usagef("touch takes one path"))
	}
	p, err := remotePath(pos[0])
	if err != nil {
		return fail(stderr, g, err)
	}
	if p == "/" {
		return fail(stderr, g, exitcode.Usagef("the path names the folder's root, not a file"))
	}
	fid, err := requireFolder(folder)
	if err != nil {
		return fail(stderr, g, err)
	}
	q := url.Values{"path": {p}}
	if parents {
		q.Set("parents", "true")
	}
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	resp, err := send(c, request{method: "POST", path: folderRoute(fid, "/files"), query: q, body: []byte{},
		contentType: "application/octet-stream"})
	if err != nil {
		return fail(stderr, g, err)
	}
	if err := printNode(stdout, resp.Body, g.JSON); err != nil {
		return fail(stderr, g, err)
	}
	return exitcode.OK
}
