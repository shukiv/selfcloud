// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
)

// davOn turns the WebDAV feature on for a test server.
func davOn(c *config.Config) { c.Dav.Enabled = true }

// apwItem is the shape of one app password in a list or a create answer (without the secret).
type apwItem struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Access     string     `json:"access"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

// apwCreated is a create answer: the item plus the secret, shown once.
type apwCreated struct {
	apwItem
	Secret string `json:"secret"`
}

const apwPath = "/api/v1/me/app-passwords"

// makeAppPassword creates an app password for the session and returns the create answer.
func makeAppPassword(t *testing.T, s *Server, sess testSession, name, access string) apwCreated {
	t.Helper()
	body := map[string]any{"name": name}
	if access != "" {
		body["access"] = access
	}
	rec := callSession(t, s, sess, "POST", apwPath, body)
	wantStatus(t, rec, 201)
	return decode[apwCreated](t, rec)
}

func TestAppPasswordCreateListRevoke(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, davOn)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")

	created := makeAppPassword(t, s, sess, "laptop", "read")
	if !strings.HasPrefix(created.ID, "apw_") {
		t.Errorf("ID = %q, want an apw_ id", created.ID)
	}
	if created.Name != "laptop" || created.Access != "read" {
		t.Errorf("created = %+v", created.apwItem)
	}
	if !strings.HasPrefix(created.Secret, "scp_") || !strings.Contains(created.Secret, created.ID) {
		t.Errorf("secret = %q, want scp_<id>_<random>", created.Secret)
	}
	if created.Prefix == "" || !strings.HasPrefix(created.Secret, created.Prefix) || created.Prefix == created.Secret {
		t.Errorf("prefix = %q is not a safe start of the secret", created.Prefix)
	}
	if created.RevokedAt != nil {
		t.Error("a new app password is already revoked")
	}

	// The list shows the item but never the secret.
	rec := callSession(t, s, sess, "GET", apwPath, nil)
	wantStatus(t, rec, 200)
	list := decode[listResponse[apwItem]](t, rec)
	if len(list.Items) != 1 || list.Items[0].ID != created.ID {
		t.Fatalf("list = %+v", list.Items)
	}
	if strings.Contains(rec.Body.String(), created.Secret) {
		t.Error("the list leaks the secret")
	}

	// Revoke, then the item is marked revoked and the secret is gone from any later create.
	wantStatus(t, callSession(t, s, sess, "DELETE", apwPath+"/"+created.ID, nil), 204)
	rec = callSession(t, s, sess, "GET", apwPath, nil)
	list = decode[listResponse[apwItem]](t, rec)
	if len(list.Items) != 1 || list.Items[0].RevokedAt == nil {
		t.Fatalf("after revoke, list = %+v", list.Items)
	}
	// Revoking again keeps the first revocation (idempotent), still 204.
	wantStatus(t, callSession(t, s, sess, "DELETE", apwPath+"/"+created.ID, nil), 204)
}

func TestAppPasswordRoutesAreSessionOnly(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, davOn)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	u, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	created := makeAppPassword(t, s, sess, "mount", "read")

	tok := mintFor(t, s, key, acc.ID, map[string]any{"user": u.ID, "all": true})
	_, devTok := approvedToken(t, s, sess)

	for _, cred := range []string{key, admin, tok, devTok} {
		wantStatus(t, call(t, s, "GET", apwPath, cred, nil), 401)
		wantStatus(t, call(t, s, "POST", apwPath, cred, map[string]any{"name": "x"}), 401)
		wantStatus(t, call(t, s, "DELETE", apwPath+"/"+created.ID, cred, nil), 401)
	}
	// No credential at all is refused too.
	wantStatus(t, call(t, s, "GET", apwPath, "", nil), 401)
}

func TestAppPasswordRefusesImpersonation(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true, davOn)
	imp, _ := fx.signedInAsMember(t)
	e := wantError(t, callSession(t, fx.s, imp, "POST", apwPath, map[string]any{"name": "x"}),
		403, apierr.Forbidden)
	if e.Details["reason"] != "impersonation" {
		t.Errorf("reason = %v, want impersonation", e.Details["reason"])
	}
	wantStatus(t, callSession(t, fx.s, imp, "GET", apwPath, nil), 403)
}

func TestAppPasswordRefusesMustChange(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, davOn)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	u, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	setMustChange(t, s, acc.ID, u.ID, true)
	e := wantError(t, callSession(t, s, sess, "POST", apwPath, map[string]any{"name": "x"}),
		403, apierr.Forbidden)
	if e.Details["reason"] != "password_change_required" {
		t.Errorf("reason = %v, want password_change_required", e.Details["reason"])
	}
}

func TestAppPasswordNameRules(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, davOn)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")

	for _, name := range []string{"", "   ", strings.Repeat("a", 65), "a\nb", "a\x00b"} {
		wantStatus(t, callSession(t, s, sess, "POST", apwPath, map[string]any{"name": name}), 400)
	}
	// A 64-character name is allowed.
	created := makeAppPassword(t, s, sess, strings.Repeat("a", 64), "read")
	if created.Name != strings.Repeat("a", 64) {
		t.Errorf("name = %q", created.Name)
	}
}

func TestAppPasswordAccessValues(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, davOn)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")

	if got := makeAppPassword(t, s, sess, "r", "read"); got.Access != "read" {
		t.Errorf("access = %q, want read", got.Access)
	}
	if got := makeAppPassword(t, s, sess, "w", "write"); got.Access != "write" {
		t.Errorf("access = %q, want write", got.Access)
	}
	// Omitted access defaults to read.
	if got := makeAppPassword(t, s, sess, "d", ""); got.Access != "read" {
		t.Errorf("default access = %q, want read", got.Access)
	}
	wantStatus(t, callSession(t, s, sess, "POST", apwPath, map[string]any{"name": "x", "access": "admin"}), 400)
}

func TestAppPasswordCap(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, davOn)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")

	for i := 0; i < 20; i++ {
		makeAppPassword(t, s, sess, "mount", "read")
	}
	e := wantError(t, callSession(t, s, sess, "POST", apwPath, map[string]any{"name": "one too many"}),
		409, apierr.Conflict)
	if e.Details["reason"] != "limit" {
		t.Errorf("reason = %v, want limit", e.Details["reason"])
	}
	// Revoking one frees a place.
	list := decode[listResponse[apwItem]](t, callSession(t, s, sess, "GET", apwPath, nil))
	wantStatus(t, callSession(t, s, sess, "DELETE", apwPath+"/"+list.Items[0].ID, nil), 204)
	makeAppPassword(t, s, sess, "fits now", "read")
}

func TestAppPasswordOtherUsersItemIsNotFound(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, davOn)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, annSess := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	_, bobSess := verifiedUser(t, s, key, acc.ID, "bob@example.com")

	created := makeAppPassword(t, s, annSess, "ann's mount", "read")
	// Bob cannot see or revoke Ann's app password.
	list := decode[listResponse[apwItem]](t, callSession(t, s, bobSess, "GET", apwPath, nil))
	if len(list.Items) != 0 {
		t.Fatalf("Bob sees Ann's app passwords: %+v", list.Items)
	}
	wantStatus(t, callSession(t, s, bobSess, "DELETE", apwPath+"/"+created.ID, nil), 404)
	// A made-up id is not found too.
	wantStatus(t, callSession(t, s, annSess, "DELETE", apwPath+"/apw_00000000000000000000000000", nil), 404)
}

func TestAppPasswordRoutesOffWhenDavDisabled(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}) // dav.enabled stays false
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")

	for _, rec := range []*httptest.ResponseRecorder{
		callSession(t, s, sess, "GET", apwPath, nil),
		callSession(t, s, sess, "POST", apwPath, map[string]any{"name": "x"}),
		callSession(t, s, sess, "DELETE", apwPath+"/apw_00000000000000000000000000", nil),
	} {
		e := wantError(t, rec, 404, apierr.NotFound)
		if !strings.Contains(e.Message, "WebDAV is turned off") {
			t.Errorf("message = %q, want it to say WebDAV is turned off", e.Message)
		}
	}
}

func TestAppPasswordWriteRateLimit(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, davOn)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")

	// Create and revoke twenty times: the active count stays low, but each create spends a write token.
	var lastID string
	for i := 0; i < 20; i++ {
		created := makeAppPassword(t, s, sess, "mount", "read")
		lastID = created.ID
		wantStatus(t, callSession(t, s, sess, "DELETE", apwPath+"/"+created.ID, nil), 204)
	}
	// The twenty-first create is rate-limited, not capped.
	wantError(t, callSession(t, s, sess, "POST", apwPath, map[string]any{"name": "x"}),
		429, apierr.RateLimited)
	// Revocation is never rate-limited out.
	wantStatus(t, callSession(t, s, sess, "DELETE", apwPath+"/"+lastID, nil), 204)
}

func TestAppPasswordAuditIsPrivate(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, davOn)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, memSess := verifiedUser(t, s, key, acc.ID, "member@example.com")
	owner, ownerSess := verifiedUser(t, s, key, acc.ID, "owner@example.com")
	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+acc.ID+"/users/"+owner.ID, key,
		map[string]any{"role": "owner"}), 200)

	created := makeAppPassword(t, s, memSess, "private mount", "read")
	auditPath := "/api/v1/accounts/" + acc.ID + "/audit"

	// The owner's session does not see the member's private entry.
	ownerLog := decode[listResponse[auditJSON]](t, callSession(t, s, ownerSess, "GET", auditPath, nil))
	for _, e := range ownerLog.Items {
		if e.Action == "app_password.create" {
			t.Error("the owner sees the member's private app_password.create entry")
		}
	}
	// The account key sees it, and the details never hold the secret.
	keyLog := decode[listResponse[auditJSON]](t, call(t, s, "GET", auditPath, key, nil))
	var found bool
	for _, e := range keyLog.Items {
		if e.Action == "app_password.create" && e.Target == created.ID {
			found = true
			for k, v := range e.Details {
				if sv, ok := v.(string); ok && strings.Contains(sv, created.Secret) {
					t.Errorf("audit detail %q leaks the secret", k)
				}
			}
		}
	}
	if !found {
		t.Error("the account key does not see the app_password.create entry")
	}
}
