// Package config loads Selfcloud's configuration from defaults, an optional YAML file and
// environment variables, in that order of precedence (environment wins).
//
// Every setting has an environment variable named SELFCLOUD_ followed by its YAML path in upper
// case, joined by underscores: upload.max_chunk_size is SELFCLOUD_UPLOAD_MAX_CHUNK_SIZE. Lists
// are comma-separated.
package config

import (
	"encoding"
	"errors"
	"fmt"
	"io"
	netmail "net/mail"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// EnvPrefix starts every environment variable name.
const EnvPrefix = "SELFCLOUD"

// Config is the whole configuration.
type Config struct {
	// Listen is the address the server listens on, e.g. ":8080".
	Listen string `yaml:"listen"`
	// MetricsListen, when set, serves /metrics on its own address instead of Listen.
	MetricsListen string `yaml:"metrics_listen"`
	// PublicURL is the address clients reach this server at. Links, upload locations and
	// emails are built from it.
	PublicURL string `yaml:"public_url"`
	// ContentURL optionally serves file contents from a separate origin.
	ContentURL string `yaml:"content_url"`
	// DataDir holds the metadata database (by default) and all file contents.
	DataDir string `yaml:"data_dir"`
	// TrustedProxies lists the addresses or CIDRs of proxies whose ClientIPHeader is believed.
	TrustedProxies []string `yaml:"trusted_proxies"`
	// ClientIPHeader carries the client address when the peer is a trusted proxy:
	// "X-Forwarded-For", or a single-address header such as "CF-Connecting-IP".
	ClientIPHeader string `yaml:"client_ip_header"`
	// ShutdownTimeout is how long in-flight requests get to finish on shutdown.
	ShutdownTimeout Duration `yaml:"shutdown_timeout"`
	// AdminKeyFile names a file holding an installation admin key (from `selfcloud admin
	// generate-key`). On start the key is added if it is not known yet, so automated
	// deployments can provision the first admin key as a secret.
	AdminKeyFile string `yaml:"admin_key_file"`

	Log       LogConfig       `yaml:"log"`
	Store     StoreConfig     `yaml:"store"`
	Storage   StorageConfig   `yaml:"storage"`
	Blobs     BlobsConfig     `yaml:"blobs"`
	Copy      CopyConfig      `yaml:"copy"`
	Upload    UploadConfig    `yaml:"upload"`
	UI        UIConfig        `yaml:"ui"`
	Users     UsersConfig     `yaml:"users"`
	Sessions  SessionsConfig  `yaml:"sessions"`
	Mail      MailConfig      `yaml:"mail"`
	RateLimit RateLimitConfig `yaml:"rate_limit"`
}

// UsersConfig controls users, verification and password reset.
type UsersConfig struct {
	// VerificationTTL is how long a verification link works.
	VerificationTTL Duration `yaml:"verification_ttl"`
	// ResetTTL is how long a password-reset link works.
	ResetTTL Duration `yaml:"reset_ttl"`
	// VerifyURLTemplate builds the verification link. {public_url} and {token} are replaced;
	// the token is URL-escaped and sits in the fragment, so it stays out of server logs.
	VerifyURLTemplate string `yaml:"verify_url_template"`
	// ResetURLTemplate builds the password-reset link, with the same placeholders.
	ResetURLTemplate string `yaml:"reset_url_template"`
}

// SessionsConfig controls web sessions.
type SessionsConfig struct {
	// IdleTimeout ends a session that has not been used for this long.
	IdleTimeout Duration `yaml:"idle_timeout"`
	// MaxAge ends a session this long after it began, however busy it is.
	MaxAge Duration `yaml:"max_age"`
}

// MailConfig controls outgoing mail. Mail is configured when smtp_host and from are set.
type MailConfig struct {
	SMTPHost string `yaml:"smtp_host"`
	SMTPPort int    `yaml:"smtp_port"`
	// SMTPUsername and SMTPPasswordFile authenticate to the SMTP server. The password is read
	// from the file, never from the configuration, so it stays out of YAML and the environment.
	SMTPUsername     string `yaml:"smtp_username"`
	SMTPPasswordFile string `yaml:"smtp_password_file"`
	// SMTPTLS is starttls (the default), tls (implicit TLS, usually port 465) or none.
	SMTPTLS string `yaml:"smtp_tls"`
	// From is the sender address, optionally with a name: "Drive <drive@example.com>".
	From string `yaml:"from"`
	// TemplatesDir optionally overrides the built-in mail templates. Its files are named like
	// the built-in ones, for example verify.en.txt.
	TemplatesDir string `yaml:"templates_dir"`
}

// Configured reports whether outgoing mail is set up.
func (m MailConfig) Configured() bool { return m.SMTPHost != "" && m.From != "" }

// RateLimitConfig limits attempts at sign-in, verification and password reset.
type RateLimitConfig struct {
	// PerIPPerMinute limits each client address. Verification and reset are limited only this way.
	PerIPPerMinute int `yaml:"per_ip_per_minute"`
	// PerEmailPerMinute limits each email address at sign-in and password-reset requests.
	PerEmailPerMinute int `yaml:"per_email_per_minute"`
}

// LogConfig controls logging.
type LogConfig struct {
	Level  string `yaml:"level"`  // debug, info, warn, error
	Format string `yaml:"format"` // json, text
}

// StoreConfig controls the metadata database.
type StoreConfig struct {
	Driver string `yaml:"driver"` // sqlite
	// Path is the SQLite file; empty means <data_dir>/selfcloud.db.
	Path string `yaml:"path"`
	// AllowNetworkFS lets the database sit on NFS, SMB or FUSE. Unsafe; for experts only.
	AllowNetworkFS bool `yaml:"allow_network_fs"`
}

// StorageConfig controls file storage.
type StorageConfig struct {
	// MinFree is the free disk below which the server reports not ready.
	MinFree ByteSize `yaml:"min_free"`
}

// CopyConfig controls copying folders.
type CopyConfig struct {
	// MaxNodes is the most nodes one copy may make. Larger copies are refused.
	MaxNodes int `yaml:"max_nodes"`
}

// BlobsConfig controls the removal of file contents that nothing refers to any more.
type BlobsConfig struct {
	// DeleteGrace is how long an unreferenced blob is kept before its file is removed. It is the
	// window in which a backup taken before the removal still has the file.
	DeleteGrace Duration `yaml:"delete_grace"`
}

// UploadConfig controls uploads.
type UploadConfig struct {
	MaxFileSize ByteSize `yaml:"max_file_size"`
	// MaxChunkSize caps one upload request. Behind a proxy with a 100 MB request cap, keep it at
	// or under 90MiB.
	MaxChunkSize ByteSize `yaml:"max_chunk_size"`
	// MaxSmallFileSize caps a file written in one request (a create or an edit of a file under
	// it). Larger files go through resumable uploads.
	MaxSmallFileSize ByteSize `yaml:"max_small_file_size"`
	// BodyTimeout is the time a request body may take to arrive, plus one second per MiB of the
	// size cap, so a slow client cannot hold a write open for ever.
	BodyTimeout Duration `yaml:"body_timeout"`
	Expiry      Duration `yaml:"expiry"`
	// MaxOpenPerPrincipal is how many unfinished uploads one caller (key or user) may hold.
	MaxOpenPerPrincipal int `yaml:"max_open_per_principal"`
	// MaxOpenPerAccount is how many unfinished uploads an account may hold.
	MaxOpenPerAccount int `yaml:"max_open_per_account"`
}

// UIConfig controls the web interface.
type UIConfig struct {
	Enabled bool `yaml:"enabled"`
}

// Defaults returns the configuration used when nothing is set.
func Defaults() *Config {
	return &Config{
		Listen:          ":8080",
		PublicURL:       "http://localhost:8080",
		DataDir:         "data",
		ClientIPHeader:  "X-Forwarded-For",
		ShutdownTimeout: Duration(30 * time.Second),
		Log:             LogConfig{Level: "info", Format: "json"},
		Store:           StoreConfig{Driver: "sqlite"},
		Storage:         StorageConfig{MinFree: 1 * GiB},
		Blobs:           BlobsConfig{DeleteGrace: Duration(7 * 24 * time.Hour)},
		Copy:            CopyConfig{MaxNodes: 200000},
		Upload: UploadConfig{
			MaxFileSize:         100 * GiB,
			MaxChunkSize:        64 * MiB,
			MaxSmallFileSize:    16 * MiB,
			BodyTimeout:         Duration(30 * time.Second),
			Expiry:              Duration(24 * time.Hour),
			MaxOpenPerPrincipal: 100,
			MaxOpenPerAccount:   1000,
		},
		Users: UsersConfig{
			VerificationTTL:   Duration(72 * time.Hour),
			ResetTTL:          Duration(time.Hour),
			VerifyURLTemplate: "{public_url}/verify#token={token}",
			ResetURLTemplate:  "{public_url}/reset-password#token={token}",
		},
		Sessions: SessionsConfig{
			IdleTimeout: Duration(168 * time.Hour),
			MaxAge:      Duration(720 * time.Hour),
		},
		Mail: MailConfig{SMTPPort: 587, SMTPTLS: "starttls"},
		RateLimit: RateLimitConfig{
			PerIPPerMinute:    10,
			PerEmailPerMinute: 5,
		},
	}
}

// Load builds the configuration: defaults, then the YAML file at path (if path is not empty),
// then environment variables found through lookup (normally os.LookupEnv). The result is
// validated.
func Load(path string, lookup func(string) (string, bool)) (*Config, error) {
	cfg := Defaults()
	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
		defer f.Close()
		dec := yaml.NewDecoder(f)
		dec.KnownFields(true)
		if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("config: %s: %w", path, err)
		}
	}
	if lookup != nil {
		if err := applyEnv(reflect.ValueOf(cfg).Elem(), EnvPrefix, lookup); err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
	}
	cfg.normalize()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) normalize() {
	c.PublicURL = strings.TrimRight(c.PublicURL, "/")
	c.ContentURL = strings.TrimRight(c.ContentURL, "/")
	if c.Store.Path == "" && c.DataDir != "" {
		c.Store.Path = filepath.Join(c.DataDir, "selfcloud.db")
	}
}

// Validate reports every invalid setting at once.
func (c *Config) Validate() error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf("config: "+format, args...)) }

	if c.Listen == "" {
		bad("listen is empty")
	}
	if err := checkBaseURL(c.PublicURL); err != nil {
		bad("public_url: %v", err)
	}
	if c.ContentURL != "" {
		if err := checkBaseURL(c.ContentURL); err != nil {
			bad("content_url: %v", err)
		}
	}
	if c.DataDir == "" {
		bad("data_dir is empty")
	}
	if _, err := ParsePrefixes(c.TrustedProxies); err != nil {
		bad("trusted_proxies: %v", err)
	}
	if c.ShutdownTimeout <= 0 {
		bad("shutdown_timeout must be positive")
	}
	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		bad("log.level %q is not debug, info, warn or error", c.Log.Level)
	}
	switch c.Log.Format {
	case "json", "text":
	default:
		bad("log.format %q is not json or text", c.Log.Format)
	}
	if c.Store.Driver != "sqlite" {
		bad("store.driver %q is not supported (only sqlite so far)", c.Store.Driver)
	}
	if strings.ContainsAny(c.Store.Path, "?#") {
		bad("store.path may not contain ? or #")
	}
	if c.Storage.MinFree < 0 {
		bad("storage.min_free is negative")
	}
	if c.Upload.MaxFileSize <= 0 {
		bad("upload.max_file_size must be positive")
	}
	if c.Upload.MaxChunkSize <= 0 || c.Upload.MaxChunkSize > c.Upload.MaxFileSize {
		bad("upload.max_chunk_size must be positive and at most upload.max_file_size")
	}
	if c.Upload.MaxSmallFileSize <= 0 || c.Upload.MaxSmallFileSize > c.Upload.MaxFileSize {
		bad("upload.max_small_file_size must be positive and at most upload.max_file_size")
	}
	if c.Blobs.DeleteGrace < 0 {
		bad("blobs.delete_grace is negative")
	}
	if c.Copy.MaxNodes <= 0 {
		bad("copy.max_nodes must be positive")
	}
	if c.Upload.BodyTimeout <= 0 {
		bad("upload.body_timeout must be positive")
	}
	if c.Upload.Expiry <= 0 {
		bad("upload.expiry must be positive")
	}
	if c.Upload.MaxOpenPerPrincipal <= 0 || c.Upload.MaxOpenPerAccount <= 0 {
		bad("upload.max_open_per_principal and upload.max_open_per_account must be positive")
	}
	if c.Users.VerificationTTL <= 0 || c.Users.ResetTTL <= 0 {
		bad("users.verification_ttl and users.reset_ttl must be positive")
	}
	for name, tmpl := range map[string]string{
		"users.verify_url_template": c.Users.VerifyURLTemplate,
		"users.reset_url_template":  c.Users.ResetURLTemplate,
	} {
		if !strings.Contains(tmpl, "{token}") {
			bad("%s must contain {token}", name)
		}
	}
	if c.Sessions.IdleTimeout <= 0 || c.Sessions.MaxAge <= 0 {
		bad("sessions.idle_timeout and sessions.max_age must be positive")
	}
	if c.RateLimit.PerIPPerMinute <= 0 || c.RateLimit.PerEmailPerMinute <= 0 {
		bad("rate_limit values must be positive")
	}
	if c.Mail.Configured() {
		if c.Mail.SMTPPort < 1 || c.Mail.SMTPPort > 65535 {
			bad("mail.smtp_port %d is not a port number", c.Mail.SMTPPort)
		}
		switch c.Mail.SMTPTLS {
		case "starttls", "tls", "none":
		default:
			bad("mail.smtp_tls %q is not starttls, tls or none", c.Mail.SMTPTLS)
		}
		if _, err := netmail.ParseAddress(c.Mail.From); err != nil {
			bad("mail.from %q is not an address: %v", c.Mail.From, err)
		}
	}
	return errors.Join(errs...)
}

// checkBaseURL accepts an absolute http or https URL with no path, query or fragment.
// Serving under a sub-path is not supported.
func checkBaseURL(s string) error {
	u, err := url.Parse(s)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%q must start with http:// or https://", s)
	}
	if u.Host == "" {
		return fmt.Errorf("%q has no host", s)
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("%q must be scheme and host only", s)
	}
	return nil
}

// ParsePrefixes parses addresses and CIDRs; a bare address becomes a single-host prefix.
func ParsePrefixes(list []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(list))
	for _, s := range list {
		s = strings.TrimSpace(s)
		if strings.Contains(s, "/") {
			p, err := netip.ParsePrefix(s)
			if err != nil {
				return nil, err
			}
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(s)
		if err != nil {
			return nil, err
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

var textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()

// applyEnv walks the struct and sets each field whose environment variable is present.
func applyEnv(v reflect.Value, prefix string, lookup func(string) (string, bool)) error {
	t := v.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		tag, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if tag == "" || tag == "-" {
			continue
		}
		name := prefix + "_" + strings.ToUpper(tag)
		fv := v.Field(i)
		if fv.Kind() == reflect.Struct && !fv.Addr().Type().Implements(textUnmarshalerType) {
			if err := applyEnv(fv, name, lookup); err != nil {
				return err
			}
			continue
		}
		s, ok := lookup(name)
		if !ok {
			continue
		}
		if err := setFromString(fv, s); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

func setFromString(fv reflect.Value, s string) error {
	if u, ok := fv.Addr().Interface().(encoding.TextUnmarshaler); ok {
		return u.UnmarshalText([]byte(s))
	}
	switch fv.Kind() {
	case reflect.String:
		fv.SetString(s)
	case reflect.Bool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return err
		}
		fv.SetBool(b)
	case reflect.Int, reflect.Int64:
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return err
		}
		fv.SetInt(n)
	case reflect.Slice:
		if fv.Type().Elem().Kind() != reflect.String {
			return fmt.Errorf("unsupported list type %s", fv.Type())
		}
		var items []string
		for _, part := range strings.Split(s, ",") {
			if part = strings.TrimSpace(part); part != "" {
				items = append(items, part)
			}
		}
		fv.Set(reflect.ValueOf(items))
	default:
		return fmt.Errorf("unsupported type %s", fv.Type())
	}
	return nil
}
