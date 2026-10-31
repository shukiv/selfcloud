// SPDX-License-Identifier: AGPL-3.0-or-later

package sweep

import (
	"context"

	"github.com/shukiv/selfcloud/internal/store"
)

// copyCleanupLimit is how many staged rows one cleanup step removes, the same bound the copy's own cleanup uses.
const copyCleanupLimit = 2000

// cleanupCopies removes the rows of chunked copies whose job has finished without them: a copy that failed, or a
// process that stopped during cleanup, or a published copy whose snapshot was not dropped. A copy whose job is still
// queued or running is never touched: its runner owns it. It returns how many copies it finished cleaning.
func (w *Sweeper) cleanupCopies(ctx context.Context) (int, error) {
	ids, err := w.st.AbandonedCopyJobs(ctx, copyCleanupLimit)
	if err != nil {
		return 0, err
	}
	cleaned := 0
	for _, jobID := range ids {
		for {
			done, err := w.st.CleanupCopyStep(ctx, jobID, copyCleanupLimit, store.Now())
			if err != nil {
				return cleaned, err
			}
			if done {
				cleaned++
				break
			}
		}
	}
	return cleaned, nil
}
