package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"selfcloud/internal/id"
)

// newAccountRow adds an account and returns its ID.
func newAccountRow(t *testing.T, s *Store, name string) string {
	t.Helper()
	acc := &Account{ID: id.New("acc"), Name: name, Status: AccountActive, CreatedAt: Now(), UpdatedAt: Now()}
	if err := s.Write(context.Background(), func(tx *Tx) error { return tx.InsertAccount(context.Background(), acc) }); err != nil {
		t.Fatal(err)
	}
	return acc.ID
}

// newUserRow adds a user to accountID and returns it.
func newUserRow(t *testing.T, s *Store, accountID, email, externalID string) *User {
	t.Helper()
	now := Now()
	u := &User{ID: id.New("usr"), AccountID: accountID, ExternalID: externalID, Email: email,
		EmailKey: email, Locale: "en", Role: RoleMember, Status: UserPending, CreatedAt: now, UpdatedAt: now}
	if err := s.Write(context.Background(), func(tx *Tx) error { return tx.InsertUser(context.Background(), u) }); err != nil {
		t.Fatal(err)
	}
	return u
}

func TestUserRoundTripAndAccountScope(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	b := newAccountRow(t, s, "B")
	u := newUserRow(t, s, a, "ann@example.com", "ext-1")

	got, err := s.User(ctx, a, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExternalID != "ext-1" || got.Status != UserPending || got.PasswordHash != "" || got.VerifiedAt != nil {
		t.Fatalf("got %+v", got)
	}
	// Another account cannot see the user, even with its ID.
	if _, err := s.User(ctx, b, u.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account read = %v, want ErrNotFound", err)
	}
	err = s.Write(ctx, func(tx *Tx) error {
		if err := tx.DeleteUser(ctx, b, u.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("cross-account delete = %v", err)
		}
		if _, err := tx.User(ctx, b, u.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("cross-account tx read = %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUserUniqueness(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	b := newAccountRow(t, s, "B")
	newUserRow(t, s, a, "ann@example.com", "ext-1")

	// The same email in one account is a conflict; in another account it is fine.
	dup := &User{ID: id.New("usr"), AccountID: a, Email: "ann@example.com", EmailKey: "ann@example.com",
		Locale: "en", Role: RoleMember, Status: UserPending, CreatedAt: Now(), UpdatedAt: Now()}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertUser(ctx, dup) }); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate email = %v, want ErrConflict", err)
	}
	newUserRow(t, s, b, "ann@example.com", "ext-1")

	// The external ID is unique within an account only.
	ext := &User{ID: id.New("usr"), AccountID: a, ExternalID: "ext-1", Email: "bob@example.com",
		EmailKey: "bob@example.com", Locale: "en", Role: RoleMember, Status: UserPending, CreatedAt: Now(), UpdatedAt: Now()}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertUser(ctx, ext) }); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate external_id = %v, want ErrConflict", err)
	}

	err := s.Write(ctx, func(tx *Tx) error {
		found, err := tx.UserByEmail(ctx, a, "ann@example.com")
		if err != nil {
			return err
		}
		found.ExternalID = "ext-1"
		found.UpdatedAt = Now()
		return tx.UpdateUser(ctx, found)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFindUsersByEmailCrossesAccounts(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	b := newAccountRow(t, s, "B")
	newUserRow(t, s, a, "shared@example.com", "")
	newUserRow(t, s, b, "shared@example.com", "")
	newUserRow(t, s, b, "other@example.com", "")

	all, err := s.FindUsersByEmail(ctx, "shared@example.com", "", 10)
	if err != nil || len(all) != 2 {
		t.Fatalf("all accounts = %d, %v", len(all), err)
	}
	only, err := s.FindUsersByEmail(ctx, "shared@example.com", b, 10)
	if err != nil || len(only) != 1 || only[0].AccountID != b {
		t.Fatalf("one account = %+v, %v", only, err)
	}
	capped, _ := s.FindUsersByEmail(ctx, "shared@example.com", "", 1)
	if len(capped) != 1 {
		t.Errorf("limit ignored: %d", len(capped))
	}
}

func TestUsersPagingAndFilters(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	var ids []string
	for _, e := range []string{"a@example.com", "b@example.com", "c@example.com"} {
		ids = append(ids, newUserRow(t, s, a, e, "").ID)
	}
	first, next, err := s.Users(ctx, a, UserFilter{}, Page{Limit: 2})
	if err != nil || len(first) != 2 || next == "" {
		t.Fatalf("first page = %d, %q, %v", len(first), next, err)
	}
	// Rows made in the same millisecond order by ID, so check the pages together, not their order.
	second, next2, err := s.Users(ctx, a, UserFilter{}, Page{Limit: 2, Cursor: next})
	if err != nil || len(second) != 1 || next2 != "" {
		t.Fatalf("second page = %+v, %q, %v", second, next2, err)
	}
	seen := map[string]bool{first[0].ID: true, first[1].ID: true, second[0].ID: true}
	for _, want := range ids {
		if !seen[want] {
			t.Errorf("paging missed user %s", want)
		}
	}
	byEmail, _, _ := s.Users(ctx, a, UserFilter{EmailKey: "b@example.com"}, Page{Limit: 10})
	if len(byEmail) != 1 || byEmail[0].ID != ids[1] {
		t.Errorf("by email = %+v", byEmail)
	}
	if _, _, err := s.Users(ctx, a, UserFilter{}, Page{Limit: 2, Cursor: "not-a-cursor"}); !errors.Is(err, ErrBadCursor) {
		t.Errorf("bad cursor = %v", err)
	}
}

func TestUpdateUserStatusAndPassword(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	u := newUserRow(t, s, a, "ann@example.com", "")
	at := Now()
	err := s.Write(ctx, func(tx *Tx) error {
		u.Status = UserActive
		u.VerifiedAt = &at
		u.PasswordHash = "$argon2id$stub"
		u.UpdatedAt = at
		return tx.UpdateUser(ctx, u)
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.User(ctx, a, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != UserActive || got.VerifiedAt == nil || !got.VerifiedAt.Equal(at) || got.PasswordHash != "$argon2id$stub" {
		t.Fatalf("got %+v", got)
	}
}

func TestVerificationTokens(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	u := newUserRow(t, s, a, "ann@example.com", "")
	now := Now()
	v1 := &Verification{ID: id.New("vrf"), UserID: u.ID, Purpose: PurposeVerify, TokenHash: []byte("hash-one-0123456789012345678901"),
		ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	v2 := &Verification{ID: id.New("vrf"), UserID: u.ID, Purpose: PurposeReset, TokenHash: []byte("hash-two-0123456789012345678901"),
		ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	err := s.Write(ctx, func(tx *Tx) error {
		if err := tx.InsertVerification(ctx, v1); err != nil {
			return err
		}
		return tx.InsertVerification(ctx, v2)
	})
	if err != nil {
		t.Fatal(err)
	}
	// A token hash is unique.
	dup := *v1
	dup.ID = id.New("vrf")
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertVerification(ctx, &dup) }); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate token hash = %v", err)
	}

	err = s.Write(ctx, func(tx *Tx) error {
		got, err := tx.VerificationByHash(ctx, v1.TokenHash)
		if err != nil || got.Purpose != PurposeVerify || got.UsedAt != nil || !got.ExpiresAt.Equal(v1.ExpiresAt) {
			t.Fatalf("lookup = %+v, %v", got, err)
		}
		if err := tx.UseVerification(ctx, v1.ID, now); err != nil {
			return err
		}
		// Using it again fails: a token is single-use.
		if err := tx.UseVerification(ctx, v1.ID, now); !errors.Is(err, ErrNotFound) {
			t.Errorf("second use = %v", err)
		}
		// Keep v1, drop the other verifications of its purpose; the reset token is a different purpose and stays.
		if err := tx.DeleteVerifications(ctx, u.ID, PurposeVerify, v1.ID); err != nil {
			return err
		}
		if _, err := tx.VerificationByHash(ctx, v2.TokenHash); err != nil {
			t.Errorf("reset token removed by a verify purge: %v", err)
		}
		return tx.DeleteVerifications(ctx, u.ID, PurposeReset, "")
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Write(ctx, func(tx *Tx) error {
		if _, err := tx.VerificationByHash(ctx, v2.TokenHash); !errors.Is(err, ErrNotFound) {
			t.Errorf("reset token survived purge: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDeletingAccountRemovesUsersAndTokens(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := newAccountRow(t, s, "A")
	u := newUserRow(t, s, a, "ann@example.com", "")
	now := Now()
	err := s.Write(ctx, func(tx *Tx) error {
		if err := tx.InsertVerification(ctx, &Verification{ID: id.New("vrf"), UserID: u.ID, Purpose: PurposeVerify,
			TokenHash: []byte("cascade-hash-0123456789012345678"), ExpiresAt: now, CreatedAt: now}); err != nil {
			return err
		}
		return tx.InsertSession(ctx, &Session{IDHash: []byte("cascade-session-01234567890123"), UserID: u.ID,
			AccountID: a, CSRFToken: "c", CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour)})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteAccount(ctx, a) }); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.r.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM users) + (SELECT COUNT(*) FROM verifications) + (SELECT COUNT(*) FROM sessions)").Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows left after account delete = %d, %v", n, err)
	}
}
