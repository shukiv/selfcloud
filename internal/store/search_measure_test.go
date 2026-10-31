// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestMeasureSearchBackfillAt1M is the backfill of an upgrade at a million nodes. It runs only when SELFCLOUD_MEASURE_SEARCH
// is set, because it seeds the nodes with no search rows, as an upgrade finds them, and then indexes them the way the search
// job does: batches of SearchBatch, each its own write. It reports the longest write, the total, and the growth of the
// database. Run it under nice, with TMPDIR on the disk to measure (SELFCLOUD_MEASURE_NODES sets the count).
func TestMeasureSearchBackfillAt1M(t *testing.T) {
	if os.Getenv("SELFCLOUD_MEASURE_SEARCH") == "" {
		t.Skip("set SELFCLOUD_MEASURE_SEARCH=1 to run the 1,000,000-node backfill measurement")
	}
	nodes := 1_000_000
	if v := os.Getenv("SELFCLOUD_MEASURE_NODES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			t.Fatal(err)
		}
		nodes = n
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	path := filepath.Join(t.TempDir(), "backfill.db")
	s, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	const accounts, foldersPer = 10, 5
	seedStart := time.Now()
	for a := range accounts {
		acc := fmt.Sprintf("acc_m%02d", a)
		if _, err := s.w.ExecContext(ctx, `INSERT INTO accounts (id, name, status, created_at, updated_at)
			VALUES (?, ?, 'active', 0, 0)`, acc, acc); err != nil {
			t.Fatal(err)
		}
		for f := range foldersPer {
			fld := fmt.Sprintf("fld_m%02d_%d", a, f)
			if _, err := s.w.ExecContext(ctx, `INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at)
				VALUES (?, ?, ?, ?, 0, 0)`, fld, acc, fld, fld); err != nil {
				t.Fatal(err)
			}
			if _, err := s.w.ExecContext(ctx, `INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
				VALUES (?, ?, ?, NULL, '', '', 'dir', 0, 0)`, "nod_root_"+fld, acc, fld); err != nil {
				t.Fatal(err)
			}
		}
	}
	perAcc := nodes / accounts
	const chunk = 20_000
	for a := range accounts {
		acc := fmt.Sprintf("acc_m%02d", a)
		for i := 0; i < perAcc; i += chunk {
			end := min(i+chunk, perAcc)
			if err := s.Write(ctx, func(tx *Tx) error {
				for j := i; j < end; j++ {
					fld := fmt.Sprintf("fld_m%02d_%d", a, j%foldersPer)
					name := fmt.Sprintf("report %07d word%05d.pdf", j, (j*7919)%20000)
					if _, err := tx.q.ExecContext(ctx, `INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key,
						kind, size, created_at, modified_at) VALUES (?, ?, ?, ?, ?, ?, 'file', 10, 0, 0)`,
						fmt.Sprintf("nod_m%02d_%07d", a, j), acc, fld, "nod_root_"+fld, name, name); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Logf("seeded %d nodes in %s; database %s", perAcc*accounts, time.Since(seedStart).Round(time.Millisecond), sizeOf(t, path))

	// The upgrade queues every folder (migration 0032 does this for the folders that exist at upgrade).
	if err := s.SearchRebuild(ctx); err != nil {
		t.Fatal(err)
	}
	before := sizeOf(t, path)
	var worst, total time.Duration
	var steps int
	runStart := time.Now()
	for {
		start := time.Now()
		done, err := s.SearchIndexStep(ctx, SearchBatch)
		d := time.Since(start)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
		steps++
		total += d
		worst = max(worst, d)
	}
	wall := time.Since(runStart)
	n, w, g := searchCounts(t, s)
	t.Logf("backfill: %d batches of %d; longest write %s; mean write %s; wall %s",
		steps, SearchBatch, worst.Round(time.Millisecond), (total / time.Duration(max(steps, 1))).Round(time.Millisecond),
		wall.Round(time.Second))
	t.Logf("rows: names %d, words %d, trigram %d (nodes seeded %d)", n, w, g, perAcc*accounts)
	t.Logf("database before backfill %s, after %s", before, sizeOf(t, path))
	if n != perAcc*accounts || w != n || g != n {
		t.Fatalf("index incomplete: names %d words %d trigram %d", n, w, g)
	}
	if err := s.SearchRepair(ctx); err != nil {
		t.Fatal(err)
	}
	d, err := s.SearchCheck(ctx)
	if err != nil || d.Missing != 0 || d.Extra != 0 || d.FTSMismatch {
		t.Fatalf("check after backfill = %+v, %v; want clean", d, err)
	}
}

// sizeOf reports the size of the database file and its write-ahead log, in MiB.
func sizeOf(t *testing.T, path string) string {
	t.Helper()
	var total int64
	for _, p := range []string{path, path + "-wal"} {
		if fi, err := os.Stat(p); err == nil {
			total += fi.Size()
		}
	}
	return fmt.Sprintf("%.1f MiB", float64(total)/(1<<20))
}
