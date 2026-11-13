// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http/httptest"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/mail"
)

func wantMailNotConfigured(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	e := wantError(t, rec, 400, apierr.InvalidRequest)
	if got := e.Details["reason"]; got != "mail_not_configured" {
		t.Fatalf("details.reason = %v, want mail_not_configured: %v", got, e.Details)
	}
}

func TestMailNotConfiguredRefusalNamesItsReason(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, nil)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	users := "/api/v1/accounts/" + acc.ID + "/users"

	// Creating a person.
	wantMailNotConfigured(t, call(t, s, "POST", users, key,
		map[string]any{"email": "ann@example.com", "verification": "email"}))

	// Resending a verification for a pending person.
	rec := call(t, s, "POST", users, key, map[string]any{"email": "bob@example.com", "verification": "link"})
	wantStatus(t, rec, 201)
	pending := decode[userResponse](t, rec)
	wantMailNotConfigured(t, call(t, s, "POST", users+"/"+pending.ID+"/verification", key,
		map[string]any{"method": "email"}))

	// An email reset for an active person.
	active, _ := verifiedUser(t, s, key, acc.ID, "cara@example.com")
	wantMailNotConfigured(t, call(t, s, "POST", users+"/"+active.ID+"/password-reset", key,
		map[string]any{"method": "email"}))

	// Changing a person's address by email.
	wantMailNotConfigured(t, call(t, s, "PATCH", users+"/"+active.ID, key,
		map[string]any{"email": "cara.new@example.com", "verification": "email"}))

	// An installation admin creating an account's owner by email.
	wantMailNotConfigured(t, call(t, s, "POST", "/api/v1/accounts", admin, map[string]any{
		"name":  "Beta",
		"owner": map[string]any{"email": "owner@example.com", "verification": "email"},
	}))

	// An installation admin creating an admin by email.
	wantMailNotConfigured(t, call(t, s, "POST", "/api/v1/admins", admin,
		map[string]any{"email": "inst@example.com", "verification": "email"}))
}

func TestMailConfiguredRoutesAreUnchanged(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	users := "/api/v1/accounts/" + acc.ID + "/users"

	// Creating a person sends the mail and answers 201.
	wantStatus(t, call(t, s, "POST", users, key,
		map[string]any{"email": "ann@example.com", "verification": "email"}), 201)

	// Resending a verification answers 200 and sends the mail.
	created := call(t, s, "POST", users, key, map[string]any{"email": "bob@example.com", "verification": "link"})
	wantStatus(t, created, 201)
	pending := decode[userResponse](t, created)
	wantStatus(t, call(t, s, "POST", users+"/"+pending.ID+"/verification", key,
		map[string]any{"method": "email"}), 200)

	// An email reset answers 200 for an active person.
	active, _ := verifiedUser(t, s, key, acc.ID, "cara@example.com")
	wantStatus(t, call(t, s, "POST", users+"/"+active.ID+"/password-reset", key,
		map[string]any{"method": "email"}), 200)

	// A change of a person's address by email answers 200 and sends the confirmation.
	wantStatus(t, call(t, s, "PATCH", users+"/"+active.ID, key,
		map[string]any{"email": "cara.new@example.com", "verification": "email"}), 200)

	// An installation admin creates an account's owner by email and the mail goes out.
	wantStatus(t, call(t, s, "POST", "/api/v1/accounts", admin, map[string]any{
		"name":  "Beta",
		"owner": map[string]any{"email": "owner@example.com", "verification": "email"},
	}), 201)

	// An installation admin creates another admin by email.
	wantStatus(t, call(t, s, "POST", "/api/v1/admins", admin,
		map[string]any{"email": "inst@example.com", "verification": "email"}), 201)

	if len(rec.Messages()) != 6 {
		t.Fatalf("mails = %d, want 6", len(rec.Messages()))
	}
}
