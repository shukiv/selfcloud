// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// Version history (slice m, design section 8). Whenever a file's content is replaced, the content it loses
// is kept as a version, so it can be read back or restored until it is pruned. A version's bytes count toward
// the account's usage and quota, as the current content does.

// currentVersionID names the current content in a version listing and in the error for deleting it. The
// current content is not a version row, so it has no ID of its own.
const currentVersionID = "current"

// maxLabelRunes is the longest label a version may carry.
const maxLabelRunes = 100

// versionJSON is one entry of a version listing. The current content is the first entry, with Current set and
// ID "current". Its ReplacedAt and ReplacedBy are empty, since it has not been replaced.
type versionJSON struct {
	ID          string     `json:"id"`
	Current     bool       `json:"current"`
	Size        int64      `json:"size"`
	MimeType    string     `json:"mime_type,omitempty"`
	ETag        string     `json:"etag"`
	SHA256      string     `json:"sha256,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	CreatedBy   string     `json:"created_by,omitempty"`
	ReplacedAt  *time.Time `json:"replaced_at,omitempty"`
	ReplacedBy  string     `json:"replaced_by,omitempty"`
	Label       *string    `json:"label"`
	KeepForever bool       `json:"keep_forever"`
}

// versionOf is the listing entry for a stored version.
func versionOf(v *store.Version) versionJSON {
	replaced := v.ReplacedAt
	return versionJSON{ID: v.ID, Size: v.Size, MimeType: v.MimeType, ETag: v.ETag, SHA256: v.SHA256,
		CreatedAt: v.CreatedAt, CreatedBy: v.CreatedBy, ReplacedAt: &replaced, ReplacedBy: v.ReplacedBy,
		Label: v.Label, KeepForever: v.KeepForever}
}

// currentOf is the listing entry for a node's current content.
func currentOf(n *store.Node) versionJSON {
	return versionJSON{ID: currentVersionID, Current: true, Size: n.Size, MimeType: n.MimeType, ETag: n.ETag,
		SHA256: n.SHA256, CreatedAt: n.ModifiedAt, CreatedBy: n.ModifiedBy}
}

// keepVersion records the content that cur is about to lose as a version: its blob, size, hash and ETag as
// they are now. It runs inside the transaction that replaces the content, before cur takes the new content.
// It does not change usage: the caller's usage change already counts the new content in full, and the old
// content's bytes are the version's. It then prunes the versions past versions.max_count.
func (s *Server) keepVersion(ctx context.Context, tx *store.Tx, cur *store.Node, by string, at time.Time) error {
	v := &store.Version{ID: id.New("ver"), AccountID: cur.AccountID, NodeID: cur.ID, BlobID: cur.BlobID,
		Size: cur.Size, SHA256: cur.SHA256, ETag: cur.ETag, MimeType: cur.MimeType, CreatedAt: cur.ModifiedAt,
		CreatedBy: cur.ModifiedBy, ReplacedAt: at, ReplacedBy: by}
	if err := tx.InsertVersion(ctx, v); err != nil {
		return err
	}
	_, _, err := tx.PruneVersions(ctx, cur.AccountID, cur.ID, s.cfg.Versions.MaxCount, nil)
	return err
}

// fileNodeOf loads a file the caller holds at least minRole on. A folder has no versions.
func (s *Server) fileNodeOf(r *http.Request, nodeID, minRole string) (*store.Node, error) {
	n, err := s.nodeOf(r, nodeID, minRole)
	if err != nil {
		return nil, err
	}
	if n.Kind != store.KindFile {
		return nil, apierr.New(apierr.InvalidRequest, "a folder has no versions")
	}
	return n, nil
}

// validVersionID checks a version ID in a path. The current content is not a version, so its name is a
// not_found here; a delete of it is answered on its own.
func validVersionID(s string) error {
	if !id.Valid("ver", s) {
		return notFound("version")
	}
	return nil
}

// handleListVersions lists a file's versions, newest replaced first, with the current content first. Any
// role that reads the file lists its versions.
func (s *Server) handleListVersions(w http.ResponseWriter, r *http.Request) error {
	n, _, err := s.nodeAccess(r, r.PathValue("nod"))
	if err != nil {
		return err
	}
	if n.Kind != store.KindFile {
		return apierr.New(apierr.InvalidRequest, "a folder has no versions")
	}
	vs, err := s.store.Versions(r.Context(), n.AccountID, n.ID)
	if err != nil {
		return err
	}
	out := []versionJSON{currentOf(n)}
	for _, v := range vs {
		out = append(out, versionOf(v))
	}
	writeJSON(w, http.StatusOK, listResponse[versionJSON]{Items: out})
	return nil
}

// handleGetVersionContent sends an earlier content of a file. It follows the content route: the same ranges,
// validators and disposition, and the same no_download rule, since it is the file's content too.
func (s *Server) handleGetVersionContent(w http.ResponseWriter, r *http.Request) error {
	n, role, err := s.nodeAccess(r, r.PathValue("nod"))
	if err != nil {
		return err
	}
	if n.Kind != store.KindFile {
		return apierr.New(apierr.InvalidRequest, "a folder has no versions")
	}
	verID := r.PathValue("ver")
	if err := validVersionID(verID); err != nil {
		return err
	}
	v, err := s.store.Version(r.Context(), n.AccountID, n.ID, verID)
	if err != nil {
		return storeErrAs(err, notFound("version"))
	}
	inline := false
	switch d := r.URL.Query().Get("disposition"); d {
	case "", dispAttachment:
	case dispInline:
		inline = true
	default:
		return apierr.New(apierr.InvalidRequest, "disposition must be attachment or inline")
	}
	restricted, err := s.downloadRestricted(r.Context(), n, role)
	if err != nil {
		return err
	}
	if restricted && !s.inlineAllowed(r, n, inline) {
		return errDownloadRestricted()
	}
	// The version is served as a file of its own: its blob, size, type and ETag, under the file's name.
	vn := &store.Node{ID: n.ID, AccountID: n.AccountID, FolderID: n.FolderID, Name: n.Name, Kind: store.KindFile,
		Size: v.Size, MimeType: v.MimeType, BlobID: v.BlobID, SHA256: v.SHA256, ETag: v.ETag, ModifiedAt: v.CreatedAt}
	return s.serveFile(w, r, vn, inline, corpSameOrigin)
}

// storeErrAs maps a store miss to the given error and anything else through storeErr.
func storeErrAs(err, miss error) error {
	if errors.Is(err, store.ErrNotFound) {
		return miss
	}
	return storeErr(err)
}

// handleUpdateVersion sets a version's label and whether it is kept forever. Both are optional, and a
// request must name one. An empty label removes it.
func (s *Server) handleUpdateVersion(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Label       *string `json:"label"`
		KeepForever *bool   `json:"keep_forever"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if in.Label == nil && in.KeepForever == nil {
		return apierr.New(apierr.InvalidRequest, "give label or keep_forever")
	}
	if in.Label != nil && utf8.RuneCountInString(*in.Label) > maxLabelRunes {
		return apierr.Newf(apierr.InvalidRequest, "a label may be at most %d characters", maxLabelRunes)
	}
	n, err := s.fileNodeOf(r, r.PathValue("nod"), store.RoleEditor)
	if err != nil {
		return err
	}
	verID := r.PathValue("ver")
	if err := validVersionID(verID); err != nil {
		return err
	}
	ctx := r.Context()
	p := principalOf(r)
	var out *store.Version
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		cur, err := tx.Node(ctx, n.AccountID, n.ID)
		if err != nil {
			return nodeNotFound()
		}
		if _, err := requireNodeRole(ctx, tx, p, cur.ID, store.RoleEditor); err != nil {
			return err
		}
		v, err := tx.Version(ctx, cur.AccountID, cur.ID, verID)
		if err != nil {
			return storeErrAs(err, notFound("version"))
		}
		if in.KeepForever != nil && *in.KeepForever && !v.KeepForever {
			kept, err := tx.KeptVersions(ctx, cur.AccountID, cur.ID)
			if err != nil {
				return err
			}
			if kept >= s.cfg.Versions.MaxKept {
				return apierr.Newf(apierr.Conflict, "a file keeps at most %d versions forever; unmark one first",
					s.cfg.Versions.MaxKept).With("max_kept", s.cfg.Versions.MaxKept)
			}
		}
		changes := map[string]any{}
		if in.Label != nil {
			var label *string
			if *in.Label != "" {
				l := *in.Label
				label = &l
			}
			v.Label = label
			changes["label"] = *in.Label
		}
		if in.KeepForever != nil {
			v.KeepForever = *in.KeepForever
			changes["keep_forever"] = v.KeepForever
		}
		if err := tx.UpdateVersion(ctx, v); err != nil {
			return err
		}
		out = v
		return tx.InsertAudit(ctx, s.auditEntry(r, cur.AccountID, "node.version.update", cur.ID,
			map[string]any{"version": v.ID, "changes": changes}, cur.FolderID))
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, versionOf(out))
	return nil
}

// handleRestoreVersion makes an earlier version the current content. The current content becomes a version,
// so the restore can be undone the same way. It needs contributor, since it replaces the content, and If-Match
// with the current ETag, as a content replace does. The chosen version stays in the history with its row,
// label and keep_forever; its bytes then count as the file's content and as the version, so usage grows by its
// size and the quota is checked for it.
func (s *Server) handleRestoreVersion(w http.ResponseWriter, r *http.Request) error {
	ifMatch := r.Header.Get("If-Match")
	if ifMatch == "" {
		return apierr.New(apierr.PreconditionRequired, "If-Match is required: send the ETag from the last read")
	}
	if !strings.HasPrefix(ifMatch, `"`) {
		return apierr.New(apierr.InvalidRequest, "If-Match must be the ETag of the current content")
	}
	n, err := s.fileNodeOf(r, r.PathValue("nod"), store.RoleContributor)
	if err != nil {
		return err
	}
	verID := r.PathValue("ver")
	if err := validVersionID(verID); err != nil {
		return err
	}
	if n.ETag != ifMatch {
		return apierr.New(apierr.PreconditionFailed, "the file changed since it was read; read it again")
	}
	ctx := r.Context()
	p := principalOf(r)
	var out *store.Node
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		cur, err := tx.Node(ctx, n.AccountID, n.ID)
		if err != nil {
			return nodeNotFound()
		}
		if _, err := requireNodeRole(ctx, tx, p, cur.ID, store.RoleContributor); err != nil {
			return err
		}
		if cur.ETag != ifMatch {
			return apierr.New(apierr.PreconditionFailed, "the file changed since it was read; read it again")
		}
		v, err := tx.Version(ctx, cur.AccountID, cur.ID, verID)
		if err != nil {
			return storeErrAs(err, notFound("version"))
		}
		// The chosen version keeps its row, label and keep_forever: it is now the file's content as well as a
		// version, so its bytes count twice and usage grows by its size, checked like a content replace. The
		// content it replaces is kept as a version.
		if err := s.applyUsage(ctx, tx, cur.AccountID, store.UsageChange{Bytes: v.Size}); err != nil {
			return err
		}
		if err := s.keepVersion(ctx, tx, cur, p.ID, store.Now()); err != nil {
			return err
		}
		prev := cur.Size
		cur.BlobID, cur.Size, cur.MimeType, cur.SHA256 = v.BlobID, v.Size, v.MimeType, v.SHA256
		cur.ETag, cur.ModifiedAt, cur.ModifiedBy = newETag(), store.Now(), p.ID
		cur.ContentTag = store.NewContentTag()
		if err := tx.UpdateNode(ctx, cur); err != nil {
			return err
		}
		out = cur
		return tx.InsertAudit(ctx, s.auditEntry(r, cur.AccountID, "node.version.restore", cur.ID,
			map[string]any{"version": v.ID, "size": v.Size, "previous_size": prev}, cur.FolderID))
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toNodeJSON(out))
	return nil
}

// handleDeleteVersion deletes an earlier version for good, and takes its bytes off usage. The current content
// is not a version and cannot be deleted this way: delete the file, or restore an older version first.
func (s *Server) handleDeleteVersion(w http.ResponseWriter, r *http.Request) error {
	n, err := s.fileNodeOf(r, r.PathValue("nod"), store.RoleEditor)
	if err != nil {
		return err
	}
	verID := r.PathValue("ver")
	if verID == currentVersionID {
		return apierr.New(apierr.Conflict, "the current content cannot be deleted; delete the file, or restore "+
			"an older version first")
	}
	if err := validVersionID(verID); err != nil {
		return err
	}
	ctx := r.Context()
	p := principalOf(r)
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		cur, err := tx.Node(ctx, n.AccountID, n.ID)
		if err != nil {
			return nodeNotFound()
		}
		if _, err := requireNodeRole(ctx, tx, p, cur.ID, store.RoleEditor); err != nil {
			return err
		}
		v, err := tx.Version(ctx, cur.AccountID, cur.ID, verID)
		if err != nil {
			return storeErrAs(err, notFound("version"))
		}
		if err := tx.DeleteVersion(ctx, cur.AccountID, v); err != nil {
			return err
		}
		if err := tx.AdjustUsage(ctx, cur.AccountID, store.UsageChange{Bytes: -v.Size}); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, cur.AccountID, "node.version.delete", cur.ID,
			map[string]any{"version": v.ID, "size": v.Size}, cur.FolderID))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
