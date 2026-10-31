// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"os"
	"sort"
	"testing"
	"time"
)

// TestWebhookScaleMeasure times the webhook sweep and the metrics scrape on a store of a million events and a million
// deliveries (review finding 8). It is skipped unless SELFCLOUD_SCALE=1, because it seeds about two million rows and
// takes minutes. Each figure is the median of its runs, logged with -v.
func TestWebhookScaleMeasure(t *testing.T) {
	if os.Getenv("SELFCLOUD_SCALE") != "1" {
		t.Skip("set SELFCLOUD_SCALE=1 to measure at a million rows")
	}
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "Acme")
	e := newEndpointRow(t, s, acc, "https://scale.example.com/in", "file.created")
	const n = 1000000
	seed := time.Now()
	if err := s.Write(ctx, func(tx *Tx) error {
		if _, err := tx.q.ExecContext(ctx, `WITH RECURSIVE k(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM k WHERE i < ?)
			INSERT INTO webhook_events (event_id, account_id, type, payload, created_at)
			SELECT 'evt_s' || i, ?, 'file.created', '{}', 0 FROM k`, n, acc); err != nil {
			return err
		}
		// Nearly every delivery is delivered and finished long ago: the rows the sweep and the scrape meet.
		_, err := tx.q.ExecContext(ctx, `INSERT INTO webhook_deliveries (id, account_id, endpoint_id, event_row_id, state,
			attempts, next_attempt_at, created_at, finished_at, generation)
			SELECT 'dlv_s' || id, ?, ?, id, CASE WHEN id % 1000 = 0 THEN 'failed' ELSE 'delivered' END, 1, 0, 0, 0, 0
			FROM webhook_events WHERE account_id = ?`, acc, e.ID, acc)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	t.Logf("seeded %d events and %d deliveries in %v", n, n, time.Since(seed).Round(time.Millisecond))

	median := func(f func() time.Duration, runs int) time.Duration {
		var d []time.Duration
		for i := 0; i < runs; i++ {
			d = append(d, f())
		}
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		return d[len(d)/2]
	}

	// The metrics scrape: the counts by state over the million deliveries.
	scrape := median(func() time.Duration {
		start := time.Now()
		if _, err := s.WebhookQueueStats(ctx); err != nil {
			t.Fatal(err)
		}
		return time.Since(start)
	}, 5)
	t.Logf("WebhookQueueStats at %d deliveries: median %v", n, scrape.Round(time.Millisecond))

	// One event batch with every event named by a delivery: the walk must read the batch and delete nothing. The cursor
	// starts at the far end of the table, where a scan from the start would cost the most.
	kept := median(func() time.Duration {
		start := time.Now()
		if _, _, err := s.PruneWebhookEvents(ctx, n-webhookTestBatch, webhookTestBatch); err != nil {
			t.Fatal(err)
		}
		return time.Since(start)
	}, 5)
	t.Logf("PruneWebhookEvents batch of %d, all named, at the end of %d events: median %v", webhookTestBatch, n, kept.Round(time.Microsecond))

	// Remove the deliveries, then the same batch deletes its events.
	if err := s.Write(ctx, func(tx *Tx) error {
		_, err := tx.q.ExecContext(ctx, "DELETE FROM webhook_deliveries")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	calls, removed := 0, 0
	cursor := int64(0)
	start := time.Now()
	for {
		next, k, err := s.PruneWebhookEvents(ctx, cursor, webhookTestBatch)
		if err != nil {
			t.Fatal(err)
		}
		calls++
		removed += k
		cursor = next
		if cursor == 0 {
			break
		}
	}
	total := time.Since(start)
	t.Logf("full walk of %d events in %d batches of %d: %v, removed %d, %v per batch",
		n, calls, webhookTestBatch, total.Round(time.Millisecond), removed, (total / time.Duration(calls)).Round(time.Microsecond))
}

// webhookTestBatch is the batch the measurement uses: the sweep's batch size.
const webhookTestBatch = 500
