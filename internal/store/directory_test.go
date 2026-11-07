// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"slices"
	"testing"
)

// TestSearchDirectoryEmptyQueryListsTheAccount: an empty q matches every active user and every group of the
// account, a user with no display name included, and nothing of another account, a pending user or a disabled
// one. A real q keeps LIKE's wildcards literal.
func TestSearchDirectoryEmptyQueryListsTheAccount(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.w.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_d', 'D', 'active', 0, 0)`)
	exec(`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_o', 'O', 'active', 0, 0)`)
	user := func(id, acc, email, name, status string) {
		t.Helper()
		exec(`INSERT INTO users (id, account_id, email, email_key, display_name, role, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, 'member', ?, 0, 0)`, id, acc, email, email, name, status)
	}
	user("usr_ann", "acc_d", "ann@example.test", "Ann", "active")
	user("usr_nn", "acc_d", "nn@example.test", "", "active")
	user("usr_pen", "acc_d", "pen@example.test", "Pending", "pending")
	user("usr_dis", "acc_d", "dis@example.test", "Disabled", "disabled")
	user("usr_oth", "acc_o", "oth@example.test", "Other", "active")
	exec(`INSERT INTO groups (id, account_id, name, name_key, created_at, updated_at) VALUES ('grp_fin', 'acc_d', 'Finance', 'finance', 0, 0)`)
	exec(`INSERT INTO groups (id, account_id, name, name_key, created_at, updated_at) VALUES ('grp_ops', 'acc_d', 'Ops', 'ops', 0, 0)`)
	exec(`INSERT INTO groups (id, account_id, name, name_key, created_at, updated_at) VALUES ('grp_oth', 'acc_o', 'Elsewhere', 'elsewhere', 0, 0)`)

	ids := func(es []DirectoryEntry) []string {
		var out []string
		for _, e := range es {
			out = append(out, e.ID)
		}
		slices.Sort(out)
		return out
	}

	users, err := s.SearchUsers(ctx, "acc_d", "", 21)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(users), []string{"usr_ann", "usr_nn"}; !slices.Equal(got, want) {
		t.Errorf("empty q users = %v, want %v", got, want)
	}
	for _, u := range users {
		if u.ID == "usr_nn" && u.Name != "nn@example.test" {
			t.Errorf("a user with no display name is named %q, want its email", u.Name)
		}
	}

	groups, err := s.SearchGroups(ctx, "acc_d", "", 21)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(groups), []string{"grp_fin", "grp_ops"}; !slices.Equal(got, want) {
		t.Errorf("empty q groups = %v, want %v", got, want)
	}

	// A real q stays literal: "a%" and "a_" match no email or name here.
	for _, q := range []string{"a%", "a_", "%"} {
		got, err := s.SearchUsers(ctx, "acc_d", q, 21)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Errorf("q %q matched %v; wildcards must be literal", q, ids(got))
		}
	}

	// The limit is honoured, so a caller that asks for one more than it shows can tell there is more.
	few, err := s.SearchUsers(ctx, "acc_d", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(few) != 1 {
		t.Errorf("limit 1 returned %d users", len(few))
	}
}
