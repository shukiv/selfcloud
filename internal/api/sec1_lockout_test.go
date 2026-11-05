// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
)

// sec1 item 1 (decision 84): a guesser at one address must not lock the real user out from another address.
// Before, the per-email bucket was keyed on the email alone, so a few tries from one address made every later
// sign-in and reset for that email answer 429, from anywhere.
func TestGuessingFromOneAddressDoesNotLockOutTheUserElsewhere(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec, func(c *config.Config) { c.RateLimit.PerEmailPerMinute = 2 })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")

	const attacker = "192.0.2.10:4000"
	const victim = "192.0.2.11:4000"

	// The attacker has the per-email budget of two for its own address, so its third try is refused.
	for i := 0; i < 2; i++ {
		wantError(t, loginWith(t, s, attacker, "ann@example.com", "wrong password here"), 401, apierr.Unauthenticated)
	}
	wantError(t, loginWith(t, s, attacker, "ann@example.com", "wrong password here"), 429, apierr.RateLimited)

	// The user signs in from another address, unaffected: the cap is not spent.
	wantStatus(t, loginWith(t, s, victim, "ann@example.com", testPassword), 200)

	forgot := func(addr string) *httptest.ResponseRecorder {
		return remoteCall(t, s, "POST", "/api/v1/auth/password/forgot", addr, map[string]any{"email": "ann@example.com"})
	}
	wantStatus(t, forgot(attacker), 202)
	wantStatus(t, forgot(attacker), 202)
	wantError(t, forgot(attacker), 429, apierr.RateLimited)
	wantStatus(t, forgot(victim), 202)
	s.bg.Wait()
	// Three reset mails in all: the attacker's two, and the user's.
	if n := len(rec.Messages()); n != 3 {
		t.Errorf("reset mails = %d, want 3 (the user's request is served)", n)
	}
}

// sec1 item 1 (decision 84): online guessing of one email from many addresses is still bounded. Each address has its
// own per-email budget, and a cap of 30 failed sign-ins an hour counts every address together. An unknown email is
// charged the same way, so the answer cannot show whether the address is known.
func TestGuessingOneEmailFromManyAddressesIsBounded(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.RateLimit.PerEmailPerMinute = 2 })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")

	for _, email := range []string{"ann@example.com", "nobody@example.com"} {
		spendCap(t, s, email, 30)
		wantError(t, loginWith(t, s, "192.0.2.90:4000", email, "wrong password here"), 429, apierr.RateLimited)
	}
}

// spendCap makes n wrong sign-ins to email, each from its own address, so the cap for the email is spent. The
// addresses are 10.<tag>.0.<i>, so one test can use several caps without sharing addresses.
func spendCap(t *testing.T, s *Server, email string, tag int) {
	t.Helper()
	for i := 1; i <= 30; i++ {
		addr := fmt.Sprintf("10.%d.0.%d:4000", tag, i)
		wantError(t, loginWith(t, s, addr, email, "wrong password here"), 401, apierr.Unauthenticated)
	}
}

// loginWith posts a sign-in from addr, with the cookies given.
func loginWith(t *testing.T, s *Server, addr, email, password string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	return postFrom(t, s, addr, "/api/v1/auth/login", map[string]any{"email": email, "password": password}, cookies...)
}

// postFrom posts body to path from addr, with the cookies given.
func postFrom(t *testing.T, s *Server, addr, path string, body map[string]any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := jsonRequest(t, "POST", path, body)
	req.RemoteAddr = addr
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// deviceCookieFrom returns the device cookie an answer set, or fails the test.
func deviceCookieFrom(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == "selfcloud_device" {
			return c
		}
	}
	t.Fatalf("no selfcloud_device cookie in the answer: %v", rec.Header())
	return nil
}

// sec1 item 1 (decision 84): a device the user has signed in from before is not refused by the email's cap, so a
// guesser who has spent the cap, from one address or from many, cannot keep the user out.
func TestDeviceCookieSignsInWhileTheCapIsSpent(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		tag  int
	}{{"one address", 1}, {"many addresses", 2}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newAuthServer(t, &mail.Recorder{})
			admin := adminKey(t, s)
			acc, key := newAccount(t, s, admin, "Acme")
			verifiedUser(t, s, key, acc.ID, "ann@example.com")

			dev := deviceCookieFrom(t, loginWith(t, s, "10.9.0.1:4000", "ann@example.com", testPassword))
			spendCap(t, s, "ann@example.com", 30+tc.tag)
			wantError(t, loginWith(t, s, "10.9.0.2:4000", "ann@example.com", "wrong password here"), 429, apierr.RateLimited)

			wantStatus(t, loginWith(t, s, "10.9.0.3:4000", "ann@example.com", testPassword, dev), 200)
		})
	}
}

// sec1 item 1 (decision 84): without a device cookie, a sign-in is subject to the cap once it is spent. The answer
// is the same for an unknown email, and it points the person at Forgot password.
func TestNoDeviceCookieIsRefusedWhileTheCapIsSpent(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	spendCap(t, s, "ann@example.com", 40)
	spendCap(t, s, "nobody@example.com", 41)

	known := wantError(t, loginWith(t, s, "10.9.1.1:4000", "ann@example.com", testPassword), 429, apierr.RateLimited)
	unknown := wantError(t, loginWith(t, s, "10.9.1.2:4000", "nobody@example.com", testPassword), 429, apierr.RateLimited)
	if known.Message != unknown.Message {
		t.Errorf("known and unknown answers differ: %q vs %q", known.Message, unknown.Message)
	}
	if want := "Forgot password"; !strings.Contains(known.Message, want) {
		t.Errorf("message %q does not point at %q", known.Message, want)
	}
}

// sec1 item 1 (decision 84): a forged cookie, a cookie for another user, and an expired cookie are not trusted.
func TestForgedOtherUsersAndExpiredDeviceCookiesAreNotTrusted(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	verifiedUser(t, s, key, acc.ID, "bob@example.com")

	annCookie := deviceCookieFrom(t, loginWith(t, s, "10.8.0.1:4000", "ann@example.com", testPassword))
	bobCookie := deviceCookieFrom(t, loginWith(t, s, "10.8.0.2:4000", "bob@example.com", testPassword))
	forged := *annCookie
	last := forged.Value[len(forged.Value)-1]
	if last == 'A' {
		forged.Value = forged.Value[:len(forged.Value)-1] + "B"
	} else {
		forged.Value = forged.Value[:len(forged.Value)-1] + "A"
	}

	spendCap(t, s, "ann@example.com", 50)
	wantError(t, loginWith(t, s, "10.8.1.1:4000", "ann@example.com", testPassword, &forged), 429, apierr.RateLimited)
	wantError(t, loginWith(t, s, "10.8.1.2:4000", "ann@example.com", testPassword, bobCookie), 429, apierr.RateLimited)
	// Positive control: the genuine cookie for ann is trusted while the cap is spent.
	wantStatus(t, loginWith(t, s, "10.8.1.3:4000", "ann@example.com", testPassword, annCookie), 200)

	// An expired cookie: the same server shape with a one-second lifetime.
	short := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.Sessions.DeviceCookieLifetime = config.Duration(time.Second) })
	admin = adminKey(t, short)
	acc, key = newAccount(t, short, admin, "Acme")
	verifiedUser(t, short, key, acc.ID, "ann@example.com")
	old := deviceCookieFrom(t, loginWith(t, short, "10.8.2.1:4000", "ann@example.com", testPassword))
	time.Sleep(2 * time.Second)
	spendCap(t, short, "ann@example.com", 51)
	wantError(t, loginWith(t, short, "10.8.2.2:4000", "ann@example.com", testPassword, old), 429, apierr.RateLimited)
}

// sec1 item 1 (decision 84): a device cookie has a budget of its own. When it has failed too often, it stops being
// trusted, and the cap applies to it again.
func TestDeviceCookieThatFailsItsOwnBudgetStopsBeingTrusted(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")

	dev := deviceCookieFrom(t, loginWith(t, s, "10.10.0.1:4000", "ann@example.com", testPassword))
	spendCap(t, s, "ann@example.com", 60)

	// Wrong passwords from the device: each is a failure against the device's own budget.
	for i := 0; i < 10; i++ {
		wantError(t, loginWith(t, s, "10.10.1.1:4000", "ann@example.com", "wrong password here", dev), 401, apierr.Unauthenticated)
	}
	// The device has spent its budget, so the cap applies, and the right password is refused.
	wantError(t, loginWith(t, s, "10.10.1.1:4000", "ann@example.com", testPassword, dev), 429, apierr.RateLimited)
}

// sec1 item 5 (decision 84): a password reset is not refused by the cap, and a successful reset sets a device cookie
// that signs the user in while the cap stays spent.
func TestResetWorksWhileTheCapIsSpentAndSetsADeviceCookie(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	spendCap(t, s, "ann@example.com", 70)

	wantStatus(t, remoteCall(t, s, "POST", "/api/v1/auth/password/forgot", "10.11.0.1:4000",
		map[string]any{"email": "ann@example.com"}), 202)
	s.bg.Wait()
	if n := len(rec.Messages()); n != 1 {
		t.Fatalf("reset mails = %d, want 1", n)
	}
	token := tokenFromMail(t, rec.Messages()[0])
	const newPassword = "a new passphrase here"
	reset := postFrom(t, s, "10.11.0.2:4000", "/api/v1/auth/password/reset",
		map[string]any{"token": token, "password": newPassword})
	wantStatus(t, reset, 200)
	dev := deviceCookieFrom(t, reset)

	wantStatus(t, loginWith(t, s, "10.11.0.3:4000", "ann@example.com", newPassword, dev), 200)
}

// cookieAcrossEvent is the check for review F9 (decision 84). A device cookie made before an event (a password change or
// reset, an admin's set-password, or a disable) stops working after it: with the email's cap spent, the old cookie is
// refused, while a cookie made after the event is trusted. The event is run by the caller; newPassword is the password
// the user has after it.
func cookieAcrossEvent(t *testing.T, s *Server, tag int, email, newPassword string, dev *http.Cookie) {
	t.Helper()
	// Before the cap is spent, a fresh sign-in from a new device makes the cookie that must keep working. It comes a
	// millisecond after the event: a cookie in the cut-off's own millisecond is void (the cut-off is in milliseconds).
	time.Sleep(2 * time.Millisecond)
	fresh := deviceCookieFrom(t, loginWith(t, s, fmt.Sprintf("10.%d.2.1:4000", tag), email, newPassword))
	spendCap(t, s, email, tag)
	wantError(t, loginWith(t, s, fmt.Sprintf("10.%d.1.1:4000", tag), email, newPassword, dev), 429, apierr.RateLimited)
	wantStatus(t, loginWith(t, s, fmt.Sprintf("10.%d.3.1:4000", tag), email, newPassword, fresh), 200)
}

// sec1 item 1, review F9 (decision 84): a password change ends the device cookies made before it.
func TestDeviceCookieEndsWithAPasswordChange(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, annSess := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	dev := deviceCookieFrom(t, loginWith(t, s, "10.50.9.1:4000", "ann@example.com", testPassword))
	const newPassword = "a changed passphrase here"
	wantStatus(t, callSession(t, s, annSess, "POST", "/api/v1/me/password",
		map[string]any{"current_password": testPassword, "new_password": newPassword}), 204)
	cookieAcrossEvent(t, s, 50, "ann@example.com", newPassword, dev)
}

// sec1 item 1, review F9 (decision 84): a reset ends the device cookies made before it.
func TestDeviceCookieEndsWithAReset(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	dev := deviceCookieFrom(t, loginWith(t, s, "10.51.9.1:4000", "ann@example.com", testPassword))
	wantStatus(t, remoteCall(t, s, "POST", "/api/v1/auth/password/forgot", "10.51.8.1:4000",
		map[string]any{"email": "ann@example.com"}), 202)
	s.bg.Wait()
	if n := len(rec.Messages()); n != 1 {
		t.Fatalf("reset mails = %d, want 1", n)
	}
	const newPassword = "a reset passphrase here"
	wantStatus(t, postFrom(t, s, "10.51.8.2:4000", "/api/v1/auth/password/reset",
		map[string]any{"token": tokenFromMail(t, rec.Messages()[0]), "password": newPassword}), 200)
	cookieAcrossEvent(t, s, 51, "ann@example.com", newPassword, dev)
}

// sec1 item 1, review F9 (decision 84): an admin's set-password (the typed password) ends the device cookies made before it.
func TestDeviceCookieEndsWithAnAdminsTypedPassword(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	dev := deviceCookieFrom(t, loginWith(t, fx.s, "10.52.9.1:4000", "member@example.com", testPassword))
	const newPassword = "a typed passphrase here"
	wantStatus(t, callSession(t, fx.s, fx.adminSess, "POST",
		"/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.member.ID+"/password",
		map[string]any{"password": newPassword, "admin_password": testPassword}), 204)
	cookieAcrossEvent(t, fx.s, 52, "member@example.com", newPassword, dev)
}

// sec1 item 1, review F9 (decision 84): a disable ends the device cookies made before it, and they stay dead when the user
// is enabled again.
func TestDeviceCookieEndsWithADisable(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	ann, _ := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	dev := deviceCookieFrom(t, loginWith(t, s, "10.53.9.1:4000", "ann@example.com", testPassword))
	users := "/api/v1/accounts/" + acc.ID + "/users/" + ann.ID
	wantStatus(t, call(t, s, "PATCH", users, key, map[string]any{"status": "disabled"}), 200)
	wantStatus(t, call(t, s, "PATCH", users, key, map[string]any{"status": "active"}), 200)
	cookieAcrossEvent(t, s, 53, "ann@example.com", testPassword, dev)
}
