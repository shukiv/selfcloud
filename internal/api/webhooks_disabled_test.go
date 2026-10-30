// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/store"
)

// disableEndpoint marks an endpoint disabled for reason, with seven failures behind it.
func disableEndpoint(t *testing.T, fx *webhookFixture, hook, reason string) {
	t.Helper()
	ctx := context.Background()
	if err := fx.s.store.Write(ctx, func(tx *store.Tx) error {
		e, err := tx.WebhookEndpoint(ctx, fx.acc.ID, hook)
		if err != nil {
			return err
		}
		e.State, e.DisabledReason, e.ConsecutiveFailures = store.WebhookDisabled, reason, 7
		return tx.UpdateWebhookEndpoint(ctx, e)
	}); err != nil {
		t.Fatal(err)
	}
}

// TestWebhookDisabledLeavesOnlyWithANewURLOrARotation: for each disabled reason, a state change alone
// (to paused or to active) is refused. An unchanged URL keeps the state and the reason. A new URL in the
// same request moves it, clears the reason and resets the failure count. A rotation pauses it.
func TestWebhookDisabledLeavesOnlyWithANewURLOrARotation(t *testing.T) {
	t.Parallel()
	for _, reason := range []string{"failing", "overflow", "secret_unreadable"} {
		t.Run(reason, func(t *testing.T) {
			fx := newWebhookFixture(t)
			hook, _ := fx.create(t, nil)
			disableEndpoint(t, fx, hook, reason)

			wantError(t, call(t, fx.s, "PATCH", fx.base()+"/"+hook, fx.key, map[string]any{"state": "paused"}), 409, apierr.Conflict)
			wantError(t, call(t, fx.s, "PATCH", fx.base()+"/"+hook, fx.key, map[string]any{"state": "active"}), 409, apierr.Conflict)
			wantError(t, call(t, fx.s, "PATCH", fx.base()+"/"+hook, fx.key,
				map[string]any{"url": "https://hooks.example.com/in", "state": "paused"}), 409, apierr.Conflict)
			if e := fx.endpoint(t, hook); e.State != store.WebhookDisabled || e.DisabledReason != reason {
				t.Fatalf("a refused change moved it: state %s, reason %q", e.State, e.DisabledReason)
			}

			// An unchanged URL with a description keeps the state and the reason.
			wantStatus(t, call(t, fx.s, "PATCH", fx.base()+"/"+hook, fx.key,
				map[string]any{"url": "https://hooks.example.com/in", "description": "d"}), 200)
			if e := fx.endpoint(t, hook); e.State != store.WebhookDisabled || e.DisabledReason != reason {
				t.Fatalf("an unchanged URL moved it: state %s, reason %q", e.State, e.DisabledReason)
			}

			// A new URL, with or without a state, leaves the disabled state and resets the count.
			wantStatus(t, call(t, fx.s, "PATCH", fx.base()+"/"+hook, fx.key,
				map[string]any{"url": "https://hooks.example.com/new"}), 200)
			e := fx.endpoint(t, hook)
			if e.State != store.WebhookPaused || e.DisabledReason != "" || e.ConsecutiveFailures != 0 {
				t.Fatalf("after a new URL: state %s, reason %q, failures %d", e.State, e.DisabledReason, e.ConsecutiveFailures)
			}

			// A rotation of a disabled endpoint pauses it and clears the reason.
			disableEndpoint(t, fx, hook, reason)
			wantStatus(t, call(t, fx.s, "POST", fx.base()+"/"+hook+"/secret", fx.key, nil), 200)
			if e := fx.endpoint(t, hook); e.State != store.WebhookPaused || e.DisabledReason != "" {
				t.Fatalf("after a rotation: state %s, reason %q", e.State, e.DisabledReason)
			}
		})
	}
}
