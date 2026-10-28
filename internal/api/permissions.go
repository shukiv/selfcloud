// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// permissionJSON is a permission on a node. InheritedFrom is the ancestor the permission sits on,
// and null when it sits on the node itself.
type permissionJSON struct {
	SubjectKind   string    `json:"subject_kind"` // user or group
	SubjectID     string    `json:"subject_id"`
	Role          string    `json:"role"`
	InheritedFrom *string   `json:"inherited_from"`
	CreatedAt     time.Time `json:"created_at"`
	CreatedBy     string    `json:"created_by"`
}

// subjectOf reads a subject from a path: a usr_ or grp_ ID. Anything else is invalid.
func subjectOf(s string) (kind, subjectID string, err error) {
	switch {
	case strings.HasPrefix(s, "usr_"):
		if err := validID("usr", s); err != nil {
			return "", "", err
		}
		return store.SubjectUser, s, nil
	case strings.HasPrefix(s, "grp_"):
		if err := validID("grp", s); err != nil {
			return "", "", err
		}
		return store.SubjectGroup, s, nil
	}
	return "", "", apierr.New(apierr.InvalidRequest, "the subject must be a user (usr_...) or a group (grp_...)")
}

// handleListNodePermissions lists the permissions that reach a node: the ones on the node and the
// ones on its ancestors. Any role on the node lets the caller see them.
//
// Only a manager of the node, or an account key, sees every permission that reaches it. Anyone else
// sees only the permissions held by the caller or by a group it is in, and those may sit above the
// node. A delegated token reads no permissions at all: section 7 keeps them out of token reach.
func (s *Server) handleListNodePermissions(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if p != nil && p.Token != nil {
		return apierr.New(apierr.Forbidden, "a delegated token cannot read permissions")
	}
	n, role, err := s.nodeAccess(r, r.PathValue("nod"))
	if err != nil {
		return err
	}
	// The account key holds manager everywhere, so the filter below applies to users only.
	subject := ""
	if p.Kind == auth.KindUser && store.RoleRank(role) < store.RoleRank(store.RoleManager) {
		subject = p.UserID
	}
	perms, err := s.store.ChainPermissions(r.Context(), n.AccountID, n.ID, subject)
	if err != nil {
		return err
	}
	out := listResponse[permissionJSON]{Items: []permissionJSON{}}
	for _, pm := range perms {
		j := permissionJSON{SubjectKind: pm.SubjectKind, SubjectID: pm.SubjectID, Role: pm.Role,
			CreatedAt: pm.CreatedAt, CreatedBy: pm.CreatedBy}
		if pm.NodeID != n.ID {
			from := pm.NodeID
			j.InheritedFrom = &from
		}
		out.Items = append(out.Items, j)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// handleSetNodePermission gives a user or a group a role on a node, or changes the role it has.
// The caller needs manager on the node, or is an account key. A caller may not grant a role above
// its own, so a manager of a folder cannot make an editor into a manager. The subject must belong
// to the same account, and an installation admin cannot hold a permission.
func (s *Server) handleSetNodePermission(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Role string `json:"role"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if !store.ValidRole(in.Role) {
		return apierr.New(apierr.InvalidRequest, "role must be viewer, commenter, contributor, editor or manager")
	}
	kind, subj, err := subjectOf(r.PathValue("subject"))
	if err != nil {
		return err
	}
	ctx := r.Context()
	p := principalOf(r)
	nodeID := r.PathValue("nod")
	var out permissionJSON
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		n, err := requireNodeRole(ctx, tx, p, nodeID, store.RoleManager)
		if err != nil {
			return err
		}
		if err := s.checkSubject(ctx, tx, n.AccountID, kind, subj); err != nil {
			return err
		}
		if err := checkGrantRole(ctx, tx, p, n.ID, in.Role); err != nil {
			return err
		}
		now := store.Now()
		pm := &store.Permission{NodeID: n.ID, AccountID: n.AccountID, SubjectKind: kind, SubjectID: subj,
			Role: in.Role, CreatedAt: now, CreatedBy: p.ID}
		if err := tx.SetPermission(ctx, pm); err != nil {
			return err
		}
		out = permissionJSON{SubjectKind: kind, SubjectID: subj, Role: in.Role, CreatedAt: now, CreatedBy: p.ID}
		return tx.InsertAudit(ctx, s.auditEntry(r, n.AccountID, "permission.set", n.ID,
			map[string]any{"subject_kind": kind, "subject_id": subj, "role": in.Role}, n.FolderID))
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// handleRemoveNodePermission removes a permission that sits on the node itself. A permission above
// the node is removed on the node it sits on. The caller needs manager on the node, or an account key.
func (s *Server) handleRemoveNodePermission(w http.ResponseWriter, r *http.Request) error {
	kind, subj, err := subjectOf(r.PathValue("subject"))
	if err != nil {
		return err
	}
	ctx := r.Context()
	p := principalOf(r)
	nodeID := r.PathValue("nod")
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		n, err := requireNodeRole(ctx, tx, p, nodeID, store.RoleManager)
		if err != nil {
			return err
		}
		if err := tx.DeletePermission(ctx, n.ID, kind, subj); errors.Is(err, store.ErrNotFound) {
			return notFound("permission")
		} else if err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, n.AccountID, "permission.remove", n.ID,
			map[string]any{"subject_kind": kind, "subject_id": subj}, n.FolderID))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// checkSubject checks that a subject of a permission exists in accountID. An installation admin is
// refused: it has no account and no files, so a permission on it means nothing.
func (s *Server) checkSubject(ctx context.Context, tx *store.Tx, accountID, kind, subj string) error {
	if kind == store.SubjectGroup {
		if _, err := tx.Group(ctx, accountID, subj); errors.Is(err, store.ErrNotFound) {
			return notFound("group")
		} else if err != nil {
			return err
		}
		return nil
	}
	u, err := tx.User(ctx, accountID, subj)
	if errors.Is(err, store.ErrNotFound) {
		return notFound("user")
	}
	if err != nil {
		return err
	}
	if u.Role == store.RoleAdmin {
		return apierr.New(apierr.InvalidRequest, "an installation admin cannot hold a permission")
	}
	return nil
}

// checkGrantRole refuses a role above the caller's own on the node. An account key holds manager,
// so it grants any role.
func checkGrantRole(ctx context.Context, rd accessReader, p *auth.Principal, nodeID, role string) error {
	_, own, err := nodeAccessWith(ctx, rd, p, nodeID)
	if err != nil {
		return err
	}
	if store.RoleRank(role) > store.RoleRank(own) {
		return apierr.New(apierr.Forbidden, "you may not grant a role above your own")
	}
	return nil
}

// handleSharedWithMe lists the nodes that were shared with the caller: nodes it holds a permission
// on, directly or through a group. A node inside the caller's own personal folder is left out, and
// so is a node whose folder root the caller already reaches, since that reaches it too. The caller
// gets each node with its role there. An account key has no user and gets forbidden.
func (s *Server) handleSharedWithMe(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := callerCheck(p); err != nil {
		return err
	}
	if p.Kind != auth.KindUser {
		return apierr.New(apierr.Forbidden, "only a user has shared items; an account key has none")
	}
	page, err := pageParams(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	shared, next, err := s.store.SharedNodes(ctx, p.AccountID, p.UserID, p.Role == auth.RoleOwner, page)
	if err != nil {
		return storeErr(err)
	}
	out := listResponse[nodeJSON]{Items: []nodeJSON{}, NextCursor: next}
	for _, sn := range shared {
		n, role, err := nodeAccessWith(ctx, s.store, p, sn.NodeID)
		if apierr.From(err).Code == apierr.NotFound {
			continue // a token that does not reach the node leaves it out
		}
		if err != nil {
			return err
		}
		j := toNodeJSON(n)
		j.Role = role
		out.Items = append(out.Items, j)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}
