// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"strings"
)

// ReachRow is what one node of a ReachRows page says about a user, read in one query. It holds the facts the
// access rules combine (see api reachRoles); it does not decide access by itself.
type ReachRow struct {
	NodeID   string
	FolderID string
	// Implicit is set when the user holds manager on the node's folder by rule: an owner on a team space, or the
	// user of a personal folder.
	Implicit bool
	// Rank is the highest rank the user holds on the node through permissions (direct or through a group, live
	// only) and organisation links it has opened, over the node and its ancestors. 0 for none.
	Rank int
	// Trashed is set when the node or one of its ancestors is in the trash.
	Trashed bool
	// Within is set when the node is, or lies below, the node named in ReachRows. It is false when none was named.
	Within bool
}

// ReachRows reads the access facts of a user for a page of nodes in one recursive query, so a listing does not
// walk each node's ancestry on its own. It is the batched form of the walk access.go makes per node: the same
// permission, group, link and policy conditions (subjectPredicate, liveClause and linkLiveSQL), the same implicit
// rule, and the same trash test over the node and its ancestors. A node of another account, or one that does not
// exist, has no row. userRole is the user's role in the account ("owner" or "member"), which the implicit rule
// reads. within names a node the caller's token is confined to, or "" for none.
func (s *Store) ReachRows(ctx context.Context, accountID, userID, userRole string, nodeIDs []string,
	within string) ([]ReachRow, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	marks := make([]string, len(nodeIDs))
	// The arguments follow the placeholders in the query text, in order of appearance.
	args := make([]any, 0, len(nodeIDs)+16)
	for i, id := range nodeIDs {
		marks[i] = "(?)"
		args = append(args, id)
	}
	now := nowMillis()
	args = append(args, accountID, accountID) // the seed walk, then the recursive step
	args = append(args, within)               // the token's node scope
	args = append(args, accountID)            // permissions
	args = append(args, userID, userID, now)  // subject and liveness
	args = append(args, accountID, userID, now)
	args = append(args, userID, userRole) // implicit rule
	args = append(args, accountID)        // the final node and folder join

	query := `WITH RECURSIVE seed(id) AS (VALUES ` + strings.Join(marks, ", ") + `),
		chain(origin, nid, parent, depth) AS (
			SELECT n.id, n.id, n.parent_id, 0 FROM nodes n JOIN seed s ON s.id = n.id WHERE n.account_id = ?
			UNION ALL
			SELECT c.origin, p.id, p.parent_id, c.depth + 1 FROM nodes p JOIN chain c ON p.id = c.parent
			WHERE p.account_id = ?
		),
		scope AS (
			SELECT c.origin AS origin, MAX(CASE WHEN c.nid = ? THEN 1 ELSE 0 END) AS within FROM chain c GROUP BY c.origin
		),
		trash AS (
			SELECT c.origin AS origin, MAX(CASE WHEN tn.trashed_at IS NOT NULL THEN 1 ELSE 0 END) AS trashed
			FROM chain c JOIN nodes tn ON tn.id = c.nid GROUP BY c.origin
		),
		grants AS (
			SELECT c.origin AS origin, pm.role AS role FROM chain c
			JOIN permissions pm ON pm.node_id = c.nid AND pm.account_id = ?
			WHERE ` + subjectPredicate + ` AND ` + liveClause + `
			UNION ALL
			SELECT c.origin AS origin, sl.role AS role FROM chain c
			JOIN share_links sl ON sl.node_id = c.nid AND sl.account_id = ?
			JOIN share_link_visits v ON v.link_id = sl.id AND v.user_id = ?
			JOIN nodes ln ON ln.id = sl.node_id JOIN folders lf ON lf.id = ln.folder_id
			WHERE ` + linkLiveSQL + `
		),
		ranks AS (
			SELECT origin, MAX(` + fmt.Sprintf(roleRankSQL, "role") + `) AS rank FROM grants GROUP BY origin
		)
		SELECT n.id, n.folder_id,
			CASE WHEN (f.kind = 'personal' AND f.owner_user_id = ?) OR (f.kind = 'team' AND ? = 'owner')
				THEN 1 ELSE 0 END,
			COALESCE(ranks.rank, 0), COALESCE(trash.trashed, 0), COALESCE(scope.within, 0)
		FROM seed s
		JOIN nodes n ON n.id = s.id AND n.account_id = ?
		JOIN folders f ON f.id = n.folder_id
		LEFT JOIN ranks ON ranks.origin = n.id
		LEFT JOIN trash ON trash.origin = n.id
		LEFT JOIN scope ON scope.origin = n.id`
	rows, err := s.r.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReachRow
	for rows.Next() {
		var r ReachRow
		var implicit int
		var trashed, scoped int
		if err := rows.Scan(&r.NodeID, &r.FolderID, &implicit, &r.Rank, &trashed, &scoped); err != nil {
			return nil, err
		}
		r.Implicit, r.Trashed, r.Within = implicit == 1, trashed == 1, scoped == 1
		out = append(out, r)
	}
	return out, rows.Err()
}
