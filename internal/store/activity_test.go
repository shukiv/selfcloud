// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// activityFixture is a store with two accounts and a few audit entries about one node of the first. Every test reads the
// entries back through Activity, so each filter of the query is tested by what it keeps out.
type activityFixture struct {
	s   *Store
	ctx context.Context
	at  time.Time
	// ceiling is the scan ceiling of the reads, 0 for the default.
	ceiling int
}

func newActivityFixture(t *testing.T) *activityFixture {
	t.Helper()
	fx := &activityFixture{s: openTest(t), ctx: context.Background(), at: time.Unix(1_700_000_000, 0)}
	for _, id := range []string{"acc_a", "acc_b"} {
		if err := fx.s.Write(fx.ctx, func(tx *Tx) error {
			return tx.InsertAccount(fx.ctx, &Account{ID: id, Name: id, Status: AccountActive, CreatedAt: fx.at, UpdatedAt: fx.at})
		}); err != nil {
			t.Fatal(err)
		}
	}
	return fx
}

// add writes one entry and returns its ID.
func (fx *activityFixture) add(t *testing.T, e AuditEntry) int64 {
	t.Helper()
	if e.Time.IsZero() {
		e.Time = fx.at
	}
	if e.ActorKind == "" {
		e.ActorKind, e.ActorID = "user", "usr_x"
	}
	if err := fx.s.Write(fx.ctx, func(tx *Tx) error { return tx.InsertAudit(fx.ctx, &e) }); err != nil {
		t.Fatal(err)
	}
	return e.ID
}

// read is a read of node nod_1 of account acc_a for the actions given, as a reader that sees only what is not private.
func (fx *activityFixture) read(t *testing.T, mod func(*ActivityRead), actions ...string) ([]int64, bool) {
	t.Helper()
	q := ActivityRead{AccountID: "acc_a", NodeID: "nod_1", Actions: actions, ViewerUserID: "usr_v", Limit: 100}
	if mod != nil {
		mod(&q)
	}
	page, err := fx.s.Activity(fx.ctx, q, fx.ceiling)
	rows, more := page.Rows, page.Next != 0
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	return ids, more
}

func (fx *activityFixture) field(t *testing.T, id string, restricted bool) {
	t.Helper()
	if err := fx.s.Write(fx.ctx, func(tx *Tx) error {
		return tx.InsertLabelField(fx.ctx, &LabelField{ID: id, AccountID: "acc_a", Name: id, Key: id, Type: LabelTypeText,
			Restricted: restricted, CreatedBy: "usr_x", CreatedAt: fx.at, UpdatedBy: "usr_x", UpdatedAt: fx.at})
	}); err != nil {
		t.Fatal(err)
	}
}

// TestActivityListsOnlyTheNodeAccountAndActions: an entry about another node, one of another account with the same
// target, an installation-wide one, and an action that is not asked for are all left out.
func TestActivityListsOnlyTheNodeAccountAndActions(t *testing.T) {
	fx := newActivityFixture(t)
	want := fx.add(t, AuditEntry{AccountID: "acc_a", Action: "node.write", Target: "nod_1"})
	fx.add(t, AuditEntry{AccountID: "acc_a", Action: "node.write", Target: "nod_2"})
	fx.add(t, AuditEntry{AccountID: "acc_b", Action: "node.write", Target: "nod_1"}) // another tenant's node, same target
	fx.add(t, AuditEntry{Action: "node.write", Target: "nod_1"})                     // installation-wide
	fx.add(t, AuditEntry{AccountID: "acc_a", Action: "permission.set", Target: "nod_1"})
	got, more := fx.read(t, nil, "node.write")
	if !slices.Equal(got, []int64{want}) || more {
		t.Fatalf("listed %v (more %v), want only %d", got, more, want)
	}
	if got, _ := fx.read(t, nil); len(got) != 0 {
		t.Fatalf("no actions listed %v, want nothing", got)
	}
}

// TestActivityIgnoresDeletionRecords: the record of an account's deletion lives at installation level with the account's
// name as its target (decision 48). It has no account, so no node's read reaches it, even when a name equals a node ID.
func TestActivityIgnoresDeletionRecords(t *testing.T) {
	fx := newActivityFixture(t)
	fx.add(t, AuditEntry{Action: "account.deleted", Target: "nod_1", ActorKind: "system", ActorID: "job"})
	fx.add(t, AuditEntry{Action: "account.delete", Target: "nod_1"})
	for _, actions := range [][]string{{"account.deleted", "account.delete"}, {"node.write"}} {
		if got, _ := fx.read(t, nil, actions...); len(got) != 0 {
			t.Fatalf("a read with %v listed the deletion records %v", actions, got)
		}
	}
}

// TestActivityPrivateRule is the rule of audit.go for a node: an entry private to a user is listed to that user, to the
// reader of a node that is in that user's personal folder now, and to the account key. Any other reader does not see it.
func TestActivityPrivateRule(t *testing.T) {
	fx := newActivityFixture(t)
	open := fx.add(t, AuditEntry{AccountID: "acc_a", Action: "node.write", Target: "nod_1"})
	mine := fx.add(t, AuditEntry{AccountID: "acc_a", Action: "node.write", Target: "nod_1", PrivateUserID: "usr_v"})
	owners := fx.add(t, AuditEntry{AccountID: "acc_a", Action: "node.write", Target: "nod_1", PrivateUserID: "usr_o"})
	other := fx.add(t, AuditEntry{AccountID: "acc_a", Action: "node.write", Target: "nod_1", PrivateUserID: "usr_z"})
	cases := []struct {
		name string
		mod  func(*ActivityRead)
		want []int64
	}{
		{"a reader sees open entries and its own", nil, []int64{mine, open}},
		{"a node in a personal folder shows the owner's entries to whoever reaches it, and a third person's stay hidden",
			func(q *ActivityRead) { q.PrivateOwner = "usr_o" }, []int64{owners, mine, open}},
		{"the account key sees all", func(q *ActivityRead) { q.SeeAll = true }, []int64{other, owners, mine, open}},
		{"a reader that is no user sees the open ones", func(q *ActivityRead) { q.ViewerUserID = "" }, []int64{open}},
	}
	for _, c := range cases {
		got, _ := fx.read(t, c.mod, "node.write")
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: listed %v, want %v", c.name, got, c.want)
		}
	}
}

// TestActivityRestrictedLabelRule: with HideRestricted, a label event is listed only when its field exists and is not
// restricted. A field that was deleted, a restricted one, and an entry with no field are all hidden. Without it all are
// listed. Other actions are not touched.
func TestActivityRestrictedLabelRule(t *testing.T) {
	fx := newActivityFixture(t)
	fx.field(t, "lbf_open", false)
	fx.field(t, "lbf_secret", true)
	open := fx.add(t, AuditEntry{AccountID: "acc_a", Action: "node.label_set", Target: "nod_1", Details: map[string]any{"field": "lbf_open"}})
	secret := fx.add(t, AuditEntry{AccountID: "acc_a", Action: "node.label_set", Target: "nod_1", Details: map[string]any{"field": "lbf_secret"}})
	gone := fx.add(t, AuditEntry{AccountID: "acc_a", Action: "node.label_clear", Target: "nod_1", Details: map[string]any{"field": "lbf_deleted"}})
	bare := fx.add(t, AuditEntry{AccountID: "acc_a", Action: "node.label_clear", Target: "nod_1"})
	write := fx.add(t, AuditEntry{AccountID: "acc_a", Action: "node.write", Target: "nod_1"})
	acts := []string{"node.label_set", "node.label_clear", "node.write"}
	got, _ := fx.read(t, func(q *ActivityRead) { q.HideRestricted = true }, acts...)
	if !slices.Equal(got, []int64{write, open}) {
		t.Errorf("a reader who may not read restricted fields lists %v, want %v", got, []int64{write, open})
	}
	got, _ = fx.read(t, nil, acts...)
	if !slices.Equal(got, []int64{write, bare, gone, secret, open}) {
		t.Errorf("a manager lists %v", got)
	}
}

// TestActivityPagesAreExactAcrossFilters: pages cut by the query hold exactly Limit visible entries, and the entries that
// a filter removes do not shorten a page. The cursor continues without a repeat or a gap.
func TestActivityPagesAreExactAcrossFilters(t *testing.T) {
	fx := newActivityFixture(t)
	var visible []int64
	for i := 0; i < 7; i++ {
		visible = append(visible, fx.add(t, AuditEntry{AccountID: "acc_a", Action: "node.write", Target: "nod_1"}))
		// Two hidden entries after each visible one.
		fx.add(t, AuditEntry{AccountID: "acc_a", Action: "node.write", Target: "nod_1", PrivateUserID: "usr_z"})
		fx.add(t, AuditEntry{AccountID: "acc_a", Action: "permission.set", Target: "nod_1"})
	}
	slices.Reverse(visible)
	var got []int64
	var before int64
	for page := 0; page < 10; page++ {
		ids, more := fx.read(t, func(q *ActivityRead) { q.Limit = 3; q.Before = before }, "node.write")
		if more && len(ids) != 3 {
			t.Fatalf("page %d holds %d entries and says there is more, want 3", page, len(ids))
		}
		got = append(got, ids...)
		if !more {
			break
		}
		before = ids[len(ids)-1]
	}
	if !slices.Equal(got, visible) {
		t.Fatalf("pages gave %v, want %v", got, visible)
	}
}

// populate fills audit_log with rows of two accounts over many nodes, so that audit_log_account and audit_log_time have
// real statistics to compete with audit_log_target, and runs ANALYZE.
func (fx *activityFixture) populate(t *testing.T, rows int) {
	t.Helper()
	if _, err := fx.s.w.ExecContext(fx.ctx, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?)
		INSERT INTO audit_log (time, account_id, actor_kind, actor_id, action, target, outcome)
		SELECT 1700000000000 + i, CASE WHEN i % 2 = 0 THEN 'acc_a' ELSE 'acc_b' END, 'user', 'usr_' || (i % 20),
		       CASE WHEN i % 7 = 0 THEN 'node.rename' ELSE 'node.write' END, 'nod_' || (i % 400), 'ok' FROM n`, rows); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.s.w.ExecContext(fx.ctx, `ANALYZE`); err != nil {
		t.Fatal(err)
	}
}

// planOf returns the query plan of a statement as one line.
func (fx *activityFixture) planOf(t *testing.T, sql string, args ...any) string {
	t.Helper()
	rows, err := fx.s.r.QueryContext(fx.ctx, "EXPLAIN QUERY PLAN "+sql, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	return strings.Join(plan, " | ")
}

// TestActivityUsesTheTargetIndex reads the query plan of the window statement and of every shape of the main statement on
// tables that hold rows and have been analysed, with audit_log_account and audit_log_time populated as competitors. A
// scan of audit_log, or a sort of its rows, would make a node's history cost the size of the account's whole log. A plan
// read on an empty database proves nothing: SQLite would not have a second index to prefer.
func TestActivityUsesTheTargetIndex(t *testing.T) {
	fx := newActivityFixture(t)
	fx.populate(t, 60000)
	check := func(name, plan string) {
		t.Helper()
		if !strings.Contains(plan, "INDEX audit_log_target") {
			t.Errorf("%s: the plan does not use audit_log_target: %s", name, plan)
		}
		if strings.Contains(plan, "SCAN a") || strings.Contains(plan, "SCAN audit_log") || strings.Contains(plan, "TEMP B-TREE") {
			t.Errorf("%s: the plan scans or sorts: %s", name, plan)
		}
		t.Logf("%s: %s", name, plan)
	}
	check("window", fx.planOf(t, activityWindowSQL, "acc_a", "nod_1", int64(1<<62), ActivityScanCeiling))
	for _, private := range []bool{false, true} {
		for _, restricted := range []bool{false, true} {
			args := []any{"acc_a", "nod_1", int64(1 << 62), "node.write", "node.rename", "node.create"}
			if private {
				args = append(args, "u1", "u2")
			}
			args = append(args, int64(5), 51)
			check(fmt.Sprintf("private=%v restricted=%v", private, restricted), fx.planOf(t, activitySQL(3, private, restricted), args...))
		}
	}
}

// TestActivityScanCeiling: a read looks at no more than the ceiling of the node's newest entries. When the ceiling ends
// before the page is full the page is short and carries the position to go on from, and following those positions lists
// every matching entry once, newest first.
func TestActivityScanCeiling(t *testing.T) {
	fx := newActivityFixture(t)
	var rare []int64
	for i := 0; i < 100; i++ {
		action := "node.write"
		if i%25 == 0 {
			action = "permission.set"
		}
		id := fx.add(t, AuditEntry{AccountID: "acc_a", Action: action, Target: "nod_1"})
		if action == "permission.set" {
			rare = append([]int64{id}, rare...)
		}
	}
	fx.ceiling = 10
	if first, more := fx.read(t, nil, "permission.set"); len(first) != 0 || !more {
		t.Fatalf("the newest 10 entries hold no permission.set: the read listed %v (more %v), want an empty page with a position", first, more)
	}
	var got []int64
	var before int64
	pages := 0
	for ; pages < 50; pages++ {
		page, err := fx.s.Activity(fx.ctx, ActivityRead{AccountID: "acc_a", NodeID: "nod_1", Actions: []string{"permission.set"},
			SeeAll: true, Before: before, Limit: 100}, 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range page.Rows {
			got = append(got, r.ID)
		}
		if page.Next == 0 {
			break
		}
		before = page.Next
	}
	if !slices.Equal(got, rare) {
		t.Fatalf("following the positions listed %v, want %v", got, rare)
	}
	if pages < 9 {
		t.Fatalf("100 entries at a ceiling of 10 took %d pages, want about 10", pages+1)
	}
}
