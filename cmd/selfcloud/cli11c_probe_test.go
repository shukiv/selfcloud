// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// The comments and activity commands run against the real API with the probe account's key. Both routes accept an
// account key, and the account is a manager, so the probes expect them to succeed. The node is made outside the
// recording by the file commands.

// cli11cProbes are the commands of this slice that need a file fixture.
var cli11cProbes = map[string]func(p *fileProbe) []string{
	"comment ls": func(p *fileProbe) []string {
		return []string{p.touch("/comment-ls.txt")}
	},
	"activity": func(p *fileProbe) []string {
		return []string{p.touch("/activity.txt")}
	},
}

func init() {
	for name, fn := range cli11cProbes {
		fileProbes[name] = fn
	}
}
