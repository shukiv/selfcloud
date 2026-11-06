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
		MaxFile: int64(s.cfg.Upload.MaxFileSize), MaxTotal: int64(a.MaxTotalBytes)}
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
		failure = errors.New(cur.Failure)
	}
	until := time.Now().Add(s.importYield)
	for {
		cur, err := s.store.Import(ctx, imp.ID)
		if err != nil {
			return err
		}
		switch cur.Phase {
		case store.ImportPhaseScan:
			err = s.importScan(ctx, cur.ID, p)
		case store.ImportPhaseStage:
			err = s.importStage(ctx, cur.ID, p, until)
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
		failure = err
		if err := s.importFail(ctx, cur.ID, p, err); err != nil {
			return err
		}
	}
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

// importStage writes the archive's entries into the staging folder, from the cursor on. It returns when every entry is
// written (the phase moves to publish), when the time budget is used (jobs.ErrYield), or with errImportStopped or a pause.
func (s *Server) importStage(ctx context.Context, impID string, p *auth.Principal, until time.Time) error {
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
	err = imports.Walk(f, size, imports.Format(cur.ArchiveFormat), int(cur.Cursor), func(e imports.Entry, body io.Reader) error {
		if int64(e.Index) > start && time.Now().After(until) {
			return jobs.ErrYield
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
	if have != "" {
		n, err := tx.Node(ctx, im.AccountID, have)
		if err != nil {
			return "", err
		}
		expected := parent
		if parent == im.StagingRootID {
			expected = im.ParentID
		}
		if n.Kind == store.KindDir && n.FolderID == im.FolderID && n.ParentID == expected {
			if err := tx.InsertImportItem(ctx, &store.ImportItem{ImportID: im.ID, Path: key, Name: name.Name,
				Kind: store.KindDir, State: "skipped", NodeID: have, CreatedAt: now}); err != nil {
				return "", err
			}
			im.Skipped++
			return have, nil
		}
	}
	n := &store.Node{ID: id.New("nod"), AccountID: im.AccountID, FolderID: im.StagingFolderID, ParentID: parent,
		Name: name.Name, NameKey: name.Key, Kind: store.KindDir, ETag: newETag(), CreatedAt: now, ModifiedAt: now,
		CreatedBy: p.ID, ModifiedBy: p.ID}
	if err := tx.InsertNodeNoIndex(ctx, n); err != nil {
		return "", err
	}
	if err := tx.PutImportSource(ctx, im.AccountID, store.SourceArchive, src, n.ID, im.ID); err != nil {
		return "", err
	}
	if err := tx.InsertImportItem(ctx, &store.ImportItem{ImportID: im.ID, Path: key, Name: name.Name,
		Kind: store.KindDir, State: "done", NodeID: n.ID, CreatedAt: now}); err != nil {
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
	if have, err := s.store.ImportSourceNode(ctx, cur.AccountID, cur.FolderID, store.SourceArchive, src); err != nil {
		return err
	} else if have == "" {
		// The disk is checked before the reservation, so a full disk leaves nothing reserved.
		if err := s.checkDiskNow(ctx, cur.AccountID); err != nil {
			return s.importDiskPause(err)
		}
	}
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
	return false, err
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
		if err := s.importTargetRole(ctx, tx, im, p); err != nil {
			return err
		}
		tops, err := tx.StagedTopLevel(ctx, im.AccountID, im.StagingFolderID)
		if err != nil {
			return err
		}
		now := store.Now()
		for _, n := range tops {
			parentID := n.ParentID
			if parentID == im.StagingRootID {
				parentID = im.ParentID
			}
			parent, err := tx.Node(ctx, im.AccountID, parentID)
			if err != nil {
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
			if err := tx.SetStagedParent(ctx, im.AccountID, n.ID, parent.ID, plan.name, plan.key, now); err != nil {
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
		if err := tx.DropStaging(ctx, im.AccountID, im.StagingFolderID, im.StagingRootID); err != nil {
			return err
		}
		items, err := tx.ImportFileItems(ctx, im.ID)
		if err != nil {
			return err
		}
		for _, it := range items {
			details := map[string]any{"kind": store.KindFile, "name": it.Name, "size": it.Bytes, "via": "import",
				"import": im.ID, "job": im.JobID}
			if err := tx.InsertAudit(ctx, auditAs(p, im.AccountID, "node.create", it.NodeID, details, im.FolderID)); err != nil {
				return err
			}
		}
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
	return s.store.Write(ctx, func(tx *store.Tx) error {
		im, err := tx.Import(ctx, impID)
		if err != nil {
			return err
		}
		if im.State != store.ImportRunning && im.State != store.ImportQueued {
			return nil
		}
		now := store.Now()
		im.State, im.Phase, im.ErrorCode, im.Failure = store.ImportFailed, store.ImportPhaseCleanup, code, cause.Error()
		im.FinishedAt = &now
		if err := tx.SaveImport(ctx, im, now); err != nil {
			return err
		}
		return s.importEnd(ctx, tx, p, im, "failed", code, now)
	})
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
