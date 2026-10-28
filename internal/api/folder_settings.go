// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"errors"
	"net/http"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// handleUpdateFolderSettings changes a team space's restrictions. A manager of its root may, which covers
// the account's owners and an account key. A personal folder has no such settings, so it is refused.
// Every field is optional, but a request must name one.
func (s *Server) handleUpdateFolderSettings(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		MembersOnly          *bool `json:"members_only"`
		SharingManagersOnly  *bool `json:"sharing_managers_only"`
		NoDownloadForViewers *bool `json:"no_download_for_viewers"`
		AllowLinks           *bool `json:"allow_links"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if in.MembersOnly == nil && in.SharingManagersOnly == nil && in.NoDownloadForViewers == nil && in.AllowLinks == nil {
		return apierr.New(apierr.InvalidRequest, "give at least one setting")
	}
	p := principalOf(r)
	if err := callerCheck(p); err != nil {
		return err
	}
	if p.Token != nil {
		return apierr.New(apierr.Forbidden, "a delegated token cannot change a space's settings")
	}
	fid := r.PathValue("fld")
	if !id.Valid("fld", fid) {
		return notFound("folder")
	}
	ctx := r.Context()
	var out *store.Folder
	s.atWrite()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		f, err := tx.Folder(ctx, p.AccountID, fid)
		if errors.Is(err, store.ErrNotFound) {
			return notFound("folder")
		}
		if err != nil {
			return err
		}
		// The reach check comes first, so a caller who cannot reach a personal folder learns nothing
		// about it, not even its kind.
		root, err := tx.RootNode(ctx, p.AccountID, f.ID)
		if err != nil {
			return err
		}
		if _, err := requireNodeRole(ctx, tx, p, root.ID, store.RoleManager); err != nil {
			// A folder the caller cannot reach answers as a missing one does, so the answer does not tell
			// the two apart. The role check itself names the node, so its answer is replaced here.
			if apierr.From(err).Code == apierr.NotFound {
				return notFound("folder")
			}
			return err
		}
		if f.Kind != store.KindTeam {
			return apierr.New(apierr.InvalidRequest, "a personal folder has no team-space settings")
		}
		changed := map[string]any{}
		if in.MembersOnly != nil {
			f.MembersOnly = *in.MembersOnly
			changed["members_only"] = f.MembersOnly
		}
		if in.SharingManagersOnly != nil {
			f.SharingManagersOnly = *in.SharingManagersOnly
			changed["sharing_managers_only"] = f.SharingManagersOnly
		}
		if in.NoDownloadForViewers != nil {
			f.NoDownloadForViewers = *in.NoDownloadForViewers
			changed["no_download_for_viewers"] = f.NoDownloadForViewers
		}
		if in.AllowLinks != nil {
			f.AllowLinks = *in.AllowLinks
			changed["allow_links"] = f.AllowLinks
		}
		f.UpdatedAt = store.Now()
		if err := tx.UpdateFolderSettings(ctx, f); err != nil {
			return err
		}
		out = f
		return tx.InsertAudit(ctx, s.auditEntry(r, f.AccountID, "folder.settings", f.ID, changed, f.ID))
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toFolderJSON(out))
	return nil
}

// handleHideFolder hides a team space from the caller's own list. It is per user and changes nothing
// about access: the user still reaches the space, and it is still listed with ?hidden=true.
func (s *Server) handleHideFolder(w http.ResponseWriter, r *http.Request) error {
	return s.setFolderHidden(w, r, true)
}

// handleShowFolder shows a hidden team space again.
func (s *Server) handleShowFolder(w http.ResponseWriter, r *http.Request) error {
	return s.setFolderHidden(w, r, false)
}

// setFolderHidden records the caller's choice for a team space it reaches. Only a user can hide a space,
// and a personal folder is never hidden, since it is the user's own. Reaching a space is required, so a
// caller cannot hide a space it cannot see.
func (s *Server) setFolderHidden(w http.ResponseWriter, r *http.Request, hidden bool) error {
	p := principalOf(r)
	if err := callerCheck(p); err != nil {
		return err
	}
	if p.Kind != auth.KindUser {
		return apierr.New(apierr.Forbidden, "only a user can hide a team space from its own list")
	}
	fid := r.PathValue("fld")
	if !id.Valid("fld", fid) {
		return notFound("folder")
	}
	ctx := r.Context()
	f, _, err := folderAccessWith(ctx, s.store, p, fid)
	if err != nil {
		return err
	}
	if f.Kind != store.KindTeam {
		return apierr.New(apierr.InvalidRequest, "a personal folder cannot be hidden")
	}
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		// The reach check is repeated inside the write, so a membership removed meanwhile stops it.
		if _, _, err := folderAccessWith(ctx, tx, p, fid); err != nil {
			return err
		}
		return tx.SetHidden(ctx, p.AccountID, p.UserID, fid, hidden, store.Now())
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
