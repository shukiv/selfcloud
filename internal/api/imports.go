// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/imports"
	"github.com/shukiv/selfcloud/internal/store"
)

// The import routes (decision 92, slice im1). A signed-in user starts an import of an uploaded archive into a folder they
// may write to, and follows it through these routes: the state and counts, the items, and pause, resume and cancel. Only
// the user who started an import may act on it; a manager of a team space may read the import that went into it.

// jobImport is the kind of an import job. Its jobs run in a pool of their own (imports.max_concurrent_jobs).
const jobImport = "import"

// importBody is the body of POST /imports.
type importBody struct {
	Source     string `json:"source"`
	Upload     string `json:"upload"`
	Folder     string `json:"folder"`
	Parent     string `json:"parent"`
	OnConflict string `json:"on_conflict"`
}

// importJSON is an import as the API shows it. The failure text is not shown: the error_code names the failure.
type importJSON struct {
	ID            string     `json:"id"`
	Source        string     `json:"source"`
	State         string     `json:"state"`
	PauseReason   string     `json:"pause_reason,omitempty"`
	ErrorCode     string     `json:"error_code,omitempty"`
	Folder        string     `json:"folder"`
	Parent        string     `json:"parent"`
	OnConflict    string     `json:"on_conflict"`
	Format        string     `json:"format,omitempty"`
	Entries       int64      `json:"entries"`
	DeclaredBytes int64      `json:"declared_bytes"`
	Files         int64      `json:"files"`
	Bytes         int64      `json:"bytes"`
	Skipped       int64      `json:"skipped"`
	Job           string     `json:"job"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	FinishedAt    *time.Time `json:"finished_at"`
}

func toImportJSON(im *store.Import) importJSON {
	return importJSON{ID: im.ID, Source: im.Source, State: im.State, PauseReason: im.PauseReason, ErrorCode: im.ErrorCode,
		Folder: im.FolderID, Parent: im.ParentID, OnConflict: im.OnConflict, Format: im.ArchiveFormat, Entries: im.Entries,
		DeclaredBytes: im.DeclaredBytes, Files: im.Files, Bytes: im.Bytes, Skipped: im.Skipped, Job: im.JobID,
		CreatedAt: im.CreatedAt, UpdatedAt: im.UpdatedAt, FinishedAt: im.FinishedAt}
}

// importCaller returns nil for the signed-in user who calls an import route, and a forbidden answer for anyone else.
func importCaller(p *auth.Principal) error {
	if p == nil || p.Kind != auth.KindUser || p.UserID == "" {
		return apierr.New(apierr.Forbidden, "imports are for a signed-in user")
	}
	return nil
}

// handleImportSources lists the import sources, and whether the caller's account has each one on.
func (s *Server) handleImportSources(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := importCaller(p); err != nil {
		return err
	}
	on, err := s.store.ImportSwitchOn(r.Context(), p.AccountID)
	if err != nil {
		return err
	}
	archive := s.cfg.Imports.Enabled && s.cfg.Imports.Archive.Enabled && on
	writeJSON(w, http.StatusOK, map[string]any{"sources": []map[string]any{{"source": store.SourceArchive,
		"enabled": archive}}})
	return nil
}

// checkImportSource refuses an import from a source that is off for the installation or for the account.
func (s *Server) checkImportSource(ctx context.Context, tx *store.Tx, accountID, source string) error {
	if !s.cfg.Imports.Enabled {
		return apierr.New(apierr.Forbidden, "imports are turned off on this server")
	}
	if source != store.SourceArchive || !s.cfg.Imports.Archive.Enabled {
		return apierr.New(apierr.Forbidden, "that import source is turned off on this server")
	}
	on, err := tx.ImportSwitchOn(ctx, accountID)
	if err != nil {
		return err
	}
	if !on {
		return apierr.New(apierr.Forbidden, "that import source is turned off for this account").With("reason", "account_off")
	}
	return nil
}

// handleCreateImport starts an import of an archive upload into a folder. The archive's headers are read now when the
// archive is a zip, so a refused archive, or one that does not fit the quota, is answered at once; a tar is read by the
// job, since its headers are only known after it is decompressed.
func (s *Server) handleCreateImport(w http.ResponseWriter, r *http.Request) error {
	var in importBody
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	p := principalOf(r)
	if err := importCaller(p); err != nil {
		return err
	}
	if in.Source == "" {
		in.Source = store.SourceArchive
	}
	if in.OnConflict == "" {
		in.OnConflict = "rename"
	}
	if !id.Valid("upl", in.Upload) {
		return apierr.New(apierr.InvalidRequest, "upload must be an upload ID")
	}
	if !id.Valid("fld", in.Folder) {
		return apierr.New(apierr.InvalidRequest, "folder must be a folder ID")
	}
	if in.Parent != "" && !id.Valid("nod", in.Parent) {
		return apierr.New(apierr.InvalidRequest, "parent must be a directory ID")
	}
	switch in.OnConflict {
	case "fail", "rename", "replace":
	default:
		return apierr.New(apierr.InvalidRequest, "on_conflict must be fail, rename or replace")
	}
	ctx := r.Context()
	// The archive's upload is read first, outside the write: it is a finished file, and its zip headers are read here.
	up, err := s.store.Upload(ctx, p.AccountID, in.Upload)
	if errors.Is(err, store.ErrNotFound) || (err == nil && (up.CreatedBy != p.ID || up.Purpose != store.UploadPurposeImport)) {
		return notFound("upload")
	}
	if err != nil {
		return err
	}
	if up.DurableOffset != up.Length {
		return apierr.New(apierr.UploadOffsetMismatch, "the upload is not complete yet")
	}
	probe, err := s.probeImportArchive(up)
	if err != nil {
		return err
	}

	digest, err := importDigest(up)
	if err != nil {
		return err
	}
	im := &store.Import{ID: id.New("imp"), JobID: id.New("job"), AccountID: p.AccountID, UserID: p.UserID,
		Source: in.Source, FolderID: in.Folder, OnConflict: in.OnConflict, State: store.ImportQueued,
		Phase: store.ImportPhaseScan, ArchiveBlobID: up.BlobID, ArchiveSHA256: digest}
	if probe != nil {
		im.ArchiveFormat, im.Entries, im.DeclaredBytes = string(probe.Format), int64(probe.Entries), probe.Bytes
	}
	actor, err := marshalActor(p)
	if err != nil {
		return err
	}
	im.Actor = actor
	now := store.Now()
	im.CreatedAt, im.UpdatedAt = now, now
	var job *store.Job
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		if err := s.checkImportSource(ctx, tx, p.AccountID, in.Source); err != nil {
			return err
		}
		folder, err := tx.Folder(ctx, p.AccountID, in.Folder)
		if errors.Is(err, store.ErrNotFound) {
			return notFound("folder")
		}
		if err != nil {
			return err
		}
		if _, err := tx.UnfinishedImport(ctx, p.AccountID, p.UserID, in.Source); err == nil {
			return apierr.New(apierr.Conflict, "an import from this source is already running; wait for it or cancel it").
				With("reason", "too_many_imports")
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		unfinished, err := tx.CountUnfinishedImports(ctx, p.AccountID, p.UserID)
		if err != nil {
			return err
		}
		if unfinished >= s.cfg.Imports.MaxUnfinishedPerUser {
			return apierr.New(apierr.Conflict, "too many imports are unfinished; finish, resume or cancel one first").
				With("reason", "too_many_unfinished")
		}
		role := store.RoleContributor
		if folder.Kind == store.KindTeam {
			role = store.RoleEditor
		}
		parent := in.Parent
		if parent == "" {
			root, err := tx.RootNode(ctx, p.AccountID, in.Folder)
			if err != nil {
				return err
			}
			parent = root.ID
		}
		dir, err := requireNodeRole(ctx, tx, p, parent, role)
		if err != nil {
			return err
		}
		if dir.FolderID != in.Folder || dir.Kind != store.KindDir {
			return apierr.New(apierr.InvalidRequest, "parent must be a directory of the folder")
		}
		if probe != nil {
			st, err := tx.QuotaStateOf(ctx, p.AccountID, in.Folder)
			if err != nil {
				return err
			}
			if err := s.checkUserLimit(p, p.AccountID, st, probe.Bytes); err != nil {
				return err
			}
			if err := s.checkUsage(ctx, tx, p, p.AccountID, store.FolderChange{FolderID: in.Folder,
				Change: store.UsageChange{Bytes: probe.Bytes, Files: int64(probe.Files)}}); err != nil {
				return err
			}
		}
		im.ParentID = parent
		im.StagingFolderID, im.StagingRootID = id.New("fld"), id.New("nod")
		if err := tx.CreateStagingFolder(ctx, p.AccountID, im.StagingFolderID, im.StagingRootID, now); err != nil {
			return err
		}
		// The archive is kept under its own blob, which no node names until the job ends. Its upload row goes now.
		if err := tx.InsertBlob(ctx, &store.Blob{ID: up.BlobID, AccountID: p.AccountID, Size: up.Length,
			SHA256: im.ArchiveSHA256, State: store.BlobLive, CreatedAt: now}); err != nil {
			return err
		}
		if err := s.blobs.FinishUpload(p.AccountID, up.ID, up.BlobID); err != nil {
			return apierr.Wrap(apierr.Internal, "the archive could not be kept", err)
		}
		// The archive's reservation moves from the upload's folder to the staging folder, which holds it until the import
		// ends. The upload's row goes without a release (round 3, F1).
		if err := tx.MoveUsage(ctx, p.AccountID, up.FolderID, im.StagingFolderID, store.UsageChange{Reserved: up.Length}); err != nil {
			return err
		}
		im.ArchiveReserved = up.Length
		if err := tx.DeleteUploadHanded(ctx, p.AccountID, up.ID); err != nil {
			return err
		}
		body, err := marshalPayload(actor)
		if err != nil {
			return err
		}
		job = &store.Job{ID: im.JobID, AccountID: p.AccountID, Kind: jobImport, Target: im.ID,
			Status: store.JobQueued, Payload: body, CreatedAt: now, UpdatedAt: now}
		if err := tx.InsertJob(ctx, job); err != nil {
			return err
		}
		if err := tx.InsertImport(ctx, im); err != nil {
			return err
		}
		if err := tx.InsertAudit(ctx, s.auditEntry(r, p.AccountID, "import.start", im.ID,
			map[string]any{"source": in.Source, "folder": in.Folder, "parent": parent, "on_conflict": in.OnConflict,
				"job": im.JobID}, in.Folder)); err != nil {
			return err
		}
		return tx.EmitEvent(ctx, p.AccountID, "import.started", now, importEvent(im, ""))
	})
	if err != nil {
		return err
	}
	s.jobs.Wake()
	w.Header().Set("Location", "/api/v1/imports/"+im.ID)
	writeJSON(w, http.StatusAccepted, toImportJSON(im))
	return nil
}

// hexDigestOf returns the SHA-256 of a finished upload, from the hash state it kept. The archive's digest names its
// entries in the import's memory (import_sources), so the same archive gives the same names.
// importDigest is the archive's SHA-256, for the import record and its dedupe key. An archive whose digest cannot be read
// is refused: an empty hash would make different archives match one another (round 3, F15).
func importDigest(up *store.Upload) (string, error) {
	h, err := restoreHash(up.HashState, up.Length)
	if err != nil {
		return "", apierr.Wrap(apierr.Internal, "the archive's digest could not be read", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// marshalActor is the caller as a job and an import record it: the actor the job policies rebuild the caller from.
func marshalActor(p *auth.Principal) (string, error) {
	b, err := json.Marshal(actorOf(p))
	return string(b), err
}

// marshalPayload is the payload of an import job: the actor, under the key the job list reads it by (queuedBy).
func marshalPayload(actor string) (string, error) {
	b, err := json.Marshal(map[string]json.RawMessage{"actor": json.RawMessage(actor)})
	return string(b), err
}

// probeImportArchive reads a zip's headers when the archive is a zip, against the caps, so a refusal is answered at the
// request. A tar is not read here; it is nil.
func (s *Server) probeImportArchive(up *store.Upload) (*imports.Summary, error) {
	f, err := s.blobs.OpenUpload(up.AccountID, up.ID)
	if err != nil {
		return nil, apierr.Wrap(apierr.Internal, "the archive cannot be read", err)
	}
	defer f.Close()
	format, err := imports.Detect(f, up.Length)
	if err != nil {
		return nil, err
	}
	if format != imports.Zip {
		return nil, nil
	}
	sum, err := imports.Scan(f, up.Length, s.importCaps())
	if err != nil {
		return nil, err
	}
	return &sum, nil
}

// readImport loads an import for the caller, who must be the one who started it, or a manager of the team space it
// went into. Anyone else is told it does not exist.
func (s *Server) readImport(r *http.Request, p *auth.Principal) (*store.Import, error) {
	if err := importCaller(p); err != nil {
		return nil, err
	}
	im, err := s.store.Import(r.Context(), r.PathValue("imp"))
	if errors.Is(err, store.ErrNotFound) || (err == nil && im.AccountID != p.AccountID) {
		return nil, notFound("import")
	}
	if err != nil {
		return nil, err
	}
	if im.UserID == p.UserID {
		return im, nil
	}
	folder, err := s.store.Folder(r.Context(), im.AccountID, im.FolderID)
	if err == nil && folder.Kind == store.KindTeam {
		if root, err := s.store.RootNode(r.Context(), im.AccountID, im.FolderID); err == nil {
			if _, err := requireNodeRole(r.Context(), s.store, p, root.ID, store.RoleManager); err == nil {
				return im, nil
			}
		}
	}
	return nil, notFound("import")
}

// handleGetImport returns an import's state and counts.
func (s *Server) handleGetImport(w http.ResponseWriter, r *http.Request) error {
	im, err := s.readImport(r, principalOf(r))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toImportJSON(im))
	return nil
}

// handleImportItems returns a page of an import's items, in the order they were written. after is the seq of the last
// item the caller has, and next is the seq to pass for the following page, or null at the end.
func (s *Server) handleImportItems(w http.ResponseWriter, r *http.Request) error {
	im, err := s.readImport(r, principalOf(r))
	if err != nil {
		return err
	}
	var after int64
	if v := r.URL.Query().Get("after"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return apierr.New(apierr.InvalidRequest, "after must be a whole number")
		}
		after = n
	}
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			return apierr.New(apierr.InvalidRequest, "limit must be from 1 to 500")
		}
		limit = n
	}
	items, err := s.store.ImportItems(r.Context(), im.ID, after, limit)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(items))
	var next any
	for _, it := range items {
		out = append(out, map[string]any{"seq": it.Seq, "name": it.Name, "kind": it.Kind, "state": it.State,
			"node": nilIfEmpty(it.NodeID), "bytes": it.Bytes, "error_code": nilIfEmpty(it.ErrorCode)})
		next = it.Seq
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "next": next})
	return nil
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// handlePauseImport parks an import that is queued or running. Its job stops at its next checkpoint.
func (s *Server) handlePauseImport(w http.ResponseWriter, r *http.Request) error {
	return s.changeImport(w, r, "pause")
}

// handleResumeImport queues a paused import again.
func (s *Server) handleResumeImport(w http.ResponseWriter, r *http.Request) error {
	return s.changeImport(w, r, "resume")
}

// handleCancelImport cancels an import that has not finished. Its staged entries and archive are removed by its job.
func (s *Server) handleCancelImport(w http.ResponseWriter, r *http.Request) error {
	return s.changeImport(w, r, "cancel")
}

// changeImport is the state change of pause, resume and cancel, made by the caller who started the import. Each is one
// write: it checks the state, changes it, writes its audit entry, and queues the job the change needs when none is
// unfinished, so an import never has two jobs at once.
func (s *Server) changeImport(w http.ResponseWriter, r *http.Request, action string) error {
	p := principalOf(r)
	im, err := s.readImport(r, p)
	if err != nil {
		return err
	}
	if im.UserID != p.UserID {
		return notFound("import")
	}
	var job *store.Job
	var refused error
	err = s.store.Write(r.Context(), func(tx *store.Tx) error {
		cur, err := tx.Import(r.Context(), im.ID)
		if err != nil {
			return err
		}
		now := store.Now()
		switch action {
		case "pause":
			if cur.State != store.ImportQueued && cur.State != store.ImportRunning {
				return importState(cur)
			}
			cur.State, cur.PauseReason = store.ImportPaused, ""
			if err := tx.InsertAudit(r.Context(), s.auditEntry(r, cur.AccountID, "import.pause", cur.ID,
				map[string]any{"job": cur.JobID, "state": cur.State}, cur.FolderID)); err != nil {
				return err
			}
		case "resume":
			if cur.State != store.ImportPaused {
				return importState(cur)
			}
			if err := s.importTargetRole(r.Context(), tx, cur, p); err != nil {
				if !isForbidden(err) {
					return err
				}
				// The rights are gone since the import was asked for: it fails here, and its cleanup job removes what it staged.
				refused = err
				cur.State, cur.Phase, cur.PauseReason = store.ImportFailed, store.ImportPhaseCleanup, ""
				cur.ErrorCode, cur.Failure = string(apierr.Forbidden), string(apierr.Forbidden)
				if err := tx.InsertAudit(r.Context(), s.auditEntry(r, cur.AccountID, "import.resume", cur.ID,
					map[string]any{"job": cur.JobID, "state": cur.State}, cur.FolderID)); err != nil {
					return err
				}
				break
			}
			if _, err := tx.UnfinishedImport(r.Context(), cur.AccountID, cur.UserID, cur.Source); err == nil {
				return apierr.New(apierr.Conflict, "an import from this source is already running; wait for it or cancel it").
					With("reason", "too_many_imports")
			} else if !errors.Is(err, store.ErrNotFound) {
				return err
			}
			if err := s.checkImportSource(r.Context(), tx, cur.AccountID, cur.Source); err != nil {
				return err
			}
			cur.State, cur.PauseReason = store.ImportQueued, ""
			if err := tx.InsertAudit(r.Context(), s.auditEntry(r, cur.AccountID, "import.resume", cur.ID,
				map[string]any{"job": cur.JobID, "state": cur.State}, cur.FolderID)); err != nil {
				return err
			}
		case "cancel":
			if cur.State != store.ImportQueued && cur.State != store.ImportRunning && cur.State != store.ImportPaused {
				return importState(cur)
			}
			cur.State, cur.Phase = store.ImportCancelled, store.ImportPhaseCleanup
			if err := tx.InsertAudit(r.Context(), s.auditEntry(r, cur.AccountID, "import.cancel", cur.ID,
				map[string]any{"job": cur.JobID, "state": cur.State}, cur.FolderID)); err != nil {
				return err
			}
			cur.PauseReason = ""
		}
		queued, err := tx.ImportJobUnfinished(r.Context(), cur.ID)
		if err != nil {
			return err
		}
		if action != "pause" && !queued {
			// The import's job has ended, so a new one takes it on: a resume runs the import, a cancel removes what it staged.
			job = &store.Job{ID: id.New("job"), AccountID: cur.AccountID, Kind: jobImport, Target: cur.ID,
				Status: store.JobQueued, CreatedAt: now, UpdatedAt: now}
			body, err := marshalPayload(cur.Actor)
			if err != nil {
				return err
			}
			job.Payload = body
			if err := tx.InsertJob(r.Context(), job); err != nil {
				return err
			}
			cur.JobID = job.ID
		}
		if err := tx.SaveImport(r.Context(), cur, now); err != nil {
			return err
		}
		im = cur
		return nil
	})
	if err != nil {
		return err
	}
	if job != nil {
		s.jobs.Wake()
	}
	if refused != nil {
		return refused
	}
	writeJSON(w, http.StatusOK, toImportJSON(im))
	return nil
}

// isForbidden reports whether err is a refusal for the caller's role.
func isForbidden(err error) bool {
	var ae *apierr.Error
	return errors.As(err, &ae) && ae.Code == apierr.Forbidden
}

// importState refuses an action on an import in the state it is in.
func importState(im *store.Import) error {
	return apierr.New(apierr.Conflict, "the import is "+im.State+"; this action does not apply").With("state", im.State)
}

// handleSetArchiveSwitch turns the archive source on or off for an account. Only the account's owner (or an admin for it)
// may change it.
func (s *Server) handleSetArchiveSwitch(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	acc := r.PathValue("acc")
	if !p.ManagesAccount(acc) {
		return notFound("account")
	}
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if in.Enabled == nil {
		return apierr.New(apierr.InvalidRequest, "enabled must be true or false")
	}
	err := s.store.Write(r.Context(), func(tx *store.Tx) error {
		if err := tx.SetImportSwitch(r.Context(), acc, *in.Enabled); err != nil {
			return err
		}
		return tx.InsertAudit(r.Context(), s.auditEntry(r, acc, "imports.archive", acc,
			map[string]any{"enabled": *in.Enabled}))
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": store.SourceArchive, "enabled": *in.Enabled})
	return nil
}
