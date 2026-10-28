// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/fsutil"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/names"
	"github.com/shukiv/selfcloud/internal/store"
)

// Resumable uploads follow tus 1.0.0 (design section 5). The protocol is built in: creation,
// creation-with-upload, expiration, checksum and termination.
//
// Bytes go straight into uploads/<account>/<upl> at the durable offset, through a 1 MiB buffer.
// After each piece the file is synced, and only then is the new offset recorded. The offset and the
// SHA-256 state recorded with it are the only truth: bytes past the offset are discarded before the
// next piece is written.

const (
	tusVersion    = "1.0.0"
	tusExtensions = "creation,creation-with-upload,expiration,checksum,termination"
	// uploadBodyType is the media type of a tus piece.
	uploadBodyType = "application/offset+octet-stream"
	// maxUploadMetadata bounds the Upload-Metadata header before it is parsed.
	maxUploadMetadata = 8 << 10
	// nodeHeader names the file a completing request made, since a 204 has no body.
	nodeHeader = "Selfcloud-Node-ID"
	// stateHeader says an upload is waiting for a name (awaiting_commit).
	stateHeader = "Selfcloud-Upload-State"
	// chunkHeader advertises upload.max_chunk_size, so clients behind a proxy pick a piece size.
	chunkHeader = "Selfcloud-Max-Chunk-Size"
)

// tusHeaders lists every header the upload routes read or write. The routes test checks that the
// OpenAPI document mentions each of them on the upload operations.
var tusHeaders = []string{"Tus-Resumable", "Tus-Version", "Tus-Extension", "Tus-Max-Size",
	"Tus-Checksum-Algorithm", "Upload-Offset", "Upload-Length", "Upload-Metadata", "Upload-Expires",
	"Upload-Checksum", chunkHeader, nodeHeader, stateHeader, "Location"}

// uploadBufs are the 1 MiB copy buffers of PATCH bodies. Pooled, so memory stays flat per upload.
var uploadBufs = sync.Pool{New: func() any {
	b := make([]byte, 1<<20)
	return &b
}}

// tusVersionOK sets Tus-Resumable and refuses a request that does not speak tus 1.0.0.
func tusVersionOK(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Tus-Resumable", tusVersion)
	if r.Header.Get("Tus-Resumable") != tusVersion {
		w.Header().Set("Tus-Version", tusVersion)
		return apierr.New(apierr.PreconditionFailed, "Tus-Resumable must be 1.0.0")
	}
	return nil
}

// handleUploadOptions advertises the protocol and the limits. It needs no credential.
func (s *Server) handleUploadOptions(w http.ResponseWriter, r *http.Request) error {
	h := w.Header()
	h.Set("Tus-Resumable", tusVersion)
	h.Set("Tus-Version", tusVersion)
	h.Set("Tus-Extension", tusExtensions)
	h.Set("Tus-Checksum-Algorithm", "sha1,sha256")
	h.Set("Tus-Max-Size", strconv.FormatInt(int64(s.cfg.Upload.MaxFileSize), 10))
	h.Set(chunkHeader, strconv.FormatInt(int64(s.cfg.Upload.MaxChunkSize), 10))
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- headers and metadata ----

// parseDecimal reads a non-negative decimal header value.
func parseDecimal(field, v string) (int64, error) {
	if v == "" || len(v) > 19 {
		return 0, apierr.Newf(apierr.InvalidRequest, "%s must be a non-negative integer", field)
	}
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return 0, apierr.Newf(apierr.InvalidRequest, "%s must be a non-negative integer", field)
		}
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, apierr.Newf(apierr.InvalidRequest, "%s must be a non-negative integer", field)
	}
	return n, nil
}

// parseUploadMetadata reads the tus Upload-Metadata header: comma-separated "key base64value" pairs,
// where the value is optional. Keys must be unique. Unknown keys are kept and ignored, as tus clients
// send their own.
func parseUploadMetadata(h string) (map[string]string, error) {
	out := map[string]string{}
	if h == "" {
		return out, nil
	}
	if len(h) > maxUploadMetadata {
		return nil, apierr.Newf(apierr.InvalidRequest, "Upload-Metadata is longer than %d bytes", maxUploadMetadata)
	}
	for _, pair := range strings.Split(h, ",") {
		key, val, _ := strings.Cut(strings.TrimSpace(pair), " ")
		if key == "" {
			return nil, apierr.New(apierr.InvalidRequest, "Upload-Metadata has an empty key")
		}
		for i := 0; i < len(key); i++ {
			if key[i] <= ' ' || key[i] > '~' {
				return nil, apierr.New(apierr.InvalidRequest, "Upload-Metadata keys are ASCII without spaces")
			}
		}
		if _, dup := out[key]; dup {
			return nil, apierr.Newf(apierr.InvalidRequest, "Upload-Metadata repeats the key %s", key)
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(val))
		if err != nil {
			return nil, apierr.Newf(apierr.InvalidRequest, "Upload-Metadata value of %s is not base64", key)
		}
		out[key] = string(raw)
	}
	return out, nil
}

// chunkSum is a per-piece checksum from Upload-Checksum: "<algorithm> <base64 digest>".
type chunkSum struct {
	alg    string
	digest []byte
}

// parseChecksum reads Upload-Checksum. An empty header is no checksum.
func parseChecksum(h string) (*chunkSum, error) {
	if h == "" {
		return nil, nil
	}
	alg, val, ok := strings.Cut(strings.TrimSpace(h), " ")
	if !ok {
		return nil, apierr.New(apierr.InvalidRequest, "Upload-Checksum is \"<algorithm> <base64 digest>\"")
	}
	want := map[string]int{"sha256": sha256.Size, "sha1": sha1.Size}[alg]
	if want == 0 {
		return nil, apierr.New(apierr.InvalidRequest, "Upload-Checksum algorithm must be sha256 or sha1")
	}
	digest, err := base64.StdEncoding.DecodeString(val)
	if err != nil || len(digest) != want {
		return nil, apierr.New(apierr.InvalidRequest, "Upload-Checksum digest is not valid base64 for the algorithm")
	}
	return &chunkSum{alg: alg, digest: digest}, nil
}

func (c *chunkSum) newHash() hash.Hash {
	if c.alg == "sha1" {
		return sha1.New()
	}
	return sha256.New()
}

// uploadTarget is what Upload-Metadata names: where the file goes and how a clash is settled.
type uploadTarget struct {
	folder, parent, dirPath, name, nameKey, onConflict, mime, sha string
	parents                                                       bool
}

// uploadAccess checks the role on an upload's target. An upload aimed at a directory by ID needs
// minRole on that directory. One aimed at a path needs it on the deepest directory that exists on
// the path, since the rest is created under that one. The folder check is folded into both.
func uploadAccess(ctx context.Context, rd accessReader, p *auth.Principal, up *store.Upload, minRole string) error {
	if up.ParentID != "" {
		_, err := requireNodeRole(ctx, rd, p, up.ParentID, minRole)
		return err
	}
	segs, err := parsePath(up.DirPath)
	if err != nil {
		return err
	}
	_, _, err = writeAnchor(ctx, rd, p, up.FolderID, segs, minRole)
	return err
}

// readTarget checks the metadata fields with the same rules as the JSON API: IDs, paths and names.
func readTarget(meta map[string]string) (*uploadTarget, error) {
	t := &uploadTarget{folder: meta["folder"], parent: meta["parent"], onConflict: meta["on_conflict"]}
	if err := validID("fld", t.folder); err != nil {
		return nil, err
	}
	if t.parent != "" {
		if err := validID("nod", t.parent); err != nil {
			return nil, err
		}
	}
	if path, ok := meta["path"]; ok && path != "" {
		if t.parent != "" {
			return nil, apierr.New(apierr.InvalidRequest, "give parent or path, not both")
		}
		segs, err := parsePath(path)
		if err != nil {
			return nil, err
		}
		parts := make([]string, 0, len(segs))
		for _, sg := range segs {
			parts = append(parts, sg.name)
		}
		t.dirPath = strings.Join(parts, "/")
	}
	nameIn, ok := meta["name"]
	if !ok {
		nameIn = meta["filename"] // the tus-js-client and Uppy convention
	}
	name, key, err := names.Clean(nameIn)
	if err != nil {
		return nil, err
	}
	t.name, t.nameKey = name, key
	switch t.onConflict {
	case "":
		t.onConflict = "fail"
	case "fail", "rename", "replace":
	default:
		return nil, apierr.New(apierr.InvalidRequest, "on_conflict must be fail, rename or replace")
	}
	if v, ok := meta["parents"]; ok && v != "" {
		if t.parents, err = strconv.ParseBool(v); err != nil {
			return nil, apierr.New(apierr.InvalidRequest, "parents must be true or false")
		}
	}
	if v := meta["mime_type"]; v != "" {
		mt, _, err := mime.ParseMediaType(v)
		if err != nil || len(mt) > maxMimeLength {
			return nil, apierr.New(apierr.InvalidRequest, "mime_type is not a media type")
		}
		t.mime = mt
	}
	if v := meta["sha256"]; v != "" {
		raw, err := hex.DecodeString(v)
		if err != nil || len(raw) != sha256.Size {
			return nil, apierr.New(apierr.InvalidRequest, "sha256 must be the 64 hex digits of the file's SHA-256")
		}
		t.sha = hex.EncodeToString(raw)
	}
	return t, nil
}

// restoreHash returns the SHA-256 of the first offset bytes, from the state recorded with them.
func restoreHash(state []byte, offset int64) (hash.Hash, error) {
	h := sha256.New()
	if offset == 0 {
		return h, nil
	}
	u, ok := h.(encoding.BinaryUnmarshaler)
	if !ok || len(state) == 0 {
		return nil, errors.New("upload has no hash state for its offset")
	}
	if err := u.UnmarshalBinary(state); err != nil {
		return nil, err
	}
	return h, nil
}

// ---- access to an upload ----

// uploadDeadline is when an upload expires, given that it was just created, or just written, at now:
// upload.expiry after now, but never upload.max_lifetime after its creation.
func (s *Server) uploadDeadline(created, now time.Time) time.Time {
	idle := now.Add(s.cfg.Upload.Expiry.D())
	if hard := created.Add(s.cfg.Upload.MaxLifetime.D()); hard.Before(idle) {
		return hard
	}
	return idle
}

// ownedUpload loads an upload for its creator. Anyone else gets not_found, so upload IDs cannot be
// probed. An upload past its expiry, or past upload.max_lifetime, is upload_expired.
func (s *Server) ownedUpload(r *http.Request, p *auth.Principal, uplID string) (*store.Upload, error) {
	if !id.Valid("upl", uplID) {
		return nil, notFound("upload")
	}
	up, err := s.store.Upload(r.Context(), p.AccountID, uplID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound("upload")
	}
	if err != nil {
		return nil, err
	}
	if up.CreatedBy != p.ID || up.CreatedKind != string(p.Kind) {
		return nil, notFound("upload")
	}
	now := store.Now()
	if !up.ExpiresAt.After(now) || !now.Before(up.CreatedAt.Add(s.cfg.Upload.MaxLifetime.D())) {
		return nil, apierr.New(apierr.UploadExpired, "the upload expired; start it again")
	}
	return up, nil
}

// lockUpload takes the single PATCH slot of an upload. It returns false when another request
// holds it. The slot is per process, which suits a server that is not shared between processes.

// ---- creating ----

// uploadDirNow resolves the directory an upload lands in, read-only, for the early checks at
// creation. It returns nil when the directory does not exist yet and parents is set.
func (s *Server) uploadDirNow(r *http.Request, up *store.Upload) (*store.Node, error) {
	ctx := r.Context()
	if up.ParentID != "" {
		n, err := s.store.Node(ctx, up.AccountID, up.ParentID)
		if errors.Is(err, store.ErrNotFound) || (err == nil && n.FolderID != up.FolderID) {
			return nil, notFound("parent")
		}
		if err != nil {
			return nil, err
		}
		if n.Kind != store.KindDir {
			return nil, apierr.New(apierr.InvalidRequest, "the parent is a file, not a directory")
		}
		return n, nil
	}
	root, err := s.store.RootNode(ctx, up.AccountID, up.FolderID)
	if err != nil {
		return nil, err
	}
	if up.DirPath == "" {
		return root, nil
	}
	segs, err := parsePath(up.DirPath)
	if err != nil {
		return nil, err
	}
	dir, err := walkDirs(ctx, func(ctx context.Context, parentID, key string) (*store.Node, error) {
		return s.store.Child(ctx, up.AccountID, parentID, key)
	}, root, segs)
	if err != nil {
		return nil, fileOnPath(ctx, s.store, principalOf(r), err, "parent directory")
	}
	if dir == nil && !up.Parents {
		return nil, notFound("parent directory")
	}
	return dir, nil
}

// handleCreateUpload starts an upload (tus creation). The body may carry the first piece
// (creation-with-upload). The caller needs write on the target folder.

// discardUploadFile removes an upload's file after its row was not committed. The sweep would
// remove it after the expiry anyway; this is just sooner.

// isUploadBody reports whether a request carries a tus piece.
func isUploadBody(r *http.Request) bool {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return mt == uploadBodyType
}

// checkPieceSize refuses a piece over upload.max_chunk_size, or one that would pass the declared
// length, before its body is read. An unknown length (-1) is checked while streaming.
func (s *Server) checkPieceSize(contentLength, remaining int64) error {
	maxChunk := int64(s.cfg.Upload.MaxChunkSize)
	if contentLength > maxChunk {
		return chunkTooLarge(maxChunk)
	}
	if contentLength > remaining {
		return tooLargeUpload(remaining)
	}
	return nil
}

func chunkTooLarge(maxChunk int64) error {
	return apierr.Newf(apierr.ChunkTooLarge, "a piece may be at most %d bytes", maxChunk).
		With("max_chunk_size", maxChunk)
}

func tooLargeUpload(remaining int64) error {
	return apierr.New(apierr.TooLarge, "the piece goes past the declared length of the upload").
		With("limit_bytes", remaining)
}

// ---- pieces ----

// chunkWriter writes a piece at its offset and feeds the running hashes with exactly the bytes that
// reached the file.
type chunkWriter struct {
	f   *os.File
	off int64
	h   hash.Hash
	ch  hash.Hash // the piece's own checksum, when the client sent one
}

func (c *chunkWriter) Write(p []byte) (int, error) {
	n, err := c.f.WriteAt(p, c.off)
	c.off += int64(n)
	c.h.Write(p[:n])
	if c.ch != nil {
		c.ch.Write(p[:n])
	}
	return n, err
}

// appendChunk streams the request body into the upload at its durable offset, then syncs and
// records the new offset. A body that stops early keeps the bytes that arrived: they are synced and
// recorded, and the error is returned. A piece that fails its checksum or passes its limits is
// removed again, so nothing is appended. The caller holds the upload's PATCH slot.

func marshalHash(h hash.Hash) ([]byte, error) {
	m, ok := h.(encoding.BinaryMarshaler)
	if !ok {
		return nil, errors.New("hash cannot be saved")
	}
	return m.MarshalBinary()
}

// recordPiece syncs the file and then records the new offset, in a transaction that checks access
// again. The upload struct is updated when the record is committed.
func (s *Server) recordPiece(ctx context.Context, p *auth.Principal, up *store.Upload, f *os.File,
	from, to int64, state []byte, expires time.Time) error {
	if err := fsutil.Datasync(f); err != nil {
		return apierr.Wrap(apierr.Internal, "the piece could not be synced to disk", err)
	}
	now := store.Now()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		if err := uploadAccess(ctx, tx, p, up, store.RoleContributor); err != nil {
			return err
		}
		if err := tx.RecordUploadProgress(ctx, up.AccountID, up.ID, from, to, state, now, expires); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return notFound("upload")
			}
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	up.DurableOffset, up.HashState, up.ExpiresAt, up.UpdatedAt = to, state, expires, now
	return nil
}

// handlePatchUpload appends a piece (tus PATCH). It answers 204 with the new Upload-Offset. A piece
// that completes the upload also completes the file; the new node's ID comes back in a header.

// ---- completing ----

// completeUpload turns a finished upload into a file. It checks the whole-file SHA-256 if the client
// gave one, then, in one transaction: it re-checks access on the folder the upload row names, moves
// the file into blobs/ (idempotent, so a retry after a crash is safe), records the blob and creates
// or replaces the node, updates usage, audits, and deletes the upload. A name clash under fail
// leaves the upload complete and waiting.

// uploadParentTx resolves the target directory inside the completion transaction, creating missing
// directories when the upload asked for parents.
func (s *Server) uploadParentTx(r *http.Request, tx *store.Tx, p *auth.Principal, up *store.Upload) (*store.Node, error) {
	ctx := r.Context()
	if up.ParentID != "" {
		n, err := tx.Node(ctx, up.AccountID, up.ParentID)
		if errors.Is(err, store.ErrNotFound) || (err == nil && n.FolderID != up.FolderID) {
			return nil, notFound("parent")
		}
		if err != nil {
			return nil, err
		}
		if n.Kind != store.KindDir {
			return nil, apierr.New(apierr.InvalidRequest, "the parent is a file, not a directory")
		}
		return n, nil
	}
	root, err := tx.RootNode(ctx, up.AccountID, up.FolderID)
	if err != nil {
		return nil, err
	}
	if up.DirPath == "" {
		return root, nil
	}
	segs, err := parsePath(up.DirPath)
	if err != nil {
		return nil, err
	}
	return s.walkDirsTx(r, tx, up.AccountID, up.FolderID, root, segs, up.Parents, p.ID)
}

// ---- reading, deleting ----

// handleHeadUpload reports how far an upload got, for resuming. Only its creator may ask.

// handleDeleteUpload terminates an upload: its row and its file go at once. It needs write on the
// folder, and waits its turn behind a piece in progress.

// uploadSlot is the PATCH slot of one upload while a request holds it.
type uploadSlot struct {
	stop     func()        // makes the holder's pending body read fail
	released chan struct{} // closed when the holder gives the slot back
}

// takeUploadSlot takes the slot of an upload for the request that owns w. A slot held by a request
// that has stopped sending (a half-open connection, say) is asked to stop, and this request waits up
// to uploadSlotWait for it to let go. A holder that does not let go in time keeps the slot, and this
// request gets busy (409 locked). The returned func gives the slot back.
func (s *Server) takeUploadSlot(w http.ResponseWriter, uplID string) (func(), error) {
	deadline := time.Now().Add(s.uploadSlotWait)
	for {
		s.uploadSlotMu.Lock()
		cur := s.uploadSlots[uplID]
		if cur == nil {
			me := &uploadSlot{released: make(chan struct{})}
			// Setting a read deadline in the past fails the holder's body read at once, which is what
			// stops a request whose client has vanished without closing the connection.
			me.stop = func() { _ = http.NewResponseController(w).SetReadDeadline(time.Now()) }
			s.uploadSlots[uplID] = me
			s.uploadSlotMu.Unlock()
			return func() {
				s.uploadSlotMu.Lock()
				if s.uploadSlots[uplID] == me {
					delete(s.uploadSlots, uplID)
				}
				s.uploadSlotMu.Unlock()
				close(me.released)
			}, nil
		}
		s.uploadSlotMu.Unlock()
		cur.stop()
		wait := time.Until(deadline)
		if wait <= 0 {
			return nil, busyError()
		}
		timer := time.NewTimer(wait)
		select {
		case <-cur.released:
			timer.Stop()
		case <-timer.C:
			return nil, busyError()
		}
	}
}

func busyError() error {
	return apierr.New(apierr.Locked, "another request is writing this upload; retry after it finishes")
}

// diskUsable is the disk space uploads may still be promised: the free space of the data directory
// less storage.min_free. ok is false where free space cannot be read, and then no disk check is made.
func (s *Server) diskUsable() (usable int64, ok bool, err error) {
	free, err := s.freeBytes(s.cfg.DataDir)
	if errors.Is(err, fsutil.ErrUnsupported) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, apierr.Wrap(apierr.Internal, "the free disk space could not be read", err)
	}
	return max(0, int64(free)-int64(s.cfg.Storage.MinFree)), true, nil
}

// checkDisk refuses uploads the disk cannot hold. mine is the unwritten bytes of the caller's
// account and total those of every account; usable is from diskUsable. Two rules:
//
//   - the whole pool, total, must fit in usable: the disk must hold what every open upload still
//     has to write (install-wide);
//   - when creating, mine must stay within upload.max_disk_share_percent of usable, so that no one
//     account can take the whole pool and refuse every other account's uploads.
//
// Each refusal is storage_full, with details.scope "server" or "account".
func (s *Server) checkDisk(usable, mine, total int64, creating bool) error {
	if total > usable {
		return apierr.New(apierr.StorageFull, "the server's disk does not have room for this upload").
			With("required_bytes", total).With("available_bytes", usable).With("scope", "server")
	}
	if creating {
		share := usable * int64(s.cfg.Upload.MaxDiskSharePercent) / 100
		if mine > share {
			return apierr.New(apierr.StorageFull, "this account already holds its share of the disk for uploads").
				With("required_bytes", mine).With("available_bytes", share).With("scope", "account")
		}
	}
	return nil
}

// checkDiskNow is the install-wide disk check for a piece. It reads the counts outside a transaction;
// a piece adds no unwritten bytes, so only the install-wide rule applies.
func (s *Server) checkDiskNow(ctx context.Context, accountID string) error {
	usable, ok, err := s.diskUsable()
	if err != nil || !ok {
		return err
	}
	mine, total, err := s.store.UnwrittenBytes(ctx, accountID)
	if err != nil {
		return err
	}
	return s.checkDisk(usable, mine, total, false)
}

// reserveTx reserves an upload's declared length, inside tx, the transaction that inserted it: the
// disk must hold the account's share and the whole pool, and the account's quota (or its reservation
// cap) must allow the length. A refusal rolls the whole creation back.
func (s *Server) reserveTx(ctx context.Context, tx *store.Tx, up *store.Upload) error {
	usable, ok, err := s.diskUsable()
	if err != nil {
		return err
	}
	if ok {
		mine, total, err := tx.UnwrittenBytes(ctx, up.AccountID)
		if err != nil {
			return err
		}
		if err := s.checkDisk(usable, mine, total, true); err != nil {
			return err
		}
	}
	return s.applyUsage(ctx, tx, up.AccountID, store.UsageChange{Reserved: up.Length})
}

// handleCreateUpload starts an upload (tus creation). The body may carry the first piece
// (creation-with-upload). The caller needs write on the target folder, and may hold only so many
// unfinished uploads.
func (s *Server) handleCreateUpload(w http.ResponseWriter, r *http.Request) error {
	if err := tusVersionOK(w, r); err != nil {
		return err
	}
	length, err := parseDecimal("Upload-Length", r.Header.Get("Upload-Length"))
	if err != nil {
		return apierr.New(apierr.InvalidRequest, "Upload-Length is required; deferred lengths are not supported")
	}
	if length > int64(s.cfg.Upload.MaxFileSize) {
		return apierr.Newf(apierr.TooLarge, "a file may be at most %d bytes", s.cfg.Upload.MaxFileSize).
			With("limit_bytes", int64(s.cfg.Upload.MaxFileSize))
	}
	meta, err := parseUploadMetadata(r.Header.Get("Upload-Metadata"))
	if err != nil {
		return err
	}
	tgt, err := readTarget(meta)
	if err != nil {
		return err
	}
	sum, err := parseChecksum(r.Header.Get("Upload-Checksum"))
	if err != nil {
		return err
	}
	withBody := isUploadBody(r)
	if withBody {
		if off := r.Header.Get("Upload-Offset"); off != "" && off != "0" {
			return apierr.New(apierr.InvalidRequest, "a creation with a body starts at Upload-Offset 0")
		}
		if err := s.checkPieceSize(r.ContentLength, length); err != nil {
			return err
		}
	}
	p := principalOf(r)
	ctx := r.Context()
	if err := s.rateLimit("upload.create", string(p.Kind)+":"+p.ID); err != nil {
		return err
	}
	now := store.Now()
	up := &store.Upload{ID: id.New("upl"), AccountID: p.AccountID, FolderID: tgt.folder,
		ParentID: tgt.parent, DirPath: tgt.dirPath, Parents: tgt.parents, Name: tgt.name, NameKey: tgt.nameKey,
		OnConflict: tgt.onConflict, MimeType: mimeOf(tgt.mime, tgt.name), SHA256: tgt.sha, Length: length,
		BlobID: id.New("blb"), CreatedBy: p.ID, CreatedKind: string(p.Kind), CreatedAt: now, UpdatedAt: now,
		ExpiresAt: s.uploadDeadline(now, now)}
	if err := uploadAccess(ctx, s.store, p, up, store.RoleContributor); err != nil {
		return err
	}

	// Checked before anything is written: the directory and a name already taken (fail).
	dir, err := s.uploadDirNow(r, up)
	if err != nil {
		return err
	}
	if dir != nil && up.OnConflict == "fail" {
		if taken, err := nameTakenBy(s.store.Child(ctx, up.AccountID, dir.ID, up.NameKey)); err != nil {
			return err
		} else if taken != nil {
			return nameConflict(taken)
		}
	}
	if err := s.blobs.CreateUpload(up.AccountID, up.ID); err != nil {
		return apierr.Wrap(apierr.Internal, "the upload file could not be created", err)
	}
	// The disk, the quota and the reservation are checked in the transaction that inserts the upload,
	// so two creations cannot both pass on the same space.
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		if err := uploadAccess(ctx, tx, p, up, store.RoleContributor); err != nil {
			return err
		}
		if n, err := tx.CountOpenUploads(ctx, up.AccountID, p.ID, string(p.Kind)); err != nil {
			return err
		} else if n >= s.cfg.Upload.MaxOpenPerPrincipal {
			return tooManyUploads("caller", s.cfg.Upload.MaxOpenPerPrincipal)
		}
		if n, err := tx.CountAccountUploads(ctx, up.AccountID); err != nil {
			return err
		} else if n >= s.cfg.Upload.MaxOpenPerAccount {
			return tooManyUploads("account", s.cfg.Upload.MaxOpenPerAccount)
		}
		if err := tx.InsertUpload(ctx, up); err != nil {
			return err
		}
		if err := s.reserveTx(ctx, tx, up); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, up.AccountID, "upload.create", up.ID,
			map[string]any{"folder": up.FolderID, "name": up.Name, "length": up.Length}, up.FolderID))
	})
	if err != nil {
		s.discardUploadFile(up)
		return err
	}

	if withBody {
		release, err := s.takeUploadSlot(w, up.ID) // the upload is new, so this never waits
		if err != nil {
			return s.dropFailedCreation(r, p, up, err)
		}
		err = s.appendChunk(w, r, p, up, sum)
		release()
		if err != nil {
			return s.dropFailedCreation(r, p, up, err)
		}
	}
	if up.DurableOffset == up.Length {
		node, err := s.completeUpload(r, p, up, nil)
		if err != nil {
			// The upload stays while it waits for a name; the reply names it, and Location says where.
			markAwaiting(w, up, err)
			return err
		}
		w.Header().Set(nodeHeader, node.ID)
	}
	h := w.Header()
	h.Set("Location", s.cfg.PublicURL+"/api/v1/uploads/"+up.ID)
	h.Set("Upload-Offset", strconv.FormatInt(up.DurableOffset, 10))
	h.Set("Upload-Expires", up.ExpiresAt.UTC().Format(http.TimeFormat))
	w.WriteHeader(http.StatusCreated)
	return nil
}

// dropFailedCreation removes an upload whose creation-with-upload failed before completing: the
// client got no Location for it, so nothing else refers to it. It returns the error to send.
func (s *Server) dropFailedCreation(r *http.Request, p *auth.Principal, up *store.Upload, cause error) error {
	err := s.store.Write(context.WithoutCancel(r.Context()), func(tx *store.Tx) error {
		return s.removeUploadTx(r, tx, up, "creation failed")
	})
	if err != nil {
		s.log.Warn("cannot drop a failed upload", "upload", up.ID, "error", err)
	}
	s.discardUploadFile(up)
	return cause
}

// tooManyUploads is the refusal for an open-upload cap.
func tooManyUploads(scope string, limit int) error {
	return apierr.Newf(apierr.TooManyUploads, "this %s holds the most unfinished uploads it may (%d); finish or delete some",
		scope, limit).With("scope", scope).With("limit", limit)
}

// removeUploadTx deletes an upload's row, in a transaction, with its audit entry. Its files are
// removed by discardUploadFile once the transaction has committed.
func (s *Server) removeUploadTx(r *http.Request, tx *store.Tx, up *store.Upload, reason string) error {
	if err := tx.DeleteUpload(r.Context(), up.AccountID, up.ID); errors.Is(err, store.ErrNotFound) {
		return notFound("upload")
	} else if err != nil {
		return err
	}
	return tx.InsertAudit(r.Context(), s.auditEntry(r, up.AccountID, "upload.delete", up.ID,
		map[string]any{"reason": reason, "length": up.Length, "durable_offset": up.DurableOffset}, up.FolderID))
}

// discardUploadFile removes an upload's file and any blob a completion moved into blobs/ without
// committing it. Call it only after the upload's row has been deleted in a committed transaction:
// then no commit can still turn that blob into a file a node refers to.
func (s *Server) discardUploadFile(up *store.Upload) {
	if err := s.blobs.RemoveUpload(up.AccountID, up.ID); err != nil {
		s.log.Warn("cannot remove an upload file", "upload", up.ID, "error", err)
	}
	if err := s.blobs.Remove(up.AccountID, up.BlobID); err != nil {
		s.log.Warn("cannot remove an uncommitted blob", "blob", up.BlobID, "error", err)
	}
}

// awaitingState is the Selfcloud-Upload-State of an upload whose bytes are all in, waiting for a name.
const awaitingState = "awaiting_commit"

// markAwaiting adds the headers that let a client find an upload waiting for a name: its URL and its
// state. It does nothing for other errors.
func markAwaiting(w http.ResponseWriter, up *store.Upload, err error) {
	if apierr.From(err).Details["state"] != awaitingState {
		return
	}
	w.Header().Set("Location", "/api/v1/uploads/"+up.ID)
	w.Header().Set(stateHeader, awaitingState)
}

// appendChunk streams the request body into the upload at its durable offset, then syncs and
// records the new offset. A body that stops early keeps the bytes that arrived, synced and recorded,
// and the error is returned, unless the piece carries a checksum: then it cannot be verified, so it
// is dropped. A piece that fails its checksum or its limits is also dropped, so nothing is appended.
// The caller holds the upload's PATCH slot.
func (s *Server) appendChunk(w http.ResponseWriter, r *http.Request, p *auth.Principal, up *store.Upload, sum *chunkSum) error {
	ctx := r.Context()
	start := up.DurableOffset
	remaining := up.Length - start
	maxChunk := int64(s.cfg.Upload.MaxChunkSize)
	if err := s.checkPieceSize(r.ContentLength, remaining); err != nil {
		return err
	}
	chunkCap := min(maxChunk, remaining)
	// The piece's bytes are already in the reservation the disk check counts, so none are added.
	if err := s.checkDiskNow(ctx, up.AccountID); err != nil {
		return err
	}
	h, err := restoreHash(up.HashState, start)
	if err != nil {
		return apierr.Wrap(apierr.Internal, "the upload's state is damaged", err)
	}
	f, err := s.openUploadFile(up)
	if err != nil {
		return err
	}
	defer f.Close()
	// Bytes past the durable offset came from a crash or a refused piece: they are not part of the
	// upload. A file shorter than the offset has lost data.
	st, err := f.Stat()
	if err != nil {
		return apierr.Wrap(apierr.Internal, "the upload's file could not be read", err)
	}
	if st.Size() < start {
		return apierr.New(apierr.Internal, "the upload's file is shorter than its recorded offset")
	}
	if st.Size() > start {
		if err := f.Truncate(start); err != nil {
			return apierr.Wrap(apierr.Internal, "the upload's file could not be trimmed", err)
		}
	}

	s.setBodyDeadline(w, maxChunk)
	cw := &chunkWriter{f: f, off: start, h: h}
	if sum != nil {
		cw.ch = sum.newHash()
	}
	buf := uploadBufs.Get().(*[]byte)
	defer uploadBufs.Put(buf)
	_, copyErr := io.CopyBuffer(cw, io.LimitReader(r.Body, chunkCap), *buf)

	// A body longer than the cap is refused, not cut short.
	if copyErr == nil && cw.off-start == chunkCap {
		var one [1]byte
		m, perr := io.ReadFull(r.Body, one[:])
		switch {
		case m == 1:
			_ = f.Truncate(start) // a failed truncate is repaired before the next piece
			if maxChunk <= remaining {
				return chunkTooLarge(maxChunk)
			}
			return tooLargeUpload(remaining)
		case perr != io.EOF:
			copyErr = perr
		}
	}
	if copyErr != nil && sum != nil {
		// Cut short, so the checksum cannot be checked: the piece is dropped whole.
		_ = f.Truncate(start) // a failed truncate is repaired before the next piece
		return apierr.Wrap(apierr.InvalidRequest, "the request body was cut short; the piece is dropped, send it again", copyErr)
	}
	if copyErr == nil && sum != nil && !bytes.Equal(cw.ch.Sum(nil), sum.digest) {
		_ = f.Truncate(start) // a failed truncate is repaired before the next piece
		return apierr.New(apierr.ChecksumMismatch, "the piece does not match its Upload-Checksum; send it again").
			With("offset", start)
	}

	if got := cw.off - start; got > 0 {
		state, err := marshalHash(cw.h)
		if err != nil {
			return apierr.Wrap(apierr.Internal, "the hash state could not be saved", err)
		}
		expires := s.uploadDeadline(up.CreatedAt, store.Now())
		if err := s.recordPiece(ctx, p, up, f, start, cw.off, state, expires); err != nil {
			return err
		}
	}
	if copyErr != nil {
		return apierr.Wrap(apierr.InvalidRequest, "the request body could not be read", copyErr)
	}
	return nil
}

// openUploadFile opens an upload's file for a piece. A file missing while nothing is recorded yet
// (the offset is 0) is made again, since no byte is lost. Missing after that is an internal fault.
func (s *Server) openUploadFile(up *store.Upload) (*os.File, error) {
	f, err := s.blobs.OpenUpload(up.AccountID, up.ID)
	if errors.Is(err, fs.ErrNotExist) && up.DurableOffset == 0 {
		if err := s.blobs.CreateUpload(up.AccountID, up.ID); err != nil {
			return nil, apierr.Wrap(apierr.Internal, "the upload's file could not be made again", err)
		}
		f, err = s.blobs.OpenUpload(up.AccountID, up.ID)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil, apierr.New(apierr.Internal, "the upload's file is missing")
	}
	if err != nil {
		return nil, apierr.Wrap(apierr.Internal, "the upload's file could not be opened", err)
	}
	return f, nil
}

// handlePatchUpload appends a piece (tus PATCH). It answers 204 with the new Upload-Offset. A piece
// that completes the upload also completes the file; the new node's ID comes back in a header. A
// piece that completes an upload whose name is taken answers 409 conflict and leaves it waiting for
// a name (see handleCommitUpload).
func (s *Server) handlePatchUpload(w http.ResponseWriter, r *http.Request) error {
	if err := tusVersionOK(w, r); err != nil {
		return err
	}
	if !isUploadBody(r) {
		return apierr.Newf(apierr.UnsupportedMedia, "the body must be %s", uploadBodyType)
	}
	offset, err := parseDecimal("Upload-Offset", r.Header.Get("Upload-Offset"))
	if err != nil {
		return err
	}
	sum, err := parseChecksum(r.Header.Get("Upload-Checksum"))
	if err != nil {
		return err
	}
	p := principalOf(r)
	uplID := r.PathValue("upl")
	if _, err := s.ownedUpload(r, p, uplID); err != nil {
		return err
	}
	release, err := s.takeUploadSlot(w, uplID)
	if err != nil {
		return err
	}
	defer release()
	// Read again under the slot: a piece that ran before us may have moved the offset.
	up, err := s.ownedUpload(r, p, uplID)
	if err != nil {
		return err
	}
	if err := uploadAccess(r.Context(), s.store, p, up, store.RoleContributor); err != nil {
		return err
	}
	if offset != up.DurableOffset {
		return apierr.Newf(apierr.UploadOffsetMismatch, "Upload-Offset is %d; the upload is at %d", offset,
			up.DurableOffset).With("offset", up.DurableOffset)
	}
	if up.DurableOffset < up.Length {
		if err := s.appendChunk(w, r, p, up, sum); err != nil {
			return err
		}
	} else if r.ContentLength > 0 {
		return tooLargeUpload(0)
	}
	if up.DurableOffset == up.Length {
		node, err := s.completeUpload(r, p, up, nil)
		if err != nil {
			markAwaiting(w, up, err)
			return err
		}
		w.Header().Set(nodeHeader, node.ID)
	}
	w.Header().Set("Upload-Offset", strconv.FormatInt(up.DurableOffset, 10))
	w.Header().Set("Upload-Expires", up.ExpiresAt.UTC().Format(http.TimeFormat))
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- completing ----

// completionTimeout bounds a completion's transaction. It runs on a context detached from the
// request, so a client that disconnects at the last byte does not roll back finished work.
const completionTimeout = 30 * time.Second

// completeUpload turns a finished upload into a file. It checks the whole-file SHA-256 if the client
// gave one, then in one transaction: re-checks access on the folder the row names, settles the name,
// records the blob, creates or replaces the node, updates usage, audits, deletes the upload row, and
// only then moves the file into blobs/ (the last step, so a failure before it leaves nothing behind).
// A name taken under fail leaves the upload waiting for a name: it answers conflict with
// details.state "awaiting_commit", and nothing moves. override, when set, is a new target for the
// upload (a commit); it is saved in the same transaction.
func (s *Server) completeUpload(r *http.Request, p *auth.Principal, up *store.Upload, override *uploadTarget) (*store.Node, error) {
	h, err := restoreHash(up.HashState, up.Length)
	if err != nil {
		return nil, apierr.Wrap(apierr.Internal, "the upload's state is damaged", err)
	}
	digest := hex.EncodeToString(h.Sum(nil))
	if up.SHA256 != "" && digest != up.SHA256 {
		// The contents cannot be fixed in place, so the upload is dropped and must start again.
		s.dropCompleted(r, p, up, "checksum mismatch")
		return nil, apierr.New(apierr.ChecksumMismatch, "the file does not match the SHA-256 given at creation; the upload was dropped").
			With("expected", up.SHA256).With("actual", digest)
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), completionTimeout)
	defer cancel()
	var out *store.Node
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		cur, err := tx.Upload(ctx, up.AccountID, up.ID)
		if errors.Is(err, store.ErrNotFound) {
			return notFound("upload")
		}
		if err != nil {
			return err
		}
		if cur.DurableOffset != cur.Length {
			return apierr.New(apierr.UploadOffsetMismatch, "the upload is not complete")
		}
		if override != nil {
			cur.ParentID, cur.DirPath, cur.Parents = override.parent, override.dirPath, override.parents
			cur.Name, cur.NameKey, cur.OnConflict = override.name, override.nameKey, override.onConflict
			cur.UpdatedAt = store.Now()
			if err := tx.UpdateUploadTarget(ctx, cur); err != nil {
				return storeNameErr(err)
			}
		}
		// Everything below uses the folder the row names, which is the one checked here.
		if err := uploadAccess(ctx, tx, p, cur, store.RoleContributor); err != nil {
			return err
		}
		parent, err := s.uploadParentTx(r, tx, p, cur)
		if err != nil {
			return err
		}
		plan := &transferPlan{folderID: cur.FolderID, parent: parent, name: cur.Name, key: cur.NameKey}
		taken, err := nameTakenBy(tx.Child(ctx, cur.AccountID, parent.ID, cur.NameKey))
		if err != nil {
			return err
		}
		if taken != nil {
			if cur.OnConflict == "fail" {
				return apierr.New(apierr.Conflict, "a file or folder with this name already exists here; "+
					"commit the upload under another name").With("upload_id", cur.ID).With("state", awaitingState)
			}
			if err := applyConflict(ctx, tx, plan, taken, cur.OnConflict, store.KindFile, ""); err != nil {
				return err
			}
			if err := requireReplaceRole(ctx, tx, p, plan); err != nil {
				return err
			}
		}
		now := store.Now()
		if err := tx.InsertBlob(ctx, &store.Blob{ID: cur.BlobID, AccountID: cur.AccountID, Size: cur.Length,
			SHA256: digest, State: store.BlobLive, CreatedAt: now}); err != nil {
			return err
		}
		var action string
		var delta, files int64
		if plan.replaced != nil {
			n := plan.replaced
			prev := n.Size
			n.BlobID, n.Size, n.MimeType, n.ETag, n.SHA256 = cur.BlobID, cur.Length, cur.MimeType, newETag(), digest
			n.ModifiedAt, n.ModifiedBy = now, p.ID
			if err := tx.UpdateNode(ctx, n); err != nil {
				return storeNameErr(err)
			}
			out, action, delta = n, "node.write", cur.Length-prev
			if err := tx.InsertAudit(ctx, s.auditEntry(r, cur.AccountID, action, n.ID,
				map[string]any{"size": cur.Length, "previous_size": prev, "upload": cur.ID}, cur.FolderID)); err != nil {
				return err
			}
		} else {
			n := &store.Node{ID: id.New("nod"), AccountID: cur.AccountID, FolderID: cur.FolderID, ParentID: parent.ID,
				Name: plan.name, NameKey: plan.key, Kind: store.KindFile, Size: cur.Length, MimeType: cur.MimeType,
				BlobID: cur.BlobID, ETag: newETag(), SHA256: digest, CreatedAt: now, ModifiedAt: now,
				CreatedBy: p.ID, ModifiedBy: p.ID}
			if err := tx.InsertNode(ctx, n); err != nil {
				return storeNameErr(err)
			}
			out, action, delta, files = n, "node.create", cur.Length, 1
			if err := tx.InsertAudit(ctx, s.auditEntry(r, cur.AccountID, action, n.ID,
				map[string]any{"kind": store.KindFile, "name": n.Name, "parent": parent.ID, "size": cur.Length,
					"upload": cur.ID}, cur.FolderID)); err != nil {
				return err
			}
		}
		// Completion turns the reservation into usage, so it never grows the quota and is not checked
		// against it: DeleteUpload releases the reservation, and the usage takes the bytes. A quota
		// lowered while the upload was going does not strand a file that has been written in full.
		if err := tx.DeleteUpload(ctx, cur.AccountID, cur.ID); err != nil {
			return err
		}
		if err := tx.AdjustUsage(ctx, cur.AccountID, store.UsageChange{Bytes: delta, Files: files}); err != nil {
			return err
		}
		// The last step before commit: the file moves only once every check and insert has passed.
		// If the commit fails after this, the file is in blobs/ with no row, and discardUploadFile
		// removes it when the upload is deleted or expires.
		if err := s.blobs.FinishUpload(cur.AccountID, cur.ID, cur.BlobID); err != nil {
			return apierr.Wrap(apierr.Internal, "the file could not be committed", err)
		}
		if s.beforeCommitUpload != nil {
			return s.beforeCommitUpload()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// dropCompleted deletes a completed upload that cannot be committed, with its audit entry.
func (s *Server) dropCompleted(r *http.Request, p *auth.Principal, up *store.Upload, reason string) {
	err := s.store.Write(context.WithoutCancel(r.Context()), func(tx *store.Tx) error {
		return s.removeUploadTx(r, tx, up, reason)
	})
	if err != nil {
		s.log.Warn("cannot drop an upload", "upload", up.ID, "error", err)
	}
	s.discardUploadFile(up)
}

// handleHeadUpload reports how far an upload got, for resuming. Only its creator may ask. An upload
// whose bytes are all in and that waits for a name says so in Selfcloud-Upload-State.
func (s *Server) handleHeadUpload(w http.ResponseWriter, r *http.Request) error {
	if err := tusVersionOK(w, r); err != nil {
		return err
	}
	p := principalOf(r)
	up, err := s.ownedUpload(r, p, r.PathValue("upl"))
	if err != nil {
		return err
	}
	if err := uploadAccess(r.Context(), s.store, p, up, store.RoleViewer); err != nil {
		return err
	}
	h := w.Header()
	h.Set("Upload-Offset", strconv.FormatInt(up.DurableOffset, 10))
	h.Set("Upload-Length", strconv.FormatInt(up.Length, 10))
	h.Set("Upload-Expires", up.ExpiresAt.UTC().Format(http.TimeFormat))
	h.Set("Cache-Control", "no-store")
	if up.DurableOffset == up.Length {
		h.Set(stateHeader, awaitingState)
	}
	w.WriteHeader(http.StatusOK)
	return nil
}

// handleDeleteUpload terminates an upload: its row and its files go at once. It needs write on the
// folder, and takes its turn behind a piece in progress.
func (s *Server) handleDeleteUpload(w http.ResponseWriter, r *http.Request) error {
	if err := tusVersionOK(w, r); err != nil {
		return err
	}
	p := principalOf(r)
	uplID := r.PathValue("upl")
	up, err := s.ownedUpload(r, p, uplID)
	if err != nil {
		return err
	}
	release, err := s.takeUploadSlot(w, uplID)
	if err != nil {
		return err
	}
	defer release()
	ctx := r.Context()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		if err := uploadAccess(ctx, tx, p, up, store.RoleContributor); err != nil {
			return err
		}
		return s.removeUploadTx(r, tx, up, "terminated")
	})
	if err != nil {
		return err
	}
	s.discardUploadFile(up)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// commitBody is the target a waiting upload is committed to. A missing parent or path keeps the
// upload's directory; a missing name or on_conflict keeps the upload's own.
type commitBody struct {
	Parent     *string `json:"parent"`
	Path       *string `json:"path"`
	Name       *string `json:"name"`
	OnConflict *string `json:"on_conflict"`
}

// handleCommitUpload completes an upload that waits for a name. Only its creator may commit it, and
// only while it waits. The completion runs again with the new target, which is checked for access
// and for a free name; on success it answers 201 with the file.
func (s *Server) handleCommitUpload(w http.ResponseWriter, r *http.Request) error {
	var in commitBody
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	p := principalOf(r)
	uplID := r.PathValue("upl")
	if _, err := s.ownedUpload(r, p, uplID); err != nil {
		return err
	}
	release, err := s.takeUploadSlot(w, uplID)
	if err != nil {
		return err
	}
	defer release()
	up, err := s.ownedUpload(r, p, uplID)
	if err != nil {
		return err
	}
	if up.DurableOffset != up.Length {
		return apierr.New(apierr.Conflict, "the upload is not waiting for a name").With("state", "uploading")
	}
	meta := map[string]string{"folder": up.FolderID, "name": up.Name, "on_conflict": up.OnConflict}
	if in.Name != nil {
		meta["name"] = *in.Name
	}
	if in.OnConflict != nil {
		meta["on_conflict"] = *in.OnConflict
	}
	if in.Parent != nil {
		meta["parent"] = *in.Parent
	}
	if in.Path != nil {
		meta["path"] = *in.Path
	}
	tgt, err := readTarget(meta)
	if err != nil {
		return err
	}
	if in.Parent == nil && in.Path == nil {
		tgt.parent, tgt.dirPath = up.ParentID, up.DirPath
	}
	tgt.parents = up.Parents
	node, err := s.completeUpload(r, p, up, tgt)
	if err != nil {
		markAwaiting(w, up, err)
		return err
	}
	w.Header().Set("Location", "/api/v1/nodes/"+node.ID)
	writeJSON(w, http.StatusCreated, toNodeJSON(node))
	return nil
}
