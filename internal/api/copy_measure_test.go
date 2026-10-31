// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// TestMeasureChunkedCopyAt100k is the measurement of a full chunked copy at the default copy.max_nodes of 100,000, with
// a text label on every node. It runs only when SELFCLOUD_MEASURE_COPY is set, because it seeds 100,000 nodes and their
// labels. It reports the longest single write hold, the total time, and the worst wait of a small write made every
// 100 ms while the copy runs. The database is in TMPDIR (set it to choose the disk).
func TestMeasureChunkedCopyAt100k(t *testing.T) {
	if os.Getenv("SELFCLOUD_MEASURE_COPY") == "" {
		t.Skip("set SELFCLOUD_MEASURE_COPY=1 to run the 100,000-node measurement")
	}
	nodes := 100000
	if v := os.Getenv("SELFCLOUD_MEASURE_NODES"); v != "" {
		if _, err := fmt.Sscan(v, &nodes); err != nil {
			t.Fatal(err)
		}
	}
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.Copy.MaxNodes = nodes })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	root, err := s.store.RootNode(context.Background(), acc.ID, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	seedTree(t, s, acc.ID, f.ID, root.ID, nodes)

	type phaseHold struct {
		phase string
		d     time.Duration
	}
	var holds []phaseHold
	s.copyHold = func(phase string, d time.Duration) { holds = append(holds, phaseHold{phase, d}) }
	src := measureSrcID
	rec := call(t, s, "POST", "/api/v1/nodes/"+src+"/copy", key, map[string]any{"name": "copy"})
	wantStatus(t, rec, 202)
	job := decode[jobJSON](t, rec)

	// A small write every 100 ms, measured from the call until it returns: its wait for the writer.
	ctx := context.Background()
	var mu sync.Mutex
	var waits []time.Duration
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				t0 := time.Now()
				_ = s.store.Write(ctx, func(tx *store.Tx) error { return nil })
				mu.Lock()
				waits = append(waits, time.Since(t0))
				mu.Unlock()
			}
		}
	}()

	start := time.Now()
	if err := s.jobs.RunPending(ctx); err != nil {
		t.Fatal(err)
	}
	total := time.Since(start)
	close(stop)
	wg.Wait()

	done := decode[jobJSON](t, call(t, s, "GET", "/api/v1/jobs/"+job.ID, key, nil))
	if done.Status != "succeeded" {
		t.Fatalf("job = %+v", done)
	}
	// The longest write of each phase, and the overall longest, one line each for the runs to be compared.
	longest := map[string]time.Duration{}
	count := map[string]int{}
	var overall time.Duration
	for _, h := range holds {
		count[h.phase]++
		if h.d > longest[h.phase] {
			longest[h.phase] = h.d
		}
		overall = max(overall, h.d)
	}
	sort.Slice(waits, func(i, j int) bool { return waits[i] > waits[j] })
	t.Logf("copy of %d nodes with a label on each: total %v, writes %d", nodes, total, len(holds))
	for _, p := range []string{"snapshot", "batch", "publish", "cleanup"} {
		fmt.Fprintf(os.Stderr, "MEASURE nodes=%d phase=%s writes=%d longest=%v\n", nodes, p, count[p], longest[p])
	}
	fmt.Fprintf(os.Stderr, "MEASURE nodes=%d overall_longest=%v total=%v small_writes=%d worst_wait=%v\n",
		nodes, overall, total, len(waits), waits[0])
}

// measureSrcID is the source folder of the measurement: a valid node ID, so the API takes it as the copy's source.
var measureSrcID = id.Derive("nod", "measure-src")

// seedTree writes a source folder of n nodes under parent: 100 directories holding the files, each file with a text
// label. It writes the rows straight to the database, as the copy reads them.
func seedTree(t *testing.T, s *Server, accountID, folderID, parentID string, n int) {
	t.Helper()
	db, err := sql.Open("sqlite", s.cfg.Store.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	must := func(q string, args ...any) {
		if _, err := tx.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	const dirs = 100
	fieldID := id.New("lbf")
	must(`INSERT INTO label_fields (id, account_id, name, name_key, type, created_at, updated_at)
		VALUES (?, ?, 'Note', 'note', 'text', 0, 0)`, fieldID, accountID)
	must(`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
		VALUES (?, ?, ?, ?, 'src', 'src', 'dir', 0, 0)`, measureSrcID, accountID, folderID, parentID)
	perDir := (n - 1 - dirs) / dirs
	for d := 0; d < dirs; d++ {
		dir := fmt.Sprintf("nod_seed_d_%03d", d)
		must(`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
			VALUES (?, ?, ?, ?, ?, ?, 'dir', 0, 0)`, dir, accountID, folderID, measureSrcID, dir, dir)
		for i := 0; i < perDir; i++ {
			fid := fmt.Sprintf("nod_seed_f_%03d_%05d", d, i)
			name := fmt.Sprintf("file-%03d-%05d.txt", d, i)
			must(`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, size, etag, created_at, modified_at)
				VALUES (?, ?, ?, ?, ?, ?, 'file', 1, 'e', 0, 0)`, fid, accountID, folderID, dir, name, name)
			must(`INSERT INTO node_labels (node_id, field_id, account_id, value_text, set_at) VALUES (?, ?, ?, 'a remark', 0)`,
				fid, fieldID, accountID)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
