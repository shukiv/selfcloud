// SPDX-License-Identifier: AGPL-3.0-or-later

package sweep

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/blob"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/webhook"
)

// TestSweepPrunesWebhookRows: one pass removes a delivered row past its week, the event no delivery names, and the grace
// secret whose period has ended; a row inside its retention stays.
func TestSweepPrunesWebhookRows(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, store.Options{Path: filepath.Join(dir, "selfcloud.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	blobs, err := blob.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	acc := &store.Account{ID: id.New("acc"), Name: "A", Status: store.AccountActive, CreatedAt: store.Now(), UpdatedAt: store.Now()}
	if err := st.Write(ctx, func(tx *store.Tx) error { return tx.InsertAccount(ctx, acc) }); err != nil {
		t.Fatal(err)
	}
	now := store.Now()
	old := now.Add(-8 * 24 * time.Hour)
	expired := now.Add(-time.Hour)
	e := &store.WebhookEndpoint{ID: id.New("whk"), AccountID: acc.ID, URL: "https://hooks.example.com/in",
		Events: []string{"file.created"}, State: store.WebhookActive, SecretEnc: []byte("sealed-secret-bytes"),
		PreviousSecretEnc: []byte("sealed-previous-bytes"), PreviousSecretUntil: &expired,
		CreatedBy: "key_test", CreatedAt: now, UpdatedAt: now}
	if err := st.Write(ctx, func(tx *store.Tx) error { return tx.InsertWebhookEndpoint(ctx, e) }); err != nil {
		t.Fatal(err)
	}
	// One event eight days old, delivered eight days ago: past the week, so the sweep removes both.
	if err := st.Write(ctx, func(tx *store.Tx) error {
		return tx.EmitEvent(ctx, acc.ID, "file.created", old, webhook.File{ID: "nod_old", Name: "old"})
	}); err != nil {
		t.Fatal(err)
	}
	jobs, err := st.ClaimWebhookJobs(ctx, old, time.Minute, 1, e.ID)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim = %d, %v", len(jobs), err)
	}
	if err := st.RecordWebhookOutcome(ctx, store.WebhookOutcome{DeliveryID: jobs[0].DeliveryID, AccountID: acc.ID,
		EndpointID: e.ID, At: old, Code: 204, Lease: jobs[0].Lease}); err != nil {
		t.Fatal(err)
	}
	// A recent event, still inside its retention, stays.
	if err := st.Write(ctx, func(tx *store.Tx) error {
		return tx.EmitEvent(ctx, acc.ID, "file.created", now, webhook.File{ID: "nod_new", Name: "new"})
	}); err != nil {
		t.Fatal(err)
	}

	sw := New(Options{Store: st, Blobs: blobs})
	res, err := sw.Once(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if res.WebhookDeliveries != 1 || res.WebhookEvents != 1 || res.WebhookSecrets != 1 {
		t.Fatalf("pass removed %d deliveries, %d events, cleared %d secrets; want 1, 1, 1",
			res.WebhookDeliveries, res.WebhookEvents, res.WebhookSecrets)
	}
	got, err := st.WebhookEndpoint(ctx, acc.ID, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PreviousSecretEnc != nil || got.PreviousSecretUntil != nil {
		t.Fatal("the expired grace secret is still in the row")
	}
	list, _, err := st.WebhookDeliveries(ctx, acc.ID, e.ID, store.Page{Limit: 10})
	if err != nil || len(list) != 1 || list[0].State != "pending" {
		t.Fatalf("the recent delivery should remain pending: %d rows, %v", len(list), err)
	}
}
