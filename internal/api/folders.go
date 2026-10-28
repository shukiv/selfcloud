// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/names"
	"github.com/shukiv/selfcloud/internal/store"
)

type folderJSON struct {
	ID         string    `json:"id"`
	ExternalID *string   `json:"external_id"`
	Name       string    `json:"name"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func toFolderJSON(f *store.Folder) folderJSON {
	out := folderJSON{ID: f.ID, Name: f.Name, CreatedAt: f.CreatedAt, UpdatedAt: f.UpdatedAt}
	if f.ExternalID != "" {
		out.ExternalID = &f.ExternalID
	}
	return out
}

// folderAccessJSON is a folder the caller can reach, with the level it has there.
type folderAccessJSON struct {
	folderJSON
	Access string `json:"access"`
}

// grantJSON is a grant. A folder's grants list user_id; a user's grants list folder_id.
type grantJSON struct {
	FolderID  string    `json:"folder_id,omitempty"`
	UserID    string    `json:"user_id,omitempty"`
	Level     string    `json:"level"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by"`
}

// folderNameTaken is the error for a name key already used by another folder of the account.
func folderNameTaken(existing *store.Folder) error {
	return apierr.New(apierr.Conflict, "a folder with this name already exists in the account").
		With("id", existing.ID)
}

// handleCreateFolder makes a folder with its root. With an external_id, repeating the request with
// the same name returns the existing folder (200), so a retry is safe.
func (s *Server) handleCreateFolder(w http.ResponseWriter, r *http.Request) error {
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
	var out *store.Folder
	created := false
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		if ext != "" {
			existing, err := tx.FolderByExternalID(ctx, acc.ID, ext)
			switch {
			case err == nil && existing.NameKey == key:
				out = existing
				return nil
			case err == nil:
				return apierr.New(apierr.Conflict, "a folder with this external_id exists with a different name").
					With("id", existing.ID)
			case !errors.Is(err, store.ErrNotFound):
				return err
			}
		}
		if existing, err := tx.FolderByNameKey(ctx, acc.ID, key); err == nil {
			return folderNameTaken(existing)
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		now := store.Now()
		out = &store.Folder{ID: id.New("fld"), AccountID: acc.ID, ExternalID: ext, Name: name, NameKey: key,
			CreatedAt: now, UpdatedAt: now}
		if err := tx.InsertFolder(ctx, out, p.ID); errors.Is(err, store.ErrConflict) {
			return apierr.New(apierr.Conflict, "a folder with this name or external_id already exists in the account")
		} else if err != nil {
			return err
		}
		created = true
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "folder.create", out.ID,
			map[string]any{"name": name, "external_id": ext}))
	})
	if err != nil {
		return err
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
		w.Header().Set("Location", "/api/v1/accounts/"+acc.ID+"/folders/"+out.ID)
	}
	writeJSON(w, status, toFolderJSON(out))
	return nil
}

func (s *Server) handleListFolders(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.managedAccount(r)
	if err != nil {
		return err
	}
	page, err := pageParams(r)
	if err != nil {
		return err
	}
	folders, next, err := s.store.Folders(r.Context(), acc.ID,
		store.FolderFilter{ExternalID: r.URL.Query().Get("external_id")}, page)
	if err != nil {
		return storeErr(err)
	}
	out := listResponse[folderJSON]{Items: []folderJSON{}, NextCursor: next}
	for _, f := range folders {
		out.Items = append(out.Items, toFolderJSON(f))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// folderOf loads the folder named in the path, within the managed account.
func (s *Server) folderOf(r *http.Request, accountID string) (*store.Folder, error) {
	f, err := s.store.Folder(r.Context(), accountID, r.PathValue("fld"))
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound("folder")
	}
	return f, err
}

func (s *Server) handleGetFolder(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.managedAccount(r)
	if err != nil {
		return err
	}
	f, err := s.folderOf(r, acc.ID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toFolderJSON(f))
	return nil
}

// handleUpdateFolder renames a folder or changes its external ID. A name that another folder
// has is a conflict.
func (s *Server) handleUpdateFolder(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.writableAccount(r)
	if err != nil {
		return err
	}
	var in struct {
		Name       *string `json:"name"`
		ExternalID *string `json:"external_id"` // "" clears it
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	var out *store.Folder
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		f, err := tx.Folder(ctx, acc.ID, r.PathValue("fld"))
		if errors.Is(err, store.ErrNotFound) {
			return notFound("folder")
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
			if other, err := tx.FolderByNameKey(ctx, acc.ID, key); err == nil && other.ID != f.ID {
				return folderNameTaken(other)
			} else if err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
			f.Name, f.NameKey = name, key
			changed["name"] = name
		}
		if in.ExternalID != nil {
			if f.ExternalID, err = cleanExternalID(*in.ExternalID); err != nil {
				return err
			}
			changed["external_id"] = f.ExternalID
		}
		f.UpdatedAt = store.Now()
		if err := tx.UpdateFolder(ctx, f); errors.Is(err, store.ErrConflict) {
			return apierr.New(apierr.Conflict, "another folder in the account has this external_id")
		} else if err != nil {
			return err
		}
		out = f
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "folder.update", f.ID, changed))
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toFolderJSON(out))
	return nil
}

// handleDeleteFolder removes a folder with its grants and its file tree, and takes its files off
// the account's usage in the same transaction.
func (s *Server) handleDeleteFolder(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.writableAccount(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	fld := r.PathValue("fld")
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.DeleteFolder(ctx, acc.ID, fld); errors.Is(err, store.ErrNotFound) {
			return notFound("folder")
		} else if err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "folder.delete", fld, nil))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// handleListFolderGrants lists who holds a grant on a folder.
func (s *Server) handleListFolderGrants(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.managedAccount(r)
	if err != nil {
		return err
	}
	f, err := s.folderOf(r, acc.ID)
	if err != nil {
		return err
	}
	grants, err := s.store.GrantsOfFolder(r.Context(), f.ID)
	if err != nil {
		return err
	}
	out := listResponse[grantJSON]{Items: []grantJSON{}}
	for _, g := range grants {
		out.Items = append(out.Items, grantJSON{UserID: g.UserID, Level: g.Level, CreatedAt: g.CreatedAt, CreatedBy: g.CreatedBy})
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// handleSetGrant shares a folder with a member user at a level, or changes the level of an
// existing grant. Owners and admins reach every folder without one, so a grant to anyone else
// is refused.
func (s *Server) handleSetGrant(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.writableAccount(r)
	if err != nil {
		return err
	}
	var in struct {
		Level string `json:"level"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if in.Level != accessRead && in.Level != accessWrite {
		return apierr.New(apierr.InvalidRequest, "level must be read or write")
	}
	ctx := r.Context()
	p := principalOf(r)
	fld, usr := r.PathValue("fld"), r.PathValue("usr")
	var out *store.Grant
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		f, err := tx.Folder(ctx, acc.ID, fld)
		if errors.Is(err, store.ErrNotFound) {
			return notFound("folder")
		}
		if err != nil {
			return err
		}
		u, err := tx.User(ctx, acc.ID, usr)
		if errors.Is(err, store.ErrNotFound) {
			return notFound("user")
		}
		if err != nil {
			return err
		}
		if u.Role != store.RoleMember {
			return apierr.New(apierr.InvalidRequest,
				"only a member can hold a grant; owners reach every folder of the account without one")
		}
		out = &store.Grant{FolderID: f.ID, UserID: u.ID, Level: in.Level, CreatedAt: store.Now(), CreatedBy: p.ID}
		if err := tx.SetGrant(ctx, out); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "grant.set", f.ID+"/"+u.ID,
			map[string]any{"user_id": u.ID, "level": in.Level}))
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, grantJSON{FolderID: out.FolderID, UserID: out.UserID, Level: out.Level,
		CreatedAt: out.CreatedAt, CreatedBy: out.CreatedBy})
	return nil
}

func (s *Server) handleRemoveGrant(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.writableAccount(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	fld, usr := r.PathValue("fld"), r.PathValue("usr")
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		if _, err := tx.Folder(ctx, acc.ID, fld); errors.Is(err, store.ErrNotFound) {
			return notFound("folder")
		} else if err != nil {
			return err
		}
		if err := tx.DeleteGrant(ctx, fld, usr); errors.Is(err, store.ErrNotFound) {
			return notFound("grant")
		} else if err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "grant.remove", fld+"/"+usr, nil))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// handleListUserGrants lists the folders shared with a user, with their levels.
func (s *Server) handleListUserGrants(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.managedAccount(r)
	if err != nil {
		return err
	}
	usr := r.PathValue("usr")
	if _, err := s.store.User(r.Context(), acc.ID, usr); errors.Is(err, store.ErrNotFound) {
		return notFound("user")
	} else if err != nil {
		return err
	}
	grants, err := s.store.GrantsOfUser(r.Context(), acc.ID, usr)
	if err != nil {
		return err
	}
	out := listResponse[grantJSON]{Items: []grantJSON{}}
	for _, g := range grants {
		out.Items = append(out.Items, grantJSON{FolderID: g.FolderID, Level: g.Level, CreatedAt: g.CreatedAt, CreatedBy: g.CreatedBy})
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// handleMyFolders lists the folders the caller can reach, paged the same way for everyone. An
// account key or an owner session gets every folder of its account with write. A member session
// gets its granted folders at the granted level. Admins get forbidden, the same rule folderAccess
// applies: they manage tenants and never read their files.
func (s *Server) handleMyFolders(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if p.IsAdmin() {
		return apierr.New(apierr.Forbidden, "admins manage tenants and may not read their files")
	}
	ctx := r.Context()
	out := listResponse[folderAccessJSON]{Items: []folderAccessJSON{}}
	page, err := pageParams(r)
	if err != nil {
		return err
	}
	if p.Kind == auth.KindUser && p.Role == auth.RoleMember {
		granted, next, err := s.store.GrantedFolders(ctx, p.AccountID, p.UserID, page)
		if err != nil {
			return storeErr(err)
		}
		out.NextCursor = next
		for _, g := range granted {
			out.Items = append(out.Items, folderAccessJSON{folderJSON: toFolderJSON(g.Folder), Access: g.Level})
		}
		writeJSON(w, http.StatusOK, out)
		return nil
	}
	folders, next, err := s.store.Folders(ctx, p.AccountID, store.FolderFilter{}, page)
	if err != nil {
		return storeErr(err)
	}
	out.NextCursor = next
	for _, f := range folders {
		out.Items = append(out.Items, folderAccessJSON{folderJSON: toFolderJSON(f), Access: accessWrite})
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}
