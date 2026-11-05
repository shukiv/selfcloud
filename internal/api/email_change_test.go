// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// userAnswer is the part of a user's answer that the email tests read.
type userAnswer struct {
	ID           string            `json:"id"`
	Email        string            `json:"email"`
	PendingEmail *string           `json:"pending_email"`
	Verification *verificationJSON `json:"verification,omitempty"`
}

// userPath is the path of one user of an account.
func userPath(accID, usrID string) string {
	return "/api/v1/accounts/" + accID + "/users/" + usrID
}

// emailOf reads a user with a key and returns its answer.
func emailOf(t *testing.T, s *Server, key, accID, usrID string) userAnswer {
	t.Helper()
	rec := call(t, s, "GET", userPath(accID, usrID), key, nil)
	wantStatus(t, rec, 200)
	return decode[userAnswer](t, rec)
}

// signsIn reports whether email signs in to accID with testPassword.
func signsIn(t *testing.T, s *Server, email, accID string) bool {
	t.Helper()
	return login(t, s, email, testPassword, accID).Code == 200
}

// TestKeyChangesEmailAtOnce: an account key with verification none changes the address at once. The user signs in with
// the new address and not the old one, keeps its personal folder, and its session still works.
func TestKeyChangesEmailAtOnce(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, sess := verifiedUser(t, s, key, acc.ID, "old@example.com")
	ctx := context.Background()
	before, err := s.store.PersonalFolder(ctx, acc.ID, usr.ID)
	if err != nil {
		t.Fatal(err)
	}

	rec := call(t, s, "PATCH", userPath(acc.ID, usr.ID), key, map[string]any{"email": "new@example.com", "verification": "none"})
	wantStatus(t, rec, 200)
	if got := decode[userAnswer](t, rec); got.Email != "new@example.com" || got.PendingEmail != nil || got.Verification != nil {
		t.Fatalf("answer = %+v, want the new address, nothing pending and no verification", got)
	}
	if !signsIn(t, s, "new@example.com", acc.ID) {
		t.Error("the new address does not sign in")
	}
	if signsIn(t, s, "old@example.com", acc.ID) {
		t.Error("the old address still signs in")
	}
	if rec := callSession(t, s, sess, "GET", "/api/v1/me", nil); rec.Code != 200 {
		t.Errorf("the session from before the change answers %d, want 200", rec.Code)
	}
	after, err := s.store.PersonalFolder(ctx, acc.ID, usr.ID)
	if err != nil || after.ID != before.ID {
		t.Errorf("personal folder after the change = %v, %v; want %s", after, err, before.ID)
	}
}

// TestAdminSessionChangesEmailOnlyWithItsPassword: an installation admin in a session needs its own password, and
// tenant access must be on. Without the password, or with a wrong one, nothing changes.
func TestAdminSessionChangesEmailOnlyWithItsPassword(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.Admin.TenantAccess = true })
	admin := adminKey(t, s)
	_, adminSess := newAdminUser(t, s, admin, "ann@example.com")
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "member@example.com")
	path := userPath(acc.ID, usr.ID)

	wantError(t, callSession(t, s, adminSess, "PATCH", path,
		map[string]any{"email": "new@example.com", "verification": "email"}), 403, apierr.Forbidden)
	wantError(t, callSession(t, s, adminSess, "PATCH", path,
		map[string]any{"email": "new@example.com", "verification": "email", "admin_password": "not the password"}), 403, apierr.Forbidden)
	if got := emailOf(t, s, key, acc.ID, usr.ID); got.Email != "member@example.com" || got.PendingEmail != nil {
		t.Fatalf("a refused change moved the address: %+v", got)
	}

	rec := callSession(t, s, adminSess, "PATCH", path,
		map[string]any{"email": "new@example.com", "verification": "email", "admin_password": testPassword})
	wantStatus(t, rec, 200)
	got := decode[userAnswer](t, rec)
	if got.Email != "member@example.com" || got.PendingEmail == nil || *got.PendingEmail != "new@example.com" {
		t.Errorf("answer = %+v, want the old address kept and the new one pending", got)
	}

	// A session may not make the change at once, even with the password.
	wantError(t, callSession(t, s, adminSess, "PATCH", path,
		map[string]any{"email": "other@example.com", "verification": "none", "admin_password": testPassword}), 403, apierr.Forbidden)
	// A link is for an API key, not a session; the session gets email only.
	wantError(t, callSession(t, s, adminSess, "PATCH", path,
		map[string]any{"email": "other@example.com", "verification": "link", "admin_password": testPassword}), 403, apierr.Forbidden)
}

// TestAdminKeyChangesEmailOnlyWithTenantAccess: an installation admin's key is refused with its reason while
// admin.tenant_access is off, and changes the address once it is on (decision 41). An account key is not gated by it.
func TestAdminKeyChangesEmailOnlyWithTenantAccess(t *testing.T) {
	t.Parallel()
	off := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, off)
	acc, key := newAccount(t, off, admin, "Acme")
	usr, _ := verifiedUser(t, off, key, acc.ID, "old@example.com")
	e := wantError(t, call(t, off, "PATCH", userPath(acc.ID, usr.ID), admin,
		map[string]any{"email": "new@example.com", "verification": "none"}), 403, apierr.Forbidden)
	if e.Details["reason"] != reasonTenantAccessOff {
		t.Errorf("admin key refused with reason %v, want %s", e.Details["reason"], reasonTenantAccessOff)
	}
	if got := emailOf(t, off, key, acc.ID, usr.ID); got.Email != "old@example.com" {
		t.Errorf("a refused admin key moved the address to %s", got.Email)
	}

	on := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.Admin.TenantAccess = true })
	admin = adminKey(t, on)
	acc, key = newAccount(t, on, admin, "Acme")
	usr, _ = verifiedUser(t, on, key, acc.ID, "old@example.com")
	wantStatus(t, call(t, on, "PATCH", userPath(acc.ID, usr.ID), admin,
		map[string]any{"email": "new@example.com", "verification": "none"}), 200)
	if got := emailOf(t, on, key, acc.ID, usr.ID); got.Email != "new@example.com" {
		t.Errorf("with tenant access on, the admin key's change did not apply: %+v", got)
	}
}

// TestAdminSessionEmailChangeNeedsTenantAccess: with admin.tenant_access off, an admin session may not change a user's
// address, whatever the password, and the refusal says why.
func TestAdminSessionEmailChangeNeedsTenantAccess(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	_, adminSess := newAdminUser(t, s, admin, "ann@example.com")
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "member@example.com")
	e := wantError(t, callSession(t, s, adminSess, "PATCH", userPath(acc.ID, usr.ID),
		map[string]any{"email": "new@example.com", "verification": "email", "admin_password": testPassword}), 403, apierr.Forbidden)
	if e.Details["reason"] != reasonTenantAccessOff {
		t.Errorf("refused with reason %v, want %s", e.Details["reason"], reasonTenantAccessOff)
	}
}

// TestOwnersMembersAndTokensCannotChangeEmail: an owner session, a member session and a delegated token each leave the
// address alone. An owner is refused with forbidden; the member and the token do not reach the account at all.
func TestOwnersMembersAndTokensCannotChangeEmail(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	makeOwner(t, s, key, acc.ID, "owner@example.com")
	ownerSess := sessionFrom(t, s, login(t, s, "owner@example.com", testPassword, acc.ID))
	member, memberSess := verifiedUser(t, s, key, acc.ID, "member@example.com")
	fld := folderOf(t, s, key, acc.ID)
	tok := mintToken(t, s, key, acc.ID, fld, member.ID)
	body := map[string]any{"email": "taken-over@example.com", "verification": "none"}
	path := userPath(acc.ID, member.ID)

	// An owner reaches the owner guard of checkEmailChange and is refused for its reason.
	e := wantError(t, callSession(t, s, ownerSess, "PATCH", path, body), 403, apierr.Forbidden)
	if e.Details["reason"] != reasonEmailCaller {
		t.Errorf("owner refused with reason %v, want %s", e.Details["reason"], reasonEmailCaller)
	}
	// A member and a delegated token never reach that guard: managedAccount refuses an account they do not manage.
	wantError(t, callSession(t, s, memberSess, "PATCH", path, body), 404, apierr.NotFound)
	wantError(t, call(t, s, "PATCH", path, tok, body), 404, apierr.NotFound)
	if got := emailOf(t, s, key, acc.ID, member.ID); got.Email != "member@example.com" || got.PendingEmail != nil {
		t.Errorf("a refused change moved the address: %+v", got)
	}
	if !signsIn(t, s, "member@example.com", acc.ID) || signsIn(t, s, "taken-over@example.com", acc.ID) {
		t.Error("sign-in does not show the address unchanged")
	}
}

// TestTakenSameAndInvalidAddresses: an address another user of the account has is a conflict, whatever its case. The
// user's own address is a no-op success, with no verification. A malformed address is refused.
func TestTakenSameAndInvalidAddresses(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	a, _ := verifiedUser(t, s, key, acc.ID, "a@example.com")
	b, _ := verifiedUser(t, s, key, acc.ID, "b@example.com")

	wantError(t, call(t, s, "PATCH", userPath(acc.ID, b.ID), key,
		map[string]any{"email": "A@example.com", "verification": "none"}), 409, apierr.Conflict)
	rec := call(t, s, "PATCH", userPath(acc.ID, a.ID), key, map[string]any{"email": "a@example.com", "verification": "link"})
	wantStatus(t, rec, 200)
	if got := decode[userAnswer](t, rec); got.Verification != nil || got.PendingEmail != nil || got.Email != "a@example.com" {
		t.Errorf("same address answered %+v, want a plain no-op", got)
	}
	wantError(t, call(t, s, "PATCH", userPath(acc.ID, a.ID), key,
		map[string]any{"email": "not an address", "verification": "none"}), 400, apierr.InvalidRequest)
	wantError(t, call(t, s, "PATCH", userPath(acc.ID, a.ID), key,
		map[string]any{"email": "Name <c@example.com>", "verification": "none"}), 400, apierr.InvalidRequest)
	wantError(t, call(t, s, "PATCH", userPath(acc.ID, a.ID), key,
		map[string]any{"email": "c@example.com", "verification": "maybe"}), 400, apierr.InvalidRequest)
}

// TestLinkChangeWaitsForConfirmation: with verification link the old address keeps working, and the new one is shown as
// pending. The confirmation applies it once; the same link does not work twice.
func TestLinkChangeWaitsForConfirmation(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "old@example.com")

	rec := call(t, s, "PATCH", userPath(acc.ID, usr.ID), key, map[string]any{"email": "new@example.com", "verification": "link"})
	wantStatus(t, rec, 200)
	got := decode[userAnswer](t, rec)
	if got.Email != "old@example.com" || got.PendingEmail == nil || *got.PendingEmail != "new@example.com" {
		t.Fatalf("answer = %+v, want the old address and the new one pending", got)
	}
	if got.Verification == nil || got.Verification.Token == "" || got.Verification.URL == "" {
		t.Fatalf("verification = %+v, want the link and its token", got.Verification)
	}
	if signsIn(t, s, "new@example.com", acc.ID) || !signsIn(t, s, "old@example.com", acc.ID) {
		t.Fatal("before confirmation the old address must sign in and the new one must not")
	}

	token := got.Verification.Token
	wantStatus(t, call(t, s, "POST", "/api/v1/auth/email-change/confirm", "", map[string]any{"token": token}), 200)
	if !signsIn(t, s, "new@example.com", acc.ID) || signsIn(t, s, "old@example.com", acc.ID) {
		t.Error("after confirmation the new address must sign in and the old one must not")
	}
	if after := emailOf(t, s, key, acc.ID, usr.ID); after.Email != "new@example.com" || after.PendingEmail != nil {
		t.Errorf("after confirmation the user reads %+v", after)
	}
	wantError(t, call(t, s, "POST", "/api/v1/auth/email-change/confirm", "", map[string]any{"token": token}), 400, apierr.InvalidRequest)
}

// TestNewerRequestReplacesThePendingChange: a second request replaces the pending address, and the first link stops
// working.
func TestNewerRequestReplacesThePendingChange(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "old@example.com")
	path := userPath(acc.ID, usr.ID)

	first := decode[userAnswer](t, call(t, s, "PATCH", path, key, map[string]any{"email": "second@example.com", "verification": "link"}))
	second := decode[userAnswer](t, call(t, s, "PATCH", path, key, map[string]any{"email": "third@example.com", "verification": "link"}))
	if first.Verification == nil || second.Verification == nil {
		t.Fatal("no verification in an answer")
	}
	wantError(t, call(t, s, "POST", "/api/v1/auth/email-change/confirm", "", map[string]any{"token": first.Verification.Token}),
		400, apierr.InvalidRequest)
	wantStatus(t, call(t, s, "POST", "/api/v1/auth/email-change/confirm", "", map[string]any{"token": second.Verification.Token}), 200)
	if got := emailOf(t, s, key, acc.ID, usr.ID); got.Email != "third@example.com" {
		t.Errorf("address = %s, want the newest request", got.Email)
	}
}

// TestExpiredChangeLinkIsRefused: a confirmation link past its time is refused, and the address does not change.
func TestExpiredChangeLinkIsRefused(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "old@example.com")
	s.cfg.Users.VerificationTTL = config.Duration(20 * time.Millisecond)

	got := decode[userAnswer](t, call(t, s, "PATCH", userPath(acc.ID, usr.ID), key,
		map[string]any{"email": "new@example.com", "verification": "link"}))
	if got.Verification == nil {
		t.Fatal("no verification in the answer")
	}
	time.Sleep(60 * time.Millisecond)
	wantError(t, call(t, s, "POST", "/api/v1/auth/email-change/confirm", "", map[string]any{"token": got.Verification.Token}),
		400, apierr.InvalidRequest)
	if after := emailOf(t, s, key, acc.ID, usr.ID); after.Email != "old@example.com" {
		t.Errorf("address = %s after an expired link, want the old one", after.Email)
	}
}

// TestResetLinkMadeBeforeTheChangeStopsWorking: a password-reset link issued before the address changed does not work
// after it.
func TestResetLinkMadeBeforeTheChangeStopsWorking(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "old@example.com")
	reset := decode[verificationJSON](t, call(t, s, "POST", userPath(acc.ID, usr.ID)+"/password-reset", key,
		map[string]any{"method": "link"}))
	if reset.Token == "" {
		t.Fatal("no reset token")
	}
	wantStatus(t, call(t, s, "PATCH", userPath(acc.ID, usr.ID), key,
		map[string]any{"email": "new@example.com", "verification": "none"}), 200)
	wantError(t, call(t, s, "POST", "/api/v1/auth/password/reset", "",
		map[string]any{"token": reset.Token, "password": "another correct horse"}), 400, apierr.InvalidRequest)
}

// TestNoticeGoesToTheOldAddressWhenMailIsSet: the confirmation goes to the new address, and once the change applies the
// old address gets a notice with no link. Without a mailer no notice is sent and the change still applies.
func TestNoticeGoesToTheOldAddressWhenMailIsSet(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "old@example.com")

	wantStatus(t, call(t, s, "PATCH", userPath(acc.ID, usr.ID), key,
		map[string]any{"email": "new@example.com", "verification": "email"}), 200)
	sent := rec.Messages()
	if len(sent) != 1 || sent[0].To != "new@example.com" {
		t.Fatalf("mail after the request = %+v, want one to the new address", sent)
	}
	wantStatus(t, call(t, s, "POST", "/api/v1/auth/email-change/confirm", "",
		map[string]any{"token": tokenFromMail(t, sent[0])}), 200)
	sent = rec.Messages()
	if len(sent) != 2 || sent[1].To != "old@example.com" {
		t.Fatalf("mail after the confirmation = %+v, want a notice to the old address", sent)
	}
	if strings.Contains(sent[1].Body, "#token=") || strings.Contains(sent[1].Body, "http") {
		t.Errorf("the notice carries a link: %q", sent[1].Body)
	}
	if strings.Contains(sent[1].Body, "new@example.com") {
		t.Errorf("the notice names the new address: %q", sent[1].Body)
	}

	quiet := newAuthServer(t, nil)
	quietAdmin := adminKey(t, quiet)
	quietAcc, quietKey := newAccount(t, quiet, quietAdmin, "Quiet")
	quietUser, _ := verifiedUser(t, quiet, quietKey, quietAcc.ID, "q@example.com")
	wantStatus(t, call(t, quiet, "PATCH", userPath(quietAcc.ID, quietUser.ID), quietKey,
		map[string]any{"email": "r@example.com", "verification": "none"}), 200)
	if got := emailOf(t, quiet, quietKey, quietAcc.ID, quietUser.ID); got.Email != "r@example.com" {
		t.Errorf("without mail the change did not apply: %+v", got)
	}
}

// TestEmailChangeIsAuditedWithTheNewAddress: the change writes user.email_changed with the new address, and the entry
// does not hold the old one.
func TestEmailChangeIsAuditedWithTheNewAddress(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "old@example.com")
	wantStatus(t, call(t, s, "PATCH", userPath(acc.ID, usr.ID), key,
		map[string]any{"email": "new@example.com", "verification": "none"}), 200)

	entries, _, err := s.store.AuditLog(context.Background(), acc.ID, "", true, store.Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, e := range entries {
		if e.Action != "user.email_changed" {
			continue
		}
		found++
		if e.Target != usr.ID || e.Details["email"] != "new@example.com" {
			t.Errorf("entry = %+v, want the user and the new address", e)
		}
		b, _ := json.Marshal(e.Details)
		if strings.Contains(string(b), "old@example.com") {
			t.Errorf("the entry holds the old address: %s", b)
		}
	}
	if found != 1 {
		t.Errorf("%d user.email_changed entries, want 1", found)
	}
}
