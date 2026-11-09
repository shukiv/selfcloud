// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// TestAdminAccountsEscapesControlCharacters stores an account whose name and external ID hold a tab, a line break and an
// escape, the way a local create-account once could. The listing must print it as one row, with the controls written out,
// and no row may start with the account ID that the name forges.
func TestAdminAccountsEscapesControlCharacters(t *testing.T) {
	f, _ := accountsFixture(t)
	now := store.Now()
	hostile := &store.Account{ID: id.New("acc"), Name: "Acme\tinjected\nacc_fake\tx\x1b[2J", Status: store.AccountActive,
		ExternalID: "ext\tx\ny", CreatedAt: now, UpdatedAt: now}
	if err := f.st.Write(f.ctx, func(tx *store.Tx) error { return tx.InsertAccount(f.ctx, hostile) }); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runCmd(t, "admin", "accounts")
	if code != 0 {
		t.Fatalf("admin accounts = %d, stderr %q", code, errOut)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 1+4 {
		t.Fatalf("got %d lines, want the header and four accounts:\n%s", len(lines), out)
	}
	for _, l := range lines[1:] {
		if n := strings.Count(l, "\t"); n != 7 {
			t.Errorf("row has %d tabs, want 7: %q", n, l)
		}
		if strings.HasPrefix(l, "acc_fake") {
			t.Errorf("a forged account row: %q", l)
		}
	}
	for _, want := range []string{`Acme\tinjected\nacc_fake\tx\x1b[2J`, `ext\tx\ny`} {
		if !strings.Contains(out, want) {
			t.Errorf("listing does not print %q:\n%s", want, out)
		}
	}
	if strings.ContainsRune(out, 0x1b) {
		t.Errorf("an escape byte reached the output: %q", out)
	}
}

// TestAdminAuditEscapesControlCharacters stores an entry whose action, target and details hold a tab, a line break and an
// escape. Each entry must stay one line of six cells, and no control byte may reach the output.
func TestAdminAuditEscapesControlCharacters(t *testing.T) {
	f := newAuditFixture(t)
	e := &store.AuditEntry{Time: store.Now(), AccountID: f.a1, ActorKind: "user", ActorID: f.ann,
		Action: "file.create\tnode.delete", Target: "nod_9\nacc_fake", Details: map[string]any{"name": "a\x1b[2J\tb\nc"}}
	if err := f.st.Write(f.ctx, func(tx *store.Tx) error { return tx.InsertAudit(f.ctx, e) }); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runCmd(t, "admin", "audit")
	if code != 0 {
		t.Fatalf("admin audit = %d, stderr %q", code, errOut)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 1+6 {
		t.Fatalf("got %d lines, want the header and six entries:\n%s", len(lines), out)
	}
	for _, l := range lines[1:] {
		if n := strings.Count(l, "\t"); n != 5 {
			t.Errorf("row has %d tabs, want 5: %q", n, l)
		}
	}
	for _, want := range []string{`file.create\tnode.delete`, `nod_9\nacc_fake`, `name=a\x1b[2J\tb\nc`} {
		if !strings.Contains(out, want) {
			t.Errorf("audit does not print %q:\n%s", want, out)
		}
	}
	if strings.ContainsRune(out, 0x1b) {
		t.Errorf("an escape byte reached the output: %q", out)
	}
}

// TestAdminAuditPrintsIntegersAsStored checks that a whole number in the details prints as the stored integer, not in
// exponent form: 1073741824 is not 1.073741824e+09.
func TestAdminAuditPrintsIntegersAsStored(t *testing.T) {
	f := newAuditFixture(t)
	e := &store.AuditEntry{Time: store.Now(), AccountID: f.a1, ActorKind: "system", ActorID: "cli", Action: "account.update",
		Target: f.a1, Details: map[string]any{"quota_bytes": int64(1073741824), "used": int64(1048576)}}
	if err := f.st.Write(f.ctx, func(tx *store.Tx) error { return tx.InsertAudit(f.ctx, e) }); err != nil {
		t.Fatal(err)
	}
	_, _, rows, _ := auditLines(t, "-action", "account.update", "-limit", "1")
	if len(rows) != 1 {
		t.Fatalf("rows = %v, want the one entry", rows)
	}
	if want := "quota_bytes=1073741824 used=1048576"; rows[0][5] != want {
		t.Errorf("details = %q, want %q", rows[0][5], want)
	}
}

// TestAdminCreateAccountRefusesControlCharacters checks that create-account applies the API's rule to the name and the
// external ID: no control character, and valid UTF-8. A refused account stores nothing.
func TestAdminCreateAccountRefusesControlCharacters(t *testing.T) {
	t.Setenv("SELFCLOUD_DATA_DIR", t.TempDir())
	for _, args := range [][]string{
		{"-name", "Acme\tinjected"},
		{"-name", "Acme\nacc_fake"},
		{"-name", "Acme\x1b[2J"},
		{"-name", "Acme\xff"},
		{"-name", "Acme", "-external-id", "ext\ty"},
		{"-name", "Acme", "-external-id", "ext\x1b[2J"},
	} {
		code, out, errOut := runCmd(t, append([]string{"admin", "create-account"}, args...)...)
		if code != 2 || out != "" {
			t.Errorf("create-account %q = %d %q %q, want exit 2 and no output", args, code, out, errOut)
		}
	}
	code, out, errOut := runCmd(t, "admin", "accounts")
	if code != 0 || strings.Count(out, "\n") != 1 {
		t.Errorf("accounts after the refusals = %d %q %q, want the header only", code, out, errOut)
	}
}
