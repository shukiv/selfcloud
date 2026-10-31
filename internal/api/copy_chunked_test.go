// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// chunkedFixture is an account with a folder holding bigTree (5 files of one byte, 7 nodes with the tree), and a
// copy of that tree queued as a chunked job. The copy is asked for in the folder, into the same folder.
type chunkedFixture struct {
	s     *Server
	admin string
	acc   accountJSON
	key   string
	f     folderJSON
	tree  nodeJSON
	job   jobJSON
}

func newChunkedFixture(t *testing.T) *chunkedFixture {
	t.Helper()
	s := newAuthServer(t, &mail.Recorder{})
	s.copySyncNodes = 3 // the 7-node tree is a job
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	tree := bigTree(t, s, key, f.ID)
	fx := &chunkedFixture{s: s, admin: admin, acc: acc, key: key, f: f, tree: tree}
	return fx
}

// queue asks for the copy and returns the job; nothing runs yet.
func (fx *chunkedFixture) queue(t *testing.T) jobJSON {
	t.Helper()
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.tree.ID+"/copy", fx.key, map[string]any{"name": "copy"})
	wantStatus(t, rec, 202)
	fx.job = decode[jobJSON](t, rec)
	return fx.job
}

// run runs the queued job once, as the runner does, and returns the stored job.
func (fx *chunkedFixture) run(t *testing.T) store.Job {
	t.Helper()
	if err := fx.s.jobs.RunPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	j, err := fx.s.store.Job(context.Background(), fx.job.ID)
	if err != nil {
		t.Fatal(err)
	}
	return *j
}

// topLevel counts the top-level entries of the folder, as the account key sees them.
func (fx *chunkedFixture) topLevel(t *testing.T) int {
	t.Helper()
	return len(decode[listResponse[nodeJSON]](t, call(t, fx.s, "GET", "/api/v1/folders/"+fx.f.ID+"/children", fx.key, nil)).Items)
}

// assertNothingVisible checks the state a failed copy must leave: the folder as it was, no usage charged, no
// reservation held, no copy row, and the job's root not found.
func (fx *chunkedFixture) assertNothingVisible(t *testing.T, before usageJSON) {
	t.Helper()
	if n := fx.topLevel(t); n != 1 {
		t.Errorf("top-level entries after the failed copy = %d, want the original 1", n)
	}
	u := usageOf(t, fx.s, fx.key, fx.acc.ID)
	if u.ReservedBytes != 0 {
		t.Errorf("reserved after the failed copy = %d, want 0", u.ReservedBytes)
	}
	if u.UsedBytes != before.UsedBytes || u.UsedFiles != before.UsedFiles {
		t.Errorf("usage after the failed copy = %d bytes, %d files; was %d, %d", u.UsedBytes, u.UsedFiles,
			before.UsedBytes, before.UsedFiles)
	}
	if _, err := fx.s.store.CopyJobOf(context.Background(), fx.job.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("copy row after cleanup: err = %v, want not found", err)
	}
	wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.job.Target, fx.key, nil), 404, apierr.NotFound)
}

// TestStagedCopyIsHiddenUntilPublish checks the state between the request and the publish: the reservation is held and
// shows as reserved; the tree is invisible to a listing and to a lookup of its root, which is the job's target; and it
// appears in one step, when the job publishes.
func TestStagedCopyIsHiddenUntilPublish(t *testing.T) {
	t.Parallel()
	fx := newChunkedFixture(t)
	job := fx.queue(t)
	if u := usageOf(t, fx.s, fx.key, fx.acc.ID); u.ReservedBytes != 5 || u.UsedBytes != 5 {
		t.Errorf("while queued: reserved %d, used %d; want reserved 5 (the copy's bytes) and used 5", u.ReservedBytes, u.UsedBytes)
	}
	if n := fx.topLevel(t); n != 1 {
		t.Errorf("top-level entries while queued = %d, want 1", n)
	}
	wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+job.Target, fx.key, nil), 404, apierr.NotFound)

	done := fx.run(t)
	if done.Status != store.JobSucceeded {
		t.Fatalf("job = %+v, want succeeded", done)
	}
	if n := fx.topLevel(t); n != 2 {
		t.Errorf("top-level entries after publish = %d, want 2", n)
	}
	root := decode[nodeJSON](t, call(t, fx.s, "GET", "/api/v1/nodes/"+job.Target, fx.key, nil))
	if root.Name != "copy" {
		t.Errorf("published root = %+v", root)
	}
	if u := usageOf(t, fx.s, fx.key, fx.acc.ID); u.ReservedBytes != 0 || u.UsedBytes != 10 || u.UsedFiles != 10 {
		t.Errorf("after publish: reserved %d, used %d bytes, %d files; want 0, 10, 10", u.ReservedBytes, u.UsedBytes, u.UsedFiles)
	}
}

// TestChunkedCopyFailsCleanlyAtEachStage injects a failure at each stage of the job, and checks that the copy leaves
// nothing visible, charges nothing, holds no reservation, and removes its rows.
func TestChunkedCopyFailsCleanlyAtEachStage(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"snapshot", "batch", "publish"} {
		t.Run(stage, func(t *testing.T) {
			fx := newChunkedFixture(t)
			fx.s.copyBatchRows = 2
			before := usageOf(t, fx.s, fx.key, fx.acc.ID)
			fx.s.copyFault = func(got string) error {
				if got == stage {
					return errors.New("injected at " + stage)
				}
				return nil
			}
			fx.queue(t)
			done := fx.run(t)
			if done.Status != store.JobFailed || done.Error != "injected at "+stage {
				t.Fatalf("job = %+v, want failed with the injected error", done)
			}
			fx.assertNothingVisible(t, before)
		})
	}
}

// TestCleanupFailureIsResumed injects a failure into the cleanup of a failed copy. The copy row stays, the reservation
// stays held, and a second cleanup, as a restart or the sweep would run it, finishes the job.
func TestCleanupFailureIsResumed(t *testing.T) {
	t.Parallel()
	fx := newChunkedFixture(t)
	before := usageOf(t, fx.s, fx.key, fx.acc.ID)
	fx.s.copyFault = func(stage string) error {
		if stage == "publish" {
			return errors.New("injected")
		}
		if stage == "cleanup" {
			return errors.New("cleanup interrupted")
		}
		return nil
	}
	fx.queue(t)
	if done := fx.run(t); done.Status != store.JobFailed {
		t.Fatalf("job = %+v, want failed", done)
	}
	if _, err := fx.s.store.CopyJobOf(context.Background(), fx.job.ID); err != nil {
		t.Fatalf("copy row after interrupted cleanup: %v, want kept", err)
	}
	if u := usageOf(t, fx.s, fx.key, fx.acc.ID); u.ReservedBytes != 5 {
		t.Errorf("reserved while cleanup is unfinished = %d, want the 5 still held", u.ReservedBytes)
	}
	fx.s.copyFault = nil
	if err := fx.s.copyCleanup(context.Background(), fx.job.ID); err != nil {
		t.Fatalf("resumed cleanup: %v", err)
	}
	fx.assertNothingVisible(t, before)
}

// TestRestartResumesTheBatches stops a copy in the middle of its batches, as a shutdown would, and runs it again. The
// copy finishes with each node made once.
func TestRestartResumesTheBatches(t *testing.T) {
	t.Parallel()
	fx := newChunkedFixture(t)
	fx.s.copyBatchRows = 2
	fx.queue(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	batches := 0
	fx.s.copyFault = func(stage string) error {
		if stage == "batch" {
			batches++
			if batches == 3 {
				cancel()
				return ctx.Err()
			}
		}
		return nil
	}
	stored, err := fx.s.store.Job(ctx, fx.job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.s.runCopyJob(ctx, stored); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted run = %v, want context canceled", err)
	}
	cj, err := fx.s.store.CopyJobOf(context.Background(), fx.job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cj.Phase != store.CopyPhaseCopy || cj.Cursor == 0 || cj.Nodes == 0 {
		t.Fatalf("copy after the interruption: phase %q, cursor %d, nodes %d; want copy, some progress", cj.Phase, cj.Cursor, cj.Nodes)
	}
	fx.s.copyFault = nil
	if err := fx.s.runCopyJob(context.Background(), stored); err != nil {
		t.Fatalf("resumed run: %v", err)
	}
	if n := fx.topLevel(t); n != 2 {
		t.Errorf("top-level after the resumed run = %d, want 2", n)
	}
	kids := decode[listResponse[nodeJSON]](t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.job.Target+"/children", fx.key, nil))
	if len(kids.Items) != 4 {
		t.Errorf("copied children = %d, want 4 (no node made twice)", len(kids.Items))
	}
	if u := usageOf(t, fx.s, fx.key, fx.acc.ID); u.UsedFiles != 10 || u.ReservedBytes != 0 {
		t.Errorf("usage after the resumed run = %d files, %d reserved; want 10, 0", u.UsedFiles, u.ReservedBytes)
	}
}

// TestBatchesAreBounded checks that a copy of the tree is made in batches of the size asked, and that every batch is
// a write of its own that the measurement sees.
func TestBatchesAreBounded(t *testing.T) {
	t.Parallel()
	fx := newChunkedFixture(t)
	fx.s.copyBatchRows = 2
	var holds []time.Duration
	fx.s.copyHold = func(_ string, d time.Duration) { holds = append(holds, d) }
	fx.queue(t)
	if done := fx.run(t); done.Status != store.JobSucceeded {
		t.Fatalf("job = %+v", done)
	}
	// The writes measured: one reset of the snapshot (none was left), one write of the 7 snapshot rows, four copy
	// batches of 2, 2, 2 and 1 rows, the publish, and one cleanup step that drops the snapshot. The phase changes
	// between them are not measured.
	if len(holds) != 8 {
		t.Errorf("writes measured = %d, want 8: reset, snapshot, 4 batches, publish, cleanup", len(holds))
	}
}

// TestBatchSizerFollowsTheHold checks the sizing rule of the batches with fixed inputs, so it cannot flake under load:
// a batch that held the writer past the target halves the next, one that held under half of it grows the next, and
// both stay within their bounds.
func TestBatchSizerFollowsTheHold(t *testing.T) {
	t.Parallel()
	b := newBatchSizer(0)
	if b.Next() != copyBatchStart {
		t.Fatalf("first batch = %d, want %d", b.Next(), copyBatchStart)
	}
	b.Observe(copyBatchTarget + time.Millisecond)
	if b.Next() != copyBatchStart/2 {
		t.Errorf("after a long batch = %d, want %d", b.Next(), copyBatchStart/2)
	}
	for range 20 {
		b.Observe(copyBatchTarget * 2)
	}
	if b.Next() != copyBatchMin {
		t.Errorf("after many long batches = %d, want the minimum %d", b.Next(), copyBatchMin)
	}
	for range 60 {
		b.Observe(time.Millisecond)
	}
	if b.Next() != copyBatchMax {
		t.Errorf("after many short batches = %d, want the maximum %d", b.Next(), copyBatchMax)
	}
	if f := newBatchSizer(7); f.Next() != 7 {
		t.Fatalf("fixed sizer = %d, want 7", f.Next())
	}
}

// TestQuotaRefusesBeforeWork checks that a copy the quota cannot hold is refused at the request, before any job or
// staging folder is made, and that nothing is reserved.
func TestQuotaRefusesBeforeWork(t *testing.T) {
	t.Parallel()
	fx := newChunkedFixture(t)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID, fx.admin,
		map[string]any{"quota_bytes": 8}), 200) // 5 bytes used, so 3 more: the copy needs 5
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.tree.ID+"/copy", fx.key, map[string]any{"name": "copy"})
	if rec.Code == 202 {
		t.Fatal("a copy over quota was queued")
	}
	if u := usageOf(t, fx.s, fx.key, fx.acc.ID); u.ReservedBytes != 0 || u.UsedBytes != 5 {
		t.Errorf("usage after the refusal: reserved %d, used %d; want 0 and 5", u.ReservedBytes, u.UsedBytes)
	}
	if n := fx.topLevel(t); n != 1 {
		t.Errorf("top-level after the refusal = %d, want 1", n)
	}
}

// TestStagedCopyIsIsolatedAcrossAccounts checks that a copy in one account is invisible to another, both by its root's
// ID and in usage, while it is staged.
func TestStagedCopyIsIsolatedAcrossAccounts(t *testing.T) {
	t.Parallel()
	fx := newChunkedFixture(t)
	job := fx.queue(t)
	other, otherKey := newAccount(t, fx.s, fx.admin, "Other")
	wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+job.Target, otherKey, nil), 404, apierr.NotFound)
	if u := usageOf(t, fx.s, otherKey, other.ID); u.ReservedBytes != 0 || u.UsedBytes != 0 {
		t.Errorf("another account's usage = reserved %d, used %d; want 0, 0", u.ReservedBytes, u.UsedBytes)
	}
}

// TestCopyCancelHasNoRoute states that a chunked copy cannot be cancelled: no route exists for it. Only the job's own
// route answers, and it reports the job, not a copy to stop.
func TestCopyCancelHasNoRoute(t *testing.T) {
	t.Parallel()
	fx := newChunkedFixture(t)
	fx.queue(t)
	wantError(t, call(t, fx.s, "POST", "/api/v1/jobs/"+fx.job.ID+"/cancel", fx.key, nil), 404, apierr.NotFound)
}

// TestChunkedCopyEmitsNothingBeforePublish checks the events of a chunked copy: while its batches run, the outbox gains
// no event, and at publish it gains the events the one-write copy would have written (one file.created per file).
func TestChunkedCopyEmitsNothingBeforePublish(t *testing.T) {
	t.Parallel()
	fx := newWebhookFixture(t)
	fx.create(t, nil) // an endpoint that takes file.created
	s := fx.s
	s.copySyncNodes = 3
	f := newFolder(t, s, fx.key, fx.acc.ID, "Docs")
	tree := bigTree(t, s, fx.key, f.ID)
	s.copyBatchRows = 2
	before := len(outboxOf(t, s, fx.acc.ID))

	rec := call(t, s, "POST", "/api/v1/nodes/"+tree.ID+"/copy", fx.key, map[string]any{"name": "copy"})
	wantStatus(t, rec, 202)
	job := decode[jobJSON](t, rec)
	if n := len(outboxOf(t, s, fx.acc.ID)); n != before {
		t.Fatalf("outbox after the request = %d events, was %d: a queued copy writes no event", n, before)
	}
	if err := s.jobs.RunPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if done := decode[jobJSON](t, call(t, s, "GET", "/api/v1/jobs/"+job.ID, fx.key, nil)); done.Status != "succeeded" {
		t.Fatalf("job = %+v", done)
	}
	var created int
	for _, e := range outboxOf(t, s, fx.acc.ID)[before:] {
		if e.Type == "file.created" {
			created++
		}
	}
	if created != 5 {
		t.Errorf("file.created events at publish = %d, want one per file (5)", created)
	}
}

// TestNameTakenAtPublishFollowsOnConflict checks the name rule at publish. The name is not held from the request: a file
// that takes it while the copy is staged is met at publish. With on_conflict rename the copy is renamed; with the default
// fail the job fails and nothing is visible.
func TestNameTakenAtPublishFollowsOnConflict(t *testing.T) {
	t.Parallel()
	fx := newChunkedFixture(t)
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.tree.ID+"/copy", fx.key, map[string]any{"name": "copy", "on_conflict": "rename"})
	wantStatus(t, rec, 202)
	fx.job = decode[jobJSON](t, rec)
	putNode(t, fx.s, fx.key, fx.f.ID, "/copy", "taken")
	if done := fx.run(t); done.Status != store.JobSucceeded {
		t.Fatalf("rename job = %+v, want succeeded", done)
	}
	renamed := decode[nodeJSON](t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.job.Target, fx.key, nil))
	if renamed.Name != "copy (1)" {
		t.Errorf("published name = %q, want \"copy (1)\"", renamed.Name)
	}

	rec = call(t, fx.s, "POST", "/api/v1/nodes/"+fx.tree.ID+"/copy", fx.key, map[string]any{"name": "other"})
	wantStatus(t, rec, 202)
	fx.job = decode[jobJSON](t, rec)
	putNode(t, fx.s, fx.key, fx.f.ID, "/other", "taken")
	before := usageOf(t, fx.s, fx.key, fx.acc.ID) // the taken name is a file of its own: count it
	if done := fx.run(t); done.Status != store.JobFailed {
		t.Fatalf("fail job = %+v, want failed on the taken name", done)
	}
	if u := usageOf(t, fx.s, fx.key, fx.acc.ID); u.ReservedBytes != 0 || u.UsedBytes != before.UsedBytes {
		t.Errorf("after the refused publish: reserved %d, used %d; want 0 and %d", u.ReservedBytes, u.UsedBytes, before.UsedBytes)
	}
}

// TestSourceRemovedDuringCopyFailsNothingVisible checks that a copy whose source is trashed while it is staged fails at
// publish, and that nothing it staged becomes visible.
func TestSourceRemovedDuringCopyFailsNothingVisible(t *testing.T) {
	t.Parallel()
	fx := newChunkedFixture(t)
	before := usageOf(t, fx.s, fx.key, fx.acc.ID)
	fx.queue(t)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.tree.ID, fx.key, nil), 204)
	if done := fx.run(t); done.Status != store.JobFailed {
		t.Fatalf("job = %+v, want failed on the removed source", done)
	}
	// The source is in the trash, so the folder lists none of the tree, and the copy's root is not found.
	if n := fx.topLevel(t); n != 0 {
		t.Errorf("visible entries after the failed copy = %d, want 0 (the source is trashed)", n)
	}
	if u := usageOf(t, fx.s, fx.key, fx.acc.ID); u.ReservedBytes != 0 || u.UsedBytes != before.UsedBytes {
		t.Errorf("usage after the failed copy = reserved %d, used %d; want 0 and %d", u.ReservedBytes, u.UsedBytes, before.UsedBytes)
	}
	wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.job.Target, fx.key, nil), 404, apierr.NotFound)
}
