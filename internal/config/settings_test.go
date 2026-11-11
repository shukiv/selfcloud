// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// loadYAML loads a configuration from body, with env as the environment.
func loadYAML(t *testing.T, body string, env map[string]string) *Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "selfcloud.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// settingOf returns the settings entry named key.
func settingOf(t *testing.T, c *Config, key string) Setting {
	t.Helper()
	for _, s := range c.Settings() {
		if s.Key == key {
			return s
		}
	}
	t.Fatalf("no setting %q", key)
	return Setting{}
}

// changeError returns err as a *ChangeError, failing the test when it is not one.
func changeError(t *testing.T, err error) *ChangeError {
	t.Helper()
	var ce *ChangeError
	if !errors.As(err, &ce) {
		t.Fatalf("error %v (%T) is not a *ChangeError", err, err)
	}
	return ce
}

// problemKeys returns every key the problems of e name, sorted and once each.
func problemKeys(e *ChangeError) []string {
	var keys []string
	for _, p := range e.Problems {
		for _, k := range p.Keys {
			if !slices.Contains(keys, k) {
				keys = append(keys, k)
			}
		}
	}
	slices.Sort(keys)
	return keys
}

func TestFileSetKeyIsLocked(t *testing.T) {
	t.Parallel()
	c := loadYAML(t, "upload:\n  max_file_size: 2GiB\n", nil)
	s := settingOf(t, c, "upload.max_file_size")
	if s.Source != SourceFile || !s.Locked || s.Restart || s.Value != "2GiB" || s.Default != "100GiB" || s.Type != TypeBytes {
		t.Errorf("upload.max_file_size = %+v, want file, locked, 2GiB, default 100GiB", s)
	}
}

func TestEnvSetKeyIsLocked(t *testing.T) {
	t.Parallel()
	c := loadYAML(t, "", map[string]string{"SELFCLOUD_SESSIONS_IDLE_TIMEOUT": "2h"})
	s := settingOf(t, c, "sessions.idle_timeout")
	if s.Source != SourceEnv || !s.Locked || s.Value != "2h0m0s" || s.Type != TypeDuration {
		t.Errorf("sessions.idle_timeout = %+v, want env, locked, 2h0m0s", s)
	}
}

func TestDefaultDoesNotLock(t *testing.T) {
	t.Parallel()
	c := loadYAML(t, "upload:\n  max_file_size: 2GiB\n", nil)
	s := settingOf(t, c, "trash.retention")
	if s.Source != SourceDefault || s.Locked || s.Value != s.Default || s.Value != "720h0m0s" {
		t.Errorf("trash.retention = %+v, want the default, not locked", s)
	}
	// Defaults alone, with no file and no environment, lock nothing but the keys that need a restart.
	for _, s := range Defaults().Settings() {
		if s.Locked != s.Restart || s.Source != SourceDefault {
			t.Errorf("%s from Defaults() = %+v, want unlocked unless it needs a restart", s.Key, s)
		}
	}
}

// TestKeyWrittenAsItsDefaultStillLocks: a key the file writes is locked, even when the file writes the default value.
func TestKeyWrittenAsItsDefaultStillLocks(t *testing.T) {
	t.Parallel()
	c := loadYAML(t, "trash:\n  retention: 720h\n", nil)
	if s := settingOf(t, c, "trash.retention"); s.Source != SourceFile || !s.Locked {
		t.Errorf("trash.retention = %+v, want file and locked", s)
	}
}

func TestEnvWinsOverFileAsTheSource(t *testing.T) {
	t.Parallel()
	c := loadYAML(t, "trash:\n  retention: 48h\n", map[string]string{"SELFCLOUD_TRASH_RETENTION": "72h"})
	if s := settingOf(t, c, "trash.retention"); s.Source != SourceEnv || s.Value != "72h0m0s" {
		t.Errorf("trash.retention = %+v, want env and 72h0m0s", s)
	}
}

// TestAliasAndMergeKeysCountAsWritten: a key reached through a YAML alias or a merge key is written in the file.
func TestAliasAndMergeKeysCountAsWritten(t *testing.T) {
	t.Parallel()
	c := loadYAML(t, "ui:\n  brand:\n    palette:\n      light: &l\n        accent: \"#112233\"\n"+
		"      dark: *l\n    sidebar:\n      <<: {bg: \"#000000\"}\n", nil)
	for _, key := range []string{"ui.brand.palette.light.accent", "ui.brand.palette.dark.accent", "ui.brand.sidebar.bg"} {
		if got := c.Source(key); got != SourceFile {
			t.Errorf("Source(%s) = %q, want file", key, got)
		}
	}
	if got := c.Source("ui.brand.sidebar.text"); got != SourceDefault {
		t.Errorf("Source(ui.brand.sidebar.text) = %q, want default", got)
	}
}

// TestStoredValuesSitBetweenTheFileAndTheDefault: the file or environment wins over the settings table, and the table
// wins over the built-in default.
func TestStoredValuesSitBetweenTheFileAndTheDefault(t *testing.T) {
	t.Parallel()
	c := loadYAML(t, "upload:\n  max_file_size: 2GiB\n", nil)
	var skipped []string
	c.ApplyStored(map[string]string{"upload.max_file_size": "4GiB", "trash.retention": "48h0m0s"},
		func(key, _ string) { skipped = append(skipped, key) })
	if c.Upload.MaxFileSize != 2*GiB || c.Source("upload.max_file_size") != SourceFile {
		t.Errorf("upload.max_file_size = %v from %s, want the file's 2GiB", c.Upload.MaxFileSize, c.Source("upload.max_file_size"))
	}
	if c.Trash.Retention != Duration(48*time.Hour) || c.Source("trash.retention") != SourceSettings {
		t.Errorf("trash.retention = %v from %s, want the table's 48h", c.Trash.Retention, c.Source("trash.retention"))
	}
	if c.Versions.MaxAge != Defaults().Versions.MaxAge || c.Source("versions.max_age") != SourceDefault {
		t.Errorf("versions.max_age = %v from %s, want the default", c.Versions.MaxAge, c.Source("versions.max_age"))
	}
	if !slices.Equal(skipped, []string{"upload.max_file_size"}) {
		t.Errorf("skipped = %v, want the row the file overrides", skipped)
	}
	if s := settingOf(t, c, "trash.retention"); s.Locked || s.Value != "48h0m0s" {
		t.Errorf("trash.retention = %+v, want 48h0m0s and editable", s)
	}
}

// TestStoredRowThatFailsIsSkipped: a row that does not parse, breaks a bound, names an unknown key or a key that needs a
// restart is skipped and named; the rest apply.
func TestStoredRowThatFailsIsSkipped(t *testing.T) {
	t.Parallel()
	c := Defaults()
	skipped := map[string]string{}
	c.ApplyStored(map[string]string{
		"trash.retention":       "1s",
		"upload.max_chunk_size": "lots",
		"nope.key":              "x",
		"office.enabled":        "true",
		"links.max_ttl":         "2h0m0s",
	}, func(key, reason string) { skipped[key] = reason })
	for _, key := range []string{"trash.retention", "upload.max_chunk_size", "nope.key", "office.enabled"} {
		if skipped[key] == "" {
			t.Errorf("row %s was not skipped (skipped %v)", key, skipped)
		}
	}
	if c.Links.MaxTTL != Duration(2*time.Hour) || c.Source("links.max_ttl") != SourceSettings {
		t.Errorf("links.max_ttl = %v from %s, want the table's 2h", c.Links.MaxTTL, c.Source("links.max_ttl"))
	}
	if c.Trash.Retention != Defaults().Trash.Retention || c.Office.Enabled {
		t.Errorf("a skipped row was applied: trash %v, office %v", c.Trash.Retention, c.Office.Enabled)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("the result is not valid: %v", err)
	}
}

// TestStoredRowsValidTogetherApplyTogether: rows that are valid only as a set (a default lifetime above the old longest)
// apply together.
func TestStoredRowsValidTogetherApplyTogether(t *testing.T) {
	t.Parallel()
	c := Defaults()
	c.ApplyStored(map[string]string{"links.default_ttl": "48h0m0s", "links.max_ttl": "72h0m0s"},
		func(key, reason string) { t.Errorf("row %s skipped: %s", key, reason) })
	if c.Links.DefaultTTL != Duration(48*time.Hour) || c.Links.MaxTTL != Duration(72*time.Hour) {
		t.Errorf("links = %+v, want 48h and 72h", c.Links)
	}
}

// TestApplyStoredTwiceIsTheSame: the admin commands and the server both apply the table; a second pass changes nothing,
// and a row gone from the table goes back to its default.
func TestApplyStoredTwiceIsTheSame(t *testing.T) {
	t.Parallel()
	c := Defaults()
	rows := map[string]string{"trash.retention": "48h0m0s", "search.enabled": "false"}
	c.ApplyStored(rows, nil)
	c.ApplyStored(rows, nil)
	if c.Trash.Retention != Duration(48*time.Hour) || c.Search.Enabled {
		t.Fatalf("after two passes: trash %v, search %v", c.Trash.Retention, c.Search.Enabled)
	}
	c.ApplyStored(map[string]string{"trash.retention": "48h0m0s"}, nil)
	if !c.Search.Enabled || c.Source("search.enabled") != SourceDefault {
		t.Errorf("search.enabled = %v from %s, want the default once its row is gone", c.Search.Enabled, c.Source("search.enabled"))
	}
}

// TestApplyStoredMarksTheConfig: a configuration records that the table was applied to it, failing rows and all, and a
// change keeps the mark, so a server given it does not apply the table a second time.
func TestApplyStoredMarksTheConfig(t *testing.T) {
	t.Parallel()
	c := Defaults()
	if c.StoredApplied() {
		t.Fatal("a fresh configuration says the table was applied")
	}
	c.ApplyStored(map[string]string{"nope.key": "1"}, nil)
	if !c.StoredApplied() {
		t.Fatal("after ApplyStored the configuration does not say so")
	}
	next, err := c.Change(map[string]string{"trash.retention": "48h"}, nil)
	if err != nil || !next.StoredApplied() {
		t.Errorf("a change lost the mark: %v", err)
	}
}

// TestChangeIsAllOrNothing: one bad value among good ones refuses the whole change, names the bad key only, and leaves the
// configuration as it was.
func TestChangeIsAllOrNothing(t *testing.T) {
	t.Parallel()
	c := Defaults()
	next, err := c.Change(map[string]string{"upload.max_file_size": "2GiB", "sessions.idle_timeout": "1m"}, nil)
	if next != nil {
		t.Fatal("a change with a bad value returned a configuration")
	}
	ce := changeError(t, err)
	if got := problemKeys(ce); !slices.Equal(got, []string{"sessions.idle_timeout"}) {
		t.Errorf("problem keys = %v, want sessions.idle_timeout", got)
	}
	if c.Upload.MaxFileSize != 100*GiB || c.Sessions.IdleTimeout != Duration(168*time.Hour) {
		t.Errorf("the configuration changed: %v, %v", c.Upload.MaxFileSize, c.Sessions.IdleTimeout)
	}
}

func TestChangeAppliesAndRecordsTheSource(t *testing.T) {
	t.Parallel()
	c := Defaults()
	next, err := c.Change(map[string]string{"trash.retention": "48h", "office.lock_minutes": "10", "search.enabled": "false"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.Trash.Retention != Duration(48*time.Hour) || next.Office.LockMinutes != 10 || next.Search.Enabled {
		t.Errorf("next = trash %v, lock %d, search %v", next.Trash.Retention, next.Office.LockMinutes, next.Search.Enabled)
	}
	for _, key := range []string{"trash.retention", "office.lock_minutes", "search.enabled"} {
		if next.Source(key) != SourceSettings {
			t.Errorf("Source(%s) = %s, want settings", key, next.Source(key))
		}
	}
	if text, ok := next.StoredText("trash.retention"); !ok || text != "48h0m0s" {
		t.Errorf("stored text = %q %v, want 48h0m0s", text, ok)
	}
	if c.Trash.Retention != Defaults().Trash.Retention || c.Source("trash.retention") != SourceDefault {
		t.Error("Change changed the configuration it was called on")
	}
}

func TestChangeRefusesLockedAndUnknownKeys(t *testing.T) {
	t.Parallel()
	c := loadYAML(t, "upload:\n  max_file_size: 2GiB\n", nil)
	_, err := c.Change(map[string]string{"upload.max_file_size": "4GiB", "office.enabled": "true", "trash.retention": "48h"}, nil)
	ce := changeError(t, err)
	if !slices.Equal(ce.Locked, []string{"office.enabled", "upload.max_file_size"}) || len(ce.Unknown) != 0 {
		t.Errorf("locked = %v, unknown = %v", ce.Locked, ce.Unknown)
	}
	_, err = c.Change(nil, []string{"upload.max_file_size"})
	if ce := changeError(t, err); !slices.Equal(ce.Locked, []string{"upload.max_file_size"}) {
		t.Errorf("a reset of a locked key: locked = %v", ce.Locked)
	}
	_, err = c.Change(map[string]string{"upload.max_size": "1GiB"}, []string{"listen"})
	if ce := changeError(t, err); !slices.Equal(ce.Unknown, []string{"listen", "upload.max_size"}) {
		t.Errorf("unknown = %v, want listen and upload.max_size", ce.Unknown)
	}
}

func TestChangeResetGoesBackToTheDefault(t *testing.T) {
	t.Parallel()
	c := Defaults()
	set, err := c.Change(map[string]string{"trash.retention": "48h"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	back, err := set.Change(nil, []string{"trash.retention"})
	if err != nil {
		t.Fatal(err)
	}
	if back.Trash.Retention != Defaults().Trash.Retention || back.Source("trash.retention") != SourceDefault {
		t.Errorf("after reset: %v from %s", back.Trash.Retention, back.Source("trash.retention"))
	}
	if _, ok := back.StoredText("trash.retention"); ok {
		t.Error("the reset key still has stored text")
	}
}

// TestChangeChecksTheRulesAcrossKeys: Validate runs on the merged configuration, and its message names each key it is
// about.
func TestChangeChecksTheRulesAcrossKeys(t *testing.T) {
	t.Parallel()
	c := Defaults()
	_, err := c.Change(map[string]string{"upload.max_chunk_size": "2GiB", "upload.max_file_size": "1GiB"}, nil)
	ce := changeError(t, err)
	if got := problemKeys(ce); !slices.Contains(got, "upload.max_chunk_size") {
		t.Errorf("problem keys = %v, want upload.max_chunk_size named", got)
	}
	for _, p := range ce.Problems {
		if !strings.HasPrefix(p.Message, "config: ") {
			t.Errorf("message %q is not in the config errors' style", p.Message)
		}
	}
}

// TestChangeRefusesBadValues: NaN, negative, overflowing and out-of-bounds values are refused, each named.
func TestChangeRefusesBadValues(t *testing.T) {
	t.Parallel()
	c := Defaults()
	for _, tc := range []struct{ key, value string }{
		{"upload.max_chunk_size", "NaN"},
		{"upload.max_chunk_size", "-1"},
		{"upload.max_file_size", "99999999999999999999"},
		{"upload.max_file_size", "17TiB"},
		{"upload.max_chunk_size", "512KiB"},
		{"trash.retention", "-1h"},
		{"trash.retention", "1h"},
		{"trash.retention", "9999999999999h"},
		{"versions.max_age", "1m"},
		{"versions.max_count", "-1"},
		{"versions.max_count", "1.5"},
		{"office.lock_minutes", "31"},
		{"office.lock_minutes", "0"},
		{"search.enabled", "maybe"},
		{"sharing.max_expiry", "1m"},
		{"links.max_ttl", "169h"},
		{"storage.min_free", "2TiB"},
		// The database shares the data disk, so the API keeps room for its writes.
		{"storage.min_free", "0"},
		{"storage.min_free", "255MiB"},
	} {
		_, err := c.Change(map[string]string{tc.key: tc.value}, nil)
		if err == nil {
			t.Errorf("%s = %q was accepted", tc.key, tc.value)
			continue
		}
		if got := problemKeys(changeError(t, err)); !slices.Equal(got, []string{tc.key}) {
			t.Errorf("%s = %q: problem keys %v", tc.key, tc.value, got)
		}
	}
	// Zero means no limit where the file allows it.
	if _, err := c.Change(map[string]string{"sharing.max_expiry": "0s", "versions.max_age": "0s", "versions.max_count": "0"}, nil); err != nil {
		t.Errorf("zero for no limit was refused: %v", err)
	}
	if _, err := c.Change(map[string]string{"storage.min_free": "256MiB"}, nil); err != nil {
		t.Errorf("storage.min_free at its floor was refused: %v", err)
	}
}

// TestSessionLifetimesHaveFloorsAndCaps: a session lifetime set through the settings cannot be so short that people are
// signed out at once, nor longer than its cap.
func TestSessionLifetimesHaveFloorsAndCaps(t *testing.T) {
	t.Parallel()
	c := Defaults()
	for _, tc := range []struct {
		key, low, lowOK, highOK, high string
	}{
		{"sessions.idle_timeout", "14m59s", "15m", "720h", "721h"},
		{"sessions.max_age", "59m", "1h", "2160h", "2161h"},
		// An admin session can only be made shorter than decision 84's hardening: 12 hours idle, 7 days in all.
		{"sessions.admin_idle_timeout", "4m", "5m", "12h", "12h1s"},
		{"sessions.admin_max_age", "59m", "1h", "168h", "168h1s"},
		{"users.verification_ttl", "14m", "15m", "720h", "721h"},
		{"users.reset_ttl", "4m", "5m", "24h", "25h"},
	} {
		for _, v := range []string{tc.low, tc.high} {
			if _, err := c.Change(map[string]string{tc.key: v}, nil); err == nil {
				t.Errorf("%s = %s was accepted", tc.key, v)
			}
		}
		for _, v := range []string{tc.lowOK, tc.highOK} {
			if _, err := c.Change(map[string]string{tc.key: v}, nil); err != nil {
				t.Errorf("%s = %s was refused: %v", tc.key, v, err)
			}
		}
	}
}

// TestFileKeepsItsOwnRules: the settings' floors bind values set through the settings only; a file with a shorter
// lifetime still loads, so an upgrade does not stop a working installation.
func TestFileKeepsItsOwnRules(t *testing.T) {
	t.Parallel()
	c := loadYAML(t, "sessions:\n  idle_timeout: 1m\n  max_age: 2m\ntrash:\n  retention: 1h\n", nil)
	if c.Sessions.IdleTimeout != Duration(time.Minute) || c.Trash.Retention != Duration(time.Hour) {
		t.Errorf("file values = %v, %v", c.Sessions.IdleTimeout, c.Trash.Retention)
	}
}

// TestSettingTextChecksTheJSONType: a value must have the JSON type of its setting; a string is not a bool and a
// fraction is not an integer.
func TestSettingTextChecksTheJSONType(t *testing.T) {
	t.Parallel()
	good := []struct{ key, raw, text string }{
		{"search.enabled", `false`, "false"},
		{"office.lock_minutes", `12`, "12"},
		{"trash.retention", `"48h"`, "48h"},
		{"upload.max_file_size", `"2GiB"`, "2GiB"},
		{"upload.max_file_size", `1073741824`, "1073741824"},
	}
	for _, g := range good {
		text, err := SettingText(g.key, []byte(g.raw))
		if err != nil || text != g.text {
			t.Errorf("SettingText(%s, %s) = %q, %v; want %q", g.key, g.raw, text, err, g.text)
		}
	}
	bad := []struct{ key, raw string }{
		{"search.enabled", `"true"`},
		{"search.enabled", `1`},
		{"office.lock_minutes", `1.5`},
		{"office.lock_minutes", `1e3`},
		{"office.lock_minutes", `"12"`},
		{"versions.max_count", `99999999999999999999`},
		{"trash.retention", `48`},
		{"trash.retention", `null`},
		{"upload.max_file_size", `-1`},
		{"upload.max_file_size", `1.5`},
		{"upload.max_file_size", `true`},
		{"nope.key", `1`},
		// A value longer than any setting needs, even one that parses: "0s" forty times and then "1h" is an hour.
		{"trash.retention", `"` + strings.Repeat("0s", 40) + `1h"`},
	}
	for _, b := range bad {
		if text, err := SettingText(b.key, []byte(b.raw)); err == nil {
			t.Errorf("SettingText(%s, %s) = %q, want an error", b.key, b.raw, text)
		} else if !strings.Contains(err.Error(), b.key) {
			t.Errorf("SettingText(%s, %s) error %q does not name the key", b.key, b.raw, err)
		}
	}
}

// TestEverySettingNamesAField: each setting's key is the YAML path of a field, of a kind its type can hold.
func TestEverySettingNamesAField(t *testing.T) {
	t.Parallel()
	c := Defaults()
	var keys []string
	for _, s := range c.Settings() {
		keys = append(keys, s.Key)
		if s.Value != s.Default {
			t.Errorf("%s: value %v differs from the default %v on Defaults()", s.Key, s.Value, s.Default)
		}
		switch s.Type {
		case TypeBool, TypeInt, TypeDuration, TypeBytes, TypeString:
		default:
			t.Errorf("%s has type %q", s.Key, s.Type)
		}
	}
	if !slices.IsSorted(keys) || len(slices.Compact(slices.Clone(keys))) != len(keys) {
		t.Errorf("keys are not sorted and unique: %v", keys)
	}
	want := []string{"admin.tenant_access", "links.default_ttl", "links.max_ttl", "office.discovery_url", "office.enabled",
		"office.lock_minutes", "office.max_file_bytes", "search.enabled", "search.office_text", "sessions.admin_idle_timeout",
		"sessions.admin_max_age", "sessions.idle_timeout", "sessions.max_age", "sharing.max_expiry", "storage.min_free",
		"trash.retention", "upload.max_chunk_size", "upload.max_file_size", "upload.max_small_file_size",
		"users.reset_ttl", "users.verification_ttl", "versions.max_age", "versions.max_count"}
	if !slices.Equal(keys, want) {
		t.Errorf("keys = %v\nwant   %v", keys, want)
	}
	for _, s := range c.Settings() {
		if s.Restart != (s.Key == "office.enabled" || s.Key == "office.discovery_url") {
			t.Errorf("%s restart = %v", s.Key, s.Restart)
		}
	}
}

// TestFactsHoldNoSecrets: the read-only facts never name a password file, a key file or a signing key.
func TestFactsHoldNoSecrets(t *testing.T) {
	t.Parallel()
	c := loadYAML(t, "admin_key_file: /run/secrets/admin-key\nmail:\n  smtp_host: mail.example.com\n  from: drive@example.com\n"+
		"  smtp_username: drive\n  smtp_password_file: /run/secrets/smtp\nlinks:\n  signing_key_file: /run/secrets/signing\n"+
		"  previous_key_file: /run/secrets/prev\n", nil)
	facts := c.Facts()
	seen := map[string]Fact{}
	for _, f := range facts {
		seen[f.Key] = f
		for _, secret := range []string{"/run/secrets", "drive@example.com"} {
			if s, ok := f.Value.(string); ok && strings.Contains(s, secret) && f.Key != "mail.from" {
				t.Errorf("fact %s shows %q", f.Key, s)
			}
		}
		if strings.Contains(f.Key, "password") || strings.Contains(f.Key, "key_file") || f.Key == "mail.smtp_username" {
			t.Errorf("fact %s is listed", f.Key)
		}
	}
	if f := seen["mail.smtp_host"]; f.Value != "mail.example.com" || f.Source != SourceFile {
		t.Errorf("mail.smtp_host = %+v", f)
	}
	if f := seen["public_url"]; f.Source != SourceDefault || f.Value != "http://localhost:8080" {
		t.Errorf("public_url = %+v", f)
	}
	for _, key := range []string{"listen", "data_dir", "store.path", "trusted_proxies", "cors.allowed_origins",
		"webhooks.allow_networks", "rate_limit.per_ip_per_minute", "log.level"} {
		if _, ok := seen[key]; !ok {
			t.Errorf("fact %s is missing", key)
		}
	}
}
