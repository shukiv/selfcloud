// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// seedAt18 builds a database as it was after migration 0018, with data in every table that refers to
// nodes, and returns its path. The data is what an operator's database would hold at that schema.
func seedAt18(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "at18.db")
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
	for _, m := range migs[:18] {
		exec(m.sql)
		exec("INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, 0)", m.version, m.name)
	}
	exec(`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_a', 'A', 'active', 0, 0)`)
	exec(`INSERT INTO users (id, account_id, email, email_key, role, status, created_at, updated_at)
		VALUES ('usr_own', 'acc_a', 'own@x', 'own@x', 'owner', 'active', 0, 0),
		       ('usr_ann', 'acc_a', 'ann@x', 'ann@x', 'member', 'active', 0, 0)`)
	exec(`INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at) VALUES
		('fld_docs', 'acc_a', 'Docs', 'docs', 0, 0)`)
	exec(`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, size, created_at, modified_at, created_by, owner_id, trashed_at)
		VALUES ('nod_docs', 'acc_a', 'fld_docs', NULL, '', '', 'dir', 0, 0, 0, '', '', NULL),
		       ('nod_d', 'acc_a', 'fld_docs', 'nod_docs', 'd', 'd', 'dir', 0, 0, 0, 'usr_ann', 'usr_ann', NULL),
		       ('nod_f', 'acc_a', 'fld_docs', 'nod_d', 'f.txt', 'f.txt', 'file', 3, 0, 0, 'usr_ann', 'usr_ann', NULL),
		       ('nod_t', 'acc_a', 'fld_docs', 'nod_docs', 'old', 'old', 'file', 5, 0, 0, 'usr_ann', 'usr_ann', 1)`)
	exec(`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, size, created_at, modified_at, created_by, owner_id, no_download, editors_can_share)
		VALUES ('nod_n', 'acc_a', 'fld_docs', 'nod_docs', 'n', 'n', 'file', 0, 0, 0, 'usr_ann', 'usr_ann', 1, 1)`)
	exec(`INSERT INTO permissions (node_id, account_id, subject_kind, subject_id, role, created_at)
		VALUES ('nod_d', 'acc_a', 'user', 'usr_ann', 'editor', 0), ('nod_f', 'acc_a', 'user', 'usr_ann', 'viewer', 0)`)
	exec(`INSERT INTO stars (user_id, node_id, account_id, created_at) VALUES ('usr_ann', 'nod_f', 'acc_a', 0)`)
	exec(`INSERT INTO recent (user_id, node_id, account_id, viewed_at, edited_at, last_at) VALUES ('usr_ann', 'nod_f', 'acc_a', 0, NULL, 0)`)
	exec(`INSERT INTO versions (id, account_id, node_id, blob_id, size, etag, created_at, replaced_at)
		VALUES ('ver_1', 'acc_a', 'nod_f', 'blb_old', 2, 'e1', 0, 0)`)
	return path
}

// tableCounts returns the row count of every table, schema_migrations included.
func tableCounts(t *testing.T, db *sql.DB) map[string]int {
	t.Helper()
	ctx := context.Background()
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	rows.Close()
	out := map[string]int{}
	for _, n := range names {
		var c int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+n).Scan(&c); err != nil {
			t.Fatal(err)
		}
		out[n] = c
	}
	return out
}

// indexNames lists the named indexes of a table; the automatic ones are left out.
func indexNames(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		"SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = ? AND sql IS NOT NULL ORDER BY name", table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

// foreignKeyViolations counts the rows PRAGMA foreign_key_check reports.
func foreignKeyViolations(t *testing.T, db *sql.DB) int {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestUpgradeFrom0018RebuildsNodes opens a database at 0018 with data in every table that refers to nodes. The
// rebuild must keep every row, index and foreign key, and the cascades must still work afterwards.
func TestUpgradeFrom0018RebuildsNodes(t *testing.T) {
	ctx := context.Background()
	path := seedAt18(t)
	before := func() map[string]int {
		db, err := sql.Open("sqlite", dsn(path, []string{"busy_timeout(10000)"}, ""))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		return tableCounts(t, db)
	}()

	s, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatalf("open at 0018: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	migs, _ := loadMigrations()
	if v, _ := s.SchemaVersion(ctx); v != len(migs) {
		t.Fatalf("schema version = %d, want %d", v, len(migs))
	}
	// The open adds rows of its own (the migration row, and a personal folder with its root for each user),
	// so the counts may only grow, and the seeded rows must all be there.
	after := tableCounts(t, s.w)
	for name, n := range before {
		if name != "schema_migrations" && name != "folders" && name != "nodes" && after[name] != n {
			t.Errorf("rows of %s: %d before, %d after", name, n, after[name])
		}
	}
	for _, id := range []string{"nod_docs", "nod_d", "nod_f", "nod_t", "nod_n"} {
		var c int
		if err := s.w.QueryRowContext(ctx, "SELECT COUNT(*) FROM nodes WHERE id = ?", id).Scan(&c); err != nil || c != 1 {
			t.Errorf("seeded node %s after the upgrade = %d, %v; want 1", id, c, err)
		}
	}
	wantIndexes := []string{"nodes_blob", "nodes_child", "nodes_folder", "nodes_root", "nodes_size", "nodes_target", "nodes_trash"}
	got := indexNames(t, s.w, "nodes")
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(wantIndexes, ",") {
		t.Errorf("indexes on nodes = %v, want %v", got, wantIndexes)
	}
	// The access request row names the shortcut a request came through: two columns and an index (0019).
	cols := map[string]bool{}
	crows, err := s.w.QueryContext(ctx, "PRAGMA table_info(access_requests)")
	if err != nil {
		t.Fatal(err)
	}
	for crows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := crows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		cols[name] = true
	}
	crows.Close()
	for _, c := range []string{"via_node_id", "via_shortcut"} {
		if !cols[c] {
			t.Errorf("access_requests has no %s after the upgrade", c)
		}
	}
	hasIndex := false
	for _, n := range indexNames(t, s.w, "access_requests") {
		hasIndex = hasIndex || n == "access_requests_via"
	}
	if !hasIndex {
		t.Error("access_requests_via is missing after the upgrade")
	}
	if n := foreignKeyViolations(t, s.w); n != 0 {
		t.Errorf("foreign_key_check reports %d rows after the upgrade", n)
	}
	var fk int
	if err := s.w.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Errorf("foreign_keys after the upgrade = %d, %v; want on", fk, err)
	}
	// The cascades still work on the rebuilt table: deleting a directory takes its children's rows.
	if _, err := s.w.ExecContext(ctx, "DELETE FROM nodes WHERE id = 'nod_d'"); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := s.w.QueryRowContext(ctx, "SELECT COUNT(*) FROM permissions WHERE node_id IN ('nod_d', 'nod_f')").Scan(&left); err != nil || left != 0 {
		t.Errorf("permissions under a deleted directory = %d, %v; want 0 (cascade)", left, err)
	}
	if err := s.w.QueryRowContext(ctx, "SELECT COUNT(*) FROM nodes WHERE id = 'nod_f'").Scan(&left); err != nil || left != 0 {
		t.Errorf("child of a deleted directory = %d, %v; want 0 (cascade)", left, err)
	}
}

// openAt18 opens the seeded database without running migrations, so a test can run one by hand.
func openAt18(t *testing.T, path string) *Store {
	t.Helper()
	w, err := sql.Open("sqlite", dsn(path, []string{"busy_timeout(10000)", "foreign_keys(1)", "synchronous(NORMAL)", "journal_mode(WAL)"}, "immediate"))
	if err != nil {
		t.Fatal(err)
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	s := &Store{w: w, log: slog.New(slog.DiscardHandler)}
	t.Cleanup(func() { w.Close() })
	return s
}

// TestFailedRebuildRollsBack runs the real 0019 rebuild with a failure appended, and with a foreign key left
// dangling. Either way the database must be exactly as it was: schema 18, every row, the old nodes table, and
// foreign keys on again.
func TestFailedRebuildRollsBack(t *testing.T) {
	ctx := context.Background()
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	real := migs[18]
	cases := map[string]string{
		"a statement fails after the rebuild": real.sql + "\nINSERT INTO no_such_table VALUES (1);",
		"a foreign key dangles at the commit": real.sql + "\nINSERT INTO permissions (node_id, account_id, subject_kind, subject_id, role, created_at)" +
			" VALUES ('nod_gone', 'acc_a', 'user', 'usr_ann', 'viewer', 0);",
	}
	for name, sqlText := range cases {
		t.Run(name, func(t *testing.T) {
			path := seedAt18(t)
			s := openAt18(t, path)
			before := tableCounts(t, s.w)
			bad := migration{version: real.version, name: real.name, sql: sqlText, foreignKeysOff: true}
			if err := s.applyMigrations(ctx, []migration{bad}); err == nil {
				t.Fatal("the migration applied; want an error")
			}
			if v, _ := s.SchemaVersion(ctx); v != 18 {
				t.Errorf("schema version = %d, want 18", v)
			}
			after := tableCounts(t, s.w)
			for tbl, n := range before {
				if after[tbl] != n {
					t.Errorf("rows of %s: %d before, %d after the failed rebuild", tbl, n, after[tbl])
				}
			}
			var tmp int
			if err := s.w.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE name = 'nodes_new'").Scan(&tmp); err != nil || tmp != 0 {
				t.Errorf("nodes_new after the failed rebuild = %d, %v; want none", tmp, err)
			}
			var fk int
			if err := s.w.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
				t.Errorf("foreign_keys after the failed rebuild = %d, %v; want on", fk, err)
			}
			if n := foreignKeyViolations(t, s.w); n != 0 {
				t.Errorf("foreign_key_check reports %d rows after the rollback", n)
			}
		})
	}
}

// TestRebuildMigrationIsMarkedForeignKeysOff keeps the runner's mode on the right migration: the rebuild runs with
// foreign keys off, and no other migration does.
func TestRebuildMigrationIsMarkedForeignKeysOff(t *testing.T) {
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migs {
		want := m.version == 19
		if m.foreignKeysOff != want {
			t.Errorf("migration %s: foreignKeysOff = %v, want %v", m.name, m.foreignKeysOff, want)
		}
	}
}

// TestDirectiveCountsOnlyOnTheFirstLine: the runner's mode is the first line of a migration. The same text on a later
// line is a comment, and must not switch foreign keys off.
func TestDirectiveCountsOnlyOnTheFirstLine(t *testing.T) {
	if hasDirective([]byte("-- a comment\n-- runner: foreign_keys=off\nCREATE TABLE x (a INTEGER);\n")) {
		t.Error("a directive on the second line switched foreign keys off")
	}
	if !hasDirective([]byte("-- runner: foreign_keys=off\nCREATE TABLE x (a INTEGER);\n")) {
		t.Error("a directive on the first line did not switch foreign keys off")
	}
}

// TestForeignKeyFailureNamesTheTables: a rebuild that leaves a dangling reference is refused with the tables that
// hold the violations, so an operator knows where to look.
func TestForeignKeyFailureNamesTheTables(t *testing.T) {
	ctx := context.Background()
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	dangling := migs[18].sql + "\nINSERT INTO permissions (node_id, account_id, subject_kind, subject_id, role, created_at)" +
		" VALUES ('nod_gone', 'acc_a', 'user', 'usr_ann', 'viewer', 0);"
	s := openAt18(t, seedAt18(t))
	err = s.applyMigrations(ctx, []migration{{version: 19, name: "0019_shortcuts.sql", sql: dangling, foreignKeysOff: true}})
	if err == nil {
		t.Fatal("a dangling reference was accepted")
	}
	if !strings.Contains(err.Error(), "permissions") {
		t.Errorf("the error does not name the table with the violation: %v", err)
	}
}

// TestRunnerRefusesForeignKeysOnInside: the migration's transaction must see foreign keys off. A transaction that
// starts with them on is refused, so a cascading DROP TABLE cannot run.
func TestRunnerRefusesForeignKeysOnInside(t *testing.T) {
	ctx := context.Background()
	s := openAt18(t, seedAt18(t))
	// The writer has one connection, so each transaction is closed before the next statement runs on it.
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := requireForeignKeysOff(ctx, tx); err == nil {
		t.Error("a transaction with foreign keys on was accepted for a rebuild")
	}
	tx.Rollback()
	if _, err := s.w.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatal(err)
	}
	tx2, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := requireForeignKeysOff(ctx, tx2); err != nil {
		t.Errorf("foreign keys off was refused: %v", err)
	}
	tx2.Rollback()
}
