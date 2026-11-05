// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
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
	for i := 2; i <= 4; i++ {
		r := signInAsked(t, s, fmt.Sprintf("10.86.1.%d:4000", i), "ann@example.com", testPassword)
		if a, b := shapeOf(first), shapeOf(r); a != b {
			t.Fatalf("attempt %d answers differently:\n%s\n---\n%s", i, a, b)
		}
	}
	s.bg.Wait()
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

// M1: while an email's cap is spent, its password checks are limited (rate_limit.capped_per_email_per_hour). Past the
// limit an attempt sends no mail, and its answer is the same.
func TestCappedEmailHasAHardLimitOnPasswordChecks(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec, func(c *config.Config) { c.RateLimit.CappedPerEmailPerHour = 2 })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	spendCap(t, s, "ann@example.com", 92)

	first := signInAsked(t, s, "10.92.1.1:4000", "ann@example.com", testPassword)
	second := signInAsked(t, s, "10.92.1.2:4000", "ann@example.com", testPassword)
	third := signInAsked(t, s, "10.92.1.3:4000", "ann@example.com", testPassword)
	for _, r := range []*httptest.ResponseRecorder{first, second, third} {
		wantConfirmAnswer(t, r)
	}
	if a, b := shapeOf(first), shapeOf(third); a != b {
		t.Errorf("the answer past the limit differs:\n%s\n---\n%s", a, b)
	}
	s.bg.Wait()
	if n := len(confirmMailTo(rec, "ann@example.com")); n != 2 {
		t.Errorf("confirmation mails = %d, want 2 (the third password check is past the limit)", n)
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
	overWrong := signInAsked(t, s, "10.90.1.2:4000", "ann@example.com", "wrong password here")
	overRight := signInAsked(t, s, "10.90.1.3:4000", "ann@example.com", testPassword)
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
		wantConfirmAnswer(t, signInAsked(t, s, fmt.Sprintf("10.99.1.%d:4000", i), "ann@example.com", testPassword))
	}
	s.bg.Wait()
	if n := len(confirmMailTo(rec, "ann@example.com")); n != 3 {
		t.Errorf("confirmation mails = %d, want 3 to the address in the window", n)
	}
}
