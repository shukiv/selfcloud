// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/jobs"
	"github.com/shukiv/selfcloud/internal/store"
)

// A stored zip (design section 14, slice s2, decision 35) is made by a job of kind node.zip, targeting the zip's row.
// The job reads the tree once, as a snapshot, before the first byte is written: the plan holds every entry, with the
// blob it reads, so the zip shows the tree as it was at snapshot_at. Its file is written as it goes, and the job
// yields its turn after zipYieldAfter (decision 34's model). The writer, the open file and the plan are kept in memory
// between turns, because archive/zip keeps the central directory until Close and cannot reopen a partial file. A
// restart loses them: the job then starts again from the snapshot, and removes the partial file.

const (
	jobZip = "node.zip"
	// zipMaxRoots is how many nodes one zip request may name.
	zipMaxRoots = 1000
	// zipMaxPath is the longest path an entry may have, in bytes. Longer ones fail the zip: none is cut silently.
	zipMaxPath = 4096
	// zipYieldAfter is how long a zip job runs in one turn before it yields the runner to the queue.
	zipYieldAfter = 30 * time.Second
)

// zipPayload is what a zip job carries: the caller it runs for. Its access is checked as that caller, every turn.
type zipPayload struct {
	Actor actorPayload `json:"actor"`
}

// zipEntry is one entry of a zip's plan: a directory, or a file with the node whose blob it reads.
type zipEntry struct {
	name string
	dir  bool
	node *store.Node
	mod  time.Time
}

// zipWork is a zip being made, kept in memory between the turns of its job.
type zipWork struct {
	file     *os.File
	zw       *zip.Writer
	sum      hash.Hash
	count    *zipCounter
	plan     []zipEntry
	cursor   int
	reserved int64
	skipped  int
	partPath string
	acc      string
	id       string
	caller   *auth.Principal
	roots    int
}

// zipCounter counts the bytes the zip writes, and feeds them to the hash.
type zipCounter struct {
	w io.Writer
	n int64
}

func (c *zipCounter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// zipRegistry is the zips being made, by zip ID. A job is run by one goroutine at a time, but requests and the sweep
// read the server beside it, so the map has a lock.
type zipRegistry struct {
	mu    sync.Mutex
	works map[string]*zipWork
}

func (r *zipRegistry) get(zipID string) *zipWork {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.works[zipID]
}

func (r *zipRegistry) put(w *zipWork) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.works == nil {
		r.works = map[string]*zipWork{}
	}
	r.works[w.id] = w
}

func (r *zipRegistry) drop(zipID string) *zipWork {
	r.mu.Lock()
	defer r.mu.Unlock()
	w := r.works[zipID]
	delete(r.works, zipID)
	return w
}

// zipStage is the fault-injection point of a zip job. Production never sets it.
func (s *Server) zipStage(stage string) error {
	if s.zipFault != nil {
		return s.zipFault(stage)
	}
	return nil
}

// errZipDisk is the refusal when the stored zips would pass zip.max_disk.
func errZipDisk() error {
	return apierr.New(apierr.StorageFull, "the stored zips would fill the disk budget; ask again once some have expired").
		With("reason", "zip_disk")
}

// errZipAccountDisk is the refusal when one account's stored zips would pass its share of zip.max_disk. It is not
// the installation's refusal: the reason says which share is full.
func errZipAccountDisk() error {
	return apierr.New(apierr.StorageFull, "this account's stored zips would pass its disk share; ask again once some have expired").
		With("reason", "zip_account_disk")
}

// handleCreateZip queues a zip of the nodes named. Each root must be readable by the caller and must not be restricted
// by its no-download rule (the copy's rule). A shortcut is not zipped: zip its target.
func (s *Server) handleCreateZip(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Nodes []string `json:"nodes"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if len(in.Nodes) < 1 || len(in.Nodes) > zipMaxRoots {
		return apierr.Newf(apierr.InvalidRequest, "nodes must name between 1 and %d items", zipMaxRoots)
	}
	seen := make(map[string]bool, len(in.Nodes))
	for _, nid := range in.Nodes {
		if !id.Valid("nod", nid) {
			return apierr.New(apierr.InvalidRequest, "nodes must hold node IDs")
		}
		if seen[nid] {
			return apierr.New(apierr.InvalidRequest, "a node is named twice")
		}
		seen[nid] = true
	}
	p := principalOf(r)
	if err := s.rateLimit("zip.create", string(p.Kind)+":"+p.ID); err != nil {
		return err
	}
	acc := ""
	for _, nid := range in.Nodes {
		n, role, err := s.nodeAccess(r, nid)
		if err != nil {
			return err
		}
		if n.Kind == store.KindShortcut {
			return apierr.New(apierr.InvalidRequest, "a shortcut is not zipped; zip the item it points to").
				With("reason", "shortcut")
		}
		restricted, err := s.copyRestricted(r.Context(), s.store, n, role)
		if err != nil {
			return err
		}
		if restricted {
			return errDownloadRestricted()
		}
		acc = n.AccountID
	}
	ctx := r.Context()
	zipID := id.New("zip")
	now := store.Now()
	var job *store.Job
	s.atWrite()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		mine, err := s.store.CountPendingZips(ctx, acc, p.ID)
		if err != nil {
			return err
		}
		if mine >= s.cfg.Zip.MaxPerPrincipal {
			return apierr.Newf(apierr.Conflict, "you may have %d unfinished zips at once; wait for one to finish", s.cfg.Zip.MaxPerPrincipal).
				With("reason", "too_many_zips")
		}
		all, err := s.store.CountPendingZips(ctx, acc, "")
		if err != nil {
			return err
		}
		if all >= s.cfg.Zip.MaxPerAccount {
			return apierr.Newf(apierr.Conflict, "the account may have %d unfinished zips at once; wait for one to finish", s.cfg.Zip.MaxPerAccount).
				With("reason", "too_many_zips_account")
		}
		total, err := s.store.CountAllPendingZips(ctx)
		if err != nil {
			return err
		}
		if total >= s.cfg.Zip.MaxUnfinished {
			busy := apierr.New(apierr.RateLimited, "the server is making its maximum number of zips; ask again shortly")
			busy.RetryAfter = 30 * time.Second
			return busy
		}
		z := &store.Zip{ID: zipID, AccountID: acc, CreatedBy: p.ID, CreatedKind: string(p.Kind), CreatedCred: zipCred(p),
			CreatedAt: now,
			Roots:     in.Nodes}
		if err := tx.InsertZip(ctx, z); err != nil {
			return err
		}
		body, err := json.Marshal(zipPayload{Actor: actorOf(p)})
		if err != nil {
			return err
		}
		job = &store.Job{ID: id.New("job"), AccountID: acc, Kind: jobZip, Target: zipID, Status: store.JobQueued,
			Payload: string(body), CreatedAt: now, UpdatedAt: now}
		return tx.InsertJob(ctx, job)
	})
	if err != nil {
		return err
	}
	s.jobs.Wake()
	w.Header().Set("Location", "/api/v1/jobs/"+job.ID)
	writeJSON(w, http.StatusAccepted, toJobJSON(job))
	return nil
}

// zipOwner returns the zip of the caller's account that the caller made, or not_found. A zip is for its maker only:
// another member of the account, and an admin, get the same answer as for a zip that does not exist.
func (s *Server) zipOwner(r *http.Request) (*store.Zip, *auth.Principal, error) {
	p := principalOf(r)
	zipID := r.PathValue("zip")
	if !id.Valid("zip", zipID) || p == nil || p.AccountID == "" {
		return nil, nil, notFound("zip")
	}
	z, err := s.store.Zip(r.Context(), p.AccountID, zipID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, notFound("zip")
	}
	if err != nil {
		return nil, nil, err
	}
	if z.CreatedBy != p.ID || z.CreatedKind != string(p.Kind) || z.CreatedCred != zipCred(p) {
		return nil, nil, notFound("zip")
	}
	return z, p, nil
}

// handleGetZip sends a finished zip to its maker. Access is checked on every call, including a Range resume: each root
// must still be readable by the caller and not trashed. The no-download rule is not checked again, as decision 35 says
// a zip stays until its expiry once it is made. A zip that is not ready yet answers 202.
func (s *Server) handleGetZip(w http.ResponseWriter, r *http.Request) error {
	z, p, err := s.zipOwner(r)
	if err != nil {
		return err
	}
	switch z.State {
	case store.ZipPending:
		w.Header().Set("Retry-After", "5")
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusAccepted, map[string]string{"state": "pending"})
		return nil
	case store.ZipReady:
	default:
		return notFound("zip")
	}
	if !z.ExpiresAt.After(store.Now()) {
		return notFound("zip")
	}
	var name string
	for i, root := range z.Roots {
		n, _, err := nodeAccessWith(r.Context(), s.store, p, root)
		if err != nil {
			return err
		}
		if i == 0 && len(z.Roots) == 1 {
			name = n.Name + ".zip"
		}
	}
	if name == "" {
		name = "download.zip"
	}
	path, err := s.zipFiles.Path(z.AccountID, z.ID)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return apierr.Wrap(apierr.Internal, "the zip's file is missing", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return apierr.Wrap(apierr.Internal, "the zip's file cannot be read", err)
	}
	if fi.Size() != z.Size {
		return apierr.Wrap(apierr.Internal, "the zip's file does not match its size",
			fmt.Errorf("zip %s holds %d bytes, the row says %d", z.ID, fi.Size(), z.Size))
	}
	etag := `"` + z.SHA256 + `"`
	mod := z.SnapshotAt.UTC().Truncate(time.Second)
	h := w.Header()
	h.Set("Cache-Control", "private, no-cache")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", contentCSP)
	h.Set("Cross-Origin-Resource-Policy", corpSameOrigin)
	h.Set("X-Selfcloud-Skipped", strconv.Itoa(z.Skipped))
	status, err := preconditions(r, etag, mod)
	if err != nil {
		return err
	}
	if status == http.StatusNotModified {
		setValidators(h, etag, mod)
		w.WriteHeader(status)
		return nil
	}
	if err := applyRange(h, r, etag, mod, z.Size); err != nil {
		return err
	}
	setValidators(h, etag, mod)
	h.Set("Accept-Ranges", "bytes")
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", contentDisposition(dispAttachment, name))
	http.ServeContent(w, r, name, mod, f)
	return nil
}

// handleDeleteZip removes a finished zip now, rather than at its expiry. A zip still being made is refused.
func (s *Server) handleDeleteZip(w http.ResponseWriter, r *http.Request) error {
	z, _, err := s.zipOwner(r)
	if err != nil {
		return err
	}
	if z.State == store.ZipPending {
		return apierr.New(apierr.Conflict, "the zip is still being made; delete it when it is ready").With("reason", "zip_pending")
	}
	if err := s.zipFiles.Remove(z.AccountID, z.ID); err != nil {
		return err
	}
	s.atWrite()
	err = s.store.Write(r.Context(), func(tx *store.Tx) error {
		_, err := tx.DeleteZip(r.Context(), z.AccountID, z.ID)
		return err
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// runZipJob makes a zip. It is safe to run again from the start: a job whose state is gone starts over.
func (s *Server) runZipJob(ctx context.Context, job *store.Job) error {
	z, err := s.store.Zip(ctx, job.AccountID, job.Target)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	switch z.State {
	case store.ZipReady:
		return nil
	case store.ZipFailed:
		return errors.New("the zip failed: " + z.Error)
	}
	var pl zipPayload
	if err := json.Unmarshal([]byte(job.Payload), &pl); err != nil {
		return fmt.Errorf("zip job payload: %w", err)
	}
	p := &auth.Principal{Kind: auth.Kind(pl.Actor.Kind), ID: pl.Actor.ID, AccountID: job.AccountID,
		UserID: pl.Actor.UserID, Role: pl.Actor.Role, Token: pl.Actor.Token}
	defer s.zipRecover(ctx, z)
	// Each turn checks the caller first, as a copy's batch does: a caller revoked since the last turn gets no zip.
	if err := s.zipGuard(ctx, z, p); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return s.zipFailed(ctx, z, s.zips.get(z.ID), err)
	}
	work := s.zips.get(z.ID)
	if work == nil {
		work, err = s.startZip(ctx, z, p)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return s.zipFailed(ctx, z, nil, err)
		}
	}
	// A turn writes at least one entry before it may yield, so a job always makes progress, however small zipYield is.
	deadline := time.Now().Add(s.zipYield)
	turnStart := work.cursor
	for work.cursor < len(work.plan) {
		if err := ctx.Err(); err != nil {
			return err // a shutdown keeps the row pending; the next start makes the zip again
		}
		if work.cursor > turnStart && time.Now().After(deadline) {
			if err := s.zipGuard(ctx, z, p); err != nil {
				return s.zipFailed(ctx, z, work, err)
			}
			return jobs.ErrYield
		}
		if err := s.zipStage("entry"); err != nil {
			return s.zipFailed(ctx, z, work, err)
		}
		if err := s.writeZipEntry(z.AccountID, work, work.plan[work.cursor]); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return s.zipFailed(ctx, z, work, err)
		}
		work.cursor++
	}
	if err := s.zipGuard(ctx, z, p); err != nil {
		return s.zipFailed(ctx, z, work, err)
	}
	if err := s.zipStage("publish"); err != nil {
		return s.zipFailed(ctx, z, work, err)
	}
	return s.publishZip(ctx, z, work)
}

// zipRefusedGone is the refusal of a zip whose caller is no longer live. Like the copy's, it is the job's failure.
const zipRefusedGone = "the caller of this zip no longer has access; no zip was made"

// zipGuard checks that the caller is still live, and that every root is still readable and not restricted. It runs at
// the start and the end of each turn, and before publish, as copyGuard does for a copy: a caller who lost access
// mid-job gets no zip.
func (s *Server) zipGuard(ctx context.Context, z *store.Zip, p *auth.Principal) error {
	if err := callerActive(ctx, s.store, p, z.AccountID, zipRefusedGone); err != nil {
		return err
	}
	for _, root := range z.Roots {
		n, role, err := nodeAccessWith(ctx, s.store, p, root)
		if err != nil {
			return err
		}
		restricted, err := s.copyRestricted(ctx, s.store, n, role)
		if err != nil {
			return err
		}
		if restricted {
			return errDownloadRestricted()
		}
	}
	return nil
}

// startZip reads the snapshot, checks the disk budget, and opens the partial file. Nothing is written before the
// snapshot is complete and within its limits.
func (s *Server) startZip(ctx context.Context, z *store.Zip, p *auth.Principal) (*zipWork, error) {
	plan, skipped, total, err := s.planZip(ctx, z, p)
	if err != nil {
		return nil, err
	}
	now := store.Now()
	fits := func(ready, reserved int64) bool { return ready+reserved+total <= int64(s.cfg.Zip.MaxDisk) }
	if ready, reserved, err := s.store.ZipDiskUse(ctx); err != nil {
		return nil, err
	} else if !fits(ready, reserved) {
		// Expired zips go first. Only the sweep's own pass removes them, so the same rule runs here.
		if _, err := s.zipFiles.Sweep(ctx, now, 1000); err != nil {
			s.log.Error("zip sweep before a zip", "error", err)
		}
	}
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		ready, reserved, err := tx.ZipDiskUse(ctx)
		if err != nil {
			return err
		}
		if !fits(ready, reserved) {
			return errZipDisk()
		}
		aReady, aReserved, err := tx.ZipDiskUseOf(ctx, z.AccountID)
		if err != nil {
			return err
		}
		if aReady+aReserved+total > int64(s.cfg.Zip.MaxDiskPerAccount) {
			return errZipAccountDisk()
		}
		return tx.SetZipSnapshot(ctx, z.ID, total, len(plan), skipped, now)
	})
	if err != nil {
		return nil, err
	}
	part, err := s.zipFiles.PartPath(z.AccountID, z.ID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dirOf(part), 0o700); err != nil {
		return nil, fmt.Errorf("zips: %w", err)
	}
	f, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("zips: %w", err)
	}
	sum := sha256.New()
	count := &zipCounter{w: io.MultiWriter(f, sum)}
	work := &zipWork{file: f, zw: zip.NewWriter(count), sum: sum, count: count, plan: plan, reserved: total,
		skipped: skipped, partPath: part, acc: z.AccountID, id: z.ID, caller: p, roots: len(z.Roots)}
	s.zips.put(work)
	return work, nil
}

func dirOf(path string) string {
	if i := strings.LastIndexByte(path, os.PathSeparator); i >= 0 {
		return path[:i]
	}
	return "."
}

// planZip walks the tree of every root, as the caller, and returns the plan: every entry, in an order where a folder
// comes before its contents. A node the caller cannot read is left out and counted; a node the no-download rule
// restricts for the caller fails the zip, as a copy does; a shortcut is left out and counted, and never followed.
// Trashed nodes are not reached. The limits are checked as the plan grows, so a refused zip writes nothing.
func (s *Server) planZip(ctx context.Context, z *store.Zip, p *auth.Principal) ([]zipEntry, int, int64, error) {
	var (
		plan    []zipEntry
		skipped int
		total   int64
	)
	topNames := nameSet{}
	read := 0 // every node read so far, across roots, skipped for access or not: the zip's limit is on the walk
	for _, rootID := range z.Roots {
		root, role, err := nodeAccessWith(ctx, s.store, p, rootID)
		if err != nil {
			return nil, 0, 0, err
		}
		restricted, err := s.copyRestricted(ctx, s.store, root, role)
		if err != nil {
			return nil, 0, 0, err
		}
		if restricted {
			return nil, 0, 0, errDownloadRestricted()
		}
		if root.Kind == store.KindShortcut {
			return nil, 0, 0, apierr.New(apierr.InvalidRequest, "a shortcut is not zipped; zip the item it points to")
		}
		// One row more than the budget left: a subtree that passes the limit is refused by its count, and the query never
		// reads more than that. The budget is at least 1 here, since read never passes the limit.
		tree, err := s.store.ZipTree(ctx, z.AccountID, rootID, s.cfg.Zip.MaxEntries-read+1)
		if err != nil {
			return nil, 0, 0, err
		}
		read += len(tree)
		if read > s.cfg.Zip.MaxEntries {
			return nil, 0, 0, s.entriesTooMany()
		}
		children := map[string][]*store.Node{}
		for _, n := range tree {
			if n.ID != rootID {
				children[n.ParentID] = append(children[n.ParentID], n)
			}
		}
		for k := range children {
			sort.Slice(children[k], func(i, j int) bool { return children[k][i].NameKey < children[k][j].NameKey })
		}
		// The root's own entry, then its contents breadth first, so a folder precedes its children.
		type item struct {
			n    *store.Node
			path string
		}
		rootName := topNames.unique(zipComponent(root.Name))
		queue := []item{{n: root, path: rootName}}
		if err := s.addZipEntry(&plan, &total, root, rootName); err != nil {
			return nil, 0, 0, err
		}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			siblings := nameSet{}
			for _, c := range children[cur.n.ID] {
				if err := ctx.Err(); err != nil {
					return nil, 0, 0, err
				}
				cn, crole, err := s.zipAccess(ctx, p, c.ID)
				if err != nil {
					if !deniedAccess(err) {
						return nil, 0, 0, err
					}
					// A node the caller cannot read was not shown to it: it is left out, with what is under it.
					skipped += subtreeSize(children, c.ID)
					continue
				}
				restricted, err := s.downloadRestricted(ctx, cn, crole)
				if err != nil {
					return nil, 0, 0, err
				}
				if restricted {
					return nil, 0, 0, errDownloadRestricted()
				}
				if cn.Kind == store.KindShortcut {
					skipped++
					continue
				}
				name := siblings.unique(zipComponent(cn.Name))
				path := cur.path + "/" + name
				if len(path) > zipMaxPath {
					return nil, 0, 0, apierr.New(apierr.TooLarge, "a path in the zip is longer than 4096 bytes; rename it")
				}
				if err := s.addZipEntry(&plan, &total, cn, path); err != nil {
					return nil, 0, 0, err
				}
				if cn.Kind == store.KindDir {
					queue = append(queue, item{n: cn, path: path})
				}
			}
		}
	}
	return plan, skipped, total, nil
}

// addZipEntry appends one entry to the plan, and refuses the zip when it passes a limit.
// entriesTooMany is the refusal of a zip that holds, or walks, more nodes than zip.max_entries.
func (s *Server) entriesTooMany() error {
	return apierr.Newf(apierr.TooLarge, "a zip may hold at most %d entries", s.cfg.Zip.MaxEntries).
		With("limit_entries", s.cfg.Zip.MaxEntries)
}

func (s *Server) addZipEntry(plan *[]zipEntry, total *int64, n *store.Node, path string) error {
	if len(*plan)+1 > s.cfg.Zip.MaxEntries {
		return s.entriesTooMany()
	}
	if n.Kind == store.KindDir {
		*plan = append(*plan, zipEntry{name: path + "/", dir: true, mod: n.ModifiedAt.UTC()})
		return nil
	}
	if *total+n.Size > int64(s.cfg.Zip.MaxBytes) {
		return apierr.Newf(apierr.TooLarge, "a zip may hold at most %d bytes of files", int64(s.cfg.Zip.MaxBytes)).
			With("limit_bytes", int64(s.cfg.Zip.MaxBytes))
	}
	*total += n.Size
	*plan = append(*plan, zipEntry{name: path, node: n, mod: n.ModifiedAt.UTC()})
	return nil
}

// writeZipEntry writes one entry. A file is read from its blob, the one the snapshot named: its size and its SHA-256
// must match the node and the blob, or the zip fails. The bytes are read in pieces, never whole.
func (s *Server) writeZipEntry(accountID string, w *zipWork, e zipEntry) error {
	hdr := &zip.FileHeader{Name: e.name, Modified: e.mod}
	if e.dir {
		hdr.Method = zip.Store
		_, err := w.zw.CreateHeader(hdr)
		return err
	}
	n := e.node
	hdr.Method = zip.Deflate
	if n.SHA256 == "" {
		return errors.New("a file has no checksum to check, so it cannot be zipped")
	}
	f, err := s.blobs.Open(accountID, n.BlobID)
	if err != nil {
		return errors.New("a file's contents are missing")
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return errors.New("a file's contents cannot be read")
	}
	if fi.Size() != n.Size {
		return errors.New("a file's contents do not match its size")
	}
	h := sha256.New()
	dst, err := w.zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	if _, err := io.CopyN(dst, io.TeeReader(f, h), n.Size); err != nil {
		return errors.New("a file's contents could not be read in full")
	}
	if hex.EncodeToString(h.Sum(nil)) != n.SHA256 {
		return errors.New("a file's contents do not match its checksum")
	}
	return nil
}

// publishZip closes the archive, moves the file into place and marks the zip ready, in one write, after the disk budget
// is checked with the zip's real size.
func (s *Server) publishZip(ctx context.Context, z *store.Zip, w *zipWork) error {
	if err := w.zw.Close(); err != nil {
		return s.zipFailed(ctx, z, w, err)
	}
	if err := w.file.Sync(); err != nil {
		return s.zipFailed(ctx, z, w, errors.New("the zip could not be written to disk"))
	}
	if err := w.file.Close(); err != nil {
		return s.zipFailed(ctx, z, w, errors.New("the zip could not be written to disk"))
	}
	size := w.count.n
	sha := hex.EncodeToString(w.sum.Sum(nil))
	final, err := s.zipFiles.Path(z.AccountID, z.ID)
	if err != nil {
		return s.zipFailed(ctx, z, w, err)
	}
	if err := os.Rename(w.partPath, final); err != nil {
		return s.zipFailed(ctx, z, w, errors.New("the zip could not be put in place"))
	}
	now := store.Now()
	expires := now.Add(time.Duration(s.cfg.Zip.TTL))
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		// The caller is checked inside the write that publishes, so a revoke is seen before the zip is ready.
		if err := callerActive(ctx, tx, w.caller, z.AccountID, zipRefusedGone); err != nil {
			return err
		}
		ready, reserved, err := tx.ZipDiskUse(ctx)
		if err != nil {
			return err
		}
		others := reserved - w.reserved
		if ready+others+size > int64(s.cfg.Zip.MaxDisk) {
			return errZipDisk()
		}
		aReady, aReserved, err := tx.ZipDiskUseOf(ctx, z.AccountID)
		if err != nil {
			return err
		}
		if aReady+(aReserved-w.reserved)+size > int64(s.cfg.Zip.MaxDiskPerAccount) {
			return errZipAccountDisk()
		}
		ok, err := tx.CompleteZip(ctx, z.ID, size, sha, expires)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("the zip was not pending when it was finished")
		}
		return tx.InsertAudit(ctx, &store.AuditEntry{AccountID: z.AccountID, ActorKind: string(w.caller.Kind),
			ActorID: w.caller.ID, Action: "node.zip", Target: z.ID, Time: now,
			Details: map[string]any{"nodes": w.roots, "entries": len(w.plan), "skipped": w.skipped, "bytes": size}})
	})
	if err != nil {
		_ = os.Remove(final)
		return s.zipFailed(ctx, z, nil, err)
	}
	s.zips.drop(z.ID)
	return nil
}

// zipFailed records a zip as failed, removes its files, and returns err for the job. A shutdown is not a failure: the
// caller returns the context's error instead, and the zip is made again on the next start.
func (s *Server) zipFailed(ctx context.Context, z *store.Zip, w *zipWork, cause error) error {
	if w != nil && w.file != nil {
		_ = w.file.Close()
	}
	s.zips.drop(z.ID)
	if err := s.zipFiles.Remove(z.AccountID, z.ID); err != nil {
		s.log.Error("cannot remove a failed zip's files", "zip", z.ID, "error", err)
	}
	reason := "the zip could not be made"
	if ae := apierr.From(cause); ae != nil && ae.Message != "" {
		reason = ae.Message
	} else if cause != nil && !errors.Is(cause, context.Canceled) && !errors.Is(cause, context.DeadlineExceeded) {
		reason = cause.Error()
	}
	// The code and reason of a refusal are kept apart from the text, so the job route can name them to the interface.
	var code, errReason string
	var refusal *apierr.Error
	if errors.As(cause, &refusal) {
		code = string(refusal.Code)
		errReason, _ = refusal.Details["reason"].(string)
	}
	s.atWrite()
	if err := s.store.Write(context.WithoutCancel(ctx), func(tx *store.Tx) error {
		return tx.FailZip(ctx, z.ID, code, errReason, reason)
	}); err != nil {
		s.log.Error("cannot record a failed zip", "zip", z.ID, "error", err)
	}
	return cause
}

// zipComponent makes one path element of a zip from a node name. Node names are checked when they are made, so this
// changes nothing for a name the server accepted; it is the second line of defence for any name that reaches it. A
// path separator, a colon, a control character, a format character (bidi overrides included) or a line separator
// becomes "_". A name that is "." or ".." becomes "_", so no entry can climb out of the zip's top folder.
func zipComponent(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '/' || r == '\\' || r == ':':
			b.WriteByte('_')
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r):
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	out := norm.NFC.String(b.String())
	if out == "" || out == "." || out == ".." {
		return "_"
	}
	return cutBytes(out, 255)
}

// cutBytes shortens s to at most n bytes, at a character boundary.
func cutBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !isRuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// nameSet gives sibling names distinct keys: a second "Report" becomes "Report (1)", and a name that folds to an
// existing key is treated as the same name. The key is the name case-folded and in NFC, as the store's keys are.
type nameSet map[string]bool

func foldKey(s string) string { return norm.NFC.String(cases.Fold().String(s)) }

func (ns nameSet) unique(name string) string {
	if !ns[foldKey(name)] {
		ns[foldKey(name)] = true
		return name
	}
	stem, ext := name, ""
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		stem, ext = name[:i], name[i:]
	}
	for k := 1; ; k++ {
		cand := fitName(stem, ext, fmt.Sprintf(" (%d)", k))
		if !ns[foldKey(cand)] {
			ns[foldKey(cand)] = true
			return cand
		}
	}
}

// fitName joins stem, suffix and extension into at most 255 bytes. The suffix is kept. When the stem and the extension
// do not fit, the extension is cut first, to leave at least one byte of stem, and then the stem.
func fitName(stem, ext, suffix string) string {
	room := 255 - len(suffix)
	if len(stem)+len(ext) > room {
		if len(ext) > room-1 {
			ext = cutBytes(ext, room-1)
		}
		stem = cutBytes(stem, room-len(ext))
	}
	return stem + suffix + ext
}

// deniedAccess reports whether an access error is a refusal of the caller (the node is not there for it, or it may not
// read it), as opposed to a failure of the server.
func deniedAccess(err error) bool {
	ae := apierr.From(err)
	return ae != nil && (ae.Code == apierr.NotFound || ae.Code == apierr.Forbidden)
}

// subtreeSize counts id and every node under it in children.
func subtreeSize(children map[string][]*store.Node, id string) int {
	n := 0
	stack := []string{id}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n++
		for _, c := range children[cur] {
			stack = append(stack, c.ID)
		}
	}
	return n
}

// zipAccess is nodeAccessWith for the walk. A test sets zipWalkFault to make a node fail as if the store failed.
func (s *Server) zipAccess(ctx context.Context, p *auth.Principal, nodeID string) (*store.Node, string, error) {
	if s.zipWalkFault != nil {
		if err := s.zipWalkFault(nodeID); err != nil {
			return nil, "", err
		}
	}
	return nodeAccessWith(ctx, s.store, p, nodeID)
}

// zipRecover runs when a zip job panics. The work is released and the row failed, the same as for any other failure;
// then the panic goes on, so the runner records the job as it does for every panic.
func (s *Server) zipRecover(ctx context.Context, z *store.Zip) {
	if r := recover(); r != nil {
		_ = s.zipFailed(ctx, z, s.zips.get(z.ID), fmt.Errorf("the zip job panicked: %v", r))
		panic(r)
	}
}

// zipCred is the credential a zip is made with: the fingerprint of the delegated token it came in on, or "" for any
// other credential. Two tokens of one user are told apart by their mint, so a token reads only its own zips.
func zipCred(p *auth.Principal) string {
	if p == nil || p.Token == nil {
		return ""
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s|%d|%d|%t|%t|%s|%s|%s", p.ID, p.UserID, p.Token.MintedBy, p.Token.IssuedAt.UnixNano(),
		p.Token.ExpiresAt.UnixNano(), p.Token.All, p.Token.Write, p.Token.Node, p.Token.Audience,
		strings.Join(p.Token.Folders, ","))
	return hex.EncodeToString(h.Sum(nil))
}
