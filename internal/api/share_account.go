// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// The account-wide share link list and the bulk delete (design section 8, Share links). The owner, the account key and
// the installation admin see every live link of the account; members and tokens get the account's usual not_found. An
// owner sees the links on items in other members' My files without the item's name or path (decision 11 of the owner,
// 2026-10-29). An admin sees no item name or path at all, since admins never read files. The account key reads
// everything, as it reads the audit log.

// accountShareLinkJSON is one row of the account's list. Node and item fields are "" or null when hidden. Visitors is
// set for organisation links only. CreatedByName is "" for a link an account key made.
type accountShareLinkJSON struct {
	ID              string     `json:"id"`
	NodeID          string     `json:"node_id"`
	Audience        string     `json:"audience"`
	Role            string     `json:"role"`
	UploadOnly      bool       `json:"upload_only"`
	ExpiresAt       *time.Time `json:"expires_at"`
	CreatedAt       time.Time  `json:"created_at"`
	Paused          bool       `json:"paused"`
	PasswordSet     bool       `json:"password_set"`
	CreatedBy       string     `json:"created_by"`
	CreatedByKind   string     `json:"created_by_kind"`
	CreatedByName   string     `json:"created_by_name"`
	Visitors        *int       `json:"visitors,omitempty"`
	FolderID        string     `json:"folder_id"`
	FolderKind      string     `json:"folder_kind"`
	FolderName      string     `json:"folder_name"`
	FolderOwnerID   string     `json:"folder_owner_id"`
	FolderOwnerName string     `json:"folder_owner_name"`
	ItemName        *string    `json:"item_name"`
	ItemPath        *string    `json:"item_path"`
	Hidden          bool       `json:"hidden"`
}

// accountLinkHidden says whether the caller may not see the item a link sits on: an admin never does, and an owner
// does not for an item in another member's My files. An account key sees every item.
func accountLinkHidden(p *auth.Principal, l *store.AccountLink) bool {
	if p.IsAdmin() {
		return true
	}
	return p.Kind == auth.KindUser && l.FolderKind == store.KindPersonal && l.FolderOwnerID != p.UserID
}

// handleListAccountShareLinks lists the account's live links, with the filters audience and kind.
func (s *Server) handleListAccountShareLinks(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.managedAccount(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	filter := store.AccountLinkFilter{Audience: q.Get("audience"), Kind: q.Get("kind")}
	switch filter.Audience {
	case "", store.AudienceOrganisation, store.AudienceAnyone:
	default:
		return apierr.New(apierr.InvalidRequest, "audience must be organisation or anyone")
	}
	switch filter.Kind {
	case "", "access", "drop":
	default:
		return apierr.New(apierr.InvalidRequest, "kind must be access or drop")
	}
	page, err := pageParams(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	links, next, err := s.store.AccountShareLinks(ctx, acc.ID, time.Now(), filter, page)
	if err != nil {
		return storeErr(err)
	}
	p := principalOf(r)
	out := listResponse[accountShareLinkJSON]{Items: []accountShareLinkJSON{}, NextCursor: next}
	for _, l := range links {
		j := accountShareLinkJSON{ID: l.ID, Audience: l.Audience, Role: l.Role, UploadOnly: l.UploadOnly,
			ExpiresAt: l.ExpiresAt, CreatedAt: l.CreatedAt, Paused: l.Paused, PasswordSet: l.PasswordHash != "",
			CreatedBy: l.CreatedBy, CreatedByKind: l.CreatedKind, CreatedByName: l.CreatorName,
			FolderID: l.FolderID, FolderKind: l.FolderKind, FolderName: l.FolderName,
			FolderOwnerID: l.FolderOwnerID, FolderOwnerName: l.FolderOwner, Hidden: accountLinkHidden(p, l)}
		if l.Audience == store.AudienceOrganisation {
			visitors := l.Visitors
			j.Visitors = &visitors
		}
		if !j.Hidden {
			j.NodeID = l.NodeID
			name := l.NodeName
			j.ItemName = &name
			path, err := s.store.NodePathNames(ctx, acc.ID, l.NodeID)
			if err != nil {
				return err
			}
			full := "/" + strings.Join(path, "/")
			j.ItemPath = &full
		}
		out.Items = append(out.Items, j)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// handleDeletePublicShareLinks deletes every link that anyone can open in the account, access and drop links alike,
// at once. Their visitors lose access with them, and the open uploads of drop links are released as delete releases
// them. Each link gets its own entry, in the folder it sits on, and one entry for the whole action names the count.
// The web asks for confirmation first; this route does not.
func (s *Server) handleDeletePublicShareLinks(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.managedAccount(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var (
		released []*store.Upload
		deleted  int
	)
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		links, err := tx.AnyoneShareLinks(ctx, acc.ID)
		if err != nil {
			return err
		}
		for _, l := range links {
			sl, err := tx.ShareLink(ctx, acc.ID, l.ID)
			if err != nil {
				return err
			}
			if err := tx.DeleteShareLink(ctx, acc.ID, l.ID); err != nil {
				return err
			}
			ups, err := s.releaseDropUploads(r, tx, sl)
			if err != nil {
				return err
			}
			released = append(released, ups...)
			if err := tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "link.delete", l.ID,
				map[string]any{"audience": sl.Audience, "role": sl.Role, "upload_only": sl.UploadOnly}, l.FolderID)); err != nil {
				return err
			}
			deleted++
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "link.delete_public", acc.ID,
			map[string]any{"count": deleted}))
	})
	if err != nil {
		return err
	}
	for _, up := range released {
		s.discardUploadFile(up)
	}
	writeJSON(w, http.StatusOK, map[string]int{"deleted": deleted})
	return nil
}
