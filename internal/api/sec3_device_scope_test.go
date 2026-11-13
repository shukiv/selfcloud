// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// sec3 (decision 90): a device cookie's budget covers only the user the cookie names. Here the email is held by two users,
// ann in account Acme with testPassword and ann in account Beta with secondPassword, and the cookie is ann's from Acme.

// secondPassword is the password of the second account's ann, which the first account's ann does not have.
const secondPassword = "a second passphrase here"

// verifiedUserWith is verifiedUser with the password the user verifies with. It returns the user.
func verifiedUserWith(t *testing.T, s *Server, key, accID, email, password string) userResponse {
	t.Helper()
	rec := call(t, s, "POST", "/api/v1/accounts/"+accID+"/users", key,
		map[string]any{"email": email, "verification": "link"})
	wantStatus(t, rec, 201)
	created := decode[userResponse](t, rec)
	rec = call(t, s, "POST", "/api/v1/auth/verify", "",
		map[string]any{"token": created.Verification.Token, "password": password})
	wantStatus(t, rec, 200)
	return created
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

// failedRowsOf counts the user.login_failed rows of an account whose actor is userID, read through the account's audit log.
func failedRowsOf(t *testing.T, s *Server, accountID, key, userID string) int {
	t.Helper()
	rec := call(t, s, "GET", "/api/v1/accounts/"+accountID+"/audit", key, nil)
	wantStatus(t, rec, http.StatusOK)
	n := 0
	for _, it := range decode[listResponse[auditJSON]](t, rec).Items {
		if it.Action == "user.login_failed" && it.Actor.ID == userID {
			n++
		}
	}
	return n
}

// installationAdminWithEmail makes an installation admin with ann@example.com and password, which is not a tenant user.
func installationAdminWithEmail(t *testing.T, s *Server, password string) {
	t.Helper()
	ctx := context.Background()
	hash, err := auth.HashPassword(ctx, password)
	if err != nil {
		t.Fatal(err)
	}
	now := store.Now()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		return tx.InsertUser(ctx, &store.User{ID: id.New("usr"), Email: "ann@example.com", EmailKey: "ann@example.com",
			Role: store.RoleAdmin, Status: store.UserActive, PasswordHash: hash, CreatedAt: now, UpdatedAt: now})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// cookieStatuses sends guesses wrong sign-ins from the cookie's browser, then the cookie user's right password, and returns
// the status codes in order.
func cookieStatuses(t *testing.T, s *Server, addr string, dev *http.Cookie, guesses int) []int {
	t.Helper()
	var codes []int
	for i := 0; i < guesses; i++ {
		codes = append(codes, loginWith(t, s, addr, "ann@example.com", "wrong password here", dev).Code)
	}
	return append(codes, loginWith(t, s, addr, "ann@example.com", testPassword, dev).Code)
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
			// The cookie is made before the other account exists: a sign-in that tests two users spends an email token.
			const cookieAddr = "10.30.1.1:4000"
			dev := deviceCookieFrom(t, loginWith(t, s, cookieAddr, "ann@example.com", testPassword))
			annInAccount(t, s, admin, "Beta", secondPassword)
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

// Decision 90: with the email's cap spent, the cookie's browser still signs in as the user the
// cookie names, with that user's right password. A browser that holds a device cookie for ann is not locked out of ann
// by someone spending the email's cap.
func TestWithTheCapSpentTheCookieUserStillSignsIn(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	annInAccount(t, s, admin, "Acme", testPassword)
	const cookieAddr = "10.31.1.1:4000"
	dev := deviceCookieFrom(t, loginWith(t, s, cookieAddr, "ann@example.com", testPassword))
	annInAccount(t, s, admin, "Beta", secondPassword)
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
			const cookieAddr = "10.32.1.1:4000"
			dev := deviceCookieFrom(t, loginWith(t, s, cookieAddr, "ann@example.com", testPassword))
			annInAccount(t, s, admin, "Beta", secondPassword)
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

// sec3 item (d), decision 90 (rule 6): thirty correct sign-ins as the only user with the email, from the cookie's browser,
// give back every hold they took. The device budget is whole afterwards, and the email's cap is whole too. The clock moves
// on every read, so a refund at a later time than its reservation would restore nothing (decision 89 (k)).
func TestCorrectSignInsAsOneUserLeaveTheCapWhole(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	annInAccount(t, s, admin, "Acme", testPassword)

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
	wantStatus(t, loginWith(t, s, "10.33.2.1:4000", "ann@example.com", testPassword), 200)
}

// Decision 90: a match on one user does not refund the email's cap for another user who was tested
// in the same request and failed. The attacker owns Beta's ann, whose password is the guess, so each sign-in matches it,
// and the victim's ann in Acme is tested against the guess for free under the old rule. Here the thirty sign-ins spend
// thirty tokens of the email's cap, and the next sign-in from another address is refused.
func TestAMatchOnOneUserDoesNotRefundTheOtherUsersTest(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, nil)
	admin := adminKey(t, s)
	const guess = "a guessed passphrase"
	annInAccount(t, s, admin, "Acme", testPassword)
	annInAccount(t, s, admin, "Beta", guess)

	for i := 0; i < 30; i++ {
		wantStatus(t, loginWith(t, s, "10.40.1.1:4000", "ann@example.com", guess), 200)
	}
	wantError(t, loginWith(t, s, "10.40.2.1:4000", "ann@example.com", "wrong password here"), 429, apierr.RateLimited)
}

// Decision 90: a sign-in that matches every candidate it tested gives every hold back.
// Two users share one password, so the sign-in is answered with the account choice; the cookie's device budget and the
// email's cap are whole afterwards, and a wrong guess from another address is still answered as a failed sign-in.
func TestASignInMatchingEveryCandidateRefundsEveryHold(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, nil)
	admin := adminKey(t, s)
	annInAccount(t, s, admin, "Acme", testPassword)

	const cookieAddr = "10.41.1.1:4000"
	dev := deviceCookieFrom(t, loginWith(t, s, cookieAddr, "ann@example.com", testPassword))
	annInAccount(t, s, admin, "Beta", testPassword)
	for i := 0; i < 30; i++ {
		wantError(t, loginWith(t, s, cookieAddr, "ann@example.com", testPassword, dev), 409, apierr.Conflict)
	}
	b := s.limits["login.device"].buckets[cookieNonce(t, dev)]
	if b == nil {
		t.Fatal("the device's budget was never taken, so the sign-ins did not use it")
	}
	if got := b.lim.TokensAt(s.limitClock()); got < 9.5 {
		t.Errorf("device budget holds %.2f tokens after thirty matching sign-ins, want 10", got)
	}
	for i := 0; i < 30; i++ {
		wantError(t, loginWith(t, s, "10.41.2.1:4000", "ann@example.com", "wrong password here"), 401, apierr.Unauthenticated)
	}
}

// Decision 90: a match on the cookie's own user alone does not refund the device budget of a
// sign-in that the other user's password matched. The device budget covers the cookie's user only, so ten sign-ins with
// the other account's password spend it, and the budget is not whole afterwards.
func TestAMatchOnAnotherUserAloneDoesNotRefundTheDeviceBudget(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, nil)
	admin := adminKey(t, s)
	annInAccount(t, s, admin, "Acme", testPassword)

	const cookieAddr = "10.42.1.1:4000"
	dev := deviceCookieFrom(t, loginWith(t, s, cookieAddr, "ann@example.com", testPassword))
	annInAccount(t, s, admin, "Beta", secondPassword)
	for i := 0; i < 10; i++ {
		wantStatus(t, loginWith(t, s, cookieAddr, "ann@example.com", secondPassword, dev), 200)
	}
	b := s.limits["login.device"].buckets[cookieNonce(t, dev)]
	if b == nil {
		t.Fatal("the device's budget was never taken, so the sign-ins did not use it")
	}
	if got := b.lim.TokensAt(s.limitClock()); got > 0.5 {
		t.Errorf("device budget holds %.2f tokens after ten sign-ins with the other account's password, want 0: only the cookie's user's match refunds it", got)
	}
}

// Decision 90 (owner's choice, rule 1 as revised): a person with the same email in two accounts and different
// passwords spends one email token per sign-in, because the other account's password is tested too. The cap is spent by
// their own correct sign-ins, a browser without the cookie is refused, and the cookie's user still signs in while the cap
// is spent.
func TestTwoUsersWithOneEmailSpendOneTokenPerSignIn(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, nil)
	admin := adminKey(t, s)
	annInAccount(t, s, admin, "Acme", testPassword)

	const cookieAddr = "10.43.1.1:4000"
	dev := deviceCookieFrom(t, loginWith(t, s, cookieAddr, "ann@example.com", testPassword))
	annInAccount(t, s, admin, "Beta", secondPassword)
	for i := 0; i < 30; i++ {
		wantStatus(t, loginWith(t, s, cookieAddr, "ann@example.com", testPassword, dev), 200)
	}
	wantError(t, loginWith(t, s, "10.43.2.1:4000", "ann@example.com", "wrong password here"), 429, apierr.RateLimited)
	wantStatus(t, loginWith(t, s, cookieAddr, "ann@example.com", testPassword, dev), 200)
}

// Decision 90: with a cookie, the email's hold is taken whether or not another user holds the email.
// A lone user's ten cookie failures spend the device budget and the email's cap, so twenty more failures from other
// addresses are answered as failed sign-ins, and the next one is refused.
func TestAnEmailHeldByOneUserCountsTheCookieFailuresToo(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, nil)
	admin := adminKey(t, s)
	annInAccount(t, s, admin, "Acme", testPassword)

	const cookieAddr = "10.44.1.1:4000"
	dev := deviceCookieFrom(t, loginWith(t, s, cookieAddr, "ann@example.com", testPassword))
	for i := 0; i < 10; i++ {
		wantError(t, loginWith(t, s, cookieAddr, "ann@example.com", "wrong password here", dev), 401, apierr.Unauthenticated)
	}
	for i := 1; i <= 20; i++ {
		wantError(t, loginWith(t, s, fmt.Sprintf("10.44.0.%d:4000", i), "ann@example.com", "wrong password here"), 401, apierr.Unauthenticated)
	}
	wantError(t, loginWith(t, s, "10.44.3.1:4000", "ann@example.com", "wrong password here"), 429, apierr.RateLimited)
}

// Decision 90: a cookie browser gets the same status codes whether or not another user holds
// the email. Each run is a sequence of wrong guesses through the cookie, and then the cookie user's right password, so a
// status code that tells a lone email from a shared one would show as a difference between the two sequences.
func TestACookieBrowserSeesTheSameStatusesWhetherOrNotAnotherUserHoldsTheEmail(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mailer bool
	}{{"no mailer", false}, {"with a mailer", true}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var lone, shared mail.Sender
			if tc.mailer {
				lone, shared = &mail.Recorder{}, &mail.Recorder{}
			}
			const cookieAddr = "10.45.1.1:4000"

			sLone := newAuthServer(t, lone)
			annInAccount(t, sLone, adminKey(t, sLone), "Acme", testPassword)
			devLone := deviceCookieFrom(t, loginWith(t, sLone, cookieAddr, "ann@example.com", testPassword))
			got := cookieStatuses(t, sLone, cookieAddr, devLone, 35)

			sShared := newAuthServer(t, shared)
			adminShared := adminKey(t, sShared)
			annInAccount(t, sShared, adminShared, "Acme", testPassword)
			devShared := deviceCookieFrom(t, loginWith(t, sShared, cookieAddr, "ann@example.com", testPassword))
			annInAccount(t, sShared, adminShared, "Beta", secondPassword)
			want := cookieStatuses(t, sShared, cookieAddr, devShared, 35)

			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("status codes differ: lone %v, shared %v", got, want)
			}
			if !strings.Contains(fmt.Sprint(got), "429") && !strings.Contains(fmt.Sprint(got), "202") {
				t.Errorf("the sequence never reaches the cap: %v", got)
			}
		})
	}
}

// Decision 90: when the cap is spent and the cookie's user fails, that failure is audited against the
// cookie's user, as a failed sign-in is, with or without a mailer. With a mailer the other account's user is then
// confirmed by its own row (confirmByEmail keeps its one row), so that account's log has a row too.
func TestTheCookieUsersFailureWhileCappedIsAudited(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		mailer    bool
		wantBRows int
		wantCode  int
	}{{"no mailer", false, 0, 429}, {"with a mailer", true, 1, 202}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var mailer mail.Sender
			if tc.mailer {
				mailer = &mail.Recorder{}
			}
			s := newAuthServer(t, mailer)
			admin := adminKey(t, s)
			accA, keyA := newAccount(t, s, admin, "Acme")
			d := verifiedUserWith(t, s, keyA, accA.ID, "ann@example.com", testPassword)
			const cookieAddr = "10.46.1.1:4000"
			dev := deviceCookieFrom(t, loginWith(t, s, cookieAddr, "ann@example.com", testPassword))
			accB, keyB := newAccount(t, s, admin, "Beta")
			e := verifiedUserWith(t, s, keyB, accB.ID, "ann@example.com", secondPassword)
			spendCap(t, s, "ann@example.com", 46)
			beforeA := failedRowsOf(t, s, accA.ID, keyA, d.ID)
			beforeB := failedRowsOf(t, s, accB.ID, keyB, e.ID)

			wantStatus(t, loginWith(t, s, cookieAddr, "ann@example.com", "wrong password here", dev), tc.wantCode)

			if n := failedRowsOf(t, s, accA.ID, keyA, d.ID) - beforeA; n != 1 {
				t.Errorf("the cookie user's failed rows = %d, want 1", n)
			}
			if n := failedRowsOf(t, s, accB.ID, keyB, e.ID) - beforeB; n != tc.wantBRows {
				t.Errorf("the other account's failed rows = %d, want %d", n, tc.wantBRows)
			}
		})
	}
}

// Decision 90: a cookie for ann in one account is not trusted when the sign-in names
// another account. The narrowed candidates do not include the cookie's user, so the device budget is never taken and the
// email's cap applies. This is the known gap of decision 90 in the other direction; behaviour is unchanged.
func TestACookieNamingAnotherAccountIsNotTrusted(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, nil)
	admin := adminKey(t, s)
	annInAccount(t, s, admin, "Acme", testPassword)
	const cookieAddr = "10.47.1.1:4000"
	dev := deviceCookieFrom(t, loginWith(t, s, cookieAddr, "ann@example.com", testPassword))
	accB, keyB := newAccount(t, s, admin, "Beta")
	verifiedUserWith(t, s, keyB, accB.ID, "ann@example.com", secondPassword)
	// Beta's ann is the only candidate the sign-in narrows to, and the cookie's user is not among them: the cap applies.
	for i := 0; i < 30; i++ {
		wantError(t, postFrom(t, s, cookieAddr, "/api/v1/auth/login",
			map[string]any{"email": "ann@example.com", "password": "wrong password here", "account": accB.ID}, dev),
			401, apierr.Unauthenticated)
	}
	if b := s.limits["login.device"].buckets[cookieNonce(t, dev)]; b != nil {
		t.Errorf("the cookie was trusted for another account: its device budget was taken")
	}
	wantError(t, postFrom(t, s, "10.47.2.1:4000", "/api/v1/auth/login",
		map[string]any{"email": "ann@example.com", "password": "wrong password here", "account": accB.ID}),
		429, apierr.RateLimited)
}

// Decision 90: a cookie is not trusted under the installation admin scope. The admin's
// candidates do not include the tenant user the cookie names, so the device budget is never taken and the cap applies.
func TestACookieIsNotTrustedUnderTheAdminScope(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, nil)
	admin := adminKey(t, s)
	annInAccount(t, s, admin, "Acme", testPassword)

	const cookieAddr = "10.48.1.1:4000"
	dev := deviceCookieFrom(t, loginWith(t, s, cookieAddr, "ann@example.com", testPassword))
	installationAdminWithEmail(t, s, secondPassword)
	for i := 0; i < 30; i++ {
		wantError(t, postFrom(t, s, cookieAddr, "/api/v1/auth/login",
			map[string]any{"email": "ann@example.com", "password": "wrong password here", "account": adminChoiceID}, dev),
			401, apierr.Unauthenticated)
	}
	if b := s.limits["login.device"].buckets[cookieNonce(t, dev)]; b != nil {
		t.Errorf("the cookie was trusted under the admin scope: its device budget was taken")
	}
	wantError(t, postFrom(t, s, "10.48.2.1:4000", "/api/v1/auth/login",
		map[string]any{"email": "ann@example.com", "password": "wrong password here", "account": adminChoiceID}),
		429, apierr.RateLimited)
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
