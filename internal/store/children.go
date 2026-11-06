// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
)

// SortKey is the column a listing of children is sorted by.
type SortKey int

const (
	SortName     SortKey = iota + 1 // the name, in names.Compare order
	SortSize                        // the size in bytes; a directory holds 0
	SortModified                    // the modification time, in milliseconds
)

// ChildSort is the order of a sorted listing: its column, and whether it runs descending. Ties on the column are
// broken by ID in the same direction, so every child has one place in the order.
type ChildSort struct {
	Key  SortKey
	Desc bool
}

// ChildPos is where a sorted listing resumes: the sort columns and ID of the last row of the previous page. Kind is
// the segment that row belongs to. Text carries the name, and Num the size or modified time.
type ChildPos struct {
	Kind string
	Text string
	Num  int64
	ID   string
}

// childSegments is the order the kinds of a listing come in, whatever the sort: folders, then shortcuts, then files.
var childSegments = []string{KindDir, KindShortcut, KindFile}

// childKeyColumn is the SQL column each sort reads. Only these three texts ever reach a query: the sort is a fixed
// set, so no caller's text is built into SQL.
func childKeyColumn(k SortKey) (string, error) {
	switch k {
	case SortName:
		return "n.name COLLATE " + NameCollation, nil
	case SortSize:
		return "n.size", nil
	case SortModified:
		return "n.modified_at", nil
	}
	return "", fmt.Errorf("store: unknown sort key %d", k)
}

// PosOf returns the position of n in the listing sorted by s, for the next page to resume after.
func (s ChildSort) PosOf(n *Node) ChildPos {
	pos := ChildPos{Kind: n.Kind, ID: n.ID}
	switch s.Key {
	case SortName:
		pos.Text = n.Name
	case SortSize:
		pos.Num = n.Size
	case SortModified:
		pos.Num = millis(n.ModifiedAt)
	}
	return pos
}

// ChildrenSorted lists up to limit children of parentID of accountID in the order s, after the position after (nil for
// the first page). more says whether a child follows the last one returned. Folders come first, then shortcuts, then
// files, each kind in the order s; the kinds are read one after another, each from its own index.
func (s *Store) ChildrenSorted(ctx context.Context, accountID, parentID string, order ChildSort, limit int, after *ChildPos) (out []*Node, more bool, err error) {
	if limit < 1 {
		return nil, false, fmt.Errorf("store: limit %d is less than 1", limit)
	}
	start := 0
	if after != nil {
		if start = segmentOf(after.Kind); start < 0 {
			return nil, false, ErrBadCursor
		}
	}
	for i := start; i < len(childSegments) && len(out) <= limit; i++ {
		var from *ChildPos
		if after != nil && i == start {
			from = after
		}
		rows, err := s.childSegment(ctx, accountID, parentID, childSegments[i], order, limit+1-len(out), from)
		if err != nil {
			return nil, false, err
		}
		out = append(out, rows...)
	}
	if len(out) > limit {
		out = out[:limit]
		more = true
	}
	return out, more, nil
}

// childSegmentSQL is the statement that reads one kind of a listing. Its placeholders are, in order: the account,
// the parent, the kind, then (after a position) the position's bounds, then the limit. childSegmentArgs gives the bounds.
func childSegmentSQL(order ChildSort, after bool) string {
	col, _ := childKeyColumn(order.Key)
	dir := "ASC"
	if order.Desc {
		dir = "DESC"
	}
	query := nodeSelect + " WHERE n.account_id = ? AND n.parent_id = ? AND n.trashed_at IS NULL AND n.kind = ?" + sameFolderAsParent
	if after {
		if order.Key == SortName {
			// The planner uses a range on a column with a collation, but not a row value that includes one. So the
			// range is on the name, and the ID settles the names that compare equal (a few per folder: names differ
			// in case and accents only), as a residual filter.
			edge, idCmp := ">=", "<="
			if order.Desc {
				edge, idCmp = "<=", ">="
			}
			query += " AND " + col + " " + edge + " ? AND NOT (" + col + " = ? AND n.id " + idCmp + " ?)"
		} else {
			cmp := ">"
			if order.Desc {
				cmp = "<"
			}
			query += " AND (" + col + ", n.id) " + cmp + " (?, ?)"
		}
	}
	return query + " ORDER BY " + col + " " + dir + ", n.id " + dir + " LIMIT ?"
}

// childSegmentArgs are the bounds of a position, in the order childSegmentSQL takes them.
func childSegmentArgs(order ChildSort, from ChildPos) []any {
	if order.Key == SortName {
		return []any{from.Text, from.Text, from.ID}
	}
	return []any{from.value(order.Key), from.ID}
}

// childSegment reads up to limit children of one kind, after from when it is set.
func (s *Store) childSegment(ctx context.Context, accountID, parentID, kind string, order ChildSort, limit int, from *ChildPos) ([]*Node, error) {
	if _, err := childKeyColumn(order.Key); err != nil {
		return nil, err
	}
	args := []any{accountID, parentID, kind}
	if from != nil {
		args = append(args, childSegmentArgs(order, *from)...)
	}
	args = append(args, limit)
	rows, err := s.r.QueryContext(ctx, childSegmentSQL(order, from != nil), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// value is the position's sort column as a query parameter.
func (p ChildPos) value(k SortKey) any {
	if k == SortName {
		return p.Text
	}
	return p.Num
}

// segmentOf is the place of a kind in childSegments, or -1 for a kind that has none.
func segmentOf(kind string) int {
	for i, k := range childSegments {
		if k == kind {
			return i
		}
	}
	return -1
}
