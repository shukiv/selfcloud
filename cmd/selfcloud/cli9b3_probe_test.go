// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// The storage commands run against the real API with the probe account's key. The account key sees the users' totals
// and the team spaces' files, so every probe reaches its route with exit 0.

// init registers the probes with the recording check.
func init() {
	for name, fn := range storageProbes {
		fileProbes[name] = fn
	}
}

var storageProbes = map[string]func(p *fileProbe) []string{
	"storage policy show": func(p *fileProbe) []string { return []string{"--account", p.acc} },
	"storage policy set": func(p *fileProbe) []string {
		return []string{"--account", p.acc, "--user-quota", "1GiB"}
	},
	"storage usage":   func(p *fileProbe) []string { return []string{"--account", p.acc} },
	"storage largest": func(p *fileProbe) []string { return []string{"--account", p.acc} },
}
