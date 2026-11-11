// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/sweep"
)

// testSetting and testSettings are the answer of GET and PATCH /api/v1/settings, as a client reads it.
type testSetting struct {
	Key     string `json:"key"`
	Type    string `json:"type"`
	Value   any    `json:"value"`
	Default any    `json:"default"`
	Source  string `json:"source"`
	Locked  bool   `json:"locked"`
	Restart bool   `json:"restart"`
}

type testFact struct {
	Key    string `json:"key"`
	Value  any    `json:"value"`
	Source string `json:"source"`
}

type testSettings struct {
	Settings []testSetting `json:"settings"`
	Facts    []testFact    `json:"facts"`
}

func (ts testSettings) get(t *testing.T, key string) testSetting {
	t.Helper()
	for _, s := range ts.Settings {
		if s.Key == key {
			return s
		}
	}
	t.Fatalf("no setting %q in %+v", key, ts.Settings)
	return testSetting{}
}

// configFrom loads a configuration from yaml and env, the way the server loads its own, with the data folder in dir.
func configFrom(t *testing.T, dir, yaml string, env map[string]string) *config.Config {
	t.Helper()
	path := filepath.Join(dir, "selfcloud.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	all := map[string]string{
		"SELFCLOUD_DATA_DIR":                        dir,
		"SELFCLOUD_RATE_LIMIT_PER_IP_PER_MINUTE":    "1000",
		"SELFCLOUD_RATE_LIMIT_PER_EMAIL_PER_MINUTE": "1000",
	}
	maps.Copy(all, env)
	cfg, err := config.Load(path, func(k string) (string, bool) { v, ok := all[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// serverOn starts a server on cfg's database. seed copies a migrated database there first; a second server on the same
// database passes false.
func serverOn(t *testing.T, cfg *config.Config, seed bool) *Server {
	t.Helper()
	if seed {
		seedMigratedDB(t, cfg.Store.Path)
	}
	st, err := store.Open(context.Background(), store.Options{Path: cfg.Store.Path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s, err := New(Options{Config: cfg, Store: st, Mailer: &mail.Recorder{}})
	if err != nil {
		t.Fatal(err)
	}
	s.limitClock = frozenLimitClock
	s.webhookClient = testWebhookClient(cfg, nil)
	// The disk here may hold less than storage.min_free; the tests that read the free space set their own.
	s.freeBytes = func(string) (uint64, error) { return 1 << 40, nil }
	return s
}

// newSettingsServer is a test server whose configuration comes from a file and an environment, so the keys they set are
// locked.
func newSettingsServer(t *testing.T, yaml string, env map[string]string) *Server {
	t.Helper()
	return serverOn(t, configFrom(t, t.TempDir(), yaml, env), true)
}

// getSettings reads the settings with an admin credential.
func getSettings(t *testing.T, s *Server, admin string) testSettings {
	t.Helper()
	rec := call(t, s, "GET", "/api/v1/settings", admin, nil)
	wantStatus(t, rec, 200)
	return decode[testSettings](t, rec)
}

// patchSettings changes the settings with an admin credential.
func patchSettings(t *testing.T, s *Server, admin string, set map[string]any, reset ...string) *httptest.ResponseRecorder {
	t.Helper()
	body := map[string]any{}
	if set != nil {
		body["set"] = set
	}
	if reset != nil {
		body["reset"] = reset
	}
	return call(t, s, "PATCH", "/api/v1/settings", admin, body)
}

// settingsAudit returns the installation.settings entries, newest first.
func settingsAudit(t *testing.T, s *Server) []*store.AuditEntry {
	t.Helper()
	rows, err := s.store.AdminAuditEntries(context.Background(), store.AdminAuditQuery{ActionPrefix: "installation.settings", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestSettingsShowSourceLockAndDefault(t *testing.T) {
	t.Parallel()
	s := newSettingsServer(t, "upload:\n  max_file_size: 2GiB\nmail:\n  smtp_host: mail.example.com\n"+
		"  from: drive@example.com\n  smtp_password_file: /run/secrets/smtp\n", map[string]string{"SELFCLOUD_TRASH_RETENTION": "48h"})
	got := getSettings(t, s, adminKey(t, s))
	if v := got.get(t, "upload.max_file_size"); v.Value != "2GiB" || v.Default != "100GiB" || v.Source != "file" ||
		!v.Locked || v.Restart || v.Type != "bytes" {
		t.Errorf("upload.max_file_size = %+v", v)
	}
	if v := got.get(t, "trash.retention"); v.Value != "48h0m0s" || v.Source != "env" || !v.Locked || v.Type != "duration" {
		t.Errorf("trash.retention = %+v", v)
	}
	if v := got.get(t, "sessions.idle_timeout"); v.Value != "168h0m0s" || v.Source != "default" || v.Locked {
		t.Errorf("sessions.idle_timeout = %+v", v)
	}
	if v := got.get(t, "search.enabled"); v.Value != true || v.Type != "bool" || v.Locked {
		t.Errorf("search.enabled = %+v", v)
	}
	if v := got.get(t, "office.lock_minutes"); v.Value != float64(30) || v.Type != "int" {
		t.Errorf("office.lock_minutes = %+v", v)
	}
	if v := got.get(t, "office.enabled"); !v.Locked || !v.Restart || v.Value != false {
		t.Errorf("office.enabled = %+v, want locked and restart", v)
	}
	facts := map[string]testFact{}
	for _, f := range got.Facts {
		facts[f.Key] = f
		if s, ok := f.Value.(string); ok && strings.Contains(s, "/run/secrets") {
			t.Errorf("fact %s shows a secret's path", f.Key)
		}
	}
	if f := facts["mail.smtp_host"]; f.Value != "mail.example.com" || f.Source != "file" {
		t.Errorf("mail.smtp_host = %+v", f)
	}
	if f := facts["data_dir"]; f.Source != "env" {
		t.Errorf("data_dir = %+v, want env", f)
	}
}

// TestSettingsPatchAppliesAndIsAudited: a change answers the new settings, and one audit row names each changed key with
// its old and new values and its source.
func TestSettingsPatchAppliesAndIsAudited(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	rec := patchSettings(t, s, admin, map[string]any{"trash.retention": "48h", "search.enabled": false, "office.lock_minutes": 10})
	wantStatus(t, rec, 200)
	got := decode[testSettings](t, rec)
	if v := got.get(t, "trash.retention"); v.Value != "48h0m0s" || v.Source != "settings" || v.Locked {
		t.Errorf("trash.retention = %+v", v)
	}
	if v := getSettings(t, s, admin).get(t, "search.enabled"); v.Value != false || v.Source != "settings" {
		t.Errorf("search.enabled = %+v", v)
	}
	rows := settingsAudit(t, s)
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(rows))
	}
	e := rows[0]
	if e.AccountID != "" || e.ActorKind != "admin_key" || e.Outcome != "ok" {
		t.Errorf("audit row = %+v", e)
	}
	want := map[string]map[string]any{
		"trash.retention":     {"old": "720h0m0s", "new": "48h0m0s", "source": "settings"},
		"search.enabled":      {"old": "true", "new": "false", "source": "settings"},
		"office.lock_minutes": {"old": "30", "new": "10", "source": "settings"},
	}
	if len(e.Details) != len(want) {
		t.Errorf("audit details = %v, want the three keys", e.Details)
	}
	for key, w := range want {
		d, _ := e.Details[key].(map[string]any)
		if !maps.Equal(d, w) {
			t.Errorf("audit %s = %v, want %v", key, d, w)
		}
	}
	// A change to the value a key already has writes no row.
	wantStatus(t, patchSettings(t, s, admin, map[string]any{"trash.retention": "48h"}), 200)
	if n := len(settingsAudit(t, s)); n != 1 {
		t.Errorf("audit rows after a change to the same value = %d, want 1", n)
	}
}

// TestSettingsPatchIsAllOrNothing: one invalid value refuses the whole change, the answer names every invalid key and
// only those, and nothing is stored or audited.
func TestSettingsPatchIsAllOrNothing(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	e := wantError(t, patchSettings(t, s, admin, map[string]any{"trash.retention": "48h", "sessions.idle_timeout": "1m",
		"upload.max_chunk_size": "NaN"}), 400, apierr.InvalidRequest)
	keys, _ := e.Details["keys"].([]any)
	if !slices.Equal(keys, []any{"sessions.idle_timeout", "upload.max_chunk_size"}) {
		t.Errorf("details.keys = %v, want the two invalid keys", e.Details["keys"])
	}
	if !strings.Contains(e.Message, "config: ") || !strings.Contains(e.Message, "sessions.idle_timeout") ||
		!strings.Contains(e.Message, "upload.max_chunk_size") {
		t.Errorf("message = %q, want the config errors naming each key", e.Message)
	}
	if v := getSettings(t, s, admin).get(t, "trash.retention"); v.Value != "720h0m0s" || v.Source != "default" {
		t.Errorf("trash.retention after a refused change = %+v", v)
	}
	if n := len(settingsAudit(t, s)); n != 0 {
		t.Errorf("a refused change wrote %d audit rows", n)
	}
	// A rule across keys is checked on the merged settings: a piece size above the largest file is refused.
	e = wantError(t, patchSettings(t, s, admin, map[string]any{"upload.max_file_size": "8MiB"}), 400, apierr.InvalidRequest)
	if keys, _ := e.Details["keys"].([]any); !slices.Contains(keys, any("upload.max_file_size")) {
		t.Errorf("a piece size above the largest file: keys %v", e.Details["keys"])
	}
}

// TestSettingsPatchRefusesWrongJSONTypes: a string is not a bool, a fraction or an overflow is not an integer.
func TestSettingsPatchRefusesWrongJSONTypes(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	for _, set := range []map[string]any{
		{"search.enabled": "true"},
		{"office.lock_minutes": 1.5},
		{"office.lock_minutes": "5"},
		{"versions.max_count": 1e30},
		{"trash.retention": 48},
		{"trash.retention": nil},
	} {
		wantError(t, patchSettings(t, s, admin, set), 400, apierr.InvalidRequest)
	}
	// Nothing to change, and a key both set and reset, are refused too.
	wantError(t, patchSettings(t, s, admin, map[string]any{}), 400, apierr.InvalidRequest)
	wantError(t, patchSettings(t, s, admin, map[string]any{"trash.retention": "48h"}, "trash.retention"), 400, apierr.InvalidRequest)
	wantError(t, call(t, s, "PATCH", "/api/v1/settings", admin, map[string]any{"set": map[string]any{}, "extra": 1}), 400,
		apierr.InvalidRequest)
}

// TestSettingsPatchBoundsItsShape: a change names at most 64 keys, each at most 128 bytes and named once, and a value
// longer than any setting needs is refused without its text in the answer.
func TestSettingsPatchBoundsItsShape(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	many := map[string]any{}
	for i := range 65 {
		many["k"+strings.Repeat("x", i)] = true
	}
	for _, tc := range []struct {
		name, msg string
		set       map[string]any
		reset     []string
	}{
		{"65 keys", "at most 64", many, nil},
		{"a long key", "longer than 128", map[string]any{strings.Repeat("k", 129): true}, nil},
		{"a key reset twice", "named twice", nil, []string{"trash.retention", "trash.retention"}},
	} {
		e := wantError(t, patchSettings(t, s, admin, tc.set, tc.reset...), 400, apierr.InvalidRequest)
		if !strings.Contains(e.Message, tc.msg) || e.Details["reason"] != nil {
			t.Errorf("%s: %q %v", tc.name, e.Message, e.Details)
		}
	}
	long := strings.Repeat("0s", 40) + "48h" // 48 hours, within the bounds
	e := wantError(t, patchSettings(t, s, admin, map[string]any{"trash.retention": long}), 400, apierr.InvalidRequest)
	if keys, _ := e.Details["keys"].([]any); e.Details["reason"] != "invalid_setting" || !slices.Equal(keys, []any{"trash.retention"}) {
		t.Errorf("a long value: details = %v", e.Details)
	}
	if strings.Contains(e.Message, long) {
		t.Errorf("the refusal echoes the value: %q", e.Message)
	}
	if v := getSettings(t, s, admin).get(t, "trash.retention"); v.Source != "default" {
		t.Errorf("trash.retention = %+v after a refused change", v)
	}
}

func TestSettingsPatchRefusesLockedKeys(t *testing.T) {
	t.Parallel()
	s := newSettingsServer(t, "upload:\n  max_file_size: 2GiB\n", map[string]string{"SELFCLOUD_TRASH_RETENTION": "48h"})
	admin := adminKey(t, s)
	for _, tc := range []struct {
		set   map[string]any
		reset []string
		key   string
	}{
		{map[string]any{"upload.max_file_size": "4GiB"}, nil, "upload.max_file_size"},
		{map[string]any{"trash.retention": "72h"}, nil, "trash.retention"},
		{nil, []string{"trash.retention"}, "trash.retention"},
		{map[string]any{"office.enabled": true}, nil, "office.enabled"},
	} {
		e := wantError(t, patchSettings(t, s, admin, tc.set, tc.reset...), 409, apierr.Conflict)
		if keys, _ := e.Details["keys"].([]any); e.Details["reason"] != "setting_locked" || !slices.Equal(keys, []any{tc.key}) {
			t.Errorf("%s: details = %v", tc.key, e.Details)
		}
	}
	// A locked key in a change refuses the whole change.
	wantError(t, patchSettings(t, s, admin, map[string]any{"versions.max_age": "48h", "upload.max_file_size": "4GiB"}), 409,
		apierr.Conflict)
	if v := getSettings(t, s, admin).get(t, "versions.max_age"); v.Source != "default" {
		t.Errorf("versions.max_age = %+v after a refused change", v)
	}
}

func TestSettingsPatchRefusesUnknownKeys(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	for _, tc := range []struct {
		set   map[string]any
		reset []string
	}{
		{map[string]any{"upload.max_size": "1GiB"}, nil},
		{map[string]any{"listen": ":9090"}, nil},
		{map[string]any{"mail.smtp_password_file": "/tmp/x"}, nil},
		{nil, []string{"public_url"}},
	} {
		e := wantError(t, patchSettings(t, s, admin, tc.set, tc.reset...), 400, apierr.InvalidRequest)
		if e.Details["reason"] != "unknown_setting" {
			t.Errorf("%v %v: details = %v", tc.set, tc.reset, e.Details)
		}
	}
}

func TestSettingsResetFallsBackToTheDefault(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	wantStatus(t, patchSettings(t, s, admin, map[string]any{"trash.retention": "48h"}), 200)
	got := decode[testSettings](t, wantStatusRec(t, patchSettings(t, s, admin, nil, "trash.retention"), 200))
	if v := got.get(t, "trash.retention"); v.Value != "720h0m0s" || v.Source != "default" {
		t.Errorf("trash.retention after reset = %+v", v)
	}
	rows := settingsAudit(t, s)
	if len(rows) != 2 {
		t.Fatalf("audit rows = %d, want 2", len(rows))
	}
	d, _ := rows[0].Details["trash.retention"].(map[string]any)
	if !maps.Equal(d, map[string]any{"old": "48h0m0s", "new": "720h0m0s", "source": "default"}) {
		t.Errorf("reset audit = %v", d)
	}
	// A reset of a key that holds its default changes nothing and writes nothing.
	wantStatus(t, patchSettings(t, s, admin, nil, "trash.retention"), 200)
	if n := len(settingsAudit(t, s)); n != 2 {
		t.Errorf("audit rows after a reset of a default = %d, want 2", n)
	}
}

// wantStatusRec checks the status and returns the recorder.
func wantStatusRec(t *testing.T, rec *httptest.ResponseRecorder, status int) *httptest.ResponseRecorder {
	t.Helper()
	wantStatus(t, rec, status)
	return rec
}

// TestSettingsWhoMayChangeThem: an installation admin by key or by its own session, with the CSRF token; nobody else,
// and not an admin signed in as a tenant user.
func TestSettingsWhoMayChangeThem(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	s := fx.s
	change := map[string]any{"set": map[string]any{"trash.retention": "48h"}}

	wantStatus(t, call(t, s, "GET", "/api/v1/settings", fx.admin, nil), 200)
	wantStatus(t, callSession(t, s, fx.adminSess, "GET", "/api/v1/settings", nil), 200)
	wantStatus(t, callSession(t, s, fx.adminSess, "PATCH", "/api/v1/settings", change), 200)
	rows := settingsAudit(t, s)
	if len(rows) != 1 || rows[0].ActorKind != "user" || rows[0].ActorID != fx.adminUser.ID {
		t.Errorf("the admin session's change is audited as %+v", rows)
	}

	// The admin's session without its CSRF token is refused.
	req := jsonRequest(t, "PATCH", "/api/v1/settings", map[string]any{"set": map[string]any{"trash.retention": "72h"}})
	req.AddCookie(fx.adminSess.cookie)
	req.Header.Set("Origin", s.origin)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	wantError(t, rec, 403, apierr.Forbidden)

	// An account key, a tenant user and no credential are refused.
	wantError(t, call(t, s, "GET", "/api/v1/settings", fx.accKey, nil), 403, apierr.Forbidden)
	wantError(t, call(t, s, "PATCH", "/api/v1/settings", fx.accKey, change), 403, apierr.Forbidden)
	wantError(t, callSession(t, s, fx.memSess, "GET", "/api/v1/settings", nil), 403, apierr.Forbidden)
	wantError(t, callSession(t, s, fx.memSess, "PATCH", "/api/v1/settings", change), 403, apierr.Forbidden)
	wantError(t, call(t, s, "GET", "/api/v1/settings", "", nil), 401, apierr.Unauthenticated)

	// An admin signed in as the member is refused, as an impersonation.
	imp, _ := fx.signedInAsMember(t)
	for _, method := range []string{"GET", "PATCH"} {
		e := wantError(t, callSession(t, s, imp, method, "/api/v1/settings", change), 403, apierr.Forbidden)
		if e.Details["reason"] != reasonImpersonation {
			t.Errorf("%s as a sign-in-as: reason %v", method, e.Details["reason"])
		}
	}
	if v := getSettings(t, s, fx.admin).get(t, "trash.retention"); v.Value != "48h0m0s" {
		t.Errorf("trash.retention = %+v, want only the admin's change", v)
	}
}

// TestSettingsLiveMaxFileSize: the next upload after a change is held to the new largest file, without a restart.
func TestSettingsLiveMaxFileSize(t *testing.T) {
	t.Parallel()
	s, key, _, f := uploadSetup(t)
	admin := adminKey(t, s)
	wantStatus(t, createUpload(t, s, key, 9<<20, md("folder", f.ID, "name", "before.bin")), 201)
	wantStatus(t, patchSettings(t, s, admin, map[string]any{"upload.max_file_size": "8MiB", "upload.max_chunk_size": "4MiB",
		"upload.max_small_file_size": "1MiB"}), 200)
	e := wantError(t, createUpload(t, s, key, 9<<20, md("folder", f.ID, "name", "after.bin")), 413, apierr.TooLarge)
	if e.Details["limit_bytes"] != float64(8<<20) {
		t.Errorf("limit_bytes = %v, want 8 MiB", e.Details["limit_bytes"])
	}
	opt := upCall(t, s, "OPTIONS", "/api/v1/uploads", "", nil, nil, 0)
	if opt.Header().Get("Tus-Max-Size") != "8388608" {
		t.Errorf("Tus-Max-Size = %q", opt.Header().Get("Tus-Max-Size"))
	}
}

// TestSettingsLiveSessionLifetimes: a lower idle timeout or longest session ends an existing session on its next request.
func TestSettingsLiveSessionLifetimes(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	verifiedUser(t, s, key, acc.ID, "bob@example.com")
	t0 := time.Now()
	clock := t0
	s.sessionClock = func() time.Time { return clock }
	ann := signInKeeping(t, s, "ann@example.com", true)
	bob := signInKeeping(t, s, "bob@example.com", true)

	// Sixteen minutes unused is well inside the default week.
	clock = t0.Add(16 * time.Minute)
	wantStatus(t, callSession(t, s, bob, "GET", "/api/v1/me", nil), 200)
	wantStatus(t, patchSettings(t, s, admin, map[string]any{"sessions.idle_timeout": "15m"}), 200)
	// Ann has not been seen for sixteen minutes: her session is over. Bob was seen at once.
	wantError(t, callSession(t, s, ann, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)
	wantStatus(t, callSession(t, s, bob, "GET", "/api/v1/me", nil), 200)

	// Bob's session began over an hour before the longest session goes down to an hour. The idle timeout goes back to its
	// default first, so only the longest session can end it.
	wantStatus(t, patchSettings(t, s, admin, nil, "sessions.idle_timeout"), 200)
	clock = t0.Add(70 * time.Minute)
	wantStatus(t, callSession(t, s, bob, "GET", "/api/v1/me", nil), 200)
	wantStatus(t, patchSettings(t, s, admin, map[string]any{"sessions.max_age": "1h"}), 200)
	wantError(t, callSession(t, s, bob, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)
}

// TestSettingsSignInAsKeepsItsOwnHour: a lower idle timeout does not end a sign-in-as, which keeps the hour it was given.
func TestSettingsSignInAsKeepsItsOwnHour(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	t0 := time.Now()
	clock := t0
	fx.s.sessionClock = func() time.Time { return clock }
	imp, _ := fx.signedInAsMember(t)
	clock = t0.Add(16 * time.Minute)
	wantStatus(t, patchSettings(t, fx.s, fx.admin, map[string]any{"sessions.idle_timeout": "15m"}), 200)
	wantStatus(t, callSession(t, fx.s, imp, "GET", "/api/v1/me", nil), 200)
}

// TestSettingsTenantAccessOffTakesEffectAtOnce: turning admin.tenant_access off refuses the next sign-in-as and ends the
// running one at its next request.
func TestSettingsTenantAccessOffTakesEffectAtOnce(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	imp, _ := fx.signedInAsMember(t)
	wantStatus(t, callSession(t, fx.s, imp, "GET", "/api/v1/me", nil), 200)
	wantStatus(t, patchSettings(t, fx.s, fx.admin, map[string]any{"admin.tenant_access": false}), 200)
	e := wantError(t, fx.signInAs(t, fx.member.ID), 403, apierr.Forbidden)
	if e.Details["reason"] != reasonTenantAccessOff {
		t.Errorf("a new sign-in-as: reason %v", e.Details["reason"])
	}
	e = wantError(t, callSession(t, fx.s, imp, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)
	if e.Details["reason"] != reasonTenantAccessOff {
		t.Errorf("the running sign-in-as: reason %v", e.Details["reason"])
	}
	me := decode[map[string]any](t, callSession(t, fx.s, fx.adminSess, "GET", "/api/v1/me", nil))
	if me["tenant_access"] != false {
		t.Errorf("the admin's /me says tenant_access %v", me["tenant_access"])
	}
}

// TestSettingsInfoShowsTheNewPieceSize: /api/v1/info reports the piece size and largest file the server now uses.
func TestSettingsInfoShowsTheNewPieceSize(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	wantStatus(t, patchSettings(t, s, adminKey(t, s), map[string]any{"upload.max_chunk_size": "8MiB",
		"upload.max_file_size": "1GiB", "sharing.max_expiry": "48h"}), 200)
	info := decode[infoResponse](t, do(t, s.Handler(), "GET", "/api/v1/info"))
	if info.Limits.MaxChunkSize != 8<<20 || info.Limits.MaxFileSize != 1<<30 || info.SharingMaxExpiryDays != 2 {
		t.Errorf("info = %+v, %d days", info.Limits, info.SharingMaxExpiryDays)
	}
}

// TestSettingsMinFreeIsLive: readiness follows a new storage.min_free at once.
func TestSettingsMinFreeIsLive(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	s.freeBytes = func(string) (uint64, error) { return 10 << 30, nil }
	wantStatus(t, do(t, s.Handler(), "GET", "/readyz"), 200)
	wantStatus(t, patchSettings(t, s, adminKey(t, s), map[string]any{"storage.min_free": "20GiB"}), 200)
	wantStatus(t, do(t, s.Handler(), "GET", "/readyz"), 503)
}

// TestSettingsTableIsReadAtStart: a server started on a database with stored settings uses them, and shows their source.
func TestSettingsTableIsReadAtStart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first := serverOn(t, configFrom(t, dir, "", nil), true)
	firstAdmin := adminKey(t, first)
	wantStatus(t, patchSettings(t, first, firstAdmin, map[string]any{"trash.retention": "48h",
		"upload.max_chunk_size": "8MiB", "office.lock_minutes": 10}), 200)
	// A reset removes the row, so the next start does not bring the value back.
	wantStatus(t, patchSettings(t, first, firstAdmin, nil, "office.lock_minutes"), 200)

	// The next start writes trash.retention in the file: the file wins, and the stored piece size still applies.
	second := serverOn(t, configFrom(t, dir, "trash:\n  retention: 96h\n", nil), false)
	got := getSettings(t, second, adminKey(t, second))
	if v := got.get(t, "upload.max_chunk_size"); v.Value != "8MiB" || v.Source != "settings" {
		t.Errorf("upload.max_chunk_size at start = %+v", v)
	}
	if v := got.get(t, "trash.retention"); v.Value != "96h0m0s" || v.Source != "file" || !v.Locked {
		t.Errorf("trash.retention at start = %+v", v)
	}
	if v := got.get(t, "office.lock_minutes"); v.Source != "default" {
		t.Errorf("office.lock_minutes after its reset = %+v", v)
	}
	info := decode[infoResponse](t, do(t, second.Handler(), "GET", "/api/v1/info"))
	if info.Limits.MaxChunkSize != 8<<20 {
		t.Errorf("info piece size at start = %d", info.Limits.MaxChunkSize)
	}
}

// TestSettingsOfficeTextIsLive: the content worker reads search.office_text on each file. With office off, a document
// is skipped for the converter, not for the setting, once the setting is on.
func TestSettingsOfficeTextIsLive(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	before := fx.file(t, "before.docx", docxBytes("x"))
	drainText(t, fx.s.textWorker)
	if r := rowOf(t, fx.s, before.ID); r.Error != store.TextErrOfficeTextOff {
		t.Fatalf("row before = %+v", r)
	}
	wantStatus(t, patchSettings(t, fx.s, adminKey(t, fx.s), map[string]any{"search.office_text": true}), 200)
	after := fx.file(t, "after.docx", docxBytes("y"))
	drainText(t, fx.s.textWorker)
	if r := rowOf(t, fx.s, after.ID); r.State != "skipped" || r.Error != store.TextErrConverterOff {
		t.Errorf("row after = %+v, want skipped for the converter", r)
	}
}

// TestSettingsOfficeTextOnReleasesSkippedDocuments: with office on, turning search.office_text on queues the documents
// skipped while it was off, as a start with it on does.
func TestSettingsOfficeTextOnReleasesSkippedDocuments(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	doc := putTyped(t, fx.s, fx.key, fx.fld.ID, "memo.docx",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document", docxBytes("memo"))
	drainText(t, fx.s.textWorker)
	if r := rowOf(t, fx.s, doc.ID); r.State != "skipped" || r.Error != store.TextErrOfficeTextOff {
		t.Fatalf("row before = %+v", r)
	}
	wantStatus(t, patchSettings(t, fx.s, fx.admin, map[string]any{"search.office_text": true}), 200)
	if r := rowOf(t, fx.s, doc.ID); r.State != "pending" {
		t.Errorf("row after = %+v, want pending", r)
	}
}

// TestSettingsRetentionReachesTheNextSweep: the housekeeping reads trash.retention and versions.max_count at each pass, so
// a change reaches the next pass of the sweeper the server already runs.
func TestSettingsRetentionReachesTheNextSweep(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	admin := adminKey(t, fx.s)
	ctx := context.Background()
	gone := putNode(t, fx.s, fx.key, fx.fld.ID, "/gone.txt", "gone")
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+gone.ID, fx.key, nil), 204)
	f := putNode(t, fx.s, fx.key, fx.fld.ID, "/kept.txt", "aa")
	etag := f.ETag
	for _, body := range []string{"bb", "cc", "dd"} {
		etag = replaceContent(t, fx, f.ID, etag, body).ETag
	}

	sw := sweep.New(fx.s.sweepOptions())
	later := store.Now().Add(36 * time.Hour)
	res, err := sw.Once(ctx, later)
	if err != nil || res.Purged != 0 || res.Versions != 0 {
		t.Fatalf("a pass on the defaults = %+v, %v; want nothing purged or pruned", res, err)
	}
	wantStatus(t, patchSettings(t, fx.s, admin, map[string]any{"trash.retention": "24h", "versions.max_count": 1}), 200)
	if res, err = sw.Once(ctx, later); err != nil {
		t.Fatal(err)
	}
	if res.Purged != 1 || res.Versions != 2 {
		t.Errorf("the next pass = %+v; want the trashed file purged and two versions pruned", res)
	}
	if got := len(versionsOf(t, fx, f.ID)); got != 2 {
		t.Errorf("versions after the pass = %d, want the current and one", got)
	}
}

// snapshotFields are the Config fields the settings API changes while the server runs. Every handler reads them through
// the snapshot, so a read of s.cfg would keep the value the server started with.
var snapshotFields = []string{
	"Upload.MaxFileSize", "Upload.MaxChunkSize", "Upload.MaxSmallFileSize", "Storage.MinFree", "Sharing.MaxExpiry",
	"Links.DefaultTTL", "Links.MaxTTL", "Trash.Retention", "Versions.MaxCount", "Versions.MaxAge",
	"Sessions.IdleTimeout", "Sessions.MaxAge", "Sessions.AdminIdleTimeout", "Sessions.AdminMaxAge", "Sessions.ShortIdle",
	"Users.VerificationTTL", "Users.ResetTTL", "Search.Enabled", "Search.OfficeText", "Office.LockMinutes",
	"Office.MaxFileBytes", "Admin.TenantAccess",
}

// TestEditableSettingsAreReadThroughTheSnapshot: no file of the server reads a setting the API changes from the
// configuration it started with, field by field or as a whole section.
func TestEditableSettingsAreReadThroughTheSnapshot(t *testing.T) {
	t.Parallel()
	var pats []*regexp.Regexp
	for _, f := range snapshotFields {
		pats = append(pats, regexp.MustCompile(`\b(cfg|Config)\.`+regexp.QuoteMeta(f)+`\b`))
	}
	// A whole section passed on carries its editable fields with it. links is passed whole for its key files only.
	pats = append(pats, regexp.MustCompile(`\b(cfg|Config)\.(Upload|Storage|Sharing|Trash|Versions|Sessions|Users|Search|Office|Admin)\s*([,)]|$)`))
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			for _, p := range pats {
				if p.MatchString(line) {
					t.Errorf("%s:%d reads a live setting from the start-up configuration: %s", name, i+1, strings.TrimSpace(line))
				}
			}
		}
	}
}

// TestSettingsPatchWithSessionNeedsSameOrigin: a cross-site page cannot use the admin's cookie for a change.
func TestSettingsPatchWithSessionNeedsSameOrigin(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	_, sess := newAdminUser(t, s, adminKey(t, s), "ann@example.com")
	req := jsonRequest(t, "PATCH", "/api/v1/settings", map[string]any{"set": map[string]any{"trash.retention": "48h"}})
	req.AddCookie(sess.cookie)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("X-CSRF-Token", sess.csrf)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
		t.Errorf("a cross-site change answered %d", rec.Code)
	}
	if n := len(settingsAudit(t, s)); n != 0 {
		t.Errorf("a cross-site change wrote %d audit rows", n)
	}
}
