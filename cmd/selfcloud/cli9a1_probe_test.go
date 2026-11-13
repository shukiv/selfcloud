// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// The account key commands need an installation admin key; the quota-warning commands take the probe's account key.

func init() {
	for name, fn := range map[string]func(p *fileProbe) []string{
		"account key create": func(p *fileProbe) []string {
			return []string{"--account", p.acc, "probe"}
		},
		"account key ls": func(p *fileProbe) []string {
			return []string{"--account", p.acc}
		},
		"account key revoke": func(p *fileProbe) []string {
			restore := p.useAdminKey()
			out := p.do("account", "key", "create", "--account", p.acc, "probe")
			restore()
			key := valueOf(p.t, out, "id=")
			p.answer = key + "\n"
			return []string{"--account", p.acc, key}
		},
	} {
		adminProbes[name] = fn
	}
	for name, fn := range map[string]func(p *fileProbe) []string{
		"account quota-warning show": func(p *fileProbe) []string {
			return []string{"--account", p.acc}
		},
		"account quota-warning set": func(p *fileProbe) []string {
			return []string{"--account", p.acc, "80"}
		},
		"account quota-warning clear": func(p *fileProbe) []string {
			return []string{"--account", p.acc}
		},
	} {
		fileProbes[name] = fn
	}
}
