// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// auditFixture seeds five audit entries, oldest first, with fixed times: two in account a1, one in account a2, and two
// installation-wide ones. The actor and target of each are set, so the filters can be told apart.
type auditFixture struct {
	*recoveryFixture
	a1, a2 string
	ann    string // a user's ID, the actor of one entry and the target of another
	benID  string // a user's ID, the target of one entry
}

func newAuditFixture(t *testing.T) *auditFixture {
	t.Helper()
	f := newRecoveryFixture(t)
	a1 := f.seedAccount("Acme Ltd").ID
	a2 := f.seedAccount("Beta").ID
	ann := f.seedUser(a1, "ann@acme.example", store.RoleOwner, store.UserActive, "")
	ben := f.seedUser(a1, "ben@acme.example", store.RoleMember, store.UserActive, "")
	now := store.Now()
	entries := []*store.AuditEntry{
		{Time: now.Add(-5 * time.Hour), AccountID: a1, ActorKind: "user", ActorID: ann.ID, Action: "file.create",
			Target: "nod_1", Details: map[string]any{"name": "a.txt"}},
		{Time: now.Add(-4 * time.Hour), AccountID: a2, ActorKind: "account_key", ActorID: "key_1", Action: "account.update",
			Target: a2, Details: map[string]any{"quota_bytes": 0}},
		{Time: now.Add(-90 * time.Minute), AccountID: a1, ActorKind: "system", ActorID: "cli", Action: "user.disable",
			Target: ben.ID, Details: map[string]any{"reason": "left"}},
		{Time: now.Add(-30 * time.Minute), AccountID: "", ActorKind: "admin_key", ActorID: "key_0", Action: "admin_key.create",
			Target: "key_2", Details: map[string]any{"name": "ops"}},
		{Time: now.Add(-10 * time.Minute), AccountID: a1, ActorKind: "user", ActorID: ben.ID, Action: "user.password_change",
			Target: ben.ID},
	}
	for _, e := range entries {
		e := e
		if err := f.st.Write(f.ctx, func(tx *store.Tx) error { return tx.InsertAudit(f.ctx, e) }); err != nil {
			t.Fatal(err)
		}
	}
	return &auditFixture{recoveryFixture: f, a1: a1, a2: a2, ann: ann.ID, benID: ben.ID}
}

// auditLines runs `admin audit` with args and returns its exit code, its header and its rows, split into cells.
func auditLines(t *testing.T, args ...string) (int, []string, [][]string, string) {
	t.Helper()
	code, out, errOut := runCmd(t, append([]string{"admin", "audit"}, args...)...)
	if out == "" {
		return code, nil, nil, errOut
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	header := strings.Split(lines[0], "\t")
	var rows [][]string
	for _, l := range lines[1:] {
		rows = append(rows, strings.Split(l, "\t"))
	}
	return code, header, rows, errOut
}

// actions returns the action column of each row, in order.
func actions(rows [][]string) string {
	var out []string
	for _, r := range rows {
		out = append(out, r[3])
	}
	return strings.Join(out, ",")
}

func TestAdminAuditPrintsNewestFirst(t *testing.T) {
	newAuditFixture(t)
	code, header, rows, errOut := auditLines(t)
	if code != 0 {
		t.Fatalf("admin audit = %d, stderr %q", code, errOut)
	}
	if strings.Join(header, "\t") != "time\taccount\tactor\taction\ttarget\tdetails" {
		t.Fatalf("header = %v", header)
	}
	want := "user.password_change,admin_key.create,user.disable,account.update,file.create"
	if got := actions(rows); got != want {
		t.Errorf("actions = %s, want %s (newest first)", got, want)
	}
	// The row of an installation-wide entry shows a dash for the account; the details are key=value.
	if rows[1][1] != "-" {
		t.Errorf("installation-wide account = %q, want -", rows[1][1])
	}
	if _, err := time.Parse(time.RFC3339, rows[0][0]); err != nil || !strings.HasSuffix(rows[0][0], "Z") {
		t.Errorf("time %q is not RFC 3339 UTC", rows[0][0])
	}
	if rows[2][5] != "reason=left" {
		t.Errorf("details = %q, want reason=left", rows[2][5])
	}
	if rows[4][5] != "name=a.txt" {
		t.Errorf("details = %q, want name=a.txt", rows[4][5])
	}
}

func TestAdminAuditFilters(t *testing.T) {
	f := newAuditFixture(t)
	cases := []struct {
		name string
		args []string
		want string // actions, newest first
	}{
		{"account", []string{"-account", f.a1}, "user.password_change,user.disable,file.create"},
		{"account with no entries", []string{"-account", id.New("acc")}, ""},
		{"user as an actor or a target", []string{"-user", f.benID}, "user.password_change,user.disable"},
		{"user by email", []string{"-user", "ann@acme.example"}, "file.create"},
		{"action prefix", []string{"-action", "user."}, "user.password_change,user.disable"},
		{"action prefix is literal", []string{"-action", "user%"}, ""},
		{"since a duration", []string{"-since", "1h"}, "user.password_change,admin_key.create"},
		{"since a time", []string{"-since", store.Now().Add(-100 * time.Minute).UTC().Format(time.RFC3339)},
			"user.password_change,admin_key.create,user.disable"},
		{"limit", []string{"-limit", "2"}, "user.password_change,admin_key.create"},
		{"filters combine", []string{"-account", f.a1, "-action", "user.", "-limit", "1"}, "user.password_change"},
		{"nothing matches", []string{"-action", "zzz"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, _, rows, errOut := auditLines(t, tc.args...)
			if code != 0 {
				t.Fatalf("exit = %d, stderr %q", code, errOut)
			}
			if got := actions(rows); got != tc.want {
				t.Errorf("actions = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAdminAuditEmptyResultPrintsTheHeaderOnly(t *testing.T) {
	newAuditFixture(t)
	code, out, _ := runCmd(t, "admin", "audit", "-action", "zzz")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if strings.Count(out, "\n") != 1 || !strings.HasPrefix(out, "time\taccount\t") {
		t.Errorf("output = %q, want the header line only", out)
	}
}

func TestAdminAuditRefusesBadInput(t *testing.T) {
	newAuditFixture(t)
	bad := [][]string{
		{"-account", "usr_0000000000000000000000000"},
		{"-account", "acc"},
		{"-user", "nobody"},
		{"-user", "usr"},
		{"-action", "bad\tprefix"},
		{"-action", strings.Repeat("a", 200)},
		{"-since", "yesterday"},
		{"-since", "-1h"},
		{"-since", "0s"},
		{"-since", "2026-13-40T00:00:00Z"},
		{"-limit", "0"},
		{"-limit", "10001"},
		{"-limit", "many"},
		{"stray"},
	}
	for _, args := range bad {
		if code, _, _ := runCmd(t, append([]string{"admin", "audit"}, args...)...); code != 2 {
			t.Errorf("admin audit %q exit = %d, want 2", args, code)
		}
	}
}

func TestAdminAuditLimitBounds(t *testing.T) {
	newAuditFixture(t)
	for _, n := range []string{"1", "10000"} {
		if code, _, _ := runCmd(t, "admin", "audit", "-limit", n); code != 0 {
			t.Errorf("-limit %s exit = %d, want 0", n, code)
		}
	}
	code, _, rows, _ := auditLines(t)
	if code != 0 || len(rows) != 5 {
		t.Errorf("the default limit shows %d rows, want all 5 of this fixture", len(rows))
	}
}

// TestAdminAuditPrintsWhatIsStored checks that the command prints the stored details and nothing more, and that it
// writes no audit row of its own.
func TestAdminAuditPrintsWhatIsStored(t *testing.T) {
	f := newAuditFixture(t)
	before := len(f.auditRows(f.a1))
	_, _, rows, _ := auditLines(t, "-action", "file.")
	if len(rows) != 1 || rows[0][5] != "name=a.txt" {
		t.Fatalf("rows = %v, want the one file.create with name=a.txt", rows)
	}
	if after := len(f.auditRows(f.a1)); after != before {
		t.Errorf("audit rows went from %d to %d; admin audit is read-only", before, after)
	}
}
