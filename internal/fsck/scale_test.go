// SPDX-License-Identifier: AGPL-3.0-or-later

package fsck

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// TestScaleOnGeneratedTree measures a run on a generated data directory with SELFCLOUD_FSCK_SCALE blobs, each an empty
// file with a live row. It is skipped unless the variable is set, because it writes that many files. It logs the time of
// each run; the decision records the numbers.
func TestScaleOnGeneratedTree(t *testing.T) {
	n, err := strconv.Atoi(os.Getenv("SELFCLOUD_FSCK_SCALE"))
	if err != nil || n <= 0 {
		t.Skip("set SELFCLOUD_FSCK_SCALE to a blob count to measure")
	}
	f := newFixture(t)
	emptySum := sha256.Sum256(nil)
	empty := hex.EncodeToString(emptySum[:])
	gen := time.Now()
	rows := make([]string, 0, n)
	for i := 0; i < n; i++ {
		bid := id.New("blb")
		p := f.path(bid)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, bid)
	}
	t.Logf("generated %d files in %s", n, time.Since(gen).Round(time.Millisecond))
	ins := time.Now()
	if err := f.st.Write(f.ctx, func(tx *store.Tx) error {
		for _, bid := range rows {
			if err := tx.InsertBlob(f.ctx, &store.Blob{ID: bid, AccountID: f.acc, Size: 0, SHA256: empty,
				State: store.BlobLive, CreatedAt: time.Now()}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Logf("inserted %d rows in %s", n, time.Since(ins).Round(time.Millisecond))
	for _, verify := range []bool{false, true} {
		start := time.Now()
		r := f.run(Options{VerifyContent: verify})
		t.Logf("run verify=%v: %s, %d findings, scanned blob_files=%d live_blob_rows=%d",
			verify, time.Since(start).Round(time.Millisecond), len(r.Findings), r.Scanned["blob_files"],
			r.Scanned["live_blob_rows"])
		t.Logf("run verify=%v: read transactions=%d, longest single hold=%dms",
			verify, r.Snapshots, r.LongestSnapshotHoldMS)
		if len(r.Findings) != 0 {
			t.Fatalf("clean generated tree reported %d findings", len(r.Findings))
		}
	}
	// integrity_check holds its own read transaction for as long as it runs; batching does not bound it. Time it alone.
	cs, err := store.OpenCheck(f.ctx, f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	snap, err := cs.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	ic := time.Now()
	ok, problems, err := snap.IntegrityCheck(f.ctx)
	held := time.Since(ic)
	snap.Close()
	if err != nil || !ok {
		t.Fatalf("integrity_check on the generated database: ok=%v problems=%v err=%v", ok, problems, err)
	}
	t.Logf("integrity_check alone (one read transaction): %s", held.Round(time.Millisecond))
	t.Logf("peak resident memory of the process (high-water mark across all runs above): %s", peakRSS())
}

// peakRSS returns the process's high-water resident size from /proc, on Linux; elsewhere it says so.
func peakRSS() string {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return "not available here"
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "VmHWM:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "VmHWM:"))
		}
	}
	return "not reported"
}
