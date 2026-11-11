// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"io"
	"strings"

	"github.com/shukiv/selfcloud/internal/cli/apispec"
	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/cli/info"
	"github.com/shukiv/selfcloud/internal/cli/profilecmd"
	"github.com/shukiv/selfcloud/internal/cli/signin"
)

// remoteCommand is one command of the remote client. route is the server route it calls, or "" for a command that only
// reads and writes the local profiles file. help is its usage line. docs/cli-parity.tsv holds the row that names it, and
// TestCLIParityCommands checks the two against each other.
type remoteCommand struct {
	name  string
	route string
	// more lists the routes a command calls after route, in order. login calls start and then poll (decision 125).
	more []string
	// notProbed says why TestRemoteCommandsCallTheirRoute does not run the command against the server: the command needs a
	// credential that the test cannot make (a person's approval in the browser, for a device token). Its own package records
	// its calls instead.
	notProbed string
	// reads lists the routes a command calls for a need of its own, which another command owns (design-cli 3.7: rm
	// reads a node's name before it asks; put ends an upload it could not record, which upload cancel owns).
	// docs/cli-parity.tsv has a row for each, naming that other command, and the probe checks that a command's calls
	// stay within its routes and these reads.
	reads []string
	help  string
	run   func(args []string, stdout, stderr io.Writer) int
}

// routes returns every route the command calls, route first.
func (c remoteCommand) routes() []string {
	if c.route == "" {
		return nil
	}
	return append([]string{c.route}, c.more...)
}

// remoteCommands lists the remote commands in the order the usage text shows them.
var remoteCommands = []remoteCommand{
	{name: "info", route: "GET /api/v1/info", help: `  info [flags]                   the server's name, version, limits and features (--json: the whole document)
`, run: info.Run},
	{name: "api spec", route: "GET /api/v1/openapi.yaml", help: `  api spec [--out FILE [--overwrite]] [flags]
                                 the server's OpenAPI document, to standard output or to a new file
`, run: apispec.Run},
	{name: "login", route: "POST /api/v1/auth/cli/start", more: []string{"POST /api/v1/auth/cli/poll"},
		notProbed: "needs a person to approve the sign-in in the browser; internal/cli/signin records its calls",
		help: `  login --url ADDRESS [--profile NAME]
                                 sign in at this terminal: prints a code and a page to open, waits for the approval, and
                                 saves the device token in a profile (the password is never typed here)
`, run: signin.Login},
	{name: "logout", route: "POST /api/v1/auth/cli/revoke",
		notProbed: "needs a device token; internal/cli/signin records its calls",
		help: `  logout [--profile NAME]        end this terminal's sign-in on the server and remove its profile
`, run: signin.Logout},
	{name: "whoami", route: "GET /api/v1/folders",
		notProbed: "needs a device token; internal/cli/signin records its calls",
		help: `  whoami [--profile NAME]        the profile, its address, its kind and how many folders its token reaches
`, run: signin.Whoami},
	{name: "profile add", help: `  profile add NAME --url ADDRESS [--token-file FILE] [--ca-file FILE]
                                 save a profile; its key comes from --token-file or standard input, never from argv
`, run: profilecmd.Add},
	{name: "profile use", help: `  profile use NAME               make a profile the current one
`, run: profilecmd.Use},
	{name: "profile ls", help: `  profile ls [--json]            list the profiles with their kind; no key is shown
`, run: profilecmd.List},
	{name: "profile rm", help: `  profile rm NAME                remove a profile's local key (the server's key is not revoked)
`, run: profilecmd.Remove},
	{name: "ls", route: "GET /api/v1/folders/{fld}/children", more: []string{"GET /api/v1/nodes/{nod}/children"},
		help: `  ls [--folder FOLDER] [--all] [PATH | NODE]
                                 the children of a path in a folder (--folder names it), or of a directory node
`, run: files.Ls},
	{name: "stat", route: "GET /api/v1/nodes/{nod}",
		help: `  stat NODE [--json]             one node: its id, folder, parent, name, kind, size and modified time
`, run: files.Stat},
	{name: "mkdir", route: "POST /api/v1/folders/{fld}/directories",
		help: `  mkdir --folder FOLDER [--parents] PATH
                                 make a directory at a path in a folder (--parents makes the missing ones too)
`, run: files.Mkdir},
	{name: "touch", route: "POST /api/v1/folders/{fld}/files",
		help: `  touch --folder FOLDER [--parents] PATH
                                 make a new, empty file at a path; a name in use is a conflict
`, run: files.Touch},
	{name: "mv", route: "POST /api/v1/nodes/{nod}/move", more: []string{"PATCH /api/v1/nodes/{nod}"},
		help: `  mv NODE DEST [--folder FOLDER] [--name NAME] [--replace [--yes]]
  mv NODE --name NAME
                                 move a node into a directory (DEST is a node id, or a path in --folder), or rename it
`, run: files.Mv},
	{name: "cp", route: "POST /api/v1/nodes/{nod}/copy",
		help: `  cp NODE DEST [--folder FOLDER] [--name NAME] [--replace [--yes]]
                                 copy a node into a directory, as mv does for the destination
`, run: files.Cp},
	{name: "rm", route: "DELETE /api/v1/nodes/{nod}", reads: []string{"GET /api/v1/nodes/{nod}"},
		help: `  rm NODE... [--yes]             move one or more nodes to the Trash (asks first)
`, run: files.Rm},
	{name: "trash ls", route: "GET /api/v1/trash",
		help: `  trash ls [--folder FOLDER] [--all]
                                 the trashed nodes, of every folder or of one
`, run: files.TrashLs},
	{name: "trash restore", route: "POST /api/v1/nodes/{nod}/restore",
		help: `  trash restore NODE...          put one or more trashed nodes back where they were
`, run: files.TrashRestore},
	{name: "trash empty", route: "POST /api/v1/trash/empty",
		help: `  trash empty [--folder FOLDER] [--yes]
                                 purge the trash for good, of every folder or of one (asks first)
`, run: files.TrashEmpty},
	{name: "trash rm", route: "DELETE /api/v1/trash/{nod}", reads: []string{"GET /api/v1/trash"},
		help: `  trash rm NODE... [--yes]       purge one or more trashed nodes for good (asks first)
`, run: files.TrashRm},
	{name: "shortcut add", route: "POST /api/v1/nodes/{nod}/shortcuts",
		help: `  shortcut add DIR TARGET [--name NAME]
                                 a shortcut to TARGET, in the directory DIR (both node ids)
`, run: files.ShortcutAdd},
	{name: "unlock", route: "POST /api/v1/nodes/{nod}/unlock",
		help: `  unlock NODE                    break the editing lock on a file
`, run: files.Unlock},
	// put reads the piece limit from info and the new file from its node, and ends an upload it could not record.
	{name: "put", route: "POST /api/v1/uploads",
		more:  []string{"PATCH /api/v1/uploads/{upl}", "HEAD /api/v1/uploads/{upl}", "POST /api/v1/uploads/{upl}/commit"},
		reads: []string{"GET /api/v1/info", "GET /api/v1/nodes/{nod}", "DELETE /api/v1/uploads/{upl}"},
		help: `  put LOCAL [--folder FOLDER] [DIR] [--name NAME] [--parents] [--replace | --rename] [--piece-size SIZE]
                                 send a file in checked pieces that resume after a cut (DIR: a path or a node id)
  put --resume UPLOAD [--name NAME] [--replace | --rename]
                                 go on with an upload this client began
`, run: files.Put},
	// get reads the file's node, and a path's directory to find it; a share link is opened and renewed with no sign-in.
	{name: "get", route: "GET /api/v1/nodes/{nod}/content",
		more:  []string{"POST /api/v1/share/open", "POST /api/v1/share/renew"},
		reads: []string{"GET /api/v1/nodes/{nod}", "GET /api/v1/folders/{fld}/children"},
		help: `  get NODE | --folder FOLDER PATH | SHARE-LINK [DEST] [--overwrite] [--password-stdin] [--piece-size SIZE]
                                 read a file in ranges into DEST.part, resume after a cut, check it, then name it
`, run: files.Get},
	{name: "upload ls", help: `  upload ls [--json]             the uploads this client began and can resume
`, run: files.UploadLs},
	{name: "upload cancel", route: "DELETE /api/v1/uploads/{upl}",
		help: `  upload cancel UPLOAD           end an upload; the server drops the bytes it holds
`, run: files.UploadCancel},
}

// remoteUsage returns the remote commands' lines of the usage text, built from remoteCommands.
func remoteUsage() string {
	var b strings.Builder
	for _, c := range remoteCommands {
		b.WriteString(c.help)
	}
	return b.String()
}

// remote runs the remote command whose words start args. It reports false when no command matches.
func remote(args []string, stdout, stderr io.Writer) (int, bool) {
	for _, c := range remoteCommands {
		words := strings.Fields(c.name)
		if len(args) < len(words) {
			continue
		}
		match := true
		for i, w := range words {
			if args[i] != w {
				match = false
				break
			}
		}
		if match {
			return c.run(args[len(words):], stdout, stderr), true
		}
	}
	return 0, false
}
