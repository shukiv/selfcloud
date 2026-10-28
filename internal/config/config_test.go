package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func writeFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "selfcloud.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaultsAreValid(t *testing.T) {
	cfg, err := Load("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Store.Path != filepath.Join("data", "selfcloud.db") {
		t.Errorf("store.path = %q", cfg.Store.Path)
	}
	if cfg.Upload.MaxChunkSize != 64*MiB || cfg.Upload.MaxFileSize != 100*GiB {
		t.Errorf("upload limits = %v, %v", cfg.Upload.MaxChunkSize, cfg.Upload.MaxFileSize)
	}
}

func TestFileThenEnv(t *testing.T) {
	p := writeFile(t, `
listen: ":9000"
public_url: "https://drive.example.com/"
data_dir: /srv/selfcloud
trusted_proxies: ["10.0.0.0/8", "192.0.2.7"]
upload:
  max_chunk_size: 90MiB
  expiry: 12h
ui:
  enabled: true
`)
	cfg, err := Load(p, env(map[string]string{
		"SELFCLOUD_LISTEN":                 ":9100",
		"SELFCLOUD_UPLOAD_MAX_CHUNK_SIZE":  "95MB",
		"SELFCLOUD_TRUSTED_PROXIES":        "203.0.113.0/24, 198.51.100.1",
		"SELFCLOUD_STORE_ALLOW_NETWORK_FS": "true",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":9100" {
		t.Errorf("env did not override listen: %q", cfg.Listen)
	}
	if cfg.PublicURL != "https://drive.example.com" {
		t.Errorf("public_url = %q, want the trailing slash trimmed", cfg.PublicURL)
	}
	if cfg.Store.Path != filepath.Join("/srv/selfcloud", "selfcloud.db") {
		t.Errorf("store.path = %q", cfg.Store.Path)
	}
	if cfg.Upload.MaxChunkSize != 95_000_000 {
		t.Errorf("max_chunk_size = %d", cfg.Upload.MaxChunkSize)
	}
	if cfg.Upload.Expiry.D() != 12*time.Hour {
		t.Errorf("expiry = %v", cfg.Upload.Expiry)
	}
	if got := strings.Join(cfg.TrustedProxies, " "); got != "203.0.113.0/24 198.51.100.1" {
		t.Errorf("trusted_proxies = %q", got)
	}
	if !cfg.UI.Enabled || !cfg.Store.AllowNetworkFS {
		t.Error("booleans not applied")
	}
}

func TestUnknownKeyIsAnError(t *testing.T) {
	p := writeFile(t, "upload:\n  max_chunk: 1MiB\n")
	if _, err := Load(p, nil); err == nil {
		t.Fatal("a misspelt key was accepted")
	}
}

func TestEmptyFileIsDefaults(t *testing.T) {
	if _, err := Load(writeFile(t, ""), nil); err != nil {
		t.Fatal(err)
	}
}

func TestValidationErrors(t *testing.T) {
	for name, vars := range map[string]map[string]string{
		"relative public url":  {"SELFCLOUD_PUBLIC_URL": "drive.example.com"},
		"public url with path": {"SELFCLOUD_PUBLIC_URL": "https://example.com/drive"},
		"bad proxy":            {"SELFCLOUD_TRUSTED_PROXIES": "10.0.0.0/33"},
		"chunk over file size": {"SELFCLOUD_UPLOAD_MAX_CHUNK_SIZE": "2GiB", "SELFCLOUD_UPLOAD_MAX_FILE_SIZE": "1GiB"},
		"postgres":             {"SELFCLOUD_STORE_DRIVER": "postgres"},
		"log level":            {"SELFCLOUD_LOG_LEVEL": "loud"},
		"bad bool":             {"SELFCLOUD_UI_ENABLED": "maybe"},
		"bad duration":         {"SELFCLOUD_SHUTDOWN_TIMEOUT": "30"},
		"link ttl over cap":    {"SELFCLOUD_LINKS_DEFAULT_TTL": "48h", "SELFCLOUD_LINKS_MAX_TTL": "24h"},
		"zero link max ttl":    {"SELFCLOUD_LINKS_MAX_TTL": "0s"},
	} {
		if _, err := Load("", env(vars)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestByteSize(t *testing.T) {
	for in, want := range map[string]ByteSize{
		"0":       0,
		"1048576": 1 << 20,
		"64MiB":   64 << 20,
		"64 mib":  64 << 20,
		"95MB":    95_000_000,
		"1.5GiB":  3 << 29,
		"100GiB":  100 << 30,
		"2TB":     2_000_000_000_000,
	} {
		var b ByteSize
		if err := b.UnmarshalText([]byte(in)); err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if b != want {
			t.Errorf("%q = %d, want %d", in, b, want)
		}
	}
	for _, in := range []string{"", "MiB", "-1", "12XB", "1..2MB", "99999999TiB"} {
		var b ByteSize
		if err := b.UnmarshalText([]byte(in)); err == nil {
			t.Errorf("%q accepted as %d", in, b)
		}
	}
	if s := (64 * MiB).String(); s != "64MiB" {
		t.Errorf("String() = %q", s)
	}
}

func TestParsePrefixes(t *testing.T) {
	ps, err := ParsePrefixes([]string{"10.1.2.3/8", "::ffff:192.0.2.1", "2001:db8::/32"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.0.0.0/8", "192.0.2.1/32", "2001:db8::/32"}
	for i, p := range ps {
		if p.String() != want[i] {
			t.Errorf("prefix %d = %s, want %s", i, p, want[i])
		}
	}
}

func TestUserMailAndLimitSettings(t *testing.T) {
	cfg, err := Load("", env(map[string]string{
		"SELFCLOUD_USERS_VERIFICATION_TTL":          "24h",
		"SELFCLOUD_SESSIONS_MAX_AGE":                "48h",
		"SELFCLOUD_MAIL_SMTP_HOST":                  "smtp.example.com",
		"SELFCLOUD_MAIL_SMTP_PORT":                  "465",
		"SELFCLOUD_MAIL_SMTP_TLS":                   "tls",
		"SELFCLOUD_MAIL_FROM":                       "Drive <drive@example.com>",
		"SELFCLOUD_RATE_LIMIT_PER_EMAIL_PER_MINUTE": "3",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Users.VerificationTTL.D() != 24*time.Hour || cfg.Sessions.MaxAge.D() != 48*time.Hour ||
		cfg.Sessions.IdleTimeout.D() != 168*time.Hour {
		t.Errorf("ttl/session settings = %+v %+v", cfg.Users, cfg.Sessions)
	}
	if !cfg.Mail.Configured() || cfg.Mail.SMTPPort != 465 || cfg.Mail.SMTPTLS != "tls" {
		t.Errorf("mail = %+v", cfg.Mail)
	}
	if cfg.RateLimit.PerEmailPerMinute != 3 || cfg.RateLimit.PerIPPerMinute != 10 {
		t.Errorf("rate limits = %+v", cfg.RateLimit)
	}
}

func TestUserMailAndLimitValidation(t *testing.T) {
	for name, vars := range map[string]map[string]string{
		"template without token": {"SELFCLOUD_USERS_VERIFY_URL_TEMPLATE": "{public_url}/verify"},
		"zero ttl":               {"SELFCLOUD_USERS_RESET_TTL": "0s"},
		"zero idle timeout":      {"SELFCLOUD_SESSIONS_IDLE_TIMEOUT": "0s"},
		"zero rate limit":        {"SELFCLOUD_RATE_LIMIT_PER_IP_PER_MINUTE": "0"},
		"bad smtp tls":           {"SELFCLOUD_MAIL_SMTP_HOST": "smtp.example.com", "SELFCLOUD_MAIL_FROM": "d@example.com", "SELFCLOUD_MAIL_SMTP_TLS": "ssl"},
		"bad port":               {"SELFCLOUD_MAIL_SMTP_HOST": "smtp.example.com", "SELFCLOUD_MAIL_FROM": "d@example.com", "SELFCLOUD_MAIL_SMTP_PORT": "70000"},
		"bad from":               {"SELFCLOUD_MAIL_SMTP_HOST": "smtp.example.com", "SELFCLOUD_MAIL_FROM": "not an address"},
	} {
		if _, err := Load("", env(vars)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// TestUploadQuotaSettings: the reservation settings have defaults, and invalid values are refused.
func TestUploadQuotaSettings(t *testing.T) {
	cfg := Defaults()
	if cfg.Upload.MaxLifetime.D() != 168*time.Hour || cfg.Upload.MaxReservedPerAccount != TiB ||
		cfg.Upload.MaxDiskSharePercent != 50 {
		t.Errorf("defaults = %v, %v, %d", cfg.Upload.MaxLifetime, cfg.Upload.MaxReservedPerAccount, cfg.Upload.MaxDiskSharePercent)
	}
	for name, mutate := range map[string]func(*Config){
		"lifetime zero":   func(c *Config) { c.Upload.MaxLifetime = 0 },
		"reservations 0":  func(c *Config) { c.Upload.MaxReservedPerAccount = 0 },
		"share 0":         func(c *Config) { c.Upload.MaxDiskSharePercent = 0 },
		"share above 100": func(c *Config) { c.Upload.MaxDiskSharePercent = 101 },
	} {
		c := Defaults()
		mutate(c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: Validate accepted it", name)
		}
	}
	c, err := Load(writeFile(t, ""), env(map[string]string{"SELFCLOUD_UPLOAD_MAX_DISK_SHARE_PERCENT": "80"}))
	if err != nil || c.Upload.MaxDiskSharePercent != 80 {
		t.Errorf("env override = %+v, %v", c, err)
	}
}
