// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
)

// insertDirRow adds a directory under parent, through the store, for the rank tests.
func insertDirRow(t *testing.T, s *Store, parent *Node, name string) *Node {
	t.Helper()
	n := &Node{ID: id.New("nod"), AccountID: parent.AccountID, FolderID: parent.FolderID, ParentID: parent.ID,
		Name: name, NameKey: name, Kind: KindDir, CreatedAt: Now(), ModifiedAt: Now(), CreatedBy: "usr_test",
		ModifiedBy: "usr_test", ETag: "e"}
	if err := s.Write(context.Background(), func(tx *Tx) error { return tx.InsertNode(context.Background(), n) }); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestShareLinkRanksCountOnlyWhileLive: a visit to an organisation link gives its role on the node and
// below, in the rank queries, only while the link is live. Each way the link stops (policy, expiry, a
// reset after the visit, deletion) takes the role away at once, and the permission-only rank never
// counts it.
func TestShareLinkRanksCountOnlyWhileLive(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	a := newAccountRow(t, s, "A")
	ann := newUserRow(t, s, a, "ann@example.com", "")
	f := newFolderRow(t, s, a, "One", "one")
	root, err := s.RootNode(ctx, a, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	setPolicy := func(allow, membersOnly bool) {
		t.Helper()
		f.AllowLinks, f.MembersOnly, f.UpdatedAt = allow, membersOnly, Now()
		if err := s.Write(ctx, func(tx *Tx) error { return tx.UpdateFolderSettings(ctx, f) }); err != nil {
			t.Fatal(err)
		}
	}
	setPolicy(true, false)
	dir := insertDirRow(t, s, root, "d")
	sub := insertDirRow(t, s, dir, "e")

	now := time.Now()
	link := &ShareLink{ID: id.New("shl"), AccountID: a, NodeID: dir.ID, Audience: AudienceOrganisation,
		Role: RoleCommenter, SecretHash: []byte("hash-1"), CredentialsAt: now, CreatedBy: "usr_test",
		CreatedKind: CreatorAccountKey, CreatedAt: now, UpdatedBy: "usr_test", UpdatedKind: CreatorAccountKey, UpdatedAt: now}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertShareLink(ctx, link) }); err != nil {
		t.Fatal(err)
	}
	visit := func(at time.Time) {
		t.Helper()
		if err := s.Write(ctx, func(tx *Tx) error { return tx.RecordShareLinkVisit(ctx, link.ID, ann.ID, at) }); err != nil {
			t.Fatal(err)
		}
	}
	rankOf := func(label string, want int) {
		t.Helper()
		got, err := s.EffectiveRank(ctx, a, sub.ID, ann.ID)
		if err != nil || got != want {
			t.Fatalf("%s: rank on the sub-directory = %d, %v; want %d", label, got, err, want)
		}
		// The link sits on dir, so dir's own rank is its child's rank in the root's listing; the
		// sub-directory holds nothing of its own, so the listing of dir has no child rank for it.
		under, err := s.ChildRanks(ctx, a, dir.ID, ann.ID)
		if err != nil || len(under) != 0 {
			t.Fatalf("%s: child ranks under dir = %v, %v; want none", label, under, err)
		}
		kids, err := s.ChildRanks(ctx, a, root.ID, ann.ID)
		if err != nil {
			t.Fatal(err)
		}
		shared, _, err := s.SharedNodes(ctx, a, ann.ID, false, false, Page{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if want == 0 {
			if len(kids) != 0 || len(shared) != 0 {
				t.Fatalf("%s: child ranks %v and shared %v; want none", label, kids, shared)
			}
		} else {
			if kids[dir.ID] != want {
				t.Fatalf("%s: child rank of the link node in the root listing = %d; want %d", label, kids[dir.ID], want)
			}
			if len(shared) != 1 || shared[0].NodeID != dir.ID || shared[0].Rank != want {
				t.Fatalf("%s: shared = %v; want the link node at rank %d", label, shared, want)
			}
		}
		var perm int
		if err := s.Write(ctx, func(tx *Tx) error {
			var err error
			perm, err = tx.PermissionRank(ctx, a, sub.ID, ann.ID)
			return err
		}); err != nil || perm != 0 {
			t.Fatalf("%s: permission-only rank = %d, %v; want 0 (a visit is no permission)", label, perm, err)
		}
	}

	rankOf("before any visit", 0)
	visit(now.Add(time.Millisecond)) // a visit must come after the credentials it uses
	rankOf("after a visit", RoleRank(RoleCommenter))

	setPolicy(false, false)
	rankOf("with allow_links off", 0)
	setPolicy(true, false)
	rankOf("with allow_links back on (revived)", RoleRank(RoleCommenter))

	setPolicy(true, true)
	rankOf("with members_only below the root", 0)
	setPolicy(true, false)
	rankOf("members_only off again", RoleRank(RoleCommenter))

	past := now.Add(-time.Minute)
	link.ExpiresAt = &past
	if err := s.Write(ctx, func(tx *Tx) error { return tx.UpdateShareLink(ctx, link) }); err != nil {
		t.Fatal(err)
	}
	rankOf("expired", 0)
	link.ExpiresAt = nil

	// A reset: the visit made before the new credentials no longer counts, until the member visits again.
	later := now.Add(time.Second)
	link.CredentialsAt, link.SecretHash = later, []byte("hash-2")
	if err := s.Write(ctx, func(tx *Tx) error { return tx.UpdateShareLink(ctx, link) }); err != nil {
		t.Fatal(err)
	}
	rankOf("reset after the visit", 0)
	visit(later.Add(time.Second))
	rankOf("visit after the reset", RoleRank(RoleCommenter))

	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteShareLink(ctx, a, link.ID) }); err != nil {
		t.Fatal(err)
	}
	rankOf("deleted", 0)
}

// TestShareLinkVisitAtCredentialsIsNotLive: a visit stamped exactly when the link's credentials were made does
// not count, since the reset that made them may have been the same instant. A visit one millisecond later does.
func TestShareLinkVisitAtCredentialsIsNotLive(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	a := newAccountRow(t, s, "A")
	ann := newUserRow(t, s, a, "ann@example.com", "")
	f := newFolderRow(t, s, a, "One", "one")
	root, err := s.RootNode(ctx, a, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.AllowLinks, f.UpdatedAt = true, Now()
	if err := s.Write(ctx, func(tx *Tx) error { return tx.UpdateFolderSettings(ctx, f) }); err != nil {
		t.Fatal(err)
	}
	dir := insertDirRow(t, s, root, "d")
	at := time.UnixMilli(time.Now().UnixMilli()).UTC()
	link := &ShareLink{ID: id.New("shl"), AccountID: a, NodeID: dir.ID, Audience: AudienceOrganisation,
		Role: RoleCommenter, SecretHash: []byte("hash-cred"), CredentialsAt: at, CreatedBy: "usr_test",
		CreatedKind: CreatorAccountKey, CreatedAt: at, UpdatedBy: "usr_test", UpdatedKind: CreatorAccountKey, UpdatedAt: at}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertShareLink(ctx, link) }); err != nil {
		t.Fatal(err)
	}
	visit := func(when time.Time) {
		t.Helper()
		if err := s.Write(ctx, func(tx *Tx) error { return tx.RecordShareLinkVisit(ctx, link.ID, ann.ID, when) }); err != nil {
			t.Fatal(err)
		}
	}
	rank := func() int {
		t.Helper()
		got, err := s.EffectiveRank(ctx, a, dir.ID, ann.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	visit(at)
	if got := rank(); got != 0 {
		t.Errorf("rank after a visit at the credentials' own instant = %d, want 0", got)
	}
	visit(at.Add(time.Millisecond))
	if got := rank(); got != RoleRank(RoleCommenter) {
		t.Errorf("rank after a visit one millisecond later = %d, want commenter", got)
	}
}
