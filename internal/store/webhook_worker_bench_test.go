// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/webhook"
)

// The measurements behind decision 28: what the overflow check costs at the cap, what a claim and a record hold the
// writer for, what a batch of pruning holds it for, and what the queue stats cost at a scrape.

// BenchmarkEmitAtCap is one event to an endpoint with 100,000 pending deliveries: the check reads the maintained count,
// so the cost is the same as with no queue at all.
func BenchmarkEmitAtCap(b *testing.B) {
	s := openBench(b)
	acc := newAccountRow(b, s, "Acme")
	e := newEndpointRow(b, s, acc, "https://cap.example.com/in", "file.created")
	seedPending(b, s, acc, e.ID, WebhookPendingLimit-1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := s.Write(context.Background(), func(tx *Tx) error {
			return tx.EmitEvent(context.Background(), acc, "file.created", Now(), webhook.File{ID: "nod_b", Name: "b"})
		})
		if err != nil {
			b.Fatal(err)
		}
		// Each iteration adds one pending row; undo it so every iteration sees the same count.
		if _, err := s.w.ExecContext(context.Background(), "DELETE FROM webhook_deliveries WHERE endpoint_id = ? AND event_row_id = (SELECT MAX(id) FROM webhook_events)", e.ID); err != nil {
			b.Fatal(err)
		}
		if _, err := s.w.ExecContext(context.Background(), "UPDATE webhook_endpoints SET pending_count = pending_count - 1 WHERE id = ?", e.ID); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPendingProbeAtCap is the alternative the counter replaces: a bounded probe of the pending rows, at the cap.
// It walks up to WebhookPendingLimit index entries on every event.
func BenchmarkPendingProbeAtCap(b *testing.B) {
	s := openBench(b)
	acc := newAccountRow(b, s, "Acme")
	e := newEndpointRow(b, s, acc, "https://probe.example.com/in", "file.created")
	seedPending(b, s, acc, e.ID, WebhookPendingLimit)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var n int
		if err := s.r.QueryRowContext(context.Background(),
			"SELECT COUNT(*) FROM (SELECT 1 FROM webhook_deliveries WHERE endpoint_id = ? AND state = 'pending' LIMIT ?)",
			e.ID, WebhookPendingLimit+1).Scan(&n); err != nil {
			b.Fatal(err)
		}
		if n != WebhookPendingLimit {
			b.Fatalf("probe = %d", n)
		}
	}
}

// BenchmarkClaimAndRecordFifty is one tick of the worker against 50 endpoints with one pending delivery each: one claim
// write, then 50 outcome writes, as the lanes do them. The writer is held for each write, not for the tick.
func BenchmarkClaimAndRecordFifty(b *testing.B) {
	s := openBench(b)
	acc := newAccountRow(b, s, "Acme")
	for i := 0; i < 50; i++ {
		newEndpointRow(b, s, acc, "https://n.example.com/in", "file.created")
	}
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		for j := 0; j < 50; j++ {
			emitFile(b, s, acc, "f", Now())
		}
		b.StartTimer()
		ctx := context.Background()
		now := Now()
		jobs, err := s.ClaimWebhookJobs(ctx, now, time.Minute, 50, "")
		if err != nil {
			b.Fatal(err)
		}
		for _, j := range jobs {
			if err := s.RecordWebhookOutcome(ctx, WebhookOutcome{DeliveryID: j.DeliveryID, AccountID: acc,
				EndpointID: j.EndpointID, At: now, Code: 204}); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkPruneBatch removes one batch of 500 delivered rows from a table of 200,000 delivered rows, the unit the sweep
// repeats so the writer is held one batch at a time.
func BenchmarkPruneBatch(b *testing.B) {
	s := openBench(b)
	acc := newAccountRow(b, s, "Acme")
	e := newEndpointRow(b, s, acc, "https://prune.example.com/in", "file.created")
	seedDelivered(b, s, acc, e.ID, 200000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n, err := s.PruneWebhookDeliveries(context.Background(), Now(), Now(), 500)
		if err != nil {
			b.Fatal(err)
		}
		if n != 500 && i == 0 {
			b.Fatalf("batch removed %d", n)
		}
		b.StopTimer()
		seedDelivered(b, s, acc, e.ID, 500)
		b.StartTimer()
	}
}

// BenchmarkQueueStats is one metrics scrape against 200,000 deliveries.
func BenchmarkQueueStats(b *testing.B) {
	s := openBench(b)
	acc := newAccountRow(b, s, "Acme")
	e := newEndpointRow(b, s, acc, "https://stats.example.com/in", "file.created")
	seedDelivered(b, s, acc, e.ID, 200000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.WebhookQueueStats(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFailDisabledBatch is one cleanup write of the worker: 2,000 pending deliveries of a disabled endpoint failed
// in one statement. A disable itself writes no such rows, so this is the only writer hold the queue of a disabled
// endpoint costs.
func BenchmarkFailDisabledBatch(b *testing.B) {
	s := openBench(b)
	acc := newAccountRow(b, s, "Acme")
	e := newEndpointState(b, s, acc, "https://dis.example.com/in", WebhookDisabled, "file.created")
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		seedPending(b, s, acc, e.ID, 2000)
		b.StartTimer()
		n, err := s.FailStaleDeliveries(context.Background(), 2000, Now())
		if err != nil || n != 2000 {
			b.Fatalf("failed %d, %v", n, err)
		}
	}
}

// seedDelivered writes n events, each with a delivered row for endpoint, finished long ago, in SQL.
func seedDelivered(b testing.TB, s *Store, accountID, endpoint string, n int) {
	b.Helper()
	ctx := context.Background()
	err := s.Write(ctx, func(tx *Tx) error {
		var before int64
		if err := tx.q.QueryRowContext(ctx, "SELECT COALESCE(MAX(id), 0) FROM webhook_events").Scan(&before); err != nil {
			return err
		}
		if _, err := tx.q.ExecContext(ctx, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?)
			INSERT INTO webhook_events (event_id, account_id, type, payload, created_at)
			SELECT 'evt_old_' || lower(hex(randomblob(12))), ?, 'file.created', '{}', 0 FROM n`, n, accountID); err != nil {
			return err
		}
		_, err := tx.q.ExecContext(ctx, `INSERT INTO webhook_deliveries (id, account_id, endpoint_id, event_row_id,
			state, attempts, next_attempt_at, finished_at, created_at)
			SELECT 'dlv_old_' || lower(hex(randomblob(12))), ?, ?, id, 'delivered', 1, 0, 0, 0 FROM webhook_events
			WHERE account_id = ? AND id > ?`, accountID, endpoint, accountID, before)
		return err
	})
	if err != nil {
		b.Fatal(err)
	}
}

// openBench opens a store for a benchmark.
func openBench(b *testing.B) *Store {
	b.Helper()
	return openTest(b)
}
