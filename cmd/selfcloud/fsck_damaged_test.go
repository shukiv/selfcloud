// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A damaged database is a finding: the report says integrity failed and the exit status is 1, not 2. Exit 2 is for
// a check that could not run. Mutant: return the unreadable error from fsck.Run; the exit is 2 again.
func TestFsckDamagedDatabaseExitsOneWithAFinding(t *testing.T) {
	dir, cfg := fsckDataDir(t)
	garbage := bytes.Repeat([]byte{0x55}, 8192)
	if err := os.WriteFile(filepath.Join(dir, "selfcloud.db"), garbage, 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runCmd(t, "fsck", "-config", cfg, "-json")
	if code != 1 {
		t.Fatalf("fsck on a damaged database = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	var doc struct {
		Integrity string `json:"integrity"`
		Findings  []struct {
			Kind string `json:"kind"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(bytes.TrimSpace([]byte(out)), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, out)
	}
	if doc.Integrity != "failed" {
		t.Fatalf("integrity = %q, want failed", doc.Integrity)
	}
	found := false
	for _, f := range doc.Findings {
		found = found || f.Kind == "sqlite_integrity"
	}
	if !found {
		t.Fatalf("no sqlite_integrity finding: %+v", doc.Findings)
	}
}
