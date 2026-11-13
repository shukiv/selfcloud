// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
)

// ownerSessionOf makes an owner of fx's account and signs them in, so the caller matrix has an owner's session.
func ownerSessionOf(t *testing.T, fx *impFix) testSession {
	t.Helper()
	rec := call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users", fx.accKey,
		map[string]any{"email": "owner@example.com", "verification": "link", "role": "owner"})
	wantStatus(t, rec, 201)
	created := decode[userResponse](t, rec)
	rec = call(t, fx.s, "POST", "/api/v1/auth/verify", "",
		map[string]any{"token": created.Verification.Token, "password": testPassword})
	wantStatus(t, rec, 200)
	return sessionFrom(t, fx.s, rec)
}

// TestCallersOfTheAdminWaysAreRefused is the caller matrix for the three ways in (decision 41). Each caller that is not
// an installation admin's own session is refused on sign-in-as and on a typed password. The reset link is refused for
// an installation admin's key and for an owner's session. An account key still makes a reset link, as it always has,
// which the review round leaves to the owner (the deviation is in decision 41).
func TestCallersOfTheAdminWaysAreRefused(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	owner := ownerSessionOf(t, fx)
	member := fx.member.ID
	base := "/api/v1/accounts/" + fx.acc.ID + "/users/" + member
	signIn := map[string]any{"admin_password": testPassword}
	typed := map[string]any{"password": "a new passphrase", "admin_password": testPassword}
	link := map[string]any{"method": "link", "admin_password": testPassword}

	refused := func(name string, rec *httptest.ResponseRecorder) {
		t.Helper()
		if rec.Code < 400 {
			t.Errorf("%s: status %d, want a refusal", name, rec.Code)
		}
	}
	// An installation admin's key: a key is never an admin session.
	refused("admin key sign-in-as", call(t, fx.s, "POST", base+"/sign-in", fx.admin, signIn))
	refused("admin key typed password", call(t, fx.s, "POST", base+"/password", fx.admin, typed))
	refused("admin key reset link", call(t, fx.s, "POST", base+"/password-reset", fx.admin, link))
	// An account key: the two admin ways need a session, so the key is refused there.
	refused("account key sign-in-as", call(t, fx.s, "POST", base+"/sign-in", fx.accKey, signIn))
	refused("account key typed password", call(t, fx.s, "POST", base+"/password", fx.accKey, typed))
	// An owner's session: an owner is not an installation admin.
	wantError(t, callSession(t, fx.s, owner, "POST", base+"/sign-in", signIn), 403, apierr.Forbidden)
	wantError(t, callSession(t, fx.s, owner, "POST", base+"/password", typed), 403, apierr.Forbidden)
	wantError(t, callSession(t, fx.s, owner, "POST", base+"/password-reset", link), 403, apierr.Forbidden)
}

// TestAdminKeyNeverMintsALink: an installation admin's key gets no link to an existing user, a reset or a resend,
// whatever admin.tenant_access says. It may still create a user and mail that user's link, as before.
func TestAdminKeyNeverMintsALink(t *testing.T) {
	t.Parallel()
	for _, access := range []bool{false, true} {
		fx := newImpFix(t, access)
		base := "/api/v1/accounts/" + fx.acc.ID + "/users/" + fx.member.ID
		wantError(t, call(t, fx.s, "POST", base+"/password-reset", fx.admin,
			map[string]any{"method": "link"}), 403, apierr.Forbidden)
		wantError(t, call(t, fx.s, "POST", base+"/verification", fx.admin,
			map[string]any{"method": "link"}), 403, apierr.Forbidden)
		if len(fx.mailer.Messages()) != 0 {
			t.Errorf("tenant_access=%v: a mail went out", access)
		}
	}
}

// TestAdminSessionResetLinkNeedsThePassword: the reset link from an admin's session needs the admin's own password,
// as a typed password does. The email reset needs none.
func TestAdminSessionResetLinkNeedsThePassword(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	path := "/api/v1/accounts/" + fx.acc.ID + "/users/" + fx.member.ID + "/password-reset"
	wantError(t, callSession(t, fx.s, fx.adminSess, "POST", path, map[string]any{"method": "link"}),
		403, apierr.Forbidden)
	wantError(t, callSession(t, fx.s, fx.adminSess, "POST", path,
		map[string]any{"method": "link", "admin_password": "wrong password here"}), 403, apierr.Forbidden)
	rec := callSession(t, fx.s, fx.adminSess, "POST", path,
		map[string]any{"method": "link", "admin_password": testPassword})
	wantStatus(t, rec, 200)
	if got := decode[verificationJSON](t, rec); got.Token == "" {
		t.Fatalf("no link: %+v", got)
	}
}

// TestGoBackWithAWrongOriginIsRefused: the go-back route checks the Origin header before anything else. A wrong
// origin is refused and the impersonation keeps running.
func TestGoBackWithAWrongOriginIsRefused(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	imp, par := fx.signedInAsMember(t)
	req := jsonRequest(t, "POST", "/api/v1/auth/impersonation/end", nil)
	req.AddCookie(imp.cookie)
	req.AddCookie(&http.Cookie{Name: fx.s.returnCookieName(), Value: par})
	req.Header.Set("Origin", "https://elsewhere.example")
	req.Header.Set("X-CSRF-Token", imp.csrf)
	rec := httptest.NewRecorder()
	fx.s.Handler().ServeHTTP(rec, req)
	wantError(t, rec, 403, apierr.Forbidden)
	wantStatus(t, callSession(t, fx.s, imp, "GET", "/api/v1/me", nil), 200)

	// The route's own sameSite check refuses this first. Calling the handler directly checks the handler's check too.
	direct := httptest.NewRecorder()
	req = jsonRequest(t, "POST", "/api/v1/auth/impersonation/end", nil)
	req.AddCookie(imp.cookie)
	req.AddCookie(&http.Cookie{Name: fx.s.returnCookieName(), Value: par})
	req.Header.Set("Origin", "https://elsewhere.example")
	req.Header.Set("X-CSRF-Token", imp.csrf)
	err := fx.s.handleEndImpersonation(direct, req)
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Code != apierr.Forbidden {
		t.Fatalf("handler with a wrong origin: %v, want forbidden", err)
	}
	wantStatus(t, callSession(t, fx.s, imp, "GET", "/api/v1/me", nil), 200)
}

// TestTenantAccessOffEndsARunningSignInAs: turning admin.tenant_access off ends a sign-in-as at once. Go back
// still restores the admin's session.
func TestTenantAccessOffEndsARunningSignInAs(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	imp, par := fx.signedInAsMember(t)
	wantStatus(t, callSession(t, fx.s, imp, "GET", "/api/v1/me", nil), 200)
	fx.s.cfg.Admin.TenantAccess = false
	e := wantError(t, callSession(t, fx.s, imp, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)
	if e.Details["reason"] != reasonTenantAccessOff {
		t.Errorf("reason %v, want %s", e.Details["reason"], reasonTenantAccessOff)
	}
	rec := goBack(t, fx.s, imp, par)
	wantStatus(t, rec, 200)
	wantStatus(t, callSession(t, fx.s, fx.adminSess, "GET", "/api/v1/me", nil), 200)
}

// TestAdminSessionEndsItsSignInsAs: an admin who changes its password, signs out, or loses its session by five wrong
// passwords ends each sign-in-as it began, and each end is audited with its reason.
func TestAdminSessionEndsItsSignInsAs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, reason string
		do           func(t *testing.T, fx *impFix)
	}{
		{"password change", "admin_password_change", func(t *testing.T, fx *impFix) {
			wantStatus(t, callSession(t, fx.s, fx.adminSess, "POST", "/api/v1/me/password",
				map[string]any{"current_password": testPassword, "new_password": "another passphrase"}), 204)
		}},
		{"sign out", "admin_signed_out", func(t *testing.T, fx *impFix) {
			wantStatus(t, callSession(t, fx.s, fx.adminSess, "POST", "/api/v1/auth/logout", nil), 204)
		}},
		{"five wrong passwords", "admin_session_ended", func(t *testing.T, fx *impFix) {
			for i := 0; i < maxPasswordFails; i++ {
				callSession(t, fx.s, fx.adminSess, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.member.ID+"/sign-in",
					map[string]any{"admin_password": "wrong password here"})
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fx := newImpFix(t, true)
			imp, _ := fx.signedInAsMember(t)
			c.do(t, fx)
			if got := callSession(t, fx.s, imp, "GET", "/api/v1/me", nil); got.Code != 401 {
				t.Errorf("the sign-in-as still runs: status %d", got.Code)
			}
			ended := findAudit(fx.auditOf(t, fx.accKey), "impersonation.end")
			if ended == nil || ended.Details["reason"] != c.reason {
				t.Errorf("impersonation.end is missing or has the wrong reason, want %s", c.reason)
			}
		})
	}
}
