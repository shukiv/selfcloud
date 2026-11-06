// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/mail"
)

// Decision 86, L5: an email change voids the user's open device-confirm links, in the same transaction as the change. A
// link mailed before the change is refused after it. Two changes apply an address: one at once (verification none), and
// the confirmation of a change with a link.

// TestAnEmailChangeAtOnceVoidsThePendingDeviceConfirmLink: an account key changes the address at once. The link mailed to
// the old address before the change is refused afterwards.
func TestAnEmailChangeAtOnceVoidsThePendingDeviceConfirmLink(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "old@example.com")
	spendCap(t, s, "old@example.com", 230)

	ask := signInAsked(t, s, "10.231.1.1:4000", "old@example.com", testPassword)
	wantConfirmAnswer(t, ask)
	s.bg.Wait()
	token := tokenFromMail(t, confirmMailTo(rec, "old@example.com")[0])
	nonce := answerCookie(ask, s.confirmCookieName())

	changed := call(t, s, "PATCH", userPath(acc.ID, usr.ID), key,
		map[string]any{"email": "new@example.com", "verification": "none"})
	wantStatus(t, changed, 200)

	wantError(t, redeemConfirm(t, s, "10.231.1.1:4000", token, nonce), 400, apierr.InvalidRequest)
}

// TestAConfirmedLinkChangeVoidsThePendingDeviceConfirmLink: a change with a link waits for its confirmation. The
// device-confirm link mailed before the confirmation is refused after it.
func TestAConfirmedLinkChangeVoidsThePendingDeviceConfirmLink(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usr, _ := verifiedUser(t, s, key, acc.ID, "old@example.com")
	spendCap(t, s, "old@example.com", 240)

	ask := signInAsked(t, s, "10.241.1.1:4000", "old@example.com", testPassword)
	wantConfirmAnswer(t, ask)
	s.bg.Wait()
	token := tokenFromMail(t, confirmMailTo(rec, "old@example.com")[0])
	nonce := answerCookie(ask, s.confirmCookieName())

	requested := call(t, s, "PATCH", userPath(acc.ID, usr.ID), key,
		map[string]any{"email": "new@example.com", "verification": "link"})
	wantStatus(t, requested, 200)
	confirm := decode[userAnswer](t, requested).Verification
	if confirm == nil || confirm.Token == "" {
		t.Fatal("the change with a link answers no confirmation token")
	}
	wantStatus(t, call(t, s, "POST", "/api/v1/auth/email-change/confirm", "", map[string]any{"token": confirm.Token}), 200)

	wantError(t, redeemConfirm(t, s, "10.241.1.1:4000", token, nonce), 400, apierr.InvalidRequest)
}
