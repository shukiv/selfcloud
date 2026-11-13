// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// adminProbes gives, for each command that needs an installation admin key, the arguments that make it call its route on
// the real server. They run with the probe's admin key (useAdminKey).
var adminProbes = map[string]func(p *fileProbe) []string{}
