// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
)

// sec1 item 12 (decision 84): an installation admin's session ends sooner than a user's: at admin_idle_timeout unused
// and admin_max_age in all, unless the user limits are shorter still.
func TestAdminSessionEndsSoonerThanAUsersSession(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	now := time.Now()
	if got, want := s.sessionExpiry(now, now, false, true), now.Add(12*time.Hour); !got.Equal(want) {
		t.Errorf("admin session expiry = %v, want %v (12 h idle)", got, want)
	}
	if got, want := s.sessionExpiry(now, now, false, false), now.Add(168*time.Hour); !got.Equal(want) {
		t.Errorf("user session expiry = %v, want %v (the user idle timeout)", got, want)
	}
}

// sec1 item 12 (decision 84): an admin session's link for a pending owner needs the admin's password, on the route that
// makes the account and on the route that sends the link again. Wrong or missing is refused as sign-in-as refuses.
func TestAdminSessionOwnerLinkNeedsThePassword(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.Admin.TenantAccess = true })
	admin := adminKey(t, s)
	_, adminSess := newAdminUser(t, s, admin, "ann@example.com")

	wantError(t, callSession(t, s, adminSess, "POST", "/api/v1/accounts", map[string]any{"name": "Acme",
		"owner": map[string]any{"email": "owner@example.com", "verification": "link"}}), 403, apierr.Forbidden)
	created := callSession(t, s, adminSess, "POST", "/api/v1/accounts", map[string]any{"name": "Acme",
		"admin_password": testPassword, "owner": map[string]any{"email": "owner@example.com", "verification": "link"}})
	wantStatus(t, created, 201)
	acc := decode[accountResponse](t, created)

	// A second pending owner of another account, sent a link again from the admin session.
	other := callSession(t, s, adminSess, "POST", "/api/v1/accounts", map[string]any{"name": "Beta",
		"admin_password": testPassword, "owner": map[string]any{"email": "boss@example.com", "verification": "email"}})
	wantStatus(t, other, 201)
	ob := decode[accountResponse](t, other)
	resend := "/api/v1/accounts/" + ob.ID + "/users/" + ob.Owner.ID + "/verification"
	wantError(t, callSession(t, s, adminSess, "POST", resend, map[string]any{"method": "link"}), 403, apierr.Forbidden)
	wantStatus(t, callSession(t, s, adminSess, "POST", resend,
		map[string]any{"method": "link", "admin_password": testPassword}), 200)
	_ = acc
}

// sec1 item 12, review F4 (decision 84): an admin session that asks for a link to a new pending owner through the user
// create route needs the admin's password, as the owner link at account create and the resend do. Without it the
// link is refused; with it the owner is created.
func TestAdminSessionCreateUserOwnerLinkNeedsThePassword(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.Admin.TenantAccess = true })
	admin := adminKey(t, s)
	_, adminSess := newAdminUser(t, s, admin, "ann@example.com")
	created := callSession(t, s, adminSess, "POST", "/api/v1/accounts", map[string]any{"name": "Acme",
		"admin_password": testPassword, "owner": map[string]any{"email": "owner@example.com", "verification": "email"}})
	wantStatus(t, created, 201)
	acc := decode[accountResponse](t, created)
	users := "/api/v1/accounts/" + acc.ID + "/users"
	newOwner := map[string]any{"email": "new@example.com", "role": "owner", "verification": "link"}

	wantError(t, callSession(t, s, adminSess, "POST", users, newOwner), 403, apierr.Forbidden)
	withPassword := map[string]any{"email": "new@example.com", "role": "owner", "verification": "link",
		"admin_password": testPassword}
	wantStatus(t, callSession(t, s, adminSess, "POST", users, withPassword), 201)
}

// sec1 item 12, review F7 (decision 84): the admin's password is confirmed per signed-in session. A delegated token has
// no session, so with no session confirmAdminPassword refuses at once, with a message that says a signed-in session is
// needed. It does not count the failures under an empty key, and five wrong passwords answer 403, never 500.
// No legitimate flow sends a token here: each route that confirms the password requires a session.
func TestConfirmAdminPasswordNeedsASession(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	adminUser, _ := newAdminUser(t, s, admin, "ann@example.com")
	acc, _ := newAccount(t, s, admin, "Acme")
	p := &auth.Principal{Kind: auth.KindUser, ID: adminUser.ID, UserID: adminUser.ID, Role: auth.RoleAdmin}
	r := httptest.NewRequest("POST", "/api/v1/accounts/"+acc.ID+"/keys", nil)

	err := s.confirmAdminPassword(httptest.NewRecorder(), r, p, nil, wayCreateKey, "", acc.ID, testPassword)
	if err == nil {
		t.Fatal("a delegated token with the right password was taken as the admin's confirmation")
	}
	if !strings.Contains(err.Error(), "signed-in session") {
		t.Errorf("the refusal does not say a signed-in session is needed: %v", err)
	}
	for i := 0; i < 5; i++ {
		err := s.confirmAdminPassword(httptest.NewRecorder(), r, p, nil, wayCreateKey, "", acc.ID, "wrong password")
		if e := apierr.From(err); e != nil && e.Code == apierr.Internal {
			t.Fatalf("wrong password %d with no session: %v", i+1, err)
		}
	}
}

// sec1 item 12 (decision 84): an admin session makes an account key only with the admin's own password. A wrong or
// missing one is refused, and the right one makes the key.
func TestAdminSessionKeyNeedsThePassword(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	path := "/api/v1/accounts/" + fx.acc.ID + "/keys"

	wantError(t, callSession(t, fx.s, fx.adminSess, "POST", path, map[string]any{"name": "ci"}), 403, apierr.Forbidden)
	wantError(t, callSession(t, fx.s, fx.adminSess, "POST", path,
		map[string]any{"name": "ci", "admin_password": "wrong password here"}), 403, apierr.Forbidden)
	wantStatus(t, callSession(t, fx.s, fx.adminSess, "POST", path,
		map[string]any{"name": "ci", "admin_password": testPassword}), 201)
}
