// SPDX-License-Identifier: AGPL-3.0-or-later

package api

// Templates (decision 105): the built-in starter set, and the account's templates folder. GET /api/v1/templates?kind=
// lists the templates of a kind: the built-ins (on the first page only), then the files of the account's templates folder
// whose extension makes them templates of that kind, that the caller can read. A file is listed with its node ID and
// whether it has a thumbnail. The folder is named by GET and PUT /api/v1/accounts/{acc}/templates, by the account's owner or
// a manager (as the archive switch is). A folder that is trashed, deleted or unreadable is none: its setting stays, and
// nothing lists from it.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/templates"
)

const (
	templatesDefaultLimit = 50
	templatesMaxLimit     = 100
	// templatesScanCap is how many children one listing reads at most. A larger folder answers a cursor to go on from.
	templatesScanCap = 1000
	// templatesPageSize is how many children one read asks the store for.
	templatesPageSize = 100
)

// templateExt maps each extension a template file may have to the kind it makes: the OOXML ones and the OpenDocument ones.
var templateExt = map[string]string{
	"docx": templates.Document, "odt": templates.Document,
	"xlsx": templates.Spreadsheet, "ods": templates.Spreadsheet,
	"pptx": templates.Presentation, "odp": templates.Presentation,
}

// templateExtOf returns the lower-case extension of a file name when a template may have it, or "".
func templateExtOf(name string) string {
	ext := strings.TrimPrefix(strings.ToLower(path.Ext(name)), ".")
	if _, ok := templateExt[ext]; ok {
		return ext
	}
	return ""
}

// templateItem is one template in the listing. A built-in has no node; an account file has its node ID, which is also its
// ID, and whether it has a thumbnail.
type templateItem struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Source       string `json:"source"`
	NodeID       string `json:"node_id,omitempty"`
	HasThumbnail *bool  `json:"has_thumbnail,omitempty"`
}

// templateFolderJSON is the setting as the API answers it: the node ID, or null for none.
func templateFolderJSON(n *store.Node) map[string]any {
	if n == nil {
		return map[string]any{"node_id": nil}
	}
	return map[string]any{"node_id": n.ID}
}

// isNotFoundErr reports whether err is the API's not_found.
func isNotFoundErr(err error) bool {
	var ae *apierr.Error
	return errors.As(err, &ae) && ae.Code == apierr.NotFound
}

// templatesFolder returns the account's templates folder as the caller reaches it, or nil when none is named, the folder is
// trashed or deleted, or the caller cannot read it.
func (s *Server) templatesFolder(ctx context.Context, p *auth.Principal, accountID string) (*store.Node, error) {
	nodeID, err := s.store.TemplatesNode(ctx, accountID)
	if err != nil || nodeID == "" {
		return nil, err
	}
	n, _, err := nodeAccessWith(ctx, s.store, p, nodeID)
	if isNotFoundErr(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if n.AccountID != accountID || n.Kind != store.KindDir {
		return nil, nil
	}
	return n, nil
}

// handleGetTemplatesFolder answers the account's templates folder: its node ID, or null.
func (s *Server) handleGetTemplatesFolder(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	acc := r.PathValue("acc")
	if !p.ManagesAccount(acc) {
		return notFound("account")
	}
	n, err := s.templatesFolder(r.Context(), p, acc)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, templateFolderJSON(n))
	return nil
}

// handleSetTemplatesFolder names the account's templates folder: a directory the caller can read, in the account. null
// clears the setting. Only the account's owner or a manager may change it; the change is audited.
func (s *Server) handleSetTemplatesFolder(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	acc := r.PathValue("acc")
	if !p.ManagesAccount(acc) {
		return notFound("account")
	}
	var in struct {
		NodeID json.RawMessage `json:"node_id"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if len(in.NodeID) == 0 {
		return apierr.New(apierr.InvalidRequest, "node_id must be a directory's node ID, or null")
	}
	ctx := r.Context()
	var n *store.Node
	if string(in.NodeID) != "null" {
		var nodeID string
		if err := json.Unmarshal(in.NodeID, &nodeID); err != nil || !id.Valid("nod", nodeID) {
			return apierr.New(apierr.InvalidRequest, "node_id must be a directory's node ID, or null")
		}
		found, _, err := nodeAccessWith(ctx, s.store, p, nodeID)
		if err != nil {
			return err
		}
		if found.AccountID != acc {
			return nodeNotFound()
		}
		if found.Kind != store.KindDir {
			return apierr.New(apierr.InvalidRequest, "the templates folder is a directory")
		}
		n = found
	}
	nodeID := ""
	if n != nil {
		nodeID = n.ID
	}
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.SetTemplatesNode(ctx, acc, nodeID); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, acc, "templates.folder", acc, map[string]any{"node": nodeID}))
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, templateFolderJSON(n))
	return nil
}

// handleListTemplates lists the templates of the kind given in ?kind=document|spreadsheet|presentation. The built-ins are
// on the first page (no after). ?limit= (1 to 100, 50 by default) caps the account files on a page; ?after= is the
// next_cursor of the previous page. Office editing must be on.
func (s *Server) handleListTemplates(w http.ResponseWriter, r *http.Request) error {
	if s.office == nil {
		return notFound("templates")
	}
	q := r.URL.Query()
	kind := q.Get("kind")
	if kind != templates.Document && kind != templates.Spreadsheet && kind != templates.Presentation {
		return apierr.New(apierr.InvalidRequest, "kind must be document, spreadsheet or presentation")
	}
	limit := templatesDefaultLimit
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > templatesMaxLimit {
			return apierr.New(apierr.InvalidRequest, "limit must be from 1 to 100")
		}
		limit = n
	}
	after := q.Get("after")
	if after != "" && !store.ValidChildCursor(after) {
		return apierr.New(apierr.InvalidRequest, "after is not a cursor from this listing")
	}
	ctx := r.Context()
	p := principalOf(r)
	acc := p.AccountID
	lang := s.documentLanguage(ctx, p, acc)
	out := []templateItem{}
	if after == "" {
		for _, t := range templates.ByKind(kind) {
			out = append(out, templateItem{ID: t.ID, Name: t.Name(lang) + "." + t.Ext, Kind: kind, Source: "builtin"})
		}
	}
	folder, err := s.templatesFolder(ctx, p, acc)
	if err != nil {
		return err
	}
	var next string
	if folder != nil {
		files, cursor, err := s.templateFiles(ctx, p, folder, kind, limit, after)
		if err != nil {
			return err
		}
		next = cursor
		nodes := make([]*nodeJSON, 0, len(files))
		for _, n := range files {
			j := toNodeJSON(n)
			nodes = append(nodes, &j)
		}
		if err := s.withThumbnails(ctx, p, acc, nodes...); err != nil {
			return err
		}
		for _, j := range nodes {
			has := j.HasThumbnail != nil && *j.HasThumbnail
			out = append(out, templateItem{ID: j.ID, Name: j.Name, Kind: kind, Source: "account", NodeID: j.ID, HasThumbnail: &has})
		}
	}
	writeJSON(w, http.StatusOK, listResponse[templateItem]{Items: out, NextCursor: next})
	return nil
}

// templateFiles returns up to limit files of the folder whose extension makes them templates of kind, from the cursor after
// on, in name order. It also returns the cursor to continue from, or "" when the folder has no more to read. A file the
// caller cannot read is left out, as a node it cannot reach is.
func (s *Server) templateFiles(ctx context.Context, p *auth.Principal, folder *store.Node, kind string, limit int,
	after string) ([]*store.Node, string, error) {
	var out []*store.Node
	pos := after
	scanned := 0
	for len(out) < limit && scanned < templatesScanCap {
		children, _, err := s.store.Children(ctx, folder.AccountID, folder.ID, store.Page{Limit: templatesPageSize, Cursor: pos})
		if err != nil {
			return nil, "", err
		}
		if len(children) == 0 {
			return out, "", nil
		}
		for _, c := range children {
			scanned++
			pos = store.ChildCursor(c)
			if c.Kind != store.KindFile || templateExt[templateExtOf(c.Name)] != kind {
				continue
			}
			if _, _, err := nodeAccessWith(ctx, s.store, p, c.ID); isNotFoundErr(err) {
				continue
			} else if err != nil {
				return nil, "", err
			}
			out = append(out, c)
			if len(out) == limit {
				return out, pos, nil
			}
		}
	}
	return out, pos, nil
}
