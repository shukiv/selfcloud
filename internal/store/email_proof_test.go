// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
)

// TestEmailProofMark: the proof follows the address. A user is proved only while the proved key is the
// key of the address in use, so a change of address leaves the user unproved until the new one is proved.
func TestEmailProofMark(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	u := newUserRow(t, s, a, "ann@example.com", "")
	if u.EmailProved() {
		t.Fatal("a new user is proved")
	}
	at := Now()
	if err := s.Write(ctx, func(tx *Tx) error { return tx.SetEmailProof(ctx, u.ID, u.EmailKey, at) }); err != nil {
		t.Fatal(err)
	}
	got, err := s.User(ctx, a, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.EmailProved() || got.EmailProvedKey != u.EmailKey || got.EmailProvedAt == nil || !got.EmailProvedAt.Equal(at) {
		t.Fatalf("proved user = %+v", got)
	}
	// A proof of another address does not prove the one in use.
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.SetEmailProof(ctx, u.ID, "other@example.com", at)
	}); err != nil {
		t.Fatal(err)
	}
	got, err = s.User(ctx, a, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.EmailProved() || got.EmailProvedKey != "other@example.com" {
		t.Fatalf("user proved for another address = %+v", got)
	}
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.SetEmailProof(ctx, "usr_missing", u.EmailKey, at)
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("proof of a missing user = %v, want ErrNotFound", err)
	}
}

// TestVerificationMailedTo: a verification remembers the address its token was mailed to, and only
// that. A token returned to the caller or made by a command has no mailed address.
func TestVerificationMailedTo(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	u := newUserRow(t, s, a, "ann@example.com", "")
	_, hash := auth.NewToken()
	v := &Verification{ID: id.New("vrf"), UserID: u.ID, Purpose: PurposeVerify, TokenHash: hash,
		ExpiresAt: Now().Add(time.Hour), CreatedAt: Now(), MailedTo: u.EmailKey}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertVerification(ctx, v) }); err != nil {
		t.Fatal(err)
	}
	_, hash2 := auth.NewToken()
	v2 := &Verification{ID: id.New("vrf"), UserID: u.ID, Purpose: PurposeReset, TokenHash: hash2,
		ExpiresAt: Now().Add(time.Hour), CreatedAt: Now()}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertVerification(ctx, v2) }); err != nil {
		t.Fatal(err)
	}
	err := s.Write(ctx, func(tx *Tx) error {
		got, err := tx.VerificationByHash(ctx, hash)
		if err != nil {
			return err
		}
		if got.MailedTo != u.EmailKey {
			t.Errorf("mailed_to = %q, want %q", got.MailedTo, u.EmailKey)
		}
		got, err = tx.VerificationByHash(ctx, hash2)
		if err != nil {
			return err
		}
		if got.MailedTo != "" {
			t.Errorf("mailed_to of a returned token = %q, want empty", got.MailedTo)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestEmailProofTokens: a proof token is found by its hash, works once, and a new request voids the
// user's earlier ones.
func TestEmailProofTokens(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	u := newUserRow(t, s, a, "ann@example.com", "")
	_, hash := auth.NewToken()
	p := &EmailProof{ID: id.New("prf"), UserID: u.ID, EmailKey: u.EmailKey, TokenHash: hash,
		ExpiresAt: Now().Add(24 * time.Hour), CreatedAt: Now()}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertEmailProof(ctx, p) }); err != nil {
		t.Fatal(err)
	}
	at := Now()
	err := s.Write(ctx, func(tx *Tx) error {
		got, err := tx.EmailProofByHash(ctx, hash)
		if err != nil {
			return err
		}
		if got.ID != p.ID || got.UserID != u.ID || got.EmailKey != u.EmailKey || got.UsedAt != nil ||
			!got.ExpiresAt.Equal(p.ExpiresAt) || !got.CreatedAt.Equal(p.CreatedAt) {
			t.Fatalf("proof by hash = %+v", got)
		}
		if err := tx.UseEmailProof(ctx, p.ID, at); err != nil {
			return err
		}
		if err := tx.UseEmailProof(ctx, p.ID, at); !errors.Is(err, ErrNotFound) {
			t.Fatalf("second use = %v, want ErrNotFound", err)
		}
		got, err = tx.EmailProofByHash(ctx, hash)
		if err != nil {
			return err
		}
		if got.UsedAt == nil || !got.UsedAt.Equal(at) {
			t.Fatalf("used proof = %+v", got)
		}
		if _, err := tx.EmailProofByHash(ctx, []byte("no such hash")); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unknown hash = %v, want ErrNotFound", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// A new proof keeps only itself: the earlier ones of the user are voided.
	_, hash2 := auth.NewToken()
	p2 := &EmailProof{ID: id.New("prf"), UserID: u.ID, EmailKey: u.EmailKey, TokenHash: hash2,
		ExpiresAt: Now().Add(24 * time.Hour), CreatedAt: Now()}
	err = s.Write(ctx, func(tx *Tx) error {
		if err := tx.DeleteEmailProofs(ctx, u.ID, p2.ID); err != nil {
			return err
		}
		return tx.InsertEmailProof(ctx, p2)
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, "SELECT COUNT(*) FROM email_proofs WHERE user_id = ?", u.ID); n != 1 {
		t.Fatalf("proofs after a new request = %d, want 1", n)
	}
	err = s.Write(ctx, func(tx *Tx) error {
		if _, err := tx.EmailProofByHash(ctx, hash); !errors.Is(err, ErrNotFound) {
			t.Fatalf("voided proof = %v, want ErrNotFound", err)
		}
		if _, err := tx.EmailProofByHash(ctx, hash2); err != nil {
			t.Fatalf("kept proof = %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestEmailProofCascade: removing a user removes its proof tokens.
func TestEmailProofCascade(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	u := newUserRow(t, s, a, "ann@example.com", "")
	_, hash := auth.NewToken()
	p := &EmailProof{ID: id.New("prf"), UserID: u.ID, EmailKey: u.EmailKey, TokenHash: hash,
		ExpiresAt: Now().Add(24 * time.Hour), CreatedAt: Now()}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertEmailProof(ctx, p) }); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteUser(ctx, a, u.ID) }); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, "SELECT COUNT(*) FROM email_proofs WHERE user_id = ?", u.ID); n != 0 {
		t.Fatalf("proofs of a removed user = %d, want 0", n)
	}
}
