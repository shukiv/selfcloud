// SPDX-License-Identifier: AGPL-3.0-or-later

package sweep

import (
	"context"
	"time"
)

// Changes retention (slice cf1, decision 235). The sweep removes the journal rows older than changes.retention, and
// rows whose account is gone. A pass runs at most once an hour, and removes at most changesBatch rows per write, so a
// backlog is removed over several writes and no write holds the writer for long. Each prune raises the mark a cursor
// below which is refused with gone.
const (
	changesPruneEvery = time.Hour
	changesBatch      = 1000
	changesBatches    = 200
)

// pruneChanges removes the journal rows past the retention, each batch in its own write. A retention of zero keeps every
// row. The count removed is logged at info level; the rows' contents are never logged.
func (w *Sweeper) pruneChanges(ctx context.Context, now time.Time, res *Result) error {
	if w.changesRetention <= 0 || now.Sub(w.changesPruned) < changesPruneEvery {
		return nil
	}
	w.changesPruned = now
	before := now.Add(-w.changesRetention)
	var removed int64
	var err error
	for i := 0; i < changesBatches && ctx.Err() == nil; i++ {
		var n int64
		if n, err = w.st.PruneNodeChanges(ctx, before, changesBatch); err != nil {
			break
		}
		removed += n
		if n < changesBatch {
			break
		}
	}
	res.ChangesPruned += removed
	if removed > 0 {
		w.log.Info("changes journal pruned", "removed", removed)
	}
	return err
}
