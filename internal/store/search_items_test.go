// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
)

// TestItemsDoNotResetEachOther: a second item queued on a folder leaves the first item's cursor where the job left it, and
// both finish. Frequent copies therefore do not restart each other's work.
func TestItemsDoNotResetEachOther(t *testing.T) {
	s, _ := searchFixture(t)
	ctx := context.Background()
	addFolder(t, s, "fld_b", "nod_rootb", 0)
	write(t, s, func(tx *Tx) error {
		for i := range 30 {
			n := node("nod_x"+string(rune('a'+i)), "fld_b", "nod_rootb", "x"+string(rune('a'+i))+".txt", "file")
			if err := tx.InsertNodeNoIndex(ctx, n); err != nil {
				return err
			}
		}
		return queueFolder(ctx, tx, "fld_b")
	})
	if _, err := s.SearchIndexStep(ctx, 10); err != nil {
		t.Fatal(err)
	}
	cursorOfFirst := func() int64 {
		var c int64
		if err := s.r.QueryRowContext(ctx, "SELECT cursor FROM search_backlog ORDER BY id LIMIT 1").Scan(&c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	before := cursorOfFirst()
	if before == 0 {
		t.Fatal("the first batch did not move the cursor")
	}
	write(t, s, func(tx *Tx) error {
		if err := tx.InsertNodeNoIndex(ctx, node("nod_y", "fld_b", "nod_rootb", "later.txt", "file")); err != nil {
			return err
		}
		max, err := tx.NodeRowIDMax(ctx)
		if err != nil {
			return err
		}
		return tx.QueueSearchRange(ctx, "acc_a", "fld_b", max-1, max)
	})
	if after := cursorOfFirst(); after != before {
		t.Fatalf("the first item's cursor moved from %d to %d when a second item was queued", before, after)
	}
	for {
		done, err := s.SearchIndexStep(ctx, 10)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
	}
	if n, _, _ := searchCounts(t, s); n != 31 {
		t.Fatalf("after both items: %d rows, want 31", n)
	}
}
