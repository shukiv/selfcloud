// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/mail"
)

// sec3 (decision 90): a device cookie's budget covers only the user the cookie names. Here the email is held by two users,
// ann in account Acme with testPassword and ann in account Beta with secondPassword, and the cookie is ann's from Acme.

// secondPassword is the password of the second account's ann, which the first account's ann does not have.
const secondPassword = "a second passphrase here"

// verifiedUserWith is verifiedUser with the password the user verifies with.
func verifiedUserWith(t *testing.T, s *Server, key, accID, email, password string) {
	t.Helper()
	rec := call(t, s, "POST", "/api/v1/accounts/"+accID+"/users", key,
		map[string]any{"email": email, "verification": "link"})
	wantStatus(t, rec, 201)
	created := decode[userResponse](t, rec)
	rec = call(t, s, "POST", "/api/v1/auth/verify", "",
		map[string]any{"token": created.Verification.Token, "password": password})
	wantStatus(t, rec, 200)
}

// annInAccount makes ann@example.com a verified user of a new account, with password.
func annInAccount(t *testing.T, s *Server, admin, name, password string) {
	t.Helper()
	acc, key := newAccount(t, s, admin, name)
	verifiedUserWith(t, s, key, acc.ID, "ann@example.com", password)
}

// cookieNonce returns the nonce of a device cookie, the second field of its value.
func cookieNonce(t *testing.T, c *http.Cookie) string {
	t.Helper()
	parts := strings.Split(c.Value, ".")
	if len(parts) != 5 {
		t.Fatalf("device cookie %q does not have five fields", c.Value)
	}
	return parts[1]
}

// sec3 item (a), decision 90: a cookie for ann in one account does not let its browser guess the password of the other
// account's ann outside the email's cap. Thirty wrong guesses through the cookie's browser spend the email's cap, so a
// browser without the cookie is refused (or sent the confirmation), and the cookie's browser gets no session for the
// other account's password.
func TestACookieForOneUserDoesNotGuessAnotherUsersPassword(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mailer bool
	}{{"no mailer", false}, {"with a mailer", true}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &mail.Recorder{}
			var mailer mail.Sender
			if tc.mailer {
				mailer = rec
			}
			s := newAuthServer(t, mailer)
			admin := adminKey(t, s)
			annInAccount(t, s, admin, "Acme", testPassword)
			annInAccount(t, s, admin, "Beta", secondPassword)

			const cookieAddr = "10.30.1.1:4000"
			dev := deviceCookieFrom(t, loginWith(t, s, cookieAddr, "ann@example.com", testPassword))
			for i := 0; i < 30; i++ {
				wantError(t, loginWith(t, s, cookieAddr, "ann@example.com", "wrong password here", dev), 401, apierr.Unauthenticated)
			}
			// The thirty guesses spent the email's cap, so a browser without the cookie is refused, or sent the confirmation.
			if tc.mailer {
				wantConfirmAnswer(t, loginWith(t, s, "10.30.2.1:4000", "ann@example.com", "wrong password here"))
			} else {
				wantError(t, loginWith(t, s, "10.30.2.1:4000", "ann@example.com", "wrong password here"), 429, apierr.RateLimited)
			}
			// The cookie's browser's next guess at the other account's password gets no session.
			if tc.mailer {
				wantConfirmAnswer(t, loginWith(t, s, cookieAddr, "ann@example.com", secondPassword, dev))
				s.bg.Wait()
				if n := len(confirmMailTo(rec, "ann@example.com")); n != 1 {
					t.Errorf("confirmation mails = %d, want 1 (the other account's ann)", n)
				}
			} else {
				wantError(t, loginWith(t, s, cookieAddr, "ann@example.com", secondPassword, dev), 429, apierr.RateLimited)
			}
		})
	}
}

// sec3 item (b), decision 90 (F1 kept): with the email's cap spent, the cookie's browser still signs in as the user the
// cookie names, with that user's right password. A browser that holds a device cookie for ann is not locked out of ann
// by someone spending the email's cap.
func TestWithTheCapSpentTheCookieUserStillSignsIn(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	annInAccount(t, s, admin, "Acme", testPassword)
	annInAccount(t, s, admin, "Beta", secondPassword)

	const cookieAddr = "10.31.1.1:4000"
	dev := deviceCookieFrom(t, loginWith(t, s, cookieAddr, "ann@example.com", testPassword))
	spendCap(t, s, "ann@example.com", 31)
	wantStatus(t, loginWith(t, s, cookieAddr, "ann@example.com", testPassword, dev), 200)
}

// sec3 item (c), decision 90 (corner 4): with the email's cap spent, the cookie's browser that gives the other account's
// right password gets the confirmation with a mailer (a mail to that ann, and no session), and the refusal without one.
// The cookie's own user is not tested in that case, and the other account is tested only by the confirmation.
func TestWithTheCapSpentTheCookieBrowserGetsNoSessionForTheOtherUser(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mailer bool
	}{{"no mailer", false}, {"with a mailer", true}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &mail.Recorder{}
			var mailer mail.Sender
			if tc.mailer {
				mailer = rec
			}
			s := newAuthServer(t, mailer)
			admin := adminKey(t, s)
			annInAccount(t, s, admin, "Acme", testPassword)
			annInAccount(t, s, admin, "Beta", secondPassword)

			const cookieAddr = "10.32.1.1:4000"
			dev := deviceCookieFrom(t, loginWith(t, s, cookieAddr, "ann@example.com", testPassword))
			spendCap(t, s, "ann@example.com", 32)
			if tc.mailer {
				rsp := loginWith(t, s, cookieAddr, "ann@example.com", secondPassword, dev)
				wantConfirmAnswer(t, rsp)
				if answerCookie(rsp, s.sessionCookie) != nil {
					t.Error("the confirmation answer set a session cookie")
				}
				s.bg.Wait()
				if n := len(confirmMailTo(rec, "ann@example.com")); n != 1 {
					t.Errorf("confirmation mails = %d, want 1 (the other account's ann)", n)
				}
			} else {
				wantError(t, loginWith(t, s, cookieAddr, "ann@example.com", secondPassword, dev), 429, apierr.RateLimited)
			}
		})
	}
}

// sec3 item (d), decision 90 (rule 6): thirty correct sign-ins as the cookie's user, from the cookie's browser, give back
// every hold they took. Afterwards the device budget is whole, and a browser without the cookie signs in as the other
// account's ann, so the email's cap is whole. The clock moves on every read, so a refund at a later time than its
// reservation would restore nothing (decision 89 (k)).
func TestCorrectSignInsAsOneUserLeaveTheOtherUsersCapWhole(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	annInAccount(t, s, admin, "Acme", testPassword)
	annInAccount(t, s, admin, "Beta", secondPassword)

	const cookieAddr = "10.33.1.1:4000"
	dev := deviceCookieFrom(t, loginWith(t, s, cookieAddr, "ann@example.com", testPassword))
	// The clock starts after the setup's own reads and steps a millisecond on every read.
	base := time.Now()
	var reads atomic.Int64
	s.limitClock = func() time.Time {
		return base.Add(time.Duration(reads.Add(1)) * time.Millisecond)
	}
	for i := 0; i < 30; i++ {
		wantStatus(t, loginWith(t, s, cookieAddr, "ann@example.com", testPassword, dev), 200)
	}
	b := s.limits["login.device"].buckets[cookieNonce(t, dev)]
	if b == nil {
		t.Fatal("the device's budget was never taken, so the sign-ins did not use it")
	}
	if got := b.lim.TokensAt(s.limitClock()); got < 9.5 {
		t.Errorf("device budget holds %.2f tokens after thirty correct sign-ins, want 10: a correct sign-in must refund it", got)
	}
	wantStatus(t, loginWith(t, s, "10.33.2.1:4000", "ann@example.com", secondPassword), 200)
}

// sec3 item (e), decision 90 (rule 1): an email held by one user only. Ten wrong guesses through the cookie spend only the
// device's own budget, and the email's cap is untouched, as it is without the cookie.
func TestAnEmailHeldByOneUserOnlyCostsItsDeviceBudget(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, nil)
	admin := adminKey(t, s)
	annInAccount(t, s, admin, "Acme", testPassword)

	const cookieAddr = "10.34.1.1:4000"
	dev := deviceCookieFrom(t, loginWith(t, s, cookieAddr, "ann@example.com", testPassword))
	for i := 0; i < 10; i++ {
		wantError(t, loginWith(t, s, cookieAddr, "ann@example.com", "wrong password here", dev), 401, apierr.Unauthenticated)
	}
	// The email's cap is whole: thirty wrong guesses from other addresses are all answered as failed sign-ins.
	spendCap(t, s, "ann@example.com", 34)
}

// sec3 corner 3, decision 90: with the email's cap spent, a cookie's user who matches signs in, even when the other
// account has the same password. The 409 account choice is not offered in that corner (an owner's choice). Before the
// cap is spent, the same sign-in still gets the 409.
func TestCapSpentSamePasswordInTwoAccountsSignsInAsTheCookieUser(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	annInAccount(t, s, admin, "Acme", testPassword)

	const cookieAddr = "10.35.1.1:4000"
	dev := deviceCookieFrom(t, loginWith(t, s, cookieAddr, "ann@example.com", testPassword))
	annInAccount(t, s, admin, "Beta", testPassword)
	wantError(t, loginWith(t, s, cookieAddr, "ann@example.com", testPassword, dev), 409, apierr.Conflict)
	spendCap(t, s, "ann@example.com", 35)
	wantStatus(t, loginWith(t, s, cookieAddr, "ann@example.com", testPassword, dev), 200)
}
