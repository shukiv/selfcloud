// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"strings"
	"time"
)

// ActivityRead is one read of the activity of a node (design section 14, slice r4, decision 50): the audit entries
// whose target is the node, newest first. Every filter that decides what the reader may see is in the query, so a page
// holds only entries the reader may see.
type ActivityRead struct {
	AccountID string
	NodeID    string
	// Actions are the actions to list. The caller works them out from the reader's role and the kind it asked for. An
	// empty list reads nothing.
	Actions []string
	// SeeAll skips the private-entry rule: the account key reads every entry, as AuditLog does.
	SeeAll bool
	// ViewerUserID is the user whose private entries are listed. "" for a reader that is no user.
	ViewerUserID string
	// PrivateOwner is the owner of the personal folder the node is in now, or "" when the node is in a team space. The
	// entries private to that owner are listed to every reader who reaches the node: a file shared out of a personal
	// folder shows its history to the people it was shared with. An entry private to anyone else, such as one made
	// while the node was in another person's personal folder, stays with that person (audit.go).
	PrivateOwner string
	// HideRestricted lists a label event only when its field exists and is not restricted. A reader who may read
	// restricted fields (a manager) leaves it false.
	HideRestricted bool
	// Before is the ID of the last entry of the previous page. 0 reads from the newest.
	Before int64
	Limit  int
}

// ActivityRow is one audit entry of a node, with its details as stored. The caller projects it; it holds no IP address and
// no user agent, because the query does not read them.
type ActivityRow struct {
	ID        int64
	Time      time.Time
	ActorKind string
	ActorID   string
	Action    string
	Details   map[string]any
}

// ActivityScanCeiling is the most audit rows of one node a single read looks at. A kind that matches few of a hot node's
// rows would otherwise read them all. When the ceiling is reached before the page is full, the read ends with a short page
// and the position to continue from, so a page may be short while more remain (decision 50).
const ActivityScanCeiling = 10000

// ActivityPage is one page of activity. Next is the audit ID after which the following page starts, or 0 at the end.
type ActivityPage struct {
	Rows []*ActivityRow
	Next int64
}

// activityWindowSQL finds the oldest ID among the newest entries of a node and how many it counted, from the index alone.
const activityWindowSQL = `SELECT MIN(id), COUNT(*) FROM (SELECT id FROM audit_log INDEXED BY audit_log_target
	WHERE account_id = ? AND target = ? AND id < ? ORDER BY id DESC LIMIT ?)`

// activitySQL is the statement for n actions. Every value is a bound parameter; only the count of placeholders and the
// fixed clauses are built into the text. The arguments, in order: account, node, before, the n actions, then the two
// users of the private rule when private is set, then the lowest ID of the window and the limit.
func activitySQL(n int, private, restricted bool) string {
	var b strings.Builder
	b.WriteString(`SELECT a.id, a.time, a.actor_kind, a.actor_id, a.action, a.details FROM audit_log a INDEXED BY audit_log_target
		WHERE a.account_id = ? AND a.target = ? AND a.id < ? AND a.action IN (`)
	b.WriteString(strings.TrimSuffix(strings.Repeat("?,", n), ","))
	b.WriteString(")")
	if private {
		b.WriteString(" AND (a.private_user_id IS NULL OR a.private_user_id = ? OR a.private_user_id = ?)")
	}
	if restricted {
		b.WriteString(` AND (a.action NOT IN ('node.label_set', 'node.label_clear') OR EXISTS (
			SELECT 1 FROM label_fields f WHERE f.account_id = a.account_id
			AND f.id = json_extract(a.details, '$.field') AND f.restricted = 0))`)
	}
	b.WriteString(" AND a.id >= ? ORDER BY a.id DESC LIMIT ?")
	return b.String()
}

// Activity reads one page of a node's activity, newest first. At most ceiling rows of the node are looked at (0 means
// ActivityScanCeiling), and the filters apply to them. The reader must already have been checked to reach the node: this
// is the filter on what it may see of the node's history, not the check that it may ask.
func (s *Store) Activity(ctx context.Context, q ActivityRead, ceiling int) (ActivityPage, error) {
	var out ActivityPage
	if len(q.Actions) == 0 || q.Limit < 1 {
		return out, nil
	}
	if ceiling <= 0 {
		ceiling = ActivityScanCeiling
	}
	before := q.Before
	if before <= 0 {
		before = math.MaxInt64
	}
	// The window: the newest ceiling entries of the node after the cursor, read from the index alone.
	var lowest sql.NullInt64
	var inWindow int
	if err := s.r.QueryRowContext(ctx, activityWindowSQL, q.AccountID, q.NodeID, before, ceiling).Scan(&lowest, &inWindow); err != nil {
		return out, err
	}
	if inWindow == 0 {
		return out, nil
	}
	args := []any{q.AccountID, q.NodeID, before}
	for _, a := range q.Actions {
		args = append(args, a)
	}
	if !q.SeeAll {
		args = append(args, q.ViewerUserID, q.PrivateOwner)
	}
	args = append(args, lowest.Int64, q.Limit+1)
	res, err := s.r.QueryContext(ctx, activitySQL(len(q.Actions), !q.SeeAll, q.HideRestricted), args...)
	if err != nil {
		return out, err
	}
	defer res.Close()
	for res.Next() {
		var r ActivityRow
		var t int64
		var details sql.NullString
		if err := res.Scan(&r.ID, &t, &r.ActorKind, &r.ActorID, &r.Action, &details); err != nil {
			return out, err
		}
		r.Time = fromMillis(t)
		if details.Valid {
			if err := json.Unmarshal([]byte(details.String), &r.Details); err != nil {
				return out, err
			}
		}
		out.Rows = append(out.Rows, &r)
	}
	if err := res.Err(); err != nil {
		return out, err
	}
	switch {
	case len(out.Rows) > q.Limit:
		out.Rows = out.Rows[:q.Limit]
		out.Next = out.Rows[q.Limit-1].ID
	case inWindow == ceiling:
		// The window ended before the node's history did: continue below its oldest entry.
		out.Next = lowest.Int64
	}
	return out, nil
}
