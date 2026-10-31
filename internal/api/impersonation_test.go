// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// placeholders matches a path parameter in a route pattern, such as {usr}.
var placeholders = regexp.MustCompile(`\{[^}]+\}`)

// impFix is an installation admin with its own session, an account with a member, and the account key.
type impFix struct {
	s         *Server
	admin     string // the installation admin key, for setup
	adminUser userResponse
	adminSess testSession // the admin's own session
	acc       accountJSON
	accKey    string
	member    userResponse
	memSess   testSession // the member's own session, from its verification
	mailer    *mail.Recorder
}

func newImpFix(t *testing.T, tenantAccess bool) *impFix {
	t.Helper()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec, func(c *config.Config) { c.Admin.TenantAccess = tenantAccess })
	admin := adminKey(t, s)
	adminUser, adminSess := newAdminUser(t, s, admin, "ann@example.com")
	acc, accKey := newAccount(t, s, admin, "Acme")
	member, memSess := verifiedUser(t, s, accKey, acc.ID, "member@example.com")
	return &impFix{s: s, admin: admin, adminUser: adminUser, adminSess: adminSess, acc: acc, accKey: accKey,
		member: member, memSess: memSess, mailer: rec}
}

// signInAs asks for a sign-in-as of the member, with the admin's own password.
func (fx *impFix) signInAs(t *testing.T, usr string) *httptest.ResponseRecorder {
	t.Helper()
	return callSession(t, fx.s, fx.adminSess, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users/"+usr+"/sign-in",
		map[string]any{"admin_password": testPassword})
}

// parked returns the admin's session cookie value that a sign-in-as parked in the response.
func parked(t *testing.T, s *Server, rec *httptest.ResponseRecorder) string {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == s.returnCookieName() && c.MaxAge >= 0 {
			return c.Value
		}
	}
	t.Fatalf("no parked admin cookie in %v", rec.Header())
	return ""
}

// goBack calls the Go back route with the impersonation's session and the parked admin cookie.
func goBack(t *testing.T, s *Server, imp testSession, parkedValue string) *httptest.ResponseRecorder {
	t.Helper()
	req := jsonRequest(t, "POST", "/api/v1/auth/impersonation/end", nil)
	req.AddCookie(imp.cookie)
	if parkedValue != "" {
		req.AddCookie(&http.Cookie{Name: s.returnCookieName(), Value: parkedValue})
	}
	req.Header.Set("Origin", s.origin)
	req.Header.Set("X-CSRF-Token", imp.csrf)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// backdatedImpersonation inserts an impersonation of the member that began at created and was last used at lastSeen,
// as the admin's own session parked. It returns the browser's session and its hash.
func (fx *impFix) backdatedImpersonation(t *testing.T, created, lastSeen time.Time) (testSession, []byte) {
	t.Helper()
	secret, hash := auth.NewToken()
	csrf, _ := auth.NewToken()
	sess := &store.Session{IDHash: hash, UserID: fx.member.ID, AccountID: fx.acc.ID, CSRFToken: csrf,
		CreatedAt: created, LastSeenAt: lastSeen, ExpiresAt: created.Add(impersonationTTL),
		ImpersonatorID: fx.adminUser.ID, ImpersonatorSessionHash: auth.HashToken(fx.adminSess.cookie.Value)}
	err := fx.s.store.Write(context.Background(), func(tx *store.Tx) error {
		return tx.InsertSession(context.Background(), sess)
	})
	if err != nil {
		t.Fatal(err)
	}
	return testSession{cookie: &http.Cookie{Name: fx.s.sessionCookie, Value: secret}, csrf: csrf}, hash
}

// signedInAsMember signs the admin in as the member and returns the impersonation session and the parked cookie.
func (fx *impFix) signedInAsMember(t *testing.T) (testSession, string) {
	t.Helper()
	rec := fx.signInAs(t, fx.member.ID)
	wantStatus(t, rec, 200)
	return sessionFrom(t, fx.s, rec), parked(t, fx.s, rec)
}

// auditOf reads the account's audit log as the account key sees it.
func (fx *impFix) auditOf(t *testing.T, key string) []auditJSON {
	t.Helper()
	rec := call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/audit", key, nil)
	wantStatus(t, rec, 200)
	return decode[listResponse[auditJSON]](t, rec).Items
}

func findAudit(items []auditJSON, action string) *auditJSON {
	for i := range items {
		if items[i].Action == action {
			return &items[i]
		}
	}
	return nil
}

func TestSignInAsSessionAndBanner(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	imp, par := fx.signedInAsMember(t)

	me := decode[meResponse](t, callSession(t, fx.s, imp, "GET", "/api/v1/me", nil))
	if me.User.ID != fx.member.ID || me.Impersonation == nil || me.Account == nil || me.Account.ID != fx.acc.ID {
		t.Fatalf("me while impersonating = %+v", me)
	}
	if left := time.Until(me.Impersonation.EndsAt); left <= 59*time.Minute || left > time.Hour {
		t.Errorf("the impersonation ends in %v, want about an hour", left)
	}
	if me.PersonalFolderID == nil {
		t.Fatal("the member's My files is not reached while impersonating")
	}
	// The browser holds the new session in the session cookie and the admin's parked session under its own name.
	if par == imp.cookie.Value || par != fx.adminSess.cookie.Value {
		t.Fatal("the parked cookie does not hold the admin's own session")
	}
	// The member's own files are reached as the member: a directory in My files, audited for both (see the audit test).
	wantStatus(t, callSession(t, fx.s, imp, "POST", "/api/v1/folders/"+*me.PersonalFolderID+"/directories",
		map[string]any{"path": "Notes"}), 201)

	// Back to the admin: the parked session is restored, and the impersonated cookie is refused from then on.
	rec := goBack(t, fx.s, imp, par)
	wantStatus(t, rec, 200)
	back := decode[signedIn](t, rec)
	if back.User.ID != fx.adminUser.ID {
		t.Fatalf("go back signed in %s, want the admin", back.User.ID)
	}
	restored := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == fx.s.sessionCookie && c.Value == fx.adminSess.cookie.Value {
			restored = true
		}
	}
	if !restored {
		t.Error("go back did not restore the admin's own session cookie")
	}
	wantError(t, callSession(t, fx.s, imp, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)
	wantStatus(t, callSession(t, fx.s, fx.adminSess, "GET", "/api/v1/me", nil), 200)
}

func TestSignInAsRefusals(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	imp, par := fx.signedInAsMember(t)
	s := fx.s

	// Refused while impersonating, with the reason: the chain, a password change, a key, a token, sign-out.
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/v1/me/password"},
		{"PATCH", "/api/v1/me"},
		{"POST", "/api/v1/accounts/" + fx.acc.ID + "/keys"},
		{"POST", "/api/v1/accounts/" + fx.acc.ID + "/tokens"},
		{"POST", "/api/v1/accounts/" + fx.acc.ID + "/users/" + fx.member.ID + "/sign-in"},
		{"POST", "/api/v1/auth/logout"},
	} {
		e := wantError(t, callSession(t, s, imp, c.method, c.path, map[string]any{}), 403, apierr.Forbidden)
		if e.Details["reason"] != reasonImpersonation {
			t.Errorf("%s %s: reason %v, want %s", c.method, c.path, e.Details["reason"], reasonImpersonation)
		}
	}

	// The admin's own session may not be used to sign in as a user it is not allowed to reach: an admin target and a
	// user of another account are both not found.
	wantError(t, fx.signInAs(t, fx.adminUser.ID), 404, apierr.NotFound)
	acc2, key2 := newAccount(t, s, fx.admin, "Other")
	other, _ := verifiedUser(t, s, key2, acc2.ID, "other@example.com")
	wantError(t, callSession(t, s, fx.adminSess, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users/"+other.ID+"/sign-in",
		map[string]any{"admin_password": testPassword}), 404, apierr.NotFound)

	// Go back needs the CSRF token of the impersonation, and it refuses when there is nothing to go back from.
	req := jsonRequest(t, "POST", "/api/v1/auth/impersonation/end", nil)
	req.AddCookie(imp.cookie)
	req.AddCookie(&http.Cookie{Name: s.returnCookieName(), Value: par})
	req.Header.Set("Origin", s.origin)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	wantStatus(t, rec, 403)
	wantError(t, callSession(t, s, fx.adminSess, "POST", "/api/v1/auth/impersonation/end", nil), 409, apierr.Conflict)

	// A user who has to change its password cannot be signed in as.
	wantStatus(t, callSession(t, s, fx.adminSess, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.member.ID+"/password",
		map[string]any{"password": "a fresh password 1", "admin_password": testPassword}), 204)
	e := wantError(t, fx.signInAs(t, fx.member.ID), 409, apierr.Conflict)
	if e.Details["reason"] != reasonPasswordChangeRequired {
		t.Errorf("must-change target: reason %v", e.Details["reason"])
	}

	// The typed password ended the impersonation (its end is audited), so the old cookie fails now.
	wantError(t, callSession(t, s, imp, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)
}

func TestImpersonationHardLimit(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	// An impersonation that began 61 minutes ago is refused, whatever its idle expiry says.
	old, _ := fx.backdatedImpersonation(t, time.Now().Add(-61*time.Minute), time.Now().Add(-61*time.Minute))
	e := wantError(t, callSession(t, fx.s, old, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)
	if e.Details["reason"] != "impersonation_ended" {
		t.Errorf("reason %v, want impersonation_ended", e.Details["reason"])
	}
	// The row stays, so Go back still restores the admin, and the end is audited as expired.
	wantStatus(t, goBack(t, fx.s, old, fx.adminSess.cookie.Value), 200)
	var reason any
	for _, it := range fx.auditOf(t, fx.accKey) {
		if it.Action == "impersonation.end" {
			reason = it.Details["reason"]
		}
	}
	if reason != "expired" {
		t.Errorf("end reason %v, want expired", reason)
	}
}

func TestImpersonationIsNotRenewed(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	// Used a minute after its last use, 50 minutes in: the use must not push the expiry past the hard limit.
	created := time.Now().Add(-50 * time.Minute)
	sess, hash := fx.backdatedImpersonation(t, created, time.Now().Add(-5*time.Minute))
	wantStatus(t, callSession(t, fx.s, sess, "GET", "/api/v1/me", nil), 200)
	row, err := fx.s.store.SessionByHash(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if limit := created.Add(impersonationTTL); row.ExpiresAt.After(limit.Add(time.Second)) {
		t.Errorf("a use moved the expiry to %v, past the hard limit %v", row.ExpiresAt, limit)
	}
}

func TestTenantAccessOffRefusesTheThreeWays(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, false)
	s := fx.s
	wantError(t, fx.signInAs(t, fx.member.ID), 403, apierr.Forbidden)
	for _, c := range []struct {
		name, path string
		body       any
	}{
		{"typed password", "/api/v1/accounts/" + fx.acc.ID + "/users/" + fx.member.ID + "/password",
			map[string]any{"password": "a fresh password 1", "admin_password": testPassword}},
		{"reset link", "/api/v1/accounts/" + fx.acc.ID + "/users/" + fx.member.ID + "/password-reset",
			map[string]any{"method": "link"}},
	} {
		e := wantError(t, callSession(t, s, fx.adminSess, "POST", c.path, c.body), 403, apierr.Forbidden)
		if e.Details["reason"] != reasonTenantAccessOff {
			t.Errorf("%s: reason %v, want %s", c.name, e.Details["reason"], reasonTenantAccessOff)
		}
	}
	// The email reset reaches the user, so it stays open, and its answer carries no token.
	rec := callSession(t, s, fx.adminSess, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.member.ID+"/password-reset",
		map[string]any{"method": "email"})
	wantStatus(t, rec, 200)
	if got := decode[verificationJSON](t, rec); got.Token != "" {
		t.Error("the email reset carried a token")
	}
	// The admin's client learns the setting; a member does not.
	me := decode[map[string]any](t, callSession(t, s, fx.adminSess, "GET", "/api/v1/me", nil))
	if me["tenant_access"] != false || me["mail_configured"] != true {
		t.Errorf("admin /me = %v, want tenant_access false and mail_configured true", me)
	}
	mem := decode[map[string]any](t, callSession(t, s, fx.memSess, "GET", "/api/v1/me", nil))
	if _, ok := mem["tenant_access"]; ok {
		t.Error("a member's /me reports the setting")
	}
}

func TestTypedPasswordForcesChange(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	s := fx.s
	const newPw = "a fresh password 1"
	path := "/api/v1/accounts/" + fx.acc.ID + "/users/" + fx.member.ID + "/password"

	// A wrong admin password is refused, and counted: the fifth in a row ends the admin's session.
	for i := 0; i < maxPasswordFails-1; i++ {
		wantError(t, callSession(t, s, fx.adminSess, "POST", path,
			map[string]any{"password": newPw, "admin_password": "not my password"}), 403, apierr.Forbidden)
	}
	wantError(t, callSession(t, s, fx.adminSess, "POST", path,
		map[string]any{"password": newPw, "admin_password": "not my password"}), 401, apierr.Unauthenticated)
	fx.adminSess = fx.signInAdminAgain(t)

	// The password rules apply as they do anywhere else.
	wantError(t, callSession(t, s, fx.adminSess, "POST", path,
		map[string]any{"password": "short", "admin_password": testPassword}), 400, apierr.InvalidRequest)

	wantStatus(t, callSession(t, s, fx.adminSess, "POST", path,
		map[string]any{"password": newPw, "admin_password": testPassword}), 204)
	// The member's session has ended with its password.
	wantError(t, callSession(t, s, fx.memSess, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)

	// The member signs in with the new password, and is confined until it changes it.
	rec := login(t, s, "member@example.com", newPw, "")
	wantStatus(t, rec, 200)
	sess := sessionFrom(t, s, rec)
	me := decode[meResponse](t, callSession(t, s, sess, "GET", "/api/v1/me", nil))
	if !me.MustChangePassword {
		t.Fatal("the typed password did not set the must-change flag")
	}
	e := wantError(t, callSession(t, s, sess, "GET", "/api/v1/me/storage/largest", nil), 403, apierr.Forbidden)
	if e.Details["reason"] != reasonPasswordChangeRequired {
		t.Errorf("confined user: reason %v", e.Details["reason"])
	}
	wantStatus(t, callSession(t, s, sess, "POST", "/api/v1/me/password",
		map[string]any{"current_password": newPw, "new_password": "my own password 2"}), 204)

	// The audit entry holds no value.
	for _, it := range fx.auditOf(t, fx.accKey) {
		raw, _ := json.Marshal(it)
		if strings.Contains(string(raw), newPw) || strings.Contains(string(raw), testPassword) {
			t.Errorf("an audit entry holds a password: %s", raw)
		}
	}
	// Changed: the member reaches the rest, and the flag is off.
	sess = sessionFrom(t, s, login(t, s, "member@example.com", "my own password 2", ""))
	wantStatus(t, callSession(t, s, sess, "GET", "/api/v1/me/storage/largest", nil), 200)
	if decode[meResponse](t, callSession(t, s, sess, "GET", "/api/v1/me", nil)).MustChangePassword {
		t.Error("the flag stayed on after the change")
	}
}

// signInAdminAgain signs the admin in again, after a test ended its session with wrong passwords.
func (fx *impFix) signInAdminAgain(t *testing.T) testSession {
	t.Helper()
	rec := login(t, fx.s, "ann@example.com", testPassword, "")
	wantStatus(t, rec, 200)
	return sessionFrom(t, fx.s, rec)
}

func TestResetLinkWhenAllowedClearsTheFlag(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	s := fx.s
	path := "/api/v1/accounts/" + fx.acc.ID + "/users/" + fx.member.ID + "/password-reset"
	// A typed password first, so there is a must-change flag for the reset to clear.
	wantStatus(t, callSession(t, s, fx.adminSess, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.member.ID+"/password",
		map[string]any{"password": "a fresh password 1", "admin_password": testPassword}), 204)
	// A reset link is shown to the admin once. It is one-time and expires as any other reset does.
	rec := callSession(t, s, fx.adminSess, "POST", path, map[string]any{"method": "link", "admin_password": testPassword})
	wantStatus(t, rec, 200)
	token := decode[verificationJSON](t, rec).Token
	if token == "" {
		t.Fatal("the reset link carried no token")
	}
	// Using it sets the password and clears the must-change flag.
	rec = call(t, s, "POST", "/api/v1/auth/password/reset", "", map[string]any{"token": token, "password": "a link password 2"})
	wantStatus(t, rec, 200)
	if decode[signedIn](t, rec).User.ID != fx.member.ID {
		t.Fatal("the reset link signed in someone else")
	}
	// Used once: the same link is refused the second time.
	if rec := call(t, s, "POST", "/api/v1/auth/password/reset", "", map[string]any{"token": token,
		"password": "a link password 3"}); rec.Code == 200 {
		t.Error("a reset link worked twice")
	}
	sess := sessionFrom(t, s, login(t, s, "member@example.com", "a link password 2", ""))
	if decode[meResponse](t, callSession(t, s, sess, "GET", "/api/v1/me", nil)).MustChangePassword {
		t.Error("the reset did not clear the must-change flag")
	}
}

func TestImpersonatedWriteIsAuditedForBothPeople(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	s := fx.s
	// An owner of the tenant, to read the audit log as the owner does.
	rec := call(t, s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users", fx.accKey,
		map[string]any{"email": "owner@example.com", "role": "owner", "verification": "link"})
	wantStatus(t, rec, 201)
	created := decode[userResponse](t, rec)
	rec = call(t, s, "POST", "/api/v1/auth/verify", "", map[string]any{"token": created.Verification.Token, "password": testPassword})
	wantStatus(t, rec, 200)
	owner := sessionFrom(t, s, rec)

	imp, _ := fx.signedInAsMember(t)
	me := decode[meResponse](t, callSession(t, s, imp, "GET", "/api/v1/me", nil))
	wantStatus(t, callSession(t, s, imp, "POST", "/api/v1/folders/"+*me.PersonalFolderID+"/directories",
		map[string]any{"path": "Impersonated"}), 201)

	// The owner reads the entry, though it is in the member's My files: an impersonated entry is not private.
	rec = callSession(t, s, owner, "GET", "/api/v1/accounts/"+fx.acc.ID+"/audit", nil)
	wantStatus(t, rec, 200)
	items := decode[listResponse[auditJSON]](t, rec).Items
	made := findAudit(items, "node.create")
	if made == nil {
		t.Fatal("the owner does not see the impersonated write in the audit log")
	}
	if made.Actor.ID != fx.member.ID || made.Details["impersonated_by"] != fx.adminUser.ID {
		t.Errorf("the write records actor %+v and details %v, want the member, impersonated by the admin", made.Actor, made.Details)
	}
	start := findAudit(items, "impersonation.start")
	if start == nil || start.Actor.ID != fx.adminUser.ID || start.Target != fx.member.ID {
		t.Errorf("impersonation.start = %+v, want the admin, targeting the member", start)
	}
}

func TestImpersonationEndsOnDisableAndAdminRemoval(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	s := fx.s
	imp, _ := fx.signedInAsMember(t)
	// A user disable ends it, and the end is audited under the admin.
	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.member.ID, fx.accKey,
		map[string]any{"status": "disabled"}), 200)
	wantError(t, callSession(t, s, imp, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)
	if end := findAudit(fx.auditOf(t, fx.accKey), "impersonation.end"); end == nil || end.Details["reason"] != "disabled" ||
		end.Actor.ID != fx.adminUser.ID {
		t.Errorf("disable: end = %+v", end)
	}
	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.member.ID, fx.accKey,
		map[string]any{"status": "active"}), 200)

	// An admin's removal ends the impersonations it began.
	// A second admin stays, so the first is not the last one.
	newAdminUser(t, s, fx.admin, "bob@example.com")
	imp2, _ := fx.signedInAsMember(t)
	wantStatus(t, call(t, s, "DELETE", "/api/v1/admins/"+fx.adminUser.ID, fx.admin, nil), 204)
	wantError(t, callSession(t, s, imp2, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)
	if end := findAudit(fx.auditOf(t, fx.accKey), "impersonation.end"); end == nil || end.Details["reason"] != "admin_removed" {
		t.Errorf("admin removal: end = %+v", end)
	}
}

// TestImpersonationRefusalsAreChecked walks the route table: every route the table refuses while impersonating must
// answer with the refusal, and every route must carry a class (routes_test.go checks the rest).
func TestImpersonationRefusalsAreChecked(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	s := fx.s
	imp, _ := fx.signedInAsMember(t)
	var refused int
	for _, rt := range s.routes() {
		if rt.imp != impRefuse || !(rt.auth || rt.session || rt.sameSite) {
			continue
		}
		refused++
		path := placeholders.ReplaceAllString(rt.path, "x")
		e := wantError(t, callSession(t, s, imp, rt.method, path, map[string]any{}), 403, apierr.Forbidden)
		if e.Details["reason"] != reasonImpersonation {
			t.Errorf("%s %s while impersonating: reason %v", rt.method, rt.path, e.Details["reason"])
		}
	}
	if refused < 40 {
		t.Errorf("only %d routes are refused while impersonating; the table lost its classes", refused)
	}
}

func TestEveryRouteIsClassified(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	var mustChange []string
	for _, rt := range s.routes() {
		if rt.imp == impUnclassified {
			t.Errorf("route %s %s has no class for impersonation (set imp to impAllow or impRefuse)", rt.method, rt.path)
		}
		if rt.mustChange {
			mustChange = append(mustChange, rt.method+" "+rt.path)
		}
	}
	// Only these three routes stay open to a user whose password an admin typed: the change, sign-out, and the profile
	// the change page reads for its CSRF token.
	want := []string{"GET /api/v1/me", "POST /api/v1/me/password", "POST /api/v1/auth/logout"}
	sort.Strings(mustChange)
	sort.Strings(want)
	if strings.Join(mustChange, ",") != strings.Join(want, ",") {
		t.Errorf("must-change routes = %v, want %v", mustChange, want)
	}
}

func TestImpersonatedViewsCountNowhere(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	vr := fx.s.views
	node := &store.Node{ID: "nod_test", AccountID: fx.acc.ID, Kind: store.KindFile}
	fx.s.noteView(&auth.Principal{Kind: auth.KindUser, ID: fx.member.ID, UserID: fx.member.ID, AccountID: fx.acc.ID,
		Impersonator: fx.adminUser.ID}, node)
	if n := len(vr.ch); n != 0 {
		t.Errorf("an impersonated view was queued %d times", n)
	}
	fx.s.noteView(&auth.Principal{Kind: auth.KindUser, ID: fx.member.ID, UserID: fx.member.ID, AccountID: fx.acc.ID}, node)
	if n := len(vr.ch); n != 1 {
		t.Errorf("a plain view was queued %d times, want 1", n)
	}
}
