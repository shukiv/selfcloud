// SPDX-License-Identifier: AGPL-3.0-or-later

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
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"

	"github.com/shukiv/selfcloud/internal/thumb"
)

// EnvPrefix starts every environment variable name.
const EnvPrefix = "SELFCLOUD"

// Config is the whole configuration.
type Config struct {
	// Listen is the address the server listens on, e.g. ":8080".
	Listen string `yaml:"listen"`
	// MetricsListen, when set, serves /metrics on its own address instead of Listen.
	MetricsListen string `yaml:"metrics_listen"`
	// MetricsOnMain serves /metrics on the main listener. It is off by default, since /metrics shows storage totals,
	// route counts and the webhook queue to anyone who can reach the listener (sec1, decision 84). It has no effect
	// when metrics_listen is set, which serves /metrics on its own address.
	MetricsOnMain bool `yaml:"metrics_on_main"`
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
	Derived   DerivedConfig   `yaml:"derived"`
	Zip       ZipConfig       `yaml:"zip"`
	Search    SearchConfig    `yaml:"search"`
	Copy      CopyConfig      `yaml:"copy"`
	Imports   ImportsConfig   `yaml:"imports"`
	Bundle    BundleConfig    `yaml:"bundle"`
	Sharing   SharingConfig   `yaml:"sharing"`
	Trash     TrashConfig     `yaml:"trash"`
	Audit     AuditConfig     `yaml:"audit"`
	Versions  VersionsConfig  `yaml:"versions"`
	Upload    UploadConfig    `yaml:"upload"`
	Links     LinksConfig     `yaml:"links"`
	Tokens    TokensConfig    `yaml:"tokens"`
	Webhooks  WebhooksConfig  `yaml:"webhooks"`
	CORS      CORSConfig      `yaml:"cors"`
	UI        UIConfig        `yaml:"ui"`
	Office    OfficeConfig    `yaml:"office"`
	Users     UsersConfig     `yaml:"users"`
	Sessions  SessionsConfig  `yaml:"sessions"`
	Download  DownloadConfig  `yaml:"download"`
	Admin     AdminConfig     `yaml:"admin"`
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
	// EmailChangeURLTemplate builds the link that confirms a new address (decision 89), with the same placeholders.
	EmailChangeURLTemplate string `yaml:"email_change_url_template"`
	// SignInLinkTTL is how long a sign-in link works. At most 10 minutes.
	SignInLinkTTL Duration `yaml:"sign_in_link_ttl"`
	// SignInURLTemplate builds the sign-in link, with {public_url} and {token} replaced. The token
	// goes in the fragment, so it never reaches a server log or a Referer.
	SignInURLTemplate string `yaml:"sign_in_url_template"`
	// DeviceConfirmTTL is how long the link that confirms a new browser works, after the email's sign-in cap is spent
	// (sec2, decision 86). At most 30 minutes.
	DeviceConfirmTTL Duration `yaml:"device_confirm_ttl"`
	// DeviceConfirmURLTemplate builds that confirmation link, with {public_url} and {token} replaced. The token goes in
	// the fragment, as the other links' do.
	DeviceConfirmURLTemplate string `yaml:"device_confirm_url_template"`
}

// AdminConfig controls what an installation admin may do to tenant users (decision 41).
type AdminConfig struct {
	// TenantAccess turns on three ways for an installation admin to get into a tenant user's account:
	// a one-time password-reset link shown to the admin, a password the admin types (the user must
	// change it at the next sign-in), and sign-in-as (the admin uses the user's session, for an hour).
	// Off by default. It does not turn off the reset email, which reaches the user. An admin can
	// already reach a tenant's files by creating an audited account key, whatever this is set to.
	TenantAccess bool `yaml:"tenant_access"`
}

// DownloadConfig bounds how long a download may take to be read (sec1, decision 84). A download's write deadline is
// timeout plus its bytes divided by min_rate, set once before the copy, so a reader that stops is cut off and a reader
// that keeps up at min_rate is not.
type DownloadConfig struct {
	// Timeout is the base time a download may take, whatever its size.
	Timeout Duration `yaml:"timeout"`
	// MinRate is the slowest rate, in bytes a second, at which a reader is expected to take a download.
	MinRate ByteSize `yaml:"min_rate"`
}

// SessionsConfig controls web sessions.
type SessionsConfig struct {
	// IdleTimeout ends a session that has not been used for this long.
	IdleTimeout Duration `yaml:"idle_timeout"`
	// MaxAge ends a session this long after it began, however busy it is.
	MaxAge Duration `yaml:"max_age"`
	// ShortIdleTimeout ends a session the browser did not keep ("Keep me signed in" unticked) after this long unused.
	// Zero means unset: ShortIdle then gives the shorter of 12 hours and IdleTimeout.
	ShortIdleTimeout Duration `yaml:"short_idle_timeout"`
	// DeviceCookieLifetime is how long the device cookie a successful sign-in or password reset sets lasts. A
	// sign-in from a device with a valid cookie is not refused by the email's failed-sign-in cap (sec1, decision 84).
	DeviceCookieLifetime Duration `yaml:"device_cookie_lifetime"`
	// AdminIdleTimeout and AdminMaxAge are the idle timeout and the maximum age of an installation admin's session, which
	// are shorter than a user's by default (sec1, decision 84). A session of an admin ends at the shorter of these and
	// the user limits above.
	AdminIdleTimeout Duration `yaml:"admin_idle_timeout"`
	AdminMaxAge      Duration `yaml:"admin_max_age"`
}

// defaultShortIdle is the longest a session the browser did not keep may go unused, when the setting is unset.
const defaultShortIdle = Duration(12 * time.Hour)

// ShortIdle is the effective sessions.short_idle_timeout. Every reader uses this one value: the setting when it is
// set, else the shorter of 12 hours and sessions.idle_timeout, so an installation with a shorter idle timeout starts
// (decision 75).
func (c SessionsConfig) ShortIdle() Duration {
	if c.ShortIdleTimeout > 0 {
		return c.ShortIdleTimeout
	}
	if c.IdleTimeout > 0 && c.IdleTimeout < defaultShortIdle {
		return c.IdleTimeout
	}
	return defaultShortIdle
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
	// PerEmailPerMinute limits each email address at sign-in and password-reset requests, per client
	// address: one address's tries never use up another address's budget (sec1). Failed sign-ins to an
	// email from all addresses together are capped at 30 an hour, a fixed limit.
	PerEmailPerMinute int `yaml:"per_email_per_minute"`
	// CappedPerEmailPerHour limits the password checks made for one email from one client address, while the email's cap
	// on failed sign-ins is spent (sec2, decision 86, round 2 M1, per address since round 3). Past it, an attempt is
	// answered without a password check and sends no mail. At least 1.
	CappedPerEmailPerHour int `yaml:"capped_per_email_per_hour"`
	// CappedEmailPerHour is the ceiling on password checks for one email from all addresses together, while its cap on
	// failed sign-ins is spent (round 4). It bounds the hashing an attacker with many addresses can make, and it is the
	// price of the per-address budget: an attacker with enough addresses spends it and blocks the real user's mail for
	// the hour. The device cookie still works meanwhile. At least 1.
	CappedEmailPerHour int `yaml:"capped_email_per_hour"`
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
// ZipConfig is the stored zip download (design section 14, slice s2, decision 35). A zip is made as a job and kept
// for TTL. The limits are checked before the first byte is written.
type ZipConfig struct {
	// MaxEntries is the most entries (files and folders) one zip may hold. The default, 100,000, is a placeholder: the
	// owner's figure for a typical folder is not known.
	MaxEntries int `yaml:"max_entries"`
	// MaxBytes is the most bytes of files one zip may hold, counted on the tree as it was read. Larger zips are refused.
	MaxBytes ByteSize `yaml:"max_bytes"`
	// MaxDisk is the disk for all stored zips together: the ready ones and the bytes the unfinished ones reserve.
	// A zip that would pass it is refused before it writes anything. It is separate from derived.max_bytes, so
	// thumbnails are never evicted for a zip.
	MaxDisk ByteSize `yaml:"max_disk"`
	// MaxDiskPerAccount is the share of MaxDisk one account's zips may hold, ready and reserved together, so one
	// account cannot use the whole budget and starve the others. It is enforced in the same write as MaxDisk.
	MaxDiskPerAccount ByteSize `yaml:"max_disk_per_account"`
	// TTL is how long a finished zip is kept. The sweep removes it after that.
	TTL Duration `yaml:"ttl"`
	// MaxPerPrincipal is how many unfinished zips one caller may have at once.
	MaxPerPrincipal int `yaml:"max_per_principal"`
	// MaxPerAccount is how many unfinished zips an account may have at once, across its callers.
	MaxPerAccount int `yaml:"max_per_account"`
	// MaxUnfinished is how many unfinished zips the installation may have at once. Each one holds its open file and
	// its entry list in memory until it finishes, so this bounds the memory.
	MaxUnfinished int `yaml:"max_unfinished"`
}

// validateZip checks the zip block.
func validateZip(z ZipConfig, bad func(string, ...any)) {
	if z.MaxEntries < 1 || z.MaxEntries > 1_000_000 {
		bad("zip.max_entries must be between 1 and 1,000,000")
	}
	if z.MaxBytes <= 0 || z.MaxDisk < z.MaxBytes {
		bad("zip.max_bytes must be positive, and zip.max_disk must be at least zip.max_bytes")
	}
	if z.MaxDiskPerAccount <= 0 || z.MaxDiskPerAccount > z.MaxDisk {
		bad("zip.max_disk_per_account must be positive and at most zip.max_disk")
	}
	if z.TTL < Duration(time.Minute) {
		bad("zip.ttl must be at least one minute")
	}
	if z.MaxPerPrincipal < 1 || z.MaxPerAccount < z.MaxPerPrincipal || z.MaxUnfinished < z.MaxPerAccount {
		bad("zip.max_per_principal, zip.max_per_account and zip.max_unfinished must be 1 or more, each at least the one before")
	}
}

type CopyConfig struct {
	// MaxNodes is the most nodes one copy may make. Larger copies are refused with too_large. The default, 50,000, is
	// the largest measured size (20,000, 50,000, 100,000) whose longest write of a chunked copy is at most 5 s, worst of
	// three runs on the measured machine: 1.6 s at 50,000 and 7.9 s at 100,000 (design.md decision 34, section 8). The
	// copy's batches are bounded separately. A higher value makes the publish hold the writer longer: other writers
	// wait, and one that waits past busy_timeout fails with SQLITE_BUSY. Raise it only with that cost in mind.
	MaxNodes int `yaml:"max_nodes"`
}

// SharingConfig controls how sharing works.
type SharingConfig struct {
	// MaxExpiry is the longest a permission may run from the moment it is set: a permission's
	// expires_at may be at most this far ahead. Zero means no cap.
	MaxExpiry Duration `yaml:"max_expiry"`
}

// TrashConfig controls the trash: deleted items wait here before they are purged.
type TrashConfig struct {
	// Retention is how long a deleted item stays in the trash. The retention sweep purges it after that,
	// and the listing shows that time as purge_at. It must be positive.
	Retention Duration `yaml:"retention"`
}

// AuditConfig controls the audit log (decision 48). Deleting an account deletes its entries; this setting is the
// retention of the rest.
type AuditConfig struct {
	// RetentionDays is how many days an audit entry is kept, installation-wide entries included. Older entries are
	// deleted by the sweep, for good. Zero keeps every entry for ever. An operator with legal retention duties must set
	// this period to what the law requires.
	RetentionDays int `yaml:"retention_days"`
}

// DerivedDir is the directory the derived files are kept in: derived.dir, or data_dir/derived when that is empty. The
// server and selfcloud fsck both use it, so they cannot disagree about where the files are.
func (c *Config) DerivedDir() string {
	if c.Derived.Dir != "" {
		return c.Derived.Dir
	}
	return filepath.Join(c.DataDir, "derived")
}

// DerivedConfig controls the files made from file contents, such as thumbnails (design section 14, slice s1).
// They are kept apart from the blobs, have their own budget, and do not count toward any quota.
type DerivedConfig struct {
	// Dir holds the derived files. Empty means data_dir/derived.
	Dir string `yaml:"dir"`
	// MaxBytes is the budget for the derived files of all accounts together. Past it, the least recently
	// used files are removed.
	MaxBytes ByteSize `yaml:"max_bytes"`
	// ThumbSizes are the longer edges, in pixels, that thumbnails are made at. The smallest is the size
	// has_thumbnail refers to, and the size a thumbnail request takes by default.
	ThumbSizes []int `yaml:"thumb_sizes"`
	// MaxSourceBytes is the largest source a thumbnail is made from.
	MaxSourceBytes ByteSize `yaml:"max_source_bytes"`
	// AudioMaxSourceBytes is the largest audio file whose cover and tags are read (decision 56). The reader takes the head and
	// the tail of the file, not all of it, so this limit is the size of a file that is read at all, and it is far larger than
	// an image's.
	AudioMaxSourceBytes ByteSize `yaml:"audio_max_source_bytes"`
	// MaxPixels is the most pixels a source image may have. The header is read first, so a larger image is
	// refused before any of its pixels are allocated.
	MaxPixels int64 `yaml:"max_pixels"`
	// Timeout is how long one decode may take.
	Timeout Duration `yaml:"timeout"`
	// Workers is how many decodes run at once. Each runs in a child process.
	Workers int `yaml:"workers"`
	// Queue is how many thumbnails may wait for a worker. A request past it is refused with a retry-later.
	Queue int `yaml:"queue"`
	// MemoryBytes is how much memory one decode may map beyond what the runtime holds at start (the child's
	// address-space limit). It must hold the pixels of max_pixels: see validateDerived.
	MemoryBytes ByteSize `yaml:"memory"`
	// CPUSeconds is the CPU time one decode may use.
	CPUSeconds int `yaml:"cpu_seconds"`
	// ConvertWorkers is how many conversions run at once through Collabora (design decision 37). It is separate from
	// Workers: a conversion waits on the network, not the CPU. It needs office.enabled.
	ConvertWorkers int `yaml:"convert_workers"`
	// ConvertQueue is how many conversions may wait, over all accounts. A request past it is refused with a retry-later.
	ConvertQueue int `yaml:"convert_queue"`
	// ConvertAccountQueue is how many of those places one account may hold, so one account cannot fill the queue (decision 37).
	ConvertAccountQueue int `yaml:"convert_account_queue"`
	// MaxConvertBytes is the largest file one conversion may return. A larger answer fails the conversion.
	MaxConvertBytes ByteSize `yaml:"max_convert_bytes"`
}

// SearchConfig controls search by name and label values (design section 9, slice q, decision 40). Off, the route answers
// not_found; the index is still kept, so turning it back on needs no backfill.
type SearchConfig struct {
	Enabled bool `yaml:"enabled"`
	// MaxQueryBytes is the longest q, in bytes.
	MaxQueryBytes int `yaml:"max_query_bytes"`
	// MaxTerms is the most terms one q may have.
	MaxTerms int `yaml:"max_terms"`
	// Timeout is the longest one request may take.
	Timeout Duration `yaml:"timeout"`
	// RatePerMinute is the searches one caller may start a minute.
	RatePerMinute int `yaml:"rate_per_minute"`
	// FullTextMaxBytes is the largest file whose text is read, plain or converted (decision 13: 50 MiB). A larger file is
	// not read: its content row is skipped as too large, and its name stays searchable (decision 46).
	FullTextMaxBytes ByteSize `yaml:"fulltext_max_bytes"`
	// TextMaxBytes is the text kept for each file (decision 13: 1 MiB). A longer text is cut at a character boundary and
	// marked truncated, so a word past the cut is not found.
	TextMaxBytes ByteSize `yaml:"text_max_bytes"`
	// OfficeText is whether documents (Word, spreadsheets, presentations, PDFs) are sent to the converter for their text
	// (review S4). It is off by default: a converter run for previews has not been agreed for every tenant's documents.
	// With it off, no document is sent, and document files have no content search.
	OfficeText bool `yaml:"office_text"`
	// MaxTextPerAccount is the stored text one account may hold, in bytes of extracted text, and MaxTextTotal the text of
	// the whole installation (decision 46). A file that would pass a cap is skipped with the cap's reason, and nothing is
	// read for it. 0 means no cap. The text is not charged to the tenant's quota.
	MaxTextPerAccount ByteSize `yaml:"max_text_per_account"`
	MaxTextTotal      ByteSize `yaml:"max_text_total"`
}

// BundleConfig is the disk for bundles (decision 120). A bundle's zip is kept until its owner deletes it, so its disk is
// a budget of its own: one budget shared with the stored zips, which expire, would let the two crowd each other. The caps on a bundle's entries and bytes are the import's (imports.archive).
type BundleConfig struct {
	// MaxDisk is the disk for all bundles together: the ready ones and the bytes the unfinished ones reserve. A bundle that
	// would pass it is refused before it writes anything. The default, 30 GiB, is a placeholder.
	MaxDisk ByteSize `yaml:"max_disk"`
	// MaxDiskPerAccount is the share of MaxDisk one account's bundles may hold, ready and reserved together. The default,
	// 10 GiB, is a placeholder.
	MaxDiskPerAccount ByteSize `yaml:"max_disk_per_account"`
}

// validateBundle checks the bundle block. A zero budget would refuse every bundle, and a share above the budget could
// never be used in full, so each must be positive and the share at most the budget.
func validateBundle(b BundleConfig, bad func(string, ...any)) {
	if b.MaxDisk <= 0 {
		bad("bundle.max_disk must be positive")
	}
	if b.MaxDiskPerAccount <= 0 || b.MaxDiskPerAccount > b.MaxDisk {
		bad("bundle.max_disk_per_account must be positive and at most bundle.max_disk")
	}
}

// validateImports checks the imports block (decision 92). A zero or negative cap would refuse every archive, so each cap
// must be positive; the job pool and the retention must be positive too.
func validateImports(i ImportsConfig, bad func(string, ...any)) {
	if i.MaxConcurrentJobs < 1 || i.MaxConcurrentJobs > 32 {
		bad("imports.max_concurrent_jobs must be from 1 to 32")
	}
	if i.JobRetentionDays < 1 {
		bad("imports.job_retention_days must be at least 1")
	}
	if i.MaxUnfinishedPerUser < 1 || i.MaxUnfinishedPerUser > 100 {
		bad("imports.max_unfinished_per_user must be from 1 to 100")
	}
	if i.MaxArchiveUploadsPerUser < 1 || i.MaxArchiveUploadsPerUser > 100 {
		bad("imports.max_archive_uploads_per_user must be from 1 to 100")
	}
	if i.UnclaimedExpiryHours < 1 {
		bad("imports.unclaimed_expiry_hours must be at least 1")
	}
	if i.PausedExpiryDays < 1 {
		bad("imports.paused_expiry_days must be at least 1")
	}
	a := i.Archive
	if a.MaxEntries < 1 || a.MaxEntries > 1_000_000 {
		bad("imports.archive.max_entries must be from 1 to 1000000")
	}
	if a.MaxDepth < 1 || a.MaxDepth > 256 {
		bad("imports.archive.max_depth must be from 1 to 256")
	}
	if a.MaxRatio < 1 {
		bad("imports.archive.max_ratio must be at least 1")
	}
	if a.MaxTotalBytes <= 0 {
		bad("imports.archive.max_total_bytes must be positive")
	}
	if a.MaxPathBytes <= 0 {
		bad("imports.archive.max_path_bytes must be positive")
	}
}

// validateSearch checks the search block.
func validateSearch(s SearchConfig, bad func(string, ...any)) {
	if s.TextMaxBytes < 1024 {
		bad("search.text_max_bytes must be at least 1 KiB, got %d", s.TextMaxBytes)
	}
	if s.MaxTextPerAccount != 0 && s.MaxTextPerAccount < s.TextMaxBytes {
		bad("search.max_text_per_account (%d) must be 0 or at least search.text_max_bytes (%d), or no file fits", s.MaxTextPerAccount, s.TextMaxBytes)
	}
	if s.MaxTextTotal != 0 && s.MaxTextPerAccount != 0 && s.MaxTextTotal < s.MaxTextPerAccount {
		bad("search.max_text_total (%d) must be 0 or at least search.max_text_per_account (%d)", s.MaxTextTotal, s.MaxTextPerAccount)
	}
	if s.FullTextMaxBytes < s.TextMaxBytes {
		bad("search.fulltext_max_bytes (%d) must not be below search.text_max_bytes (%d)", s.FullTextMaxBytes, s.TextMaxBytes)
	}
	if s.MaxQueryBytes < 1 || s.MaxQueryBytes > 1000 {
		bad("search.max_query_bytes must be between 1 and 1000, got %d", s.MaxQueryBytes)
	}
	if s.MaxTerms < 1 || s.MaxTerms > 16 {
		bad("search.max_terms must be between 1 and 16, got %d", s.MaxTerms)
	}
	if s.Timeout.D() <= 0 {
		bad("search.timeout must be positive")
	}
	if s.RatePerMinute < 1 {
		bad("search.rate_per_minute must be at least 1, got %d", s.RatePerMinute)
	}
}

// validateDerived checks the derived block. The memory must hold a decode of the largest image the pixel cap allows,
// read at the source size cap, and a thumbnail at each configured size: see thumb.MemoryFloor.
func validateDerived(d DerivedConfig, bad func(string, ...any)) {
	if d.MaxBytes <= 0 {
		bad("derived.max_bytes must be positive")
	}
	if len(d.ThumbSizes) == 0 || len(d.ThumbSizes) > 4 {
		bad("derived.thumb_sizes must name one to four sizes")
	}
	seen := map[int]bool{}
	for _, s := range d.ThumbSizes {
		switch {
		case s < 32 || s > 4096:
			bad("derived.thumb_sizes: %d is outside 32 to 4096", s)
		case seen[s]:
			bad("derived.thumb_sizes: %d is listed twice", s)
		}
		seen[s] = true
	}
	if d.MaxSourceBytes <= 0 || d.MaxSourceBytes > 1*GiB {
		bad("derived.max_source_bytes must be positive and at most 1GiB")
	}
	// An audio file is read at its head and tail, so its limit is the size a file may be, not a memory bound: up to 1 TiB.
	if d.AudioMaxSourceBytes <= 0 || d.AudioMaxSourceBytes > 1024*GiB {
		bad("derived.audio_max_source_bytes must be positive and at most 1TiB")
	}
	if d.MaxPixels <= 0 {
		bad("derived.max_pixels must be positive")
	}
	for _, s := range d.ThumbSizes {
		if floor := thumb.MemoryFloor(d.MaxPixels, int64(d.MaxSourceBytes), s); int64(d.MemoryBytes) < floor {
			bad("derived.memory is %d bytes, below %d, the floor for derived.max_pixels, max_source_bytes and the "+
				"thumb_sizes entry %d", d.MemoryBytes, floor, s)
		}
	}
	if d.Timeout <= 0 {
		bad("derived.timeout must be positive")
	}
	if d.Workers < 1 || d.Workers > 64 {
		bad("derived.workers must be between 1 and 64")
	}
	if d.Queue < 1 {
		bad("derived.queue must be positive")
	}
	if d.CPUSeconds < 1 {
		bad("derived.cpu_seconds must be at least 1")
	}
	if d.ConvertWorkers < 1 || d.ConvertWorkers > 16 {
		bad("derived.convert_workers must be between 1 and 16")
	}
	if d.ConvertQueue < 1 || d.ConvertQueue > 1024 {
		bad("derived.convert_queue must be between 1 and 1024")
	}
	if d.ConvertAccountQueue < 1 || d.ConvertAccountQueue > d.ConvertQueue {
		bad("derived.convert_account_queue must be between 1 and derived.convert_queue")
	}
	if d.MaxConvertBytes <= 0 || d.MaxConvertBytes > 4*GiB {
		bad("derived.max_convert_bytes must be positive and at most 4GiB")
	}
	// A converted file is kept in the budget, and one larger than the budget could never be kept: the budget must hold the
	// largest file a conversion may return (decision 37).
	if d.MaxBytes < d.MaxConvertBytes {
		bad("derived.max_bytes must be at least derived.max_convert_bytes, so one conversion's output fits the budget")
	}
}

// VersionsConfig controls the version history of files: the earlier contents kept when a file's content
// is replaced.
type VersionsConfig struct {
	// MaxCount is the most versions a file keeps that are not kept forever. Older ones go first. Zero means
	// no count limit.
	MaxCount int `yaml:"max_count"`
	// MaxAge is how long a version that is not kept forever is kept after its content was replaced. Zero
	// means no age limit.
	MaxAge Duration `yaml:"max_age"`
	// MaxKept is the most versions of one file that may be kept forever. Marking another is refused.
	MaxKept int `yaml:"max_kept"`
}

// BlobsConfig controls the removal of file contents that nothing refers to any more.
type BlobsConfig struct {
	// DeleteGrace is how long an unreferenced blob is kept before its file is removed. It is the
	// window in which a backup taken before the removal still has the file.
	DeleteGrace Duration `yaml:"delete_grace"`
}

// ImportsConfig controls background imports (decision 92). The archive source is the first one; the others are added
// by later slices, each with its own switch.
type ImportsConfig struct {
	// Enabled turns imports on. Off, no import can be started and the source list is empty.
	Enabled bool `yaml:"enabled"`
	// MaxConcurrentJobs is how many import jobs run at once, across the installation. Each runs in a worker of its own,
	// apart from the other background jobs.
	MaxConcurrentJobs int `yaml:"max_concurrent_jobs"`
	// JobRetentionDays is how long a finished import's job record (its state and item rows) is kept. The sweep removes
	// older records. The files it imported stay.
	JobRetentionDays int `yaml:"job_retention_days"`
	// MaxUnfinishedPerUser caps the imports a user has queued, running or paused at once. A paused import holds its
	// archive, so without the cap a user could hold any number of them.
	MaxUnfinishedPerUser int `yaml:"max_unfinished_per_user"`
	// MaxArchiveUploadsPerUser caps the import archives a user holds as uploads that no import has claimed yet. Each one
	// reserves its length against the quota, so the cap bounds what an unclaimed archive can hold (round 3, F1).
	MaxArchiveUploadsPerUser int `yaml:"max_archive_uploads_per_user"`
	// UnclaimedExpiryHours is how long an import archive that no import claimed lives after it was uploaded. Its
	// reservation goes with it (round 3, F1).
	UnclaimedExpiryHours int `yaml:"unclaimed_expiry_hours"`
	// PausedExpiryDays is how long an import may stay paused. Then it is cancelled and what it staged is removed.
	PausedExpiryDays int                 `yaml:"paused_expiry_days"`
	Archive          ArchiveImportConfig `yaml:"archive"`
}

// ArchiveImportConfig caps what an archive (zip or tar, with gzip for tar) may hold. A cap refused on the archive's
// headers stops the job before any entry is written; a cap refused on the bytes stops it at that entry, and what was
// staged is removed. Each cap is checked again as the bytes are written.
type ArchiveImportConfig struct {
	// Enabled turns the archive source on for the installation. An account owner may turn it off for one account.
	Enabled bool `yaml:"enabled"`
	// MaxEntries is the most entries (files, folders and links) one archive may hold.
	MaxEntries int `yaml:"max_entries"`
	// MaxDepth is the most folders an entry may sit below the archive's root.
	MaxDepth int `yaml:"max_depth"`
	// MaxRatio is the most a file may expand: its uncompressed size over its compressed size, per zip entry, and over
	// the whole stream for a tar. A file that expands more is refused, as a zip bomb is.
	MaxRatio int `yaml:"max_ratio"`
	// MaxTotalBytes is the most uncompressed bytes all the files of one archive may hold.
	MaxTotalBytes ByteSize `yaml:"max_total_bytes"`
	// MaxPathBytes is the most bytes the entries' full paths of one archive may hold, summed over the entries. It bounds the
	// memory a scan keeps to find the names that clash (round 4, N2).
	MaxPathBytes ByteSize `yaml:"max_path_bytes"`
}

// UploadConfig controls uploads.
type UploadConfig struct {
	MaxFileSize ByteSize `yaml:"max_file_size"`
	// MaxChunkSize caps one upload request. Behind a proxy with a 100 MB request cap, keep it at
	// or under 90MiB.
	MaxChunkSize ByteSize `yaml:"max_chunk_size"`
	// MaxSmallFileSize caps a file written in one request (a create or an edit of a file under
	// it). Larger files go through resumable uploads. A PUT of a text file is checked for UTF-8 after the
	// body is spooled (decision 113), so a larger cap makes a large refused text PUT take longer.
	MaxSmallFileSize ByteSize `yaml:"max_small_file_size"`
	// BodyTimeout is the time a request body may take to arrive, plus one second per MiB of the
	// size cap, so a slow client cannot hold a write open for ever.
	BodyTimeout Duration `yaml:"body_timeout"`
	Expiry      Duration `yaml:"expiry"`
	// MaxOpenPerPrincipal is how many unfinished uploads one caller (key or user) may hold.
	MaxOpenPerPrincipal int `yaml:"max_open_per_principal"`
	// MaxOpenPerAccount is how many unfinished uploads an account may hold.
	MaxOpenPerAccount int `yaml:"max_open_per_account"`
	// MaxLifetime ends an upload this long after it was created, however often it is written to.
	// upload.expiry still applies between writes.
	MaxLifetime Duration `yaml:"max_lifetime"`
	// MaxReservedPerAccount caps the bytes that unfinished uploads may reserve in an account that
	// has no quota. An account with a quota is held to the quota instead.
	MaxReservedPerAccount ByteSize `yaml:"max_reserved_per_account"`
	// MaxDiskSharePercent caps the unwritten bytes of one account's uploads, as a percentage of
	// the disk space that uploads may use (free space less storage.min_free). It is checked when
	// an upload is created, so no single account can take the whole pool; 1 to 100.
	MaxDiskSharePercent int `yaml:"max_disk_share_percent"`
}

// LinksConfig controls signed download links.
type LinksConfig struct {
	// DefaultTTL is a link's lifetime when a request names none.
	DefaultTTL Duration `yaml:"default_ttl"`
	// MaxTTL is the longest lifetime a link may be asked for. A longer request is refused.
	MaxTTL Duration `yaml:"max_ttl"`
	// SigningKeyFile names the file holding the current signing key: 32 raw bytes, mode 0600.
	// Empty means data_dir/keys/signing.key, which is made on first start if it is missing.
	SigningKeyFile string `yaml:"signing_key_file"`
	// PreviousKeyFile names the key that links signed before the last rotation still verify
	// with. Empty means data_dir/keys/signing.prev.key, used when that file exists.
	PreviousKeyFile string `yaml:"previous_key_file"`
}

// WebhooksConfig controls webhook endpoints (design section 10).
type WebhooksConfig struct {
	// AllowHTTP admits http:// endpoint URLs. Off by default: an endpoint must be HTTPS. For development only.
	AllowHTTP bool `yaml:"allow_http"`
	// AllowNetworks are the networks (CIDRs, or single addresses) a webhook may connect to even though the deny table
	// refuses them, such as a same-host application on a private Docker network. Empty by default, so a fresh install
	// refuses private addresses (decision 28).
	AllowNetworks []string `yaml:"allow_networks"`
}

// OfficeConfig controls office editing through WOPI with Collabora Online (design section 10, decision 31). Off by
// default: while it is off, every /wopi route and the office route answer not_found, and nothing is fetched.
type OfficeConfig struct {
	// Enabled turns the WOPI host and the office route on.
	Enabled bool `yaml:"enabled"`
	// DiscoveryURL is Collabora's WOPI discovery URL, such as https://office.example.com/hosting/discovery. It is
	// operator configuration and the only address this server fetches for office editing.
	DiscoveryURL string `yaml:"discovery_url"`
	// WOPIURL is the address Collabora uses to reach Selfcloud, with no path, when it differs from public_url. The
	// proof-key check signs the URL built from it, so it must be the address Collabora actually calls.
	WOPIURL string `yaml:"wopi_url"`
	// LockMinutes is how long a WOPI lock lasts without a refresh. The WOPI specification says 30, and Validate
	// refuses more.
	LockMinutes int `yaml:"lock_minutes"`
	// MaxFileBytes is the largest file that may be opened for editing, and the largest one PutFile may write.
	MaxFileBytes ByteSize `yaml:"max_file_bytes"`
	// TokenMinutes is how long an access token lives. A token never outlives the delegated token it came from.
	TokenMinutes int `yaml:"token_minutes"`
}

// TokensConfig controls delegated tokens, the short-lived credentials an account key mints for a
// browser.
type TokensConfig struct {
	// MaxTTL is the longest lifetime a delegated token may be minted with. A longer request is
	// refused, not shortened, as for signed links.
	MaxTTL Duration `yaml:"max_ttl"`
}

// CORSConfig controls cross-origin requests from browser applications.
type CORSConfig struct {
	// AllowedOrigins lists the origins (scheme, host and port, such as https://app.example.com)
	// whose pages may call the API. Empty turns CORS off. Cookies are never accepted on
	// cross-origin requests, so these origins use delegated tokens.
	AllowedOrigins []string `yaml:"allowed_origins"`
}

// UIConfig controls the web interface.
type UIConfig struct {
	// Enabled serves the web interface at /. The API is unaffected either way.
	Enabled bool `yaml:"enabled"`
	// Brand is the installation's look. It is the same for every user; per-tenant branding is
	// not built.
	Brand BrandConfig `yaml:"brand"`
}

// DefaultBrandName is the name of a new installation, and the name that shows the SelfCloud artwork when no logo is set
// (decision 109).
const DefaultBrandName = "SelfCloud"

// BrandConfig is the installation-wide name, accent colour and logo of the web interface.
type BrandConfig struct {
	// Name is shown in the header and the page title.
	Name string `yaml:"name"`
	// Accent is the primary colour as #rrggbb.
	Accent string `yaml:"accent"`
	// LogoFile optionally names a PNG or SVG file, read at start (at most 256 KiB). Its path is
	// never sent to clients; they get the URL /brand/logo instead.
	LogoFile string `yaml:"logo_file"`
	// IconFile optionally names a square PNG of 512 to 2048 pixels on a side, or an SVG file, read at start (at most 256 KiB).
	// It is used for the app icons and the browser's tab icon, and for nothing else (decision 119). Its path is never sent to
	// clients.
	IconFile string `yaml:"icon_file"`
	// Palette overrides colour tokens of the default look, one set per theme. An unset token keeps
	// the default; the interface derives the rest of the accent family from Accent.
	Palette BrandPalette `yaml:"palette"`
	// Sidebar overrides the sidebar's colours. An unset token keeps the default.
	Sidebar BrandSidebar `yaml:"sidebar"`
}

// BrandPalette holds the token overrides for each theme.
type BrandPalette struct {
	Light BrandTokens `yaml:"light"`
	Dark  BrandTokens `yaml:"dark"`
}

// BrandTokens are the accent tokens one theme may override, each as #rrggbb. Their names are the
// token names of the web interface's design system, in snake case.
type BrandTokens struct {
	Accent           string `yaml:"accent"`
	AccentFill       string `yaml:"accent_fill"`
	AccentFillHover  string `yaml:"accent_fill_hover"`
	AccentFillActive string `yaml:"accent_fill_active"`
	AccentText       string `yaml:"accent_text"`
	AccentSoft       string `yaml:"accent_soft"`
	AccentBorder     string `yaml:"accent_border"`
}

// BrandSidebar holds the sidebar's colour overrides, each as #rrggbb.
type BrandSidebar struct {
	Bg       string `yaml:"bg"`
	Text     string `yaml:"text"`
	Caption  string `yaml:"caption"`
	Active   string `yaml:"active"`
	OnActive string `yaml:"on_active"`
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
		Derived: DerivedConfig{MaxBytes: 10 * GiB, ThumbSizes: []int{256, 1024}, MaxSourceBytes: 50 * MiB, AudioMaxSourceBytes: 100 * GiB,
			MaxPixels: 50_000_000, Timeout: Duration(60 * time.Second), Workers: 2, Queue: 256,
			MemoryBytes: 1 * GiB, CPUSeconds: 30, ConvertWorkers: 2, ConvertQueue: 32, ConvertAccountQueue: 8,
			MaxConvertBytes: 500 * MiB},
		Zip: ZipConfig{MaxEntries: 100_000, MaxBytes: 10 * GiB, MaxDisk: 30 * GiB, MaxDiskPerAccount: 10 * GiB,
			TTL:             Duration(24 * time.Hour),
			MaxPerPrincipal: 2, MaxPerAccount: 4, MaxUnfinished: 8},
		Search: SearchConfig{Enabled: true, MaxQueryBytes: 200, MaxTerms: 8,
			Timeout: Duration(2 * time.Second), RatePerMinute: 60,
			FullTextMaxBytes: ByteSize(50 << 20), TextMaxBytes: ByteSize(1 << 20),
			MaxTextPerAccount: ByteSize(64 << 20), MaxTextTotal: ByteSize(1 << 30)},
		Copy:   CopyConfig{MaxNodes: 50000},
		Bundle: BundleConfig{MaxDisk: 30 * GiB, MaxDiskPerAccount: 10 * GiB},
		Imports: ImportsConfig{Enabled: true, MaxConcurrentJobs: 2, JobRetentionDays: 30,
			MaxUnfinishedPerUser: 3, PausedExpiryDays: 14, MaxArchiveUploadsPerUser: 3, UnclaimedExpiryHours: 24,
			Archive: ArchiveImportConfig{Enabled: true, MaxEntries: 50000, MaxDepth: 32, MaxRatio: 100,
				MaxTotalBytes: 100 * GiB, MaxPathBytes: 64 * MiB}},
		Sharing:  SharingConfig{MaxExpiry: Duration(365 * 24 * time.Hour)},
		Trash:    TrashConfig{Retention: Duration(30 * 24 * time.Hour)},
		Audit:    AuditConfig{RetentionDays: 365},
		Versions: VersionsConfig{MaxCount: 100, MaxAge: Duration(30 * 24 * time.Hour), MaxKept: 200},
		Links:    LinksConfig{DefaultTTL: Duration(5 * time.Minute), MaxTTL: Duration(24 * time.Hour)},
		Tokens:   TokensConfig{MaxTTL: Duration(time.Hour)},
		Upload: UploadConfig{
			MaxFileSize:           100 * GiB,
			MaxChunkSize:          64 * MiB,
			MaxSmallFileSize:      16 * MiB,
			BodyTimeout:           Duration(30 * time.Second),
			Expiry:                Duration(24 * time.Hour),
			MaxOpenPerPrincipal:   100,
			MaxOpenPerAccount:     1000,
			MaxLifetime:           Duration(168 * time.Hour),
			MaxReservedPerAccount: 1 * TiB,
			MaxDiskSharePercent:   50,
		},
		Users: UsersConfig{
			VerificationTTL:          Duration(72 * time.Hour),
			ResetTTL:                 Duration(time.Hour),
			VerifyURLTemplate:        "{public_url}/verify#token={token}",
			ResetURLTemplate:         "{public_url}/reset#token={token}",
			EmailChangeURLTemplate:   "{public_url}/email-change#token={token}",
			SignInLinkTTL:            Duration(60 * time.Second),
			SignInURLTemplate:        "{public_url}/sign-in#token={token}",
			DeviceConfirmTTL:         Duration(15 * time.Minute),
			DeviceConfirmURLTemplate: "{public_url}/confirm-device#token={token}",
		},
		Download: DownloadConfig{Timeout: Duration(30 * time.Second), MinRate: ByteSize(64 << 10)},
		Sessions: SessionsConfig{
			IdleTimeout:          Duration(168 * time.Hour),
			MaxAge:               Duration(720 * time.Hour),
			DeviceCookieLifetime: Duration(4320 * time.Hour),
			AdminIdleTimeout:     Duration(12 * time.Hour),
			AdminMaxAge:          Duration(168 * time.Hour),
		},
		UI:     UIConfig{Enabled: true, Brand: BrandConfig{Name: DefaultBrandName, Accent: "#053D8E"}},
		Office: OfficeConfig{LockMinutes: 30, MaxFileBytes: 50 * MiB, TokenMinutes: 600},
		Mail:   MailConfig{SMTPPort: 587, SMTPTLS: "starttls"},
		RateLimit: RateLimitConfig{
			PerIPPerMinute:        10,
			PerEmailPerMinute:     5,
			CappedPerEmailPerHour: 10,
			CappedEmailPerHour:    100,
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
	// Browsers send origins in lower case, so the configured ones are compared that way too.
	for i, o := range c.CORS.AllowedOrigins {
		c.CORS.AllowedOrigins[i] = strings.ToLower(strings.TrimSpace(o))
	}
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
	if _, err := ParsePrefixes(c.Webhooks.AllowNetworks); err != nil {
		bad("webhooks.allow_networks: %v", err)
	}
	checkOffice(c.Office, bad)
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
	validateDerived(c.Derived, bad)
	validateSearch(c.Search, bad)
	validateZip(c.Zip, bad)
	if c.Copy.MaxNodes <= 0 {
		bad("copy.max_nodes must be positive")
	}
	validateImports(c.Imports, bad)
	validateBundle(c.Bundle, bad)
	if c.Sharing.MaxExpiry < 0 {
		bad("sharing.max_expiry is negative; use 0 for no cap")
	}
	if c.Trash.Retention <= 0 {
		bad("trash.retention must be positive")
	}
	if c.Audit.RetentionDays < 0 {
		bad("audit.retention_days is negative; use 0 to keep audit entries for ever")
	}
	// Past this many days the retention would overflow a time.Duration (about 270 years).
	if c.Audit.RetentionDays > 100_000 {
		bad("audit.retention_days is more than 100,000 days")
	}
	if c.Versions.MaxCount < 0 {
		bad("versions.max_count is negative; use 0 for no count limit")
	}
	if c.Versions.MaxAge < 0 {
		bad("versions.max_age is negative; use 0 for no age limit")
	}
	if c.Versions.MaxKept <= 0 {
		bad("versions.max_kept must be positive")
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
	if c.Upload.MaxLifetime <= 0 {
		bad("upload.max_lifetime must be positive")
	}
	if c.Upload.MaxReservedPerAccount <= 0 {
		bad("upload.max_reserved_per_account must be positive")
	}
	if c.Upload.MaxDiskSharePercent < 1 || c.Upload.MaxDiskSharePercent > 100 {
		bad("upload.max_disk_share_percent must be from 1 to 100")
	}
	if c.Links.DefaultTTL <= 0 || c.Links.MaxTTL <= 0 || c.Links.DefaultTTL > c.Links.MaxTTL {
		bad("links.default_ttl and links.max_ttl must be positive, and default_ttl at most max_ttl")
	}
	if c.Tokens.MaxTTL < Duration(time.Minute) {
		bad("tokens.max_ttl must be at least one minute")
	}
	for _, o := range c.CORS.AllowedOrigins {
		if err := checkOrigin(o); err != nil {
			bad("cors.allowed_origins: %v", err)
		}
	}
	if c.Users.VerificationTTL <= 0 || c.Users.ResetTTL <= 0 {
		bad("users.verification_ttl and users.reset_ttl must be positive")
	}
	if c.Users.SignInLinkTTL <= 0 || c.Users.SignInLinkTTL > Duration(10*time.Minute) {
		bad("users.sign_in_link_ttl must be positive and at most 10m")
	}
	if c.Users.DeviceConfirmTTL <= 0 || c.Users.DeviceConfirmTTL > Duration(30*time.Minute) {
		bad("users.device_confirm_ttl must be positive and at most 30m")
	}
	for name, tmpl := range map[string]string{
		"users.verify_url_template":         c.Users.VerifyURLTemplate,
		"users.reset_url_template":          c.Users.ResetURLTemplate,
		"users.email_change_url_template":   c.Users.EmailChangeURLTemplate,
		"users.sign_in_url_template":        c.Users.SignInURLTemplate,
		"users.device_confirm_url_template": c.Users.DeviceConfirmURLTemplate,
	} {
		if !strings.Contains(tmpl, "{token}") {
			bad("%s must contain {token}", name)
		}
	}
	// A sign-in link signs a browser in, and so does a device confirmation, so their tokens must stay in the fragment,
	// which never reaches a server, a proxy's log or a Referer header.
	for name, tmpl := range map[string]string{
		"users.sign_in_url_template":        c.Users.SignInURLTemplate,
		"users.device_confirm_url_template": c.Users.DeviceConfirmURLTemplate,
	} {
		if i := strings.Index(tmpl, "{token}"); i >= 0 && !strings.Contains(tmpl[:i], "#") {
			bad("%s must carry {token} in the fragment, after #", name)
		}
	}
	if c.Sessions.IdleTimeout <= 0 || c.Sessions.MaxAge <= 0 {
		bad("sessions.idle_timeout and sessions.max_age must be positive")
	}
	if c.Download.Timeout <= 0 || c.Download.MinRate <= 0 {
		bad("download.timeout and download.min_rate must be positive")
	}
	if c.Sessions.DeviceCookieLifetime <= 0 {
		bad("sessions.device_cookie_lifetime must be positive")
	}
	if c.Sessions.AdminIdleTimeout <= 0 || c.Sessions.AdminMaxAge <= 0 {
		bad("sessions.admin_idle_timeout and sessions.admin_max_age must be positive")
	}
	if s := c.Sessions.ShortIdleTimeout; s < 0 || s > c.Sessions.IdleTimeout {
		bad("sessions.short_idle_timeout must be unset or positive, and not more than sessions.idle_timeout")
	}
	if c.RateLimit.CappedPerEmailPerHour <= 0 {
		bad("rate_limit.capped_per_email_per_hour must be at least 1")
	}
	if c.RateLimit.CappedEmailPerHour <= 0 {
		bad("rate_limit.capped_email_per_hour must be at least 1")
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
	if err := c.UI.Brand.validate(); err != nil {
		bad("ui.brand: %v", err)
	}
	return errors.Join(errs...)
}

// checkOffice validates the office block. The discovery URL must be https, name a host, and have exactly the
// discovery path; the WOPI URL must be https with no path. Both are checked even when office is off, so a typo is
// found at start-up rather than when someone turns the editor on.
func checkOffice(o OfficeConfig, bad func(string, ...any)) {
	if o.Enabled && o.DiscoveryURL == "" {
		bad("office.discovery_url is required when office.enabled is true")
	}
	if o.DiscoveryURL != "" {
		u, err := url.Parse(o.DiscoveryURL)
		switch {
		case err != nil:
			bad("office.discovery_url: %v", err)
		case u.Scheme != "https":
			bad("office.discovery_url must be https")
		case u.Host == "":
			bad("office.discovery_url has no host")
		case u.Path != "/hosting/discovery":
			bad("office.discovery_url must end in /hosting/discovery")
		case u.RawQuery != "" || u.Fragment != "" || u.User != nil:
			bad("office.discovery_url must have no query, fragment or user information")
		}
	}
	if o.WOPIURL != "" {
		if err := checkBaseURL(o.WOPIURL); err != nil {
			bad("office.wopi_url: %v", err)
		} else if !strings.HasPrefix(o.WOPIURL, "https://") {
			bad("office.wopi_url must be https")
		}
	}
	// The WOPI specification fixes a lock at 30 minutes, and an editor may assume it, so a longer lock is refused.
	if o.LockMinutes < 1 || o.LockMinutes > 30 {
		bad("office.lock_minutes must be between 1 and 30, the WOPI lock limit")
	}
	if o.TokenMinutes < 1 || o.TokenMinutes > 1440 {
		bad("office.token_minutes must be between 1 and 1440")
	}
	if o.MaxFileBytes <= 0 {
		bad("office.max_file_bytes must be positive")
	}
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

// checkOrigin accepts an exact origin: http or https, a host, and an optional port. A path, a
// query, a fragment, user information and wildcards are refused, because a browser sends only
// the bare origin and a looser entry would match nothing, or match too much.
func checkOrigin(s string) error {
	if s == "" {
		return errors.New("an origin is empty")
	}
	if strings.Contains(s, "*") {
		return fmt.Errorf("%q: wildcards are not allowed; list each origin", s)
	}
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("%q: %w", s, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%q must start with http:// or https://", s)
	}
	if u.Host == "" {
		return fmt.Errorf("%q has no host", s)
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil || strings.HasSuffix(s, "?") || strings.HasSuffix(s, "#") {
		return fmt.Errorf("%q must be the origin only: scheme, host and port, with no path, query or fragment", s)
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

var accentPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// Artwork reports whether the installation shows the SelfCloud artwork: its name is the default one and it has no logo file. The
// artwork is one picture with the name inside it, so an installation that names itself or sets a logo shows its own (decision
// 109). An installation that picks the default name by hand gets the artwork too.
func (b BrandConfig) Artwork() bool {
	return b.Name == DefaultBrandName && b.LogoFile == ""
}

// validate checks the brand. The accent is only checked for form: MASTER.md asks the UI to refuse
// a colour that fails contrast, but black or white text always reaches 4.5:1 on any colour, so that
// rule could never refuse anything and is not enforced.
func (b BrandConfig) validate() error {
	n := utf8.RuneCountInString(b.Name)
	if n == 0 {
		return errors.New("name is empty")
	}
	if n > 64 {
		return errors.New("name is longer than 64 characters")
	}
	for _, r := range b.Name {
		if unicode.IsControl(r) {
			return errors.New("name contains a control character")
		}
	}
	if !accentPattern.MatchString(b.Accent) {
		return fmt.Errorf("accent %q is not a colour as #rrggbb", b.Accent)
	}
	colours := []struct{ key, value string }{
		{"palette.light.accent", b.Palette.Light.Accent},
		{"palette.light.accent_fill", b.Palette.Light.AccentFill},
		{"palette.light.accent_fill_hover", b.Palette.Light.AccentFillHover},
		{"palette.light.accent_fill_active", b.Palette.Light.AccentFillActive},
		{"palette.light.accent_text", b.Palette.Light.AccentText},
		{"palette.light.accent_soft", b.Palette.Light.AccentSoft},
		{"palette.light.accent_border", b.Palette.Light.AccentBorder},
		{"palette.dark.accent", b.Palette.Dark.Accent},
		{"palette.dark.accent_fill", b.Palette.Dark.AccentFill},
		{"palette.dark.accent_fill_hover", b.Palette.Dark.AccentFillHover},
		{"palette.dark.accent_fill_active", b.Palette.Dark.AccentFillActive},
		{"palette.dark.accent_text", b.Palette.Dark.AccentText},
		{"palette.dark.accent_soft", b.Palette.Dark.AccentSoft},
		{"palette.dark.accent_border", b.Palette.Dark.AccentBorder},
		{"sidebar.bg", b.Sidebar.Bg},
		{"sidebar.text", b.Sidebar.Text},
		{"sidebar.caption", b.Sidebar.Caption},
		{"sidebar.active", b.Sidebar.Active},
		{"sidebar.on_active", b.Sidebar.OnActive},
	}
	for _, c := range colours {
		// An empty token keeps the default; any other value must be a colour.
		if c.value != "" && !accentPattern.MatchString(c.value) {
			return fmt.Errorf("%s %q is not a colour as #rrggbb", c.key, c.value)
		}
	}
	if b.LogoFile != "" {
		switch strings.ToLower(filepath.Ext(b.LogoFile)) {
		case ".png", ".svg":
		default:
			return fmt.Errorf("logo_file %q must be a .png or .svg file", b.LogoFile)
		}
	}
	if b.IconFile != "" {
		switch strings.ToLower(filepath.Ext(b.IconFile)) {
		case ".png", ".svg":
		default:
			return fmt.Errorf("icon_file %q must be a .png or .svg file", b.IconFile)
		}
	}
	return nil
}
