// SPDX-License-Identifier: AGPL-3.0-or-later

package sharing

import (
	"io"

	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/files"
)

// shareSwitch turns one of a node's sharing switches on or off. The second argument must be exactly on or off, and the
// node must be named by a nod_ id, both before any request. The body names that one field only, so a switch never
// renames the node.
func shareSwitch(name, field string, args []string, stdout, stderr io.Writer) int {
	r, code, ok := begin(name, args, stdout, stderr, nil)
	if !ok {
		return code
	}
	if len(r.pos) != 2 {
		return r.fail(exitcode.Usagef("%s takes a node (nod_...) and on or off", name))
	}
	nodeID, err := idArg("nod", "a node", r.pos[0])
	if err != nil {
		return r.fail(err)
	}
	var on bool
	switch r.pos[1] {
	case "on":
		on = true
	case "off":
		on = false
	default:
		return r.fail(exitcode.Usagef("%s takes on or off", name))
	}
	c, err := r.connect()
	if err != nil {
		return r.fail(err)
	}
	resp, err := files.Send(c, "PATCH", nodePath(nodeID, ""), nil, map[string]any{field: on})
	if err != nil {
		return r.fail(err)
	}
	if err := r.printDoc(resp.Body, "id", "no_download", "editors_can_share"); err != nil {
		return r.fail(err)
	}
	return exitcode.OK
}

// ShareNoDownload is `selfcloud share no-download`: turns a node's no-download rule on or off. It needs editor.
func ShareNoDownload(args []string, stdout, stderr io.Writer) int {
	return shareSwitch("share no-download", "no_download", args, stdout, stderr)
}

// ShareEditorsCanShare is `selfcloud share editors-can-share`: lets a node's editors share it, or stops them. It needs
// manager.
func ShareEditorsCanShare(args []string, stdout, stderr io.Writer) int {
	return shareSwitch("share editors-can-share", "editors_can_share", args, stdout, stderr)
}
