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

// handleTransferOwnership gives a file or folder to another active user of the same account. Only the
// node's owner, or an account key, may transfer it, and the caller needs editor on the node, as a move
// does.
//
// In a personal folder the item moves, with its subtree and direct permissions, into the new owner's My
// files, and the previous owner keeps editor on it. A name the new owner already has is renamed, as a
// move does. In a team space the item stays where it is: only the owner of the node and its subtree
// changes. Usage does not change in either case: both users are in the same account.
func (s *Server) handleTransferOwnership(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		To string `json:"to"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if err := validID("usr", in.To); err != nil {
		return apierr.New(apierr.InvalidRequest, "to must be the ID of a user (usr_...)")
	}
	p := principalOf(r)
	if err := callerCheck(p); err != nil {
		return err
	}
	if p.Token != nil {
		return apierr.New(apierr.Forbidden, "a delegated token cannot transfer ownership")
	}
	ctx := r.Context()
	nodeID := r.PathValue("nod")
	if !id.Valid("nod", nodeID) {
		return nodeNotFound()
	}
	var out *store.Node
	s.atWrite()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		n, role, err := nodeAccessWith(ctx, tx, p, nodeID)
		if err != nil {
			return err
		}
		isKey := p.Kind != auth.KindUser
		if !isKey && n.OwnerID != p.UserID {
			return apierr.New(apierr.Forbidden, "only the owner of the item, or an account key, can transfer it")
		}
		if store.RoleRank(role) < store.RoleRank(store.RoleEditor) {
			return roleTooLow(store.RoleEditor)
		}
		to, err := tx.User(ctx, n.AccountID, in.To)
		if errors.Is(err, store.ErrNotFound) {
			return notFound("user")
		}
		if err != nil {
			return err
		}
		if to.Status != store.UserActive {
			return apierr.New(apierr.InvalidRequest, "the new owner must be an active user of the account")
		}
		if n.IsRoot() {
			return apierr.New(apierr.InvalidRequest, "a folder's root cannot be transferred; transfer its contents instead")
		}
		if n.OwnerID == to.ID {
			out = n
			return nil
		}
		f, err := tx.Folder(ctx, n.AccountID, n.FolderID)
		if err != nil {
			return err
		}
		prev := n.OwnerID
		cur, err := tx.Node(ctx, n.AccountID, n.ID)
		if errors.Is(err, store.ErrNotFound) {
			return nodeNotFound()
		}
		if err != nil {
			return err
		}
		if f.Kind == store.KindTeam {
			if err := tx.SetSubtreeOwner(ctx, n.AccountID, cur.ID, to.ID); err != nil {
				return err
			}
			cur.OwnerID = to.ID
			out = cur
			return tx.InsertAudit(ctx, s.auditEntry(r, n.AccountID, "node.transfer", cur.ID,
				map[string]any{"from": prev, "to": to.ID}, n.FolderID))
		}
		// A personal folder's item moves into the new owner's My files, as a move does.
		dest, err := tx.PersonalFolder(ctx, n.AccountID, to.ID)
		if errors.Is(err, store.ErrNotFound) {
			return apierr.New(apierr.Conflict, "the new owner has no My files to receive the item")
		}
		if err != nil {
			return err
		}
		root, err := tx.RootNode(ctx, n.AccountID, dest.ID)
		if err != nil {
			return err
		}
		plan := &transferPlan{folderID: dest.ID, parent: root, name: cur.Name, key: cur.NameKey}
		taken, err := nameTakenBy(tx.Child(ctx, n.AccountID, root.ID, plan.key))
		if err != nil {
			return err
		}
		if taken != nil {
			if err := freeName(ctx, tx, plan, cur.Kind); err != nil {
				return err
			}
		}
		cur.FolderID, cur.ParentID = plan.folderID, root.ID
		cur.Name, cur.NameKey = plan.name, plan.key
		cur.OwnerID = to.ID
		cur.ModifiedAt, cur.ModifiedBy = store.Now(), p.ID
		if err := tx.UpdateNode(ctx, cur); err != nil {
			return storeNameErr(err)
		}
		if err := tx.SetSubtreeFolder(ctx, n.AccountID, cur.ID, dest.ID); err != nil {
			return err
		}
		// The caller manages the recipient's My files, not this subtree, so it cannot leave editors able to
		// share here: the flag comes off the moved item and everything below it.
		if err := tx.ClearSubtreeEditorsCanShare(ctx, n.AccountID, cur.ID); err != nil {
			return err
		}
		// The previous owner keeps editor on what it gave away. A role it already holds there is not
		// lowered, so a grant above editor stays.
		if prev != "" {
			held, err := tx.EffectiveRank(ctx, n.AccountID, cur.ID, prev)
			if err != nil {
				return err
			}
			if held < store.RoleRank(store.RoleEditor) {
				if err := tx.SetPermission(ctx, &store.Permission{NodeID: cur.ID, AccountID: n.AccountID,
					SubjectKind: store.SubjectUser, SubjectID: prev, Role: store.RoleEditor,
					CreatedAt: store.Now(), CreatedBy: p.ID}); err != nil {
					return err
				}
			}
		}
		out = cur
		// The audit entry is private to the new owner, since the item now lives in its My files.
		return tx.InsertAudit(ctx, s.auditEntry(r, n.AccountID, "node.transfer", cur.ID,
			map[string]any{"from": prev, "to": to.ID, "folder": dest.ID}, dest.ID))
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toNodeJSON(out))
	return nil
}
