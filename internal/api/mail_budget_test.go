package api

import (
	"fmt"
	"testing"
	"time"

	"selfcloud/internal/apierr"
	"selfcloud/internal/mail"
)

// TestRefusedAddressSpendsNoAccountBudget checks that a message refused by the per-address limit
// does not use up the account's budget: the account keeps its full 30 for other addresses.
func TestRefusedAddressSpendsNoAccountBudget(t *testing.T) {
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

// TestForgotAccountCap checks that one account's users cannot all be mailed by forgot requests
// from many addresses: the account has 30 an hour, shared by its users. Another account is not
// affected. The per-client limit is raised here, so this test isolates the account cap.
func TestForgotAccountCap(t *testing.T) {
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	s.limits["mail.forgot.ip"] = newLimiterRate(1000, time.Hour)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Alpha")
	for i := range 31 {
		verifiedUser(t, s, key, acc.ID, fmt.Sprintf("u%d@example.com", i))
	}
	for i := range 31 {
		wantStatus(t, call(t, s, "POST", "/api/v1/auth/password/forgot", "",
			map[string]any{"email": fmt.Sprintf("u%d@example.com", i)}), 202)
	}
	s.bg.Wait()
	if n := len(rec.Messages()); n != 30 {
		t.Fatalf("reset mails for one account = %d, want 30", n)
	}

	// The second account still invites normally.
	other, otherKey := newAccount(t, s, admin, "Beta")
	invited := decode[userResponse](t, call(t, s, "POST", "/api/v1/accounts/"+other.ID+"/users", otherKey,
		map[string]any{"email": "u0@example.com", "verification": "email"}))
	if invited.Verification == nil || invited.Verification.Sent == nil || !*invited.Verification.Sent {
		t.Fatalf("invitation in another account was not sent: %+v", invited.Verification)
	}
}
