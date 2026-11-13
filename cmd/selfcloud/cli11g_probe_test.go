// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// The document and template commands run against the real API with the probe account's key. Office is off in the test
// configuration, as it is by default, so the template listing is refused (404, exit 4); the command still calls its
// route, which is what the check is about. A document of type txt needs no office.

func init() {
	probeExit["template ls"] = 4
	fileProbes["doc new"] = func(p *fileProbe) []string {
		return []string{p.mkdir("/probe-doc"), "--type", "txt", "--name", "probe.txt"}
	}
	fileProbes["template ls"] = func(p *fileProbe) []string {
		return []string{"--kind", "document"}
	}
	fileProbes["template folder show"] = func(p *fileProbe) []string {
		return []string{"--account", p.acc}
	}
	fileProbes["template folder set"] = func(p *fileProbe) []string {
		return []string{p.mkdir("/probe-templates"), "--account", p.acc}
	}
}
