// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// The node label commands (cli11f) run against the real API with the probe account's key. A field is made through
// label field add, outside the recording, and a file through touch.

func init() {
	fileProbes["label field ls"] = func(*fileProbe) []string { return nil }
	for name, fn := range nodeLabelProbes {
		fileProbes[name] = fn
	}
}

// nodeField makes a field for a node label probe and returns its id. A choice field is made with its choices.
func nodeField(p *fileProbe, name, typ string, choices ...string) string {
	args := []string{"label", "field", "add", "--account", p.acc, "--type", typ}
	for _, choice := range choices {
		args = append(args, "--choice", choice)
	}
	return valueOf(p.t, p.do(append(args, name)...), "id=")
}

var nodeLabelProbes = map[string]func(p *fileProbe) []string{
	"label show": func(p *fileProbe) []string {
		nodeField(p, "probe-node-label-show-text", "text")
		nodeField(p, "probe-node-label-show-choice", "choice", "probe-low", "probe-high")
		return []string{p.touch("/probe-label-show.txt")}
	},
	"label set": func(p *fileProbe) []string {
		field := nodeField(p, "probe-node-label-set", "text")
		nodeField(p, "probe-node-label-set-choice", "choice", "probe-low", "probe-high")
		return []string{p.touch("/probe-label-set.txt"), field, "probe value"}
	},
	"label clear": func(p *fileProbe) []string {
		field := nodeField(p, "probe-node-label-clear", "text")
		nodeField(p, "probe-node-label-clear-choice", "choice", "probe-low", "probe-high")
		node := p.touch("/probe-label-clear.txt")
		p.do("label", "set", node, field, "probe value")
		p.answer = field + "\n"
		return []string{node, field}
	},
}
