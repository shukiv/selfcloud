// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// reachRoles is the batched form of nodeAccessWith for a user principal: it returns the role the caller holds on
// each node of nodeIDs, and leaves out every node the caller does not reach. One store query reads the facts for the
// whole page (store.ReachRows); reachRole then applies the same rules nodeRoleIn applies node by node. The property
// test in reach_test.go keeps the two in step.
func reachRoles(ctx context.Context, st *store.Store, p *auth.Principal, nodeIDs []string) (map[string]string, error) {
	if len(nodeIDs) == 0 {
		return map[string]string{}, nil
	}
	if p == nil || p.Kind != auth.KindUser {
		return nil, apierr.New(apierr.Forbidden, "only a user has this view")
	}
	within := ""
	if p.Token != nil {
		within = p.Token.Node
	}
	var out map[string]string
	// The user and the facts are read in one snapshot, as the search's candidates are (store.Read).
	err := st.Read(ctx, func(q store.Querier) error {
		var err error
		out, err = reachRolesIn(ctx, q, p, nodeIDs, within)
		return err
	})
	return out, err
}

// reachRolesIn is reachRoles on the querier of one snapshot. within is the node the search or the token is confined
// to, or "" for none (store.ReachRowsIn). A caller of a search may pass a narrower node than its token's.
func reachRolesIn(ctx context.Context, q store.Querier, p *auth.Principal, nodeIDs []string, within string) (map[string]string, error) {
	out := map[string]string{}
	rows, err := reachRowsIn(ctx, q, p, nodeIDs, within)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if role := reachRole(p, row); role != "" {
			out[row.NodeID] = role
		}
	}
	return out, nil
}

// reachRowsIn reads the reach facts of the user principal p for nodeIDs on q: the user must be active, and each node
// of the account has at most one row. The caller applies reachRole to each row, and may apply more (a search's scope).
func reachRowsIn(ctx context.Context, q store.Querier, p *auth.Principal, nodeIDs []string, within string) ([]store.ReachRow, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(nodeIDs))
	seen := make(map[string]bool, len(nodeIDs))
	for _, id := range nodeIDs {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	// An account key has no user. It reaches every node of its account, and reachRole makes it the manager of each node
	// that is not in the trash; the rows still say which nodes are trashed or lie below the scope.
	if p.Kind == auth.KindAccountKey {
		return store.ReachRowsIn(ctx, q, p.AccountID, "", "", ids, within)
	}
	u, err := store.UserIn(ctx, q, p.AccountID, p.UserID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, apierr.New(apierr.Unauthenticated, "the session has ended; sign in again")
	}
	if err != nil {
		return nil, err
	}
	if err := checkUserStatus(u); err != nil {
		return nil, err
	}
	return store.ReachRowsIn(ctx, q, p.AccountID, p.UserID, u.Role, ids, within)
}

// reachRole is the role a user principal holds on one node, from the facts of its ReachRows row, or "" when the
// caller does not reach it. It mirrors nodeRoleIn for a user: the token's folder list and node scope, the trash
// refusal, the implicit rule, the highest of the permission rank, and the token's role cap.
func reachRole(p *auth.Principal, row store.ReachRow) string {
	if p.Token != nil {
		if !p.Token.Reaches(row.FolderID) {
			return ""
		}
		if p.Token.Node != "" && !row.Within {
			return ""
		}
	}
	if row.Trashed {
		return ""
	}
	role := ""
	if row.Implicit || p.Kind == auth.KindAccountKey {
		role = store.RoleManager
	}
	if store.RoleRank(role) < row.Rank {
		role = store.RoleOfRank(row.Rank)
	}
	if p.Token != nil && store.RoleRank(role) > store.RoleRank(tokenRoleCap(p.Token)) {
		role = tokenRoleCap(p.Token)
	}
	if store.RoleRank(role) == 0 {
		return ""
	}
	return role
}
