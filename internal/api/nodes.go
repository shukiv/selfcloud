package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"selfcloud/internal/apierr"
	"selfcloud/internal/blob"
	"selfcloud/internal/config"
	"selfcloud/internal/id"
	"selfcloud/internal/names"
	"selfcloud/internal/store"
)

// maxPathLength bounds a path before it is split.
const maxPathLength = 4096

// maxMimeLength is the longest stored media type.
const maxMimeLength = 255

// pathSeg is one checked segment of a path: the name as stored and its key.
type pathSeg struct {
	name, key string
}

// parsePath splits a path such as "/a/b.txt" into checked segments. "" and "/" name the folder's
// root and have no segments. An empty segment, "." and ".." are refused, not resolved.
func parsePath(p string) ([]pathSeg, error) {
	if len(p) > maxPathLength {
		return nil, apierr.Newf(apierr.InvalidPath, "the path is longer than %d bytes", maxPathLength)
	}
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return nil, nil
	}
	parts := strings.Split(p, "/")
	out := make([]pathSeg, 0, len(parts))
	for _, part := range parts {
		name, key, err := names.Clean(part)
		if err != nil {
			return nil, apierr.Newf(apierr.InvalidPath, "the path is not valid: %s", apierr.From(err).Message)
		}
		out = append(out, pathSeg{name: name, key: key})
	}
	return out, nil
}

// newETag returns a strong entity tag, random so that it changes with every write.
func newETag() string {
	var b [16]byte
	rand.Read(b[:]) // crypto/rand.Read never returns an error.
	return `"` + hex.EncodeToString(b[:]) + `"`
}

// mimeOf returns the media type to store for a file: the one the client sent, else one from the
// name's extension, else application/octet-stream.
func mimeOf(header, name string) string {
	if mt, _, err := mime.ParseMediaType(header); err == nil && mt != "" && mt != "application/octet-stream" &&
		len(mt) <= maxMimeLength {
		return mt
	}
	if ext := path.Ext(name); ext != "" {
		if t := mime.TypeByExtension(ext); t != "" {
			if mt, _, err := mime.ParseMediaType(t); err == nil {
				return mt
			}
		}
	}
	return "application/octet-stream"
}

// validID reports an invalid ID in a body as invalid_request.
func validID(prefix, s string) error {
	if !id.Valid(prefix, s) {
		return apierr.Newf(apierr.InvalidRequest, "%s ID is not valid", prefix)
	}
	return nil
}

type nodeJSON struct {
	ID         string    `json:"id"`
	FolderID   string    `json:"folder_id"`
	ParentID   *string   `json:"parent_id"` // null for a folder's root
	Name       string    `json:"name"`
	Kind       string    `json:"kind"`
	Size       int64     `json:"size"`
	MimeType   string    `json:"mime_type,omitempty"`
	ETag       string    `json:"etag,omitempty"`
	SHA256     string    `json:"sha256,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	ModifiedAt time.Time `json:"modified_at"`
}

func toNodeJSON(n *store.Node) nodeJSON {
	out := nodeJSON{ID: n.ID, FolderID: n.FolderID, Name: n.Name, Kind: n.Kind, Size: n.Size,
		CreatedAt: n.CreatedAt, ModifiedAt: n.ModifiedAt}
	if !n.IsRoot() {
		parent := n.ParentID
		out.ParentID = &parent
	}
	if n.Kind == store.KindFile {
		out.MimeType, out.ETag, out.SHA256 = n.MimeType, n.ETag, n.SHA256
	}
	return out
}

// nodeNotFound is what a node the caller cannot reach looks like.
func nodeNotFound() error { return notFound("node") }

// nodeOf loads a node of the caller's account. It checks that the caller reaches the node's folder,
// at write when write is set. A node the caller cannot reach is not_found; a reachable one that
// needs write is forbidden, because the caller already knows it exists.
func (s *Server) nodeOf(r *http.Request, nodeID string, write bool) (*store.Node, error) {
	p := principalOf(r)
	if p == nil {
		return nil, apierr.New(apierr.Unauthenticated, "credentials are required")
	}
	if p.IsAdmin() {
		return nil, apierr.New(apierr.Forbidden, "admins manage tenants and may not read their files")
	}
	if !id.Valid("nod", nodeID) {
		return nil, nodeNotFound()
	}
	n, err := s.store.Node(r.Context(), p.AccountID, nodeID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nodeNotFound()
	}
	if err != nil {
		return nil, err
	}
	_, level, err := s.folderAccess(r, n.FolderID)
	if err != nil {
		if apierr.From(err).Code == apierr.NotFound {
			return nil, nodeNotFound()
		}
		return nil, err
	}
	if write && level != accessWrite {
		return nil, apierr.New(apierr.Forbidden, "this needs write access to the folder")
	}
	return n, nil
}

// folderFor loads a folder the caller reaches, at write when write is set.
func (s *Server) folderFor(r *http.Request, folderID string, write bool) (*store.Folder, error) {
	f, level, err := s.folderAccess(r, folderID)
	if err != nil {
		return nil, err
	}
	if write && level != accessWrite {
		return nil, apierr.New(apierr.Forbidden, "this needs write access to the folder")
	}
	return f, nil
}

// nodeFinder looks up the child of a directory by name key, from the store or a transaction.
type nodeFinder func(ctx context.Context, parentID, key string) (*store.Node, error)

// walkDirs follows the directories named by segs from root, without creating anything. It returns
// the last directory, or nil with no error when one on the way is missing. A file on the way is an
// invalid path.
func walkDirs(ctx context.Context, find nodeFinder, root *store.Node, segs []pathSeg) (*store.Node, error) {
	cur := root
	for _, seg := range segs {
		child, err := find(ctx, cur.ID, seg.key)
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if child.Kind != store.KindDir {
			return nil, apierr.Newf(apierr.InvalidPath, "%s is a file, not a directory", seg.name)
		}
		cur = child
	}
	return cur, nil
}

// walkDirsTx follows segs from root inside a transaction, creating missing directories when create
// is set, each with its own audit entry. It returns the last directory.
func (s *Server) walkDirsTx(r *http.Request, tx *store.Tx, accountID, folderID string, root *store.Node,
	segs []pathSeg, create bool, by string) (*store.Node, error) {
	ctx := r.Context()
	cur := root
	for _, seg := range segs {
		child, err := tx.Child(ctx, accountID, cur.ID, seg.key)
		if err == nil {
			if child.Kind != store.KindDir {
				return nil, apierr.Newf(apierr.InvalidPath, "%s is a file, not a directory", seg.name)
			}
			cur = child
			continue
		}
		if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		if !create {
			return nil, notFound("parent directory")
		}
		dir, err := s.newNode(ctx, tx, accountID, folderID, cur.ID, seg, store.KindDir, by)
		if err != nil {
			return nil, err
		}
		if err := tx.InsertAudit(ctx, s.auditEntry(r, accountID, "node.create", dir.ID,
			map[string]any{"kind": store.KindDir, "name": seg.name, "parent": cur.ID})); err != nil {
			return nil, err
		}
		cur = dir
	}
	return cur, nil
}

// newNode inserts a directory or file node under parentID with the given name, and returns it.
func (s *Server) newNode(ctx context.Context, tx *store.Tx, accountID, folderID, parentID string, seg pathSeg,
	kind, by string) (*store.Node, error) {
	now := store.Now()
	n := &store.Node{ID: id.New("nod"), AccountID: accountID, FolderID: folderID, ParentID: parentID,
		Name: seg.name, NameKey: seg.key, Kind: kind, CreatedAt: now, ModifiedAt: now,
		CreatedBy: by, ModifiedBy: by}
	if err := tx.InsertNode(ctx, n); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, apierr.New(apierr.Conflict, "a file or folder with this name already exists here")
		}
		return nil, err
	}
	return n, nil
}

// nameTakenBy checks a sibling name. It returns the node that has the key, if one does.
func nameTakenBy(existing *store.Node, err error) (*store.Node, error) {
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	return existing, err
}

// nameConflict is the error for a name a sibling already has.
func nameConflict(existing *store.Node) error {
	return apierr.New(apierr.Conflict, "a file or folder with this name already exists here").
		With("id", existing.ID)
}

// writeBlob streams the request body into a new blob. It refuses, before writing, a body that
// declares itself larger than limit, and it stops writing at limit. The body must arrive within a
// deadline that grows with the cap, so a stalled client cannot hold a write open.
func (s *Server) writeBlob(w http.ResponseWriter, r *http.Request, accountID string, limit int64) (blob.Committed, error) {
	if r.ContentLength > limit {
		return blob.Committed{}, tooLarge(limit)
	}
	s.setBodyDeadline(w, limit)
	wr, err := s.blobs.Create(r.Context(), accountID, limit)
	if err != nil {
		return blob.Committed{}, err
	}
	defer wr.Abort() //nolint:errcheck // removes the partial file unless Commit succeeded
	if _, err := io.Copy(wr, r.Body); err != nil {
		if errors.Is(err, blob.ErrTooLarge) {
			return blob.Committed{}, tooLarge(limit)
		}
		return blob.Committed{}, apierr.Wrap(apierr.InvalidRequest, "the request body could not be read", err)
	}
	return wr.Commit()
}

// setBodyDeadline sets the time a request body may take to arrive: upload.body_timeout plus one
// second per MiB of the size cap. Not every writer supports deadlines (test recorders do not); the
// cap still holds without one.
func (s *Server) setBodyDeadline(w http.ResponseWriter, limit int64) {
	deadline := s.cfg.Upload.BodyTimeout.D() + time.Duration(limit/int64(config.MiB))*time.Second
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(deadline))
}

func tooLarge(limit int64) error {
	return apierr.Newf(apierr.TooLarge, "a file written in one request may be at most %d bytes; "+
		"larger files need resumable uploads", limit).With("limit_bytes", limit)
}

// ---- reading ----

func (s *Server) handleStatNode(w http.ResponseWriter, r *http.Request) error {
	n, err := s.nodeOf(r, r.PathValue("nod"), false)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toNodeJSON(n))
	return nil
}

// writeChildren answers a page of a directory's children.
func (s *Server) writeChildren(w http.ResponseWriter, r *http.Request, dir *store.Node) error {
	page, err := pageParams(r)
	if err != nil {
		return err
	}
	kids, next, err := s.store.Children(r.Context(), dir.AccountID, dir.ID, page)
	if err != nil {
		return storeErr(err)
	}
	out := listResponse[nodeJSON]{Items: []nodeJSON{}, NextCursor: next}
	for _, k := range kids {
		out.Items = append(out.Items, toNodeJSON(k))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) handleListNodeChildren(w http.ResponseWriter, r *http.Request) error {
	dir, err := s.nodeOf(r, r.PathValue("nod"), false)
	if err != nil {
		return err
	}
	if dir.Kind != store.KindDir {
		return apierr.New(apierr.InvalidRequest, "this node is a file, not a directory")
	}
	return s.writeChildren(w, r, dir)
}

// handleListPathChildren lists a directory of a folder by its path; ?path= empty or "/" is the root.
func (s *Server) handleListPathChildren(w http.ResponseWriter, r *http.Request) error {
	f, err := s.folderFor(r, r.PathValue("fld"), false)
	if err != nil {
		return err
	}
	segs, err := parsePath(r.URL.Query().Get("path"))
	if err != nil {
		return err
	}
	ctx := r.Context()
	root, err := s.store.RootNode(ctx, f.AccountID, f.ID)
	if err != nil {
		return err
	}
	dir, err := walkDirs(ctx, func(ctx context.Context, parentID, key string) (*store.Node, error) {
		return s.store.Child(ctx, f.AccountID, parentID, key)
	}, root, segs)
	if err != nil {
		return err
	}
	if dir == nil {
		return notFound("path")
	}
	return s.writeChildren(w, r, dir)
}

// ---- creating ----

// handleMkdir makes a directory in a folder. With parents, missing directories on the way are
// made, and an existing directory is answered with 200 rather than a conflict.
func (s *Server) handleMkdir(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Path    string `json:"path"`
		Parents bool   `json:"parents"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	segs, err := parsePath(in.Path)
	if err != nil {
		return err
	}
	if len(segs) == 0 {
		return apierr.New(apierr.InvalidPath, "the path names the folder's root, which already exists")
	}
	f, err := s.folderFor(r, r.PathValue("fld"), true)
	if err != nil {
		return err
	}
	ctx := r.Context()
	p := principalOf(r)
	var out *store.Node
	created := false
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		if _, err := requireAccess(ctx, tx, p, f.ID, true); err != nil {
			return err
		}
		root, err := tx.RootNode(ctx, f.AccountID, f.ID)
		if err != nil {
			return err
		}
		parent, err := s.walkDirsTx(r, tx, f.AccountID, f.ID, root, segs[:len(segs)-1], in.Parents, p.ID)
		if err != nil {
			return err
		}
		leaf := segs[len(segs)-1]
		existing, err := nameTakenBy(tx.Child(ctx, f.AccountID, parent.ID, leaf.key))
		if err != nil {
			return err
		}
		if existing != nil {
			if in.Parents && existing.Kind == store.KindDir {
				out = existing
				return nil
			}
			return nameConflict(existing)
		}
		dir, err := s.newNode(ctx, tx, f.AccountID, f.ID, parent.ID, leaf, store.KindDir, p.ID)
		if err != nil {
			return err
		}
		out, created = dir, true
		return tx.InsertAudit(ctx, s.auditEntry(r, f.AccountID, "node.create", dir.ID,
			map[string]any{"kind": store.KindDir, "name": leaf.name, "parent": parent.ID}))
	})
	if err != nil {
		return err
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
		w.Header().Set("Location", "/api/v1/nodes/"+out.ID)
	}
	writeJSON(w, status, toNodeJSON(out))
	return nil
}

// handleCreateFile writes a new small file from the request body. The path is ?path=/a/b.txt and
// ?parents=true makes missing directories. A file that exists is a conflict; with If-None-Match: *
// it is a precondition failure.
func (s *Server) handleCreateFile(w http.ResponseWriter, r *http.Request) error {
	segs, err := parsePath(r.URL.Query().Get("path"))
	if err != nil {
		return err
	}
	if len(segs) == 0 {
		return apierr.New(apierr.InvalidPath, "the path names the folder's root, not a file")
	}
	parents := false
	if v := r.URL.Query().Get("parents"); v != "" {
		if parents, err = strconv.ParseBool(v); err != nil {
			return apierr.New(apierr.InvalidRequest, "parents must be true or false")
		}
	}
	ifNoneMatch := r.Header.Get("If-None-Match")
	if ifNoneMatch != "" && ifNoneMatch != "*" {
		return apierr.New(apierr.InvalidRequest, "If-None-Match may only be *")
	}
	f, err := s.folderFor(r, r.PathValue("fld"), true)
	if err != nil {
		return err
	}
	ctx := r.Context()
	p := principalOf(r)
	leaf := segs[len(segs)-1]
	dirs := segs[:len(segs)-1]
	mt := mimeOf(r.Header.Get("Content-Type"), leaf.name)

	// Checked before the body is written, so a refused name costs no disk.
	if err := s.checkFreeName(ctx, f, dirs, leaf, parents, ifNoneMatch != ""); err != nil {
		return err
	}
	c, err := s.writeBlob(w, r, f.AccountID, int64(s.cfg.Upload.MaxSmallFileSize))
	if err != nil {
		return err
	}
	var out *store.Node
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		if _, err := requireAccess(ctx, tx, p, f.ID, true); err != nil {
			return err
		}
		root, err := tx.RootNode(ctx, f.AccountID, f.ID)
		if err != nil {
			return err
		}
		parent, err := s.walkDirsTx(r, tx, f.AccountID, f.ID, root, dirs, parents, p.ID)
		if err != nil {
			return err
		}
		existing, err := nameTakenBy(tx.Child(ctx, f.AccountID, parent.ID, leaf.key))
		if err != nil {
			return err
		}
		if existing != nil {
			if ifNoneMatch != "" {
				return apierr.New(apierr.PreconditionFailed, "a file with this name already exists")
			}
			return nameConflict(existing)
		}
		if err := tx.InsertBlob(ctx, &store.Blob{ID: c.ID, AccountID: f.AccountID, Size: c.Size,
			SHA256: c.SHA256, State: store.BlobLive, CreatedAt: store.Now()}); err != nil {
			return err
		}
		n, err := s.newNode(ctx, tx, f.AccountID, f.ID, parent.ID, leaf, store.KindFile, p.ID)
		if err != nil {
			return err
		}
		n.Size, n.MimeType, n.BlobID, n.ETag, n.SHA256 = c.Size, mt, c.ID, newETag(), c.SHA256
		if err := tx.UpdateNode(ctx, n); err != nil {
			return err
		}
		// slice h: quota counters move with the files, in this transaction.
		if err := tx.AdjustUsage(ctx, f.AccountID, c.Size, 1); err != nil {
			return err
		}
		out = n
		return tx.InsertAudit(ctx, s.auditEntry(r, f.AccountID, "node.create", n.ID,
			map[string]any{"kind": store.KindFile, "name": leaf.name, "parent": parent.ID, "size": c.Size}))
	})
	if err != nil {
		s.discardBlob(f.AccountID, c.ID)
		return err
	}
	w.Header().Set("Location", "/api/v1/nodes/"+out.ID)
	writeJSON(w, http.StatusCreated, toNodeJSON(out))
	return nil
}

// checkFreeName is the read-only check before a file is written: the folder's directories exist (or
// parents is set), and the name is free. It is repeated inside the write transaction.
func (s *Server) checkFreeName(ctx context.Context, f *store.Folder, dirs []pathSeg, leaf pathSeg,
	parents, ifNoneMatch bool) error {
	root, err := s.store.RootNode(ctx, f.AccountID, f.ID)
	if err != nil {
		return err
	}
	parent, err := walkDirs(ctx, func(ctx context.Context, parentID, key string) (*store.Node, error) {
		return s.store.Child(ctx, f.AccountID, parentID, key)
	}, root, dirs)
	if err != nil {
		return err
	}
	if parent == nil {
		if !parents {
			return notFound("parent directory")
		}
		return nil
	}
	existing, err := nameTakenBy(s.store.Child(ctx, f.AccountID, parent.ID, leaf.key))
	if err != nil {
		return err
	}
	if existing == nil {
		return nil
	}
	if ifNoneMatch {
		return apierr.New(apierr.PreconditionFailed, "a file with this name already exists")
	}
	return nameConflict(existing)
}

// discardBlob removes a blob whose database change did not commit. Its file is unreferenced, so
// the collector would remove it later anyway; this just does it now.
func (s *Server) discardBlob(accountID, blobID string) {
	if err := s.blobs.Remove(accountID, blobID); err != nil {
		s.log.Warn("cannot remove an uncommitted blob", "blob", blobID, "error", err)
	}
}

// ---- editing ----

// handleRenameNode changes the name of a file or folder inside its directory.
func (s *Server) handleRenameNode(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	name, key, err := names.Clean(in.Name)
	if err != nil {
		return err
	}
	n, err := s.nodeOf(r, r.PathValue("nod"), true)
	if err != nil {
		return err
	}
	if n.IsRoot() {
		return apierr.New(apierr.InvalidRequest, "a folder's root has no name; rename the folder instead")
	}
	ctx := r.Context()
	p := principalOf(r)
	var out *store.Node
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		cur, err := tx.Node(ctx, n.AccountID, n.ID)
		if errors.Is(err, store.ErrNotFound) {
			return nodeNotFound()
		}
		if err != nil {
			return err
		}
		if _, err := requireAccess(ctx, tx, p, cur.FolderID, true); err != nil {
			return err
		}
		if taken, err := nameTakenBy(tx.Child(ctx, cur.AccountID, cur.ParentID, key)); err != nil {
			return err
		} else if taken != nil && taken.ID != cur.ID {
			return nameConflict(taken)
		}
		from := cur.Name
		cur.Name, cur.NameKey = name, key
		cur.ModifiedAt, cur.ModifiedBy = store.Now(), p.ID
		if err := tx.UpdateNode(ctx, cur); err != nil {
			return storeNameErr(err)
		}
		out = cur
		return tx.InsertAudit(ctx, s.auditEntry(r, cur.AccountID, "node.rename", cur.ID,
			map[string]any{"from": from, "to": name}))
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toNodeJSON(out))
	return nil
}

// storeNameErr maps a sibling clash found by the store to a conflict.
func storeNameErr(err error) error {
	if errors.Is(err, store.ErrConflict) {
		return apierr.New(apierr.Conflict, "a file or folder with this name already exists here")
	}
	return err
}

// handleDeleteNode removes a file, or a folder with everything in it. The blobs are left for the
// collector. The usage counters come down in the same transaction.
func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) error {
	n, err := s.nodeOf(r, r.PathValue("nod"), true)
	if err != nil {
		return err
	}
	if n.IsRoot() {
		return apierr.New(apierr.InvalidRequest, "a folder's root cannot be deleted; delete the folder instead")
	}
	ctx := r.Context()
	p := principalOf(r)
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		cur, err := tx.Node(ctx, n.AccountID, n.ID)
		if errors.Is(err, store.ErrNotFound) {
			return nodeNotFound()
		}
		if err != nil {
			return err
		}
		if _, err := requireAccess(ctx, tx, p, cur.FolderID, true); err != nil {
			return err
		}
		files, bytes, err := tx.SubtreeFiles(ctx, n.AccountID, n.ID)
		if err != nil {
			return err
		}
		if err := tx.DeleteNode(ctx, n.AccountID, n.ID); errors.Is(err, store.ErrNotFound) {
			return nodeNotFound()
		} else if err != nil {
			return err
		}
		// slice h: quota counters move with the files, in this transaction.
		if err := tx.AdjustUsage(ctx, n.AccountID, -bytes, -files); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, n.AccountID, "node.delete", n.ID,
			map[string]any{"kind": n.Kind, "name": n.Name, "files": files, "bytes": bytes}))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// handlePutContent replaces a file's contents. It needs If-Match with the ETag the caller read, so
// two editors cannot overwrite each other. The new contents are a new blob; the old one is left
// for the collector.
func (s *Server) handlePutContent(w http.ResponseWriter, r *http.Request) error {
	ifMatch := r.Header.Get("If-Match")
	if ifMatch == "" {
		return apierr.New(apierr.PreconditionRequired, "If-Match is required: send the ETag from the last read")
	}
	if !strings.HasPrefix(ifMatch, `"`) {
		return apierr.New(apierr.InvalidRequest, "If-Match must be the ETag of the version being replaced")
	}
	n, err := s.nodeOf(r, r.PathValue("nod"), true)
	if err != nil {
		return err
	}
	if n.Kind != store.KindFile {
		return apierr.New(apierr.InvalidRequest, "a folder has no contents to replace")
	}
	if n.ETag != ifMatch {
		return apierr.New(apierr.PreconditionFailed, "the file changed since it was read; read it again")
	}
	c, err := s.writeBlob(w, r, n.AccountID, int64(s.cfg.Upload.MaxSmallFileSize))
	if err != nil {
		return err
	}
	ctx := r.Context()
	p := principalOf(r)
	mt := mimeOf(r.Header.Get("Content-Type"), n.Name)
	var out *store.Node
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		cur, err := tx.Node(ctx, n.AccountID, n.ID)
		if errors.Is(err, store.ErrNotFound) {
			return nodeNotFound()
		}
		if err != nil {
			return err
		}
		if _, err := requireAccess(ctx, tx, p, cur.FolderID, true); err != nil {
			return err
		}
		if cur.ETag != ifMatch {
			return apierr.New(apierr.PreconditionFailed, "the file changed since it was read; read it again")
		}
		if err := tx.InsertBlob(ctx, &store.Blob{ID: c.ID, AccountID: cur.AccountID, Size: c.Size,
			SHA256: c.SHA256, State: store.BlobLive, CreatedAt: store.Now()}); err != nil {
			return err
		}
		prev := cur.Size
		cur.BlobID, cur.Size, cur.MimeType, cur.ETag, cur.SHA256 = c.ID, c.Size, mt, newETag(), c.SHA256
		cur.ModifiedAt, cur.ModifiedBy = store.Now(), p.ID
		if err := tx.UpdateNode(ctx, cur); err != nil {
			return err
		}
		// slice h: quota counters move with the contents, in this transaction.
		if err := tx.AdjustUsage(ctx, cur.AccountID, c.Size-prev, 0); err != nil {
			return err
		}
		out = cur
		return tx.InsertAudit(ctx, s.auditEntry(r, cur.AccountID, "node.write", cur.ID,
			map[string]any{"size": c.Size, "previous_size": prev}))
	})
	if err != nil {
		s.discardBlob(n.AccountID, c.ID)
		return err
	}
	writeJSON(w, http.StatusOK, toNodeJSON(out))
	return nil
}

// atWrite runs the test hook, if a test set one, just before a mutating request takes the write
// lock. Production leaves it nil.
func (s *Server) atWrite() {
	if s.beforeWrite != nil {
		s.beforeWrite()
	}
}
