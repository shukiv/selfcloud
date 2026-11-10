// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// parityTableRoot returns the nearest directory above this package that holds go.mod.
func parityTableRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test package")
		}
		dir = parent
	}
}

// TestCLIParityCommands keeps the command tables and docs/cli-parity.tsv in step (design section 5.2). Every row whose
// command is not "-" names a registered command. Each admin command has a cmd row. Each remote command that reaches a
// route has a route row that names it and that route; each remote command that reaches no route has a cmd row. A route
// row names a remote command only when that command calls the row's route. The recording check that a command makes
// exactly that call is TestRemoteCommandsCallTheirRoute.
func TestCLIParityCommands(t *testing.T) {
	t.Parallel()
	f, err := os.Open(filepath.Join(parityTableRoot(t), "docs", "cli-parity.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.Comma = '\t'
	r.LazyQuotes = false
	r.FieldsPerRecord = 5
	recs, err := r.ReadAll()
	if err != nil {
		t.Fatalf("docs/cli-parity.tsv: %v", err)
	}
	adminNamed := func(name string) bool {
		_, ok := adminCommandNamed(name)
		return ok
	}
	remoteNamed := func(name string) (remoteCommand, bool) {
		for _, c := range remoteCommands {
			if c.name == name {
				return c, true
			}
		}
		return remoteCommand{}, false
	}
	registered := func(name string) bool {
		_, ok := remoteNamed(name)
		return ok || adminNamed(name)
	}
	cmdRows := map[string]bool{}
	routeRows := map[string]string{} // route key -> the command the row names
	for _, rec := range recs[1:] {
		kind, key, command := rec[0], rec[1], rec[2]
		if command != "-" && !registered(command) {
			t.Errorf("docs/cli-parity.tsv: %s %q names the command %q, which no command registers", kind, key, command)
		}
		switch kind {
		case "cmd":
			cmdRows[key] = true
			if !registered(key) {
				t.Errorf("docs/cli-parity.tsv: the cmd row %q is not a registered command", key)
			}
		case "route":
			routeRows[key] = command
			if command == "-" {
				continue
			}
			if rc, ok := remoteNamed(command); !ok || !slices.Contains(rc.routes(), key) {
				t.Errorf("docs/cli-parity.tsv: the route row %q names %q, which does not call that route", key, command)
			}
		}
	}
	for _, c := range adminCommands {
		if !cmdRows[c.name] {
			t.Errorf("admin command %q is in the table but has no cmd row in docs/cli-parity.tsv", c.name)
		}
	}
	// A declared read is a route another command owns, which this command calls for a check of its own (design-cli 3.7:
	// rm reads a node's name before it asks). It must have a row naming that other command, and it must not be one of this
	// command's own routes, which the rows above already cover.
	for _, c := range remoteCommands {
		for _, read := range c.reads {
			owner, ok := routeRows[read]
			switch {
			case !ok:
				t.Errorf("remote command %q declares the read %s, which has no route row in docs/cli-parity.tsv", c.name, read)
			case owner == c.name:
				t.Errorf("remote command %q declares the read %s, which is its own route; a read must belong to another command", c.name, read)
			}
			if slices.Contains(c.routes(), read) {
				t.Errorf("remote command %q declares the read %s, which is already one of its routes", c.name, read)
			}
		}
	}
	for _, c := range remoteCommands {
		if c.route == "" {
			if !cmdRows[c.name] {
				t.Errorf("remote command %q is in the table but has no cmd row in docs/cli-parity.tsv", c.name)
			}
			continue
		}
		for _, route := range c.routes() {
			if got, ok := routeRows[route]; !ok || got != c.name {
				t.Errorf("remote command %q calls %s, but docs/cli-parity.tsv has no route row naming it (row names %q)",
					c.name, route, got)
			}
		}
	}
}
