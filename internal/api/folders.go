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
	ID          string    `json:"id"`
	ExternalID  *string   `json:"external_id"`
	Name        string    `json:"name"`
	Kind        string    `json:"kind"`                    // team or personal
	OwnerUserID *string   `json:"owner_user_id,omitempty"` // the user of a personal folder
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	// The team-space settings (slice n2). They are always false on a personal folder.
	MembersOnly          bool `json:"members_only"`
	SharingManagersOnly  bool `json:"sharing_managers_only"`
	NoDownloadForViewers bool `json:"no_download_for_viewers"`
	AllowLinks           bool `json:"allow_links"`
}

func toFolderJSON(f *store.Folder) folderJSON {
	out := folderJSON{ID: f.ID, Name: f.Name, Kind: f.Kind, CreatedAt: f.CreatedAt, UpdatedAt: f.UpdatedAt,
		MembersOnly: f.MembersOnly, SharingManagersOnly: f.SharingManagersOnly,
		NoDownloadForViewers: f.NoDownloadForViewers, AllowLinks: f.AllowLinks}
	if f.ExternalID != "" {
		out.ExternalID = &f.ExternalID
	}
	if f.OwnerUserID != "" {
		out.OwnerUserID = &f.OwnerUserID
	}
	return out
}

// folderAccessJSON is a folder the caller can reach, with the level it has there.
type folderAccessJSON struct {
	folderJSON
	// Access is the grants-route level the caller holds (read or write), kept for clients that
	// predate roles. Role is the role itself.
	Access string `json:"access"`
	Role   string `json:"role"`
	// Hidden is true when the caller has hidden this team space from its own list. Hiding changes
	// nothing about access.
	Hidden bool `json:"hidden"`
}

// folderAccessOf builds a folder the caller reaches, from its folder JSON and the role it holds.
func folderAccessOf(f *store.Folder, role string) folderAccessJSON {
	return folderAccessJSON{folderJSON: toFolderJSON(f), Access: grantLevel(role), Role: role}
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
	// The management list is the team spaces. A personal folder is reached through its user, or
	// through GET /folders, and is not listed to whoever manages the account.
	folders, next, err := s.store.Folders(r.Context(), acc.ID,
		store.FolderFilter{ExternalID: r.URL.Query().Get("external_id"), Kind: store.KindTeam}, page)
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
// The account key reaches a personal folder like any other, so it may read one; every other
// caller, an owner session included, gets not_found for it.
func (s *Server) folderOf(r *http.Request, accountID string) (*store.Folder, error) {
	f, err := s.store.Folder(r.Context(), accountID, r.PathValue("fld"))
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound("folder")
	}
	if err == nil && f.Kind != store.KindTeam && principalOf(r).Kind != auth.KindAccountKey {
		return nil, notFound("folder")
	}
	return f, err
}

// teamFolderOf is folderOf for the grants routes, which apply to team spaces only.
func (s *Server) teamFolderOf(r *http.Request, accountID string) (*store.Folder, error) {
	f, err := s.folderOf(r, accountID)
	if err == nil && f.Kind != store.KindTeam {
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
		if err == nil && f.Kind != store.KindTeam {
			return notFound("folder")
		}
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
		// A personal folder goes with its user, through DELETE /users/{usr}, never through here.
		if f, err := tx.Folder(ctx, acc.ID, fld); err == nil && f.Kind != store.KindTeam {
			return notFound("folder")
		}
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

// grantLevel is the grants route's name for a role: viewer and commenter read, and every role that
// may change content writes. The grants routes exist for clients that predate roles.
func grantLevel(role string) string {
	if store.RoleRank(role) <= store.RoleRank(store.RoleCommenter) {
		return accessRead
	}
	return accessWrite
}

// grantRole is the role a grants route level writes: read is viewer, write is editor.
func grantRole(level string) string {
	if level == accessWrite {
		return store.RoleEditor
	}
	return store.RoleViewer
}

// handleListFolderGrants lists who holds a grant on a team space. The grants are the user permissions
// on its root, shown at the level they map to. A personal folder has no grants.
func (s *Server) handleListFolderGrants(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.managedAccount(r)
	if err != nil {
		return err
	}
	f, err := s.teamFolderOf(r, acc.ID)
	if err != nil {
		return err
	}
	root, err := s.store.RootNode(r.Context(), acc.ID, f.ID)
	if err != nil {
		return err
	}
	perms, err := s.store.NodePermissions(r.Context(), acc.ID, root.ID)
	if err != nil {
		return err
	}
	out := listResponse[grantJSON]{Items: []grantJSON{}}
	for _, pm := range perms {
		if pm.SubjectKind != store.SubjectUser {
			continue
		}
		out.Items = append(out.Items, grantJSON{UserID: pm.SubjectID, Level: grantLevel(pm.Role),
			CreatedAt: pm.CreatedAt, CreatedBy: pm.CreatedBy})
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// handleSetGrant shares a team space with a member user at a level, or changes the level of an
// existing grant. It writes a user permission on the folder's root: read is viewer and write is
// editor. Owners reach every team space without one, so a grant to anyone else is refused.
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
	var out grantJSON
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		f, err := tx.Folder(ctx, acc.ID, fld)
		if errors.Is(err, store.ErrNotFound) || (err == nil && f.Kind != store.KindTeam) {
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
		root, err := tx.RootNode(ctx, acc.ID, f.ID)
		if err != nil {
			return err
		}
		now := store.Now()
		if err := tx.SetPermission(ctx, &store.Permission{NodeID: root.ID, AccountID: acc.ID,
			SubjectKind: store.SubjectUser, SubjectID: u.ID, Role: grantRole(in.Level), CreatedAt: now,
			CreatedBy: p.ID}); err != nil {
			return err
		}
		out = grantJSON{FolderID: f.ID, UserID: u.ID, Level: in.Level, CreatedAt: now, CreatedBy: p.ID}
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "grant.set", f.ID+"/"+u.ID,
			map[string]any{"user_id": u.ID, "level": in.Level}))
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// handleRemoveGrant removes a user's permission on a team space's root.
func (s *Server) handleRemoveGrant(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.writableAccount(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	fld, usr := r.PathValue("fld"), r.PathValue("usr")
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		f, err := tx.Folder(ctx, acc.ID, fld)
		if errors.Is(err, store.ErrNotFound) || (err == nil && f.Kind != store.KindTeam) {
			return notFound("folder")
		} else if err != nil {
			return err
		}
		root, err := tx.RootNode(ctx, acc.ID, f.ID)
		if err != nil {
			return err
		}
		if err := tx.DeletePermission(ctx, root.ID, store.SubjectUser, usr); errors.Is(err, store.ErrNotFound) {
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

// handleListUserGrants lists the team spaces shared with a user, with their levels.
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
	grants, err := s.store.UserRootGrants(r.Context(), acc.ID, usr)
	if err != nil {
		return err
	}
	out := listResponse[grantJSON]{Items: []grantJSON{}}
	for _, g := range grants {
		out.Items = append(out.Items, grantJSON{FolderID: g.FolderID, Level: grantLevel(g.Role),
			CreatedAt: g.CreatedAt, CreatedBy: g.CreatedBy})
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// handleMyFolders lists the folders the caller can reach, paged the same way for everyone. A team
// space is listed when the caller reaches its root: an account owner reaches every team space, and
// a member the team spaces it holds a permission on, directly or through a group. The caller's own
// personal folder comes first on the first page. An account key gets every folder of its account,
// personal ones included, each with manager. Admins get forbidden, the same rule folderAccess
// applies: they manage tenants and never read their files.
func (s *Server) handleMyFolders(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := callerCheck(p); err != nil {
		return err
	}
	ctx := r.Context()
	out := listResponse[folderAccessJSON]{Items: []folderAccessJSON{}}
	page, err := pageParams(r)
	if err != nil {
		return err
	}
	// A user's hidden team spaces are left out unless ?hidden=true asks for only those. Tokens and
	// keys have no list of their own to hide from, so the filter is for users only.
	onlyHidden, err := hiddenParam(r)
	if err != nil {
		return err
	}
	if onlyHidden && (p.Token != nil || p.Kind != auth.KindUser) {
		// Only a user hides spaces, so for a token or a key the hidden list is empty.
		writeJSON(w, http.StatusOK, out)
		return nil
	}
	if p.Token != nil {
		// A token names at most tokenMaxFolders folders, so they are listed in one page, each at the
		// role folderAccess gives the token.
		for _, fid := range p.Token.Folders {
			f, role, err := folderAccessWith(ctx, s.store, p, fid)
			if err != nil {
				if apierr.From(err).Code == apierr.NotFound {
					continue // the folder is gone, or the caller reaches nothing at its root
				}
				return err
			}
			out.Items = append(out.Items, folderAccessOf(f, role))
		}
		writeJSON(w, http.StatusOK, out)
		return nil
	}
	if p.Kind != auth.KindUser {
		folders, next, err := s.store.Folders(ctx, p.AccountID, store.FolderFilter{}, page)
		if err != nil {
			return storeErr(err)
		}
		out.NextCursor = next
		for _, f := range folders {
			out.Items = append(out.Items, folderAccessOf(f, store.RoleManager))
		}
		writeJSON(w, http.StatusOK, out)
		return nil
	}

	own, err := s.store.PersonalFolder(ctx, p.AccountID, p.UserID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	hide := store.HideFilter{UserID: p.UserID, Only: onlyHidden}
	if onlyHidden {
		// A personal folder is never hidden, so the hidden listing leaves it out.
		own = nil
	}
	teams := func(tp store.Page) ([]folderAccessJSON, string, error) {
		if p.Role == auth.RoleOwner {
			folders, next, err := s.store.Folders(ctx, p.AccountID, store.FolderFilter{Kind: store.KindTeam, Hide: hide}, tp)
			if err != nil {
				return nil, "", storeErr(err)
			}
			var items []folderAccessJSON
			for _, f := range folders {
				items = append(items, folderAccessOf(f, store.RoleManager))
			}
			return items, next, nil
		}
		reached, next, err := s.store.RootReachedFolders(ctx, p.AccountID, p.UserID, hide, tp)
		if err != nil {
			return nil, "", storeErr(err)
		}
		var items []folderAccessJSON
		for _, rf := range reached {
			items = append(items, folderAccessOf(rf.Folder, store.RoleOfRank(rf.Rank)))
		}
		return items, next, nil
	}
	out.Items, out.NextCursor, err = pageWithPersonal(page, own, teams)
	if err != nil {
		return err
	}
	hidden, err := s.store.HiddenFolderIDs(ctx, p.AccountID, p.UserID)
	if err != nil {
		return err
	}
	for i := range out.Items {
		out.Items[i].Hidden = hidden[out.Items[i].ID]
	}
	// An empty listing is an empty array, never null: clients read items as a list.
	if out.Items == nil {
		out.Items = []folderAccessJSON{}
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// hiddenParam reads ?hidden=: absent or false leaves hidden spaces out, and true lists only them.
// Any other value is refused, so a typo does not silently show the wrong list.
func hiddenParam(r *http.Request) (bool, error) {
	switch v := r.URL.Query().Get("hidden"); v {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, apierr.New(apierr.InvalidRequest, "hidden must be true or false")
	}
}

// pageWithPersonal lists a caller's personal folder first, on the first page, then the team spaces.
// The personal folder takes a slot of the page, so a page never holds more than the limit. When the
// page is only that slot, the next page starts the team spaces from the start cursor.
func pageWithPersonal(page store.Page, own *store.Folder,
	teams func(store.Page) ([]folderAccessJSON, string, error)) ([]folderAccessJSON, string, error) {
	var items []folderAccessJSON
	tp := page
	if page.Cursor == "" && own != nil {
		items = append(items, folderAccessOf(own, store.RoleManager))
		if page.Limit <= 1 {
			return items, store.StartCursor(), nil
		}
		tp.Limit = page.Limit - 1
	}
	more, next, err := teams(tp)
	if err != nil {
		return nil, "", err
	}
	return append(items, more...), next, nil
}
