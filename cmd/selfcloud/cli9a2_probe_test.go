// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// The account commands need an installation admin key, and the account key the other probes use is refused by their
// routes with a 403. The fixture for account rm makes the account it removes through the same command.

func init() {
	adminProbes["account create"] = func(p *fileProbe) []string {
		return []string{"Probe created"}
	}
	adminProbes["account ls"] = func(p *fileProbe) []string {
		return nil
	}
	adminProbes["account show"] = func(p *fileProbe) []string {
		return []string{"--account", p.acc}
	}
	adminProbes["account update"] = func(p *fileProbe) []string {
		return []string{"--account", p.acc, "--name", "Probe account renamed"}
	}
	adminProbes["account rm"] = func(p *fileProbe) []string {
		p.answer = "Probe rm\n"
		return []string{"--account", valueOf(p.t, p.do("account", "create", "Probe rm"), "id=")}
	}
	adminProbes["account usage"] = func(p *fileProbe) []string {
		return []string{"--account", p.acc}
	}
	adminProbes["account audit"] = func(p *fileProbe) []string {
		return []string{"--account", p.acc}
	}
}
