// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// TestUpgradeFrom0008KeepsEveryMembersAccess builds a database as it was at migration 0008, with
// grants and users made by the old code, and opens it with this binary. Every member must keep the
// access the grant gave: read becomes viewer and write becomes editor, on the folder's root. Each
// user gets a personal folder, and the owner of every node is backfilled.
func TestUpgradeFrom0008KeepsEveryMembersAccess(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	old, err := sql.Open("sqlite", dsn(path, []string{"busy_timeout(10000)", "foreign_keys(1)", "journal_mode(WAL)"}, ""))
	if err != nil {
		t.Fatal(err)
	}
	old.SetMaxOpenConns(1)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := old.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at INTEGER NOT NULL)`)
	for _, m := range migs[:8] {
		exec(m.sql)
		exec("INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, 0)", m.version, m.name)
	}

	exec(`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_a', 'A', 'active', 0, 0)`)
	exec(`INSERT INTO users (id, account_id, email, email_key, role, status, created_at, updated_at)
		VALUES ('usr_own', 'acc_a', 'own@x', 'own@x', 'owner', 'active', 0, 0),
		       ('usr_ann', 'acc_a', 'ann@x', 'ann@x', 'member', 'active', 0, 0),
		       ('usr_bob', 'acc_a', 'bob@x', 'bob@x', 'member', 'active', 0, 0)`)
	exec(`INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at) VALUES
		('fld_docs', 'acc_a', 'Docs', 'docs', 0, 0), ('fld_pics', 'acc_a', 'Pics', 'pics', 0, 0)`)
	exec(`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at, created_by)
		VALUES ('nod_docs', 'acc_a', 'fld_docs', NULL, '', '', 'dir', 0, 0, ''),
		       ('nod_pics', 'acc_a', 'fld_pics', NULL, '', '', 'dir', 0, 0, ''),
		       ('nod_d', 'acc_a', 'fld_docs', 'nod_docs', 'd', 'd', 'dir', 0, 0, 'usr_ann'),
		       ('nod_f', 'acc_a', 'fld_docs', 'nod_d', 'f.txt', 'f.txt', 'file', 0, 0, 'usr_ann')`)
	exec(`INSERT INTO grants (folder_id, user_id, level, created_at, created_by) VALUES
		('fld_docs', 'usr_ann', 'read', 0, 'usr_own'),
		('fld_pics', 'usr_ann', 'write', 0, 'usr_own'),
		('fld_docs', 'usr_bob', 'write', 0, 'usr_own')`)
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatalf("open at 0008: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if v, _ := s.SchemaVersion(ctx); v != len(migs) {
		t.Fatalf("schema version = %d, want %d", v, len(migs))
	}
	rank := func(node, user string) int {
		t.Helper()
		r, err := s.EffectiveRank(ctx, "acc_a", node, user)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if got := rank("nod_f", "usr_ann"); got != RoleRank(RoleViewer) {
		t.Errorf("ann on Docs after upgrade = rank %d, want viewer", got)
	}
	if got := rank("nod_pics", "usr_ann"); got != RoleRank(RoleEditor) {
		t.Errorf("ann on Pics after upgrade = rank %d, want editor", got)
	}
	if got := rank("nod_f", "usr_bob"); got != RoleRank(RoleEditor) {
		t.Errorf("bob on Docs after upgrade = rank %d, want editor", got)
	}
	if got := rank("nod_pics", "usr_bob"); got != 0 {
		t.Errorf("bob on Pics after upgrade = rank %d, want none", got)
	}

	var grantsLeft int
	if err := s.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE name = 'grants'").Scan(&grantsLeft); err != nil || grantsLeft != 0 {
		t.Errorf("grants table after upgrade = %d, %v; want dropped", grantsLeft, err)
	}
	for _, u := range []string{"usr_own", "usr_ann", "usr_bob"} {
		if pf, err := s.PersonalFolder(ctx, "acc_a", u); err != nil || pf.Kind != KindPersonal {
			t.Errorf("personal folder of %s = %+v, %v", u, pf, err)
		}
	}
	if n, err := s.Node(ctx, "acc_a", "nod_f"); err != nil || n.OwnerID != "usr_ann" {
		t.Errorf("owner of the file after upgrade = %+v, %v; want usr_ann", n, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// A second start backfills nothing: still one personal folder per user.
	s2, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if n := countRows(t, s2, "SELECT COUNT(*) FROM folders WHERE kind = 'personal'", ""); n != 3 {
		t.Errorf("personal folders after a second start = %d, want 3", n)
	}
}
