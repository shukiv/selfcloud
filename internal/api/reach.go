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
	out := map[string]string{}
	if len(nodeIDs) == 0 {
		return out, nil
	}
	if p == nil || p.Kind != auth.KindUser {
		return nil, apierr.New(apierr.Forbidden, "only a user has this view")
	}
	u, err := st.User(ctx, p.AccountID, p.UserID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, apierr.New(apierr.Unauthenticated, "the session has ended; sign in again")
	}
	if err != nil {
		return nil, err
	}
	if err := checkUserStatus(u); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(nodeIDs))
	seen := make(map[string]bool, len(nodeIDs))
	for _, id := range nodeIDs {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	within := ""
	if p.Token != nil {
		within = p.Token.Node
	}
	rows, err := st.ReachRows(ctx, p.AccountID, p.UserID, u.Role, ids, within)
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
	if row.Implicit {
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
