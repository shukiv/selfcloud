// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// adminProbes are the probes of the commands that need the installation admin key. TestRemoteCommandsCallTheirRoute
// runs them with that key, as it runs settingsProbes. Each command's probe file adds its entries in an init.
var adminProbes = map[string]func(p *fileProbe) []string{}
