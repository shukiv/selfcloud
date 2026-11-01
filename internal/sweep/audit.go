// SPDX-License-Identifier: AGPL-3.0-or-later

package sweep

import (
	"context"
	"time"
)

// Audit retention (decision 48). The sweep removes the audit entries older than audit.retention_days, installation-wide
// entries included. A pass runs at most once an hour, and removes at most auditBatch entries per write, so a backlog
// is removed over several writes and no write holds the writer for long.
const (
	auditPruneEvery = time.Hour
	auditBatch      = 1000
	auditBatches    = 200
)

// pruneAudit removes the audit entries past the retention, each batch in its own write. A retention of zero keeps every
// entry. The count removed is logged at info level; the entries' contents are never logged.
func (w *Sweeper) pruneAudit(ctx context.Context, now time.Time, res *Result) error {
	if w.auditRetention <= 0 || now.Sub(w.auditPruned) < auditPruneEvery {
		return nil
	}
	w.auditPruned = now
	before := now.Add(-w.auditRetention)
	var removed int64
	var err error
	for i := 0; i < auditBatches && ctx.Err() == nil; i++ {
		var n int64
		if n, err = w.st.PruneAudit(ctx, before, auditBatch); err != nil {
			break
		}
		removed += n
		if n < auditBatch {
			break
		}
	}
	res.AuditPruned += removed
	if removed > 0 {
		w.log.Info("audit log pruned", "removed", removed)
	}
	return err
}
