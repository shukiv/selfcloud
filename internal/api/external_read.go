// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"errors"
	"net/http"

	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// externalPrincipal builds the external reader's principal for a request, or returns nil to leave the caller as
// it authenticated (decision 188). It is called only on the routes that carry the ext flag, and only after the
// caller has authenticated. A signed-in user becomes an external reader of the node when every condition holds:
//
//   - it is an ordinary user session: not an admin, not a delegated token, not an impersonation, and not a user
//     who must still change its password;
//   - the node is not one its own account holds (that is an ordinary read, left to the normal walk);
//   - the node belongs to another account, whose status is active;
//   - the reader is active and has proved control of its address by a mailed link;
//   - both accounts have the external-sharing switch on;
//   - a live grant of the reader's address reaches the node from some root at or above it;
//   - in a members-only team space, that root is the space's own root.
//
// Every refusal returns nil, so the handler then answers not_found through the one access walk, the same answer
// a node the caller cannot reach gets. Only a store error is returned as an error. The principal it builds is
// never a key and never a member of the item's account: it reaches only what the grant gives, at viewer.
func (s *Server) externalPrincipal(r *http.Request, p *auth.Principal) (*auth.Principal, error) {
	if p == nil || p.Kind != auth.KindUser || p.IsAdmin() || p.Token != nil || p.Impersonator != "" || p.MustChangePassword {
		return nil, nil
	}
	ctx := r.Context()
	nod := r.PathValue("nod")
	// The reader's own account holds the node: an ordinary read, not a cross-account one.
	if _, err := s.store.Node(ctx, p.AccountID, nod); err == nil {
		return nil, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	itemAccount, err := s.store.NodeAccountID(ctx, nod)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if itemAccount == p.AccountID {
		return nil, nil
	}
	reader, err := s.store.User(ctx, p.AccountID, p.UserID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if reader.Status != store.UserActive || !reader.EmailProved() {
		return nil, nil
	}
	acc, err := s.store.Account(ctx, itemAccount)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if acc.Status != store.AccountActive {
		return nil, nil
	}
	for _, accountID := range []string{itemAccount, p.AccountID} {
		on, err := s.store.ExternalSharing(ctx, accountID)
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if !on {
			return nil, nil
		}
	}
	g, err := s.store.ExternalGrantRoot(ctx, itemAccount, nod, reader.EmailKey, store.Now())
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// A members-only team space reaches its root only: a grant below the root stops working once the space
	// becomes members-only, while a grant on the root still opens it.
	n, err := s.store.Node(ctx, itemAccount, nod)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f, err := s.store.Folder(ctx, itemAccount, n.FolderID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if f.Kind == store.KindTeam && f.MembersOnly {
		root, err := s.store.RootNode(ctx, itemAccount, f.ID)
		if err != nil {
			return nil, err
		}
		if g.NodeID != root.ID {
			return nil, nil
		}
	}
	return &auth.Principal{
		Kind:      auth.KindExternal,
		ID:        reader.ID,
		AccountID: itemAccount,
		Ext: &auth.ExternalScope{
			Node:            g.NodeID,
			Role:            store.RoleViewer,
			ReaderAccountID: p.AccountID,
			ReaderUserID:    reader.ID,
			ReaderEmail:     reader.Email,
			ExpiresAt:       g.ExpiresAt,
		},
	}, nil
}
