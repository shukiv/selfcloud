// SPDX-License-Identifier: AGPL-3.0-or-later

package sweep

import (
	"context"
	"time"
)

// Imports are pruned in batches, as the audit log is (decision 92). A batch is one write of at most importBatch records,
// and a pass makes at most importBatches of them.
const (
	importBatch   = 500
	importBatches = 100
)

// pruneImports removes the records of finished imports (their state and their item rows) that finished before the
// retention. The files they imported stay, and so does what the imports remember of them. A retention of zero keeps
// every record.
func (w *Sweeper) pruneImports(ctx context.Context, now time.Time, res *Result) error {
	if w.importRetention <= 0 {
		return nil
	}
	before := now.Add(-w.importRetention)
	var removed int64
	for i := 0; i < importBatches && ctx.Err() == nil; i++ {
		n, err := w.st.PurgeImports(ctx, before, importBatch)
		if err != nil {
			res.ImportsPruned += removed
			return err
		}
		removed += n
		if n < importBatch {
			break
		}
	}
	res.ImportsPruned += removed
	if removed > 0 {
		w.log.Info("import records pruned", "removed", removed)
	}
	return nil
}
