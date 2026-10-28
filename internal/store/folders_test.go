package store

import (
	"context"
	"errors"
	"testing"

	"selfcloud/internal/id"
)

// newFolderRow adds a folder to accountID and returns it.
func newFolderRow(t *testing.T, s *Store, accountID, name, nameKey string) *Folder {
	t.Helper()
	now := Now()
	f := &Folder{ID: id.New("fld"), AccountID: accountID, Name: name, NameKey: nameKey, CreatedAt: now, UpdatedAt: now}
	if err := s.Write(context.Background(), func(tx *Tx) error {
		return tx.InsertFolder(context.Background(), f, "usr_test")
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

// countRows counts rows of a query that takes one ID argument.
func countRows(t *testing.T, s *Store, query, arg string) int {
	t.Helper()
	var n int
	if err := s.r.QueryRowContext(context.Background(), query, arg).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestFolderRootAndNameConflicts(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	b := newAccountRow(t, s, "B")
	f := newFolderRow(t, s, a, "Reports", "reports")

	// Creating a folder creates its root: one dir node with no parent and the empty name.
	if n := countRows(t, s, "SELECT COUNT(*) FROM nodes WHERE folder_id = ? AND parent_id IS NULL", f.ID); n != 1 {
		t.Fatalf("roots of the folder = %d, want 1", n)
	}

	// The same name key in one account conflicts; in another account it does not.
	dup := &Folder{ID: id.New("fld"), AccountID: a, Name: "REPORTS", NameKey: "reports", CreatedAt: Now(), UpdatedAt: Now()}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertFolder(ctx, dup, "") }); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate name = %v, want ErrConflict", err)
	}
	newFolderRow(t, s, b, "Reports", "reports")

	got, err := s.Folder(ctx, b, f.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("folder of A read through B = %v, %v", got, err)
	}
}

func TestDeleteFolderCascadesAndAdjustsUsage(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "A")
	usr := newUserRow(t, s, acc, "ann@example.com", "")
	f := newFolderRow(t, s, acc, "Docs", "docs")
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.SetGrant(ctx, &Grant{FolderID: f.ID, UserID: usr.ID, Level: LevelRead, CreatedAt: Now(), CreatedBy: "x"})
	}); err != nil {
		t.Fatal(err)
	}
	// Two files of 100 and 50 bytes count against the account; the folder's usage goes with them.
	err := s.Write(ctx, func(tx *Tx) error {
		if _, err := tx.q.ExecContext(ctx, `INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key,
			kind, size, created_at, modified_at) SELECT ?, ?, ?, id, 'a', 'a', 'file', 100, 0, 0 FROM nodes
			WHERE folder_id = ? AND parent_id IS NULL`, id.New("nod"), acc, f.ID, f.ID); err != nil {
			return err
		}
		if _, err := tx.q.ExecContext(ctx, `INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key,
			kind, size, created_at, modified_at) SELECT ?, ?, ?, id, 'b', 'b', 'file', 50, 0, 0 FROM nodes
			WHERE folder_id = ? AND parent_id IS NULL`, id.New("nod"), acc, f.ID, f.ID); err != nil {
			return err
		}
		return tx.AdjustUsage(ctx, acc, UsageChange{Bytes: 150, Files: 2})
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteFolder(ctx, acc, f.ID) }); err != nil {
		t.Fatal(err)
	}
	a, _ := s.Account(ctx, acc)
	if a.UsedBytes != 0 || a.UsedFiles != 0 {
		t.Errorf("usage after delete = %d bytes, %d files; want 0, 0", a.UsedBytes, a.UsedFiles)
	}
	if n := countRows(t, s, "SELECT COUNT(*) FROM nodes WHERE folder_id = ?", f.ID); n != 0 {
		t.Errorf("nodes left = %d", n)
	}
	if grants, _ := s.GrantsOfFolder(ctx, f.ID); len(grants) != 0 {
		t.Errorf("grants left = %v", grants)
	}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteFolder(ctx, acc, f.ID) }); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete = %v, want ErrNotFound", err)
	}
}

func TestGrantsAndUserDeletion(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	b := newAccountRow(t, s, "B")
	ann := newUserRow(t, s, a, "ann@example.com", "")
	f1 := newFolderRow(t, s, a, "One", "one")
	f2 := newFolderRow(t, s, a, "Two", "two")
	other := newFolderRow(t, s, b, "Other", "other")

	set := func(folder, level string) error {
		return s.Write(ctx, func(tx *Tx) error {
			return tx.SetGrant(ctx, &Grant{FolderID: folder, UserID: ann.ID, Level: level, CreatedAt: Now(), CreatedBy: "k"})
		})
	}
	if err := set(f1.ID, LevelRead); err != nil {
		t.Fatal(err)
	}
	if err := set(f2.ID, LevelWrite); err != nil {
		t.Fatal(err)
	}
	// Setting again changes the level rather than adding a second grant.
	if err := set(f1.ID, LevelWrite); err != nil {
		t.Fatal(err)
	}
	g, err := s.Grant(ctx, f1.ID, ann.ID)
	if err != nil || g.Level != LevelWrite {
		t.Fatalf("grant = %+v, %v", g, err)
	}
	grants, _ := s.GrantsOfUser(ctx, a, ann.ID)
	if len(grants) != 2 {
		t.Fatalf("grants of user = %d, want 2", len(grants))
	}
	// Account B sees none of them.
	if none, _ := s.GrantsOfUser(ctx, b, ann.ID); len(none) != 0 {
		t.Errorf("grants of the user under B = %v", none)
	}
	// A grant on a folder of another account is refused by the join, not by trusting the caller.
	if err := set(other.ID, LevelRead); err != nil {
		t.Fatal(err)
	}
	if n, _, _ := s.GrantedFolders(ctx, a, ann.ID, Page{Limit: 50}); len(n) != 2 {
		t.Errorf("granted folders under A = %d, want 2", len(n))
	}

	// Deleting the user cascades its grants.
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteUser(ctx, a, ann.ID) }); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.GrantsOfFolder(ctx, f1.ID); len(g) != 0 {
		t.Errorf("grants after user delete = %v", g)
	}
}

func TestDeleteGrant(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	ann := newUserRow(t, s, a, "ann@example.com", "")
	f := newFolderRow(t, s, a, "One", "one")
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.SetGrant(ctx, &Grant{FolderID: f.ID, UserID: ann.ID, Level: LevelRead, CreatedAt: Now()})
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteGrant(ctx, f.ID, ann.ID) }); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteGrant(ctx, f.ID, ann.ID) }); !errors.Is(err, ErrNotFound) {
		t.Errorf("second revoke = %v, want ErrNotFound", err)
	}
}
