// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
)

// extDir adds a directory below parentID and returns it.
func extDir(t *testing.T, s *Store, accountID, folderID, parentID, name, by string) *Node {
	t.Helper()
	n := &Node{ID: id.New("nod"), AccountID: accountID, FolderID: folderID, ParentID: parentID,
		Name: name, NameKey: name, Kind: KindDir, CreatedAt: Now(), ModifiedAt: Now(), CreatedBy: by, ModifiedBy: by}
	if err := s.Write(context.Background(), func(tx *Tx) error { return tx.InsertNode(context.Background(), n) }); err != nil {
		t.Fatal(err)
	}
	return n
}

// extGrant writes a grant of nodeID to email, as created, and returns it.
func extGrant(t *testing.T, s *Store, accountID, nodeID, email, by string, created time.Time, expires *time.Time) *ExternalPermission {
	t.Helper()
	p := &ExternalPermission{NodeID: nodeID, AccountID: accountID, Email: email, EmailKey: email,
		Role: RoleViewer, CreatedBy: by, CreatedAt: created, ExpiresAt: expires}
	if err := s.Write(context.Background(), func(tx *Tx) error { return tx.SetExternalPermission(context.Background(), p) }); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestExternalPermissionSetAndList: a grant is written once and updated in place; the update keeps
// who made it and when. The list is scoped to the account that owns the node.
func TestExternalPermissionSetAndList(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	b := newAccountRow(t, s, "B")
	u := newUserRow(t, s, a, "ann@example.com", "")
	f := newFolderRow(t, s, a, "Team", "team")
	root, err := s.RootNode(ctx, a, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	first := Now().Add(-time.Hour)
	extGrant(t, s, a, root.ID, "guest@other.example", u.ID, first, nil)
	list, err := s.ExternalPermissions(ctx, a, root.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v, %v; want one grant", list, err)
	}
	g := list[0]
	if g.NodeID != root.ID || g.AccountID != a || g.Email != "guest@other.example" || g.EmailKey != "guest@other.example" ||
		g.Role != RoleViewer || g.CreatedBy != u.ID || !g.CreatedAt.Equal(first) || g.ExpiresAt != nil {
		t.Fatalf("grant = %+v", g)
	}
	// The same address again updates the row in place, keeping its origin.
	exp := Now().Add(time.Hour)
	extGrant(t, s, a, root.ID, "guest@other.example", "key_other", Now(), &exp)
	list, err = s.ExternalPermissions(ctx, a, root.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list after update = %v, %v; want one grant", list, err)
	}
	g = list[0]
	if g.CreatedBy != u.ID || !g.CreatedAt.Equal(first) || g.ExpiresAt == nil || !g.ExpiresAt.Equal(exp) {
		t.Fatalf("updated grant = %+v", g)
	}
	// Another account sees nothing, even with the node's ID.
	list, err = s.ExternalPermissions(ctx, b, root.ID)
	if err != nil || len(list) != 0 {
		t.Fatalf("cross-account list = %v, %v; want none", list, err)
	}
}

// TestExternalPermissionRemove: a grant is removed by its address key, once.
func TestExternalPermissionRemove(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	u := newUserRow(t, s, a, "ann@example.com", "")
	f := newFolderRow(t, s, a, "Team", "team")
	root, err := s.RootNode(ctx, a, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	extGrant(t, s, a, root.ID, "guest@other.example", u.ID, Now(), nil)
	err = s.Write(ctx, func(tx *Tx) error {
		if err := tx.RemoveExternalPermission(ctx, root.ID, "guest@other.example"); err != nil {
			return err
		}
		if err := tx.RemoveExternalPermission(ctx, root.ID, "guest@other.example"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("second remove = %v, want ErrNotFound", err)
		}
		if err := tx.RemoveExternalPermission(ctx, root.ID, "nobody@other.example"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("remove of an absent grant = %v, want ErrNotFound", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if list, err := s.ExternalPermissions(ctx, a, root.ID); err != nil || len(list) != 0 {
		t.Fatalf("list after remove = %v, %v", list, err)
	}
}

// TestExternalPermissionCountAndCascade: the count is the node's own grants, and removing the node
// or its account removes them.
func TestExternalPermissionCountAndCascade(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	u := newUserRow(t, s, a, "ann@example.com", "")
	f := newFolderRow(t, s, a, "Team", "team")
	root, err := s.RootNode(ctx, a, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	child := extDir(t, s, a, f.ID, root.ID, "sub", u.ID)
	extGrant(t, s, a, root.ID, "one@other.example", u.ID, Now(), nil)
	extGrant(t, s, a, root.ID, "two@other.example", u.ID, Now(), nil)
	extGrant(t, s, a, child.ID, "three@other.example", u.ID, Now(), nil)
	if n, err := s.CountExternalPermissions(ctx, root.ID); err != nil || n != 2 {
		t.Fatalf("count on the root = %d, %v; want 2", n, err)
	}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteFolder(ctx, a, f.ID) }); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, "SELECT COUNT(*) FROM external_permissions WHERE account_id = ?", a); n != 0 {
		t.Fatalf("grants after the folder's removal = %d, want 0", n)
	}
	// Removing the account takes the grants of its other nodes too.
	f2 := newFolderRow(t, s, a, "Two", "two")
	root2, err := s.RootNode(ctx, a, f2.ID)
	if err != nil {
		t.Fatal(err)
	}
	extGrant(t, s, a, root2.ID, "four@other.example", u.ID, Now(), nil)
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteAccount(ctx, a) }); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, "SELECT COUNT(*) FROM external_permissions WHERE account_id = ?", a); n != 0 {
		t.Fatalf("grants after the account's removal = %d, want 0", n)
	}
}

// TestExternalSharingSwitch: the switch is off by default and changes only when set.
func TestExternalSharingSwitch(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	on, err := s.ExternalSharing(ctx, a)
	if err != nil || on {
		t.Fatalf("new account's switch = %v, %v; want off", on, err)
	}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.SetExternalSharing(ctx, a, true, Now()) }); err != nil {
		t.Fatal(err)
	}
	if on, err := s.ExternalSharing(ctx, a); err != nil || !on {
		t.Fatalf("switch after setting = %v, %v; want on", on, err)
	}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.SetExternalSharing(ctx, a, false, Now()) }); err != nil {
		t.Fatal(err)
	}
	if on, err := s.ExternalSharing(ctx, a); err != nil || on {
		t.Fatalf("switch after clearing = %v, %v; want off", on, err)
	}
	if _, err := s.ExternalSharing(ctx, "acc_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("switch of a missing account = %v, want ErrNotFound", err)
	}
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.SetExternalSharing(ctx, "acc_missing", true, Now())
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("set of a missing account = %v, want ErrNotFound", err)
	}
}

// TestNodeAccountID: a node names its account, and a missing node is ErrNotFound.
func TestNodeAccountID(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	f := newFolderRow(t, s, a, "Team", "team")
	root, err := s.RootNode(ctx, a, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if acc, err := s.NodeAccountID(ctx, root.ID); err != nil || acc != a {
		t.Fatalf("node's account = %q, %v; want %q", acc, err, a)
	}
	if _, err := s.NodeAccountID(ctx, "nod_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing node = %v, want ErrNotFound", err)
	}
}

// TestExternalGrantRoot: the reach of a grant walks down from the node it sits on, for the granted
// address only, and the topmost live grant of that address wins. An expired grant reaches nothing.
func TestExternalGrantRoot(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	b := newAccountRow(t, s, "B")
	u := newUserRow(t, s, a, "ann@example.com", "")
	f := newFolderRow(t, s, a, "Team", "team")
	root, err := s.RootNode(ctx, a, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	child := extDir(t, s, a, f.ID, root.ID, "sub", u.ID)
	grand := extDir(t, s, a, f.ID, child.ID, "deep", u.ID)
	now := Now()
	if _, err := s.ExternalGrantRoot(ctx, a, grand.ID, "reader@x", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ungranted chain = %v, want ErrNotFound", err)
	}
	// Two addresses hold grants on the same chain: each reads only its own.
	extGrant(t, s, a, root.ID, "other@x", u.ID, now, nil)
	extGrant(t, s, a, child.ID, "reader@x", u.ID, now, nil)
	if g, err := s.ExternalGrantRoot(ctx, a, grand.ID, "reader@x", now); err != nil || g.NodeID != child.ID {
		t.Fatalf("reader's grant root = %+v, %v; want the child's grant", g, err)
	}
	if g, err := s.ExternalGrantRoot(ctx, a, grand.ID, "other@x", now); err != nil || g.NodeID != root.ID {
		t.Fatalf("other's grant root = %+v, %v; want the root's grant", g, err)
	}
	if _, err := s.ExternalGrantRoot(ctx, a, grand.ID, "third@x", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("third address = %v, want ErrNotFound", err)
	}
	// A live grant of the reader above wins: the reach starts at the topmost one.
	extGrant(t, s, a, root.ID, "reader@x", u.ID, now, nil)
	if g, err := s.ExternalGrantRoot(ctx, a, grand.ID, "reader@x", now); err != nil || g.NodeID != root.ID {
		t.Fatalf("grant root with two live grants = %+v, %v; want the root's", g, err)
	}
	// An expired grant of the reader above the live one leaves the live one.
	past := now.Add(-time.Hour)
	extGrant(t, s, a, root.ID, "reader@x", u.ID, now, &past)
	if g, err := s.ExternalGrantRoot(ctx, a, grand.ID, "reader@x", now); err != nil || g.NodeID != child.ID {
		t.Fatalf("grant root under an expired grant = %+v, %v; want the child's", g, err)
	}
	// The node of another account is not reached.
	if _, err := s.ExternalGrantRoot(ctx, b, grand.ID, "reader@x", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account chain = %v, want ErrNotFound", err)
	}
}

// TestExternalGrantRootBounds: the walk stays inside the account and stops at 1000 levels, so a
// grant reaches through neither a parent of another account nor an endless chain.
func TestExternalGrantRootBounds(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	b := newAccountRow(t, s, "B")
	u := newUserRow(t, s, a, "ann@example.com", "")
	fa := newFolderRow(t, s, a, "Team", "team")
	fb := newFolderRow(t, s, b, "Other", "other")
	rootB, err := s.RootNode(ctx, b, fb.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := Now()
	extGrant(t, s, b, rootB.ID, "reader@x", u.ID, now, nil)
	// A node of A whose parent sits in B: the walk must not cross the account.
	cross := &Node{ID: id.New("nod"), AccountID: a, FolderID: fa.ID, ParentID: rootB.ID,
		Name: "cross", NameKey: "cross", Kind: KindDir, CreatedAt: now, ModifiedAt: now, CreatedBy: u.ID, ModifiedBy: u.ID}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertNode(ctx, cross) }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExternalGrantRoot(ctx, a, cross.ID, "reader@x", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the walk crossed accounts: %v, want ErrNotFound", err)
	}
	// A chain of 1002 nodes: a grant 1000 levels up is reached, one 1001 levels up is not.
	chain := make([]*Node, 1002)
	err = s.Write(ctx, func(tx *Tx) error {
		root, err := tx.RootNode(ctx, a, fa.ID)
		if err != nil {
			return err
		}
		parent := root.ID
		for i := 1001; i >= 0; i-- {
			name := fmt.Sprintf("d%d", i)
			n := &Node{ID: id.New("nod"), AccountID: a, FolderID: fa.ID, ParentID: parent,
				Name: name, NameKey: name, Kind: KindDir, CreatedAt: now, ModifiedAt: now, CreatedBy: u.ID, ModifiedBy: u.ID}
			if err := tx.InsertNode(ctx, n); err != nil {
				return err
			}
			chain[i] = n
			parent = n.ID
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	extGrant(t, s, a, chain[1000].ID, "at1000@x", u.ID, now, nil)
	extGrant(t, s, a, chain[1001].ID, "at1001@x", u.ID, now, nil)
	if g, err := s.ExternalGrantRoot(ctx, a, chain[0].ID, "at1000@x", now); err != nil || g.NodeID != chain[1000].ID {
		t.Fatalf("grant at depth 1000 = %+v, %v; want it found", g, err)
	}
	if _, err := s.ExternalGrantRoot(ctx, a, chain[0].ID, "at1001@x", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("grant at depth 1001 = %v, want ErrNotFound", err)
	}
}

// TestExternalSharedWith: the list holds the live grants of other accounts whose switch is on, newest
// first, with the sharer's name. The caller's own account, a switched-off or suspended account, and an
// expired grant are left out.
func TestExternalSharedWith(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "Sharer")
	b := newAccountRow(t, s, "Recipient")
	sharer := newUserRow(t, s, a, "sharer@example.com", "")
	sharer.DisplayName = "The Sharer"
	if err := s.Write(ctx, func(tx *Tx) error { return tx.UpdateUser(ctx, sharer) }); err != nil {
		t.Fatal(err)
	}
	newUserRow(t, s, b, "guest@example.com", "")
	f := newFolderRow(t, s, a, "Team", "team")
	root, err := s.RootNode(ctx, a, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	older := Now().Add(-time.Hour)
	extGrant(t, s, a, root.ID, "guest@example.com", sharer.ID, older, nil)
	// The sharer's switch is off: nothing is shared yet.
	if list, err := s.ExternalSharedWith(ctx, b, "guest@example.com", 10); err != nil || len(list) != 0 {
		t.Fatalf("list with the switch off = %v, %v", list, err)
	}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.SetExternalSharing(ctx, a, true, Now()) }); err != nil {
		t.Fatal(err)
	}
	list, err := s.ExternalSharedWith(ctx, b, "guest@example.com", 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v, %v; want one", list, err)
	}
	sh := list[0]
	if sh.Node == nil || sh.Node.ID != root.ID || sh.Permission == nil || sh.Permission.Email != "guest@example.com" ||
		sh.AccountName != "Sharer" || sh.SharedByName != "The Sharer" {
		t.Fatalf("shared item = %+v", sh)
	}
	// The caller's own account is never in its list, even if a grant names its address.
	extGrant(t, s, a, root.ID, "own@example.com", sharer.ID, Now(), nil)
	if list, err := s.ExternalSharedWith(ctx, a, "own@example.com", 10); err != nil || len(list) != 0 {
		t.Fatalf("own account's list = %v, %v; want none", list, err)
	}
	// A key that made the grant has no display name.
	child := extDir(t, s, a, f.ID, root.ID, "sub", sharer.ID)
	extGrant(t, s, a, child.ID, "guest@example.com", "key_abc", Now(), nil)
	list, err = s.ExternalSharedWith(ctx, b, "guest@example.com", 10)
	if err != nil || len(list) != 2 {
		t.Fatalf("list with two grants = %v, %v", list, err)
	}
	if list[0].Node.ID != child.ID || list[0].SharedByName != "" {
		t.Fatalf("newest first with a key's name = %+v", list[0])
	}
	if list[1].Node.ID != root.ID {
		t.Fatalf("order = %+v", list)
	}
	if list, err := s.ExternalSharedWith(ctx, b, "guest@example.com", 1); err != nil || len(list) != 1 {
		t.Fatalf("limited list = %v, %v; want one", list, err)
	}
	// An expired grant is left out.
	past := Now().Add(-time.Hour)
	extGrant(t, s, a, child.ID, "guest@example.com", sharer.ID, Now(), &past)
	if list, err := s.ExternalSharedWith(ctx, b, "guest@example.com", 10); err != nil || len(list) != 1 {
		t.Fatalf("list with an expired grant = %v, %v; want one", list, err)
	}
	// A suspended account shares nothing.
	acc, err := s.Account(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	acc.Status = AccountSuspended
	if err := s.Write(ctx, func(tx *Tx) error { return tx.UpdateAccount(ctx, acc) }); err != nil {
		t.Fatal(err)
	}
	if list, err := s.ExternalSharedWith(ctx, b, "guest@example.com", 10); err != nil || len(list) != 0 {
		t.Fatalf("list of a suspended account = %v, %v", list, err)
	}
}
