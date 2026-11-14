// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
)

// TestShareLinkPausedByCreatorMustChange: a link made by a user is paused while that user must change
// its password, in every place the live/paused rule is read — the rank queries (linkLiveSQL), the
// sharer's chain and the account list. Clearing the flag revives it with the visit it had. A link made
// by an account key, or by another user whose flag is clear, is not paused.
func TestShareLinkPausedByCreatorMustChange(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	a := newAccountRow(t, s, "A")
	creator := newUserRow(t, s, a, "creator@example.com", "")
	bob := newUserRow(t, s, a, "bob@example.com", "")
	carl := newUserRow(t, s, a, "carl@example.com", "")
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
	sub := insertDirRow(t, s, dir, "e")

	now := time.Now()
	mkLink := func(node *Node, by, kind string) *ShareLink {
		t.Helper()
		l := &ShareLink{ID: id.New("shl"), AccountID: a, NodeID: node.ID, Audience: AudienceOrganisation,
			Role: RoleCommenter, SecretHash: []byte("hash-" + node.ID), CredentialsAt: now, CreatedBy: by,
			CreatedKind: kind, CreatedAt: now, UpdatedBy: by, UpdatedKind: kind, UpdatedAt: now}
		if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertShareLink(ctx, l) }); err != nil {
			t.Fatal(err)
		}
		return l
	}
	link := mkLink(dir, creator.ID, CreatorUser)
	visit := func(l *ShareLink, at time.Time) {
		t.Helper()
		if err := s.Write(ctx, func(tx *Tx) error { return tx.RecordShareLinkVisit(ctx, l.ID, bob.ID, at) }); err != nil {
			t.Fatal(err)
		}
	}
	setFlag := func(u *User, on bool) {
		t.Helper()
		u.MustChangePassword, u.UpdatedAt = on, Now()
		if err := s.Write(ctx, func(tx *Tx) error { return tx.UpdateUser(ctx, u) }); err != nil {
			t.Fatal(err)
		}
	}
	rank := func() int {
		t.Helper()
		got, err := s.EffectiveRank(ctx, a, sub.ID, bob.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	chainPaused := func() bool {
		t.Helper()
		views, err := s.ChainShareLinks(ctx, a, sub.ID, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range views {
			if v.ID == link.ID {
				return v.Paused
			}
		}
		t.Fatalf("the chain does not list the link")
		return false
	}
	listPaused := func() bool {
		t.Helper()
		links, _, err := s.AccountShareLinks(ctx, a, time.Now(), AccountLinkFilter{}, Page{Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range links {
			if l.ID == link.ID {
				return l.Paused
			}
		}
		t.Fatalf("the account list does not list the link")
		return false
	}
	visit(link, now.Add(time.Millisecond))
	if got := rank(); got != RoleRank(RoleCommenter) {
		t.Fatalf("rank before the flag = %d, want commenter", got)
	}
	if chainPaused() || listPaused() {
		t.Fatal("the link is paused before the flag is set")
	}

	setFlag(creator, true)
	if got := rank(); got != 0 {
		t.Errorf("rank while the creator must change = %d, want 0", got)
	}
	if !chainPaused() {
		t.Error("the sharer's chain does not say paused")
	}
	if !listPaused() {
		t.Error("the account list does not say paused")
	}
	// The permission-only rank never counts a visit, flag or no flag.
	var perm int
	if err := s.Write(ctx, func(tx *Tx) error {
		var err error
		perm, err = tx.PermissionRank(ctx, a, sub.ID, bob.ID)
		return err
	}); err != nil || perm != 0 {
		t.Fatalf("permission-only rank = %d, %v; want 0", perm, err)
	}

	// Guards: an account key's link and another user's link stay live while the creator is flagged.
	keyDir := insertDirRow(t, s, root, "kd")
	keySub := insertDirRow(t, s, keyDir, "ke")
	keyLink := mkLink(keyDir, "usr_test", CreatorAccountKey)
	visit(keyLink, now.Add(time.Millisecond))
	carlDir := insertDirRow(t, s, root, "cd")
	carlSub := insertDirRow(t, s, carlDir, "ce")
	carlLink := mkLink(carlDir, carl.ID, CreatorUser)
	visit(carlLink, now.Add(time.Millisecond))
	for _, c := range []struct {
		node *Node
		want int
	}{{keySub, RoleRank(RoleCommenter)}, {carlSub, RoleRank(RoleCommenter)}} {
		got, err := s.EffectiveRank(ctx, a, c.node.ID, bob.ID)
		if err != nil || got != c.want {
			t.Errorf("rank on a key/other-user link while the creator is flagged = %d, %v; want %d", got, err, c.want)
		}
	}
	keyViews, err := s.ChainShareLinks(ctx, a, keySub.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range keyViews {
		if v.ID == keyLink.ID && v.Paused {
			t.Error("an account key's link is paused by a user's flag")
		}
	}

	setFlag(creator, false)
	if got := rank(); got != RoleRank(RoleCommenter) {
		t.Errorf("rank after the flag is cleared = %d, want commenter (the visit still counts)", got)
	}
	if chainPaused() || listPaused() {
		t.Error("the link is still paused after the flag is cleared")
	}
}
