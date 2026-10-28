package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestSessionLifecycle(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	u := newUserRow(t, s, a, "ann@example.com", "")
	now := Now()
	hash := []byte("session-hash-0123456789012345678")
	sess := &Session{IDHash: hash, UserID: u.ID, AccountID: a, CSRFToken: "csrf-value", CreatedAt: now,
		LastSeenAt: now, ExpiresAt: now.Add(time.Hour), IP: "192.0.2.10", UserAgent: "test"}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertSession(ctx, sess) }); err != nil {
		t.Fatal(err)
	}
	got, err := s.SessionByHash(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID != u.ID || got.AccountID != a || got.CSRFToken != "csrf-value" || got.IP != "192.0.2.10" ||
		!got.ExpiresAt.Equal(sess.ExpiresAt) {
		t.Fatalf("got %+v", got)
	}
	if _, err := s.SessionByHash(ctx, []byte("unknown")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown hash = %v", err)
	}

	later := now.Add(10 * time.Minute)
	if err := s.Write(ctx, func(tx *Tx) error { return tx.TouchSession(ctx, hash, later, later.Add(time.Hour)) }); err != nil {
		t.Fatal(err)
	}
	got, _ = s.SessionByHash(ctx, hash)
	if !got.LastSeenAt.Equal(later) {
		t.Errorf("last_seen_at = %v, want %v", got.LastSeenAt, later)
	}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.TouchSession(ctx, []byte("unknown"), later, later) }); !errors.Is(err, ErrNotFound) {
		t.Errorf("touch of a missing session = %v", err)
	}

	// Deleting a session, or deleting one that is gone, is not an error.
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteSession(ctx, hash) }); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteSession(ctx, hash) }); err != nil {
		t.Fatalf("second delete = %v", err)
	}
	if _, err := s.SessionByHash(ctx, hash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete = %v", err)
	}
}

func TestDeleteUserSessionsSignsOutEverywhere(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	ann := newUserRow(t, s, a, "ann@example.com", "")
	bob := newUserRow(t, s, a, "bob@example.com", "")
	now := Now()
	for i, uid := range []string{ann.ID, ann.ID, bob.ID} {
		sess := &Session{IDHash: []byte(fmt.Sprintf("hash-%d-0123456789012345678", i)),
			UserID: uid, AccountID: a, CSRFToken: "c", CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour)}
		if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertSession(ctx, sess) }); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteUserSessions(ctx, ann.ID) }); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE user_id = ?", ann.ID).Scan(&n); err != nil || n != 0 {
		t.Errorf("ann's sessions left = %d, %v", n, err)
	}
	if err := s.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE user_id = ?", bob.ID).Scan(&n); err != nil || n != 1 {
		t.Errorf("bob's sessions = %d, %v; want 1", n, err)
	}
}

func TestSessionNeedsExistingUser(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	now := Now()
	err := s.Write(ctx, func(tx *Tx) error {
		return tx.InsertSession(ctx, &Session{IDHash: []byte("orphan-hash-0123456789012345678"), UserID: "usr_missing",
			AccountID: a, CSRFToken: "c", CreatedAt: now, LastSeenAt: now, ExpiresAt: now})
	})
	if err == nil {
		t.Fatal("a session for a missing user was stored")
	}
}
