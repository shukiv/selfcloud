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
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// maxMessageRunes is the longest note a share or an access request may carry.
const maxMessageRunes = 500

// permissionJSON is a permission on a node. InheritedFrom is the ancestor the permission sits on,
// and null when it sits on the node itself. ExpiresAt is null for a permission that never ends.
type permissionJSON struct {
	SubjectKind   string     `json:"subject_kind"` // user or group
	SubjectID     string     `json:"subject_id"`
	Role          string     `json:"role"`
	InheritedFrom *string    `json:"inherited_from"`
	ExpiresAt     *time.Time `json:"expires_at"`
	CreatedAt     time.Time  `json:"created_at"`
	CreatedBy     string     `json:"created_by"`
	// SubjectName is the user's display name, or email, or the group's name. SubjectStatus is the
	// user's status: pending, active or disabled. Both are shown only to a caller that sees the full
	// chain: a manager of the node, or an account key. Other callers get neither.
	SubjectName   string `json:"subject_name,omitempty"`
	SubjectStatus string `json:"subject_status,omitempty"`
}

// setPermissionJSON is the answer to a change: the permission as it now stands, and whether the
// subject was told by mail. Reason says why a subject was not told, when it was not.
type setPermissionJSON struct {
	permissionJSON
	Notified bool   `json:"notified"`
	Reason   string `json:"reason,omitempty"`
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

// subjectDetails returns the name a permission's subject is shown by, and a user's status. A user
// subject that no longer exists has no name; the caller then shows none.
func (s *Server) subjectDetails(ctx context.Context, accountID, kind, subj string) (name, status string) {
	if kind == store.SubjectGroup {
		g, err := s.store.Group(ctx, accountID, subj)
		if err != nil {
			return "", ""
		}
		return g.Name, ""
	}
	u, err := s.store.User(ctx, accountID, subj)
	if err != nil {
		return "", ""
	}
	name = u.DisplayName
	if name == "" {
		name = u.Email
	}
	return name, u.Status
}

// seesFullChain reports whether a caller sees every permission that reaches a node, and the names of
// their subjects: an account key, or a caller whose role on the node is manager.
func seesFullChain(p *auth.Principal, role string) bool {
	return p.Kind != auth.KindUser || store.RoleRank(role) >= store.RoleRank(store.RoleManager)
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
	full := seesFullChain(p, role)
	out := listResponse[permissionJSON]{Items: []permissionJSON{}}
	for _, pm := range perms {
		j := toPermissionJSON(pm)
		if pm.NodeID != n.ID {
			from := pm.NodeID
			j.InheritedFrom = &from
		}
		if full {
			j.SubjectName, j.SubjectStatus = s.subjectDetails(r.Context(), n.AccountID, pm.SubjectKind, pm.SubjectID)
		}
		out.Items = append(out.Items, j)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// toPermissionJSON is a stored permission without its inheritance or subject details.
func toPermissionJSON(pm *store.Permission) permissionJSON {
	return permissionJSON{SubjectKind: pm.SubjectKind, SubjectID: pm.SubjectID, Role: pm.Role,
		ExpiresAt: pm.ExpiresAt, CreatedAt: pm.CreatedAt, CreatedBy: pm.CreatedBy}
}

// sharingReader is what the sharing rule reads: the store, or a write transaction.
type sharingReader interface {
	EditorsCanShare(ctx context.Context, accountID, nodeID string) (bool, error)
	SharingManagersOnly(ctx context.Context, accountID, nodeID string) (bool, error)
}

// mayShare reports whether a caller at role on n may set and remove permissions there. A manager may,
// and so may an editor when the node or an ancestor allows editors to share and the team space does not
// keep sharing for managers. The caller's own role bounds what it may grant, so an editor cannot make a
// manager. An account key is at manager.
func mayShare(ctx context.Context, rd sharingReader, n *store.Node, role string) (bool, error) {
	switch store.RoleRank(role) {
	case store.RoleRank(store.RoleManager):
		return true, nil
	case store.RoleRank(store.RoleEditor):
		allowed, err := rd.EditorsCanShare(ctx, n.AccountID, n.ID)
		if err != nil || !allowed {
			return false, err
		}
		managersOnly, err := rd.SharingManagersOnly(ctx, n.AccountID, n.ID)
		if err != nil {
			return false, err
		}
		return !managersOnly, nil
	}
	return false, nil
}

// requireSharer checks, inside tx, that the caller may set permissions on a node (see mayShare). It
// returns the node and the role the caller holds there, which bounds what it may grant.
func requireSharer(ctx context.Context, tx *store.Tx, p *auth.Principal, nodeID string) (*store.Node, string, error) {
	n, role, err := nodeAccessWith(ctx, tx, p, nodeID)
	if err != nil {
		return nil, "", err
	}
	ok, err := mayShare(ctx, tx, n, role)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return nil, "", roleTooLow(store.RoleManager)
	}
	return n, role, nil
}

// checkNotAbove refuses a change to a permission held above the caller's own role. An editor, for
// example, may not change or remove a manager's row on the node. A row the caller cannot see is not
// checked here: the caller's own role already bounds what a row can hold.
func checkNotAbove(ctx context.Context, tx *store.Tx, nodeID, kind, subj, own string) error {
	existing, err := tx.PermissionOn(ctx, nodeID, kind, subj, time.Now())
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if store.RoleRank(existing.Role) > store.RoleRank(own) {
		return apierr.New(apierr.Forbidden, "you may not change a permission above your own role")
	}
	return nil
}

// checkMembersOnly enforces a team space's members-only setting for a permission below its root. The
// subject must already hold a permission on the root, directly or through a group, so an outsider
// cannot be named on a folder inside the space. A permission on the root itself is never refused here.
func checkMembersOnly(ctx context.Context, tx *store.Tx, f *store.Folder, n *store.Node, kind, subj string) error {
	if !f.MembersOnly || n.IsRoot() {
		return nil
	}
	root, err := tx.RootNode(ctx, n.AccountID, f.ID)
	if err != nil {
		return err
	}
	ok := false
	if kind == store.SubjectGroup {
		_, err := tx.PermissionOn(ctx, root.ID, store.SubjectGroup, subj, time.Now())
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		ok = err == nil
	} else {
		rank, err := tx.EffectiveRank(ctx, n.AccountID, root.ID, subj)
		if err != nil {
			return err
		}
		ok = rank > 0
	}
	if !ok {
		return apierr.New(apierr.Conflict, "the team space is members only: give the user or group a role on the space first")
	}
	return nil
}

// checkExpiry checks a permission's expires_at. It must lie in the future, and at most sharing.max_expiry
// ahead, unless that is zero, which means no cap. A nil value means the permission never ends.
func (s *Server) checkExpiry(at *time.Time, now time.Time) error {
	if at == nil {
		return nil
	}
	if !at.After(now) {
		return apierr.New(apierr.InvalidRequest, "expires_at must be in the future")
	}
	if max := s.cfg.Sharing.MaxExpiry.D(); max > 0 && at.After(now.Add(max)) {
		return apierr.Newf(apierr.InvalidRequest, "expires_at may be at most %d seconds from now", int64(max/time.Second)).
			With("max_expiry_seconds", int64(max/time.Second))
	}
	return nil
}

// handleSetNodePermission gives a user or a group a role on a node, or changes the role it has. The
// caller needs manager on the node, or an editor when the node allows editors to share, or is an
// account key. A caller may not grant a role above its own, so an editor cannot make a manager. The
// subject must belong to the same account, and an installation admin cannot hold a permission.
//
// The body may give expires_at, after which the permission grants nothing, and a note for the mail the
// subject gets. notify (default true) decides whether the subject is mailed at all. A delegated token
// cannot change permissions.
func (s *Server) handleSetNodePermission(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Role      string     `json:"role"`
		ExpiresAt *time.Time `json:"expires_at"`
		Notify    *bool      `json:"notify"`
		Message   string     `json:"message"`
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
	in.Message = cleanNote(in.Message)
	if utf8.RuneCountInString(in.Message) > maxMessageRunes {
		return apierr.Newf(apierr.InvalidRequest, "message may be at most %d characters", maxMessageRunes)
	}
	now := time.Now()
	if err := s.checkExpiry(in.ExpiresAt, now); err != nil {
		return err
	}
	ctx := r.Context()
	p := principalOf(r)
	if p.Token != nil {
		return apierr.New(apierr.Forbidden, "a delegated token cannot change permissions")
	}
	nodeID := r.PathValue("nod")
	var out permissionJSON
	var n *store.Node
	var f *store.Folder
	var own string
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		var err error
		n, own, err = requireSharer(ctx, tx, p, nodeID)
		if err != nil {
			return err
		}
		if err := s.checkSubject(ctx, tx, n.AccountID, kind, subj); err != nil {
			return err
		}
		if err := checkGrantRole(ctx, tx, p, n.ID, in.Role); err != nil {
			return err
		}
		if err := checkNotAbove(ctx, tx, n.ID, kind, subj, own); err != nil {
			return err
		}
		f, err = tx.Folder(ctx, n.AccountID, n.FolderID)
		if err != nil {
			return err
		}
		if err := checkMembersOnly(ctx, tx, f, n, kind, subj); err != nil {
			return err
		}
		created := store.Now()
		pm := &store.Permission{NodeID: n.ID, AccountID: n.AccountID, SubjectKind: kind, SubjectID: subj,
			Role: in.Role, CreatedAt: created, CreatedBy: p.ID, ExpiresAt: in.ExpiresAt}
		if err := tx.SetPermission(ctx, pm); err != nil {
			return err
		}
		out = toPermissionJSON(pm)
		details := map[string]any{"subject_kind": kind, "subject_id": subj, "role": in.Role}
		if in.ExpiresAt != nil {
			details["expires_at"] = in.ExpiresAt.UTC().Format(time.RFC3339)
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, n.AccountID, "permission.set", n.ID, details, n.FolderID))
	})
	if err != nil {
		return err
	}
	notice := shareNotice{Reason: "not_requested"}
	if in.Notify == nil || *in.Notify {
		notice = s.shareMail(ctx, p, n, f, kind, subj, in.Role, in.Message)
	}
	if seesFullChain(p, own) {
		out.SubjectName, out.SubjectStatus = s.subjectDetails(ctx, n.AccountID, kind, subj)
	}
	writeJSON(w, http.StatusOK, setPermissionJSON{permissionJSON: out, Notified: notice.Notified, Reason: notice.Reason})
	return nil
}

// handleRemoveNodePermission removes a permission that sits on the node itself. A permission above
// the node is removed on the node it sits on. The caller needs the same right as to set one.
func (s *Server) handleRemoveNodePermission(w http.ResponseWriter, r *http.Request) error {
	kind, subj, err := subjectOf(r.PathValue("subject"))
	if err != nil {
		return err
	}
	ctx := r.Context()
	p := principalOf(r)
	if p.Token != nil {
		return apierr.New(apierr.Forbidden, "a delegated token cannot change permissions")
	}
	nodeID := r.PathValue("nod")
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		n, own, err := requireSharer(ctx, tx, p, nodeID)
		if err != nil {
			return err
		}
		if err := checkNotAbove(ctx, tx, n.ID, kind, subj, own); err != nil {
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

// checkGrantRole refuses a role above the caller's own on the node. An account key holds manager, so
// it grants any role.
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
