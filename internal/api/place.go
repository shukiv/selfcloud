// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"

	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// placeNames returns the display name of each parent in parentIDs, keyed by ID, for the parents the caller reaches. A
// parent is left out when the caller does not reach it, so the answer never names a folder above what the person
// reaches: no role on it (a trashed parent included, since the walk refuses the trash), or a parent outside a token's
// folders or node scope. It is left out too when it is a folder's root, since the top level names nothing, and when
// it does not exist. The caller's reach is read for all the parents in one query (reachRowsIn, which applies the rules
// of nodeAccessWith, as reachRole does), and their names in one more (store.NodesIn). Both run on q, so a list reads
// its places in the snapshot its items come from. The callers here are users, account keys and tokens, the principals
// their routes admit; a share-link visitor is refused before this.
func placeNames(ctx context.Context, q store.Querier, p *auth.Principal, parentIDs []string) (map[string]string, error) {
	out := map[string]string{}
	if len(parentIDs) == 0 {
		return out, nil
	}
	within := ""
	if p.Token != nil {
		within = p.Token.Node
	}
	rows, err := reachRowsIn(ctx, q, p, parentIDs, within)
	if err != nil {
		return nil, err
	}
	reached := make([]string, 0, len(rows))
	for _, row := range rows {
		if reachRole(p, row) != "" {
			reached = append(reached, row.NodeID)
		}
	}
	if len(reached) == 0 {
		return out, nil
	}
	nodes, err := store.NodesIn(ctx, q, p.AccountID, reached)
	if err != nil {
		return nil, storeErr(err)
	}
	for _, id := range reached {
		n := nodes[id]
		if n == nil || n.ParentID == "" {
			continue // a folder's root names nothing
		}
		out[id] = n.Name
	}
	return out, nil
}

// parentIDOf is a node's parent ID for placeNames, "" for a folder's root.
func parentIDOf(n *nodeJSON) string {
	if n.ParentID == nil {
		return ""
	}
	return *n.ParentID
}

// placesIn returns the place of each item, aligned with items, read on q for the caller: the name of the item's parent
// when the caller reaches it, and "" otherwise (placeNames). One query reads every parent's reach and one its names,
// however long the list.
func placesIn(ctx context.Context, q store.Querier, p *auth.Principal, items []*nodeJSON) ([]string, error) {
	parents := make([]string, len(items))
	for i, it := range items {
		parents[i] = parentIDOf(it)
	}
	byParent, err := placeNames(ctx, q, p, parents)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(items))
	for i, parent := range parents {
		out[i] = byParent[parent]
	}
	return out, nil
}

// placesOfItems is placesIn read in one snapshot of the store, for a list whose items are read before it.
func (s *Server) placesOfItems(ctx context.Context, p *auth.Principal, items []*nodeJSON) ([]string, error) {
	var out []string
	err := s.store.Read(ctx, func(q store.Querier) error {
		var err error
		out, err = placesIn(ctx, q, p, items)
		return err
	})
	return out, err
}
