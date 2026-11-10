// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"io"
	"strings"

	"github.com/shukiv/selfcloud/internal/cli/apispec"
	"github.com/shukiv/selfcloud/internal/cli/info"
	"github.com/shukiv/selfcloud/internal/cli/profilecmd"
)

// remoteCommand is one command of the remote client. route is the server route it calls, or "" for a command that only
// reads and writes the local profiles file. help is its usage line. docs/cli-parity.tsv holds the row that names it, and
// TestCLIParityCommands checks the two against each other.
type remoteCommand struct {
	name  string
	route string
	help  string
	run   func(args []string, stdout, stderr io.Writer) int
}

// remoteCommands lists the remote commands in the order the usage text shows them.
var remoteCommands = []remoteCommand{
	{name: "info", route: "GET /api/v1/info", help: `  info [flags]                   the server's name, version, limits and features (--json: the whole document)
`, run: info.Run},
	{name: "api spec", route: "GET /api/v1/openapi.yaml", help: `  api spec [--out FILE [--overwrite]] [flags]
                                 the server's OpenAPI document, to standard output or to a new file
`, run: apispec.Run},
	{name: "profile add", help: `  profile add NAME --url ADDRESS [--token-file FILE] [--ca-file FILE]
                                 save a profile; its key comes from --token-file or standard input, never from argv
`, run: profilecmd.Add},
	{name: "profile use", help: `  profile use NAME               make a profile the current one
`, run: profilecmd.Use},
	{name: "profile ls", help: `  profile ls [--json]            list the profiles with their kind; no key is shown
`, run: profilecmd.List},
	{name: "profile rm", help: `  profile rm NAME                remove a profile's local key (the server's key is not revoked)
`, run: profilecmd.Remove},
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
