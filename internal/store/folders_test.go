// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
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

// setRootPermission gives a user a role on a folder's root, the way the grants routes do.
func setRootPermission(t *testing.T, s *Store, accountID, folderID, userID, role string) {
	t.Helper()
	ctx := context.Background()
	if err := s.Write(ctx, func(tx *Tx) error {
		root, err := tx.RootNode(ctx, accountID, folderID)
		if err != nil {
			return err
		}
		return tx.SetPermission(ctx, &Permission{NodeID: root.ID, AccountID: accountID, SubjectKind: SubjectUser,
			SubjectID: userID, Role: role, CreatedAt: Now(), CreatedBy: "x"})
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteFolderCascadesAndAdjustsUsage(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "A")
	usr := newUserRow(t, s, acc, "ann@example.com", "")
	f := newFolderRow(t, s, acc, "Docs", "docs")
	setRootPermission(t, s, acc, f.ID, usr.ID, RoleViewer)
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
	// The permissions sat on nodes of the folder, so they went with its nodes.
	if n := countRows(t, s, "SELECT COUNT(*) FROM permissions WHERE subject_id = ?", usr.ID); n != 0 {
		t.Errorf("permissions left = %d", n)
	}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteFolder(ctx, acc, f.ID) }); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete = %v, want ErrNotFound", err)
	}
}

func TestRootPermissionsAndUserDeletion(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	b := newAccountRow(t, s, "B")
	ann := newUserRow(t, s, a, "ann@example.com", "")
	f1 := newFolderRow(t, s, a, "One", "one")
	f2 := newFolderRow(t, s, a, "Two", "two")
	other := newFolderRow(t, s, b, "Other", "other")

	setRootPermission(t, s, a, f1.ID, ann.ID, RoleViewer)
	setRootPermission(t, s, a, f2.ID, ann.ID, RoleEditor)
	// Setting again changes the role rather than adding a second permission.
	setRootPermission(t, s, a, f1.ID, ann.ID, RoleEditor)
	grants, err := s.UserRootGrants(ctx, a, ann.ID)
	if err != nil || len(grants) != 2 {
		t.Fatalf("root grants of user = %+v, %v; want 2", grants, err)
	}
	if grants[0].FolderID != f1.ID || grants[0].Role != RoleEditor {
		t.Errorf("first grant = %+v, want folder one at editor", grants[0])
	}
	// Account B sees none of them.
	if none, _ := s.UserRootGrants(ctx, b, ann.ID); len(none) != 0 {
		t.Errorf("root grants of the user under B = %v", none)
	}
	// A permission on a folder of another account is refused by the join, not by trusting the caller.
	setRootPermission(t, s, b, other.ID, ann.ID, RoleViewer)
	if reached, _, _ := s.RootReachedFolders(ctx, a, ann.ID, Page{Limit: 50}); len(reached) != 2 {
		t.Errorf("folders reached under A = %d, want 2", len(reached))
	}

	// Deleting the user removes its permissions, as the API does in the same transaction.
	if err := s.Write(ctx, func(tx *Tx) error {
		if _, err := tx.DeleteSubjectPermissions(ctx, a, SubjectUser, ann.ID); err != nil {
			return err
		}
		return tx.DeleteUser(ctx, a, ann.ID)
	}); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.UserRootGrants(ctx, a, ann.ID); len(g) != 0 {
		t.Errorf("root grants after user delete = %v", g)
	}
}

func TestDeletePermission(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	ann := newUserRow(t, s, a, "ann@example.com", "")
	f := newFolderRow(t, s, a, "One", "one")
	setRootPermission(t, s, a, f.ID, ann.ID, RoleViewer)
	if err := s.Write(ctx, func(tx *Tx) error {
		root, err := tx.RootNode(ctx, a, f.ID)
		if err != nil {
			return err
		}
		return tx.DeletePermission(ctx, root.ID, SubjectUser, ann.ID)
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx *Tx) error {
		root, err := tx.RootNode(ctx, a, f.ID)
		if err != nil {
			return err
		}
		return tx.DeletePermission(ctx, root.ID, SubjectUser, ann.ID)
	}); !errors.Is(err, ErrNotFound) {
		t.Errorf("second revoke = %v, want ErrNotFound", err)
	}
}
