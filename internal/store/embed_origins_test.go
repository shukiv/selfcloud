// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
)

// TestEmbedOriginsStore: an account's embed origins start empty, are replaced whole, read back sorted,
// and go with the account (decision 185).
func TestEmbedOriginsStore(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	b := newAccountRow(t, s, "B")

	got, err := s.EmbedOrigins(ctx, a)
	if err != nil || len(got) != 0 {
		t.Fatalf("a new account's origins = %v, %v; want none", got, err)
	}
	now := Now()
	err = s.Write(ctx, func(tx *Tx) error {
		return tx.ReplaceEmbedOrigins(ctx, a, []string{"https://b.example", "https://a.example"}, now)
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err = s.EmbedOrigins(ctx, a)
	if err != nil || !reflect.DeepEqual(got, []string{"https://a.example", "https://b.example"}) {
		t.Fatalf("origins = %v, %v; want them sorted", got, err)
	}
	// Another account's list is its own.
	if got, err := s.EmbedOrigins(ctx, b); err != nil || len(got) != 0 {
		t.Fatalf("other account's origins = %v, %v", got, err)
	}
	// A replace overwrites, and an empty list clears.
	err = s.Write(ctx, func(tx *Tx) error {
		return tx.ReplaceEmbedOrigins(ctx, a, []string{"https://c.example"}, now)
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.EmbedOrigins(ctx, a); !reflect.DeepEqual(got, []string{"https://c.example"}) {
		t.Fatalf("after replace = %v", got)
	}
	err = s.Write(ctx, func(tx *Tx) error { return tx.ReplaceEmbedOrigins(ctx, a, nil, now) })
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.EmbedOrigins(ctx, a); len(got) != 0 {
		t.Fatalf("after clear = %v", got)
	}
	// Deleting the account removes its origins.
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteAccount(ctx, a) }); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM account_embed_origins WHERE account_id = ?", a).Scan(&n); err != nil || n != 0 {
		t.Fatalf("origins left after account delete = %d, %v", n, err)
	}
}

// TestSessionFrameRoundTrip: a session carries whether it was made for a frame, and an ordinary one
// does not (decision 185).
func TestSessionFrameRoundTrip(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	u := newUserRow(t, s, a, "ann@example.com", "")
	now := Now()
	for _, frame := range []bool{false, true} {
		hash := []byte("frame-hash-false-0123456789012345")
		if frame {
			hash = []byte("frame-hash-true-01234567890123456")
		}
		sess := &Session{IDHash: hash, UserID: u.ID, AccountID: a, CSRFToken: "c", CreatedAt: now,
			LastSeenAt: now, ExpiresAt: now.Add(time.Hour), Frame: frame}
		if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertSession(ctx, sess) }); err != nil {
			t.Fatal(err)
		}
		got, err := s.SessionByHash(ctx, hash)
		if err != nil || got.Frame != frame {
			t.Fatalf("frame = %v, %v; want %v", got.Frame, err, frame)
		}
	}
}

// TestSignInLinkFrameRoundTrip: a sign-in link carries whether it was made for a frame (decision 185).
func TestSignInLinkFrameRoundTrip(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	u := newUserRow(t, s, a, "ann@example.com", "")
	now := Now()
	for _, frame := range []bool{false, true} {
		hash := []byte("link-hash-false-01234567890123456")
		if frame {
			hash = []byte("link-hash-true-012345678901234567")
		}
		l := &SignInLink{ID: id.New("sil"), AccountID: a, UserID: u.ID, TokenHash: hash, Next: "/files",
			ExpiresAt: now.Add(time.Minute), CreatedAt: now, Frame: frame}
		var got *SignInLink
		err := s.Write(ctx, func(tx *Tx) error {
			if err := tx.InsertSignInLink(ctx, l); err != nil {
				return err
			}
			var err error
			got, err = tx.SignInLinkByHash(ctx, hash)
			return err
		})
		if err != nil || got.Frame != frame {
			t.Fatalf("frame = %v, %v; want %v", got.Frame, err, frame)
		}
	}
}
