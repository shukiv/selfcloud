// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/jobs"
	"github.com/shukiv/selfcloud/internal/store"
)

// A chunked copy (decision 34) builds the new tree in a staging folder, in writes of bounded size, and publishes it in one
// write. Its phases are in store/copy_jobs.go: snapshot, copy, publish, and cleanup after a failure. The reservation is
// made when the copy is asked for, and released when it publishes or fails.

// Batch sizing. A batch's writer hold is measured; the next batch is halved when the last held the writer past
// copyBatchTarget, and grows when it held under half of that.
const (
	copyBatchTarget = 400 * time.Millisecond
	copyBatchMin    = 100
	copyBatchMax    = 5000
	copyBatchStart  = 500
	// copyYieldAfter is how long a chunked copy runs in one turn before it yields to the queue, so the other jobs of the
	// queue (other accounts, account deletion) are not held behind it.
	copyYieldAfter = 30 * time.Second
	// maxUnfinishedCopies is how many chunked copies an account may have queued or running at once.
	maxUnfinishedCopies = 2
	copySnapshotBatch   = 5000
	copyCleanupBatch    = 2000
)

// batchSizer chooses how many snapshot rows the next copy batch handles. A fixed sizer (tests) always gives the same
// size and ignores the holds.
type batchSizer struct {
	size  int
	fixed bool
}

func newBatchSizer(fixed int) *batchSizer {
	if fixed > 0 {
		return &batchSizer{size: fixed, fixed: true}
	}
	return &batchSizer{size: copyBatchStart}
}

// Next is the size of the next batch.
func (b *batchSizer) Next() int { return b.size }

// Observe takes the writer hold of the batch that just ran, and sizes the next one from it.
func (b *batchSizer) Observe(hold time.Duration) {
	if b.fixed {
		return
	}
	switch {
	case hold > copyBatchTarget:
		b.size = max(copyBatchMin, b.size/2)
	case hold < copyBatchTarget/2:
		b.size = min(copyBatchMax, b.size+b.size/4+1)
	}
}

// queueChunkedCopy checks the copy's plan now, so a refusal (a name, a role, the quota) is answered at once. It makes
// the staging folder, takes the reservation of the subtree's bytes against the quota, and queues the job. Nothing else
// is written before the job runs, and nothing is visible until it publishes.
func (s *Server) queueChunkedCopy(w http.ResponseWriter, r *http.Request, src *store.Node, in transferBody,
	p *auth.Principal) error {
	ctx := r.Context()
	_, bytes, err := s.store.CopyableTotals(ctx, src.AccountID, src.ID)
	if err != nil {
		return err
	}
	var job *store.Job
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		cur, err := tx.Node(ctx, src.AccountID, src.ID)
		if errors.Is(err, store.ErrNotFound) {
			return nodeNotFound()
		}
		if err != nil {
			return err
		}
		if _, err := requireNodeRole(ctx, tx, p, cur.ID, store.RoleViewer); err != nil {
			return err
		}
		_, dest, err := resolveDestination(ctx, tx, p, cur, in)
		if err != nil {
			return err
		}
		if _, err := requireNodeRole(ctx, tx, p, dest.ID, store.RoleContributor); err != nil {
			return err
		}
		plan, err := planTransfer(ctx, tx, p, cur, in, false)
		if err != nil {
			return err
		}
		keep, err := sharingKept(ctx, tx, p, plan.parent.ID)
		if err != nil {
			return err
		}
		now := store.Now()
		rootID := id.New("nod")
		stagingFolder, stagingRoot := id.New("fld"), id.New("nod")
		if err := tx.CreateStagingFolder(ctx, cur.AccountID, stagingFolder, stagingRoot, now); err != nil {
			return err
		}
		// A fairness cap: an account's unfinished chunked copies, queued or running, are at most maxUnfinishedCopies.
		// The refusal is made before anything is staged.
		unfinished, err := tx.CountUnfinishedCopies(ctx, cur.AccountID)
		if err != nil {
			return err
		}
		if unfinished >= maxUnfinishedCopies {
			return apierr.New(apierr.Conflict, "an account may have at most 2 unfinished chunked copies; wait for one to finish").
				With("reason", "too_many_copies")
		}
		// The destination user's limit is checked now, as a small copy's is: the reservation sits in the staging folder,
		// a team folder, where no user limit applies, so it would otherwise be found out only at the publish.
		dst, err := tx.QuotaStateOf(ctx, cur.AccountID, plan.folderID)
		if err != nil {
			return err
		}
		if err := s.checkUserLimit(p, cur.AccountID, dst, bytes); err != nil {
			return err
		}
		// The reservation is the whole cost. checkUsage refuses it before anything is held, and the write rolls back.
		if err := s.applyUsage(ctx, tx, p, cur.AccountID, store.FolderChange{FolderID: stagingFolder,
			Change: store.UsageChange{Reserved: bytes}}); err != nil {
			return err
		}
		body, err := json.Marshal(copyPayload{Source: cur.ID, Root: rootID, Folder: plan.folderID,
			Parent: plan.parent.ID, Name: in.Name, OnConflict: in.OnConflict, Chunked: true, Actor: actorOf(p)})
		if err != nil {
			return err
		}
		actor, err := json.Marshal(actorOf(p))
		if err != nil {
			return err
		}
		job = &store.Job{ID: id.New("job"), AccountID: cur.AccountID, Kind: jobCopyNode, Target: rootID,
			Status: store.JobQueued, Payload: string(body), CreatedAt: now, UpdatedAt: now}
		if err := tx.InsertJob(ctx, job); err != nil {
			return err
		}
		return tx.InsertCopyJob(ctx, &store.CopyJob{JobID: job.ID, AccountID: cur.AccountID,
			StagingFolderID: stagingFolder, StagingRootID: stagingRoot, RootID: rootID, SourceID: cur.ID,
			DestFolderID: plan.folderID, DestParentID: plan.parent.ID, Name: in.Name, NameKey: plan.key,
			OnConflict: in.OnConflict, KeepSharing: keep, KeepRestricted: -1, Phase: store.CopyPhaseSnapshot,
			ReservedBytes: bytes, Actor: string(actor), CreatedAt: now, UpdatedAt: now})
	})
	if err != nil {
		return err
	}
	s.jobs.Wake()
	w.Header().Set("Location", "/api/v1/jobs/"+job.ID)
	writeJSON(w, http.StatusAccepted, toJobJSON(job))
	return nil
}

// runCopyJob runs a queued copy. A copy with a copy row runs in phases; one queued before migration 0027 has none and
// runs in one write, as before.
func (s *Server) runCopyJob(ctx context.Context, job *store.Job) error {
	var pl copyPayload
	if err := json.Unmarshal([]byte(job.Payload), &pl); err != nil {
		return fmt.Errorf("copy job payload: %w", err)
	}
	if _, err := s.store.CopyJobOf(ctx, job.ID); errors.Is(err, store.ErrNotFound) {
		// A job made before chunked copies had a row is the one-write copy. A chunked job with no row either published
		// and was cleaned up (its root exists: nothing to do), or lost its state: it fails, and never falls back to a
		// one-write copy of the tree.
		if !pl.Chunked {
			return s.runCopyJobWhole(ctx, job)
		}
		// The root is in the destination only once published; while staged it is in the staging folder, and it is
		// found by its ID too, so the folder is what tells the two apart.
		if root, err := s.store.Node(ctx, job.AccountID, job.Target); err == nil && root.FolderID == pl.Folder {
			return nil
		}
		return errors.New("copy_state_lost: the copy's state is gone; nothing was copied")
	} else if err != nil {
		return err
	}
	p := &auth.Principal{Kind: auth.Kind(pl.Actor.Kind), ID: pl.Actor.ID, AccountID: job.AccountID,
		UserID: pl.Actor.UserID, Role: pl.Actor.Role, Token: pl.Actor.Token}
	// failed is the error of the phase that failed in this run, which keeps its API code when cleanup reports it. A run
	// that resumes a cleanup has only the text, which the copy row kept.
	var failed error
	for {
		cj, err := s.store.CopyJobOf(ctx, job.ID)
		if errors.Is(err, store.ErrNotFound) {
			return nil // published and cleaned up by an earlier run
		}
		if err != nil {
			return err
		}
		switch cj.Phase {
		case store.CopyPhaseSnapshot:
			err = s.copySnapshot(ctx, cj)
		case store.CopyPhaseCopy:
			err = s.copyBatches(ctx, cj, p, time.Now().Add(s.copyYield))
		case store.CopyPhasePublish:
			err = s.copyPublish(ctx, cj, p)
		case store.CopyPhaseDone:
			return s.copyCleanup(ctx, cj.JobID)
		case store.CopyPhaseCleanup:
			if err := s.copyCleanup(ctx, cj.JobID); err != nil {
				return err
			}
			if failed != nil {
				// The jobs runner stores this error's text, and callers read its code, so it keeps the code.
				return jobError(failed)
			}
			return errors.New(cj.Failure)
		default:
			return fmt.Errorf("copy phase %q is not known", cj.Phase)
		}
		if err == nil {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err() // a shutdown leaves the phase as it is; the next start resumes it
		}
		// A failure moves the copy to cleanup, which removes what it staged and releases the reservation. The
		// next pass of the loop does that, and then reports the failure.
		if errors.Is(err, jobs.ErrYield) {
			return err // not a failure: the job is queued again, and nothing is undone
		}
		failed = err
		now := store.Now()
		if werr := s.store.Write(ctx, func(tx *store.Tx) error {
			return tx.SetCopyPhase(ctx, job.ID, store.CopyPhaseCleanup, jobFailure(err), now)
		}); werr != nil {
			return werr
		}
	}
}

// jobFailure is the text a failed copy job keeps. GET /jobs shows it to every owner session and to the key.
func jobFailure(err error) string {
	return jobError(err).Error()
}

// jobError is the error a failed copy job reports, with its code. GET /jobs shows its text to every owner session and to
// the key, so a quota refusal keeps its code and drops its numbers: the numbers are for the user whose limit it was and
// the key (quota.go, "Who learns the numbers"). A refusal at the request, which is not stored, keeps its numbers.
func jobError(err error) error {
	var ae *apierr.Error
	if errors.As(err, &ae) && ae.Code == apierr.QuotaExceeded {
		return apierr.New(apierr.QuotaExceeded, "the storage limit would be passed")
	}
	return err
}

// copyStage is the fault-injection point of a stage. Production never sets copyFault.
func (s *Server) copyStage(stage string) error {
	if s.copyFault != nil {
		return s.copyFault(stage)
	}
	return nil
}

// observeHold records the writer hold of one write, for measurement.
func (s *Server) observeHold(phase string, hold time.Duration) {
	if s.copyHold != nil {
		s.copyHold(phase, hold)
	}
}

// copySnapshot records the source's subtree in the copy's snapshot. It first removes rows that an interrupted run
// left, in bounded steps. The snapshot is read outside any write, so reading a large tree holds no lock; nodes created
// or moved in after it are not in the copy (a race the copy states, not a snapshot isolation it has).
func (s *Server) copySnapshot(ctx context.Context, cj *store.CopyJob) error {
	for {
		if err := s.copyStage("snapshot"); err != nil {
			return err
		}
		var done bool
		start := time.Now()
		if err := s.store.Write(ctx, func(tx *store.Tx) error {
			var err error
			done, err = tx.ResetCopySnapshot(ctx, cj.JobID, copySnapshotBatch)
			return err
		}); err != nil {
			return err
		}
		s.observeHold("snapshot", time.Since(start))
		if done {
			break
		}
	}
	// The subtree is read in one statement, so it is one consistent view of the tree, and it is checked against
	// copy.max_nodes: a tree past the limit fails the copy, with nothing copied. The rows are then stored a page at a
	// time. Later changes to the tree are caught by the batches and the publish, which compare each row with the tree.
	if err := s.copyStage("snapshot"); err != nil {
		return err
	}
	rows, err := s.store.SubtreeRows(ctx, cj.AccountID, cj.SourceID, s.cfg.Copy.MaxNodes)
	if err != nil {
		return err
	}
	if len(rows) > s.cfg.Copy.MaxNodes {
		return s.tooManyNodes(len(rows))
	}
	for from := 0; from < len(rows); from += copySnapshotBatch {
		to := min(from+copySnapshotBatch, len(rows))
		if err := s.copyStage("snapshot"); err != nil {
			return err
		}
		start := time.Now()
		if err := s.store.Write(ctx, func(tx *store.Tx) error {
			return tx.InsertCopySnapshot(ctx, cj.JobID, from+1, rows[from:to])
		}); err != nil {
			return err
		}
		s.observeHold("snapshot", time.Since(start))
	}
	return s.store.Write(ctx, func(tx *store.Tx) error {
		return tx.SetSnapshotDone(ctx, cj.JobID, len(rows), store.Now())
	})
}

// copyGuard checks, inside a write, that the copy's caller may still act and may still read the source. It returns
// whether the copies keep restricted values: a copy must not widen who can read one (copyKeepsRestricted). A source
// with no_download, or a source the caller may not download from under the restricted rule, refuses the copy.
func (s *Server) copyGuard(ctx context.Context, tx *store.Tx, p *auth.Principal, cj *store.CopyJob) (bool, error) {
	if err := copyActorLive(ctx, tx, p, cj.AccountID); err != nil {
		return false, err
	}
	src, err := tx.Node(ctx, cj.AccountID, cj.SourceID)
	if errors.Is(err, store.ErrNotFound) {
		return false, errors.New("the source no longer exists; nothing was copied")
	}
	if err != nil {
		return false, err
	}
	_, role, err := nodeAccessWith(ctx, tx, p, src.ID)
	if err != nil {
		return false, err
	}
	if store.RoleRank(role) < store.RoleRank(store.RoleViewer) {
		return false, roleTooLow(store.RoleViewer)
	}
	restricted, err := s.copyRestricted(ctx, tx, src, role)
	if err != nil {
		return false, err
	}
	if restricted {
		return false, errDownloadRestricted()
	}
	return copyKeepsRestricted(role), nil
}

// recordOrCheckRestricted decides, at the first batch, whether the copies keep restricted values, and records it. Every
// later check (each batch and the publish) must give the same answer: a copy made with restricted values, then finished
// by a caller who no longer manages the source, would hand those values to the destination, and a copy made without
// them, then finished by a caller who does, would be incomplete. Either change fails the copy.
func recordOrCheckRestricted(ctx context.Context, tx *store.Tx, cj *store.CopyJob, keep bool) error {
	if cj.KeepRestricted == -1 {
		return tx.SetKeepRestricted(ctx, cj.JobID, keep, store.Now())
	}
	if (cj.KeepRestricted == 1) != keep {
		return apierr.New(apierr.Conflict, "the restricted-label rule of the source changed during the copy; nothing was copied")
	}
	return nil
}

// copyActorLive refuses a copy whose caller was revoked since it was asked for. The check reads the same delegation the
// one-write copy reads.
func copyActorLive(ctx context.Context, tx *store.Tx, p *auth.Principal, accountID string) error {
	d, err := tx.Delegation(ctx, accountID, callerKeyID(p), p.UserID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !callerLive(p, d, time.Now())) {
		return apierr.New(apierr.Forbidden, "the caller of this copy no longer has access; nothing was copied")
	}
	return err
}

// copyBatches copies the snapshot in batches. Each batch is one write: its guard, its rows and its cursor. A batch
// that fails is rolled back whole, and the cursor stays where the last committed batch left it.
func (s *Server) copyBatches(ctx context.Context, cj *store.CopyJob, p *auth.Principal, until time.Time) error {
	sizer := newBatchSizer(s.copyBatchRows)
	for {
		if err := s.copyStage("batch"); err != nil {
			return err
		}
		var more bool
		start := time.Now()
		err := s.store.Write(ctx, func(tx *store.Tx) error {
			cur, err := tx.CopyJobOf(ctx, cj.JobID)
			if err != nil {
				return err
			}
			keepRestricted, err := s.copyGuard(ctx, tx, p, cur)
			if err != nil {
				return err
			}
			if err := recordOrCheckRestricted(ctx, tx, cur, keepRestricted); err != nil {
				return err
			}
			_, more, err = tx.StagedCopyBatch(ctx, cur, sizer.Next(), keepRestricted, p.ID, store.Now())
			return err
		})
		hold := time.Since(start)
		s.observeHold("batch", hold)
		if err != nil {
			return err
		}
		sizer.Observe(hold)
		// The turn is used up and rows remain: the job yields, and resumes from its cursor when it is run again.
		if more && time.Now().After(until) {
			return jobs.ErrYield
		}
		if !more {
			break
		}
	}
	return s.store.Write(ctx, func(tx *store.Tx) error {
		return tx.SetCopyPhase(ctx, cj.JobID, store.CopyPhasePublish, "", store.Now())
	})
}

// copyPublish moves the staged tree into the destination in one write. The name, the role on the destination and the
// source's access are checked again, as the caller at this moment, so a change since the copy began fails it. Nothing is
// visible before this write, and nothing partial is after it: the write either happens whole or not at all.
func (s *Server) copyPublish(ctx context.Context, cj *store.CopyJob, p *auth.Principal) error {
	if err := s.copyStage("publish"); err != nil {
		return err
	}
	s.atWrite()
	start := time.Now()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		cur, err := tx.CopyJobOf(ctx, cj.JobID)
		if err != nil {
			return err
		}
		if err := copyActorLive(ctx, tx, p, cur.AccountID); err != nil {
			return err
		}
		src, err := tx.Node(ctx, cur.AccountID, cur.SourceID)
		if errors.Is(err, store.ErrNotFound) {
			return errors.New("the source no longer exists; nothing was copied")
		}
		if err != nil {
			return err
		}
		// The same guard as every batch: the caller, the source's role, no_download on the source, and the restricted-label
		// rule, which must still be the one the first batch recorded.
		keepRestricted, err := s.copyGuard(ctx, tx, p, cur)
		if err != nil {
			return err
		}
		if err := recordOrCheckRestricted(ctx, tx, cur, keepRestricted); err != nil {
			return err
		}
		// The source's structure must still be the snapshot's: a node moved, trashed or deleted since its batch fails
		// the copy. A node created since the snapshot is not in it, and is not copied.
		changed, err := tx.CopySnapshotChanged(ctx, cur)
		if err != nil {
			return err
		}
		if changed {
			return store.ErrSourceChanged
		}
		in := transferBody{Folder: cur.DestFolderID, Parent: cur.DestParentID, Name: cur.Name, OnConflict: cur.OnConflict}
		_, dest, err := resolveDestination(ctx, tx, p, src, in)
		if err != nil {
			return err
		}
		if _, err := requireNodeRole(ctx, tx, p, dest.ID, store.RoleContributor); err != nil {
			return err
		}
		plan, err := planTransfer(ctx, tx, p, src, in, false)
		if err != nil {
			return err
		}
		if err := requireReplaceRole(ctx, tx, p, plan); err != nil {
			return err
		}
		replaced, err := applyReplace(ctx, tx, plan, p.ID)
		if err != nil {
			return err
		}
		keep, err := sharingKept(ctx, tx, p, plan.parent.ID)
		if err != nil {
			return err
		}
		if keep != cur.KeepSharing {
			return apierr.New(apierr.Conflict, "the destination's sharing rule changed during the copy; nothing was copied")
		}
		now := store.Now()
		files, bytes, err := tx.StagedTotals(ctx, cur)
		if err != nil {
			return err
		}
		// The events are those of the one-write copy: one per node that is not a folder, shortcuts included. The usage
		// charge above counts files only.
		items, err := tx.StagedItems(ctx, cur)
		if err != nil {
			return err
		}
		report, err := tx.ReportFor(ctx, cur.AccountID, "file.created", int(cur.Nodes), int(items))
		if err != nil {
			return err
		}
		if report == store.ReportEachFile {
			nodes, err := tx.StagedFiles(ctx, cur, int(items))
			if err != nil {
				return err
			}
			for _, n := range nodes {
				n.FolderID = plan.folderID
				if err := tx.EmitEvent(ctx, cur.AccountID, "file.created", now, store.FileEvent(n)); err != nil {
					return err
				}
			}
		}
		// The charge is the copy's real bytes and files, and the reservation is released in the same change.
		if err := s.applyUsage(ctx, tx, p, cur.AccountID,
			store.FolderChange{FolderID: cur.StagingFolderID, Change: store.UsageChange{Reserved: -cur.ReservedBytes}},
			store.FolderChange{FolderID: plan.folderID, Change: store.UsageChange{Bytes: bytes, Files: files}}); err != nil {
			return err
		}
		if err := tx.PublishStaged(ctx, cur, plan.folderID, plan.parent.ID, plan.name, plan.key, now); err != nil {
			return err
		}
		if report == store.ReportFolder {
			root := &store.Node{ID: cur.RootID, FolderID: plan.folderID, ParentID: plan.parent.ID}
			if err := tx.EmitEvent(ctx, cur.AccountID, "file.created", now, store.FolderEvent(root)); err != nil {
				return err
			}
		}
		if src.Kind != store.KindShortcut {
			if err := noteEdit(ctx, tx, p, cur.AccountID, cur.RootID); err != nil {
				return err
			}
		}
		if replaced != nil {
			if err := tx.InsertAudit(ctx, auditAs(p, cur.AccountID, "node.trash", replaced.ID,
				map[string]any{"kind": replaced.Kind, "name": replaced.Name, "replaced_by": cur.RootID}, src.FolderID, plan.folderID)); err != nil {
				return err
			}
		}
		return tx.InsertAudit(ctx, auditAs(p, cur.AccountID, "node.copy", cur.RootID,
			map[string]any{"source": src.ID, "nodes": cur.Nodes, "bytes": bytes, "job": cur.JobID},
			src.FolderID, plan.folderID))
	})
	s.observeHold("publish", time.Since(start))
	return err
}

// copyCleanup removes what a copy staged, in bounded writes, and so releases its reservation. It runs after a failure,
// and after a publish to drop the snapshot. A repeat finds nothing left to do.
func (s *Server) copyCleanup(ctx context.Context, jobID string) error {
	for {
		if err := s.copyStage("cleanup"); err != nil {
			return err
		}
		start := time.Now()
		done, err := s.store.CleanupCopyStep(ctx, jobID, copyCleanupBatch, store.Now())
		s.observeHold("cleanup", time.Since(start))
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}
