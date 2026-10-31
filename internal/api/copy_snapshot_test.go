// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/store"
)

// TestSnapshotGrowingPastTheLimitFailsNothingVisible: the tree is asked for when it fits copy.max_nodes (7 nodes, limit
// 10), and it grows before the snapshot reads it. The snapshot checks the limit at every page, so a tree past it fails
// the copy with nothing copied, and a tree that grows to exactly the limit still copies.
func TestSnapshotGrowingPastTheLimitFailsNothingVisible(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		grow     int
		wantFail bool
	}{
		{"past the limit", 4, true},
		{"at the limit", 3, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx := newChunkedFixture(t)
			fx.s.cfg.Copy.MaxNodes = 10
			fx.queue(t)
			// The growth is the source changing, not the copy, so the baseline is taken after it.
			var before usageJSON
			grown := false
			fx.s.copyFault = func(stage string) error {
				if stage == "snapshot" && !grown {
					grown = true
					for i := 0; i < tc.grow; i++ {
						putNode(t, fx.s, fx.key, fx.f.ID, "/tree/grow"+string(rune('a'+i))+".txt", "g")
					}
					before = usageOf(t, fx.s, fx.key, fx.acc.ID)
				}
				return nil
			}
			done := fx.run(t)
			if !tc.wantFail {
				if done.Status != store.JobSucceeded {
					t.Fatalf("job = %+v, want succeeded at the limit", done)
				}
				return
			}
			if done.Status != store.JobFailed {
				t.Fatalf("job = %+v, want failed past the limit", done)
			}
			if !strings.Contains(done.Error, "at most 10 nodes") {
				t.Errorf("job error = %q, want the node limit", done.Error)
			}
			fx.assertNothingVisible(t, before)
		})
	}
}
