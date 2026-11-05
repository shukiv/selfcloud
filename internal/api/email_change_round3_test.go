// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// TestConfirmIsRefusedWhenTheOldAddressBudgetIsSpent: with a mailer, confirming a link change charges the notice to the old
// address. Here that address's budget is spent first, so the confirmation is refused as rate limited before any write: the
// address stays, the pending address stays, and the link is not used up, so a second try is refused the same way.
func TestConfirmIsRefusedWhenTheOldAddressBudgetIsSpent(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	a, _ := verifiedUser(t, s, key, acc.ID, "a@example.com")
	b, _ := verifiedUser(t, s, key, acc.ID, "b@example.com")
	// b asks for k@example.com by email until mail.target for k is spent. The address is free, so nothing else is refused.
	spent := false
	for i := 0; i < 10 && !spent; i++ {
		spent = call(t, s, "PATCH", userPath(acc.ID, b.ID), key,
			map[string]any{"email": "k@example.com", "verification": "email"}).Code == 429
	}
	if !spent {
		t.Fatal("mail.target for k was never spent")
	}
	// a moves to k with none. Its notice is charged to a, not k, so the change applies.
	wantStatus(t, call(t, s, "PATCH", userPath(acc.ID, a.ID), key,
		map[string]any{"email": "k@example.com", "verification": "none"}), 200)
	pending := decode[userAnswer](t, call(t, s, "PATCH", userPath(acc.ID, a.ID), key,
		map[string]any{"email": "p@example.com", "verification": "link"}))
	confirm := map[string]any{"token": pending.Verification.Token}
	for i := 0; i < 2; i++ {
		wantError(t, call(t, s, "POST", "/api/v1/auth/email-change/confirm", "", confirm), 429, apierr.RateLimited)
	}
	if got := emailOf(t, s, key, acc.ID, a.ID); got.Email != "k@example.com" || got.PendingEmail == nil {
		t.Errorf("after the refused confirmations a reads %+v, want k and the pending p", got)
	}
}

// TestWithoutAMailerNoticesDoNotRefuse: with no mailer nothing is sent, so nothing is charged, and the same alternating
// changes that are refused with a mailer (TestNoticeBudgetRefusesTheChangeWhenSpent) all apply.
func TestWithoutAMailerNoticesDoNotRefuse(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, nil)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "victim@example.com")
	path := userPath(acc.ID, usr.ID)
	addresses := []string{"other@example.com", "victim@example.com"}
	for i := 0; i < 10; i++ {
		wantStatus(t, call(t, s, "PATCH", path, key,
			map[string]any{"email": addresses[i%2], "verification": "none"}), 200)
	}
	if got := emailOf(t, s, key, acc.ID, usr.ID); got.Email != "victim@example.com" {
		t.Errorf("after ten changes the address is %s, want victim (each change applied)", got.Email)
	}
}

// TestNoneActivatingAPendingUserIsAudited: a none change that activates a pending user records activated: true in its
// audit entry. A none change of an active user records no activated key.
func TestNoneActivatingAPendingUserIsAudited(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	pend := decode[userResponse](t, call(t, s, "POST", "/api/v1/accounts/"+acc.ID+"/users", key,
		map[string]any{"email": "p@example.com", "verification": "link"}))
	act, _ := verifiedUser(t, s, key, acc.ID, "a@example.com")
	wantStatus(t, call(t, s, "PATCH", userPath(acc.ID, pend.ID), key,
		map[string]any{"email": "q@example.com", "verification": "none"}), 200)
	wantStatus(t, call(t, s, "PATCH", userPath(acc.ID, act.ID), key,
		map[string]any{"email": "b@example.com", "verification": "none"}), 200)
	entries, _, err := s.store.AuditLog(context.Background(), acc.ID, "", true, store.Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	byTarget := map[string]map[string]any{}
	for _, e := range entries {
		if e.Action == "user.email_changed" && e.Details["verification"] == "none" {
			byTarget[e.Target] = e.Details
		}
	}
	if byTarget[pend.ID]["activated"] != true {
		t.Errorf("activating change details = %v, want activated: true", byTarget[pend.ID])
	}
	if _, ok := byTarget[act.ID]["activated"]; ok {
		t.Errorf("change of an active user records activated: %v", byTarget[act.ID])
	}
}
