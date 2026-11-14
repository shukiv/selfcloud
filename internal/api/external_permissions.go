// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// maxExternalGrants is how many external grants one node holds (decision 187).
const maxExternalGrants = 100

// externalSharedLimit bounds the list of nodes other accounts shared with the caller.
const externalSharedLimit = 500

// externalPermissionJSON is an external grant as the API shows it.
type externalPermissionJSON struct {
	Email     string     `json:"email"`
	Role      string     `json:"role"`
	ExpiresAt *time.Time `json:"expires_at"`
	CreatedAt time.Time  `json:"created_at"`
	CreatedBy string     `json:"created_by"`
	// CreatedByName is the display name of the user that made the grant; "" for a key or a user that is gone.
	CreatedByName string `json:"created_by_name"`
}

func toExternalPermissionJSON(p *store.ExternalPermission) externalPermissionJSON {
	return externalPermissionJSON{Email: p.Email, Role: p.Role, ExpiresAt: p.ExpiresAt,
		CreatedAt: p.CreatedAt, CreatedBy: p.CreatedBy}
}

// handleListExternalPermissions lists the grants a node holds. Any role on the node reads them, as
// the internal permission list is read; a delegated token reads none (decision 187).
func (s *Server) handleListExternalPermissions(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if p != nil && p.Token != nil {
		return apierr.New(apierr.Forbidden, "a delegated token cannot read permissions")
	}
	n, _, err := s.nodeAccess(r, r.PathValue("nod"))
	if err != nil {
		return err
	}
	perms, err := s.store.ExternalPermissions(r.Context(), n.AccountID, n.ID)
	if err != nil {
		return storeErr(err)
	}
	ids := make([]string, len(perms))
	for i, pm := range perms {
		ids[i] = pm.CreatedBy
	}
	names, err := s.store.UserNames(r.Context(), n.AccountID, ids)
	if err != nil {
		return storeErr(err)
	}
	out := listResponse[externalPermissionJSON]{Items: []externalPermissionJSON{}}
	for _, pm := range perms {
		j := toExternalPermissionJSON(pm)
		j.CreatedByName = names[pm.CreatedBy]
		out.Items = append(out.Items, j)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// handleSetExternalPermission grants a node to an email address of another account, as viewer only.
// The role may be left out or empty, which mean viewer. The account's switch must be on. The answer
// is the same whether or not the address names a user anywhere: a grant names an address, not a user
// (decision 187).
func (s *Server) handleSetExternalPermission(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email     string     `json:"email"`
		Role      string     `json:"role"`
		ExpiresAt *time.Time `json:"expires_at"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	p := principalOf(r)
	if p.Token != nil {
		return apierr.New(apierr.Forbidden, "a delegated token cannot change permissions")
	}
	if err := s.rateLimit("external.share", string(p.Kind)+":"+p.ID); err != nil {
		return err
	}
	nodeID := r.PathValue("nod")
	ctx := r.Context()
	var out externalPermissionJSON
	var accountID string
	s.atWrite()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		n, _, err := requireSharer(ctx, tx, p, nodeID)
		if err != nil {
			return err
		}
		accountID = n.AccountID
		on, err := tx.ExternalSharing(ctx, n.AccountID)
		if err != nil {
			return err
		}
		if !on {
			return apierr.New(apierr.Forbidden, "the account does not share with other accounts").
				With("reason", "external_sharing_off")
		}
		email, err := cleanEmail(in.Email)
		if err != nil {
			return err
		}
		if in.Role != "" && in.Role != store.RoleViewer {
			return apierr.New(apierr.InvalidRequest, "role must be viewer")
		}
		if err := s.checkExpiry(in.ExpiresAt, time.Now()); err != nil {
			return err
		}
		key := emailKey(email)
		// An address of the account itself is shared with as a user, not as an external grant.
		if _, err := tx.UserByEmail(ctx, n.AccountID, key); err == nil {
			return apierr.New(apierr.InvalidRequest, "the address belongs to this account; share with it as a user").
				With("reason", "same_account")
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		f, err := tx.Folder(ctx, n.AccountID, n.FolderID)
		if err != nil {
			return err
		}
		if f.MembersOnly && !n.IsRoot() {
			return apierr.New(apierr.Conflict, "the team space is members only: external sharing reaches its root only")
		}
		count, err := tx.CountExternalPermissions(ctx, n.ID)
		if err != nil {
			return err
		}
		if count >= maxExternalGrants {
			if _, err := tx.ExternalPermissionOn(ctx, n.ID, key); errors.Is(err, store.ErrNotFound) {
				return apierr.New(apierr.Conflict, "the node holds too many external grants").
					With("reason", "too_many_external")
			} else if err != nil {
				return err
			}
		}
		pm := &store.ExternalPermission{NodeID: n.ID, AccountID: n.AccountID, Email: email, EmailKey: key,
			Role: store.RoleViewer, CreatedBy: p.ID, CreatedAt: store.Now(), ExpiresAt: in.ExpiresAt}
		if err := tx.SetExternalPermission(ctx, pm); err != nil {
			return err
		}
		// The stored row keeps the grant's origin; an update answers with it, not with the caller.
		stored, err := tx.ExternalPermissionOn(ctx, n.ID, key)
		if err != nil {
			return err
		}
		out = toExternalPermissionJSON(stored)
		details := map[string]any{"email": email, "role": store.RoleViewer}
		if in.ExpiresAt != nil {
			details["expires_at"] = in.ExpiresAt.UTC().Format(time.RFC3339)
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, n.AccountID, "permission.external.set", n.ID, details, n.FolderID))
	})
	if err != nil {
		return err
	}
	names, err := s.store.UserNames(ctx, accountID, []string{out.CreatedBy})
	if err != nil {
		return storeErr(err)
	}
	out.CreatedByName = names[out.CreatedBy]
	writeJSON(w, http.StatusOK, out)
	return nil
}

// handleRemoveExternalPermission removes the grant a node holds for an address. It works while the
// switch is off: turning the switch off keeps the grants, and they are still removed (decision 187).
func (s *Server) handleRemoveExternalPermission(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if p.Token != nil {
		return apierr.New(apierr.Forbidden, "a delegated token cannot change permissions")
	}
	email, err := cleanEmail(strings.TrimSpace(r.PathValue("email")))
	if err != nil {
		return err
	}
	key := emailKey(email)
	nodeID := r.PathValue("nod")
	ctx := r.Context()
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		n, _, err := requireSharer(ctx, tx, p, nodeID)
		if err != nil {
			return err
		}
		if err := tx.RemoveExternalPermission(ctx, n.ID, key); errors.Is(err, store.ErrNotFound) {
			return notFound("permission")
		} else if err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, n.AccountID, "permission.external.remove", n.ID,
			map[string]any{"email": email}, n.FolderID))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// externalSharedItem is a node another account shared with the caller: the fields a link's visitor
// sees, and the grant's own (decision 187).
type externalSharedItem struct {
	nodeJSON
	AccountName  string     `json:"account_name"`
	SharedByName string     `json:"shared_by_name"`
	ExpiresAt    *time.Time `json:"expires_at"`
}

// externalSharedList is the answer of GET /shared/external.
type externalSharedList struct {
	Items       []externalSharedItem `json:"items"`
	EmailProved bool                 `json:"email_proved"`
}

// handleSharedExternal lists the nodes other accounts shared with the caller's proved address. Both
// accounts' switches must be on. A delegated token, an impersonation, an unproved address and a
// switched-off account all get the empty list; an installation admin is refused (decision 187).
func (s *Server) handleSharedExternal(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := callerCheck(p); err != nil {
		return err
	}
	if p.Kind != auth.KindUser {
		return apierr.New(apierr.Forbidden, "only a user has shared items; an account key has none")
	}
	ctx := r.Context()
	u, err := s.store.User(ctx, p.AccountID, p.UserID)
	if errors.Is(err, store.ErrNotFound) {
		return apierr.New(apierr.Unauthenticated, "the session has ended; sign in again")
	}
	if err != nil {
		return err
	}
	out := externalSharedList{Items: []externalSharedItem{}, EmailProved: u.EmailProved()}
	if p.Token != nil || p.Impersonator != "" || !u.EmailProved() {
		writeJSON(w, http.StatusOK, out)
		return nil
	}
	on, err := s.store.ExternalSharing(ctx, p.AccountID)
	if err != nil {
		return storeErr(err)
	}
	if !on {
		writeJSON(w, http.StatusOK, out)
		return nil
	}
	shares, err := s.store.ExternalSharedWith(ctx, p.AccountID, u.EmailKey, externalSharedLimit)
	if err != nil {
		return storeErr(err)
	}
	visitor := &auth.Principal{Kind: auth.KindShareLink}
	for _, sh := range shares {
		self, above, err := s.store.TrashState(ctx, sh.Node.AccountID, sh.Node.ID)
		if err != nil {
			return storeErr(err)
		}
		if self || above {
			continue
		}
		j := nodeJSONFor(visitor, sh.Node)
		j.Role = sh.Permission.Role
		out.Items = append(out.Items, externalSharedItem{nodeJSON: j, AccountName: sh.AccountName,
			SharedByName: sh.SharedByName, ExpiresAt: sh.Permission.ExpiresAt})
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}
