// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestMeasureBackfill records what the content backfill and the hourly check cost on a database of existing files (slice
// q3, decision 46). It runs only when SELFCLOUD_MEASURE_CONTENT=1, writes its rows in bulk through SQL, and logs what it
// measured. The sizes are kept under the local limit of 200,000 rows.
func TestMeasureBackfill(t *testing.T) {
	if os.Getenv("SELFCLOUD_MEASURE_CONTENT") != "1" {
		t.Skip("set SELFCLOUD_MEASURE_CONTENT=1 to measure the content backfill")
	}
	s, _ := searchFixture(t)
	ctx := context.Background()
	const files = 20000
	start := time.Now()
	if _, err := s.w.ExecContext(ctx, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?)
		INSERT INTO blobs (id, account_id, size, sha256, state, created_at)
		SELECT 'blb_m' || i, 'acc_a', 1024, 'x', 'live', 0 FROM n`, files); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?)
		INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, size, blob_id, created_at, modified_at)
		SELECT 'nod_m' || i, 'acc_a', 'fld_a', 'nod_root', 'f' || i || '.txt', 'f' || i || '.txt', 'file', 1024, 'blb_m' || i, 0, 0
		FROM n`, files); err != nil {
		t.Fatal(err)
	}
	t.Logf("bulk rows: %d blobs and %d file nodes in %v", files, files, time.Since(start).Round(time.Millisecond))

	start = time.Now()
	steps, longest := 0, time.Duration(0)
	for {
		s0 := time.Now()
		done, err := s.TextBackfillStep(ctx, 1000)
		if err != nil {
			t.Fatal(err)
		}
		if d := time.Since(s0); d > longest {
			longest = d
		}
		steps++
		if done {
			break
		}
	}
	t.Logf("backfill: %d content rows queued in %d batches of 1000, %v in all, the longest batch %v",
		files, steps, time.Since(start).Round(time.Millisecond), longest.Round(time.Millisecond))
	if n := scalarCount(t, s, "SELECT COUNT(*) FROM search_texts"); n != files {
		t.Fatalf("backfill queued %d rows, want %d", n, files)
	}

	start = time.Now()
	d, err := s.SearchCheck(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("hourly check over %d files: %v (missing content rows %d)", files, time.Since(start).Round(time.Millisecond), d.TextMissing)
}
