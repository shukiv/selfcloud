// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"fmt"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/mail"
)

// spendTargetFromAnotherAccount uses up mail.target for address (3 an hour, across the install) by verification mail from
// another account: each create with verification email sends one mail to address. Nothing of the victim's account is used.
func spendTargetFromAnotherAccount(t *testing.T, s *Server, admin, address string) {
	t.Helper()
	for i := 0; i < 3; i++ {
		accB, keyB := newAccount(t, s, admin, fmt.Sprintf("Beta %d", i))
		resp := call(t, s, "POST", "/api/v1/accounts/"+accB.ID+"/users", keyB,
			map[string]any{"email": address, "verification": "email"})
		if resp.Code >= 300 {
			t.Fatalf("another account's mail %d to %s: status %d, want the budget still to spend", i+1, address, resp.Code)
		}
	}
}

// TestAnotherAccountsMailDoesNotBlockANoticeOfAChange: another account spends the install-wide mail.target of the victim's
// address. The victim's own account then changes the address with verification none. The change applies, and the old
// address gets its notice. The notice has its own budgets, which only this account's changes spend.
func TestAnotherAccountsMailDoesNotBlockANoticeOfAChange(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	accA, keyA := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, keyA, accA.ID, "victim@example.com")
	spendTargetFromAnotherAccount(t, s, admin, "victim@example.com")
	before := messagesTo(rec, "victim@example.com")
	wantStatus(t, call(t, s, "PATCH", userPath(accA.ID, usr.ID), keyA,
		map[string]any{"email": "new@example.com", "verification": "none"}), 200)
	if got := emailOf(t, s, keyA, accA.ID, usr.ID); got.Email != "new@example.com" {
		t.Errorf("the change did not apply: the address is %s", got.Email)
	}
	if n := messagesTo(rec, "victim@example.com") - before; n != 1 {
		t.Errorf("%d mails to the old address after the change, want its one notice", n)
	}
}

// TestAnotherAccountsMailDoesNotBlockAConfirmation: the same spending, after the victim's account asked for a change by
// link. The confirmation sends the old address its notice, and it applies.
func TestAnotherAccountsMailDoesNotBlockAConfirmation(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	accA, keyA := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, keyA, accA.ID, "victim@example.com")
	pending := decode[userAnswer](t, call(t, s, "PATCH", userPath(accA.ID, usr.ID), keyA,
		map[string]any{"email": "new@example.com", "verification": "link"}))
	spendTargetFromAnotherAccount(t, s, admin, "victim@example.com")
	wantStatus(t, call(t, s, "POST", "/api/v1/auth/email-change/confirm", "",
		map[string]any{"token": pending.Verification.Token}), 200)
	if got := emailOf(t, s, keyA, accA.ID, usr.ID); got.Email != "new@example.com" {
		t.Errorf("the confirmation did not apply: the address is %s", got.Email)
	}
}

// TestNoticeAccountLimitRefusesWhenTheAccountSpendsIt: the account's own changes spend its notice budget for the account
// (30 an hour). Each change here has a fresh old address, so only the account's budget can refuse: the 31st change is
// refused with 429 and does not apply.
func TestNoticeAccountLimitRefusesWhenTheAccountSpendsIt(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "a0@example.com")
	path := userPath(acc.ID, usr.ID)
	for i := 1; i <= 30; i++ {
		wantStatus(t, call(t, s, "PATCH", path, key,
			map[string]any{"email": fmt.Sprintf("a%d@example.com", i), "verification": "none"}), 200)
	}
	wantError(t, call(t, s, "PATCH", path, key,
		map[string]any{"email": "a31@example.com", "verification": "none"}), 429, apierr.RateLimited)
	if got := emailOf(t, s, key, acc.ID, usr.ID); got.Email != "a30@example.com" {
		t.Errorf("after the refused change the address is %s, want a30", got.Email)
	}
}

// TestFailedChangesSpendNoNoticeBudget: three changes of one address fail on an external_id conflict, which UpdateUser finds
// after the change has taken its notice charge. A failed change gives its charge back, so a valid change of the same address
// still applies and its notice is sent. Without the refund the three failures use up the address's budget (red on 3adc029).
func TestFailedChangesSpendNoNoticeBudget(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "a@example.com")
	other, _ := verifiedUser(t, s, key, acc.ID, "b@example.com")
	wantStatus(t, call(t, s, "PATCH", userPath(acc.ID, other.ID), key, map[string]any{"external_id": "ext-1"}), 200)
	path := userPath(acc.ID, usr.ID)
	for i := 0; i < 3; i++ {
		wantError(t, call(t, s, "PATCH", path, key,
			map[string]any{"email": "new@example.com", "verification": "none", "external_id": "ext-1"}), 409, apierr.Conflict)
	}
	if got := emailOf(t, s, key, acc.ID, usr.ID); got.Email != "a@example.com" {
		t.Fatalf("a failed change moved the address to %s", got.Email)
	}
	wantStatus(t, call(t, s, "PATCH", path, key,
		map[string]any{"email": "new@example.com", "verification": "none"}), 200)
	if got := emailOf(t, s, key, acc.ID, usr.ID); got.Email != "new@example.com" {
		t.Errorf("the valid change did not apply: the address is %s", got.Email)
	}
	if n := messagesTo(rec, "a@example.com"); n != 1 {
		t.Errorf("%d notices to the old address, want 1", n)
	}
}

// TestFailedChangesRefundAtTheirReservationTime: the clock moves two seconds on every read, as the wall clock does between a
// charge and its refund in production. A refund cancels at the time its charge was made, so rate.CancelAt restores the
// tokens. Three changes that fail on an external_id conflict then leave the budget whole, and a valid change applies with
// its notice. Refunding at a later time restores nothing, so the valid change is refused (red on 59cc0ec).
func TestFailedChangesRefundAtTheirReservationTime(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	tick := frozenLimitInstant
	s.limitClock = func() time.Time {
		now := tick
		tick = tick.Add(2 * time.Second)
		return now
	}
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "a@example.com")
	other, _ := verifiedUser(t, s, key, acc.ID, "b@example.com")
	wantStatus(t, call(t, s, "PATCH", userPath(acc.ID, other.ID), key, map[string]any{"external_id": "ext-1"}), 200)
	path := userPath(acc.ID, usr.ID)
	for i := 0; i < 3; i++ {
		wantError(t, call(t, s, "PATCH", path, key,
			map[string]any{"email": "new@example.com", "verification": "none", "external_id": "ext-1"}), 409, apierr.Conflict)
	}
	wantStatus(t, call(t, s, "PATCH", path, key,
		map[string]any{"email": "new@example.com", "verification": "none"}), 200)
	if got := emailOf(t, s, key, acc.ID, usr.ID); got.Email != "new@example.com" {
		t.Errorf("the valid change did not apply: the address is %s", got.Email)
	}
	if n := messagesTo(rec, "a@example.com"); n != 1 {
		t.Errorf("%d notices to the old address, want 1", n)
	}
}
