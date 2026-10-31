// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
)

// The count of a subtree stops at its limit: a tree of six nodes counted with a limit of four reports four nodes, so
// a tree over WebhookNodeCap costs the cap and no more inside the write transaction.
func TestCountSubtreeStopsAtLimit(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	fx := newWebhookFixtureStore(t, s)
	dir := bulkTree(t, s, fx, 3, 2) // bulk, three folders, two files: six nodes
	if err := s.Write(ctx, func(tx *Tx) error {
		nodes, files, err := tx.countSubtree(ctx, fx.acc, dir.ID, 4)
		if err != nil {
			return err
		}
		if nodes != 4 {
			t.Fatalf("counted %d nodes with a limit of 4, want 4", nodes)
		}
		if files > 2 {
			t.Fatalf("counted %d files in a tree of two", files)
		}
		nodes, files, err = tx.countSubtree(ctx, fx.acc, dir.ID, 100)
		if err != nil {
			return err
		}
		if nodes != 6 || files != 2 {
			t.Fatalf("counted %d nodes and %d files of six nodes and two files", nodes, files)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
