// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

func TestCreateUserVerificationMethods(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	users := "/api/v1/accounts/" + acc.ID + "/users"

	// email: the mail goes out and the answer says so.
	r := call(t, s, "POST", users, key, map[string]any{"email": "mail@example.com", "verification": "email"})
	wantStatus(t, r, 201)
	e := decode[userResponse](t, r)
	if e.Verification == nil || e.Verification.Method != "email" || e.Verification.Sent == nil || !*e.Verification.Sent {
		t.Fatalf("verification = %+v", e.Verification)
	}
	if e.Verification.URL != "" || e.Verification.Token != "" {
		t.Errorf("email method returned a link or token: %+v", e.Verification)
	}
	msgs := rec.Messages()
	if len(msgs) != 1 || msgs[0].To != "mail@example.com" {
		t.Fatalf("mails = %+v", msgs)
	}
	if msgs[0].Subject != "Verify your account" {
		t.Errorf("subject = %q", msgs[0].Subject)
	}

	// none: verified at once, no verification in the answer, and the user can sign in after a password is set.
	r = call(t, s, "POST", users, key, map[string]any{"email": "none@example.com", "verification": "none"})
	wantStatus(t, r, 201)
	n := decode[userResponse](t, r)
	if n.Status != store.UserActive || n.VerifiedAt == nil || n.Verification != nil {
		t.Fatalf("none = %+v", n)
	}

	// A failed send does not fail the request: the user exists, with sent false.
	rec.Fail = true
	r = call(t, s, "POST", users, key, map[string]any{"email": "down@example.com", "verification": "email"})
	wantStatus(t, r, 201)
	f := decode[userResponse](t, r)
	if f.Verification == nil || *f.Verification.Sent {
		t.Fatalf("failed send reported %+v", f.Verification)
	}
	wantStatus(t, call(t, s, "GET", users+"/"+f.ID, key, nil), 200)
	rec.Fail = false

	// An owner session may not skip verification.
	owner, sess := verifiedUser(t, s, key, acc.ID, "owner@example.com")
	wantStatus(t, call(t, s, "PATCH", users+"/"+owner.ID, key, map[string]any{"role": "owner"}), 200)
	wantError(t, callSession(t, s, sess, "POST", users, map[string]any{"email": "x@example.com", "verification": "none"}),
		403, apierr.Forbidden)
}

func TestCreateUserWithoutMail(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, nil)
	admin := adminKey(t, s)
	acc, _ := newAccount(t, s, admin, "Acme")
	users := "/api/v1/accounts/" + acc.ID + "/users"
	wantError(t, call(t, s, "POST", users, admin, map[string]any{"email": "a@example.com", "verification": "email"}),
		400, apierr.InvalidRequest)
	// Link still works without mail.
	wantStatus(t, call(t, s, "POST", users, admin, map[string]any{"email": "a@example.com", "verification": "link"}), 201)
}

func TestCreateUserIdempotentAndConflicts(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, _ := newAccount(t, s, admin, "Acme")
	users := "/api/v1/accounts/" + acc.ID + "/users"
	body := map[string]any{"email": "ann@example.com", "display_name": "Ann", "external_id": "ext-1",
		"verification": "link"}

	first := decode[userResponse](t, call(t, s, "POST", users, admin, body))
	if first.Verification == nil {
		t.Fatal("no verification on create")
	}
	// Repeating the same fields returns the same user, with no new verification.
	again := call(t, s, "POST", users, admin, body)
	wantStatus(t, again, 200)
	if got := decode[userJSON](t, again); got.ID != first.ID {
		t.Fatalf("retry made user %s, want %s", got.ID, first.ID)
	}
	// Different fields are a conflict that names the existing user.
	changed := map[string]any{"email": "ann@example.com", "display_name": "Anne", "external_id": "ext-1",
		"verification": "link"}
	e := wantError(t, call(t, s, "POST", users, admin, changed), 409, apierr.Conflict)
	if e.Details["id"] != first.ID {
		t.Errorf("details = %v", e.Details)
	}
	// The same email under another external_id is a duplicate.
	dup := map[string]any{"email": "ANN@example.com", "external_id": "ext-2", "verification": "link"}
	wantError(t, call(t, s, "POST", users, admin, dup), 409, apierr.Conflict)
	// A user with no external_id, same email.
	wantError(t, call(t, s, "POST", users, admin, map[string]any{"email": "ann@example.com", "verification": "link"}),
		409, apierr.Conflict)
}

func TestCreateUserValidation(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, _ := newAccount(t, s, admin, "Acme")
	users := "/api/v1/accounts/" + acc.ID + "/users"

	for name, body := range map[string]map[string]any{
		"display name in address": {"email": "Ann <ann@example.com>", "verification": "link"},
		"no email":                {"verification": "link"},
		"bad locale":              {"email": "a@example.com", "locale": "en US", "verification": "link"},
		"bad role":                {"email": "a@example.com", "role": "admin", "verification": "link"},
		"no verification":         {"email": "a@example.com"},
		"unknown verification":    {"email": "a@example.com", "verification": "sms"},
		"unknown field":           {"email": "a@example.com", "verification": "link", "nickname": "x"},
		"line break in name":      {"email": "a@example.com", "display_name": "Ann\r\nBcc: x@example.com", "verification": "link"},
	} {
		if rec := call(t, s, "POST", users, admin, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", name, rec.Code, rec.Body)
		}
	}
}

func TestUsersAreIsolatedByAccount(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	a, keyA := newAccount(t, s, admin, "A")
	b, keyB := newAccount(t, s, admin, "B")
	u := decode[userResponse](t, call(t, s, "POST", "/api/v1/accounts/"+a.ID+"/users", keyA,
		map[string]any{"email": "ann@example.com", "verification": "link"}))

	// Account B's key cannot see, change or delete A's user, and cannot list A's users.
	base := "/api/v1/accounts/" + a.ID + "/users/" + u.ID
	wantError(t, call(t, s, "GET", base, keyB, nil), 404, apierr.NotFound)
	wantError(t, call(t, s, "PATCH", base, keyB, map[string]any{"display_name": "x"}), 404, apierr.NotFound)
	wantError(t, call(t, s, "DELETE", base, keyB, nil), 404, apierr.NotFound)
	wantError(t, call(t, s, "GET", "/api/v1/accounts/"+a.ID+"/users", keyB, nil), 404, apierr.NotFound)
	// Even through B's own account path, A's user ID is not found.
	wantError(t, call(t, s, "GET", "/api/v1/accounts/"+b.ID+"/users/"+u.ID, keyB, nil), 404, apierr.NotFound)

	// The same email in B is a separate user.
	wantStatus(t, call(t, s, "POST", "/api/v1/accounts/"+b.ID+"/users", keyB,
		map[string]any{"email": "ann@example.com", "verification": "link"}), 201)
}

func TestMemberSessionCannotManageUsers(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, member := verifiedUser(t, s, key, acc.ID, "member@example.com")
	wantError(t, callSession(t, s, member, "GET", "/api/v1/accounts/"+acc.ID+"/users", nil), 404, apierr.NotFound)
	wantError(t, callSession(t, s, member, "POST", "/api/v1/accounts/"+acc.ID+"/users",
		map[string]any{"email": "x@example.com", "verification": "link"}), 404, apierr.NotFound)
	// The member can still read their own account through /me.
	wantStatus(t, callSession(t, s, member, "GET", "/api/v1/me", nil), 200)
}

func TestOwnerSessionManagesItsAccount(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	owner, sess := verifiedUser(t, s, key, acc.ID, "owner@example.com")
	// Before promotion the session is a member's: it sees no users.
	wantError(t, callSession(t, s, sess, "GET", "/api/v1/accounts/"+acc.ID+"/users", nil), 404, apierr.NotFound)
	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+acc.ID+"/users/"+owner.ID, key,
		map[string]any{"role": "owner"}), 200)
	// The role is read on every request, so the promotion applies to the open session at once.
	wantStatus(t, callSession(t, s, sess, "POST", "/api/v1/accounts/"+acc.ID+"/users",
		map[string]any{"email": "new@example.com", "verification": "email"}), 201)
	wantStatus(t, callSession(t, s, sess, "GET", "/api/v1/accounts/"+acc.ID+"/users", nil), 200)
}

func TestUpdateUser(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	base := "/api/v1/accounts/" + acc.ID + "/users"
	pending := decode[userResponse](t, call(t, s, "POST", base, key,
		map[string]any{"email": "ann@example.com", "verification": "link"}))

	// The email cannot change.
	wantError(t, call(t, s, "PATCH", base+"/"+pending.ID, key, map[string]any{"email": "new@example.com"}),
		400, apierr.InvalidRequest)
	// A user who never verified cannot be activated.
	wantError(t, call(t, s, "PATCH", base+"/"+pending.ID, key, map[string]any{"status": "active"}),
		409, apierr.Conflict)
	wantError(t, call(t, s, "PATCH", base+"/"+pending.ID, key, map[string]any{"status": "pending"}),
		400, apierr.InvalidRequest)

	got := decode[userJSON](t, call(t, s, "PATCH", base+"/"+pending.ID, key,
		map[string]any{"display_name": "Ann", "locale": "pt-BR", "external_id": "ext-9"}))
	if got.DisplayName != "Ann" || got.Locale != "pt-BR" || got.ExternalID == nil || *got.ExternalID != "ext-9" {
		t.Fatalf("updated = %+v", got)
	}
	// An external ID used by another user in the account is a conflict.
	other := decode[userResponse](t, call(t, s, "POST", base, key,
		map[string]any{"email": "bob@example.com", "external_id": "ext-10", "verification": "link"}))
	wantError(t, call(t, s, "PATCH", base+"/"+other.ID, key, map[string]any{"external_id": "ext-9"}),
		409, apierr.Conflict)

	// Disabling ends sessions; a verified user can be re-enabled.
	ann := signInAfterVerify(t, s, pending.Verification.Token)
	wantStatus(t, call(t, s, "PATCH", base+"/"+pending.ID, key, map[string]any{"status": "disabled"}), 200)
	wantError(t, callSession(t, s, ann, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)
	wantStatus(t, call(t, s, "PATCH", base+"/"+pending.ID, key, map[string]any{"status": "active"}), 200)
	wantStatus(t, login(t, s, "ann@example.com", testPassword, ""), 200)

	// Delete removes the user, and then the user is not found.
	wantStatus(t, call(t, s, "DELETE", base+"/"+pending.ID, key, nil), 204)
	wantError(t, call(t, s, "GET", base+"/"+pending.ID, key, nil), 404, apierr.NotFound)
	wantError(t, login(t, s, "ann@example.com", testPassword, ""), 401, apierr.Unauthenticated)
}

// signInAfterVerify completes a verification link with the test password and returns the session.
func signInAfterVerify(t *testing.T, s *Server, token string) testSession {
	t.Helper()
	return sessionFrom(t, s, call(t, s, "POST", "/api/v1/auth/verify", "",
		map[string]any{"token": token, "password": testPassword}))
}

func TestResendVerificationVoidsOldLink(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	base := "/api/v1/accounts/" + acc.ID + "/users"
	created := decode[userResponse](t, call(t, s, "POST", base, key,
		map[string]any{"email": "ann@example.com", "verification": "link"}))
	old := created.Verification.Token

	r := call(t, s, "POST", base+"/"+created.ID+"/verification", key, map[string]any{"method": "email"})
	wantStatus(t, r, 200)
	if got := decode[userResponse](t, r); got.Verification == nil || !*got.Verification.Sent {
		t.Fatalf("resend = %+v", got)
	}
	msgs := rec.Messages()
	if len(msgs) != 1 {
		t.Fatalf("mails = %d", len(msgs))
	}
	// The earlier link no longer works; the mailed one does.
	wantError(t, call(t, s, "POST", "/api/v1/auth/verify", "", map[string]any{"token": old,
		"password": testPassword}), 400, apierr.InvalidRequest)
	wantStatus(t, call(t, s, "POST", "/api/v1/auth/verify", "", map[string]any{"token": tokenFromMail(t, msgs[0]),
		"password": testPassword}), 200)

	// An active user gets 409 for resend, and the reset link works for them.
	wantError(t, call(t, s, "POST", base+"/"+created.ID+"/verification", key, map[string]any{"method": "link"}),
		409, apierr.Conflict)
	reset := decode[verificationJSON](t, call(t, s, "POST", base+"/"+created.ID+"/password-reset", key,
		map[string]any{"method": "link"}))
	if reset.Method != "link" || reset.Token == "" {
		t.Fatalf("reset = %+v", reset)
	}
	wantStatus(t, call(t, s, "POST", "/api/v1/auth/password/reset", "", map[string]any{"token": reset.Token,
		"password": "a new passphrase"}), 200)
}

func TestNoneVerifiesPendingUser(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	base := "/api/v1/accounts/" + acc.ID + "/users"
	created := decode[userResponse](t, call(t, s, "POST", base, key,
		map[string]any{"email": "ann@example.com", "verification": "link"}))
	old := created.Verification.Token

	r := call(t, s, "POST", base+"/"+created.ID+"/verification", key, map[string]any{"method": "none"})
	wantStatus(t, r, 200)
	if got := decode[userResponse](t, r); got.Status != store.UserActive || got.VerifiedAt == nil {
		t.Fatalf("none = %+v", got)
	}
	// The pending link is voided.
	wantError(t, call(t, s, "POST", "/api/v1/auth/verify", "", map[string]any{"token": old,
		"password": testPassword}), 400, apierr.InvalidRequest)
	// An owner session may not verify outright.
	owner, sess := verifiedUser(t, s, key, acc.ID, "owner@example.com")
	wantStatus(t, call(t, s, "PATCH", base+"/"+owner.ID, key, map[string]any{"role": "owner"}), 200)
	pendingID := decode[userResponse](t, call(t, s, "POST", base, key,
		map[string]any{"email": "pending@example.com", "verification": "link"})).ID
	wantError(t, callSession(t, s, sess, "POST", base+"/"+pendingID+"/verification",
		map[string]any{"method": "none"}), 403, apierr.Forbidden)
}

func TestListUsersFilters(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	base := "/api/v1/accounts/" + acc.ID + "/users"
	ann := decode[userResponse](t, call(t, s, "POST", base, key,
		map[string]any{"email": "Ann@example.com", "external_id": "ext-a", "verification": "link"}))
	// Listing is oldest first; two users made in the same millisecond would tie, so space them.
	time.Sleep(2 * time.Millisecond)
	call(t, s, "POST", base, key, map[string]any{"email": "bob@example.com", "verification": "link"})

	list := decode[listResponse[userJSON]](t, call(t, s, "GET", base, key, nil))
	if len(list.Items) != 2 || list.Items[0].ID != ann.ID {
		t.Fatalf("list = %+v", list)
	}
	byExt := decode[listResponse[userJSON]](t, call(t, s, "GET", base+"?external_id=ext-a", key, nil))
	if len(byExt.Items) != 1 || byExt.Items[0].ID != ann.ID {
		t.Errorf("by external_id = %+v", byExt.Items)
	}
	byEmail := decode[listResponse[userJSON]](t, call(t, s, "GET", base+"?email=ann@EXAMPLE.com", key, nil))
	if len(byEmail.Items) != 1 || byEmail.Items[0].ID != ann.ID {
		t.Errorf("by email = %+v", byEmail.Items)
	}
	page := decode[listResponse[userJSON]](t, call(t, s, "GET", base+"?limit=1", key, nil))
	if len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("first page = %+v", page)
	}
	next := decode[listResponse[userJSON]](t, call(t, s, "GET", base+"?limit=1&cursor="+page.NextCursor, key, nil))
	if len(next.Items) != 1 || next.Items[0].ID == ann.ID {
		t.Errorf("second page = %+v", next)
	}
	wantError(t, call(t, s, "GET", base+"?cursor=bogus", key, nil), 400, apierr.InvalidRequest)
}
