package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"selfcloud/internal/id"
)

// newAdminRow adds an installation admin with the given email and returns it.
func newAdminRow(t *testing.T, s *Store, email string) *User {
	t.Helper()
	now := Now()
	u := &User{ID: id.New("usr"), Email: email, EmailKey: email, Locale: "en", Role: RoleAdmin,
		Status: UserActive, CreatedAt: now, UpdatedAt: now}
	if err := s.Write(context.Background(), func(tx *Tx) error { return tx.InsertUser(context.Background(), u) }); err != nil {
		t.Fatal(err)
	}
	return u
}

func TestAdminUsersHaveNoAccount(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "A")
	admin := newAdminRow(t, s, "root@example.com")

	got, err := s.User(ctx, "", admin.ID)
	if err != nil || got.AccountID != "" || got.Role != RoleAdmin {
		t.Fatalf("admin lookup = %+v, %v", got, err)
	}
	// An admin is not visible through any account.
	if _, err := s.User(ctx, acc, admin.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("admin under an account = %v, want ErrNotFound", err)
	}

	// Admin emails are unique among admins, but not against a tenant user's email.
	dup := &User{ID: id.New("usr"), Email: "root@example.com", EmailKey: "root@example.com", Locale: "en",
		Role: RoleAdmin, Status: UserPending, CreatedAt: Now(), UpdatedAt: Now()}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertUser(ctx, dup) }); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate admin email = %v, want ErrConflict", err)
	}
	newUserRow(t, s, acc, "root@example.com", "")

	// The schema rejects an admin with an account, and a tenant user without one.
	bad := &User{ID: id.New("usr"), AccountID: acc, Email: "x@example.com", EmailKey: "x@example.com", Locale: "en",
		Role: RoleAdmin, Status: UserActive, CreatedAt: Now(), UpdatedAt: Now()}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertUser(ctx, bad) }); err == nil {
		t.Error("admin role with an account was accepted")
	}
	orphan := &User{ID: id.New("usr"), Email: "y@example.com", EmailKey: "y@example.com", Locale: "en",
		Role: RoleMember, Status: UserActive, CreatedAt: Now(), UpdatedAt: Now()}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertUser(ctx, orphan) }); err == nil {
		t.Error("member without an account was accepted")
	}
}

func TestAdminSessionAndLookupScopes(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "A")
	admin := newAdminRow(t, s, "root@example.com")
	newUserRow(t, s, acc, "root@example.com", "")

	// The admin scope finds only admins; "" finds both; an account finds only its users.
	only, err := s.FindUsersByEmail(ctx, "root@example.com", AdminScope, 10)
	if err != nil || len(only) != 1 || only[0].ID != admin.ID {
		t.Errorf("admin scope = %v, %v", only, err)
	}
	both, _ := s.FindUsersByEmail(ctx, "root@example.com", "", 10)
	if len(both) != 2 {
		t.Errorf("any scope = %d users, want 2", len(both))
	}
	inAcc, _ := s.FindUsersByEmail(ctx, "root@example.com", acc, 10)
	if len(inAcc) != 1 || inAcc[0].AccountID != acc {
		t.Errorf("account scope = %v", inAcc)
	}

	// A session of an admin has no account.
	hash := []byte("admin-session-hash-0123456789abc")
	err = s.Write(ctx, func(tx *Tx) error {
		return tx.InsertSession(ctx, &Session{IDHash: hash, UserID: admin.ID, AccountID: "", CSRFToken: "c",
			CreatedAt: Now(), LastSeenAt: Now(), ExpiresAt: Now().Add(time.Hour)})
	})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.SessionByHash(ctx, hash)
	if err != nil || sess.AccountID != "" || sess.UserID != admin.ID {
		t.Fatalf("admin session = %+v, %v", sess, err)
	}
}

func TestCountActiveAdmins(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAdminRow(t, s, "a@example.com")
	b := newAdminRow(t, s, "b@example.com")
	err := s.Write(ctx, func(tx *Tx) error {
		n, err := tx.CountActiveAdmins(ctx, a.ID)
		if err != nil || n != 1 {
			t.Errorf("others of a = %d, %v; want 1", n, err)
		}
		b.Status = UserDisabled
		b.UpdatedAt = Now()
		if err := tx.UpdateUser(ctx, b); err != nil {
			return err
		}
		n, _ = tx.CountActiveAdmins(ctx, a.ID)
		if n != 0 {
			t.Errorf("others of a after b disabled = %d, want 0", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
