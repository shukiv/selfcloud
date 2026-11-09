// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/csv"
	"os"
	"path/filepath"
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

// TestCLIParityCommands keeps the admin table and docs/cli-parity.tsv in step (design section 5.2): every cmd row names a
// command in the table, every table entry has a cmd row, and every row whose command is not "-" names a registered
// command. The check that a remote command calls its route is cli3's, when the first remote command exists.
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
	registered := func(name string) bool {
		_, ok := adminCommandNamed(name)
		return ok
	}
	cmdRows := map[string]bool{}
	for _, rec := range recs[1:] {
		kind, key, command := rec[0], rec[1], rec[2]
		if command != "-" && !registered(command) {
			t.Errorf("docs/cli-parity.tsv: %s %q names the command %q, which no admin command registers", kind, key, command)
		}
		if kind == "cmd" {
			cmdRows[key] = true
			if !registered(key) {
				t.Errorf("docs/cli-parity.tsv: the cmd row %q is not in the admin command table", key)
			}
		}
	}
	for _, c := range adminCommands {
		if !cmdRows[c.name] {
			t.Errorf("admin command %q is in the table but has no cmd row in docs/cli-parity.tsv", c.name)
		}
	}
}
