// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/mail"
)

var expiryPattern = regexp.MustCompile(`\d{4}-\d{2}-\d{2} \d{2}:\d{2} UTC`)

// TestMailUsesTheRecipientsLocale checks that a verification goes out in the locale of its user, a
// regional tag resolves to its base language, and the expiry is written the same way in every
// language.
func TestMailUsesTheRecipientsLocale(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	users := "/api/v1/accounts/" + acc.ID + "/users"

	wantStatus(t, call(t, s, "POST", users, key,
		map[string]any{"email": "ana@example.com", "locale": "pt-BR", "verification": "email"}), 201)
	wantStatus(t, call(t, s, "POST", users, key,
		map[string]any{"email": "ahmad@example.com", "locale": "ar", "verification": "email"}), 201)
	msgs := rec.Messages()
	if len(msgs) != 2 || msgs[0].Subject != "Verifique a sua conta" || msgs[1].Subject != "تأكيد الحساب" {
		t.Fatalf("subjects = %+v", msgs)
	}
	for _, m := range msgs {
		if !expiryPattern.MatchString(m.Body) {
			t.Errorf("expiry is not locale-neutral in %q", m.Body)
		}
	}
}

// TestPatchMe covers a signed-in user changing their own display name and locale.
func TestPatchMe(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	ann, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")

	rec := callSession(t, s, sess, "PATCH", "/api/v1/me", map[string]any{"display_name": "Ann", "locale": "fr-CA"})
	wantStatus(t, rec, 200)
	got := decode[meResponse](t, rec)
	if got.User.ID != ann.ID || got.User.DisplayName != "Ann" || got.User.Locale != "fr-CA" ||
		got.Account == nil || got.Account.ID != acc.ID || got.CSRFToken != sess.csrf {
		t.Fatalf("PATCH /me = %+v", got)
	}
	// The same shape as GET /me.
	me := decode[meResponse](t, callSession(t, s, sess, "GET", "/api/v1/me", nil))
	if me.User.Locale != "fr-CA" || me.Account == nil || me.CSRFToken != got.CSRFToken {
		t.Errorf("GET /me disagrees: %+v", me)
	}
	// Only the fields given change; a blank display name clears it.
	wantStatus(t, callSession(t, s, sess, "PATCH", "/api/v1/me", map[string]any{"display_name": ""}), 200)
	if me := decode[meResponse](t, callSession(t, s, sess, "GET", "/api/v1/me", nil)); me.User.DisplayName != "" || me.User.Locale != "fr-CA" {
		t.Errorf("after clearing: %+v", me.User)
	}

	// The existing rules apply: a bad locale, an over-long name and an unknown field are refused.
	wantError(t, callSession(t, s, sess, "PATCH", "/api/v1/me", map[string]any{"locale": "en US"}), 400, apierr.InvalidRequest)
	long := make([]byte, 0, 300)
	for range 201 {
		long = append(long, 'a')
	}
	wantError(t, callSession(t, s, sess, "PATCH", "/api/v1/me", map[string]any{"display_name": string(long)}), 400, apierr.InvalidRequest)
	wantError(t, callSession(t, s, sess, "PATCH", "/api/v1/me", map[string]any{"email": "x@example.com"}), 400, apierr.InvalidRequest)

	// Role and status are not reachable from here.
	wantError(t, callSession(t, s, sess, "PATCH", "/api/v1/me", map[string]any{"role": "owner"}), 400, apierr.InvalidRequest)

	// It is a session route: a key is refused, and the session needs its CSRF token and Origin.
	wantError(t, call(t, s, "PATCH", "/api/v1/me", key, map[string]any{"locale": "es"}), 401, apierr.Unauthenticated)
	noCSRF := jsonRequest(t, "PATCH", "/api/v1/me", map[string]any{"locale": "es"})
	noCSRF.AddCookie(sess.cookie)
	noCSRF.Header.Set("Origin", s.origin)
	wantError(t, httptestRecorder(t, s, noCSRF), 403, apierr.Forbidden)
	wrongOrigin := jsonRequest(t, "PATCH", "/api/v1/me", map[string]any{"locale": "es"})
	wrongOrigin.AddCookie(sess.cookie)
	wrongOrigin.Header.Set("Origin", "https://evil.example.net")
	wrongOrigin.Header.Set("X-CSRF-Token", sess.csrf)
	// A session from another site is refused as unauthenticated: the cookie never counts there.
	wantError(t, httptestRecorder(t, s, wrongOrigin), 401, apierr.Unauthenticated)

	// The change is audited as user.update.
	entries := decode[listResponse[auditJSON]](t, call(t, s, "GET", "/api/v1/accounts/"+acc.ID+"/audit", admin, nil))
	found := false
	for _, e := range entries.Items {
		if e.Action == "user.update" && e.Target == ann.ID && e.Details["locale"] == "fr-CA" {
			found = true
		}
	}
	if !found {
		t.Error("the change is not audited as user.update")
	}
}

// httptestRecorder sends a prepared request through the server and returns the response.
func httptestRecorder(t *testing.T, s *Server, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// TestPatchMeForAdmin checks that an admin's own PATCH /me answers with no account.
func TestPatchMeForAdmin(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	_, sess := newAdminUser(t, s, admin, "root@example.com")
	got := decode[meResponse](t, callSession(t, s, sess, "PATCH", "/api/v1/me", map[string]any{"locale": "he"}))
	if got.Account != nil || got.User.Locale != "he" {
		t.Errorf("admin PATCH /me = %+v", got)
	}
}
