// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// messagesTo counts the mail recorded for address.
func messagesTo(rec *mail.Recorder, address string) int {
	n := 0
	for _, m := range rec.Messages() {
		if m.To == address {
			n++
		}
	}
	return n
}

// TestNoneChangeSendsTheOldAddressANoticeWithNoLink: a change with verification none, which applies at once, tells the old
// address through the notice, and the notice carries no link and does not name the new address.
func TestNoneChangeSendsTheOldAddressANoticeWithNoLink(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "old@example.com")
	wantStatus(t, call(t, s, "PATCH", userPath(acc.ID, usr.ID), key,
		map[string]any{"email": "new@example.com", "verification": "none"}), 200)
	sent := rec.Messages()
	if len(sent) != 1 || sent[0].To != "old@example.com" {
		t.Fatalf("mail = %+v, want one notice to the old address", sent)
	}
	body := sent[0].Body
	if strings.Contains(body, "http") || strings.Contains(body, "#token=") || strings.Contains(body, "new@example.com") {
		t.Errorf("the notice carries a link or the new address: %q", body)
	}
}

// TestEmailRequestsToOneAddressAreBudgeted: the new address of an email request is charged to mail.target, three an hour. The
// fourth request for it is refused as rate limited, and nothing changes: no mail, no pending address.
func TestEmailRequestsToOneAddressAreBudgeted(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "old@example.com")
	path := userPath(acc.ID, usr.ID)
	for i := 0; i < 3; i++ {
		wantStatus(t, call(t, s, "PATCH", path, key,
			map[string]any{"email": "target@example.com", "verification": "email"}), 200)
	}
	wantError(t, call(t, s, "PATCH", path, key,
		map[string]any{"email": "target@example.com", "verification": "email"}), 429, apierr.RateLimited)
	if n := messagesTo(rec, "target@example.com"); n != 3 {
		t.Errorf("%d mails to the target, want 3", n)
	}
	if got := emailOf(t, s, key, acc.ID, usr.ID); got.Email != "old@example.com" || got.PendingEmail == nil {
		t.Errorf("after the refusal the user reads %+v, want the old address and the earlier pending one", got)
	}
}

// TestNoticeBudgetRefusesTheChangeWhenSpent: with a mailer, changes that alternate between two addresses charge the old
// address of each change (mail.target, three an hour). The first six changes apply, each with its notice. The seventh would
// leave victim, whose budget is spent, so it is refused as rate limited before any write, and the address stays victim.
func TestNoticeBudgetRefusesTheChangeWhenSpent(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "victim@example.com")
	path := userPath(acc.ID, usr.ID)
	addresses := []string{"other@example.com", "victim@example.com"}
	for i := 0; i < 6; i++ {
		wantStatus(t, call(t, s, "PATCH", path, key,
			map[string]any{"email": addresses[i%2], "verification": "none"}), 200)
	}
	wantError(t, call(t, s, "PATCH", path, key,
		map[string]any{"email": "other@example.com", "verification": "none"}), 429, apierr.RateLimited)
	if got := emailOf(t, s, key, acc.ID, usr.ID); got.Email != "victim@example.com" {
		t.Errorf("after the refused seventh change the address is %s, want victim (unchanged)", got.Email)
	}
	if n := messagesTo(rec, "victim@example.com"); n != 3 {
		t.Errorf("%d notices to victim, want 3 (the budget)", n)
	}
	if n := messagesTo(rec, "other@example.com"); n != 3 {
		t.Errorf("%d notices to other, want 3 (the budget)", n)
	}
}

// TestConfirmRefusesAnAddressTakenSinceTheRequest: another user takes the pending address after the request. The
// confirmation answers conflict, and the link is not used up, so it answers conflict again rather than invalid_request.
func TestConfirmRefusesAnAddressTakenSinceTheRequest(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	a, _ := verifiedUser(t, s, key, acc.ID, "a@example.com")
	b, _ := verifiedUser(t, s, key, acc.ID, "b@example.com")
	pending := decode[userAnswer](t, call(t, s, "PATCH", userPath(acc.ID, a.ID), key,
		map[string]any{"email": "taken@example.com", "verification": "link"}))
	wantStatus(t, call(t, s, "PATCH", userPath(acc.ID, b.ID), key,
		map[string]any{"email": "taken@example.com", "verification": "none"}), 200)
	confirm := map[string]any{"token": pending.Verification.Token}
	// The explicit check at confirm time names its own reason, so it is told apart from the same conflict at request time.
	for i := 0; i < 2; i++ {
		e := wantError(t, call(t, s, "POST", "/api/v1/auth/email-change/confirm", "", confirm), 409, apierr.Conflict)
		if e.Details["reason"] != "email_taken" {
			t.Errorf("confirm %d refused with reason %v, want email_taken", i+1, e.Details["reason"])
		}
	}
	if got := emailOf(t, s, key, acc.ID, a.ID); got.Email != "a@example.com" || got.PendingEmail == nil {
		t.Errorf("after the refused confirmation a reads %+v, want its old address and the pending one", got)
	}
}

// TestConfirmForADisabledUserIsRefused: a user disabled while a change is pending is refused with user_disabled.
func TestConfirmForADisabledUserIsRefused(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "old@example.com")
	pending := decode[userAnswer](t, call(t, s, "PATCH", userPath(acc.ID, usr.ID), key,
		map[string]any{"email": "new@example.com", "verification": "link"}))
	wantStatus(t, call(t, s, "PATCH", userPath(acc.ID, usr.ID), key, map[string]any{"status": "disabled"}), 200)
	wantError(t, call(t, s, "POST", "/api/v1/auth/email-change/confirm", "",
		map[string]any{"token": pending.Verification.Token}), 403, apierr.UserDisabled)
}

// TestConfirmAfterDeleteFailsByCascade: DELETE removes the user's verification rows with the user (a cascade), so the link
// finds no row and answers invalid_request. The cascade refuses it here, not the confirm's own user lookup.
func TestConfirmAfterDeleteFailsByCascade(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "old@example.com")
	pending := decode[userAnswer](t, call(t, s, "PATCH", userPath(acc.ID, usr.ID), key,
		map[string]any{"email": "new@example.com", "verification": "link"}))
	wantStatus(t, call(t, s, "DELETE", userPath(acc.ID, usr.ID), key, nil), 204)
	wantError(t, call(t, s, "POST", "/api/v1/auth/email-change/confirm", "",
		map[string]any{"token": pending.Verification.Token}), 400, apierr.InvalidRequest)
}

// TestConfirmIsAuditedAsTheUsersOwnAct: a confirmation writes user.email_changed with the new address, the confirmation as
// its way, and the user as the actor.
func TestConfirmIsAuditedAsTheUsersOwnAct(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "old@example.com")
	pending := decode[userAnswer](t, call(t, s, "PATCH", userPath(acc.ID, usr.ID), key,
		map[string]any{"email": "new@example.com", "verification": "link"}))
	wantStatus(t, call(t, s, "POST", "/api/v1/auth/email-change/confirm", "",
		map[string]any{"token": pending.Verification.Token}), 200)
	entries, _, err := s.store.AuditLog(context.Background(), acc.ID, "", true, store.Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, e := range entries {
		if e.Action != "user.email_changed" || e.Details["verification"] != "confirmation" {
			continue
		}
		found++
		if e.ActorKind != "user" || e.ActorID != usr.ID || e.Target != usr.ID || e.Details["email"] != "new@example.com" {
			t.Errorf("confirm entry = actor %s/%s target %s details %v", e.ActorKind, e.ActorID, e.Target, e.Details)
		}
	}
	if found != 1 {
		t.Errorf("%d confirmation entries, want 1", found)
	}
}

// TestConfirmForASuspendedAccountIsRefused: a suspended account takes no confirmation (the same refusal as its sessions).
func TestConfirmForASuspendedAccountIsRefused(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "old@example.com")
	pending := decode[userAnswer](t, call(t, s, "PATCH", userPath(acc.ID, usr.ID), key,
		map[string]any{"email": "new@example.com", "verification": "link"}))
	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+acc.ID, admin, map[string]any{"status": "suspended"}), 200)
	wantError(t, call(t, s, "POST", "/api/v1/auth/email-change/confirm", "",
		map[string]any{"token": pending.Verification.Token}), 403, apierr.AccountSuspended)
}

// TestConfirmForADeletingAccountIsRefused: an account being deleted takes no confirmation, with the conflict writableAccount
// gives for it.
func TestConfirmForADeletingAccountIsRefused(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "old@example.com")
	pending := decode[userAnswer](t, call(t, s, "PATCH", userPath(acc.ID, usr.ID), key,
		map[string]any{"email": "new@example.com", "verification": "link"}))
	wantStatus(t, call(t, s, "DELETE", "/api/v1/accounts/"+acc.ID, admin, nil), 202)
	wantError(t, call(t, s, "POST", "/api/v1/auth/email-change/confirm", "",
		map[string]any{"token": pending.Verification.Token}), 409, apierr.Conflict)
}

// TestPendingAddressReadsAsNoneAfterItsLinkExpires: a pending address whose link has expired is not shown as pending; the
// address that signs in is unchanged.
func TestPendingAddressReadsAsNoneAfterItsLinkExpires(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "old@example.com")
	s.cfg.Users.VerificationTTL = config.Duration(20 * time.Millisecond)
	wantStatus(t, call(t, s, "PATCH", userPath(acc.ID, usr.ID), key,
		map[string]any{"email": "new@example.com", "verification": "link"}), 200)
	if got := emailOf(t, s, key, acc.ID, usr.ID); got.PendingEmail == nil {
		t.Fatal("a live request reads without its pending address")
	}
	time.Sleep(60 * time.Millisecond)
	if got := emailOf(t, s, key, acc.ID, usr.ID); got.PendingEmail != nil || got.Email != "old@example.com" {
		t.Errorf("after the link expired the user reads %+v, want no pending address", got)
	}
}

// TestNoneVerifiesAPendingUserAsCreateDoes: create with verification none makes a user active and verified; a none change on
// a user who never verified does the same, since the caller vouches for the address as it does for create. Its earlier
// verification link stops working.
func TestNoneVerifiesAPendingUserAsCreateDoes(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	created := decode[userResponse](t, call(t, s, "POST", "/api/v1/accounts/"+acc.ID+"/users", key,
		map[string]any{"email": "p@example.com", "verification": "link"}))
	wantStatus(t, call(t, s, "PATCH", userPath(acc.ID, created.ID), key,
		map[string]any{"email": "q@example.com", "verification": "none"}), 200)
	got := decode[struct {
		Status     string  `json:"status"`
		VerifiedAt *string `json:"verified_at"`
	}](t, call(t, s, "GET", userPath(acc.ID, created.ID), key, nil))
	if got.Status != store.UserActive || got.VerifiedAt == nil {
		t.Errorf("after a none change the user reads status %s, verified_at %v; want active and verified", got.Status, got.VerifiedAt)
	}
	wantError(t, call(t, s, "POST", "/api/v1/auth/verify", "",
		map[string]any{"token": created.Verification.Token, "password": testPassword}), 400, apierr.InvalidRequest)
}
