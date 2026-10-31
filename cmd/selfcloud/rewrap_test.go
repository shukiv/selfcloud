// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestRewrapSecretsNeedsTheCurrentKey: without a current links key the command fails, and it makes no key, since a new
// one would make every stored secret unreadable.
func TestRewrapSecretsNeedsTheCurrentKey(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("SELFCLOUD_DATA_DIR", dataDir)
	code, out, errOut := runCmd(t, "admin", "rewrap-secrets")
	if code != 1 {
		t.Fatalf("rewrap-secrets without a key = %d %q %q; want 1", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "keys", "signing.key")); !os.IsNotExist(err) {
		t.Fatalf("the command made a key file: %v", err)
	}
}

// TestRewrapSecretsPrintsCounts: with the key in place, the command prints counts and nothing that is a secret.
func TestRewrapSecretsPrintsCounts(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("SELFCLOUD_DATA_DIR", dataDir)
	keys := filepath.Join(dataDir, "keys")
	if err := os.MkdirAll(keys, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keys, "signing.key"), bytes.Repeat([]byte{5}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runCmd(t, "admin", "rewrap-secrets")
	want := "endpoints=0 rewrapped=0 unreadable=0 disabled=0 restored=0 expired=0 skipped=0\n"
	if code != 0 || out != want {
		t.Fatalf("rewrap-secrets = %d %q %q; want 0 and %q", code, out, errOut, want)
	}
	if code, _, _ := runCmd(t, "admin", "rewrap-secrets", "extra"); code != 2 {
		t.Errorf("an argument = %d, want 2", code)
	}
}
