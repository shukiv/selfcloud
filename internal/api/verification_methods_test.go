package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"selfcloud/internal/apierr"
	"selfcloud/internal/mail"
)

// TestSessionCannotUseLinkOrNone checks that a signed-in caller, owner or member, may not ask for
// a link or skip verification. A link would hand a token to the session, which could then set
// another user's password.
func TestSessionCannotUseLinkOrNone(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	users := "/api/v1/accounts/" + acc.ID + "/users"

	owner, ownerSess := verifiedUser(t, s, key, acc.ID, "owner@example.com")
	wantStatus(t, call(t, s, "PATCH", users+"/"+owner.ID, key, map[string]any{"role": "owner"}), 200)
	_, memberSess := verifiedUser(t, s, key, acc.ID, "member@example.com")
	pending := decode[userResponse](t, call(t, s, "POST", users, key,
		map[string]any{"email": "pending@example.com", "verification": "link"}))
	target, _ := verifiedUser(t, s, key, acc.ID, "target@example.com")

	// Create with link and none. A member cannot manage the account at all, so it gets 404; the
	// owner gets 403 for the method.
	for _, m := range []string{"link", "none"} {
		wantError(t, callSession(t, s, ownerSess, "POST", users,
			map[string]any{"email": "owner-" + m + "@example.com", "verification": m}), 403, apierr.Forbidden)
		wantError(t, callSession(t, s, memberSess, "POST", users,
			map[string]any{"email": "member-" + m + "@example.com", "verification": m}), 404, apierr.NotFound)
	}
	// Resend a verification, and reset a password, as a link or with none.
	wantError(t, callSession(t, s, ownerSess, "POST", users+"/"+pending.ID+"/verification",
		map[string]any{"method": "link"}), 403, apierr.Forbidden)
	wantError(t, callSession(t, s, ownerSess, "POST", users+"/"+pending.ID+"/verification",
		map[string]any{"method": "none"}), 403, apierr.Forbidden)
	wantError(t, callSession(t, s, ownerSess, "POST", users+"/"+target.ID+"/password-reset",
		map[string]any{"method": "link"}), 403, apierr.Forbidden)

	// The refused calls changed nothing: the pending user is still pending.
	if got := decode[userJSON](t, call(t, s, "GET", users+"/"+pending.ID, key, nil)); got.Status != "pending" {
		t.Fatalf("pending user status = %s", got.Status)
	}
}

// TestSessionResponsesCarryNoToken checks that email delivery, the one method a session may use,
// returns no token or link in the response.
func TestSessionResponsesCarryNoToken(t *testing.T) {
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	users := "/api/v1/accounts/" + acc.ID + "/users"

	owner, sess := verifiedUser(t, s, key, acc.ID, "owner@example.com")
	wantStatus(t, call(t, s, "PATCH", users+"/"+owner.ID, key, map[string]any{"role": "owner"}), 200)
	target, _ := verifiedUser(t, s, key, acc.ID, "target@example.com")

	created := callSession(t, s, sess, "POST", users, map[string]any{"email": "new@example.com", "verification": "email"})
	wantStatus(t, created, 201)
	reset := callSession(t, s, sess, "POST", users+"/"+target.ID+"/password-reset", map[string]any{"method": "email"})
	wantStatus(t, reset, 200)

	for _, r := range []*httptest.ResponseRecorder{created, reset} {
		body := r.Body.String()
		for _, field := range []string{`"token"`, `"url"`} {
			if strings.Contains(body, field) {
				t.Errorf("session response carries %s: %s", field, body)
			}
		}
	}
	// The mails still go out, to the users.
	if n := len(rec.Messages()); n < 2 {
		t.Errorf("mails sent = %d, want at least 2", n)
	}
}
