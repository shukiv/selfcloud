// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// TestAccountDeleteIsRecordedInstallationWide: the request to delete an account and the completed deletion are both
// kept in the installation-wide log (account_id NULL), named by the account's name and not its ID. The account's own
// entries go with it, and another account's entries stay.
func TestAccountDeleteIsRecordedInstallationWide(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t)
	admin := adminKey(t, s)
	gone, _ := newAccount(t, s, admin, "Gone Ltd")
	keep, _ := newAccount(t, s, admin, "Keep Ltd")

	rec := call(t, s, "DELETE", "/api/v1/accounts/"+gone.ID, admin, nil)
	wantStatus(t, rec, 202)
	if err := s.jobs.RunPending(ctx); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", s.cfg.Store.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	type row struct {
		account sql.NullString
		target  string
	}
	entries := func(action string) []row {
		t.Helper()
		rows, err := db.QueryContext(ctx, "SELECT account_id, target FROM audit_log WHERE action = ? ORDER BY id", action)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.account, &r.target); err != nil {
				t.Fatal(err)
			}
			out = append(out, r)
		}
		return out
	}
	for _, action := range []string{"account.delete", "account.deleted"} {
		got := entries(action)
		if len(got) != 1 {
			t.Fatalf("%s: %d entries, want 1", action, len(got))
		}
		if got[0].account.Valid || got[0].target != "Gone Ltd" {
			t.Errorf("%s: account %v target %q, want installation-wide and named Gone Ltd", action, got[0].account, got[0].target)
		}
	}
	var n int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_log WHERE account_id = ?", gone.ID).Scan(&n); err != nil || n != 0 {
		t.Errorf("the deleted account still has %d entries, %v", n, err)
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_log WHERE account_id = ?", keep.ID).Scan(&n); err != nil || n == 0 {
		t.Errorf("another account has %d entries, %v; want its own", n, err)
	}
}
