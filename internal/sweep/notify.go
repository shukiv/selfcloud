// SPDX-License-Identifier: AGPL-3.0-or-later

package sweep

import (
	"context"
	"time"
)

// Notification retention (design section 14, slice r, decision 43). A notification is kept ninety days, and the number a
// user keeps is capped where they are written (store.NotifyPerUser). A mail row that finished is kept a week, as the
// webhook log is; a row still pending after that is expired as skipped, so it never waits for mail for ever.
const (
	notificationKeep = 90 * 24 * time.Hour
	notifyMailKeep   = 7 * 24 * time.Hour
	// notifyPruneBatch is how many rows one write removes, and notifyPruneBatches how many batches one sweep runs at
	// most. A backlog is removed over several sweeps, and no sweep holds the writer for long.
	notifyPruneBatch   = 500
	notifyPruneBatches = 200
)

// pruneNotifications removes the notifications past their retention, expires the mail that waited past its keep, and
// removes the finished mail rows past theirs. Each batch is its own write.
func (w *Sweeper) pruneNotifications(ctx context.Context, now time.Time, res *Result) error {
	for i := 0; i < notifyPruneBatches && ctx.Err() == nil; i++ {
		n, err := w.st.PruneNotifications(ctx, now.Add(-notificationKeep), notifyPruneBatch)
		if err != nil {
			return err
		}
		res.Notifications += n
		if n == 0 {
			break
		}
	}
	for i := 0; i < notifyPruneBatches && ctx.Err() == nil; i++ {
		n, err := w.st.ExpireMail(ctx, now.Add(-notifyMailKeep), now, notifyPruneBatch)
		if err != nil {
			return err
		}
		res.MailExpired += n
		if n == 0 {
			break
		}
	}
	for i := 0; i < notifyPruneBatches && ctx.Err() == nil; i++ {
		n, err := w.st.PruneMailRows(ctx, now.Add(-notifyMailKeep), notifyPruneBatch)
		if err != nil {
			return err
		}
		res.MailRows += n
		if n == 0 {
			break
		}
	}
	return nil
}
