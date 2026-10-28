package api

import (
	"context"
	"testing"

	"selfcloud/internal/apierr"
	"selfcloud/internal/auth"
	"selfcloud/internal/mail"
	"selfcloud/internal/store"
)

// newAdminUser creates an admin through the API with a link, verifies it with testPassword, and
// returns the admin and the session the verification started.
func newAdminUser(t *testing.T, s *Server, admin, email string) (userResponse, testSession) {
	t.Helper()
	rec := call(t, s, "POST", "/api/v1/admins", admin, map[string]any{"email": email, "verification": "link"})
	wantStatus(t, rec, 201)
	created := decode[adminResponse](t, rec)
	if created.Role != store.RoleAdmin || created.Verification == nil || created.Verification.Token == "" {
		t.Fatalf("created admin = %+v", created)
	}
	rec = call(t, s, "POST", "/api/v1/auth/verify", "",
		map[string]any{"token": created.Verification.Token, "password": testPassword})
	wantStatus(t, rec, 200)
	return created, sessionFrom(t, s, rec)
}

func TestAdminUserSignsInAndManages(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	ann, sess := newAdminUser(t, s, admin, "ann@example.com")

	// The admin's session has no account, and says so.
	me := decode[meResponse](t, callSession(t, s, sess, "GET", "/api/v1/me", nil))
	if me.Account != nil || me.User.Role != store.RoleAdmin {
		t.Fatalf("me = %+v", me)
	}
	// It manages admins and tenants like an admin key does.
	wantStatus(t, callSession(t, s, sess, "GET", "/api/v1/admins", nil), 200)
	wantStatus(t, callSession(t, s, sess, "POST", "/api/v1/accounts", map[string]any{"name": "Acme"}), 201)

	// Signing in with the password works, and no account is needed to pick it.
	wantStatus(t, login(t, s, "ann@example.com", testPassword, ""), 200)
	wantStatus(t, login(t, s, "ann@example.com", testPassword, "admin"), 200)

	// An admin session's user stays subject to the status checks: disabled ends it.
	wantStatus(t, call(t, s, "DELETE", "/api/v1/admins/"+ann.ID, admin, nil), 204)
	wantError(t, callSession(t, s, testSession{cookie: sess.cookie}, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)
}

func TestAdminCreationRules(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	newAdminUser(t, s, admin, "ann@example.com")
	sess := sessionFrom(t, s, login(t, s, "ann@example.com", testPassword, ""))

	// An admin session makes a new admin by email only: a link is for a pending owner. Nothing may
	// skip verification.
	wantError(t, callSession(t, s, sess, "POST", "/api/v1/admins",
		map[string]any{"email": "eve@example.com", "verification": "link"}), 403, apierr.Forbidden)
	wantStatus(t, callSession(t, s, sess, "POST", "/api/v1/admins",
		map[string]any{"email": "bob@example.com", "verification": "email"}), 201)
	wantError(t, call(t, s, "POST", "/api/v1/admins", admin,
		map[string]any{"email": "bob@example.com", "verification": "none"}), 400, apierr.InvalidRequest)
	// Email needs mail; with none configured it is refused.
	s2 := newAuthServer(t, nil)
	admin2 := adminKey(t, s2)
	wantError(t, call(t, s2, "POST", "/api/v1/admins", admin2,
		map[string]any{"email": "bob@example.com", "verification": "email"}), 400, apierr.InvalidRequest)

	// Admin emails are unique among admins, case-insensitively.
	wantError(t, call(t, s, "POST", "/api/v1/admins", admin,
		map[string]any{"email": "ANN@example.com", "verification": "link"}), 409, apierr.Conflict)

	// A tenant user with the same email is a different person.
	acc, key := newAccount(t, s, admin, "Acme")
	wantStatus(t, call(t, s, "POST", "/api/v1/accounts/"+acc.ID+"/users", key,
		map[string]any{"email": "ann@example.com", "verification": "link"}), 201)

	// Only admins may create them: a tenant key is refused.
	wantError(t, call(t, s, "POST", "/api/v1/admins", key,
		map[string]any{"email": "eve@example.com", "verification": "link"}), 403, apierr.Forbidden)
	wantError(t, call(t, s, "GET", "/api/v1/admins", key, nil), 403, apierr.Forbidden)
}

func TestDeleteLastAdminNeedsAKey(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	ann, _ := newAdminUser(t, s, admin, "ann@example.com")
	// With the admin key still active, the sole admin user may go.
	wantStatus(t, call(t, s, "DELETE", "/api/v1/admins/"+ann.ID, admin, nil), 204)
	wantError(t, call(t, s, "DELETE", "/api/v1/admins/"+ann.ID, admin, nil), 404, apierr.NotFound)

	// Bob becomes the sole active admin. Then every admin key is revoked, so nothing may remove him.
	_, bobSess := newAdminUser(t, s, admin, "bob@example.com")
	ctx := context.Background()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		k, err := s.store.APIKeyByHash(ctx, auth.HashKey(admin))
		if err != nil {
			return err
		}
		return tx.RevokeAPIKey(ctx, "", k.ID, store.Now())
	})
	if err != nil {
		t.Fatal(err)
	}
	bob := decode[listResponse[userJSON]](t, callSession(t, s, bobSess, "GET", "/api/v1/admins", nil))
	if len(bob.Items) != 1 {
		t.Fatalf("admins = %+v", bob.Items)
	}
	wantError(t, callSession(t, s, bobSess, "DELETE", "/api/v1/admins/"+bob.Items[0].ID, nil), 409, apierr.Conflict)
}

func TestLoginAdminAndTenantChoice(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	newAdminUser(t, s, admin, "shared@example.com")
	verifiedUser(t, s, key, acc.ID, "shared@example.com")

	// Both matched, so the answer lists the admin with its fixed ID and name.
	e := wantError(t, login(t, s, "shared@example.com", testPassword, ""), 409, apierr.Conflict)
	choices, _ := e.Details["accounts"].([]any)
	found := false
	for _, c := range choices {
		m, _ := c.(map[string]any)
		if m["id"] == "admin" && m["name"] == "Installation admin" {
			found = true
		}
	}
	if len(choices) != 2 || !found {
		t.Fatalf("choices = %v", choices)
	}
	// A wrong password gets the same refusal as before, with no list.
	wantError(t, login(t, s, "shared@example.com", "wrong password here", ""), 401, apierr.Unauthenticated)

	// Choosing "admin" signs in the admin user; choosing the account signs in the tenant user.
	adminRec := login(t, s, "shared@example.com", testPassword, "admin")
	wantStatus(t, adminRec, 200)
	if got := decode[signedIn](t, adminRec).User; got.Role != store.RoleAdmin {
		t.Errorf("admin choice signed in %+v", got)
	}
	wantStatus(t, login(t, s, "shared@example.com", testPassword, acc.ID), 200)
}

func TestCreateAccountWithOwner(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	body := func(ext string, verification string) map[string]any {
		return map[string]any{"name": "Acme", "external_id": ext, "owner": map[string]any{
			"email": "owner@example.com", "display_name": "Owen", "verification": verification}}
	}

	// A link comes back with the owner, and the owner can then sign in.
	rec := call(t, s, "POST", "/api/v1/accounts", admin, body("c-1", "link"))
	wantStatus(t, rec, 201)
	created := decode[accountResponse](t, rec)
	if created.Owner == nil || created.Owner.Role != store.RoleOwner || created.Owner.Verification == nil ||
		created.Owner.Verification.Token == "" {
		t.Fatalf("owner = %+v", created.Owner)
	}
	rec = call(t, s, "POST", "/api/v1/auth/verify", "",
		map[string]any{"token": created.Owner.Verification.Token, "password": testPassword})
	wantStatus(t, rec, 200)

	// A retry with the same fields returns the account, not the owner again.
	again := call(t, s, "POST", "/api/v1/accounts", admin, body("c-1", "link"))
	wantStatus(t, again, 200)
	if got := decode[accountResponse](t, again); got.ID != created.ID || got.Owner != nil {
		t.Fatalf("retry = %+v", got)
	}

	// none verifies at once; a session cannot ask for it, and nothing is made on refusal.
	none := decode[accountResponse](t, call(t, s, "POST", "/api/v1/accounts", admin, map[string]any{
		"name": "Beta", "owner": map[string]any{"email": "boss@example.com", "verification": "none"}}))
	if none.Owner == nil || none.Owner.Status != store.UserActive || none.Owner.Verification != nil {
		t.Fatalf("none owner = %+v", none.Owner)
	}
	wantError(t, call(t, s, "POST", "/api/v1/accounts", admin, map[string]any{"name": "Gamma",
		"owner": map[string]any{"email": "x@example.com", "verification": "sms"}}), 400, apierr.InvalidRequest)
	wantError(t, callSession(t, s, testSession{}, "POST", "/api/v1/accounts", map[string]any{"name": "Delta"}), 401,
		apierr.Unauthenticated)
}
