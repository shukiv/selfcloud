// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/webhook"
)

// webhookFixture is an account with its key, and a verified owner of it.
type webhookFixture struct {
	s     *Server
	acc   accountJSON
	key   string
	owner testSession
	buf   *bytes.Buffer // the server's log
}

func newWebhookFixture(t *testing.T, mutate ...func(*config.Config)) *webhookFixture {
	t.Helper()
	s := newAuthServer(t, &mail.Recorder{}, mutate...)
	buf := &bytes.Buffer{}
	s.log = slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, owner := ownerUser(t, s, key, acc.ID, "owner@example.com")
	return &webhookFixture{s: s, acc: acc, key: key, owner: owner, buf: buf}
}

func (fx *webhookFixture) base() string { return "/api/v1/accounts/" + fx.acc.ID + "/webhooks" }

// create makes an endpoint through the API and returns its ID and its secret.
func (fx *webhookFixture) create(t *testing.T, body map[string]any) (string, string) {
	t.Helper()
	if body == nil {
		body = map[string]any{"url": "https://hooks.example.com/in", "events": []string{"file.created"}}
	}
	rec := call(t, fx.s, "POST", fx.base(), fx.key, body)
	wantStatus(t, rec, 201)
	out := decode[struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}](t, rec)
	return out.ID, out.Secret
}

func (fx *webhookFixture) endpoint(t *testing.T, hook string) *store.WebhookEndpoint {
	t.Helper()
	e, err := fx.s.store.WebhookEndpoint(context.Background(), fx.acc.ID, hook)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// TestWebhookKeyManagesEndpoints: the account key creates, reads, changes and deletes endpoints. The
// create answers 201 with a secret of the documented shape and a Location.
func TestWebhookKeyManagesEndpoints(t *testing.T) {
	t.Parallel()
	fx := newWebhookFixture(t)
	rec := call(t, fx.s, "POST", fx.base(), fx.key, map[string]any{
		"url": "https://hooks.example.com/in", "events": []string{"file.created", "user.created"},
		"description": "nightly sync"})
	wantStatus(t, rec, 201)
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, fx.base()+"/whk_") {
		t.Fatalf("Location = %q", loc)
	}
	created := decode[map[string]any](t, rec)
	secret, _ := created["secret"].(string)
	if !strings.HasPrefix(secret, "whsec_") || len(secret) != len("whsec_")+43 {
		t.Fatalf("secret %q has the wrong shape", secret)
	}
	hook, _ := created["id"].(string)
	if !strings.HasPrefix(hook, "whk_") {
		t.Fatalf("id %q", hook)
	}

	wantStatus(t, call(t, fx.s, "GET", fx.base()+"/"+hook, fx.key, nil), 200)
	list := decode[listResponse[map[string]any]](t, call(t, fx.s, "GET", fx.base(), fx.key, nil))
	if len(list.Items) != 1 || list.Items[0]["id"] != hook {
		t.Fatalf("list = %v", list.Items)
	}
	rec = call(t, fx.s, "PATCH", fx.base()+"/"+hook, fx.key, map[string]any{"state": "paused", "description": ""})
	wantStatus(t, rec, 200)
	if got := decode[map[string]any](t, rec)["state"]; got != "paused" {
		t.Fatalf("state after PATCH = %v", got)
	}
	wantStatus(t, call(t, fx.s, "DELETE", fx.base()+"/"+hook, fx.key, nil), 204)
	wantError(t, call(t, fx.s, "GET", fx.base()+"/"+hook, fx.key, nil), 404, apierr.NotFound)
}

// TestWebhookSecretNeverListedOrRead: the secret is in the create and rotate answers only. Reads by the
// key and by the owner never carry it.
func TestWebhookSecretNeverListedOrRead(t *testing.T) {
	t.Parallel()
	fx := newWebhookFixture(t)
	hook, secret := fx.create(t, nil)
	bodies := []string{
		call(t, fx.s, "GET", fx.base(), fx.key, nil).Body.String(),
		call(t, fx.s, "GET", fx.base()+"/"+hook, fx.key, nil).Body.String(),
		callSession(t, fx.s, fx.owner, "GET", fx.base(), nil).Body.String(),
		callSession(t, fx.s, fx.owner, "GET", fx.base()+"/"+hook, nil).Body.String(),
	}
	raw := base64.RawURLEncoding.EncodeToString(secretBytesOf(t, secret))
	for i, b := range bodies {
		if strings.Contains(b, secret) || strings.Contains(b, raw) || strings.Contains(b, `"secret"`) {
			t.Fatalf("read %d carries the secret: %s", i, b)
		}
	}
}

// secretBytesOf decodes the shown secret back to its 32 bytes, as the webhook worker will.
func secretBytesOf(t *testing.T, shown string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(shown, "whsec_"))
	if err != nil || len(b) != webhook.SecretLen {
		t.Fatalf("secret %q does not decode to %d bytes: %v", shown, webhook.SecretLen, err)
	}
	return b
}

// TestWebhookOwnerReadsButNeverChanges: an owner lists and reads without the secret, and every change is
// refused as forbidden.
func TestWebhookOwnerReadsButNeverChanges(t *testing.T) {
	t.Parallel()
	fx := newWebhookFixture(t)
	hook, _ := fx.create(t, nil)
	wantStatus(t, callSession(t, fx.s, fx.owner, "GET", fx.base(), nil), 200)
	wantStatus(t, callSession(t, fx.s, fx.owner, "GET", fx.base()+"/"+hook, nil), 200)

	wantError(t, callSession(t, fx.s, fx.owner, "POST", fx.base(), map[string]any{
		"url": "https://hooks.example.com/x", "events": []string{"file.created"}}), 403, apierr.Forbidden)
	wantError(t, callSession(t, fx.s, fx.owner, "PATCH", fx.base()+"/"+hook, map[string]any{"state": "paused"}), 403, apierr.Forbidden)
	wantError(t, callSession(t, fx.s, fx.owner, "DELETE", fx.base()+"/"+hook, nil), 403, apierr.Forbidden)
	wantError(t, callSession(t, fx.s, fx.owner, "POST", fx.base()+"/"+hook+"/secret", nil), 403, apierr.Forbidden)
	if e := fx.endpoint(t, hook); e.State != store.WebhookActive {
		t.Fatalf("the owner changed the endpoint: state %s", e.State)
	}
}

// TestWebhookAnotherAccountRefused: an endpoint of account A is not found by account B's key or owner, and
// B's key cannot create under A. A's endpoint is unchanged.
func TestWebhookAnotherAccountRefused(t *testing.T) {
	t.Parallel()
	fx := newWebhookFixture(t)
	hook, _ := fx.create(t, nil)
	admin := adminKey(t, fx.s)
	other, otherKey := newAccount(t, fx.s, admin, "Other")
	_, otherOwner := ownerUser(t, fx.s, otherKey, other.ID, "boss@example.com")
	wantError(t, call(t, fx.s, "GET", fx.base(), otherKey, nil), 404, apierr.NotFound)
	wantError(t, call(t, fx.s, "GET", fx.base()+"/"+hook, otherKey, nil), 404, apierr.NotFound)
	wantError(t, call(t, fx.s, "PATCH", fx.base()+"/"+hook, otherKey, map[string]any{"state": "paused"}), 404, apierr.NotFound)
	wantError(t, call(t, fx.s, "DELETE", fx.base()+"/"+hook, otherKey, nil), 404, apierr.NotFound)
	wantError(t, call(t, fx.s, "POST", fx.base()+"/"+hook+"/secret", otherKey, nil), 404, apierr.NotFound)
	wantError(t, callSession(t, fx.s, otherOwner, "GET", fx.base()+"/"+hook, nil), 404, apierr.NotFound)
	wantError(t, callSession(t, fx.s, otherOwner, "GET", fx.base(), nil), 404, apierr.NotFound)
	wantError(t, call(t, fx.s, "POST", fx.base(), otherKey, map[string]any{
		"url": "https://hooks.example.com/x", "events": []string{"file.created"}}), 404, apierr.NotFound)
	if e := fx.endpoint(t, hook); e.State != store.WebhookActive || e.URL != "https://hooks.example.com/in" {
		t.Fatalf("A's endpoint changed: %+v", e)
	}
}

// TestWebhookAdminAndTokenRefused: an installation admin reads nothing and changes nothing, and a
// delegated token reaches no webhook route.
func TestWebhookAdminAndTokenRefused(t *testing.T) {
	t.Parallel()
	fx := newWebhookFixture(t)
	hook, _ := fx.create(t, nil)
	admin := adminKey(t, fx.s)
	wantError(t, call(t, fx.s, "GET", fx.base(), admin, nil), 403, apierr.Forbidden)
	wantError(t, call(t, fx.s, "POST", fx.base(), admin, map[string]any{
		"url": "https://hooks.example.com/x", "events": []string{"file.created"}}), 403, apierr.Forbidden)

	f := newFolder(t, fx.s, fx.key, fx.acc.ID, "Mine")
	tok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{f.ID}})
	for _, rt := range []struct{ method, path string }{
		{"GET", fx.base()}, {"GET", fx.base() + "/" + hook}, {"POST", fx.base()}, {"PATCH", fx.base() + "/" + hook},
		{"DELETE", fx.base() + "/" + hook}, {"POST", fx.base() + "/" + hook + "/secret"},
	} {
		rec := call(t, fx.s, rt.method, rt.path, tok, map[string]any{"state": "paused", "url": "https://hooks.example.com/x", "events": []string{"file.created"}})
		if rec.Code != 403 && rec.Code != 404 {
			t.Errorf("token reached %s %s: %d %s", rt.method, rt.path, rec.Code, rec.Body)
		}
	}
	if e := fx.endpoint(t, hook); e.State != store.WebhookActive {
		t.Fatalf("a token changed the endpoint: %s", e.State)
	}
}

// TestWebhookNeedsACredential: with no credential every webhook route answers 401.
func TestWebhookNeedsACredential(t *testing.T) {
	t.Parallel()
	fx := newWebhookFixture(t)
	hook, _ := fx.create(t, nil)
	for _, rt := range []struct{ method, path string }{
		{"GET", fx.base()}, {"POST", fx.base()}, {"GET", fx.base() + "/" + hook},
		{"PATCH", fx.base() + "/" + hook}, {"DELETE", fx.base() + "/" + hook}, {"POST", fx.base() + "/" + hook + "/secret"},
	} {
		wantError(t, call(t, fx.s, rt.method, rt.path, "", nil), 401, apierr.Unauthenticated)
	}
}

// TestWebhookURLRules: the URL must be HTTPS by default, carry no user name, name a host, and stay under
// 2048 characters. A refusal never echoes the URL, so a token in its query stays out of the answer.
func TestWebhookURLRules(t *testing.T) {
	t.Parallel()
	fx := newWebhookFixture(t)
	const secretQuery = "TOPSECRET123"
	refused := map[string]string{
		"http":         "http://hooks.example.com/in",
		"ftp":          "ftp://hooks.example.com/in",
		"file":         "file:///etc/passwd",
		"javascript":   "javascript:alert(1)",
		"userinfo":     "https://user:pass@hooks.example.com/in",
		"no host":      "https:///in",
		"opaque":       "https:hooks.example.com",
		"space":        "https://hooks.example.com/a b",
		"control":      "https://hooks.example.com/a\x01b",
		"non ascii":    "https://hooks.example.com/ünï",
		"fragment":     "https://hooks.example.com/in#x",
		"bad port":     "https://hooks.example.com:70000/in",
		"zero port":    "https://hooks.example.com:0/in",
		"bad host":     "https://bad_host.example.com/in",
		"empty label":  "https://hooks..example.com/in",
		"too long":     "https://hooks.example.com/" + strings.Repeat("a", 2048),
		"empty":        "",
		"query secret": "ftp://hooks.example.com/in?token=" + secretQuery,
		// Numeric host forms that some resolvers read as an address are not DNS names (decision 28).
		"short ipv4":   "https://127.1/in",
		"single label": "https://2130706433/in",
		"hex octet":    "https://0x7f.1/in",
		"private ipv4": "https://10.0.0.5/in",
		"link local":   "https://169.254.169.254/latest",
		"loopback v6":  "https://[::1]/in",
		"doc ipv4":     "https://203.0.113.9/in",
		"doc ipv6":     "https://[2001:db8::1]/in",
	}
	for name, u := range refused {
		rec := call(t, fx.s, "POST", fx.base(), fx.key, map[string]any{"url": u, "events": []string{"file.created"}})
		e := wantError(t, rec, 400, apierr.InvalidRequest)
		if strings.Contains(e.Message, secretQuery) || strings.Contains(rec.Body.String(), secretQuery) {
			t.Errorf("%s: the refusal echoes the URL: %s", name, rec.Body)
		}
	}
	for _, u := range map[string]string{
		"https":           "https://hooks.example.com/in",
		"https with port": "https://hooks.example.com:8443/in",
		"ipv4 literal":    "https://93.184.216.34/in",
		"ipv6 literal":    "https://[2606:2800:220:1:248:1893:25c8:1946]/in",
		"max length":      "https://hooks.example.com/" + strings.Repeat("a", 2048-len("https://hooks.example.com/")),
	} {
		wantStatus(t, call(t, fx.s, "POST", fx.base(), fx.key, map[string]any{"url": u, "events": []string{"file.created"}}), 201)
	}
}

// TestWebhookAllowHTTP: the operator's switch admits plain HTTP. HTTPS keeps working either way.
func TestWebhookAllowHTTP(t *testing.T) {
	t.Parallel()
	fx := newWebhookFixture(t, func(c *config.Config) { c.Webhooks.AllowHTTP = true })
	wantStatus(t, call(t, fx.s, "POST", fx.base(), fx.key, map[string]any{
		"url": "http://hooks.example.com/in", "events": []string{"file.created"}}), 201)
	wantStatus(t, call(t, fx.s, "POST", fx.base(), fx.key, map[string]any{
		"url": "ftp://hooks.example.com/in", "events": []string{"file.created"}}), 400)
}

// TestWebhookEventsAreFixed: an event outside the list is refused, an empty list is refused, and a repeat
// is kept once.
func TestWebhookEventsAreFixed(t *testing.T) {
	t.Parallel()
	fx := newWebhookFixture(t)
	for _, evs := range [][]string{{"folder.created"}, {"file.created", "user.deleted"}, {}, nil} {
		wantError(t, call(t, fx.s, "POST", fx.base(), fx.key, map[string]any{
			"url": "https://hooks.example.com/in", "events": evs}), 400, apierr.InvalidRequest)
	}
	hook, _ := fx.create(t, map[string]any{"url": "https://hooks.example.com/in",
		"events": []string{"webhook.test", "file.created", "file.created"}})
	e := fx.endpoint(t, hook)
	if strings.Join(e.Events, ",") != "file.created,webhook.test" {
		t.Fatalf("events = %v", e.Events)
	}
	wantError(t, call(t, fx.s, "PATCH", fx.base()+"/"+hook, fx.key, map[string]any{"events": []string{"quota.warn"}}), 400, apierr.InvalidRequest)
}

// TestWebhookBodyRules: unknown fields are refused, a disabled state cannot be asked for, and the
// description has a length limit.
func TestWebhookBodyRules(t *testing.T) {
	t.Parallel()
	fx := newWebhookFixture(t)
	hook, _ := fx.create(t, nil)
	wantError(t, call(t, fx.s, "PATCH", fx.base()+"/"+hook, fx.key, map[string]any{"state": "disabled"}), 400, apierr.InvalidRequest)
	wantError(t, call(t, fx.s, "PATCH", fx.base()+"/"+hook, fx.key, map[string]any{"state": "bogus"}), 400, apierr.InvalidRequest)
	wantError(t, call(t, fx.s, "PATCH", fx.base()+"/"+hook, fx.key, map[string]any{"secret": "x"}), 400, apierr.InvalidRequest)
	wantError(t, call(t, fx.s, "POST", fx.base(), fx.key, map[string]any{
		"url": "https://hooks.example.com/in", "events": []string{"file.created"}, "secret": "x"}), 400, apierr.InvalidRequest)
	wantStatus(t, call(t, fx.s, "PATCH", fx.base()+"/"+hook, fx.key,
		map[string]any{"description": strings.Repeat("d", 200)}), 200)
	wantError(t, call(t, fx.s, "PATCH", fx.base()+"/"+hook, fx.key,
		map[string]any{"description": strings.Repeat("d", 201)}), 400, apierr.InvalidRequest)
	wantError(t, call(t, fx.s, "PATCH", fx.base()+"/"+hook, fx.key,
		map[string]any{"description": "a\x00b"}), 400, apierr.InvalidRequest)
}

// TestWebhookRotationReplacesSecret: a rotation answers the new secret once. The old secret stays usable
// only during its 24 hour grace period; after it, only the new secret is active. A second rotation ends
// the first old secret at once, since only one grace copy is kept.
func TestWebhookRotationReplacesSecret(t *testing.T) {
	t.Parallel()
	fx := newWebhookFixture(t)
	hook, first := fx.create(t, nil)
	rec := call(t, fx.s, "POST", fx.base()+"/"+hook+"/secret", fx.key, nil)
	wantStatus(t, rec, 200)
	second := decode[map[string]any](t, rec)["secret"].(string)
	if second == first || !strings.HasPrefix(second, "whsec_") {
		t.Fatalf("rotation gave %q, first was %q", second, first)
	}
	e := fx.endpoint(t, hook)
	now := time.Now()

	active, err := fx.s.webhookSecrets(e, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 || !bytes.Equal(active[0], secretBytesOf(t, second)) || !bytes.Equal(active[1], secretBytesOf(t, first)) {
		t.Fatalf("during grace: %d active secrets, want the new one then the old one", len(active))
	}
	later, err := fx.s.webhookSecrets(e, now.Add(25*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(later) != 1 || !bytes.Equal(later[0], secretBytesOf(t, second)) {
		t.Fatalf("after 24 hours: %d active secrets, want only the new one", len(later))
	}

	third := decode[map[string]any](t, call(t, fx.s, "POST", fx.base()+"/"+hook+"/secret", fx.key, nil))["secret"].(string)
	e = fx.endpoint(t, hook)
	after, err := fx.s.webhookSecrets(e, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2 || !bytes.Equal(after[1], secretBytesOf(t, second)) || bytes.Equal(after[1], secretBytesOf(t, first)) {
		t.Fatalf("second rotation kept the wrong grace copy")
	}
	if !bytes.Equal(after[0], secretBytesOf(t, third)) {
		t.Fatal("the current secret is not the newest")
	}
}

// TestWebhookSecretRowBinding: a secret copied from one endpoint's row into another's cannot be read there.
func TestWebhookSecretRowBinding(t *testing.T) {
	t.Parallel()
	fx := newWebhookFixture(t)
	a, _ := fx.create(t, nil)
	b, _ := fx.create(t, nil)
	ctx := context.Background()
	ea := fx.endpoint(t, a)
	err := fx.s.store.Write(ctx, func(tx *store.Tx) error {
		eb, err := tx.WebhookEndpoint(ctx, fx.acc.ID, b)
		if err != nil {
			return err
		}
		eb.SecretEnc = ea.SecretEnc
		return tx.UpdateWebhookEndpoint(ctx, eb)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.s.webhookSecrets(fx.endpoint(t, b), time.Now()); err == nil {
		t.Fatal("a secret moved from another endpoint's row was read")
	}
	if _, err := fx.s.webhookSecrets(ea, time.Now()); err != nil {
		t.Fatalf("the source endpoint lost its secret: %v", err)
	}
}

// TestWebhookWrongKeyCannotRead: the same blob under another signing key does not open.
func TestWebhookWrongKeyCannotRead(t *testing.T) {
	t.Parallel()
	fx := newWebhookFixture(t)
	hook, _ := fx.create(t, nil)
	other, err := webhook.NewSealer(bytes.Repeat([]byte{9}, webhook.KeyLen), nil)
	if err != nil {
		t.Fatal(err)
	}
	e := fx.endpoint(t, hook)
	if _, err := other.Open(e.SecretEnc, webhook.Binding{AccountID: e.AccountID, EndpointID: e.ID, Field: webhook.FieldSecret}); err == nil {
		t.Fatal("a wrong key opened the secret")
	}
}

// TestWebhookRotationReopensOnlyAsPaused: a disabled endpoint cannot be switched on by a state change
// alone. A new URL in the same request, or a rotation followed by a switch on, does it. Changing the URL
// clears the reason and resets the failure count.
func TestWebhookRotationReopensOnlyAsPaused(t *testing.T) {
	t.Parallel()
	fx := newWebhookFixture(t)
	hook, _ := fx.create(t, nil)
	disable := func() {
		t.Helper()
		ctx := context.Background()
		if err := fx.s.store.Write(ctx, func(tx *store.Tx) error {
			e, err := tx.WebhookEndpoint(ctx, fx.acc.ID, hook)
			if err != nil {
				return err
			}
			e.State, e.DisabledReason, e.ConsecutiveFailures = store.WebhookDisabled, "failing", 7
			return tx.UpdateWebhookEndpoint(ctx, e)
		}); err != nil {
			t.Fatal(err)
		}
	}
	disable()
	wantError(t, call(t, fx.s, "PATCH", fx.base()+"/"+hook, fx.key, map[string]any{"state": "active"}), 409, apierr.Conflict)

	rec := call(t, fx.s, "POST", fx.base()+"/"+hook+"/secret", fx.key, nil)
	wantStatus(t, rec, 200)
	e := fx.endpoint(t, hook)
	if e.State != store.WebhookPaused || e.DisabledReason != "" {
		t.Fatalf("after rotation: state %s, reason %q", e.State, e.DisabledReason)
	}
	wantStatus(t, call(t, fx.s, "PATCH", fx.base()+"/"+hook, fx.key, map[string]any{"state": "active"}), 200)

	disable()
	rec = call(t, fx.s, "PATCH", fx.base()+"/"+hook, fx.key, map[string]any{"url": "https://hooks.example.com/new", "state": "active"})
	wantStatus(t, rec, 200)
	e = fx.endpoint(t, hook)
	if e.State != store.WebhookActive || e.DisabledReason != "" || e.ConsecutiveFailures != 0 || e.URL != "https://hooks.example.com/new" {
		t.Fatalf("after a new URL: %+v", e)
	}
}

// TestWebhookSecretAbsentFromAuditAndLogs: no audit entry or log line carries the secret, and a URL's
// query stays out of both.
func TestWebhookSecretAbsentFromAuditAndLogs(t *testing.T) {
	t.Parallel()
	fx := newWebhookFixture(t)
	const tokenInQuery = "QUERYTOKEN987"
	hook, first := fx.create(t, map[string]any{"url": "https://hooks.example.com/in?token=" + tokenInQuery,
		"events": []string{"file.created"}})
	rec := call(t, fx.s, "POST", fx.base()+"/"+hook+"/secret", fx.key, nil)
	wantStatus(t, rec, 200)
	second := decode[map[string]any](t, rec)["secret"].(string)
	wantStatus(t, call(t, fx.s, "PATCH", fx.base()+"/"+hook, fx.key, map[string]any{"description": "x"}), 200)
	wantStatus(t, call(t, fx.s, "DELETE", fx.base()+"/"+hook, fx.key, nil), 204)

	audit := call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/audit", fx.key, nil)
	wantStatus(t, audit, 200)
	if !strings.Contains(audit.Body.String(), "webhook.create") {
		t.Fatalf("no webhook audit entries: %s", audit.Body)
	}
	for _, s := range []string{first, second, base64.RawURLEncoding.EncodeToString(secretBytesOf(t, first)),
		base64.RawURLEncoding.EncodeToString(secretBytesOf(t, second)), tokenInQuery} {
		if strings.Contains(audit.Body.String(), s) {
			t.Errorf("the audit log carries %q", s)
		}
		if strings.Contains(fx.buf.String(), s) {
			t.Errorf("the server log carries %q", s)
		}
	}
	if fx.buf.Len() == 0 {
		t.Fatal("the log capture recorded nothing: the check proves nothing")
	}
}

// TestWebhookCreateAuditNamesOnlyTheOrigin: a create writes an audit entry with the URL origin only.
func TestWebhookCreateAuditNamesOnlyTheOrigin(t *testing.T) {
	t.Parallel()
	fx := newWebhookFixture(t)
	fx.create(t, map[string]any{"url": "https://hooks.example.com/in?token=ABC", "events": []string{"file.created"}})
	audit := call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/audit", fx.key, nil)
	var out struct {
		Items []struct {
			Action  string         `json:"action"`
			Details map[string]any `json:"details"`
		} `json:"items"`
	}
	if err := json.Unmarshal(audit.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, it := range out.Items {
		if it.Action == "webhook.create" {
			if u, _ := it.Details["url"].(string); u != "https://hooks.example.com" {
				t.Fatalf("audit url = %q", u)
			}
			return
		}
	}
	t.Fatal("no webhook.create entry")
}
