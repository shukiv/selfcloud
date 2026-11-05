// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
)

// guardCall runs checkEmailChange for a request carrying principal p, asking for a change of address by email. The method is
// email, the one a session uses, so the method check does not refuse first and the guard is what answers.
func guardCall(t *testing.T, s *Server, p *auth.Principal) *apierr.Error {
	t.Helper()
	email := "new@example.com"
	r := httptest.NewRequest("PATCH", "/api/v1/accounts/acc_x/users/usr_x", nil)
	r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, &reqInfo{principal: p}))
	_, err := s.checkEmailChange(httptest.NewRecorder(), r, "acc_x", "usr_x", &email, "email", "")
	if err == nil {
		t.Fatal("the guard let the change through")
	}
	var e *apierr.Error
	if !errors.As(err, &e) {
		t.Fatalf("error %v is not an apierr", err)
	}
	return e
}

// TestEmailGuardRefusesATokenPrincipal: a principal carrying a delegated token is refused with the email_caller reason,
// even one that is an installation admin. managedAccount refuses a token on this route today; the guard stays so that a
// route change cannot open it.
func TestEmailGuardRefusesATokenPrincipal(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.Admin.TenantAccess = true })
	p := &auth.Principal{Kind: auth.KindUser, ID: "usr_adm", UserID: "usr_adm", Role: auth.RoleAdmin, Token: &auth.TokenScope{}}
	e := guardCall(t, s, p)
	if e.Code != apierr.Forbidden || e.Details["reason"] != reasonEmailCaller {
		t.Errorf("token principal refused with %s %v, want forbidden with reason %s", e.Code, e.Details["reason"], reasonEmailCaller)
	}
}

// TestEmailGuardRefusesAnImpersonatingAdminSession: an admin's session that carries an impersonation is refused by the
// requireAdminSession guard, which names an admin's own session. Its session cookie is absent here, so the refusal
// must come from that guard and not from the password check that follows it.
func TestEmailGuardRefusesAnImpersonatingAdminSession(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.Admin.TenantAccess = true })
	p := &auth.Principal{Kind: auth.KindUser, ID: "usr_adm", UserID: "usr_adm", Role: auth.RoleAdmin, Impersonator: "usr_other"}
	e := guardCall(t, s, p)
	if e.Code != apierr.Forbidden || !strings.Contains(e.Message, "signed in on its own session") {
		t.Errorf("impersonating admin session refused with %s %q, want the own-session refusal", e.Code, e.Message)
	}
}

// TestImpersonatingSessionCannotChangeAnEmailEndToEnd: an admin signed in as a member gets the router's refusal for this
// route (impRefuse), with reason impersonation, and the member's address is unchanged.
func TestImpersonatingSessionCannotChangeAnEmailEndToEnd(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	rec := fx.signInAs(t, fx.member.ID)
	wantStatus(t, rec, 200)
	imp := sessionFrom(t, fx.s, rec)
	e := wantError(t, callSession(t, fx.s, imp, "PATCH", userPath(fx.acc.ID, fx.member.ID),
		map[string]any{"email": "taken@example.com", "verification": "none"}), 403, apierr.Forbidden)
	if e.Details["reason"] != reasonImpersonation {
		t.Errorf("refused with reason %v, want %s", e.Details["reason"], reasonImpersonation)
	}
	if got := emailOf(t, fx.s, fx.accKey, fx.acc.ID, fx.member.ID); got.Email != "member@example.com" {
		t.Errorf("an impersonating session moved the address to %s", got.Email)
	}
}
