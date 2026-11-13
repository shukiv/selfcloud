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
// Truncated says the list is the 64 nearest and the caller reaches a folder above them (decision 140).
type ancestorsJSON struct {
	Ancestors []nodeJSON `json:"ancestors"`
	Truncated bool       `json:"truncated"`
}

// reachedNode is an ancestor the caller reaches, with the role the caller holds on it.
type reachedNode struct {
	node *store.Node
	role string
}

// reachableAncestors walks up parent_id from n, nearest first, and returns each ancestor the caller reaches. Each step
// is nodeAccessWith, the same access GET /nodes/{nod} makes, so a share link, a grant, a token's scope and the trash
// all apply. The walk stops at the first node the caller cannot reach (not_found): nothing above it is read or returned.
// Any other error is the answer, and no partial chain comes with it. rd is the store, or a stand-in in a test.
//
// truncated is true only when the walk stopped at the cap with a parent above the last item, and the caller reaches that
// parent by the same rule as each step (decision 140). That is one read more, made only at the cap. A parent the caller
// does not reach gives the answer of a chain that ends below it, truncated false, so the flag never tells that a folder
// the caller cannot reach exists.
func (s *Server) reachableAncestors(ctx context.Context, rd accessReader, p *auth.Principal, n *store.Node) ([]reachedNode, bool, error) {
	var chain []reachedNode
	id := n.ParentID
	for id != "" && len(chain) < maxAncestors {
		a, role, err := nodeAccessWith(ctx, rd, p, id)
		if err != nil {
			if apierr.From(err).Code == apierr.NotFound {
				return chain, false, nil
			}
			return nil, false, err
		}
		chain = append(chain, reachedNode{node: a, role: role})
		id = a.ParentID
	}
	if id == "" {
		return chain, false, nil
	}
	// The cap stopped the walk below a parent. Only whether the caller reaches it is kept: the node read is not.
	if _, _, err := nodeAccessWith(ctx, rd, p, id); err != nil {
		if apierr.From(err).Code == apierr.NotFound {
			return chain, false, nil
		}
		return nil, false, err
	}
	return chain, true, nil
}

// handleListAncestors answers a node's reachable ancestors in one call, so a client opening a folder needs one round trip
// for its trail, not one per level (decision 132). The node itself must be reachable, and is refused as stat
// refuses it. Each item is the stat answer for that node, built the way stat builds its own. Read-only: no audit, no write.
func (s *Server) handleListAncestors(w http.ResponseWriter, r *http.Request) error {
	n, _, err := s.nodeAccess(r, r.PathValue("nod"))
	if err != nil {
		return err
	}
	ctx := r.Context()
	p := principalOf(r)
	chain, truncated, err := s.reachableAncestors(ctx, s.store, p, n)
	if err != nil {
		return err
	}
	out := ancestorsJSON{Ancestors: make([]nodeJSON, 0, len(chain)), Truncated: truncated}
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
