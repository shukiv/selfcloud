// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// TestMeasureSearchBatchesAfterCopy measures the search index of a real publish of a chunked copy, with both full-text
// indexes on. The copy's publish writes no search rows: it queues the folder. The rows are then made in batches of
// store.SearchBatch, each its own write. The test copies a tree of SELFCLOUD_MEASURE_NODES nodes (50,000 by default, the
// default copy.max_nodes), reports the publish's hold, and then times every batch of the folder's index (rebuilt, as after
// an upgrade), reporting the longest write. It runs only when SELFCLOUD_MEASURE_SEARCH_COPY is set.
func TestMeasureSearchBatchesAfterCopy(t *testing.T) {
	if os.Getenv("SELFCLOUD_MEASURE_SEARCH_COPY") == "" {
		t.Skip("set SELFCLOUD_MEASURE_SEARCH_COPY=1 to run the copy and batch measurement")
	}
	nodes := 50000
	if v := os.Getenv("SELFCLOUD_MEASURE_NODES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			t.Fatal(err)
		}
		nodes = n
	}
	ctx := context.Background()
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.Copy.MaxNodes = nodes })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	root, err := s.store.RootNode(ctx, acc.ID, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	seedTree(t, s, acc.ID, f.ID, root.ID, nodes)

	var publish []time.Duration
	s.copyHold = func(phase string, d time.Duration) {
		if phase == "publish" {
			publish = append(publish, d)
		}
	}
	rec := call(t, s, "POST", "/api/v1/nodes/"+measureSrcID+"/copy", key, map[string]any{"name": "copy"})
	wantStatus(t, rec, http.StatusAccepted)
	copyStart := time.Now()
	// The copy job yields between its batches, so running the queue to empty finishes the copy. The search job the publish
	// queued runs after it, and is then empty.
	if err := s.jobs.RunPending(ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("copy of %d nodes done in %s; publish holds %v", nodes, time.Since(copyStart).Round(time.Millisecond), publish)

	if err := s.store.SearchRebuild(ctx); err != nil {
		t.Fatal(err)
	}
	var worst, total time.Duration
	var steps int
	batchStart := time.Now()
	for {
		start := time.Now()
		done, err := s.store.SearchIndexStep(ctx, store.SearchBatch)
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
	t.Logf("index of the folder: %d batches of %d; longest write %s; mean %s; wall %s", steps, store.SearchBatch,
		worst.Round(time.Millisecond), (total / time.Duration(max(steps, 1))).Round(time.Millisecond),
		time.Since(batchStart).Round(time.Millisecond))
	d, err := s.store.SearchCheck(ctx)
	if err != nil || d != (store.SearchDrift{}) {
		t.Fatalf("check after the batches = %+v, %v; want clean", d, err)
	}
}
