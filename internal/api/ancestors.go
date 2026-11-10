// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// maxAncestors is the most ancestors one answer gives: the 64 nearest the caller reaches, as the web walk gave before.
const maxAncestors = 64

// ancestorsJSON is the answer of GET /nodes/{nod}/ancestors: the caller's reachable ancestors of the node, topmost first.
type ancestorsJSON struct {
	Ancestors []nodeJSON `json:"ancestors"`
}

// reachedNode is an ancestor the caller reaches, with the role the caller holds on it.
type reachedNode struct {
	node *store.Node
	role string
}

// reachableAncestors walks up parent_id from n, nearest first, and returns each ancestor the caller reaches. Each step
// is nodeAccessWith, the same access GET /nodes/{nod} makes, so a share link, a grant, a token's scope and the trash
// all apply. The walk stops at the first node the caller cannot reach (not_found): nothing above it is read or returned.
// Any other error is the answer.
func (s *Server) reachableAncestors(ctx context.Context, p *auth.Principal, n *store.Node) ([]reachedNode, error) {
	var chain []reachedNode
	for id := n.ParentID; id != "" && len(chain) < maxAncestors; {
		a, role, err := nodeAccessWith(ctx, s.store, p, id)
		if err != nil {
			if apierr.From(err).Code == apierr.NotFound {
				break
			}
			return nil, err
		}
		chain = append(chain, reachedNode{node: a, role: role})
		id = a.ParentID
	}
	return chain, nil
}

// handleListAncestors answers a node's reachable ancestors in one call, so a client opening a folder needs one round trip
// for its trail, not one per level (perf-1 3.2, decision 132). The node itself must be reachable, and is refused as stat
// refuses it. Each item is the stat answer for that node, built the way stat builds its own. Read-only: no audit, no write.
func (s *Server) handleListAncestors(w http.ResponseWriter, r *http.Request) error {
	n, _, err := s.nodeAccess(r, r.PathValue("nod"))
	if err != nil {
		return err
	}
	ctx := r.Context()
	p := principalOf(r)
	chain, err := s.reachableAncestors(ctx, p, n)
	if err != nil {
		return err
	}
	out := ancestorsJSON{Ancestors: make([]nodeJSON, 0, len(chain))}
	// The walk is nearest first; the answer is topmost first, the parent last.
	for i := len(chain) - 1; i >= 0; i-- {
		j := nodeJSONFor(p, chain[i].node)
		j.Role = chain[i].role
		out.Ancestors = append(out.Ancestors, j)
	}
	if len(out.Ancestors) > 0 {
		ptrs := make([]*nodeJSON, len(out.Ancestors))
		for i := range out.Ancestors {
			ptrs[i] = &out.Ancestors[i]
		}
		if err := s.addPeopleNames(ctx, p, n.AccountID, ptrs...); err != nil {
			return err
		}
		if err := s.withTargets(ctx, p, ptrs...); err != nil {
			return err
		}
		if err := s.withThumbnails(ctx, p, n.AccountID, ptrs...); err != nil {
			return err
		}
		if err := s.withEditing(ctx, p, n.AccountID, ptrs...); err != nil {
			return err
		}
		if p.Kind == auth.KindUser {
			if err := s.withStarred(r, p.UserID, ptrs); err != nil {
				return err
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}
