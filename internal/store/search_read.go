// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"strings"
)

// The reads a search makes on the snapshot Store.Read gives it. Each takes the querier it is given, so the search's
// candidates, its access facts and the nodes it returns come from one state of the database.

// UserIn returns a user of accountID, read on q.
func UserIn(ctx context.Context, q Querier, accountID, id string) (*User, error) {
	return userIn(ctx, q, accountID, "id = ?", id)
}

// LabelFieldIn returns a label field of accountID, read on q. ErrNotFound if there is none.
func LabelFieldIn(ctx context.Context, q Querier, accountID, id string) (*LabelField, error) {
	return labelFieldIn(ctx, q, accountID, "id = ?", id)
}

// NodesIn returns the nodes of accountID with the given IDs, keyed by ID. An ID that is not a node of the account has
// no entry.
func NodesIn(ctx context.Context, q Querier, accountID string, ids []string) (map[string]*Node, error) {
	out := make(map[string]*Node, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	marks := make([]string, len(ids))
	args := make([]any, 0, len(ids)+1)
	args = append(args, accountID)
	for i, id := range ids {
		marks[i] = "?"
		args = append(args, id)
	}
	rows, err := q.QueryContext(ctx, nodeSelect+" WHERE n.account_id = ? AND n.id IN ("+strings.Join(marks, ", ")+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out[n.ID] = n
	}
	return out, rows.Err()
}
