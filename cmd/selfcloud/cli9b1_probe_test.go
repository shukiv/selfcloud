// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// The people commands run against the real API with the probe account's key. A person is made in the store for the
// commands that need one, outside the recording.

func init() {
	for name, fn := range userProbes {
		fileProbes[name] = fn
	}
}

var userProbes = map[string]func(p *fileProbe) []string{
	"user add": func(p *fileProbe) []string {
		return []string{"--account", p.acc, "--verification", "none", "probe-add@example.test"}
	},
	"user ls": func(p *fileProbe) []string { return []string{"--account", p.acc} },
	"user show": func(p *fileProbe) []string {
		return []string{"--account", p.acc, p.member("probe-show@example.test")}
	},
	"user update": func(p *fileProbe) []string {
		return []string{"--account", p.acc, p.member("probe-update@example.test"), "--name", "Probe Updated"}
	},
	"user rm": func(p *fileProbe) []string {
		user := p.member("probe-rm@example.test")
		p.answer = user + "\n"
		return []string{"--account", p.acc, user}
	},
}
