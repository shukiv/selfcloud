// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/store"
)

// TestAdminCommandsUseTheStoredSettings: an admin command reads the settings table at start as the server does, so the
// link create-admin makes lives as long as users.verification_ttl in the table says, not the default 72 hours.
func TestAdminCommandsUseTheStoredSettings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SELFCLOUD_DATA_DIR", dir)
	ctx := context.Background()
	st, err := store.Open(ctx, store.Options{Path: filepath.Join(dir, "selfcloud.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Write(ctx, func(tx *store.Tx) error {
		return tx.SetSetting(ctx, "users.verification_ttl", "1h0m0s")
	}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	before := time.Now()
	code, out, errOut := runCmd(t, "admin", "create-admin", "-email", "ann@example.com")
	if code != 0 {
		t.Fatalf("create-admin = %d %q %q", code, out, errOut)
	}
	_, rest, ok := strings.Cut(errOut, "The link expires at ")
	if !ok {
		t.Fatalf("no expiry in %q", errOut)
	}
	at, err := time.Parse(time.RFC3339, strings.Fields(rest)[0][:len("2006-01-02T15:04:05Z")])
	if err != nil {
		t.Fatalf("expiry in %q: %v", errOut, err)
	}
	if life := at.Sub(before); life < 59*time.Minute || life > 61*time.Minute {
		t.Errorf("the link lives %v, want the stored hour", life)
	}
}
