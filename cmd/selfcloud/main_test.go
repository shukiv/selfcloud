package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"selfcloud/internal/auth"
	"selfcloud/internal/store"
)

func runCmd(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestVersionAndUnknown(t *testing.T) {
	if code, out, _ := runCmd(t, "version"); code != 0 || strings.TrimSpace(out) == "" {
		t.Errorf("version = %d %q", code, out)
	}
	if code, _, _ := runCmd(t, "frobnicate"); code != 2 {
		t.Errorf("unknown command exit = %d", code)
	}
	if code, _, _ := runCmd(t, "admin", "frobnicate"); code != 2 {
		t.Errorf("unknown admin command exit = %d", code)
	}
}

func TestAdminCommands(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SELFCLOUD_DATA_DIR", dir)

	code, out, errOut := runCmd(t, "admin", "create-key", "-name", "ops")
	secret := strings.TrimSpace(out)
	if code != 0 || !auth.ValidKeyFormat(auth.AdminKeyPrefix, secret) {
		t.Fatalf("create-key = %d %q %q", code, out, errOut)
	}

	code, out, _ = runCmd(t, "admin", "list-keys")
	if code != 0 || !strings.Contains(out, "ops") || !strings.Contains(out, "active") {
		t.Fatalf("list-keys = %d\n%s", code, out)
	}
	keyID := strings.Fields(strings.Split(out, "\n")[1])[0]

	if code, out, errOut = runCmd(t, "admin", "revoke-key", keyID); code != 0 {
		t.Fatalf("revoke-key = %d %q %q", code, out, errOut)
	}
	if _, out, _ = runCmd(t, "admin", "list-keys"); !strings.Contains(out, "revoked") {
		t.Errorf("after revoke:\n%s", out)
	}
	if code, _, _ = runCmd(t, "admin", "revoke-key", "key_nope"); code != 1 {
		t.Errorf("revoking an unknown key exit = %d", code)
	}
	if code, _, _ = runCmd(t, "admin", "revoke-key"); code != 2 {
		t.Errorf("revoke-key without an ID exit = %d", code)
	}

	code, out, errOut = runCmd(t, "admin", "create-account", "-name", "Example", "-external-id", "ext-1", "-quota", "10GiB")
	if code != 0 || !strings.Contains(out, "account_id=acc_") || !strings.Contains(out, "account_key="+auth.AccountKeyPrefix) {
		t.Fatalf("create-account = %d %q %q", code, out, errOut)
	}
	if code, _, _ = runCmd(t, "admin", "create-account", "-name", "Again", "-external-id", "ext-1"); code != 1 {
		t.Errorf("duplicate external ID exit = %d", code)
	}
}

func TestBootstrapAdminKey(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, store.Options{Path: filepath.Join(dir, "selfcloud.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log := slog.New(slog.DiscardHandler)

	_, out, _ := runCmd(t, "admin", "generate-key")
	file := filepath.Join(dir, "admin.key")
	if err := os.WriteFile(file, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := bootstrapAdminKey(ctx, st, file, log); err != nil {
			t.Fatal(err)
		}
	}
	keys, _ := st.APIKeys(ctx, "")
	if len(keys) != 1 {
		t.Errorf("admin keys after two bootstraps = %d, want 1", len(keys))
	}

	bad := filepath.Join(dir, "bad.key")
	os.WriteFile(bad, []byte("not a key"), 0o600)
	if err := bootstrapAdminKey(ctx, st, bad, log); err == nil {
		t.Error("a malformed key file was accepted")
	}
}
