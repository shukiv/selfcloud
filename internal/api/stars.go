// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// maxStarsPerUser is how many stars one user may hold, listed or not.
const maxStarsPerUser = 5000

// userCaller refuses the callers that have no user of their own: no caller at all, an installation admin, and an
// account key. A signed-in user, and a delegated token that acts as its user, pass. Stars, starred, recent and
// shared-by-recency belong to a user, so the key and the admin get forbidden, as /shared does.
func userCaller(p *auth.Principal, what string) error {
	if err := callerCheck(p); err != nil {
		return err
	}
	if p.Kind != auth.KindUser {
		return apierr.New(apierr.Forbidden, "only a user has "+what+"; an account key has none")
	}
	return nil
}

// changeCaller is userCaller for the calls that change a user's stars. A delegated token may change them only with
// write: a read-only token acts for its user's reading, never for its stars.
func changeCaller(p *auth.Principal, what string) error {
	if err := userCaller(p, what); err != nil {
		return err
	}
	if p.Token != nil && !p.Token.Write {
		return apierr.New(apierr.Forbidden, "a read-only token cannot change "+what)
	}
	return nil
}

// ownListCaller is the caller of the calls that clear or remove entries of the recent list. A delegated token has no
// part in them, whatever its scope or permissions: the list is the user's own, and only the user, signed in, clears it.
func ownListCaller(p *auth.Principal) error {
	if err := userCaller(p, "a recent list"); err != nil {
		return err
	}
	if p.Token != nil {
		return apierr.New(apierr.Forbidden, "a delegated token cannot clear or remove recent items; only the signed-in user can")
	}
	return nil
}

// starredItem is a node in the starred list, with the time it was starred.
type starredItem struct {
	nodeJSON
	StarredAt time.Time `json:"starred_at"`
}

// withStarred sets the starred flag of each node JSON in items from one query: the caller's own stars among them.
func (s *Server) withStarred(r *http.Request, userID string, items []*nodeJSON) error {
	ids := make([]string, len(items))
	for i, j := range items {
		ids[i] = j.ID
	}
	set, err := s.store.StarredAmong(r.Context(), userID, ids)
	if err != nil {
		return err
	}
	for _, j := range items {
		starred := set[j.ID]
		j.Starred = &starred
	}
	return nil
}

// handleStarNode stars a node for the caller. The caller must reach the node at any role, and a star past
// maxStarsPerUser is refused with conflict and reason limit. Starring again changes nothing.
func (s *Server) handleStarNode(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := changeCaller(p, "stars"); err != nil {
		return err
	}
	nodeID := r.PathValue("nod")
	if !id.Valid("nod", nodeID) {
		return nodeNotFound()
	}
	ctx := r.Context()
	if _, _, err := nodeAccessWith(ctx, s.store, p, nodeID); err != nil {
		return err
	}
	s.atWrite()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		// The reach is checked again inside the write, so a grant revoked during the request stops the star.
		n, _, err := nodeAccessWith(ctx, tx, p, nodeID)
		if err != nil {
			return err
		}
		has, err := tx.HasStar(ctx, p.UserID, nodeID)
		if err != nil {
			return err
		}
		if has {
			return nil
		}
		count, err := tx.CountStars(ctx, p.UserID)
		if err != nil {
			return err
		}
		if count >= s.starLimit {
			return apierr.Newf(apierr.Conflict, "you may star at most %d items; remove some stars first",
				s.starLimit).With("reason", "limit").With("limit", s.starLimit)
		}
		return tx.InsertStar(ctx, n.AccountID, p.UserID, nodeID, store.Now())
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// handleUnstarNode removes the caller's own star on a node. A signed-in user needs no reach for it: a star on a node
// that is trashed or no longer shared must still come off, or it would keep counting toward the limit. Any well-formed
// node ID answers 204, so the answer reveals nothing about the node. A token acting for the user needs write and reach
// to change a star, as starring does.
func (s *Server) handleUnstarNode(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := changeCaller(p, "stars"); err != nil {
		return err
	}
	nodeID := r.PathValue("nod")
	if !id.Valid("nod", nodeID) {
		return nodeNotFound()
	}
	ctx := r.Context()
	if p.Token != nil {
		if _, _, err := nodeAccessWith(ctx, s.store, p, nodeID); err != nil {
			return err
		}
	}
	s.atWrite()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		if p.Token != nil {
			if _, _, err := nodeAccessWith(ctx, tx, p, nodeID); err != nil {
				return err
			}
		}
		return tx.DeleteStar(ctx, p.UserID, nodeID)
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// handleListStarred lists the caller's starred items, newest star first. A star on a node the caller no longer
// reaches, or one in the trash, stays in the database and is not listed. A page can therefore come back shorter
// than its limit, as /shared's does; next_cursor is set when the store had a full page to go on from.
func (s *Server) handleListStarred(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := userCaller(p, "stars"); err != nil {
		return err
	}
	page, err := pageParams(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	stars, err := s.store.StarsPage(ctx, p.UserID, page)
	if err != nil {
		return storeErr(err)
	}
	ids := make([]string, len(stars))
	for i, st := range stars {
		ids[i] = st.NodeID
	}
	reach, err := reachRoles(ctx, s.store, p, ids)
	if err != nil {
		return err
	}
	nodes, err := s.store.NodesByID(ctx, p.AccountID, ids)
	if err != nil {
		return err
	}
	out := listResponse[starredItem]{Items: []starredItem{}}
	for _, st := range stars {
		n, role := nodes[st.NodeID], reach[st.NodeID]
		if n == nil || role == "" {
			continue
		}
		j := toNodeJSON(n)
		j.Role = role
		starred := true
		j.Starred = &starred
		out.Items = append(out.Items, starredItem{nodeJSON: j, StarredAt: st.CreatedAt})
	}
	ptrs := make([]*nodeJSON, len(out.Items))
	for i := range out.Items {
		ptrs[i] = &out.Items[i].nodeJSON
	}
	if err := s.withTargets(ctx, p, ptrs...); err != nil {
		return err
	}
	if len(stars) == page.Limit {
		out.NextCursor = store.StarCursor(stars[len(stars)-1])
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}
