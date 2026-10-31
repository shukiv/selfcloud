// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/webhook"
)

// TestNewSigningKeyInvalidatesWhatItSays: after `admin new-signing-key` replaces a lost key, each thing the command names
// stops verifying under the new key: a delegated token (refused with 401 by a restarted server), a webhook secret (does not
// open), and the derived keys of signed download links and visitor credentials (they differ).
func TestNewSigningKeyInvalidatesWhatItSays(t *testing.T) {
	t.Parallel()
	var cfg *config.Config
	fx := newWebhookFixture(t, func(c *config.Config) { cfg = c })
	hook, _ := fx.create(t, nil)
	sealedUnder := fx.endpoint(t, hook).SecretEnc
	f := newFolder(t, fx.s, fx.key, fx.acc.ID, "Mine")
	tok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{f.ID}})
	probe := "/api/v1/accounts/" + fx.acc.ID + "/webhooks"
	if got := call(t, fx.s, "GET", probe, tok, nil).Code; got == 401 {
		t.Fatalf("the token was refused before the rotation: %d", got)
	}

	oldKey, err := os.ReadFile(filepath.Join(cfg.DataDir, "keys", keyFileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(cfg.DataDir, "keys", keyFileName)); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSigningKey(cfg.DataDir, cfg.Links); err != nil {
		t.Fatal(err)
	}
	newKey, err := os.ReadFile(filepath.Join(cfg.DataDir, "keys", keyFileName))
	if err != nil || bytes.Equal(newKey, oldKey) {
		t.Fatalf("the new key is not new: %v", err)
	}

	s2, err := New(Options{Config: cfg, Store: fx.s.store, Mailer: &mail.Recorder{}})
	if err != nil {
		t.Fatalf("the start after the new key was refused: %v", err)
	}
	wantStatus(t, call(t, s2, "GET", probe, tok, nil), 401)

	sealer, err := webhook.NewSealer(newKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sealer.Open(sealedUnder, webhookBinding(fx.acc.ID, hook, webhook.FieldSecret)); err == nil {
		t.Fatal("a secret sealed under the lost key opens under the new one")
	}

	oldLK, newLK := &linkKeys{current: oldKey}, &linkKeys{current: newKey}
	if bytes.Equal(linkMAC(oldLK.current, "t", []byte("p")), linkMAC(newLK.current, "t", []byte("p"))) {
		t.Fatal("a signed download link verifies under the new key")
	}
	oldShare, err := deriveShareKeys(oldLK)
	if err != nil {
		t.Fatal(err)
	}
	newShare, err := deriveShareKeys(newLK)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(oldShare.current.mac, newShare.current.mac) {
		t.Fatal("a visitor credential key did not change with the signing key")
	}
}
