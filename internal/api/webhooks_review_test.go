// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/store"
)

// TestWebhookSecretAnswersAreNotCached: an answer that shows a secret carries no-store, as a token answer does.
func TestWebhookSecretAnswersAreNotCached(t *testing.T) {
	t.Parallel()
	fx := newWebhookFixture(t)
	created := call(t, fx.s, "POST", fx.base(), fx.key, map[string]any{
		"url": "https://hooks.example.com/in", "events": []string{"file.created"}})
	wantStatus(t, created, http.StatusCreated)
	if got := created.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("create Cache-Control = %q, want no-store", got)
	}
	hook := decode[webhookJSON](t, created).ID
	rotated := call(t, fx.s, "POST", fx.base()+"/"+hook+"/secret", fx.key, nil)
	wantStatus(t, rotated, http.StatusOK)
	if got := rotated.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("rotate Cache-Control = %q, want no-store", got)
	}
}

// TestWebhookPreviousSecretUntilIsNullAfterGrace: previous_secret_until is the end of the grace period while
// it runs, and null once it has passed, even before the expired copy is removed from the row.
func TestWebhookPreviousSecretUntilIsNullAfterGrace(t *testing.T) {
	t.Parallel()
	fx := newWebhookFixture(t)
	hook, _ := fx.create(t, nil)
	wantStatus(t, call(t, fx.s, "POST", fx.base()+"/"+hook+"/secret", fx.key, nil), 200)
	got := decode[webhookJSON](t, call(t, fx.s, "GET", fx.base()+"/"+hook, fx.key, nil))
	if got.PreviousSecretUntil == nil {
		t.Fatal("during the grace period previous_secret_until is null")
	}

	ctx := context.Background()
	if err := fx.s.store.Write(ctx, func(tx *store.Tx) error {
		e, err := tx.WebhookEndpoint(ctx, fx.acc.ID, hook)
		if err != nil {
			return err
		}
		past := time.Now().Add(-time.Hour)
		e.PreviousSecretUntil = &past
		return tx.UpdateWebhookEndpoint(ctx, e)
	}); err != nil {
		t.Fatal(err)
	}
	got = decode[webhookJSON](t, call(t, fx.s, "GET", fx.base()+"/"+hook, fx.key, nil))
	if got.PreviousSecretUntil != nil {
		t.Fatalf("after the grace period previous_secret_until = %v, want null", got.PreviousSecretUntil)
	}
	owner := decode[webhookJSON](t, callSession(t, fx.s, fx.owner, "GET", fx.base()+"/"+hook, nil))
	if owner.PreviousSecretUntil != nil {
		t.Fatalf("owner sees previous_secret_until = %v after the grace period", owner.PreviousSecretUntil)
	}
}
