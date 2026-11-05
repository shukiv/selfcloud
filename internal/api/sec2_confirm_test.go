// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
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
	s := newAuthServer(t, rec, func(c *config.Config) { c.Users.DeviceConfirmTTL = config.Duration(time.Second) })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	spendCap(t, s, "ann@example.com", 84)

	ask := signInAsked(t, s, "10.84.1.1:4000", "ann@example.com", testPassword)
	wantStatus(t, ask, 202)
	s.bg.Wait()
	token := tokenFromMail(t, confirmMailTo(rec, "ann@example.com")[0])
	time.Sleep(2 * time.Second)
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

// Without a mailer there is no link to send, so the answer stays sec1's refusal.
func TestWithoutAMailerTheCapStillRefuses(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, nil)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	spendCap(t, s, "ann@example.com", 88)

	e := wantError(t, loginWith(t, s, "10.88.1.1:4000", "ann@example.com", testPassword), 429, apierr.RateLimited)
	if !strings.Contains(e.Message, "Forgot password") {
		t.Errorf("message %q does not point at Forgot password", e.Message)
	}
}

// The nonce is per browser, not per attempt: a link mailed for a right password still works in the same browser after a
// later wrong attempt from it, because that attempt keeps the browser's nonce.
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
