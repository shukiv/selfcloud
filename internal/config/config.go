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

	Log     LogConfig     `yaml:"log"`
	Store   StoreConfig   `yaml:"store"`
	Storage StorageConfig `yaml:"storage"`
	Upload  UploadConfig  `yaml:"upload"`
	UI      UIConfig      `yaml:"ui"`
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

// UploadConfig controls uploads.
type UploadConfig struct {
	MaxFileSize ByteSize `yaml:"max_file_size"`
	// MaxChunkSize caps one upload request. Behind a proxy with a 100 MB request cap, keep it at
	// or under 90MiB.
	MaxChunkSize ByteSize `yaml:"max_chunk_size"`
	Expiry       Duration `yaml:"expiry"`
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
		Upload: UploadConfig{
			MaxFileSize:  100 * GiB,
			MaxChunkSize: 64 * MiB,
			Expiry:       Duration(24 * time.Hour),
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
	if c.Upload.Expiry <= 0 {
		bad("upload.expiry must be positive")
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
