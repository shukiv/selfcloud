// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// sec2 (decision 86): with the email's cap spent and no device cookie, a sign-in does not refuse. Every attempt gets the
// same confirmation answer, and only a right password mails a single-use link that signs the browser in.

// confirmCookieName is the nonce cookie the attempt sets, bound to the browser that asked.
const confirmCookieName = "selfcloud_confirm"

// shapeOf is what an answer shows a guesser: its status, its body, and its headers, except the per-request ID and the
// Date, and except the value of a cookie (a random nonce differs from one attempt to the next, and says nothing).
func shapeOf(rec *httptest.ResponseRecorder) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d\n%s\n", rec.Code, rec.Body.String())
	keys := make([]string, 0, len(rec.Header()))
	for k := range rec.Header() {
		if k == "X-Request-Id" || k == "Date" {
			continue
		}
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		for _, v := range rec.Header()[k] {
			if k == "Set-Cookie" {
				name, rest, _ := strings.Cut(v, "=")
				_, attrs, _ := strings.Cut(rest, ";")
				v = name + "=<value>;" + attrs
			}
			fmt.Fprintf(&b, "%s: %s\n", k, v)
		}
	}
	return b.String()
}

// confirmMailTo returns the device-confirmation mails sent to addr.
func confirmMailTo(rec *mail.Recorder, addr string) []mail.Message {
	var out []mail.Message
	for _, m := range rec.Messages() {
		if m.To == addr && strings.Contains(m.Body, "/confirm-device#token=") {
			out = append(out, m)
		}
	}
	return out
}

// answerCookie returns the cookie an answer set under name, or nil.
func answerCookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// redeemConfirm posts a device-confirmation link's token from addr, with the cookies given.
func redeemConfirm(t *testing.T, s *Server, addr, token string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	return postFrom(t, s, addr, "/api/v1/auth/device-confirm", map[string]any{"token": token}, cookies...)
}

// signInAsked posts a sign-in from addr with the given password while the cap is spent.
func signInAsked(t *testing.T, s *Server, addr, email, password string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	return loginWith(t, s, addr, email, password, cookies...)
}

// The answer to a wrong password, a right one, and an unknown email is the same, byte for byte apart from the parts
// shapeOf leaves out. Only the right password mails.
func TestCapSpentAnswersAreTheSameWhateverThePassword(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	spendCap(t, s, "ann@example.com", 80)
	spendCap(t, s, "nobody@example.com", 81)

	wrong := signInAsked(t, s, "10.80.1.1:4000", "ann@example.com", "wrong password here")
	right := signInAsked(t, s, "10.80.1.2:4000", "ann@example.com", testPassword)
	unknown := signInAsked(t, s, "10.80.1.3:4000", "nobody@example.com", testPassword)
	for _, r := range []*httptest.ResponseRecorder{wrong, right, unknown} {
		wantStatus(t, r, 202)
		if got := strings.TrimSpace(r.Body.String()); got != `{"confirm":"email"}` {
			t.Errorf("body = %s, want the confirmation answer", got)
		}
	}
	if a, b := shapeOf(wrong), shapeOf(right); a != b {
		t.Errorf("wrong and right passwords differ:\n%s\n---\n%s", a, b)
	}
	if a, b := shapeOf(wrong), shapeOf(unknown); a != b {
		t.Errorf("known and unknown emails differ:\n%s\n---\n%s", a, b)
	}
	if answerCookie(right, confirmCookieName) == nil || answerCookie(wrong, confirmCookieName) == nil {
		t.Error("the attempts must set the confirmation cookie, right or wrong")
	}
	s.bg.Wait()
	if n := len(confirmMailTo(rec, "ann@example.com")); n != 1 {
		t.Errorf("confirmation mails = %d, want 1 (only the right password mails)", n)
	}
	if n := len(rec.Messages()); n != 1 {
		t.Errorf("mails = %d, want 1", n)
	}
}

// A right password's link signs the browser in, as a sign-in does, and sets its device cookie. The attempt's own
// browser is the only one that can use it, and the link is single use.
func TestConfirmLinkSignsInTheBrowserThatAskedAndRemembersIt(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	spendCap(t, s, "ann@example.com", 82)

	ask := signInAsked(t, s, "10.82.1.1:4000", "ann@example.com", testPassword)
	wantStatus(t, ask, 202)
	nonce := answerCookie(ask, confirmCookieName)
	if nonce == nil {
		t.Fatal("no confirmation cookie in the answer")
	}
	s.bg.Wait()
	mails := confirmMailTo(rec, "ann@example.com")
	if len(mails) != 1 {
		t.Fatalf("confirmation mails = %d, want 1", len(mails))
	}
	// The mail names no password, no address and no user agent: only the link and its expiry.
	for _, leak := range []string{testPassword, "10.82.1.1", "Go-http-client"} {
		if strings.Contains(mails[0].Body, leak) {
			t.Errorf("the mail holds %q", leak)
		}
	}
	token := tokenFromMail(t, mails[0])

	res := redeemConfirm(t, s, "10.82.1.1:4000", token, nonce)
	wantStatus(t, res, 200)
	if answerCookie(res, s.sessionCookie) == nil {
		t.Error("the link did not set a session cookie")
	}
	dev := deviceCookieFrom(t, res)
	if ended := answerCookie(res, confirmCookieName); ended == nil || ended.MaxAge >= 0 {
		t.Error("the link did not end its confirmation cookie")
	}
	// The device cookie makes the next sign-in from a new browser work while the cap stays spent.
	wantStatus(t, signInAsked(t, s, "10.82.2.1:4000", "ann@example.com", testPassword, dev), 200)

	// Single use: the same link, in the same browser, is refused.
	wantError(t, redeemConfirm(t, s, "10.82.1.1:4000", token, nonce), 400, apierr.InvalidRequest)

	outcomes := map[string]int{}
	for _, e := range auditActionsOf(t, s, acc.ID, "user.device_confirm_sent") {
		outcomes[e.Outcome]++
	}
	if outcomes["attempted"] != 1 || outcomes["ok"] != 1 {
		t.Errorf("device_confirm_sent outcomes = %v, want one attempted and one ok", outcomes)
	}
	if n := len(auditActionsOf(t, s, acc.ID, "user.device_confirmed")); n != 1 {
		t.Errorf("device_confirmed rows = %d, want 1", n)
	}
}

// Opened in another browser, the link signs nothing in and does not use itself up, so the browser that asked still can.
func TestConfirmLinkNeedsTheBrowserThatAsked(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	spendCap(t, s, "ann@example.com", 83)

	other := signInAsked(t, s, "10.83.1.1:4000", "ann@example.com", "wrong password here")
	asked := signInAsked(t, s, "10.83.1.2:4000", "ann@example.com", testPassword)
	wantStatus(t, other, 202)
	wantStatus(t, asked, 202)
	s.bg.Wait()
	token := tokenFromMail(t, confirmMailTo(rec, "ann@example.com")[0])

	for _, c := range []*http.Cookie{nil, answerCookie(other, confirmCookieName)} {
		var cookies []*http.Cookie
		if c != nil {
			cookies = append(cookies, c)
		}
		refused := redeemConfirm(t, s, "10.83.1.3:4000", token, cookies...)
		e := wantError(t, refused, 403, apierr.Forbidden)
		if e.Details["reason"] != "other_browser" {
			t.Errorf("reason = %v, want other_browser", e.Details["reason"])
		}
		if answerCookie(refused, s.sessionCookie) != nil {
			t.Error("another browser was signed in by the link")
		}
	}
	wantStatus(t, redeemConfirm(t, s, "10.83.1.2:4000", token, answerCookie(asked, confirmCookieName)), 200)
}

// A link is refused after its life ends.
func TestConfirmLinkExpires(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	spendCap(t, s, "ann@example.com", 84)

	ask := signInAsked(t, s, "10.84.1.1:4000", "ann@example.com", testPassword)
	wantStatus(t, ask, 202)
	s.bg.Wait()
	token := tokenFromMail(t, confirmMailTo(rec, "ann@example.com")[0])
	// The store's clock moves past the link's fifteen minutes, so no sleep is needed.
	s.store.SetClock(func() time.Time { return time.Now().Add(16 * time.Minute) })
	wantError(t, redeemConfirm(t, s, "10.84.1.1:4000", token, answerCookie(ask, confirmCookieName)), 400, apierr.InvalidRequest)
}

// A password change after the mail voids the link, as it voids a device cookie.
func TestConfirmLinkIsVoidedByAPasswordReset(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	spendCap(t, s, "ann@example.com", 85)

	ask := signInAsked(t, s, "10.85.1.1:4000", "ann@example.com", testPassword)
	wantStatus(t, ask, 202)
	s.bg.Wait()
	token := tokenFromMail(t, confirmMailTo(rec, "ann@example.com")[0])

	// A reset is not refused by the cap, and it changes the password the link was sent for.
	wantStatus(t, remoteCall(t, s, "POST", "/api/v1/auth/password/forgot", "10.85.9.1:4000",
		map[string]any{"email": "ann@example.com"}), 202)
	s.bg.Wait()
	var resetToken string
	for _, m := range rec.Messages() {
		if strings.Contains(m.Body, "/reset#token=") {
			resetToken = tokenFromMail(t, m)
		}
	}
	const newPassword = "a new passphrase here"
	wantStatus(t, postFrom(t, s, "10.85.9.2:4000", "/api/v1/auth/password/reset",
		map[string]any{"token": resetToken, "password": newPassword}), 200)

	wantError(t, redeemConfirm(t, s, "10.85.1.1:4000", token, answerCookie(ask, confirmCookieName)), 400, apierr.InvalidRequest)
}

// The fourth right-password attempt in the window sends no mail, and gets the same answer as the others.
func TestConfirmMailCapSendsNoFourthMail(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	spendCap(t, s, "ann@example.com", 86)

	first := signInAsked(t, s, "10.86.1.1:4000", "ann@example.com", testPassword)
	wantStatus(t, first, 202)
	// Each attempt's job is waited for before the next: one job in flight per user.
	s.bg.Wait()
	for i := 2; i <= 4; i++ {
		r := signInAsked(t, s, fmt.Sprintf("10.86.1.%d:4000", i), "ann@example.com", testPassword)
		if a, b := shapeOf(first), shapeOf(r); a != b {
			t.Fatalf("attempt %d answers differently:\n%s\n---\n%s", i, a, b)
		}
		s.bg.Wait()
	}
	if n := len(confirmMailTo(rec, "ann@example.com")); n != 3 {
		t.Errorf("confirmation mails = %d, want 3 in the window", n)
	}
	sent := auditActionsOf(t, s, acc.ID, "user.device_confirm_sent")
	capped := 0
	for _, e := range sent {
		if e.Outcome == "capped" {
			capped++
		}
	}
	if capped != 1 {
		t.Errorf("capped rows = %d, want 1", capped)
	}
}

// A disabled user who types the right password gets the same answer and no mail: the link could not sign them in.
func TestDisabledUserWithTheRightPasswordGetsNoMail(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	ann, _ := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+acc.ID+"/users/"+ann.ID, key,
		map[string]any{"status": "disabled"}), 200)
	spendCap(t, s, "ann@example.com", 87)

	wantStatus(t, signInAsked(t, s, "10.87.1.1:4000", "ann@example.com", testPassword), 202)
	s.bg.Wait()
	if n := len(rec.Messages()); n != 0 {
		t.Errorf("mails = %d, want none for a disabled user", n)
	}
}

// The nonce is per browser, not per attempt: a link mailed for a right password still works in the same browser after a
// later wrong attempt from it, because that attempt keeps the browser's nonce. It guards that the nonce is per browser
// and is not replaced per attempt. (The no-mailer refusal is sec1's TestNoDeviceCookieIsRefusedWhileTheCapIsSpent.)
func TestEarlierLinkWorksAfterALaterWrongAttemptInTheSameBrowser(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	spendCap(t, s, "ann@example.com", 89)

	right := signInAsked(t, s, "10.89.1.1:4000", "ann@example.com", testPassword)
	wantStatus(t, right, 202)
	s.bg.Wait()
	token := tokenFromMail(t, confirmMailTo(rec, "ann@example.com")[0])
	nonce := answerCookie(right, confirmCookieName)

	// The same browser, now with a wrong password: it sends its cookie back and keeps its nonce.
	wrong := signInAsked(t, s, "10.89.1.1:4000", "ann@example.com", "wrong password here", nonce)
	wantStatus(t, wrong, 202)
	if got := answerCookie(wrong, confirmCookieName); got != nil && got.Value != nonce.Value {
		t.Fatal("a later attempt in the same browser replaced its nonce")
	}
	wantStatus(t, redeemConfirm(t, s, "10.89.1.1:4000", token, nonce), 200)
}

// wantConfirmAnswer checks the confirmation answer in full: 202 and its body, which is the same whatever the password.
func wantConfirmAnswer(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	wantStatus(t, rec, 202)
	if got := strings.TrimSpace(rec.Body.String()); got != `{"confirm":"email"}` {
		t.Fatalf("body = %s, want the confirmation answer", got)
	}
}

// setAccountStatus sets an account's status in the store, as an admin's change would.
func setAccountStatus(t *testing.T, s *Server, accID, status string) {
	t.Helper()
	ctx := context.Background()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		acc, err := tx.Account(ctx, accID)
		if err != nil {
			return err
		}
		acc.Status = status
		return tx.UpdateAccount(ctx, acc)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Across addresses: while an email's cap is spent, the email's password checks are bounded by its ceiling
// (rate_limit.capped_email_per_hour), whichever addresses make them. Past the ceiling no password is checked and no mail
// is sent, and the answer is the same: the request does one dummy check, as a past-limit request does. Here the ceiling
// is 2 and the checks come from three addresses, so the third address's right password is not checked. Each job is
// waited for before the next request: one job in flight per user.
func TestCappedEmailHasAHardLimitOnPasswordChecks(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec, func(c *config.Config) { c.RateLimit.CappedEmailPerHour = 2 })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	spendCap(t, s, "ann@example.com", 92)

	var real, dummy int
	s.verifyHook = func(isDummy bool) {
		if isDummy {
			dummy++
		} else {
			real++
		}
	}
	var answers []*httptest.ResponseRecorder
	for _, addr := range []string{"10.92.1.1:4000", "10.92.2.1:4000", "10.92.3.1:4000"} {
		answers = append(answers, signInAsked(t, s, addr, "ann@example.com", testPassword))
		s.bg.Wait()
	}
	for _, r := range answers {
		wantConfirmAnswer(t, r)
	}
	if a, b := shapeOf(answers[0]), shapeOf(answers[2]); a != b {
		t.Errorf("the answer past the ceiling differs:\n%s\n---\n%s", a, b)
	}
	if real != 2 || dummy != 1 {
		t.Errorf("password checks: %d real and %d dummy, want 2 and 1: the third address is past the ceiling", real, dummy)
	}
	if n := len(confirmMailTo(rec, "ann@example.com")); n != 2 {
		t.Errorf("confirmation mails = %d, want 2", n)
	}
}

// M2, the oracle: while the cap is spent, a right password and a wrong one get byte-identical answers, whether or not the
// per-email limit is spent. The shape is the status, the body, the headers, and each cookie's name and attributes.
func TestCappedRightAndWrongPasswordsAnswerTheSameBytes(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec, func(c *config.Config) { c.RateLimit.CappedPerEmailPerHour = 1 })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	spendCap(t, s, "ann@example.com", 90)

	within := signInAsked(t, s, "10.90.1.1:4000", "ann@example.com", testPassword) // the one check the limit allows
	// The same address: the budget is per email and address, so these two are past the limit.
	overWrong := signInAsked(t, s, "10.90.1.1:4000", "ann@example.com", "wrong password here")
	overRight := signInAsked(t, s, "10.90.1.1:4000", "ann@example.com", testPassword)
	for _, r := range []*httptest.ResponseRecorder{within, overWrong, overRight} {
		wantConfirmAnswer(t, r)
	}
	for _, r := range []*httptest.ResponseRecorder{overWrong, overRight} {
		if a, b := shapeOf(within), shapeOf(r); a != b {
			t.Errorf("answers differ:\n%s\n---\n%s", a, b)
		}
	}
	s.bg.Wait()
	if n := len(confirmMailTo(rec, "ann@example.com")); n != 1 {
		t.Errorf("confirmation mails = %d, want 1 (only the checked right password mails)", n)
	}
}

// The nonce cookie: HttpOnly, SameSite=Lax, Path=/, a fifteen-minute life, and Secure with the __Host- prefix over https.
// Over plain http it has the plain name and no Secure flag: the prefix follows Secure, and plain http cannot carry Secure.
func TestConfirmCookieAttributes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		https      bool
		tag        int
		wantName   string
		wantSecure bool
	}{
		{"plain http", false, 100, "selfcloud_confirm", false},
		{"https", true, 101, "__Host-selfcloud_confirm", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newAuthServer(t, &mail.Recorder{})
			if tc.https {
				s.sessionCookie = "__Host-selfcloud_session"
			}
			admin := adminKey(t, s)
			acc, key := newAccount(t, s, admin, "Acme")
			verifiedUser(t, s, key, acc.ID, "ann@example.com")
			spendCap(t, s, "ann@example.com", tc.tag)

			ask := signInAsked(t, s, fmt.Sprintf("10.%d.1.1:4000", tc.tag), "ann@example.com", testPassword)
			wantConfirmAnswer(t, ask)
			c := answerCookie(ask, tc.wantName)
			if c == nil {
				t.Fatalf("no %s cookie in the answer", tc.wantName)
			}
			if !c.HttpOnly || c.Path != "/" || c.SameSite != http.SameSiteLaxMode || c.Secure != tc.wantSecure || c.MaxAge != 900 {
				t.Errorf("cookie %+v: want HttpOnly, Path /, SameSite Lax, Secure %v, Max-Age 900", c, tc.wantSecure)
			}
		})
	}
}

// The redeem is limited per client address (device.confirm.ip): past the limit, the link is not looked at.
func TestDeviceConfirmRedeemIsLimitedPerAddress(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.RateLimit.PerIPPerMinute = 2 })
	for i := 0; i < 2; i++ {
		wantError(t, redeemConfirm(t, s, "10.94.1.1:4000", "not-a-token"), 400, apierr.InvalidRequest)
	}
	wantError(t, redeemConfirm(t, s, "10.94.1.1:4000", "not-a-token"), 429, apierr.RateLimited)
}

// The "Keep me signed in" choice of the attempt is carried into the session the link starts: unticked gives a
// browser-session cookie, ticked a kept one.
func TestTheKeepSignedInChoiceCarriesIntoTheLinkSession(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	spendCap(t, s, "ann@example.com", 95)

	for _, tc := range []struct {
		tag      int
		remember bool
	}{{1, false}, {2, true}} {
		addr := fmt.Sprintf("10.95.%d.1:4000", tc.tag)
		ask := postFrom(t, s, addr, "/api/v1/auth/login",
			map[string]any{"email": "ann@example.com", "password": testPassword, "remember": tc.remember})
		wantConfirmAnswer(t, ask)
		s.bg.Wait()
		mails := confirmMailTo(rec, "ann@example.com")
		token := tokenFromMail(t, mails[len(mails)-1])
		res := redeemConfirm(t, s, addr, token, answerCookie(ask, confirmCookieName))
		wantStatus(t, res, 200)
		sess := answerCookie(res, s.sessionCookie)
		if sess == nil {
			t.Fatal("no session cookie")
		}
		if kept := sess.MaxAge > 0; kept != tc.remember {
			t.Errorf("remember %v: the session is kept %v, want %v", tc.remember, kept, tc.remember)
		}
	}
}

// The link refuses a suspended account with account_suspended, and keeps the link: once the account is active again the
// same link signs in. A deleting account refuses a link as an unknown one, with invalid_request.
func TestTheLinkRefusesASuspendedOrDeletingAccount(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	spendCap(t, s, "ann@example.com", 96)

	ask := signInAsked(t, s, "10.96.1.1:4000", "ann@example.com", testPassword)
	wantConfirmAnswer(t, ask)
	s.bg.Wait()
	token := tokenFromMail(t, confirmMailTo(rec, "ann@example.com")[0])
	cookie := answerCookie(ask, confirmCookieName)

	setAccountStatus(t, s, acc.ID, store.AccountSuspended)
	if e := decodeError(t, redeemConfirm(t, s, "10.96.1.1:4000", token, cookie)); e.Code != apierr.AccountSuspended {
		t.Errorf("suspended account: code %s, want account_suspended", e.Code)
	}
	setAccountStatus(t, s, acc.ID, store.AccountActive)
	wantStatus(t, redeemConfirm(t, s, "10.96.1.1:4000", token, cookie), 200)

	ask2 := signInAsked(t, s, "10.96.2.1:4000", "ann@example.com", testPassword)
	wantConfirmAnswer(t, ask2)
	s.bg.Wait()
	mails := confirmMailTo(rec, "ann@example.com")
	token2 := tokenFromMail(t, mails[len(mails)-1])
	setAccountStatus(t, s, acc.ID, store.AccountDeleting)
	if e := decodeError(t, redeemConfirm(t, s, "10.96.2.1:4000", token2, answerCookie(ask2, confirmCookieName))); e.Code != apierr.InvalidRequest {
		t.Errorf("deleting account: code %s, want invalid_request", e.Code)
	}
}

// M3: an installation admin gets the same answer as anyone in the capped state, so the answer shows nothing about which
// addresses are admins' (a 429 for them would). The password is still checked, and no mail is sent to an admin: an
// admin with a right password gets no link, and nothing in the answer says so. The three answers below must be
// byte-identical: status, body and cookies (shapeOf masks the cookie values).
func TestInstallationAdminAnswersAsAnyoneInTheCappedState(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	newAdminUser(t, s, admin, "ada@example.com")
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	spendCap(t, s, "ada@example.com", 97)
	spendCap(t, s, "ann@example.com", 98)

	adminRight := signInAsked(t, s, "10.97.1.1:4000", "ada@example.com", testPassword)
	adminWrong := signInAsked(t, s, "10.97.1.2:4000", "ada@example.com", "wrong password here")
	userWrong := signInAsked(t, s, "10.98.1.1:4000", "ann@example.com", "wrong password here")
	for _, r := range []*httptest.ResponseRecorder{adminRight, adminWrong, userWrong} {
		wantConfirmAnswer(t, r)
	}
	for _, r := range []*httptest.ResponseRecorder{adminWrong, userWrong} {
		if a, b := shapeOf(adminRight), shapeOf(r); a != b {
			t.Errorf("answers differ:\n%s\n---\n%s", a, b)
		}
	}
	s.bg.Wait()
	if n := len(confirmMailTo(rec, "ada@example.com")); n != 0 {
		t.Errorf("confirmation mails to the admin = %d, want none", n)
	}
}

// L4: the mail cap counts the address, not each account that matched. An email in two accounts, with the same password in
// both, gets three mails in the window, not three per account.
func TestConfirmMailCapCountsTheAddressAcrossAccounts(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	accA, keyA := newAccount(t, s, admin, "Acme")
	accB, keyB := newAccount(t, s, admin, "Beta")
	verifiedUser(t, s, keyA, accA.ID, "ann@example.com")
	verifiedUser(t, s, keyB, accB.ID, "ann@example.com")
	spendCap(t, s, "ann@example.com", 99)

	for i := 1; i <= 4; i++ {
		// Each attempt's jobs are waited for before the next: one job in flight per user.
		wantConfirmAnswer(t, signInAsked(t, s, fmt.Sprintf("10.99.1.%d:4000", i), "ann@example.com", testPassword))
		s.bg.Wait()
	}
	if n := len(confirmMailTo(rec, "ann@example.com")); n != 3 {
		t.Errorf("confirmation mails = %d, want 3 to the address in the window", n)
	}
}

// auditLogRows counts the rows of the audit log, read through the store's file, so a count taken around one request is
// the request's own writes.
func auditLogRows(t *testing.T, s *Server) int {
	t.Helper()
	db, err := sql.Open("sqlite", s.cfg.Store.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM audit_log").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// lockedBuffer collects log lines written from several goroutines.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// gatedSender records mail as mail.Recorder does, but a send waits until gate is closed, so a test can hold a job in
// flight.
type gatedSender struct {
	rec  *mail.Recorder
	gate chan struct{}
}

func (g *gatedSender) Send(ctx context.Context, m mail.Message) error {
	<-g.gate
	return g.rec.Send(ctx, m)
}

// Round 5: when the email ceiling refuses a request, the address budget the request took first is given back (chargeAll
// cancels it), so the address keeps its token. The ceiling here is one, spent by the first address; the second address
// is refused by it, and its own budget must still hold its one token.
func TestAnEmailCeilingRefusalGivesBackTheAddressBudget(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) {
		c.RateLimit.CappedPerEmailPerHour = 1
		c.RateLimit.CappedEmailPerHour = 1
	})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	spendCap(t, s, "ann@example.com", 200)

	wantConfirmAnswer(t, signInAsked(t, s, "10.201.1.1:4000", "ann@example.com", "wrong password here"))
	wantConfirmAnswer(t, signInAsked(t, s, "10.202.1.1:4000", "ann@example.com", "wrong password here"))

	// The address key is the address and the lowercased email, joined by a NUL (addressKey).
	b := s.limits["login.capped"].buckets["10.202.1.1\x00ann@example.com"]
	if b == nil {
		t.Fatal("the second address's budget was never taken, so the refusal path did not run")
	}
	// Read at the limiter's clock, which the tests freeze: time.Now would refill the bucket and hide the refund.
	if got := b.lim.TokensAt(s.limitClock()); got < 0.5 {
		t.Errorf("address budget holds %.2f tokens after the ceiling refused, want 1: the refusal must give it back", got)
	}
}

// Round 6: the audit source a job uses shares nothing with the request's principal. A change to the principal, its token
// scope (including the folder list) or its link scope after the copy does not reach the copy.
func TestAuditSourceSharesNothingWithThePrincipal(t *testing.T) {
	t.Parallel()
	p := &auth.Principal{Kind: auth.KindUser, ID: "u1",
		Token: &auth.TokenScope{Folders: []string{"f1"}, Audience: "api"},
		Link:  &auth.LinkScope{Node: "n1", Role: "viewer"}}
	cp := clonePrincipal(p)
	p.ID = "changed"
	p.Token.Folders[0] = "changed"
	p.Token.Audience = "changed"
	p.Link.Node = "changed"
	if cp.ID != "u1" || cp.Token.Folders[0] != "f1" || cp.Token.Audience != "api" || cp.Link.Node != "n1" {
		t.Errorf("the copy changed with the principal: %+v, token %+v, link %+v", cp, cp.Token, cp.Link)
	}
}

// requestRows counts the audit rows a sign-in request writes itself: a failed sign-in and a confirmation requested. A
// confirmation job's rows are other actions, so they are not counted, whether the job ran or not.
func requestRows(t *testing.T, s *Server) int {
	t.Helper()
	db, err := sql.Open("sqlite", s.cfg.Store.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM audit_log WHERE action IN ('user.login_failed', 'user.login_confirm_requested')").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The timing cases, with an accepted job: a miss, a match, a match on two accounts and an admin's right password
// each write the same number of rows inside the request, before the answer. Two modes: every job dropped (a full queue),
// and every job accepted. The job's own rows come after the answer and are not counted (requestRows).
func TestEveryAnswerWritesTheSameRowsBeforeTheAnswer(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	accA, keyA := newAccount(t, s, admin, "Acme")
	accB, keyB := newAccount(t, s, admin, "Beta")
	verifiedUser(t, s, keyA, accA.ID, "miss@example.com")
	verifiedUser(t, s, keyA, accA.ID, "one@example.com")
	verifiedUser(t, s, keyA, accA.ID, "two@example.com")
	verifiedUser(t, s, keyB, accB.ID, "two@example.com")
	newAdminUser(t, s, admin, "ada@example.com")
	for i, email := range []string{"miss@example.com", "one@example.com", "two@example.com", "ada@example.com"} {
		spendCap(t, s, email, 110+i)
	}
	cases := []struct {
		name, email, password string
		tag                   int
	}{
		{"a miss", "miss@example.com", "wrong password here", 120},
		{"a match", "one@example.com", testPassword, 121},
		{"a match on two accounts", "two@example.com", testPassword, 122},
		{"an admin's right password", "ada@example.com", testPassword, 123},
	}
	for _, mode := range []struct {
		name  string
		slots int // -1 for a full queue
		off   int // added to each address, so the two modes use different addresses
	}{{"dropped jobs", -1, 0}, {"accepted jobs", deviceConfirmSlots, 30}} {
		if mode.slots < 0 {
			s.deviceSlots = make(chan struct{})
		} else {
			s.deviceSlots = make(chan struct{}, mode.slots)
		}
		for _, c := range cases {
			before := requestRows(t, s)
			wantConfirmAnswer(t, signInAsked(t, s, fmt.Sprintf("10.%d.9.9:4000", c.tag+mode.off), c.email, c.password))
			s.bg.Wait()
			if n := requestRows(t, s) - before; n != 1 {
				t.Errorf("%s, %s: the request writes %d audit rows before the answer, want 1", mode.name, c.name, n)
			}
		}
	}
}

// Round 3, login.capped is per email and client address. An attacker who spends the budget from its own address does
// not stop the real user's mail from another address: in the capped state the answers are the same, so a guess there
// gives the attacker nothing, and the limit only bounds password hashing.
func TestAnAttackersCappedBudgetDoesNotBlockTheRealUsersMail(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec, func(c *config.Config) { c.RateLimit.CappedPerEmailPerHour = 2 })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	spendCap(t, s, "ann@example.com", 130)
	for i := 0; i < 3; i++ {
		wantConfirmAnswer(t, signInAsked(t, s, "10.131.1.1:4000", "ann@example.com", "wrong password here"))
	}
	wantConfirmAnswer(t, signInAsked(t, s, "10.132.1.1:4000", "ann@example.com", testPassword))
	s.bg.Wait()
	if n := len(confirmMailTo(rec, "ann@example.com")); n != 1 {
		t.Errorf("mails = %d, want 1: the real user's address has a budget of its own", n)
	}
}

// Round 3: logout clears the pending confirmation cookie, so a link mailed to this browser cannot be redeemed in it after
// sign-out. Without the clearing, the cookie would live on for its fifteen minutes.
func TestLogoutClearsThePendingConfirmCookie(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	spendCap(t, s, "ann@example.com", 140)
	wantConfirmAnswer(t, signInAsked(t, s, "10.141.1.1:4000", "ann@example.com", testPassword))
	s.bg.Wait()
	token := tokenFromMail(t, confirmMailTo(rec, "ann@example.com")[0])

	out := callSession(t, s, sess, "POST", "/api/v1/auth/logout", nil)
	wantStatus(t, out, 204)
	cleared := answerCookie(out, s.confirmCookieName())
	if cleared == nil || cleared.Value != "" || cleared.MaxAge >= 0 {
		t.Fatalf("logout does not clear the confirmation cookie: %+v", cleared)
	}
	// The Set-Cookie above is the logout change: it is the assertion that tests logout. The refusal below is a consequence
	// check only. The test client sends no confirm cookie here, so the nonce check refuses whether or not logout clears it.
	wantError(t, redeemConfirm(t, s, "10.141.1.1:4000", token), 403, apierr.Forbidden)
}

// Round 3: at most one confirmation job is in flight per user. A second matching request while the first is still
// sending is dropped as a duplicate: the same answer, a log line, and no second mail. Once the first has finished, the
// next request for the user is queued again.
func TestOneConfirmationJobIsInFlightPerUser(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	gate := make(chan struct{})
	s := newAuthServer(t, &gatedSender{rec: rec, gate: gate})
	var logs lockedBuffer
	s.log = slog.New(slog.NewTextHandler(&logs, nil))
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	spendCap(t, s, "ann@example.com", 150)

	first := signInAsked(t, s, "10.151.1.1:4000", "ann@example.com", testPassword)
	second := signInAsked(t, s, "10.152.1.1:4000", "ann@example.com", testPassword)
	wantConfirmAnswer(t, first)
	wantConfirmAnswer(t, second)
	if a, b := shapeOf(first), shapeOf(second); a != b {
		t.Errorf("answers differ while a job is in flight:\n%s\n---\n%s", a, b)
	}
	close(gate)
	s.bg.Wait()
	if n := len(confirmMailTo(rec, "ann@example.com")); n != 1 {
		t.Fatalf("mails = %d, want 1: the second request is a duplicate", n)
	}
	if !strings.Contains(logs.String(), "already in flight") {
		t.Errorf("no log line for the duplicate:\n%s", logs.String())
	}
	wantConfirmAnswer(t, signInAsked(t, s, "10.153.1.1:4000", "ann@example.com", testPassword))
	s.bg.Wait()
	if n := len(confirmMailTo(rec, "ann@example.com")); n != 2 {
		t.Errorf("mails after the first job finished = %d, want 2", n)
	}
}

// Round 3: with every slot busy, the confirmation is dropped and logged, and the answer is the same as a miss's.
func TestAFullQueueDropsTheMailWithTheSameAnswer(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	var logs lockedBuffer
	s.log = slog.New(slog.NewTextHandler(&logs, nil))
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	spendCap(t, s, "ann@example.com", 160)
	s.deviceSlots = make(chan struct{}) // every slot is busy
	dropped := signInAsked(t, s, "10.161.1.1:4000", "ann@example.com", testPassword)
	miss := signInAsked(t, s, "10.162.1.1:4000", "ann@example.com", "wrong password here")
	wantConfirmAnswer(t, dropped)
	wantConfirmAnswer(t, miss)
	if a, b := shapeOf(dropped), shapeOf(miss); a != b {
		t.Errorf("a dropped mail's answer differs from a miss's:\n%s\n---\n%s", a, b)
	}
	s.bg.Wait()
	if n := len(confirmMailTo(rec, "ann@example.com")); n != 0 {
		t.Errorf("mails = %d, want none with no slot free", n)
	}
	if !strings.Contains(logs.String(), "dropped") {
		t.Errorf("no log line for the dropped mail:\n%s", logs.String())
	}
}

// Round 3: shutdown waits for in-flight confirmation jobs (Serve waits on the background group). A job still sending
// when the wait starts is finished before the wait returns.
func TestTheBackgroundWaitCoversAnInFlightConfirmationMail(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	gate := make(chan struct{})
	s := newAuthServer(t, &gatedSender{rec: rec, gate: gate})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	spendCap(t, s, "ann@example.com", 170)
	wantConfirmAnswer(t, signInAsked(t, s, "10.171.1.1:4000", "ann@example.com", testPassword))
	done := make(chan struct{})
	go func() {
		s.bg.Wait()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("the wait returned while the mail was still being sent")
	case <-time.After(100 * time.Millisecond):
	}
	close(gate)
	<-done
	if n := len(confirmMailTo(rec, "ann@example.com")); n != 1 {
		t.Errorf("mails = %d, want the in-flight mail sent before the wait returned", n)
	}
}

// Round 3, the write-error path and the L1 refund: when a job's link write fails, the job logs the failure and sends no
// mail, and the mail token it took is given back, so the failed write spends nothing from the address's cap of three
// mails in fifteen minutes. Here the first of four confirmations fails; the three after it must all be mailed.
func TestAFailedLinkWriteGivesTheMailTokenBackAndLogs(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	var logs lockedBuffer
	s.log = slog.New(slog.NewTextHandler(&logs, nil))
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	spendCap(t, s, "ann@example.com", 180)
	// The clock steps two seconds on every read, so a refund that reads the clock again is at a later time than its
	// reservation. A later time restores nothing (rate.CancelAt), so the refund must use the reservation's own time.
	// The frozen clock of the other tests would hide that.
	var reads atomic.Int64
	s.limitClock = func() time.Time {
		return frozenLimitInstant.Add(time.Duration(reads.Add(1)) * 2 * time.Second)
	}
	// A trigger in the store's own file makes the first link write fail, and is dropped before the second, so the server
	// needs no test seam for it.
	db, err := sql.Open("sqlite", s.cfg.Store.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TRIGGER refuse_link BEFORE INSERT ON device_confirms BEGIN SELECT RAISE(ABORT, 'write refused'); END"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 4; i++ {
		if i == 2 {
			if _, err := db.Exec("DROP TRIGGER refuse_link"); err != nil {
				t.Fatal(err)
			}
		}
		// Each job is waited for before the next request: one job in flight per user.
		wantConfirmAnswer(t, signInAsked(t, s, fmt.Sprintf("10.%d.1.1:4000", 180+i), "ann@example.com", testPassword))
		s.bg.Wait()
	}
	if n := len(confirmMailTo(rec, "ann@example.com")); n != 3 {
		t.Errorf("mails = %d, want 3: the failed write's token must be given back", n)
	}
	if !strings.Contains(logs.String(), "cannot write the device confirmation") {
		t.Errorf("no log line for the failed write:\n%s", logs.String())
	}
}
