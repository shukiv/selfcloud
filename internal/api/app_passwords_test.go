// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
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

// apwItem is the shape of one app password in a list or a create answer (without the secret). A revoked app
// password is deleted, so the list never holds an ended one and there is no revocation time to carry.
type apwItem struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Access     string     `json:"access"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

// apwCreated is a create answer: the item under "app_password" plus the secret, shown once beside it.
type apwCreated struct {
	apwItem
	Secret string `json:"secret"`
}

func (c *apwCreated) UnmarshalJSON(b []byte) error {
	var w struct {
		AppPassword apwItem `json:"app_password"`
		Secret      string  `json:"secret"`
	}
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	c.apwItem = w.AppPassword
	c.Secret = w.Secret
	return nil
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

	// Revoke deletes the row: the list is empty afterwards, and revoking the gone id is not found.
	wantStatus(t, callSession(t, s, sess, "DELETE", apwPath+"/"+created.ID, nil), 204)
	rec = callSession(t, s, sess, "GET", apwPath, nil)
	list = decode[listResponse[apwItem]](t, rec)
	if len(list.Items) != 0 {
		t.Fatalf("after revoke the row is still listed: %+v", list.Items)
	}
	wantStatus(t, callSession(t, s, sess, "DELETE", apwPath+"/"+created.ID, nil), 404)
}

func TestAppPasswordCreateAnswerShape(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, davOn)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")

	rec := callSession(t, s, sess, "POST", apwPath, map[string]any{"name": "laptop", "access": "read"})
	wantStatus(t, rec, 201)
	// The create answer wraps the item under "app_password" and shows the secret once beside it.
	var w struct {
		AppPassword apwItem `json:"app_password"`
		Secret      string  `json:"secret"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &w); err != nil {
		t.Fatal(err)
	}
	if w.AppPassword.ID == "" || w.AppPassword.Name != "laptop" || w.AppPassword.Access != "read" {
		t.Errorf("app_password = %+v, want the created item under the app_password key", w.AppPassword)
	}
	if !strings.HasPrefix(w.Secret, "scp_") || !strings.Contains(w.Secret, w.AppPassword.ID) {
		t.Errorf("secret = %q, want scp_<id>_<random> beside the item", w.Secret)
	}
}

func TestAppPasswordRefusesAdmin(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, davOn)
	admin := adminKey(t, s)
	_, sess := newAdminUser(t, s, admin, "admin@example.com")
	// An installation admin signed in as a user has no account of its own; the routes refuse it with 403, not a 500.
	wantStatus(t, callSession(t, s, sess, "GET", apwPath, nil), 403)
	wantStatus(t, callSession(t, s, sess, "POST", apwPath, map[string]any{"name": "x"}), 403)
	wantStatus(t, callSession(t, s, sess, "DELETE", apwPath+"/apw_00000000000000000000000000", nil), 403)
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
	// Isolate the cap from the write budget so revoking a place and refilling it is not rate-limited.
	s.limits[appPasswordWriteRate] = newLimiter(1000)
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

func TestAppPasswordOnlyCreateOffWhenDavDisabled(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, davOn)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	created := makeAppPassword(t, s, sess, "mount", "read")

	// Turn the mount off. Making a new app password is refused, but a leaked one can always be listed and revoked.
	wantStatus(t, patchSettings(t, s, admin, map[string]any{"dav.enabled": false}), 200)
	e := wantError(t, callSession(t, s, sess, "POST", apwPath, map[string]any{"name": "x"}), 404, apierr.NotFound)
	if !strings.Contains(e.Message, "WebDAV is turned off") {
		t.Errorf("message = %q, want it to say WebDAV is turned off", e.Message)
	}
	list := decode[listResponse[apwItem]](t, callSession(t, s, sess, "GET", apwPath, nil))
	if len(list.Items) != 1 || list.Items[0].ID != created.ID {
		t.Fatalf("list while off = %+v, want the existing item", list.Items)
	}
	wantStatus(t, callSession(t, s, sess, "DELETE", apwPath+"/"+created.ID, nil), 204)
	list = decode[listResponse[apwItem]](t, callSession(t, s, sess, "GET", apwPath, nil))
	if len(list.Items) != 0 {
		t.Fatalf("list after revoke while off = %+v, want empty", list.Items)
	}
}

func TestAppPasswordWriteRateLimit(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, davOn)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")

	// Create twenty times, revoking all but the last: the active count stays low, but each create spends a write token.
	var lastID string
	for i := 0; i < 20; i++ {
		created := makeAppPassword(t, s, sess, "mount", "read")
		lastID = created.ID
		if i < 19 {
			wantStatus(t, callSession(t, s, sess, "DELETE", apwPath+"/"+created.ID, nil), 204)
		}
	}
	// The twenty-first create is rate-limited, not capped.
	wantError(t, callSession(t, s, sess, "POST", apwPath, map[string]any{"name": "x"}),
		429, apierr.RateLimited)
	// Revocation is never rate-limited out: the live last one is deleted even though the write budget is spent.
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

// apwEventFix is an account with one member who holds a working mount app password, on an https server so Basic
// is accepted from any address.
type apwEventFix struct {
	s      *Server
	admin  string
	acc    accountJSON
	key    string
	u      userResponse
	sess   testSession
	secret string
}

func newApwEventFix(t *testing.T) *apwEventFix {
	t.Helper()
	s := davServer(t)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	u, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	secret := makeAppPassword(t, s, sess, "mount", "read").Secret
	return &apwEventFix{s: s, admin: admin, acc: acc, key: key, u: u, sess: sess, secret: secret}
}

func (fx *apwEventFix) mount(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	return davBasic(t, fx.s, fx.u.Email, "PROPFIND", "/dav/", fx.secret, "", map[string]string{"Depth": "0"})
}

// apwHeld reads the member's app passwords straight from the store, so the check holds even when the event ended
// the session that would list them.
func apwHeld(t *testing.T, s *Server, accID, userID string) int {
	t.Helper()
	list, err := s.store.AppPasswords(context.Background(), accID, userID)
	if err != nil {
		t.Fatal(err)
	}
	return len(list)
}

// An app password ends with the password it was made under: a reset, a change, an admin's set, and the revocation of
// the person's tokens or sessions each delete it in the same transaction, so a stolen or replaced password does not
// leave a working mount credential behind.
func TestAppPasswordEndsWithEachPasswordAndSessionEvent(t *testing.T) {
	t.Parallel()

	t.Run("password reset", func(t *testing.T) {
		t.Parallel()
		fx := newApwEventFix(t)
		wantStatus(t, fx.mount(t), 207)
		wantStatus(t, call(t, fx.s, "POST", "/api/v1/auth/password/forgot", "",
			map[string]any{"email": fx.u.Email}), 202)
		fx.s.bg.Wait()
		msgs := fx.s.mailer.(*mail.Recorder).Messages()
		if len(msgs) == 0 {
			t.Fatal("no reset mail")
		}
		wantStatus(t, call(t, fx.s, "POST", "/api/v1/auth/password/reset", "",
			map[string]any{"token": tokenFromMail(t, msgs[len(msgs)-1]), "password": "a new passphrase here"}), 200)
		wantStatus(t, fx.mount(t), 401)
		if n := apwHeld(t, fx.s, fx.acc.ID, fx.u.ID); n != 0 {
			t.Errorf("after reset the member still holds %d app passwords", n)
		}
	})

	t.Run("password change", func(t *testing.T) {
		t.Parallel()
		fx := newApwEventFix(t)
		wantStatus(t, fx.mount(t), 207)
		wantStatus(t, callSession(t, fx.s, fx.sess, "POST", "/api/v1/me/password",
			map[string]any{"current_password": testPassword, "new_password": "a new passphrase here"}), 204)
		wantStatus(t, fx.mount(t), 401)
		if n := apwHeld(t, fx.s, fx.acc.ID, fx.u.ID); n != 0 {
			t.Errorf("after change the member still holds %d app passwords", n)
		}
	})

	t.Run("admin sets the password", func(t *testing.T) {
		t.Parallel()
		fx := newImpFix(t, true, davOn, func(c *config.Config) { c.PublicURL = "https://drive.example.com" })
		secret := makeAppPassword(t, fx.s, fx.memSess, "mount", "read").Secret
		mount := func() *httptest.ResponseRecorder {
			return davBasic(t, fx.s, fx.member.Email, "PROPFIND", "/dav/", secret, "", map[string]string{"Depth": "0"})
		}
		wantStatus(t, mount(), 207)
		wantStatus(t, callSession(t, fx.s, fx.adminSess, "POST",
			"/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.member.ID+"/password",
			map[string]any{"password": "a new passphrase here", "admin_password": testPassword}), 204)
		wantStatus(t, mount(), 401)
		if n := apwHeld(t, fx.s, fx.acc.ID, fx.member.ID); n != 0 {
			t.Errorf("after an admin set the member still holds %d app passwords", n)
		}
	})

	t.Run("tokens revoked", func(t *testing.T) {
		t.Parallel()
		fx := newApwEventFix(t)
		wantStatus(t, fx.mount(t), 207)
		wantStatus(t, call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.u.ID+"/tokens/revoke", fx.key, nil), 200)
		wantStatus(t, fx.mount(t), 401)
		if n := apwHeld(t, fx.s, fx.acc.ID, fx.u.ID); n != 0 {
			t.Errorf("after token revoke the member still holds %d app passwords", n)
		}
	})

	t.Run("sessions ended", func(t *testing.T) {
		t.Parallel()
		fx := newApwEventFix(t)
		wantStatus(t, fx.mount(t), 207)
		wantStatus(t, call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.u.ID+"/sessions/end", fx.key, nil), 200)
		wantStatus(t, fx.mount(t), 401)
		if n := apwHeld(t, fx.s, fx.acc.ID, fx.u.ID); n != 0 {
			t.Errorf("after sessions end the member still holds %d app passwords", n)
		}
	})
}
