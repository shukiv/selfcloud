// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"slices"
	"testing"
	"time"
)

// TestAuditLogFilteredByTime: Since includes its instant, Until excludes its own, and paging keeps the
// window the first page opened with.
func TestAuditLogFilteredByTime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTest(t)
	at := func(second int) time.Time {
		return time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(second) * time.Second)
	}
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.InsertAccount(ctx, &Account{ID: "acc_a", Name: "A", Status: AccountActive,
			CreatedAt: at(0), UpdatedAt: at(0)})
	}); err != nil {
		t.Fatal(err)
	}
	for i, action := range []string{"probe.0", "probe.1", "probe.2"} {
		e := &AuditEntry{Time: at(100 + i), AccountID: "acc_a", ActorKind: "user", ActorID: "usr_a",
			Action: action, Target: "nod_1"}
		if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertAudit(ctx, e) }); err != nil {
			t.Fatal(err)
		}
	}
	actions := func(entries []*AuditEntry) []string {
		out := make([]string, 0, len(entries))
		for _, e := range entries {
			out = append(out, e.Action)
		}
		return out
	}
	for _, tt := range []struct {
		name string
		f    AuditFilter
		want []string
	}{
		{"no bounds", AuditFilter{}, []string{"probe.2", "probe.1", "probe.0"}},
		{"since includes its instant", AuditFilter{Since: at(101)}, []string{"probe.2", "probe.1"}},
		{"until leaves its own instant out", AuditFilter{Until: at(102)}, []string{"probe.1", "probe.0"}},
		{"both bounds", AuditFilter{Since: at(101), Until: at(102)}, []string{"probe.1"}},
		{"empty window", AuditFilter{Since: at(102), Until: at(102)}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, next, err := s.AuditLogFiltered(ctx, "acc_a", "", true, tt.f, Page{Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			if next != "" {
				t.Errorf("next = %q, want none", next)
			}
			if gotActions := actions(got); !slices.Equal(gotActions, tt.want) {
				t.Errorf("actions = %v, want %v", gotActions, tt.want)
			}
		})
	}
	first, next, err := s.AuditLogFiltered(ctx, "acc_a", "", true, AuditFilter{Since: at(100)}, Page{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || next == "" {
		t.Fatalf("first page = %v, next %q; want two entries and a cursor", actions(first), next)
	}
	second, next, err := s.AuditLogFiltered(ctx, "acc_a", "", true, AuditFilter{Since: at(100)},
		Page{Limit: 2, Cursor: next})
	if err != nil {
		t.Fatal(err)
	}
	if got := actions(second); !slices.Equal(got, []string{"probe.0"}) || next != "" {
		t.Errorf("second page = %v, next %q; want probe.0 and no cursor", got, next)
	}
}
