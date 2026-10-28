// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
	"time"
)

// TestExpiredPermissionGrantsNothing: a permission whose expiry has passed is invisible to every
// read, while a live one still counts. The API sets no expiry yet (slice n2), so the row is written
// here directly.
func TestExpiredPermissionGrantsNothing(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	ann := newUserRow(t, s, a, "ann@example.com", "")
	f := newFolderRow(t, s, a, "One", "one")
	root, err := s.RootNode(ctx, a, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	put := func(role string, expires *time.Time) {
		t.Helper()
		if err := s.Write(ctx, func(tx *Tx) error {
			return tx.SetPermission(ctx, &Permission{NodeID: root.ID, AccountID: a, SubjectKind: SubjectUser,
				SubjectID: ann.ID, Role: role, CreatedAt: Now(), CreatedBy: "k", ExpiresAt: expires})
		}); err != nil {
			t.Fatal(err)
		}
	}

	past := time.Now().Add(-time.Hour)
	put(RoleEditor, &past)
	if rank, err := s.EffectiveRank(ctx, a, root.ID, ann.ID); err != nil || rank != 0 {
		t.Errorf("rank under an expired permission = %d, %v; want 0", rank, err)
	}
	if perms, err := s.ChainPermissions(ctx, a, root.ID, ""); err != nil || len(perms) != 0 {
		t.Errorf("chain permissions = %v, %v; want none", perms, err)
	}
	if reached, _, _ := s.RootReachedFolders(ctx, a, ann.ID, HideFilter{}, Page{Limit: 10}); len(reached) != 0 {
		t.Errorf("folders reached under an expired permission = %d", len(reached))
	}
	if shared, _, _ := s.SharedNodes(ctx, a, ann.ID, false, Page{Limit: 10}); len(shared) != 0 {
		t.Errorf("shared under an expired permission = %v", shared)
	}

	// Granting again with a live expiry replaces the expired row, and then the role counts.
	future := time.Now().Add(time.Hour)
	put(RoleViewer, &future)
	if rank, err := s.EffectiveRank(ctx, a, root.ID, ann.ID); err != nil || rank != RoleRank(RoleViewer) {
		t.Errorf("rank under a live permission = %d, %v; want viewer", rank, err)
	}
}

// TestPersonalFolderNodesBelongToItsUser: a node made inside a personal folder is owned by the
// folder's user, whoever made it; elsewhere the owner is the creator.
func TestPersonalFolderNodesBelongToItsUser(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	ann := newUserRow(t, s, a, "ann@example.com", "")
	bob := newUserRow(t, s, a, "bob@example.com", "")
	pf := &Folder{ID: "fld_personal1", AccountID: a, Name: PersonalFolderName, NameKey: PersonalNameKey(ann.ID),
		Kind: KindPersonal, OwnerUserID: ann.ID, CreatedAt: Now(), UpdatedAt: Now()}
	team := newFolderRow(t, s, a, "Team", "team")
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertFolder(ctx, pf, ann.ID) }); err != nil {
		t.Fatal(err)
	}
	insert := func(folder, name string) string {
		t.Helper()
		root, err := s.RootNode(ctx, a, folder)
		if err != nil {
			t.Fatal(err)
		}
		n := &Node{ID: "nod_" + name, AccountID: a, FolderID: folder, ParentID: root.ID, Name: name, NameKey: name,
			Kind: KindFile, CreatedAt: Now(), ModifiedAt: Now(), CreatedBy: bob.ID, ModifiedBy: bob.ID}
		if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertNode(ctx, n) }); err != nil {
			t.Fatal(err)
		}
		got, err := s.Node(ctx, a, n.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.OwnerID
	}
	if owner := insert(pf.ID, "mine"); owner != ann.ID {
		t.Errorf("owner in a personal folder = %q, want %q", owner, ann.ID)
	}
	if owner := insert(team.ID, "shared"); owner != bob.ID {
		t.Errorf("owner in a team space = %q, want the creator %q", owner, bob.ID)
	}
}

// TestUpdateNodeKeepsOwnerWhenUnset: a writer that sends no owner leaves the node's owner as it is.
// Only a writer that names an owner changes it.
func TestUpdateNodeKeepsOwnerWhenUnset(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	ann := newUserRow(t, s, a, "ann@example.com", "")
	f := newFolderRow(t, s, a, "Team", "team")
	root, err := s.RootNode(ctx, a, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	n := &Node{ID: "nod_owned1", AccountID: a, FolderID: f.ID, ParentID: root.ID, Name: "f.txt", NameKey: "f.txt",
		Kind: KindFile, CreatedAt: Now(), ModifiedAt: Now(), CreatedBy: ann.ID, ModifiedBy: ann.ID, OwnerID: ann.ID}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertNode(ctx, n) }); err != nil {
		t.Fatal(err)
	}
	n.Name, n.NameKey, n.OwnerID = "g.txt", "g.txt", ""
	if err := s.Write(ctx, func(tx *Tx) error { return tx.UpdateNode(ctx, n) }); err != nil {
		t.Fatal(err)
	}
	got, err := s.Node(ctx, a, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.OwnerID != ann.ID {
		t.Errorf("owner after an update that named none = %q, want %q", got.OwnerID, ann.ID)
	}
}
