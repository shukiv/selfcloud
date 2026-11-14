// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The audit export needs an account's own key or an installation admin key, as the audit list does, so its probe runs
// with the admin key beside account audit's.

func init() {
	adminProbes["account audit export"] = func(p *fileProbe) []string {
		return []string{"--account", p.acc}
	}
}

// TestAuditExportOnTheRealServer: after a few audited actions, the export has the header row and those actions, with
// the BOM and CRLF the API writes, on standard output and in a file of its own.
func TestAuditExportOnTheRealServer(t *testing.T) {
	isolateRemote(t)
	p := newFileProbe(t)
	file := p.touch("/u1c-export.txt")
	dir := p.mkdir("/u1c-export-dir")
	const header = "time,actor_kind,actor_id,actor_name,action,target,outcome,ip,user_agent,details"
	var out, errOut bytes.Buffer
	if code := run([]string{"account", "audit", "export", "--account", p.acc, "--url", p.url}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut.String())
	}
	body := out.String()
	if !strings.HasPrefix(body, "\ufeff") {
		t.Fatalf("the export has no BOM: %.40q", body)
	}
	if lines := strings.Split(strings.TrimPrefix(body, "\ufeff"), "\r\n"); lines[0] != header {
		t.Fatalf("the header row is %q", lines[0])
	}
	for _, want := range []string{"node.create", file, dir, "audit.exported"} {
		if !strings.Contains(body, want) {
			t.Errorf("the export lacks %q:\n%s", want, body)
		}
	}
	dest := filepath.Join(t.TempDir(), "audit.csv")
	out.Reset()
	errOut.Reset()
	if code := run([]string{"account", "audit", "export", "--account", p.acc, "-o", dest, "--url", p.url}, &out, &errOut); code != 0 {
		t.Fatalf("-o: exit %d, stderr %q", code, errOut.String())
	}
	if out.Len() != 0 {
		t.Fatalf("-o printed to standard output: %q", out.String())
	}
	saved, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(saved), "\ufeff"+header+"\r\n") || !strings.Contains(string(saved), "node.create") {
		t.Fatalf("the file is not the export: %.80q", saved)
	}
	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("the file's mode is %v, want 0600", fi.Mode().Perm())
	}
}
