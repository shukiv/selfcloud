// SPDX-License-Identifier: AGPL-3.0-or-later

package sweep

import (
	"context"
	"time"
)

// Webhook retention (design section 10, slice j3). A delivered row is kept a week and a failed one a month, so the
// log an owner reads reaches back that far. Then the events that no delivery names are removed.
const (
	webhookDeliveredKeep = 7 * 24 * time.Hour
	webhookFailedKeep    = 30 * 24 * time.Hour
	// webhookPruneBatch is how many rows one write removes. The writer is held for one batch at a time, so a large
	// backlog is removed over several writes.
	webhookPruneBatch = 500
	// webhookEventBatches is how many event batches one sweep walks at most (decision 28).
	webhookEventBatches = 200
)

// pruneWebhooks removes the delivered and failed deliveries past their retention, then the events no delivery refers
// to, and clears the encrypted copy of each secret whose grace period has ended. Each batch is its own write.
func (w *Sweeper) pruneWebhooks(ctx context.Context, now time.Time, res *Result) error {
	for {
		n, err := w.st.PruneWebhookDeliveries(ctx, now.Add(-webhookDeliveredKeep), now.Add(-webhookFailedKeep), webhookPruneBatch)
		if err != nil {
			return err
		}
		res.WebhookDeliveries += n
		if n == 0 || ctx.Err() != nil {
			break
		}
	}
	// The events are walked from a cursor kept on the sweeper, a bounded number of batches per sweep, so a table of a
	// million events is walked over several sweeps and no single sweep holds the writer for long.
	for i := 0; i < webhookEventBatches; i++ {
		next, n, err := w.st.PruneWebhookEvents(ctx, w.webhookEventCursor, webhookPruneBatch)
		if err != nil {
			return err
		}
		w.webhookEventCursor = next
		res.WebhookEvents += n
		if next == 0 || ctx.Err() != nil {
			break
		}
	}
	var err error
	res.WebhookSecrets, err = w.st.ClearExpiredPreviousSecrets(ctx, now)
	return err
}
