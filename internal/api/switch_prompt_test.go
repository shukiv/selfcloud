// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
)

// fu2 item 5 (decision 74): verifying an account or resetting a password from a browser that is signed in as another
// user asks first, as a sign-in link does (signed_in_as_other). Nothing is written by the refusal, so the link stays
// live for the prompt's second answer, which carries confirm.

// The verify route refuses a browser signed in as someone else, and confirm switches it.
func TestVerifyAsksBeforeSwitching(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, nil)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, ann := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	bob := decode[userResponse](t, call(t, s, "POST", "/api/v1/accounts/"+acc.ID+"/users", key,
		map[string]any{"email": "ben@example.com", "verification": "link"}))
	if bob.Verification == nil || bob.Verification.Token == "" {
		t.Fatal("no verification link token for the new user")
	}
	tok := bob.Verification.Token
	body := map[string]any{"token": tok, "password": testPassword}

	rec := callSession(t, s, ann, "POST", "/api/v1/auth/verify", body)
	e := wantError(t, rec, http.StatusConflict, apierr.Conflict)
	if e.Details["reason"] != "signed_in_as_other" {
		t.Errorf("reason = %v, want signed_in_as_other", e.Details["reason"])
	}
	if got := signedAsEmail(t, s, ann); got != "ann@example.com" {
		t.Errorf("after the prompt the browser is signed in as %q, want ann", got)
	}
	if st := decode[userJSON](t, call(t, s, "GET", "/api/v1/accounts/"+acc.ID+"/users/"+bob.ID, key, nil)).Status; st != "pending" {
		t.Errorf("after the refusal bob is %s, want still pending: the link must stay live", st)
	}

	// The second answer, with confirm, verifies bob, signs the browser in as bob, and ends ann's session.
	body["confirm"] = true
	signed := decode[signedIn](t, callSession(t, s, ann, "POST", "/api/v1/auth/verify", body))
	if signed.User.ID != bob.ID {
		t.Errorf("confirmed verify signed in %s, want bob", signed.User.ID)
	}
	if rec := callSession(t, s, ann, "GET", "/api/v1/me", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("ann's session after the switch: %d, want it ended", rec.Code)
	}
}

// The reset route refuses a browser signed in as someone else, and confirm switches it. The reset link is not used by
// the refusal, so the answer with confirm still works.
func TestResetAsksBeforeSwitching(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, nil)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, ann := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	bob, bobSess := verifiedUser(t, s, key, acc.ID, "ben@example.com")
	_ = bobSess
	reset := decode[verificationJSON](t, call(t, s, "POST",
		"/api/v1/accounts/"+acc.ID+"/users/"+bob.ID+"/password-reset", key, map[string]any{"method": "link"}))
	if reset.Token == "" {
		t.Fatal("no reset link token")
	}
	body := map[string]any{"token": reset.Token, "password": "a new password here"}

	e := wantError(t, callSession(t, s, ann, "POST", "/api/v1/auth/password/reset", body), http.StatusConflict, apierr.Conflict)
	if e.Details["reason"] != "signed_in_as_other" {
		t.Errorf("reason = %v, want signed_in_as_other", e.Details["reason"])
	}
	if got := signedAsEmail(t, s, ann); got != "ann@example.com" {
		t.Errorf("after the prompt the browser is signed in as %q, want ann", got)
	}

	body["confirm"] = true
	signed := decode[signedIn](t, callSession(t, s, ann, "POST", "/api/v1/auth/password/reset", body))
	if signed.User.ID != bob.ID {
		t.Errorf("confirmed reset signed in %s, want bob", signed.User.ID)
	}
}
