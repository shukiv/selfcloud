// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
)

// TestInsertSessionRecordsLastSignIn checks that a session of the user's own records when the user last signed in, on the
// user, where it outlives the session; a sign-in-as (a session with an impersonator) does not, since the user did not sign
// in. A user who never signed in has none.
func TestInsertSessionRecordsLastSignIn(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	u := newUserRow(t, s, a, "ann@example.com", "")
	now := Now()
	admin := &User{ID: id.New("usr"), Email: "root@example.com", EmailKey: "root@example.com", Locale: "en", Role: RoleAdmin,
		Status: UserActive, CreatedAt: now, UpdatedAt: now}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertUser(ctx, admin) }); err != nil {
		t.Fatal(err)
	}

	lastOf := func() *time.Time {
		t.Helper()
		got, err := s.User(ctx, a, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.LastSignInAt
	}
	if got := lastOf(); got != nil {
		t.Fatalf("a user who never signed in has last sign-in %v, want none", got)
	}

	own := now.Add(-time.Hour)
	sess := &Session{IDHash: []byte("own-session-hash-0123456789012345"), UserID: u.ID, AccountID: a, CSRFToken: "c",
		CreatedAt: own, LastSeenAt: own, ExpiresAt: own.Add(2 * time.Hour)}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertSession(ctx, sess) }); err != nil {
		t.Fatal(err)
	}
	if got := lastOf(); got == nil || !got.Equal(own) {
		t.Fatalf("after the user's own session, last sign-in = %v, want %v", got, own)
	}

	imp := &Session{IDHash: []byte("imp-session-hash-0123456789012345"), UserID: u.ID, AccountID: a, CSRFToken: "c",
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour), ImpersonatorID: admin.ID,
		ImpersonatorSessionHash: []byte("admin-session-hash-012345678901234")}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertSession(ctx, imp) }); err != nil {
		t.Fatal(err)
	}
	if got := lastOf(); got == nil || !got.Equal(own) {
		t.Fatalf("after a sign-in-as, last sign-in = %v, want it unchanged at %v", got, own)
	}

	// The record outlives the session: it stays when the session is deleted.
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteSession(ctx, sess.IDHash) }); err != nil {
		t.Fatal(err)
	}
	if got := lastOf(); got == nil || !got.Equal(own) {
		t.Fatalf("after the session ended, last sign-in = %v, want %v", got, own)
	}
}

// TestAccountListsCountUsers checks that both account lists, sorted and plain, carry each account's number of users in the
// same query, and that a single account read does not count them.
func TestAccountListsCountUsers(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	two := newAccountRow(t, s, "Two")
	none := newAccountRow(t, s, "None")
	newUserRow(t, s, two, "a@example.com", "")
	newUserRow(t, s, two, "b@example.com", "")

	want := map[string]int64{two: 2, none: 0}
	check := func(where string, list []*Account) {
		t.Helper()
		if len(list) != 2 {
			t.Fatalf("%s: %d accounts, want 2", where, len(list))
		}
		for _, acc := range list {
			if acc.UserCount == nil || *acc.UserCount != want[acc.ID] {
				t.Errorf("%s: %s has user count %s, want %d", where, acc.Name, userCountText(acc.UserCount), want[acc.ID])
			}
		}
	}
	sorted, _, err := s.AccountsSorted(ctx, AccountFilter{}, AccountSort{ByName: true}, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	check("sorted", sorted)
	plain, _, err := s.AccountsFiltered(ctx, AccountFilter{}, Page{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	check("plain", plain)

	one, err := s.Account(ctx, two)
	if err != nil {
		t.Fatal(err)
	}
	if one.UserCount != nil {
		t.Errorf("a single account read counts its users (%d); only the lists do", *one.UserCount)
	}
}

// userCountText is an account's user count as a test message shows it: the number, or "none".
func userCountText(n *int64) string {
	if n == nil {
		return "none"
	}
	return strconv.FormatInt(*n, 10)
}
