// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// seedAt36 builds a database as it was at migration 36, the last one before the audit log got its foreign key. It holds
// two live accounts, one account being deleted, installation-wide rows, the records of deletions, and rows that name an
// account already deleted (orphans). Open on it applies migration 37.
func seedAt36(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "at36.db")
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	old, err := sql.Open("sqlite", dsn(path, []string{"busy_timeout(10000)", "foreign_keys(1)", "journal_mode(WAL)"}, ""))
	if err != nil {
		t.Fatal(err)
	}
	old.SetMaxOpenConns(1)
	defer old.Close()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := old.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at INTEGER NOT NULL)`)
	for _, m := range migs[:36] {
		exec(m.sql)
		exec("INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, 0)", m.version, m.name)
	}
	exec(`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES
		('acc_a', 'Acme', 'active', 0, 0), ('acc_b', 'Beta', 'active', 0, 0), ('acc_del', 'Deleting Ltd', 'deleting', 0, 0)`)
	// Ids 4 to 7 are the records of deletions and the rows of acc_gone, an account already deleted. acc_del is being
	// deleted: its request (7) keeps its name; its own key (8) stays with it until the account goes.
	exec(`INSERT INTO audit_log (id, time, account_id, actor_kind, actor_id, action, target, details, private_user_id) VALUES
		(1, 100, 'acc_a', 'account_key', 'key_a', 'key.create', 'key_a', '{"n":1}', NULL),
		(2, 200, 'acc_b', 'account_key', 'key_b', 'key.create', 'key_b', NULL, 'usr_b'),
		(3, 300, NULL, 'admin_key', 'key_admin', 'account.create', 'acc_a', NULL, NULL),
		(4, 400, 'acc_gone', 'system', 'job_1', 'account.deleted', 'acc_gone', NULL, NULL),
		(5, 500, 'acc_gone', 'account_key', 'key_gone', 'key.create', 'key_gone', NULL, NULL),
		(6, 600, 'acc_gone', 'admin_key', 'key_admin', 'account.delete', 'acc_gone', NULL, NULL),
		(7, 700, 'acc_del', 'admin_key', 'key_admin', 'account.delete', 'acc_del', NULL, NULL),
		(8, 800, 'acc_del', 'account_key', 'key_del', 'key.create', 'key_del', NULL, NULL)`)
	exec(`INSERT INTO audit_log (time, account_id, actor_kind, actor_id, action, outcome) VALUES (900, 'acc_b', 'user', 'usr_b', 'node.create', 'ok')`)
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

type auditRow struct {
	id, time                int64
	account, action, target sql.NullString
	details, privateUser    sql.NullString
}

// auditRows reads every audit row of s in id order.
func auditRows(t *testing.T, s *Store) []auditRow {
	t.Helper()
	rows, err := s.w.QueryContext(context.Background(), `SELECT id, time, account_id, action, target, details, private_user_id
		FROM audit_log ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []auditRow
	for rows.Next() {
		var r auditRow
		if err := rows.Scan(&r.id, &r.time, &r.account, &r.action, &r.target, &r.details, &r.privateUser); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return got
}

// TestMigration0037KeepsDeletionRecordsAndRemovesOrphans upgrades a database from 36. The records of deletions stay at
// installation level, the pending one named by the account it is about. Other rows of an account already deleted go.
// Every row of a live account and the installation-wide rows stay with their values.
func TestMigration0037KeepsDeletionRecordsAndRemovesOrphans(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, Options{Path: seedAt36(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got := auditRows(t, s)
	// Id 5 is the one orphan of acc_gone that is not a deletion record; it goes. Id 9 is the row written in the seed.
	wantIDs := []int64{1, 2, 3, 4, 6, 7, 8, 9}
	if len(got) != len(wantIDs) {
		t.Fatalf("%d rows left, want %d: %+v", len(got), len(wantIDs), got)
	}
	for i, r := range got {
		if r.id != wantIDs[i] {
			t.Errorf("row %d has id %d, want %d", i, r.id, wantIDs[i])
		}
	}
	byID := map[int64]auditRow{}
	for _, r := range got {
		byID[r.id] = r
	}
	// The record of a deletion whose account is gone: installation level, target still the old ID (its name is gone).
	if r := byID[4]; r.account.Valid || r.action.String != "account.deleted" || r.target.String != "acc_gone" {
		t.Errorf("orphan account.deleted = %+v, want installation level with its old target", r)
	}
	if r := byID[6]; r.account.Valid || r.action.String != "account.delete" {
		t.Errorf("orphan account.delete = %+v, want installation level", r)
	}
	// The pending request of an account still being deleted: installation level, named by the account's name.
	if r := byID[7]; r.account.Valid || r.target.String != "Deleting Ltd" {
		t.Errorf("pending account.delete = %+v, want installation level and target Deleting Ltd", r)
	}
	// The pending account's own entries are not records of the deletion: they stay with the account.
	if r := byID[8]; r.account.String != "acc_del" {
		t.Errorf("entry of the pending account moved: %+v", r)
	}
	if r := byID[1]; r.details.String != `{"n":1}` || r.privateUser.Valid || r.account.String != "acc_a" {
		t.Errorf("row 1 changed: %+v", r)
	}
	if r := byID[2]; r.privateUser.String != "usr_b" || r.account.String != "acc_b" {
		t.Errorf("row 2 changed: %+v", r)
	}
	if r := byID[3]; r.account.Valid {
		t.Errorf("installation row has account %q, want NULL", r.account.String)
	}
}

// TestMigration0037KeepsIndexesAndForeignKeys checks what the rebuild must leave behind: the foreign key with ON DELETE
// CASCADE, the index that existed before, the new time index, no dangling reference, and no table left under the
// temporary name.
func TestMigration0037KeepsIndexesAndForeignKeys(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, Options{Path: seedAt36(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	if err := s.w.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name = 'audit_log_new'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("temporary table left behind: %d, %v", n, err)
	}
	var onDelete, table string
	if err := s.w.QueryRowContext(ctx, `SELECT "table", on_delete FROM pragma_foreign_key_list('audit_log')`).Scan(&table, &onDelete); err != nil {
		t.Fatalf("audit_log has no foreign key: %v", err)
	}
	if table != "accounts" || onDelete != "CASCADE" {
		t.Errorf("foreign key to %s ON DELETE %s, want accounts CASCADE", table, onDelete)
	}
	indexes := map[string]bool{}
	rows, err := s.w.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'audit_log'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		indexes[name] = true
	}
	for _, want := range []string{"audit_log_account", "audit_log_time"} {
		if !indexes[want] {
			t.Errorf("index %s missing on audit_log: have %v", want, indexes)
		}
	}
	var fk int
	if err := s.w.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_foreign_key_check('audit_log')`).Scan(&fk); err != nil || fk != 0 {
		t.Errorf("foreign key check reports %d rows, %v", fk, err)
	}
}

// TestAuditRetentionUsesTheTimeIndex checks that the retention delete finds its rows through audit_log_time and not by
// a scan of the table.
func TestAuditRetentionUsesTheTimeIndex(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	var plan string
	err := s.w.QueryRowContext(ctx, `EXPLAIN QUERY PLAN SELECT id FROM audit_log WHERE time < ? LIMIT ?`, 1, 1).Scan(new(int), new(int), new(int), &plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "audit_log_time") {
		t.Errorf("plan %q does not use audit_log_time", plan)
	}
}

// TestDeleteAccountRemovesOnlyItsAuditRows: deleting an account takes its own entries with it, and leaves another
// account's entries and the installation-wide ones.
func TestDeleteAccountRemovesOnlyItsAuditRows(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	at := time.Unix(1_700_000_000, 0)
	for _, id := range []string{"acc_gone", "acc_keep"} {
		if err := s.Write(ctx, func(tx *Tx) error {
			return tx.InsertAccount(ctx, &Account{ID: id, Name: id, Status: AccountActive, CreatedAt: at, UpdatedAt: at})
		}); err != nil {
			t.Fatal(err)
		}
		if err := s.Write(ctx, func(tx *Tx) error {
			return tx.InsertAudit(ctx, &AuditEntry{Time: at, AccountID: id, ActorKind: "admin_key", ActorID: "k", Action: "key.create", Target: id})
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.InsertAudit(ctx, &AuditEntry{Time: at, ActorKind: "admin_key", ActorID: "k", Action: "account.create", Target: "acc_gone"})
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteAccount(ctx, "acc_gone") }); err != nil {
		t.Fatal(err)
	}
	count := func(where string, args ...any) int {
		t.Helper()
		var n int
		if err := s.w.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_log WHERE "+where, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count("account_id = ?", "acc_gone"); n != 0 {
		t.Errorf("%d entries of the deleted account remain", n)
	}
	if n := count("account_id = ?", "acc_keep"); n != 1 {
		t.Errorf("another account has %d entries, want 1", n)
	}
	if n := count("account_id IS NULL"); n != 1 {
		t.Errorf("installation-wide entries = %d, want 1", n)
	}
	// Deleting an account that is gone is not an error, and it removes nothing.
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteAccount(ctx, "acc_gone") }); err != nil {
		t.Fatal(err)
	}
}

// TestInsertAuditRejectsAnUnknownAccount: an entry that names an account the database does not have is refused, so
// nothing can write a row the cascade will not reach. The installation-wide entry, with no account, is accepted.
func TestInsertAuditRejectsAnUnknownAccount(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	err := s.Write(ctx, func(tx *Tx) error {
		return tx.InsertAudit(ctx, &AuditEntry{Time: time.Now(), AccountID: "acc_missing", ActorKind: "system", Action: "x"})
	})
	if err == nil {
		t.Fatal("an entry for an account that does not exist was written")
	}
}

// TestPruneAuditRemovesOnlyOlderRows: a row older than the cutoff goes. A row just inside it stays, and so does a row
// exactly at the cutoff, since only rows before it are removed. Installation-wide rows follow the same rule.
func TestPruneAuditRemovesOnlyOlderRows(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	cutoff := time.Unix(1_700_000_000, 0)
	if err := s.Write(ctx, func(tx *Tx) error {
		if err := tx.InsertAccount(ctx, &Account{ID: "acc_a", Name: "A", Status: AccountActive, CreatedAt: cutoff, UpdatedAt: cutoff}); err != nil {
			return err
		}
		for _, e := range []AuditEntry{
			{Time: cutoff.Add(-time.Minute), AccountID: "acc_a", Action: "old"},
			{Time: cutoff.Add(time.Minute), AccountID: "acc_a", Action: "inside"},
			{Time: cutoff, AccountID: "acc_a", Action: "at.cutoff"},
			{Time: cutoff.Add(-time.Minute), Action: "old.installation"},
			{Time: cutoff.Add(time.Minute), Action: "inside.installation"},
			{Time: cutoff, Action: "at.cutoff.installation"},
		} {
			e.ActorKind, e.ActorID = "system", "t"
			if err := tx.InsertAudit(ctx, &e); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	n, err := s.PruneAudit(ctx, cutoff, 1000)
	if err != nil || n != 2 {
		t.Fatalf("PruneAudit removed %d, %v; want 2", n, err)
	}
	var left []string
	rows, err := s.w.QueryContext(ctx, "SELECT action FROM audit_log ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		left = append(left, a)
	}
	want := "inside,at.cutoff,inside.installation,at.cutoff.installation"
	if strings.Join(left, ",") != want {
		t.Errorf("left %v, want %s", left, want)
	}
}

// TestPruneAuditIsBounded: one call removes at most the batch it is given, so a large backlog is taken in several writes.
func TestPruneAuditIsBounded(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	old := time.Unix(1_600_000_000, 0)
	if err := s.Write(ctx, func(tx *Tx) error {
		for i := 0; i < 2500; i++ {
			if err := tx.InsertAudit(ctx, &AuditEntry{Time: old, ActorKind: "system", ActorID: "t", Action: "old"}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	var total int64
	for pass, want := range []int64{1000, 1000, 500, 0} {
		n, err := s.PruneAudit(ctx, now, 1000)
		if err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Fatalf("pass %d removed %d, want %d", pass, n, want)
		}
		total += n
	}
	if total != 2500 {
		t.Errorf("removed %d in all, want 2500", total)
	}
}

// TestPruneAccountAuditIsBounded: the deletion of an account takes its entries in batches. Each call removes at most
// its batch, leaves the other accounts' and the installation-wide entries alone, and a call after the last finds none.
func TestPruneAccountAuditIsBounded(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	at := time.Unix(1_700_000_000, 0)
	for _, id := range []string{"acc_big", "acc_other"} {
		if err := s.Write(ctx, func(tx *Tx) error {
			return tx.InsertAccount(ctx, &Account{ID: id, Name: id, Status: AccountDeleting, CreatedAt: at, UpdatedAt: at})
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Write(ctx, func(tx *Tx) error {
		for i := 0; i < 2500; i++ {
			if err := tx.InsertAudit(ctx, &AuditEntry{Time: at, AccountID: "acc_big", ActorKind: "system", ActorID: "t", Action: "big"}); err != nil {
				return err
			}
		}
		for i := 0; i < 5; i++ {
			if err := tx.InsertAudit(ctx, &AuditEntry{Time: at, AccountID: "acc_other", ActorKind: "system", ActorID: "t", Action: "other"}); err != nil {
				return err
			}
			if err := tx.InsertAudit(ctx, &AuditEntry{Time: at, ActorKind: "system", ActorID: "t", Action: "installation"}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for pass, want := range []int64{1000, 1000, 500, 0} {
		n, err := s.PruneAccountAudit(ctx, "acc_big", 1000)
		if err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Fatalf("pass %d removed %d, want %d", pass, n, want)
		}
	}
	var others, installation, big int
	for _, q := range []struct {
		where string
		n     *int
	}{{"account_id = 'acc_other'", &others}, {"account_id IS NULL", &installation}, {"account_id = 'acc_big'", &big}} {
		if err := s.w.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_log WHERE "+q.where).Scan(q.n); err != nil {
			t.Fatal(err)
		}
	}
	if others != 5 || installation != 5 || big != 0 {
		t.Errorf("left other=%d installation=%d big=%d, want 5, 5, 0", others, installation, big)
	}
}

// TestDeleteAccountAfterBatchesRemovesTheRest: a deletion whose batches stopped part way, as after a crash, is finished
// by the account's own delete, which takes the entries that the batches did not reach and any that arrived since.
func TestDeleteAccountAfterBatchesRemovesTheRest(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	at := time.Unix(1_700_000_000, 0)
	if err := s.Write(ctx, func(tx *Tx) error {
		if err := tx.InsertAccount(ctx, &Account{ID: "acc_x", Name: "X", Status: AccountDeleting, CreatedAt: at, UpdatedAt: at}); err != nil {
			return err
		}
		for i := 0; i < 1500; i++ {
			if err := tx.InsertAudit(ctx, &AuditEntry{Time: at, AccountID: "acc_x", ActorKind: "system", ActorID: "t", Action: "x"}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PruneAccountAudit(ctx, "acc_x", 1000); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.InsertAudit(ctx, &AuditEntry{Time: at, AccountID: "acc_x", ActorKind: "system", ActorID: "t", Action: "late"})
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteAccount(ctx, "acc_x") }); err != nil {
		t.Fatal(err)
	}
	if n := len(auditRows(t, s)); n != 0 {
		t.Errorf("%d rows left after the account was deleted", n)
	}
}
