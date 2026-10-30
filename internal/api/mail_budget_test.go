// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"fmt"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/mail"
)

// TestRefusedAddressSpendsNoAccountBudget checks that a message refused by the per-address limit
// does not use up the account's budget: the account keeps its full 30 for other addresses.
func TestRefusedAddressSpendsNoAccountBudget(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	users := "/api/v1/accounts/" + acc.ID + "/users"

	// Three messages to x: the address is at its limit of 3 an hour.
	x := decode[userResponse](t, call(t, s, "POST", users, key,
		map[string]any{"email": "x@example.com", "verification": "email"}))
	wantStatus(t, call(t, s, "POST", users+"/"+x.ID+"/verification", key, map[string]any{"method": "email"}), 200)
	wantStatus(t, call(t, s, "POST", users+"/"+x.ID+"/verification", key, map[string]any{"method": "email"}), 200)
	// The fourth is refused, and must cost the account nothing.
	wantError(t, call(t, s, "POST", users+"/"+x.ID+"/verification", key, map[string]any{"method": "email"}),
		429, apierr.RateLimited)

	// So the account still has 27 for fresh addresses after the 3 above, and the 28th is refused.
	for i := 0; i < 27; i++ {
		wantStatus(t, call(t, s, "POST", users, key, map[string]any{
			"email": "f" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + "@example.com", "verification": "email"}), 201)
	}
	wantError(t, call(t, s, "POST", users, key,
		map[string]any{"email": "last@example.com", "verification": "email"}), 429, apierr.RateLimited)
}

// TestForgotBudgetDoesNotBlockInvitations checks that someone who hammers password-forgot for an
// address cannot stop the account's owner from inviting or resending to that address. The forgot
// path has its own buckets, and the forgot answer is the same 202 whether or not mail went out.
func TestForgotBudgetDoesNotBlockInvitations(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	accA, keyA := newAccount(t, s, admin, "Alpha")
	accB, keyB := newAccount(t, s, admin, "Beta")
	verifiedUser(t, s, keyA, accA.ID, "ann@example.com")

	// Ask for a reset to ann's address five times. Each answer is 202; only three mails go out.
	for i := 0; i < 5; i++ {
		wantStatus(t, call(t, s, "POST", "/api/v1/auth/password/forgot", "",
			map[string]any{"email": "ann@example.com"}), 202)
	}
	s.bg.Wait()
	// Ann was verified by link, which sends nothing, so the three reset mails are all there is.
	if n := len(rec.Messages()); n != 3 {
		t.Fatalf("mails after forgot flood = %d, want 3", n)
	}

	// The owner of account B invites the same address, and resends to it, unaffected.
	users := "/api/v1/accounts/" + accB.ID + "/users"
	invited := decode[userResponse](t, call(t, s, "POST", users, keyB,
		map[string]any{"email": "ann@example.com", "verification": "email"}))
	wantStatus(t, call(t, s, "POST", users+"/"+invited.ID+"/verification", keyB,
		map[string]any{"method": "email"}), 200)
	if invited.Verification == nil || invited.Verification.Sent == nil || !*invited.Verification.Sent {
		t.Fatalf("invitation was not sent: %+v", invited.Verification)
	}
}

// TestForgotAccountCap checks that the account cap limits repeat resets only. Sixteen addresses
// in one account each get a first reset (16 mails), then two repeats each: 32 repeats, of which
// the account's 30 an hour let through 30. Another account is not affected. The per-client limit is
// raised here, so this test isolates the account cap.
func TestForgotAccountCap(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	s.limits["mail.forgot.ip"] = newLimiterRate(1000, time.Hour)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Alpha")
	for i := range 16 {
		verifiedUser(t, s, key, acc.ID, fmt.Sprintf("u%d@example.com", i))
	}
	forgot := func(email string) {
		t.Helper()
		wantStatus(t, call(t, s, "POST", "/api/v1/auth/password/forgot", "", map[string]any{"email": email}), 202)
	}
	for i := range 16 {
		forgot(fmt.Sprintf("u%d@example.com", i))
	}
	for i := range 16 {
		forgot(fmt.Sprintf("u%d@example.com", i))
		forgot(fmt.Sprintf("u%d@example.com", i))
	}
	s.bg.Wait()
	if n := len(rec.Messages()); n != 16+30 {
		t.Fatalf("reset mails for one account = %d, want 46 (16 first resets and 30 repeats)", n)
	}

	// The second account still invites normally.
	other, otherKey := newAccount(t, s, admin, "Beta")
	invited := decode[userResponse](t, call(t, s, "POST", "/api/v1/accounts/"+other.ID+"/users", otherKey,
		map[string]any{"email": "u0@example.com", "verification": "email"}))
	if invited.Verification == nil || invited.Verification.Sent == nil || !*invited.Verification.Sent {
		t.Fatalf("invitation in another account was not sent: %+v", invited.Verification)
	}
}

// TestFirstResetSurvivesAccountCap checks that a caller who uses up an account's cap on repeats
// cannot stop a user of that account from getting a first reset: the first reset of each address
// in an hour is reserved.
func TestFirstResetSurvivesAccountCap(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	s.limits["mail.forgot.ip"] = newLimiterRate(1000, time.Hour)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Alpha")
	for i := range 16 {
		verifiedUser(t, s, key, acc.ID, fmt.Sprintf("u%d@example.com", i))
	}
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	forgot := func(email string) {
		t.Helper()
		wantStatus(t, call(t, s, "POST", "/api/v1/auth/password/forgot", "", map[string]any{"email": email}), 202)
	}
	// Use up the account's 30 repeats: 16 addresses, a first reset and two repeats each.
	for i := range 16 {
		for range 3 {
			forgot(fmt.Sprintf("u%d@example.com", i))
		}
	}
	s.bg.Wait()
	before := len(rec.Messages())
	if before != 16+30 {
		t.Fatalf("mails before ann = %d, want 46", before)
	}
	// Ann has never been asked for: her first reset goes out although the account cap is spent.
	forgot("ann@example.com")
	s.bg.Wait()
	if n := len(rec.Messages()); n != before+1 {
		t.Fatalf("ann's first reset: mails %d, want %d", n, before+1)
	}
	// Her second request in the hour is a repeat, and the account cap refuses it.
	forgot("ann@example.com")
	s.bg.Wait()
	if n := len(rec.Messages()); n != before+1 {
		t.Fatalf("ann's repeat after the cap: mails %d, want %d", n, before+1)
	}
}

// TestFirstAdminResetSurvivesAdminRepeats checks the same for installation admins, who share the
// "admin" bucket. The admin bucket is cut to 2 here so the test stays short.
func TestFirstAdminResetSurvivesAdminRepeats(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	s.limits["mail.forgot.account"] = newLimiterRate(2, time.Hour)
	admin := adminKey(t, s)
	newAdminUser(t, s, admin, "a1@example.com")
	newAdminUser(t, s, admin, "a2@example.com")
	newAdminUser(t, s, admin, "a3@example.com")
	forgot := func(email string) {
		t.Helper()
		wantStatus(t, call(t, s, "POST", "/api/v1/auth/password/forgot", "",
			map[string]any{"email": email, "account": adminChoiceID}), 202)
	}
	// a1: a first reset and two repeats, which use the whole admin bucket.
	for range 3 {
		forgot("a1@example.com")
	}
	// a2: the first reset goes out; its repeat is refused by the admin bucket.
	forgot("a2@example.com")
	forgot("a2@example.com")
	s.bg.Wait()
	if n := len(rec.Messages()); n != 4 {
		t.Fatalf("mails after admin repeats = %d, want 4", n)
	}
	// a3 has never been asked for, so the first reset goes out.
	forgot("a3@example.com")
	s.bg.Wait()
	if n := len(rec.Messages()); n != 5 {
		t.Fatalf("a3's first reset: mails = %d, want 5", n)
	}
}
