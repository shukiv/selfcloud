// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/blob"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/imports"
	"github.com/shukiv/selfcloud/internal/jobs"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/webhook"
)

// An import (decision 92) runs as a job of kind jobImport, in a pool of its own (imports.max_concurrent_jobs). It moves
// through the phases of store.ImportPhase*: scan reads the archive's headers and checks them against the caps; stage
// writes each entry into a staging folder, one entry per write, and records the entry's item row in the same write;
// publish moves the whole staged tree into the target in one write, with the name clashes settled and the audit entries
// and the completion event written; done removes the archive. A failure or a cancel moves the import to cleanup, which
// removes what was staged in bounded writes.
//
// A run stops at a checkpoint when the import was paused or cancelled, and it yields its turn after importYieldAfter.
// Either way the import keeps its phase and its cursor, so the next run resumes after the last finished entry. An entry
// that was being written when a run stopped is written again from its first byte: its staged bytes are never kept.

const (
	// importPieceBytes is how much of an entry is copied between two checks of the import's state.
	importPieceBytes = 1 << 20
	// importCleanupBatch is how many staged nodes one cleanup write removes.
	importCleanupBatch = 2000
	// importYieldAfter is how long one run stages entries before it yields its turn to the other jobs of the pool.
	importYieldAfter = 30 * time.Second
	// importWindowMax caps a turn's window (importWindow). A deep .tar.gz has a long reach, and an uncapped window would hold
	// one of the pool's workers for many minutes (pool fairness).
	importWindowMax = 10 * time.Minute
)

// errImportStopped means the import was paused or cancelled while a run worked on it. Nothing the run did after the
// change is kept: each write checks the state first.
var errImportStopped = errors.New("the import was paused or cancelled")

// importPause stops an import for the owner: a quota or the disk is full. It is a pause, not a failure, so the import
// keeps its staged entries and waits for a resume.
type importPause struct{ reason string }

func (p *importPause) Error() string { return "the import paused: " + p.reason }

// importPrincipal is the caller an import runs for, rebuilt from the actor the import recorded when it was started.
func importPrincipal(im *store.Import) (*auth.Principal, error) {
	var a actorPayload
	if err := json.Unmarshal([]byte(im.Actor), &a); err != nil {
		return nil, fmt.Errorf("import actor: %w", err)
	}
	return jobPrincipal(a, im.AccountID), nil
}

// importCaps are the archive caps of the settings, with the upload's file size cap.
func (s *Server) importCaps() imports.Caps {
	a := s.cfg.Imports.Archive
	return imports.Caps{MaxEntries: a.MaxEntries, MaxDepth: a.MaxDepth, MaxRatio: int64(a.MaxRatio),
		MaxFile: int64(s.settings().Upload.MaxFileSize), MaxTotal: int64(a.MaxTotalBytes), MaxPathBytes: int64(a.MaxPathBytes)}
}

// importFaultAt is the fault point of a stage of an import, for the tests. Production never sets importFault.
func (s *Server) importFaultAt(stage string) error {
	if s.importFault != nil {
		return s.importFault(stage)
	}
	return nil
}

// runImport runs an import job. The job is safe to run again: the import's own state says where it is.
func (s *Server) runImport(ctx context.Context, job *store.Job) error {
	imp, err := s.store.Import(ctx, job.Target)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	p, err := importPrincipal(imp)
	if err != nil {
		return err
	}
	cur, err := s.importClaim(ctx, imp.ID, job.ID)
	if err != nil {
		return err
	}
	if cur.State == store.ImportPaused {
		return s.importParked(ctx, cur.ID)
	}
	var failure error
	if cur.State == store.ImportFailed {
		// The stored failure is the code; a record from before that carries text, so it is reported as the code too.
		failure = importCodeError(cur.ErrorCode)
	}
	for {
		cur, err := s.store.Import(ctx, imp.ID)
		if err != nil {
			return err
		}
		switch cur.Phase {
		case store.ImportPhaseScan:
			err = s.importScan(ctx, cur.ID, p)
		case store.ImportPhaseStage:
			err = s.importStage(ctx, cur.ID, p, s.importYield)
		case store.ImportPhasePublish:
			err = s.importPublish(ctx, cur.ID, p)
		case store.ImportPhaseCleanup:
			return s.importCleanup(ctx, cur.ID, p, failure)
		case store.ImportPhaseDone:
			if err := s.importRemoveArchive(ctx, cur.ID); err != nil {
				return err
			}
			return failure
		default:
			return fmt.Errorf("import phase %q is not known", cur.Phase)
		}
		if err == nil {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err() // a shutdown leaves the phase as it is; the next start resumes it
		}
		if errors.Is(err, jobs.ErrYield) {
			return err
		}
		if errors.Is(err, errImportStopped) {
			done, err := s.importStoppedEnd(ctx, cur.ID)
			if err != nil || done {
				return err
			}
			continue // cancelled: the cleanup phase runs next
		}
		var pause *importPause
		if errors.As(err, &pause) {
			if err := s.importPauseNow(ctx, cur.ID, pause.reason); err != nil {
				return err
			}
			return nil
		}
		// Any other error fails the import: its staged entries are removed in the cleanup phase, and the job reports it.
		failure = s.importJobFailure(cur.ID, err)
		if err := s.importFail(ctx, cur.ID, p, err); err != nil {
			return err
		}
	}
}

// importJobFailure is the error a failed import's job reports: the API code only. The cause's text, which can hold a
// server path or a store error, goes to the server log and not to the job record that the user can read.
func (s *Server) importJobFailure(impID string, cause error) error {
	code := string(apierr.Internal)
	var ae *apierr.Error
	if errors.As(cause, &ae) {
		code = string(ae.Code)
	}
	s.log.Warn("import failed", "import", impID, "error", cause)
	return importCodeError(code)
}

// importJobError is the error an import job reports: a failure code and nothing else. A job error that is not one of
// these is replaced by runImportJob, so no raw error text reaches jobs.error.
type importJobError string

func (e importJobError) Error() string { return string(e) }

// importCodeError is the job error of a failure code; an empty code is internal.
func importCodeError(code string) error {
	if code == "" {
		code = string(apierr.Internal)
	}
	return importJobError(code)
}

// runImportJob is the handler of an import job. A run's error reaches the job only as its code: a store error, a
// panic or any other text is logged here and reported as internal. A shutdown and a yield pass through unchanged.
func (s *Server) runImportJob(ctx context.Context, job *store.Job) (err error) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("import job panicked", "job", job.ID, "panic", r)
			err = importCodeError("")
		}
	}()
	err = s.runImport(ctx, job)
	if err == nil || errors.Is(err, jobs.ErrYield) || ctx.Err() != nil {
		return err
	}
	// The import was removed while the job ran (its target went, and the cascade took the row): there is nothing left
	// to fail, so the job ends quietly.
	if _, gone := s.store.Import(ctx, job.Target); errors.Is(gone, store.ErrNotFound) {
		return nil
	}
	var code importJobError
	if errors.As(err, &code) {
		return err
	}
	s.log.Warn("import job failed", "job", job.ID, "error", err)
	return importCodeError("")
}

// importClaim makes this run the one of the import: a queued import starts running under this job, a running one (left
// by a restart or a yield) is taken over. It returns the import as it is after the claim.
func (s *Server) importClaim(ctx context.Context, impID, jobID string) (*store.Import, error) {
	var out *store.Import
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		cur, err := tx.Import(ctx, impID)
		if err != nil {
			return err
		}
		if cur.State == store.ImportQueued || cur.State == store.ImportRunning {
			cur.State, cur.JobID = store.ImportRunning, jobID
			if err := tx.SaveImport(ctx, cur, store.Now()); err != nil {
				return err
			}
		}
		out = cur
		return nil
	})
	return out, err
}

// importParked ends a run that found its import paused. The write decides: an import still paused ends the job; one a
// resume has queued again yields, so the job runs again from its state, and no second job is needed.
func (s *Server) importParked(ctx context.Context, impID string) error {
	var next error
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		cur, err := tx.Import(ctx, impID)
		if err != nil {
			return err
		}
		switch cur.State {
		case store.ImportPaused:
			next = nil
		case store.ImportQueued, store.ImportRunning:
			next = jobs.ErrYield
		default:
			next = errImportStopped // cancelled: the cleanup phase runs in the next pass
		}
		return nil
	})
	if err != nil {
		return err
	}
	if errors.Is(next, errImportStopped) {
		return s.importCleanupAfterCancel(ctx, impID)
	}
	return next
}

// importStoppedEnd is the end of a run that a pause or a cancel stopped. It reports done when the run should end the job,
// and otherwise the loop goes on, into the cleanup phase.
func (s *Server) importStoppedEnd(ctx context.Context, impID string) (bool, error) {
	cur, err := s.store.Import(ctx, impID)
	if err != nil {
		return false, err
	}
	switch cur.State {
	case store.ImportPaused:
		return true, s.importParked(ctx, impID)
	case store.ImportCancelled:
		return false, nil
	}
	return true, jobs.ErrYield // resumed while this run stopped: run again from the state
}

// importCleanupAfterCancel runs the cleanup of a cancelled import from a parked run, which has no failure to report.
func (s *Server) importCleanupAfterCancel(ctx context.Context, impID string) error {
	cur, err := s.store.Import(ctx, impID)
	if err != nil {
		return err
	}
	p, err := importPrincipal(cur)
	if err != nil {
		return err
	}
	return s.importCleanup(ctx, impID, p, nil)
}

// importScan reads the archive's headers and checks them against the caps and the quota. A refusal fails the import.
func (s *Server) importScan(ctx context.Context, impID string, p *auth.Principal) error {
	if err := s.importFaultAt("scan"); err != nil {
		return err
	}
	cur, err := s.store.Import(ctx, impID)
	if err != nil {
		return err
	}
	f, size, err := s.openImportArchive(cur)
	if err != nil {
		return err
	}
	defer f.Close()
	sum, err := imports.Scan(f, size, s.importCaps())
	if err != nil {
		return err
	}
	return s.store.Write(ctx, func(tx *store.Tx) error {
		im, err := tx.Import(ctx, impID)
		if err != nil {
			return err
		}
		if im.State != store.ImportRunning {
			return errImportStopped
		}
		if err := s.checkUsage(ctx, tx, p, im.AccountID, store.FolderChange{FolderID: im.FolderID,
			Change: store.UsageChange{Bytes: sum.Bytes, Files: int64(sum.Files)}}); err != nil {
			return err
		}
		// The user's other unfinished imports have not all been staged: what they still have to stage is held too, so
		// several imports cannot each take the whole headroom.
		pending, err := tx.OtherUnfinishedBytes(ctx, im.AccountID, im.UserID, im.ID)
		if err != nil {
			return err
		}
		if pending > 0 {
			acc, err := tx.Account(ctx, im.AccountID)
			if err != nil {
				return err
			}
			if err := s.checkQuota(p, im.AccountID, acc, store.UsageChange{Bytes: sum.Bytes + pending}); err != nil {
				return err
			}
		}
		im.ArchiveFormat = string(sum.Format)
		im.Entries = int64(sum.Entries)
		im.DeclaredBytes = sum.Bytes
		im.Phase = store.ImportPhaseStage
		return tx.SaveImport(ctx, im, store.Now())
	})
}

// openImportArchive opens the archive of an import for reading, with its size.
func (s *Server) openImportArchive(im *store.Import) (*os.File, int64, error) {
	f, err := s.blobs.Open(im.AccountID, im.ArchiveBlobID)
	if err != nil {
		return nil, 0, apierr.Wrap(apierr.Internal, "the archive of the import cannot be read", err)
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, apierr.Wrap(apierr.Internal, "the archive of the import cannot be read", err)
	}
	return f, st.Size(), nil
}

// importWindow is how long a turn may stage entries after it reaches its first new entry: base, or twice the time the walk
// took to reach that entry if that is longer, and never more than importWindowMax. A gzipped tar is read from its start to
// reach an entry, so the reach is the cost of a re-read; below the cap the window keeps that re-read to about a third of a
// turn. Above the cap the re-read's share of a turn rises, at most about half at a 10 minute reach.
func importWindow(reach, base time.Duration) time.Duration {
	w := 2 * reach
	if w < base {
		w = base
	}
	if w > importWindowMax {
		w = importWindowMax
	}
	return w
}

// importTime is the clock of a turn's window: the wall clock, unless a test sets importNow.
func (s *Server) importTime() time.Time {
	if s.importNow != nil {
		return s.importNow()
	}
	return time.Now()
}

// importStage stages the entries of the archive after its cursor. A turn stages at least one new entry before it may
// yield, so every turn makes progress whatever a re-read costs; its window starts when the walk reaches the first new
// entry, and lasts importWindow(reach, base). The pause and cancel checkpoints stay as they were.
func (s *Server) importStage(ctx context.Context, impID string, p *auth.Principal, base time.Duration) error {
	turnStart := s.importTime()
	if err := s.importFaultAt("stage"); err != nil {
		return err
	}
	cur, err := s.store.Import(ctx, impID)
	if err != nil {
		return err
	}
	f, size, err := s.openImportArchive(cur)
	if err != nil {
		return err
	}
	defer f.Close()
	start := cur.Cursor
	var until time.Time
	staged := 0
	err = imports.Walk(ctx, f, size, imports.Format(cur.ArchiveFormat), int(cur.Cursor), func(e imports.Entry, body io.Reader) error {
		if int64(e.Index) > start {
			if until.IsZero() {
				first := s.importTime()
				until = first.Add(importWindow(first.Sub(turnStart), base))
			}
			if staged > 0 && s.importTime().After(until) {
				return jobs.ErrYield
			}
			staged++
		}
		if err := s.importFaultAt("entry"); err != nil {
			return err
		}
		if e.Dir {
			return s.importDirEntry(ctx, impID, p, e)
		}
		return s.importFileEntry(ctx, impID, p, e, body)
	})
	if err != nil {
		return err
	}
	return s.store.Write(ctx, func(tx *store.Tx) error {
		im, err := tx.Import(ctx, impID)
		if err != nil {
			return err
		}
		if im.State != store.ImportRunning {
			return errImportStopped
		}
		im.Phase = store.ImportPhasePublish
		return tx.SaveImport(ctx, im, store.Now())
	})
}

// importDirEntry writes one folder of the archive into the staging folder, or reuses the one an earlier import made.
func (s *Server) importDirEntry(ctx context.Context, impID string, p *auth.Principal, e imports.Entry) error {
	return s.store.Write(ctx, func(tx *store.Tx) error {
		im, err := tx.Import(ctx, impID)
		if err != nil {
			return err
		}
		if im.State != store.ImportRunning {
			return errImportStopped
		}
		parent, err := s.importParent(ctx, tx, im, p, e.Names[:len(e.Names)-1])
		if err != nil {
			return err
		}
		have, err := tx.ImportDirNode(ctx, im.ID, e.Key)
		if err != nil {
			return err
		}
		if have == "" {
			if _, err := s.importMakeDir(ctx, tx, im, p, parent, e.Names[len(e.Names)-1], e.Key); err != nil {
				return err
			}
		}
		im.Cursor = int64(e.Index)
		return tx.SaveImport(ctx, im, store.Now())
	})
}

// importParent returns the node that holds an entry whose folders are names: the staging root for a top-level entry, else
// the folder that the archive names, which is made when the archive does not list it. It runs inside the caller's write.
func (s *Server) importParent(ctx context.Context, tx *store.Tx, im *store.Import, p *auth.Principal, names []imports.Name) (string, error) {
	parent := im.StagingRootID
	for i := range names {
		key := importPathKey(names[:i+1])
		have, err := tx.ImportDirNode(ctx, im.ID, key)
		if err != nil {
			return "", err
		}
		if have == "" {
			if have, err = s.importMakeDir(ctx, tx, im, p, parent, names[i], key); err != nil {
				return "", err
			}
		}
		parent = have
	}
	return parent, nil
}

// importPathKey joins the clash keys of names with "/", which is the path an entry is known by in the import.
func importPathKey(names []imports.Name) string {
	keys := make([]string, len(names))
	for i, n := range names {
		keys[i] = n.Key
	}
	return strings.Join(keys, "/")
}

// importSourceKey is the key an archive entry is known by in import_sources. It names the archive, the user, the target
// folder and the target directory, so only the same archive into the same place is deduplicated: another user, or the
// same archive into another folder or parent, imports the entries again and discloses nothing.
func importSourceKey(im *store.Import, key string) string {
	return strings.Join([]string{im.ArchiveSHA256, im.UserID, im.FolderID, im.ParentID, key}, "/")
}

// importMakeDir makes the folder name under parent, as an item of the import, and returns its node. A folder that an
// earlier import of the same archive made, still in the target folder under the target directory, is reused: its
// entries are added to it, and the item says it was skipped.
func (s *Server) importMakeDir(ctx context.Context, tx *store.Tx, im *store.Import, p *auth.Principal,
	parent string, name imports.Name, key string) (string, error) {
	now := store.Now()
	src := importSourceKey(im, key)
	have, err := tx.ImportSourceNode(ctx, im.AccountID, im.FolderID, store.SourceArchive, src)
	if err != nil {
		return "", err
	}
	// A reused directory gets a placeholder in the staging folder, not the live directory: its entries are staged under the
	// placeholder, so no staged entry ever has a live parent, where it would clash with itself at publish.
	// The placeholder's entries are merged into the live directory at publish. The live directory is reused only when it
	// is in the target folder under the directory the parent stands for (the target directory, or a reused parent's live
	// directory).
	reuses := ""
	if have != "" {
		n, err := tx.Node(ctx, im.AccountID, have)
		if err != nil {
			return "", err
		}
		liveParent := im.ParentID
		if parent != im.StagingRootID {
			if liveParent, err = tx.ImportReuses(ctx, im.ID, parent); err != nil {
				return "", err
			}
		}
		if liveParent != "" && n.Kind == store.KindDir && n.FolderID == im.FolderID && n.ParentID == liveParent {
			reuses = have
		}
	}
	n := &store.Node{ID: id.New("nod"), AccountID: im.AccountID, FolderID: im.StagingFolderID, ParentID: parent,
		Name: name.Name, NameKey: name.Key, Kind: store.KindDir, ETag: newETag(), CreatedAt: now, ModifiedAt: now,
		CreatedBy: p.ID, ModifiedBy: p.ID}
	if err := tx.InsertNodeNoIndex(ctx, n); err != nil {
		return "", err
	}
	state := "done"
	if reuses != "" {
		state = "skipped"
		im.Skipped++
	} else if err := tx.PutImportSource(ctx, im.AccountID, store.SourceArchive, src, n.ID, im.ID); err != nil {
		return "", err
	}
	if err := tx.InsertImportItem(ctx, &store.ImportItem{ImportID: im.ID, Path: key, Name: name.Name,
		Kind: store.KindDir, State: state, NodeID: n.ID, Reuses: reuses, CreatedAt: now}); err != nil {
		return "", err
	}
	return n.ID, nil
}

// importFileEntry writes one file of the archive: it reserves the file's declared size against the quota, copies the
// bytes into a blob piece by piece, and then, in one write, makes the staged node and its item. An entry the import
// already brought in is skipped, and an entry that does not fit the quota or the disk pauses the import.
func (s *Server) importFileEntry(ctx context.Context, impID string, p *auth.Principal, e imports.Entry, body io.Reader) error {
	cur, err := s.store.Import(ctx, impID)
	if err != nil {
		return err
	}
	src := importSourceKey(cur, e.Key)
	// The disk is checked in the reservation's own write (importReserve), so the check and the reservation see the same
	// totals and a full disk leaves nothing reserved (decision 120).
	skip, err := s.importReserve(ctx, impID, p, e, src)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}
	if err := s.importFaultAt("reserved"); err != nil {
		return err
	}
	w, err := s.blobs.Create(ctx, cur.AccountID, e.Size)
	if err != nil {
		return apierr.Wrap(apierr.Internal, "the import could not write a file", err)
	}
	defer w.Abort() //nolint:errcheck // removes the temporary file unless the commit below took it
	buf := make([]byte, importPieceBytes)
	for {
		if err := s.importStillRunning(ctx, impID); err != nil {
			return err
		}
		n, rerr := io.ReadFull(body, buf)
		if n > 0 {
			if _, err := w.Write(buf[:n]); err != nil {
				if errors.Is(err, syscall.ENOSPC) {
					// The disk filled under the write: a pause, as a full disk is for an entry.
					return s.importDiskPause(apierr.New(apierr.StorageFull, "the server's disk is full"))
				}
				if errors.Is(err, blob.ErrTooLarge) {
					return apierr.New(apierr.TooLarge, "a file of the archive is larger than its header says").With("reason", "size")
				}
				return err
			}
		}
		if errors.Is(rerr, io.EOF) || errors.Is(rerr, io.ErrUnexpectedEOF) {
			break
		}
		if rerr != nil {
			return apierr.New(apierr.UnsupportedMedia, "a file of the archive cannot be read").With("reason", "body")
		}
	}
	committed, err := w.Commit()
	if err != nil {
		return apierr.Wrap(apierr.Internal, "the import could not write a file", err)
	}
	if err := s.store.Write(ctx, func(tx *store.Tx) error {
		im, err := tx.Import(ctx, impID)
		if err != nil {
			return err
		}
		if im.State != store.ImportRunning {
			return errImportStopped
		}
		// The reservation becomes the file: its declared size is released here, and the publish charges the real bytes.
		if err := tx.AdjustFolders(ctx, im.AccountID, store.FolderChange{FolderID: im.StagingFolderID,
			Change: store.UsageChange{Reserved: -e.Size}}); err != nil {
			return err
		}
		im.ReservedBytes = 0
		parent, err := s.importParent(ctx, tx, im, p, e.Names[:len(e.Names)-1])
		if err != nil {
			return err
		}
		now := store.Now()
		if err := tx.InsertBlob(ctx, &store.Blob{ID: committed.ID, AccountID: im.AccountID, Size: committed.Size,
			SHA256: committed.SHA256, State: store.BlobLive, CreatedAt: now}); err != nil {
			return err
		}
		name := e.Names[len(e.Names)-1]
		n := &store.Node{ID: id.New("nod"), AccountID: im.AccountID, FolderID: im.StagingFolderID, ParentID: parent,
			Name: name.Name, NameKey: name.Key, Kind: store.KindFile, Size: committed.Size, MimeType: mimeOf("", name.Name),
			BlobID: committed.ID, ETag: newETag(), SHA256: committed.SHA256, CreatedAt: now, ModifiedAt: now,
			CreatedBy: p.ID, ModifiedBy: p.ID}
		if err := tx.InsertNodeNoIndex(ctx, n); err != nil {
			return err
		}
		if err := tx.PutImportSource(ctx, im.AccountID, store.SourceArchive, src, n.ID, im.ID); err != nil {
			return err
		}
		if err := tx.InsertImportItem(ctx, &store.ImportItem{ImportID: im.ID, Path: e.Key, Name: name.Name,
			Kind: store.KindFile, State: "done", NodeID: n.ID, Bytes: committed.Size, CreatedAt: now}); err != nil {
			return err
		}
		im.Files++
		im.Bytes += committed.Size
		im.Cursor = int64(e.Index)
		return tx.SaveImport(ctx, im, now)
	}); err != nil {
		// The write failed after the blob was committed: nothing names the blob, so its file is removed here.
		_ = s.blobs.Remove(cur.AccountID, committed.ID)
		return err
	}
	return nil
}

// importReserve makes the write that reserves a file's declared size before its bytes are read. It returns skip for a
// file the import already brought in. A file that does not fit the quota is refused as a pause.
func (s *Server) importReserve(ctx context.Context, impID string, p *auth.Principal, e imports.Entry, src string) (bool, error) {
	var skip bool
	// The flight lock is taken before the write and released after it, as a zip start takes it, so the piece bytes in
	// flight are read under the lock and the lock is never taken inside a write.
	s.oneFlight.mu.Lock()
	// The deferred release also runs when the reservation panics, so a panic does not leave the lock held.
	defer s.oneFlight.mu.Unlock()
	// before-reserve is a fault point inside the lock and before the write. A test makes a competing reservation here,
	// so the check inside the write is the one that must see it.
	if err := s.importFaultAt("before-reserve"); err != nil {
		return false, err
	}
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		im, err := tx.Import(ctx, impID)
		if err != nil {
			return err
		}
		if im.State != store.ImportRunning {
			return errImportStopped
		}
		// A reservation a stopped run left (its entry was not finished) is released first, so the entry is reserved
		// once, and the record holds the one amount the folder holds.
		if im.ReservedBytes > 0 {
			if err := tx.AdjustFolders(ctx, im.AccountID, store.FolderChange{FolderID: im.StagingFolderID,
				Change: store.UsageChange{Reserved: -im.ReservedBytes}}); err != nil {
				return err
			}
			im.ReservedBytes = 0
			// The row holds the release too, so the disk check below does not count the entry's old reservation.
			if err := tx.SaveImport(ctx, im, store.Now()); err != nil {
				return err
			}
		}
		have, err := tx.ImportSourceNode(ctx, im.AccountID, im.FolderID, store.SourceArchive, src)
		if err != nil {
			return err
		}
		if have != "" {
			name := e.Names[len(e.Names)-1]
			if err := tx.InsertImportItem(ctx, &store.ImportItem{ImportID: im.ID, Path: e.Key, Name: name.Name,
				Kind: store.KindFile, State: "skipped", NodeID: have, CreatedAt: store.Now()}); err != nil {
				return err
			}
			im.Skipped++
			im.Cursor = int64(e.Index)
			skip = true
			return tx.SaveImport(ctx, im, store.Now())
		}
		// The disk must hold this entry as well as what every unfinished writer holds (decision 120). It is checked
		// before the reservation is written, in the same write, so a refused entry reserves nothing.
		if err := s.checkDiskTx(ctx, tx, im.AccountID, e.Size, s.oneFlight.total); err != nil {
			return err
		}
		if err := s.applyUsage(ctx, tx, p, im.AccountID, store.FolderChange{FolderID: im.StagingFolderID,
			Change: store.UsageChange{Reserved: e.Size}}); err != nil {
			return err
		}
		im.ReservedBytes = e.Size
		return tx.SaveImport(ctx, im, store.Now())
	})
	if err == nil {
		return skip, nil
	}
	var ae *apierr.Error
	if errors.As(err, &ae) && ae.Code == apierr.QuotaExceeded {
		return false, &importPause{reason: string(apierr.QuotaExceeded)}
	}
	return false, s.importDiskPause(err)
}

// importDiskPause turns a full disk into a pause.
func (s *Server) importDiskPause(err error) error {
	var ae *apierr.Error
	if errors.As(err, &ae) && ae.Code == apierr.StorageFull {
		return &importPause{reason: string(apierr.StorageFull)}
	}
	return err
}

// importStillRunning reads the import's state, and stops the entry when the import was paused or cancelled.
func (s *Server) importStillRunning(ctx context.Context, impID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cur, err := s.store.Import(ctx, impID)
	if err != nil {
		return err
	}
	if cur.State != store.ImportRunning {
		return errImportStopped
	}
	return nil
}

// importPublish moves the staged tree into the target in one write. Each top-level entry is placed under its parent, with
// the name clash settled by the import's on_conflict, and the audit entry and the completion event are written there too.
// importTargetRole checks that the user who asked for an import may still write to its target: an editor's role on a team
// space, a contributor's on a personal folder. The rights may have changed since the import was asked for, so the check
// is made when the import publishes and again when it resumes (decision 92).
func (s *Server) importTargetRole(ctx context.Context, tx *store.Tx, im *store.Import, p *auth.Principal) error {
	folder, err := tx.Folder(ctx, im.AccountID, im.FolderID)
	if err != nil {
		return err
	}
	parent := im.ParentID
	if parent == "" {
		root, err := tx.RootNode(ctx, im.AccountID, im.FolderID)
		if err != nil {
			return err
		}
		parent = root.ID
	}
	role := store.RoleContributor
	if folder.Kind == store.KindTeam {
		role = store.RoleEditor
	}
	_, err = requireNodeRole(ctx, tx, p, parent, role)
	return err
}

// checkReusedDirs checks that the directory id lies inside the target directory at publish. Each directory from id up to
// the target must be a directory of the target folder and not trashed, and the walk up must reach the target: a directory
// moved to the folder's root, or under a sibling of the target, is in the same folder but outside the target, and refused
// (the containment walk). reach holds, for each directory this write has walked, whether its walk
// reached the target, so a directory is walked once.
func checkReusedDirs(ctx context.Context, tx *store.Tx, im *store.Import, target, id string, reach map[string]bool) error {
	var walked []string
	reached := false
	for cur := id; ; {
		if cur == target {
			reached = true
			break
		}
		if r, known := reach[cur]; known {
			reached = r
			break
		}
		if cur == "" {
			break // the walk ended at the folder's root without reaching the target
		}
		n, err := tx.Node(ctx, im.AccountID, cur)
		if errors.Is(err, store.ErrNotFound) {
			// A purged directory is not an internal error: the import's target moved away.
			return importTargetMoved()
		}
		if err != nil {
			return err
		}
		if n.FolderID != im.FolderID || n.Kind != store.KindDir || n.TrashedAt != nil {
			return importTargetMoved()
		}
		walked = append(walked, cur)
		cur = n.ParentID
	}
	for _, w := range walked {
		reach[w] = reached
	}
	if !reached {
		return importTargetMoved()
	}
	return nil
}

// importTargetMoved is the refusal of a publish whose target, or a directory the import writes into, moved or was removed.
func importTargetMoved() error {
	return apierr.New(apierr.Conflict, "a directory the import writes into moved or was removed since the import started").
		With("reason", "target_moved")
}

// placeStaged places one staged node under parent, in the caller's write. The directory it goes into must be reached from
// the target (checked once per directory through reach). A live node of the same name meets the import's conflict policy,
// as a top-level entry always did.
func placeStaged(ctx context.Context, tx *store.Tx, im *store.Import, p *auth.Principal, target string,
	reach map[string]bool, parent *store.Node, n *store.Node, now time.Time) error {
	if err := checkReusedDirs(ctx, tx, im, target, parent.ID, reach); err != nil {
		return err
	}
	plan := &transferPlan{folderID: im.FolderID, parent: parent, name: n.Name, key: n.NameKey}
	taken, err := nameTakenBy(tx.Child(ctx, im.AccountID, parent.ID, n.NameKey))
	if err != nil {
		return err
	}
	if taken != nil {
		if err := applyConflict(ctx, tx, plan, taken, im.OnConflict, n.Kind, ""); err != nil {
			return err
		}
		if err := requireReplaceRole(ctx, tx, p, plan); err != nil {
			return err
		}
		if _, err := applyReplace(ctx, tx, plan, p.ID); err != nil {
			return err
		}
	}
	return tx.SetStagedParent(ctx, im.AccountID, im.StagingFolderID, n.ID, parent.ID, plan.name, plan.key, now)
}

func (s *Server) importPublish(ctx context.Context, impID string, p *auth.Principal) error {
	if err := s.importFaultAt("publish"); err != nil {
		return err
	}
	return s.store.Write(ctx, func(tx *store.Tx) error {
		im, err := tx.Import(ctx, impID)
		if err != nil {
			return err
		}
		if im.State != store.ImportRunning {
			return errImportStopped
		}
		// A refusal here fails the import, and its run goes on to the cleanup that removes the staged tree.
		// The target directory may have moved since staging (a cross-folder move, or a trash): it must still be a
		// directory of the target folder, and not trashed.
		if im.ParentID != "" {
			dir, err := tx.Node(ctx, im.AccountID, im.ParentID)
			if err != nil {
				return err
			}
			if dir.FolderID != im.FolderID || dir.Kind != store.KindDir || dir.TrashedAt != nil {
				return apierr.New(apierr.Conflict, "the target directory moved or was removed since the import started").
					With("reason", "target_moved")
			}
		}
		if err := s.importTargetRole(ctx, tx, im, p); err != nil {
			return err
		}
		now := store.Now()
		// Every directory this import reused is checked, whether or not an entry of it is staged now: a move of the directory
		// carries the staged entries under it along.
		// The target is a node: the folder's root when the import names no directory.
		target := im.ParentID
		if target == "" {
			root, err := tx.RootNode(ctx, im.AccountID, im.FolderID)
			if err != nil {
				return err
			}
			target = root.ID
		}
		reach := map[string]bool{}
		reused, err := tx.ReusedDirs(ctx, im.ID)
		if err != nil {
			return err
		}
		for _, d := range reused {
			if err := checkReusedDirs(ctx, tx, im, target, d.Live, reach); err != nil {
				return err
			}
		}
		// The entries staged under a reused directory go into its live directory, deepest directory first, and its
		// placeholder goes. A child is a staged entry, so the live directory holds no staged entry while this runs (B2).
		for _, d := range reused {
			live, err := tx.Node(ctx, im.AccountID, d.Live)
			if err != nil {
				return err
			}
			kids, err := tx.StagedChildren(ctx, im.AccountID, im.StagingFolderID, d.Placeholder)
			if err != nil {
				return err
			}
			for _, n := range kids {
				if err := placeStaged(ctx, tx, im, p, target, reach, live, n, now); err != nil {
					return err
				}
			}
			if err := tx.DropStagedDir(ctx, im.AccountID, im.StagingFolderID, im.ID, d.Placeholder, d.Live); err != nil {
				return err
			}
		}
		parent, err := tx.Node(ctx, im.AccountID, target)
		if err != nil {
			return err
		}
		tops, err := tx.StagedTopLevel(ctx, im.AccountID, im.StagingFolderID, im.StagingRootID)
		if err != nil {
			return err
		}
		for _, n := range tops {
			if err := placeStaged(ctx, tx, im, p, target, reach, parent, n, now); err != nil {
				return err
			}
		}
		files, bytes, err := tx.StagedCounts(ctx, im.AccountID, im.StagingFolderID)
		if err != nil {
			return err
		}
		if err := s.applyUsage(ctx, tx, p, im.AccountID,
			store.FolderChange{FolderID: im.StagingFolderID, Change: store.UsageChange{Reserved: -im.ReservedBytes}},
			store.FolderChange{FolderID: im.FolderID, Change: store.UsageChange{Bytes: bytes, Files: files}}); err != nil {
			return err
		}
		im.ReservedBytes = 0
		if err := tx.MoveStagedTree(ctx, im.AccountID, im.StagingFolderID, im.FolderID, now); err != nil {
			return err
		}
		if err := tx.ReleaseArchiveReservation(ctx, im); err != nil {
			return err
		}
		if err := tx.DropStaging(ctx, im.AccountID, im.StagingFolderID, im.StagingRootID); err != nil {
			return err
		}
		// One summary, not a row or a webhook event per file: importEnd records the counts, and
		// import.completed carries them. A copy of a large tree also reports once, not per file.
		im.Files, im.Bytes = files, bytes
		im.State, im.Phase, im.FinishedAt = store.ImportDone, store.ImportPhaseDone, &now
		if err := tx.SaveImport(ctx, im, now); err != nil {
			return err
		}
		return s.importEnd(ctx, tx, p, im, "done", "", now)
	})
}

// importEnd writes the end of an import: its audit entry with the outcome and counts, and its event, which carries IDs
// and counts only. It is written in the write that ends the import, so a restart cannot end it twice.
func (s *Server) importEnd(ctx context.Context, tx *store.Tx, p *auth.Principal, im *store.Import, outcome, code string, at time.Time) error {
	details := map[string]any{"outcome": outcome, "entries": im.Entries, "files": im.Files, "bytes": im.Bytes,
		"skipped": im.Skipped, "job": im.JobID}
	if code != "" {
		details["error_code"] = code
	}
	if err := tx.InsertAudit(ctx, auditAs(p, im.AccountID, "import.finish", im.ID, details, im.FolderID)); err != nil {
		return err
	}
	var typ string
	switch outcome {
	case "done":
		typ = "import.completed"
	case "failed":
		typ = "import.failed"
	default:
		typ = "import.cancelled"
	}
	return tx.EmitEvent(ctx, im.AccountID, typ, at, importEvent(im, code))
}

// importEvent is the data of an import event: IDs and counts, and the code of a failure.
func importEvent(im *store.Import, code string) webhook.Import {
	return webhook.Import{ID: im.ID, Source: im.Source, FolderID: im.FolderID, Entries: int(im.Entries),
		Files: int(im.Files), Bytes: im.Bytes, Skipped: int(im.Skipped), ErrorCode: code}
}

// importFail moves a running import to cleanup, with its error code and its text, and writes the event and the audit
// entry of the failure. A cancelled or paused import is left as it is: the loop then reads the state that won.
func (s *Server) importFail(ctx context.Context, impID string, p *auth.Principal, cause error) error {
	code := string(apierr.Internal)
	var ae *apierr.Error
	if errors.As(cause, &ae) {
		code = string(ae.Code)
	}
	// The record and the job carry the code only. The cause's text can name a server path or a store error, so it goes to
	// the server log.
	s.log.Warn("import failed", "import", impID, "error", cause)
	return s.store.Write(ctx, func(tx *store.Tx) error {
		im, err := tx.Import(ctx, impID)
		if err != nil {
			return err
		}
		if im.State != store.ImportRunning && im.State != store.ImportQueued {
			return nil
		}
		now := store.Now()
		im.State, im.Phase, im.ErrorCode, im.Failure = store.ImportFailed, store.ImportPhaseCleanup, code, code
		im.FinishedAt = &now
		if err := tx.SaveImport(ctx, im, now); err != nil {
			return err
		}
		return s.importEnd(ctx, tx, p, im, "failed", code, now)
	})
}

// requeueImports queues the jobs of the imports that need one, and ends the poison ones (store RequeueAbandonedImports).
func (s *Server) requeueImports(ctx context.Context) (int, error) {
	return s.store.RequeueAbandonedImports(ctx, s.endPoisonedImport)
}

// endPoisonedImport writes the end of an import the store failed as poison: its audit entry and its import.failed (or
// import.cancelled) event, in the write that cleans it up, as importFail writes them for any other failure. A poison
// import failed after repeated job failures, so its code is internal.
func (s *Server) endPoisonedImport(ctx context.Context, tx *store.Tx, im *store.Import) error {
	p, err := importPrincipal(im)
	if err != nil {
		return err
	}
	at := store.Now()
	if im.FinishedAt != nil {
		at = *im.FinishedAt
	}
	if im.State == store.ImportCancelled {
		return s.importEnd(ctx, tx, p, im, "cancelled", "", at)
	}
	s.log.Warn("import failed", "import", im.ID, "error", "poison: its jobs failed repeatedly")
	return s.importEnd(ctx, tx, p, im, "failed", string(apierr.Internal), at)
}

// importPauseNow parks a running import for the owner, with the reason. The job then ends; a resume queues it again.
func (s *Server) importPauseNow(ctx context.Context, impID, reason string) error {
	return s.store.Write(ctx, func(tx *store.Tx) error {
		im, err := tx.Import(ctx, impID)
		if err != nil {
			return err
		}
		if im.State != store.ImportRunning {
			return nil
		}
		im.State, im.PauseReason = store.ImportPaused, reason
		p, err := importPrincipal(im)
		if err != nil {
			return err
		}
		if err := tx.InsertAudit(ctx, auditAs(p, im.AccountID, "import.pause", im.ID,
			map[string]any{"reason": reason, "job": im.JobID}, im.FolderID)); err != nil {
			return err
		}
		return tx.SaveImport(ctx, im, store.Now())
	})
}

// importCleanup ends a failed or cancelled import: it writes the end once, then removes the staged nodes in bounded
// writes, their blobs, the staging folder and the archive, and it forgets what the import remembered. A repeat finds
// what is left and removes it. failure is the error the job reports, if the import failed in this run.
func (s *Server) importCleanup(ctx context.Context, impID string, p *auth.Principal, failure error) error {
	if err := s.importFaultAt("cleanup"); err != nil {
		return err
	}
	// The end of a cancelled import is written once: its FinishedAt is set in the write that also writes its event.
	if err := s.store.Write(ctx, func(tx *store.Tx) error {
		im, err := tx.Import(ctx, impID)
		if err != nil {
			return err
		}
		if im.State != store.ImportCancelled || im.FinishedAt != nil {
			return nil
		}
		now := store.Now()
		im.FinishedAt = &now
		if err := tx.SaveImport(ctx, im, now); err != nil {
			return err
		}
		return s.importEnd(ctx, tx, p, im, "cancelled", "", now)
	}); err != nil {
		return err
	}
	if err := s.importFaultAt("cleanup"); err != nil {
		return err
	}
	if err := s.store.Write(ctx, func(tx *store.Tx) error {
		im, err := tx.Import(ctx, impID)
		if err != nil {
			return err
		}
		if im.ReservedBytes > 0 {
			if err := tx.AdjustFolders(ctx, im.AccountID, store.FolderChange{FolderID: im.StagingFolderID,
				Change: store.UsageChange{Reserved: -im.ReservedBytes}}); err != nil {
				return err
			}
			im.ReservedBytes = 0
			return tx.SaveImport(ctx, im, store.Now())
		}
		return tx.DropImportSources(ctx, im.ID)
	}); err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var done bool
		var blobs []string
		var acc string
		if err := s.store.Write(ctx, func(tx *store.Tx) error {
			im, err := tx.Import(ctx, impID)
			if err != nil {
				return err
			}
			acc = im.AccountID
			var err2 error
			blobs, done, err2 = tx.CleanupStagedBatch(ctx, im.AccountID, im.StagingFolderID, importCleanupBatch)
			return err2
		}); err != nil {
			return err
		}
		for _, b := range blobs {
			_ = s.blobs.Remove(acc, b) // a file that is already gone is not an error; the sweep finds the rest
		}
		if done {
			break
		}
	}
	if err := s.store.Write(ctx, func(tx *store.Tx) error {
		im, err := tx.Import(ctx, impID)
		if err != nil {
			return err
		}
		if err := tx.ReleaseArchiveReservation(ctx, im); err != nil {
			return err
		}
		if err := tx.DropStaging(ctx, im.AccountID, im.StagingFolderID, im.StagingRootID); err != nil {
			return err
		}
		return tx.SaveImport(ctx, im, store.Now())
	}); err != nil {
		return err
	}
	if err := s.importRemoveArchive(ctx, impID); err != nil {
		return err
	}
	if err := s.store.Write(ctx, func(tx *store.Tx) error {
		im, err := tx.Import(ctx, impID)
		if err != nil {
			return err
		}
		im.Phase = store.ImportPhaseDone
		return tx.SaveImport(ctx, im, store.Now())
	}); err != nil {
		return err
	}
	return failure
}

// importRemoveArchive removes the archive an import held, its blob and its file. It runs once the archive is no longer
// needed: after the publish, and after a failure or a cancel.
func (s *Server) importRemoveArchive(ctx context.Context, impID string) error {
	var acc, archive string
	if err := s.store.Write(ctx, func(tx *store.Tx) error {
		im, err := tx.Import(ctx, impID)
		if err != nil {
			return err
		}
		if im.ArchiveBlobID == "" {
			return nil
		}
		acc, archive = im.AccountID, im.ArchiveBlobID
		if err := tx.DropBlob(ctx, acc, archive); err != nil {
			return err
		}
		im.ArchiveBlobID = ""
		return tx.SaveImport(ctx, im, store.Now())
	}); err != nil {
		return err
	}
	if archive != "" {
		_ = s.blobs.Remove(acc, archive) // gone already is not an error
	}
	return nil
}
