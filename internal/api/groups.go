// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/names"
	"github.com/shukiv/selfcloud/internal/store"
)

// groupJSON is a group of an account.
type groupJSON struct {
	ID         string    `json:"id"`
	ExternalID *string   `json:"external_id"`
	Name       string    `json:"name"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func toGroupJSON(g *store.Group) groupJSON {
	out := groupJSON{ID: g.ID, Name: g.Name, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt}
	if g.ExternalID != "" {
		out.ExternalID = &g.ExternalID
	}
	return out
}

// groupMemberJSON is a user in a group.
type groupMemberJSON struct {
	UserID    string    `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
}

// groupNameTaken is the error for a name key already used by another group of the account.
func groupNameTaken(existing *store.Group) error {
	return apierr.New(apierr.Conflict, "a group with this name already exists in the account").
		With("id", existing.ID)
}

// groupOf loads the group named in the path, within the managed account.
func (s *Server) groupOf(r *http.Request, accountID string) (*store.Group, error) {
	g, err := s.store.Group(r.Context(), accountID, r.PathValue("grp"))
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound("group")
	}
	return g, err
}

// handleCreateGroup makes a group. With an external_id, repeating the request with the same name
// returns the existing group (200), so a retry is safe, as for folders.
func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.writableAccount(r)
	if err != nil {
		return err
	}
	var in struct {
		Name       string `json:"name"`
		ExternalID string `json:"external_id"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	name, key, err := names.Clean(in.Name)
	if err != nil {
		return err
	}
	ext, err := cleanExternalID(in.ExternalID)
	if err != nil {
		return err
	}
	ctx := r.Context()
	p := principalOf(r)
	var out *store.Group
	created := false
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		if ext != "" {
			existing, err := tx.GroupByExternalID(ctx, acc.ID, ext)
			switch {
			case err == nil && existing.NameKey == key:
				out = existing
				return nil
			case err == nil:
				return apierr.New(apierr.Conflict, "a group with this external_id exists with a different name").
					With("id", existing.ID)
			case !errors.Is(err, store.ErrNotFound):
				return err
			}
		}
		if existing, err := tx.GroupByNameKey(ctx, acc.ID, key); err == nil {
			return groupNameTaken(existing)
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		now := store.Now()
		out = &store.Group{ID: id.New("grp"), AccountID: acc.ID, ExternalID: ext, Name: name, NameKey: key,
			CreatedAt: now, UpdatedAt: now}
		if err := tx.InsertGroup(ctx, out); errors.Is(err, store.ErrConflict) {
			return apierr.New(apierr.Conflict, "a group with this name or external_id already exists in the account")
		} else if err != nil {
			return err
		}
		created = true
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "group.create", out.ID,
			map[string]any{"name": name, "external_id": ext, "by": p.ID}))
	})
	if err != nil {
		return err
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
		w.Header().Set("Location", "/api/v1/accounts/"+acc.ID+"/groups/"+out.ID)
	}
	writeJSON(w, status, toGroupJSON(out))
	return nil
}

// handleListGroups lists the groups of the account, oldest first.
func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.managedAccount(r)
	if err != nil {
		return err
	}
	page, err := pageParams(r)
	if err != nil {
		return err
	}
	groups, next, err := s.store.Groups(r.Context(), acc.ID, page)
	if err != nil {
		return storeErr(err)
	}
	out := listResponse[groupJSON]{Items: []groupJSON{}, NextCursor: next}
	for _, g := range groups {
		out.Items = append(out.Items, toGroupJSON(g))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// handleGetGroup returns one group of the account.
func (s *Server) handleGetGroup(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.managedAccount(r)
	if err != nil {
		return err
	}
	g, err := s.groupOf(r, acc.ID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toGroupJSON(g))
	return nil
}

// handleUpdateGroup renames a group or changes its external ID. A name another group has is a
// conflict.
func (s *Server) handleUpdateGroup(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.writableAccount(r)
	if err != nil {
		return err
	}
	var in struct {
		Name       *string `json:"name"`
		ExternalID *string `json:"external_id"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	p := principalOf(r)
	var out *store.Group
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		g, err := tx.Group(ctx, acc.ID, r.PathValue("grp"))
		if errors.Is(err, store.ErrNotFound) {
			return notFound("group")
		}
		if err != nil {
			return err
		}
		changed := map[string]any{}
		if in.Name != nil {
			name, key, err := names.Clean(*in.Name)
			if err != nil {
				return err
			}
			if other, err := tx.GroupByNameKey(ctx, acc.ID, key); err == nil && other.ID != g.ID {
				return groupNameTaken(other)
			} else if err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
			g.Name, g.NameKey = name, key
			changed["name"] = name
		}
		if in.ExternalID != nil {
			ext, err := cleanExternalID(*in.ExternalID)
			if err != nil {
				return err
			}
			g.ExternalID = ext
			changed["external_id"] = ext
		}
		g.UpdatedAt = store.Now()
		if err := tx.UpdateGroup(ctx, g); errors.Is(err, store.ErrConflict) {
			return apierr.New(apierr.Conflict, "a group with this external_id already exists in the account")
		} else if err != nil {
			return err
		}
		out = g
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "group.update", g.ID,
			map[string]any{"changed": changed, "by": p.ID}))
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toGroupJSON(out))
	return nil
}

// handleDeleteGroup removes a group, its members and every permission it holds. The permissions go
// in the same transaction, since a permission names its group without a foreign key.
func (s *Server) handleDeleteGroup(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.writableAccount(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	grp := r.PathValue("grp")
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		if _, err := tx.Group(ctx, acc.ID, grp); errors.Is(err, store.ErrNotFound) {
			return notFound("group")
		} else if err != nil {
			return err
		}
		n, err := tx.DeleteSubjectPermissions(ctx, acc.ID, store.SubjectGroup, grp)
		if err != nil {
			return err
		}
		if err := tx.DeleteGroup(ctx, acc.ID, grp); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "group.delete", grp,
			map[string]any{"permissions": n}))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// handleListGroupMembers lists the members of a group, earliest first.
func (s *Server) handleListGroupMembers(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.managedAccount(r)
	if err != nil {
		return err
	}
	g, err := s.groupOf(r, acc.ID)
	if err != nil {
		return err
	}
	page, err := pageParams(r)
	if err != nil {
		return err
	}
	members, next, err := s.store.GroupMembers(r.Context(), g.ID, page)
	if err != nil {
		return storeErr(err)
	}
	out := listResponse[groupMemberJSON]{Items: []groupMemberJSON{}, NextCursor: next}
	for _, m := range members {
		out.Items = append(out.Items, groupMemberJSON{UserID: m.UserID, CreatedAt: m.CreatedAt})
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// handleAddGroupMember puts a user of the account in a group. A user already in it is left as it is.
// Any role may be a member; an installation admin may not, since it has no account.
func (s *Server) handleAddGroupMember(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.writableAccount(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	grp, usr := r.PathValue("grp"), r.PathValue("usr")
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		if _, err := tx.Group(ctx, acc.ID, grp); errors.Is(err, store.ErrNotFound) {
			return notFound("group")
		} else if err != nil {
			return err
		}
		u, err := tx.User(ctx, acc.ID, usr)
		if errors.Is(err, store.ErrNotFound) {
			return notFound("user")
		}
		if err != nil {
			return err
		}
		if u.Role == store.RoleAdmin {
			return apierr.New(apierr.InvalidRequest, "an installation admin cannot be a group member")
		}
		if _, err := tx.GroupMemberOf(ctx, grp, usr); err == nil {
			return nil
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err := tx.AddGroupMember(ctx, &store.GroupMember{GroupID: grp, UserID: usr, CreatedAt: store.Now()}); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "group.member.add", grp,
			map[string]any{"user_id": usr}))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// handleRemoveGroupMember takes a user out of a group. The user's group permissions stop at once,
// since they are read from the membership at every check.
func (s *Server) handleRemoveGroupMember(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.writableAccount(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	grp, usr := r.PathValue("grp"), r.PathValue("usr")
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		if _, err := tx.Group(ctx, acc.ID, grp); errors.Is(err, store.ErrNotFound) {
			return notFound("group")
		} else if err != nil {
			return err
		}
		if err := tx.RemoveGroupMember(ctx, grp, usr); errors.Is(err, store.ErrNotFound) {
			return notFound("member")
		} else if err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "group.member.remove", grp,
			map[string]any{"user_id": usr}))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
