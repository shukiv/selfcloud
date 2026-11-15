// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import "testing"

// The embed site commands run against the real API with the probe account's key.

func init() {
	for name, fn := range embedProbes {
		fileProbes[name] = fn
	}
}

var embedProbes = map[string]func(p *fileProbe) []string{
	"embed show": func(p *fileProbe) []string {
		return []string{"--account", p.acc}
	},
	"embed set": func(p *fileProbe) []string {
		// Clear the list the account already has (empty): the command still calls PUT, and the probes that
		// follow this one keep seeing no embed origins.
		p.answer = p.acc + "\n"
		return []string{"--account", p.acc, "--clear"}
	},
}

// TestEmbedRoundTripOnTheRealServer: set two origins, show them normalised by the server, clear with --yes, show empty.
func TestEmbedRoundTripOnTheRealServer(t *testing.T) {
	isolateRemote(t)
	p := newFileProbe(t)
	out := p.do("embed", "set", "--account", p.acc, "https://Host.Example:443", "http://localhost:8080")
	want := "http://localhost:8080\nhttps://host.example\n"
	if out != want {
		t.Fatalf("set printed %q, want the normalised list %q", out, want)
	}
	if out = p.do("embed", "show", "--account", p.acc); out != want {
		t.Fatalf("show printed %q, want %q", out, want)
	}
	if out = p.do("embed", "set", "--account", p.acc, "--clear", "--yes"); out != "no sites may embed this account\n" {
		t.Fatalf("clear printed %q", out)
	}
	if out = p.do("embed", "show", "--account", p.acc); out != "no sites may embed this account\n" {
		t.Fatalf("show after the clear printed %q", out)
	}
}
