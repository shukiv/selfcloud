// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
)

// refSession inserts a session of the user with the given ref and end, and returns its hash.
func refSession(t *testing.T, s *Store, userID, accountID, ref string, endsAt time.Time, imp string) []byte {
	t.Helper()
	hash := []byte("hash-" + id.New("ses"))
	now := Now()
	sess := &Session{IDHash: hash, UserID: userID, AccountID: accountID, CSRFToken: "c", CreatedAt: now,
		LastSeenAt: now, ExpiresAt: now.Add(time.Hour), SessionRef: ref, EndsAt: endsAt, ImpersonatorID: imp}
	if imp != "" {
		sess.ImpersonatorSessionHash = []byte("admin-session-hash-0123456789012")
	}
	if err := s.Write(context.Background(), func(tx *Tx) error { return tx.InsertSession(context.Background(), sess) }); err != nil {
		t.Fatal(err)
	}
	return hash
}

// sessionCount counts the sessions left for the user.
func sessionCount(t *testing.T, s *Store, userID string) int {
	t.Helper()
	var n int
	if err := s.r.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM sessions WHERE user_id = ?", userID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestEndUserSessionsByRef(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	ann := newUserRow(t, s, a, "ann@example.com", "")
	bob := newUserRow(t, s, a, "bob@example.com", "")
	adm := newUserRow(t, s, a, "admin@example.com", "")
	zero := time.Time{}
	refSession(t, s, ann.ID, a, "app-one", zero, "")
	refSession(t, s, ann.ID, a, "app-one", zero, "")
	refSession(t, s, ann.ID, a, "app-two", zero, "")
	refSession(t, s, ann.ID, a, "", zero, "")
	// An impersonation of ann with the same ref is the admin's, and stays.
	impHash := refSession(t, s, ann.ID, a, "app-one", zero, adm.ID)
	// Bob's session with the same ref is his, and stays.
	bobHash := refSession(t, s, bob.ID, a, "app-one", zero, "")

	var n int64
	if err := s.Write(ctx, func(tx *Tx) error {
		var err error
		n, err = tx.EndUserSessions(ctx, ann.ID, "app-one")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("ended %d sessions, want 2", n)
	}
	if got := sessionCount(t, s, ann.ID); got != 3 {
		t.Errorf("ann has %d sessions left, want 3 (app-two, no ref, the impersonation)", got)
	}
	if got := sessionCount(t, s, bob.ID); got != 1 {
		t.Errorf("bob has %d sessions left, want 1", got)
	}
	if _, err := s.SessionByHash(ctx, impHash); err != nil {
		t.Errorf("the impersonation ended: %v", err)
	}
	if _, err := s.SessionByHash(ctx, bobHash); err != nil {
		t.Errorf("bob's session ended: %v", err)
	}
	// A second call with the same ref ends nothing.
	if err := s.Write(ctx, func(tx *Tx) error {
		var err error
		n, err = tx.EndUserSessions(ctx, ann.ID, "app-one")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("second call ended %d, want 0", n)
	}
}

func TestEndUserSessionsAllSparesImpersonation(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	ann := newUserRow(t, s, a, "ann@example.com", "")
	bob := newUserRow(t, s, a, "bob@example.com", "")
	adm := newUserRow(t, s, a, "admin@example.com", "")
	zero := time.Time{}
	refSession(t, s, ann.ID, a, "app-one", zero, "")
	refSession(t, s, ann.ID, a, "", zero, "")
	impHash := refSession(t, s, ann.ID, a, "app-one", zero, adm.ID)
	bobHash := refSession(t, s, bob.ID, a, "", zero, "")

	var n int64
	if err := s.Write(ctx, func(tx *Tx) error {
		var err error
		n, err = tx.EndUserSessions(ctx, ann.ID, "")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("ended %d sessions, want 2", n)
	}
	if got := sessionCount(t, s, ann.ID); got != 1 {
		t.Errorf("ann has %d sessions left, want only the impersonation", got)
	}
	if _, err := s.SessionByHash(ctx, impHash); err != nil {
		t.Errorf("the impersonation ended: %v", err)
	}
	if _, err := s.SessionByHash(ctx, bobHash); err != nil {
		t.Errorf("bob's session ended: %v", err)
	}
}

func TestSessionRefAndEndsAtRoundTrip(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	u := newUserRow(t, s, a, "ann@example.com", "")
	now := Now()
	ends := now.Add(30 * time.Minute)
	hash := []byte("round-trip-hash-0123456789012345")
	sess := &Session{IDHash: hash, UserID: u.ID, AccountID: a, CSRFToken: "c", CreatedAt: now, LastSeenAt: now,
		ExpiresAt: ends, SessionRef: "app-one", EndsAt: ends}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertSession(ctx, sess) }); err != nil {
		t.Fatal(err)
	}
	got, err := s.SessionByHash(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionRef != "app-one" || !got.EndsAt.Equal(ends) {
		t.Errorf("session ref %q ends %v, want app-one and %v", got.SessionRef, got.EndsAt, ends)
	}
	// A session with no ref and no end keeps both zero.
	plain := []byte("plain-hash-012345678901234567890")
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.InsertSession(ctx, &Session{IDHash: plain, UserID: u.ID, AccountID: a, CSRFToken: "c",
			CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour)})
	}); err != nil {
		t.Fatal(err)
	}
	got, err = s.SessionByHash(ctx, plain)
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionRef != "" || !got.EndsAt.IsZero() {
		t.Errorf("plain session ref %q ends %v, want none", got.SessionRef, got.EndsAt)
	}
}

func TestSignInLinkSessionRefRoundTrip(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	u := newUserRow(t, s, a, "ann@example.com", "")
	now := Now()
	ends := now.Add(30 * time.Minute)
	l := &SignInLink{ID: "sil_ref", AccountID: a, UserID: u.ID, TokenHash: []byte("link-hash-ref-012345678901234567"),
		Next: "/files", ExpiresAt: now.Add(time.Minute), CreatedAt: now, SessionRef: "app-one", SessionEndsAt: ends}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertSignInLink(ctx, l) }); err != nil {
		t.Fatal(err)
	}
	var got *SignInLink
	if err := s.Write(ctx, func(tx *Tx) error {
		var err error
		got, err = tx.SignInLinkByHash(ctx, l.TokenHash)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got.SessionRef != "app-one" || !got.SessionEndsAt.Equal(ends) {
		t.Errorf("link ref %q ends %v, want app-one and %v", got.SessionRef, got.SessionEndsAt, ends)
	}
	// A link with no ref and no end keeps both zero, and its end is NULL in the row.
	plain := &SignInLink{ID: "sil_plain", AccountID: a, UserID: u.ID, TokenHash: []byte("link-hash-plain-0123456789012345"),
		Next: "/files", ExpiresAt: now.Add(time.Minute), CreatedAt: now}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertSignInLink(ctx, plain) }); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx *Tx) error {
		var err error
		got, err = tx.SignInLinkByHash(ctx, plain.TokenHash)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got.SessionRef != "" || !got.SessionEndsAt.IsZero() {
		t.Errorf("plain link ref %q ends %v, want none", got.SessionRef, got.SessionEndsAt)
	}
	var nulls int
	if err := s.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM sign_in_links WHERE session_ends_at IS NULL").Scan(&nulls); err != nil || nulls != 1 {
		t.Errorf("links with a NULL end = %d, %v; want 1", nulls, err)
	}
	if _, err := s.SessionByHash(ctx, []byte("missing")); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing session = %v", err)
	}
}
