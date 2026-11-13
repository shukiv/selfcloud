// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"slices"
	"testing"

	"github.com/shukiv/selfcloud/internal/cli/exitcode"
)

func init() {
	fileProbes["search"] = func(p *fileProbe) []string {
		p.touch("/cli11e-search.txt")
		return []string{"cli11e-search"}
	}
	fileProbes["people find"] = func(p *fileProbe) []string { return nil }
	fileProbes["tag ls"] = func(p *fileProbe) []string {
		return []string{p.touch("/cli11e-tags.txt")}
	}
	probeExit["tag ls"] = exitcode.NotFound
}

func TestTagLsBatchRoute(t *testing.T) {
	isolateRemote(t)
	p := newFileProbe(t)
	a := p.touch("/cli11e-tags-a.txt")
	b := p.touch("/cli11e-tags-b.txt")
	p.reset()
	var out, errOut bytes.Buffer
	code, matched := remote([]string{"tag", "ls", a, b, "--url", p.recURL}, &out, &errOut)
	if !matched || code != exitcode.NotFound {
		t.Fatalf("exit %d, matched %v, stderr %q", code, matched, errOut.String())
	}
	var routes []string
	for _, call := range p.recorded() {
		routes = append(routes, routeOf(call))
	}
	if !slices.Contains(routes, "GET /api/v1/media/tags") {
		t.Fatalf("calls %v; want the batch route", routes)
	}
	var batch remoteCommand
	for _, c := range remoteCommands {
		if c.name == "tag ls" {
			batch = c
		}
	}
	if got := undeclaredCalls(batch, routes); len(got) != 0 {
		t.Fatalf("undeclared calls %v", got)
	}
}
