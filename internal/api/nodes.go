// SPDX-License-Identifier: AGPL-3.0-or-later

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
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/blob"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/names"
	"github.com/shukiv/selfcloud/internal/store"
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
	// Role is the caller's role on the node. Listings and stat carry it; an account key gets
	// manager, the highest role, since it reaches everything in its account.
	Role string `json:"role,omitempty"`
	// NoDownload and EditorsCanShare are the node's own settings. They also reach its subtree; the
	// access rules read the ancestors.
	NoDownload      bool `json:"no_download"`
	EditorsCanShare bool `json:"editors_can_share"`
}

func toNodeJSON(n *store.Node) nodeJSON {
	out := nodeJSON{ID: n.ID, FolderID: n.FolderID, Name: n.Name, Kind: n.Kind, Size: n.Size,
		CreatedAt: n.CreatedAt, ModifiedAt: n.ModifiedAt, NoDownload: n.NoDownload,
		EditorsCanShare: n.EditorsCanShare}
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

// nodeAccess loads a node the caller reaches, with the role the caller holds on it. A node the
// caller cannot reach is not_found, and the answer is the same whether or not the node exists.
func (s *Server) nodeAccess(r *http.Request, nodeID string) (*store.Node, string, error) {
	if !id.Valid("nod", nodeID) {
		return nil, "", nodeNotFound()
	}
	return nodeAccessWith(r.Context(), s.store, principalOf(r), nodeID)
}

// nodeOf loads a node the caller holds at least minRole on. Below that role the caller already knows
// the node exists, so the answer is forbidden, not not_found.
func (s *Server) nodeOf(r *http.Request, nodeID, minRole string) (*store.Node, error) {
	if !id.Valid("nod", nodeID) {
		return nil, nodeNotFound()
	}
	return requireNodeRole(r.Context(), s.store, principalOf(r), nodeID, minRole)
}

// nodeFinder looks up the child of a directory by name key, from the store or a transaction.
type nodeFinder func(ctx context.Context, parentID, key string) (*store.Node, error)

// walkDirs follows the directories named by segs from root, without creating anything. It returns
// the last directory, or nil with no error when one on the way is missing. A file on the way is a
// notDirError; the caller answers it with fileOnPath.
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
			return nil, &notDirError{at: cur, name: seg.name}
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
			map[string]any{"kind": store.KindDir, "name": seg.name, "parent": cur.ID}, folderID)); err != nil {
			return nil, err
		}
		cur = dir
	}
	return cur, nil
}

// newNode inserts a directory or file node under parentID with the given name, and returns it.
// ownerIn is the owner a new node gets in a folder: the folder's user inside a personal folder, and
// its creator everywhere else. It is the rule the store applies when a node is inserted, given here
// so that the node in memory matches the row.
func ownerIn(ctx context.Context, tx *store.Tx, accountID, folderID, creator string) (string, error) {
	f, err := tx.Folder(ctx, accountID, folderID)
	if errors.Is(err, store.ErrNotFound) {
		return creator, nil
	}
	if err != nil {
		return "", err
	}
	if f.Kind == store.KindPersonal {
		return f.OwnerUserID, nil
	}
	return creator, nil
}

func (s *Server) newNode(ctx context.Context, tx *store.Tx, accountID, folderID, parentID string, seg pathSeg,
	kind, by string) (*store.Node, error) {
	now := store.Now()
	owner, err := ownerIn(ctx, tx, accountID, folderID, by)
	if err != nil {
		return nil, err
	}
	n := &store.Node{ID: id.New("nod"), AccountID: accountID, FolderID: folderID, ParentID: parentID,
		Name: seg.name, NameKey: seg.key, Kind: kind, CreatedAt: now, ModifiedAt: now,
		CreatedBy: by, ModifiedBy: by, OwnerID: owner}
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
	n, role, err := s.nodeAccess(r, r.PathValue("nod"))
	if err != nil {
		return err
	}
	out := toNodeJSON(n)
	out.Role = role
	writeJSON(w, http.StatusOK, out)
	return nil
}

// callerRankCap is the highest rank the caller may carry on any node: a token's cap, and manager for
// everything else. Children of a listing are capped the same way, so a token never lists more than
// it may act on.
func callerRankCap(p *auth.Principal) int {
	if p.Token != nil {
		return store.RoleRank(tokenRoleCap(p.Token))
	}
	return store.RoleRank(store.RoleManager)
}

// writeChildren answers a page of a directory's children. role is the caller's role on dir. Each
// child's role is the higher of that and its own permissions, so the parent's role is worked out
// once for the whole listing, and only the children's direct permissions are read per page.
func (s *Server) writeChildren(w http.ResponseWriter, r *http.Request, dir *store.Node, role string) error {
	page, err := pageParams(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	p := principalOf(r)
	kids, next, err := s.store.Children(ctx, dir.AccountID, dir.ID, page)
	if err != nil {
		return storeErr(err)
	}
	var ranks map[string]int
	if p.Kind == auth.KindUser {
		if ranks, err = s.store.ChildRanks(ctx, dir.AccountID, dir.ID, p.UserID); err != nil {
			return err
		}
	}
	cap := callerRankCap(p)
	base := min(store.RoleRank(role), cap)
	out := listResponse[nodeJSON]{Items: []nodeJSON{}, NextCursor: next}
	for _, k := range kids {
		j := toNodeJSON(k)
		j.Role = store.RoleOfRank(max(base, min(ranks[k.ID], cap)))
		out.Items = append(out.Items, j)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) handleListNodeChildren(w http.ResponseWriter, r *http.Request) error {
	dir, role, err := s.nodeAccess(r, r.PathValue("nod"))
	if err != nil {
		return err
	}
	if dir.Kind != store.KindDir {
		return apierr.New(apierr.InvalidRequest, "this node is a file, not a directory")
	}
	return s.writeChildren(w, r, dir, role)
}

// handleListPathChildren lists a directory of a folder by its path; ?path= empty or "/" is the root.
// The path is walked before the access check, because the directory may sit below a root the caller
// cannot reach while the caller holds a permission on it. A path the caller cannot reach answers
// not_found, the same as a missing one.
func (s *Server) handleListPathChildren(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := callerCheck(p); err != nil {
		return err
	}
	fld := r.PathValue("fld")
	if p.Token != nil && !slices.Contains(p.Token.Folders, fld) {
		return notFound("folder")
	}
	segs, err := parsePath(r.URL.Query().Get("path"))
	if err != nil {
		return err
	}
	ctx := r.Context()
	root, err := s.store.RootNode(ctx, p.AccountID, fld)
	if errors.Is(err, store.ErrNotFound) {
		return notFound("folder")
	}
	if err != nil {
		return err
	}
	dir, err := walkDirs(ctx, func(ctx context.Context, parentID, key string) (*store.Node, error) {
		return s.store.Child(ctx, p.AccountID, parentID, key)
	}, root, segs)
	if err != nil {
		return fileOnPath(ctx, s.store, p, err, "path")
	}
	if dir == nil {
		return notFound("path")
	}
	dir, role, err := nodeAccessWith(ctx, s.store, p, dir.ID)
	if err != nil {
		if apierr.From(err).Code == apierr.NotFound {
			return notFound("path")
		}
		return err
	}
	return s.writeChildren(w, r, dir, role)
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
	ctx := r.Context()
	p := principalOf(r)
	fld := r.PathValue("fld")
	// The directory is made under the deepest one that exists, so that is where contributor is needed.
	if _, _, err := writeAnchor(ctx, s.store, p, fld, segs[:len(segs)-1], store.RoleContributor); err != nil {
		return err
	}
	var out *store.Node
	created := false
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		f, _, err := writeAnchor(ctx, tx, p, fld, segs[:len(segs)-1], store.RoleContributor)
		if err != nil {
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
			map[string]any{"kind": store.KindDir, "name": leaf.name, "parent": parent.ID}, f.ID))
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
	ctx := r.Context()
	p := principalOf(r)
	leaf := segs[len(segs)-1]
	dirs := segs[:len(segs)-1]
	mt := mimeOf(r.Header.Get("Content-Type"), leaf.name)

	// The directory the file lands in needs contributor. Checked before the body is written, so a
	// refused name or role costs no disk.
	fld := r.PathValue("fld")
	f, _, err := writeAnchor(ctx, s.store, p, fld, dirs, store.RoleContributor)
	if err != nil {
		return err
	}
	if err := s.checkFreeName(ctx, p, f, dirs, leaf, parents, ifNoneMatch != ""); err != nil {
		return err
	}
	c, err := s.writeBlob(w, r, f.AccountID, int64(s.cfg.Upload.MaxSmallFileSize))
	if err != nil {
		return err
	}
	var out *store.Node
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		if _, _, err := writeAnchor(ctx, tx, p, f.ID, dirs, store.RoleContributor); err != nil {
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
		// The quota is checked here, in the transaction that makes the file.
		if err := s.applyUsage(ctx, tx, f.AccountID, store.UsageChange{Bytes: c.Size, Files: 1}); err != nil {
			return err
		}
		out = n
		return tx.InsertAudit(ctx, s.auditEntry(r, f.AccountID, "node.create", n.ID,
			map[string]any{"kind": store.KindFile, "name": leaf.name, "parent": parent.ID, "size": c.Size}, f.ID))
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
func (s *Server) checkFreeName(ctx context.Context, p *auth.Principal, f *store.Folder, dirs []pathSeg,
	leaf pathSeg, parents, ifNoneMatch bool) error {
	root, err := s.store.RootNode(ctx, f.AccountID, f.ID)
	if err != nil {
		return err
	}
	parent, err := walkDirs(ctx, func(ctx context.Context, parentID, key string) (*store.Node, error) {
		return s.store.Child(ctx, f.AccountID, parentID, key)
	}, root, dirs)
	if err != nil {
		return fileOnPath(ctx, s.store, p, err, "parent directory")
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

// handleRenameNode changes a node's name and its sharing flags. The name needs editor, and so does
// no_download; editors_can_share needs manager, since it lets editors share. Every field is optional,
// but a request must name at least one. A folder's root has no name, so its name is refused.
func (s *Server) handleRenameNode(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Name            *string `json:"name"`
		NoDownload      *bool   `json:"no_download"`
		EditorsCanShare *bool   `json:"editors_can_share"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if in.Name == nil && in.NoDownload == nil && in.EditorsCanShare == nil {
		return apierr.New(apierr.InvalidRequest, "give name, no_download or editors_can_share")
	}
	var name, key string
	if in.Name != nil {
		var err error
		if name, key, err = names.Clean(*in.Name); err != nil {
			return err
		}
	}
	need := store.RoleEditor
	if in.EditorsCanShare != nil {
		need = store.RoleManager
	}
	n, err := s.nodeOf(r, r.PathValue("nod"), need)
	if err != nil {
		return err
	}
	if n.IsRoot() && in.Name != nil {
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
		if _, err := requireNodeRole(ctx, tx, p, cur.ID, need); err != nil {
			return err
		}
		var renamed *string
		if in.Name != nil {
			if taken, err := nameTakenBy(tx.Child(ctx, cur.AccountID, cur.ParentID, key)); err != nil {
				return err
			} else if taken != nil && taken.ID != cur.ID {
				return nameConflict(taken)
			}
			from := cur.Name
			renamed = &from
			cur.Name, cur.NameKey = name, key
		}
		sharing := map[string]any{}
		if in.NoDownload != nil {
			cur.NoDownload = *in.NoDownload
			sharing["no_download"] = cur.NoDownload
		}
		if in.EditorsCanShare != nil {
			cur.EditorsCanShare = *in.EditorsCanShare
			sharing["editors_can_share"] = cur.EditorsCanShare
		}
		cur.ModifiedAt, cur.ModifiedBy = store.Now(), p.ID
		if err := tx.UpdateNode(ctx, cur); err != nil {
			return storeNameErr(err)
		}
		out = cur
		if renamed != nil {
			if err := tx.InsertAudit(ctx, s.auditEntry(r, cur.AccountID, "node.rename", cur.ID,
				map[string]any{"from": *renamed, "to": name}, cur.FolderID)); err != nil {
				return err
			}
		}
		if len(sharing) > 0 {
			return tx.InsertAudit(ctx, s.auditEntry(r, cur.AccountID, "node.sharing", cur.ID, sharing, cur.FolderID))
		}
		return nil
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

// handleDeleteNode deletes a file, or a folder with everything in it. By default the node goes to the trash:
// it keeps its contents, its permissions and its usage, and a restore brings it back until trash.retention
// purges it. With permanent=true it is deleted at once, which needs manager, since it cannot be undone. The
// blobs are left for the collector; usage comes down only for a permanent delete.
func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) error {
	permanent, err := permanentParam(r)
	if err != nil {
		return err
	}
	need := store.RoleEditor
	if permanent {
		need = store.RoleManager
	}
	n, err := s.nodeOf(r, r.PathValue("nod"), need)
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
		if _, err := requireNodeRole(ctx, tx, p, cur.ID, need); err != nil {
			return err
		}
		if permanent {
			return s.purgeIn(r, tx, cur)
		}
		return s.trashIn(r, tx, cur, p.ID)
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
	n, err := s.nodeOf(r, r.PathValue("nod"), store.RoleContributor)
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
		if _, err := requireNodeRole(ctx, tx, p, cur.ID, store.RoleContributor); err != nil {
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
		// The replacement counts in full. The old content is kept as a version, which still counts toward
		// the quota, so the growth is the new size, not the difference (design section 8, Versions).
		if err := s.applyUsage(ctx, tx, cur.AccountID, store.UsageChange{Bytes: c.Size}); err != nil {
			return err
		}
		if err := s.keepVersion(ctx, tx, cur, p.ID, store.Now()); err != nil {
			return err
		}
		cur.BlobID, cur.Size, cur.MimeType, cur.ETag, cur.SHA256 = c.ID, c.Size, mt, newETag(), c.SHA256
		cur.ModifiedAt, cur.ModifiedBy = store.Now(), p.ID
		if err := tx.UpdateNode(ctx, cur); err != nil {
			return err
		}
		out = cur
		return tx.InsertAudit(ctx, s.auditEntry(r, cur.AccountID, "node.write", cur.ID,
			map[string]any{"size": c.Size, "previous_size": prev}, cur.FolderID))
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
