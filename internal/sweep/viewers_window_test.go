// SPDX-License-Identifier: AGPL-3.0-or-later

package sweep

import (
	"testing"

	"github.com/shukiv/selfcloud/internal/store"
)

// TestRecentRetentionIsTheViewerWindow: the who-viewed list looks back as far as the recent table keeps a row, so the two
// periods must stay equal. A change to one without the other shows a view the sweep has already dropped, or hides one it
// has not.
func TestRecentRetentionIsTheViewerWindow(t *testing.T) {
	if recentRetention != store.ViewerWindow {
		t.Fatalf("recentRetention is %v and store.ViewerWindow is %v; they must be equal", recentRetention, store.ViewerWindow)
	}
}
