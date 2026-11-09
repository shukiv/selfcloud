// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// accountsFixture seeds three accounts with different names, statuses, external IDs and quotas, and two users in the
// first. The seeds are direct inserts, so the times and the counters are exact.
func accountsFixture(t *testing.T) (*recoveryFixture, []*store.Account) {
	t.Helper()
	f := newRecoveryFixture(t)
	// Created one minute apart, oldest first, so the listing order is the order here.
	base := store.Now().Add(-time.Hour)
	accs := []*store.Account{
		{Name: "Acme Ltd", Status: store.AccountActive, ExternalID: "ext-1", QuotaBytes: 1 << 30, UsedBytes: 4096},
		{Name: "Café Nord", Status: store.AccountSuspended},
		{Name: "Beta", Status: store.AccountActive, ExternalID: "ext-2"},
	}
	for i, a := range accs {
		a.ID = id.New("acc")
		a.CreatedAt = base.Add(time.Duration(i) * time.Minute)
		a.UpdatedAt = a.CreatedAt
		if err := f.st.Write(f.ctx, func(tx *store.Tx) error { return tx.InsertAccount(f.ctx, a) }); err != nil {
			t.Fatal(err)
		}
	}
	f.seedUser(accs[0].ID, "ann@acme.example", store.RoleOwner, store.UserActive, "")
	f.seedUser(accs[0].ID, "ben@acme.example", store.RoleMember, store.UserDisabled, "")
	return f, accs
}

// accountRows runs `admin accounts` with args and returns its exit code and its rows, split into cells. The header is
// the first row, and it is returned apart.
func accountRows(t *testing.T, args ...string) (int, []string, [][]string, string) {
	t.Helper()
	code, out, errOut := runCmd(t, append([]string{"admin", "accounts"}, args...)...)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if out == "" {
		return code, nil, nil, errOut
	}
	header := strings.Split(lines[0], "\t")
	var rows [][]string
	for _, l := range lines[1:] {
		rows = append(rows, strings.Split(l, "\t"))
	}
	return code, header, rows, errOut
}

func TestAdminAccountsPrintsATable(t *testing.T) {
	_, accs := accountsFixture(t)
	code, header, rows, errOut := accountRows(t)
	if code != 0 {
		t.Fatalf("admin accounts = %d, stderr %q", code, errOut)
	}
	want := []string{"id", "name", "status", "external_id", "created", "users", "used_bytes", "quota_bytes"}
	if strings.Join(header, "\t") != strings.Join(want, "\t") {
		t.Fatalf("header = %v, want %v", header, want)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3 (oldest first)", len(rows))
	}
	acme := rows[0]
	if acme[0] != accs[0].ID || acme[1] != "Acme Ltd" || acme[2] != store.AccountActive || acme[3] != "ext-1" {
		t.Errorf("acme row = %v", acme)
	}
	if acme[5] != "2" || acme[6] != "4096" || acme[7] != "1073741824" {
		t.Errorf("acme counters = users %s, used %s, quota %s; want 2, 4096, 1073741824", acme[5], acme[6], acme[7])
	}
	if _, err := time.Parse(time.RFC3339, acme[4]); err != nil || !strings.HasSuffix(acme[4], "Z") {
		t.Errorf("created %q is not RFC 3339 UTC", acme[4])
	}
	// An account with no external ID or no quota shows a dash.
	cafe := rows[1]
	if cafe[3] != "-" || cafe[7] != "-" || cafe[2] != store.AccountSuspended {
		t.Errorf("cafe row = %v; want a dash for the external ID and the quota, status suspended", cafe)
	}
}

func TestAdminAccountsFilters(t *testing.T) {
	_, accs := accountsFixture(t)
	cases := []struct {
		name string
		args []string
		want []string // account IDs, in order
	}{
		{"status suspended", []string{"-status", "suspended"}, []string{accs[1].ID}},
		{"status active", []string{"-status", "active"}, []string{accs[0].ID, accs[2].ID}},
		{"name folds case", []string{"-name", "ACME"}, []string{accs[0].ID}},
		{"name folds accents", []string{"-name", "cafe"}, []string{accs[1].ID}},
		{"name is a substring", []string{"-name", "ta"}, []string{accs[2].ID}},
		{"external ID is exact", []string{"-external-id", "ext-2"}, []string{accs[2].ID}},
		{"external ID is not a prefix", []string{"-external-id", "ext"}, nil},
		{"filters combine", []string{"-status", "active", "-name", "beta"}, []string{accs[2].ID}},
		{"empty result", []string{"-external-id", "nope"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, header, rows, errOut := accountRows(t, tc.args...)
			if code != 0 {
				t.Fatalf("exit = %d, stderr %q", code, errOut)
			}
			if len(header) != 8 {
				t.Fatalf("header = %v", header)
			}
			var got []string
			for _, r := range rows {
				got = append(got, r[0])
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("accounts = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAdminAccountsEmptyResultPrintsTheHeaderOnly(t *testing.T) {
	accountsFixture(t)
	code, out, _ := runCmd(t, "admin", "accounts", "-external-id", "nope")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if strings.Count(out, "\n") != 1 || !strings.HasPrefix(out, "id\tname\t") {
		t.Errorf("output = %q, want the header line only", out)
	}
}

func TestAdminAccountsRefusesBadInput(t *testing.T) {
	accountsFixture(t)
	bad := [][]string{
		{"-status", "deleting"},
		{"-status", "Active"},
		{"-name", "bad\tname"},
		{"-name", "bad\nname"},
		{"-external-id", strings.Repeat("x", 256)},
		{"-external-id", "ext\x00"},
		{"stray"},
	}
	for _, args := range bad {
		if code, _, _ := runCmd(t, append([]string{"admin", "accounts"}, args...)...); code != 2 {
			t.Errorf("admin accounts %q exit = %d, want 2", args, code)
		}
	}
}

func TestAdminAccountsIsReadOnly(t *testing.T) {
	f, accs := accountsFixture(t)
	before := len(f.auditRows(accs[0].ID))
	if code, _, _ := runCmd(t, "admin", "accounts"); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if after := len(f.auditRows(accs[0].ID)); after != before {
		t.Errorf("audit rows went from %d to %d; admin accounts is read-only", before, after)
	}
}
