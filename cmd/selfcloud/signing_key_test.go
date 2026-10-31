// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/webhook"
)

// TestNewSigningKeyNeedsTheFlag: without -i-lost-the-old-key the command refuses and makes no key.
func TestNewSigningKeyNeedsTheFlag(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("SELFCLOUD_DATA_DIR", dataDir)
	code, _, errOut := runCmd(t, "admin", "new-signing-key")
	if code != 2 || !strings.Contains(errOut, "-i-lost-the-old-key") {
		t.Fatalf("new-signing-key without the flag = %d %q; want 2 naming the flag", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "keys", "signing.key")); !os.IsNotExist(err) {
		t.Fatalf("the refused command made a key: %v", err)
	}
}

// TestNewSigningKeyRefusesAnExistingKey: with the flag, a key that is there is not replaced.
func TestNewSigningKeyRefusesAnExistingKey(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("SELFCLOUD_DATA_DIR", dataDir)
	keys := filepath.Join(dataDir, "keys")
	if err := os.MkdirAll(keys, 0o700); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(keys, "signing.key")
	orig := bytes.Repeat([]byte{5}, 32)
	if err := os.WriteFile(keyPath, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runCmd(t, "admin", "new-signing-key", "-i-lost-the-old-key")
	if code != 1 || !strings.Contains(errOut, "already exists") {
		t.Fatalf("new-signing-key over a key = %d %q; want 1", code, errOut)
	}
	if got, err := os.ReadFile(keyPath); err != nil || !bytes.Equal(got, orig) {
		t.Fatalf("the existing key was replaced: %v", err)
	}
}

// TestNewSigningKeyMakesAKeyAndPrintsNoKey: with the flag and no key, the command makes a 32-byte key and prints neither
// the key nor its base64.
func TestNewSigningKeyMakesAKeyAndPrintsNoKey(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("SELFCLOUD_DATA_DIR", dataDir)
	code, out, errOut := runCmd(t, "admin", "new-signing-key", "-i-lost-the-old-key")
	if code != 0 {
		t.Fatalf("new-signing-key = %d %q %q", code, out, errOut)
	}
	key, err := os.ReadFile(filepath.Join(dataDir, "keys", "signing.key"))
	if err != nil || len(key) != 32 {
		t.Fatalf("the key file = %d bytes, %v; want 32", len(key), err)
	}
	if strings.Contains(out, string(key)) || strings.Contains(out, base64.RawURLEncoding.EncodeToString(key)) ||
		strings.Contains(out, base64.StdEncoding.EncodeToString(key)) {
		t.Fatal("the command printed the key")
	}
}

// TestRewrapDisableFlagIsPassedThrough: an active endpoint whose secret neither key opens is reported and left alone
// without -disable-unreadable (exit 1), and is disabled with it (exit 0).
func TestRewrapDisableFlagIsPassedThrough(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("SELFCLOUD_DATA_DIR", dataDir)
	keys := filepath.Join(dataDir, "keys")
	if err := os.MkdirAll(keys, 0o700); err != nil {
		t.Fatal(err)
	}
	cur := bytes.Repeat([]byte{5}, 32)
	if err := os.WriteFile(filepath.Join(keys, "signing.key"), cur, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st, err := store.Open(ctx, store.Options{Path: filepath.Join(dataDir, "selfcloud.db")})
	if err != nil {
		t.Fatal(err)
	}
	acc, ep := id.New("acc"), id.New("whk")
	now := store.Now()
	// The secret is sealed under a key that is neither the current nor the previous one.
	other, err := webhook.NewSealer(bytes.Repeat([]byte{9}, 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := other.Seal([]byte("plain"), webhook.Binding{AccountID: acc, EndpointID: ep, Field: webhook.FieldSecret})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Write(ctx, func(tx *store.Tx) error {
		if err := tx.InsertAccount(ctx, &store.Account{ID: acc, Name: "Seed", Status: store.AccountActive, CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		return tx.InsertWebhookEndpoint(ctx, &store.WebhookEndpoint{ID: ep, AccountID: acc, URL: "https://hooks.example.com/in",
			State: store.WebhookActive, SecretEnc: enc, CreatedBy: "key_seed", CreatedAt: now, UpdatedAt: now})
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	state := func() string {
		s, err := store.Open(ctx, store.Options{Path: filepath.Join(dataDir, "selfcloud.db")})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		e, err := s.WebhookEndpoint(ctx, acc, ep)
		if err != nil {
			t.Fatal(err)
		}
		return e.State
	}

	code, out, errOut := runCmd(t, "admin", "rewrap-secrets")
	if code != 1 || !strings.Contains(out, "unreadable=1 disabled=0") || state() != store.WebhookActive {
		t.Fatalf("rewrap without the flag = %d %q %q, state %s; want 1, unreadable=1 disabled=0, active", code, out, errOut, state())
	}
	code, out, errOut = runCmd(t, "admin", "rewrap-secrets", "-disable-unreadable")
	if code != 0 || !strings.Contains(out, "disabled=1") || state() != store.WebhookDisabled {
		t.Fatalf("rewrap with -disable-unreadable = %d %q %q, state %s; want 0, disabled=1, disabled", code, out, errOut, state())
	}
}
