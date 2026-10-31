// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/jobs"
	"github.com/shukiv/selfcloud/internal/store"
)

// TestTooManyUnfinishedCopiesRefused: an account has at most two chunked copies queued or running. A third is refused
// with conflict and reason too_many_copies, before anything is staged or reserved.
func TestTooManyUnfinishedCopiesRefused(t *testing.T) {
	t.Parallel()
	fx := newChunkedFixture(t)
	fx.queue(t)
	fx.queue(t)
	before := usageOf(t, fx.s, fx.key, fx.acc.ID)
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.tree.ID+"/copy", fx.key, map[string]any{"name": "third"})
	wantError(t, rec, 409, apierr.Conflict)
	if !strings.Contains(rec.Body.String(), "too_many_copies") {
		t.Errorf("body = %s, want reason too_many_copies", rec.Body.String())
	}
	if u := usageOf(t, fx.s, fx.key, fx.acc.ID); u.ReservedBytes != before.ReservedBytes {
		t.Errorf("reserved = %d after the refusal, was %d", u.ReservedBytes, before.ReservedBytes)
	}
}

// TestChunkedCopyYieldsToQueuedJobs: a chunked copy that uses its turn goes to the back of the queue, so a job queued
// behind it runs before the copy ends, and the copy then finishes from its stored state.
func TestChunkedCopyYieldsToQueuedJobs(t *testing.T) {
	t.Parallel()
	fx := newChunkedFixture(t)
	fx.s.copyBatchRows = 2 // several batches, so there is a point to yield at
	fx.s.copyYield = time.Nanosecond
	fx.queue(t)
	ctx := context.Background()
	// The other job is queued after the copy, as another tenant's or the account's own deletion would be.
	other := &store.Job{ID: id.New("job"), AccountID: fx.acc.ID, Kind: "test.mark", Status: store.JobQueued,
		Payload: "{}", CreatedAt: store.Now(), UpdatedAt: store.Now()}
	if err := fx.s.store.Write(ctx, func(tx *store.Tx) error { return tx.InsertJob(ctx, other) }); err != nil {
		t.Fatal(err)
	}
	copyStatusWhenOtherRan := ""
	fx.s.jobs.Handle("test.mark", func(ctx context.Context, job *store.Job) error {
		cj, err := fx.s.store.Job(ctx, fx.job.ID)
		if err != nil {
			return err
		}
		copyStatusWhenOtherRan = cj.Status
		return nil
	})
	if err := fx.s.jobs.RunPending(ctx); err != nil {
		t.Fatal(err)
	}
	if copyStatusWhenOtherRan == "" {
		t.Fatal("the job queued behind the copy never ran")
	}
	if copyStatusWhenOtherRan == store.JobSucceeded {
		t.Errorf("the copy had finished when the other job ran; it should have yielded first")
	}
	done, err := fx.s.store.Job(ctx, fx.job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != store.JobSucceeded {
		t.Fatalf("copy job = %+v, want succeeded after resuming", done)
	}
	if n := fx.topLevel(t); n != 2 {
		t.Errorf("top-level entries = %d, want 2 (the tree and its copy)", n)
	}
}

// Ensure the jobs package's yield is the sentinel the copy returns (a compile-time reference, so a rename shows here).
var _ = jobs.ErrYield
