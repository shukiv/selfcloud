// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The settings an installation admin may change while the server runs (decision 139). A key's value comes from, in
// order: the config file or the environment, which lock it; the settings table, which the settings API writes; the
// built-in default, which does not lock. A value from the table is parsed with the rules the file's values are parsed
// with, held to the key's floor and cap below, and the whole configuration is then checked with Validate.

// Source is where a setting's value comes from.
type Source string

// The sources of a value, as the settings API names them.
const (
	SourceDefault  Source = "default"
	SourceFile     Source = "file"
	SourceEnv      Source = "env"
	SourceSettings Source = "settings"
)

// SettingType is the JSON shape of a setting's value in the settings API.
type SettingType string

// The setting types. A duration is a string such as "48h"; a byte size is a string such as "64MiB", and may also be sent
// as a whole number of bytes.
const (
	TypeBool     SettingType = "bool"
	TypeInt      SettingType = "int"
	TypeDuration SettingType = "duration"
	TypeBytes    SettingType = "bytes"
	TypeString   SettingType = "string"
)

// settingDef is one setting the settings API shows. A restart setting is shown read-only, because the server reads it
// once at start. A value set through the settings table is held to min and max, in the field's own unit (bytes,
// nanoseconds or the number), on top of Validate; with zero set, 0 is allowed too, and means no limit, as in the file.
// A setting with min and max both 0 has no bounds of its own. maxJSON is the longest value the API reads for it, in bytes
// of JSON; 0 is MaxSettingValue.
type settingDef struct {
	key      string
	typ      SettingType
	restart  bool
	min, max int64
	zero     bool
	maxJSON  int
}

// maxNameJSON is the longest JSON the settings API reads for ui.brand.name. The file allows 64 characters, and a client
// may write each as a \u escape pair of 12 bytes: 770 bytes in all, quotes included. No refusal of the name repeats it.
const maxNameJSON = 1024

const (
	minute = int64(time.Minute)
	hour   = int64(time.Hour)
	day    = 24 * hour
)

// settingDefs are the settings, sorted by key. "Versions kept" is versions.max_count; versions.max_kept, the versions
// kept for ever, stays in the file.
var settingDefs = []settingDef{
	{key: "admin.tenant_access", typ: TypeBool},
	{key: "links.default_ttl", typ: TypeDuration, min: minute, max: 7 * day},
	{key: "links.max_ttl", typ: TypeDuration, min: minute, max: 7 * day},
	{key: "office.discovery_url", typ: TypeString, restart: true},
	{key: "office.enabled", typ: TypeBool, restart: true},
	{key: "office.lock_minutes", typ: TypeInt, min: 1, max: 30},
	{key: "office.max_file_bytes", typ: TypeBytes, min: int64(MiB), max: int64(4 * GiB)},
	{key: "search.enabled", typ: TypeBool},
	{key: "search.office_text", typ: TypeBool},
	// The admin pair can only shorten decision 84's hardening (12 hours idle, 7 days in all); the file stays free.
	{key: "sessions.admin_idle_timeout", typ: TypeDuration, min: 5 * minute, max: 12 * hour},
	{key: "sessions.admin_max_age", typ: TypeDuration, min: hour, max: 7 * day},
	{key: "sessions.idle_timeout", typ: TypeDuration, min: 15 * minute, max: 30 * day},
	{key: "sessions.max_age", typ: TypeDuration, min: hour, max: 90 * day},
	{key: "sharing.max_expiry", typ: TypeDuration, min: hour, max: 3650 * day, zero: true},
	// The database shares the data disk: 256 MiB keeps room for its writes (sessions, the audit, the journal), so uploads
	// and readiness refuse before the disk is full. The file may still set less.
	{key: "storage.min_free", typ: TypeBytes, min: 256 * int64(MiB), max: int64(TiB)},
	{key: "trash.retention", typ: TypeDuration, min: day, max: 3650 * day},
	// The brand (decision 141): the file's own rules check it (BrandConfig.validate), with no bounds of its own. A colour
	// is #rrggbb, and a palette or sidebar token may be empty, which keeps the default look for it.
	{key: "ui.brand.accent", typ: TypeString},
	{key: "ui.brand.name", typ: TypeString, maxJSON: maxNameJSON},
	{key: "ui.brand.palette.dark.accent", typ: TypeString},
	{key: "ui.brand.palette.dark.accent_border", typ: TypeString},
	{key: "ui.brand.palette.dark.accent_fill", typ: TypeString},
	{key: "ui.brand.palette.dark.accent_fill_active", typ: TypeString},
	{key: "ui.brand.palette.dark.accent_fill_hover", typ: TypeString},
	{key: "ui.brand.palette.dark.accent_soft", typ: TypeString},
	{key: "ui.brand.palette.dark.accent_text", typ: TypeString},
	{key: "ui.brand.palette.light.accent", typ: TypeString},
	{key: "ui.brand.palette.light.accent_border", typ: TypeString},
	{key: "ui.brand.palette.light.accent_fill", typ: TypeString},
	{key: "ui.brand.palette.light.accent_fill_active", typ: TypeString},
	{key: "ui.brand.palette.light.accent_fill_hover", typ: TypeString},
	{key: "ui.brand.palette.light.accent_soft", typ: TypeString},
	{key: "ui.brand.palette.light.accent_text", typ: TypeString},
	{key: "ui.brand.sidebar.active", typ: TypeString},
	{key: "ui.brand.sidebar.bg", typ: TypeString},
	{key: "ui.brand.sidebar.caption", typ: TypeString},
	{key: "ui.brand.sidebar.on_active", typ: TypeString},
	{key: "ui.brand.sidebar.text", typ: TypeString},
	{key: "upload.max_chunk_size", typ: TypeBytes, min: int64(MiB), max: int64(4 * GiB)},
	{key: "upload.max_file_size", typ: TypeBytes, min: int64(MiB), max: int64(16 * TiB)},
	{key: "upload.max_small_file_size", typ: TypeBytes, min: 64 * int64(KiB), max: int64(GiB)},
	{key: "users.reset_ttl", typ: TypeDuration, min: 5 * minute, max: day},
	{key: "users.verification_ttl", typ: TypeDuration, min: 15 * minute, max: 30 * day},
	{key: "versions.max_age", typ: TypeDuration, min: day, max: 3650 * day, zero: true},
	{key: "versions.max_count", typ: TypeInt, min: 1, max: 10_000, zero: true},
}

// factKeys are the read-only facts the settings API shows: infrastructure the file owns. No secret is among them: not
// the mail password or user, not the admin key file, not the signing key files.
var factKeys = []string{
	"client_ip_header", "content_url", "cors.allowed_origins", "data_dir", "listen", "log.format", "log.level",
	"mail.from", "mail.smtp_host", "mail.smtp_port", "mail.smtp_tls", "metrics_listen", "metrics_on_main", "public_url",
	"rate_limit.capped_email_per_hour", "rate_limit.capped_per_email_per_hour", "rate_limit.per_email_per_minute",
	"rate_limit.per_ip_per_minute", "store.driver", "store.path", "trusted_proxies", "ui.enabled",
	"webhooks.allow_http", "webhooks.allow_networks",
}

// lookupDef returns the setting named key.
func lookupDef(key string) (settingDef, bool) {
	i, ok := slices.BinarySearchFunc(settingDefs, key, func(d settingDef, k string) int { return strings.Compare(d.key, k) })
	if !ok {
		return settingDef{}, false
	}
	return settingDefs[i], true
}

// Setting is one setting as the settings API shows it. Value and Default are a bool, an int64 or a string, by Type.
type Setting struct {
	Key     string
	Type    SettingType
	Value   any
	Default any
	Source  Source
	Locked  bool
	Restart bool
}

// Fact is a read-only value of the configuration that the settings API shows. Value is a string, a bool, an int64 or a
// list of strings.
type Fact struct {
	Key    string
	Value  any
	Source Source
}

// Source reports where the value of the key at the YAML path comes from.
func (c *Config) Source(key string) Source {
	if s, ok := c.present[key]; ok {
		return s
	}
	if _, ok := c.stored[key]; ok {
		return SourceSettings
	}
	return SourceDefault
}

// locked reports whether the settings API may not change d: the file or the environment writes it, or it needs a restart.
func (c *Config) locked(d settingDef) bool {
	_, written := c.present[d.key]
	return written || d.restart
}

// Settings lists the settings, sorted by key.
func (c *Config) Settings() []Setting {
	cur, def := reflect.ValueOf(c).Elem(), reflect.ValueOf(Defaults()).Elem()
	out := make([]Setting, 0, len(settingDefs))
	for _, d := range settingDefs {
		out = append(out, Setting{Key: d.key, Type: d.typ, Value: jsonValue(mustField(cur, d), d.typ),
			Default: jsonValue(mustField(def, d), d.typ), Source: c.Source(d.key), Locked: c.locked(d), Restart: d.restart})
	}
	return out
}

// Facts lists the read-only facts, sorted by key.
func (c *Config) Facts() []Fact {
	cur := reflect.ValueOf(c).Elem()
	out := make([]Fact, 0, len(factKeys))
	for _, key := range factKeys {
		fv, ok := fieldByPath(cur, key)
		if !ok {
			panic("config: fact " + key + " names no field")
		}
		var v any
		switch fv.Kind() {
		case reflect.String:
			v = fv.String()
		case reflect.Bool:
			v = fv.Bool()
		case reflect.Int, reflect.Int64:
			v = fv.Int()
		case reflect.Slice:
			v = append([]string{}, fv.Interface().([]string)...)
		default:
			panic("config: fact " + key + " has an unsupported type")
		}
		out = append(out, Fact{Key: key, Value: v, Source: c.Source(key)})
	}
	return out
}

// StoredText returns the text the settings table holds for key, when this configuration applies a row for it.
func (c *Config) StoredText(key string) (string, bool) {
	t, ok := c.stored[key]
	return t, ok
}

// MaxSettingValue is the longest JSON value of a setting, in bytes, but for the brand's name (maxNameJSON). The longest
// the others need is a size in bytes, 20 digits at most.
const MaxSettingValue = 64

// ValueText returns the value of the setting key as the text the table would hold for it, or "" for an unknown key.
func (c *Config) ValueText(key string) string {
	d, ok := lookupDef(key)
	if !ok {
		return ""
	}
	return valueText(mustField(reflect.ValueOf(c).Elem(), d), d.typ)
}

// SettingText turns a JSON value from the settings API into the text the setting is stored as. The JSON type must be the
// setting's: a string is not a bool, and a fraction, an exponent or an overflow is not an integer. The text is parsed and
// bounded later, by Change. A value longer than MaxSettingValue bytes (maxNameJSON for the name) is refused before it is
// read, so no error repeats a long input.
func SettingText(key string, raw []byte) (string, error) {
	d, ok := lookupDef(key)
	if !ok {
		return "", fmt.Errorf("config: %s is not a setting", key)
	}
	limit := MaxSettingValue
	if d.maxJSON > 0 {
		limit = d.maxJSON
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) > limit {
		return "", fmt.Errorf("config: %s is longer than %d bytes", key, limit)
	}
	s := string(raw)
	bad := func(want string) error { return fmt.Errorf("config: %s must be %s", key, want) }
	switch d.typ {
	case TypeBool:
		if s != "true" && s != "false" {
			return "", bad("true or false")
		}
		return s, nil
	case TypeInt:
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return "", bad("a whole number")
		}
		return strconv.FormatInt(n, 10), nil
	case TypeDuration:
		var text string
		if !strings.HasPrefix(s, `"`) || json.Unmarshal(raw, &text) != nil {
			return "", bad(`a duration as a string, such as "48h"`)
		}
		return text, nil
	case TypeBytes:
		if strings.HasPrefix(s, `"`) {
			var text string
			if json.Unmarshal(raw, &text) != nil {
				return "", bad(`a size as a string, such as "64MiB", or a whole number of bytes`)
			}
			return text, nil
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 0 {
			return "", bad(`a size as a string, such as "64MiB", or a whole number of bytes`)
		}
		return strconv.FormatInt(n, 10), nil
	case TypeString:
		if d.restart {
			break
		}
		var text string
		if !strings.HasPrefix(s, `"`) || json.Unmarshal(raw, &text) != nil {
			return "", bad("a string")
		}
		return text, nil
	}
	return "", fmt.Errorf("config: %s is read at start and is set in the config file only", key)
}

// The settings table's rows of the pictures an admin uploads (decision 141). They are not settings: the picture routes
// write them, the server reads them at start, and the settings API cannot name them. Each holds the picture's type and
// the SHA-256 of its bytes. The config file locks a picture by writing the key that names its file, which the API cannot
// name either.
const (
	LogoPicture = "ui.brand.logo"
	IconPicture = "ui.brand.icon"
)

// pictureFiles names, for each picture's row, the config key of its file.
var pictureFiles = map[string]string{LogoPicture: "ui.brand.logo_file", IconPicture: "ui.brand.icon_file"}

// IsPicture reports whether key is the settings table's row of an uploaded picture.
func IsPicture(key string) bool {
	_, ok := pictureFiles[key]
	return ok
}

// PictureLocked reports whether the config file or the environment writes the file key of the picture named key, even
// empty, which locks the picture against the API as decision 139 locks a setting.
func (c *Config) PictureLocked(key string) bool {
	file, ok := pictureFiles[key]
	if !ok {
		return false
	}
	_, written := c.present[file]
	return written
}

// Problem is one reason a change is refused. Keys are the changed keys its message names.
type Problem struct {
	Keys    []string
	Message string
}

// ChangeError refuses a change to the settings. Unknown names keys that are not settings; Locked names keys the file or
// the environment writes, or that need a restart; Problems are the values that are not valid.
type ChangeError struct {
	Unknown  []string
	Locked   []string
	Problems []Problem
}

func (e *ChangeError) Error() string {
	var parts []string
	if len(e.Unknown) > 0 {
		parts = append(parts, "config: not a setting: "+strings.Join(e.Unknown, ", "))
	}
	if len(e.Locked) > 0 {
		parts = append(parts, "config: set in the config file or the environment, or read at start: "+strings.Join(e.Locked, ", "))
	}
	for _, p := range e.Problems {
		parts = append(parts, p.Message)
	}
	return strings.Join(parts, "; ")
}

// Keys returns every key the problems name, sorted, each once.
func (e *ChangeError) Keys() []string {
	var keys []string
	for _, p := range e.Problems {
		keys = append(keys, p.Keys...)
	}
	slices.Sort(keys)
	return slices.Compact(keys)
}

// CheckKeys refuses, as a *ChangeError, the keys that are not settings and the keys the settings API may not change,
// each list sorted. It returns nil when every key may be changed.
func (c *Config) CheckKeys(keys []string) error {
	ce := &ChangeError{}
	for _, k := range slices.Sorted(slices.Values(keys)) {
		d, ok := lookupDef(k)
		switch {
		case !ok:
			ce.Unknown = append(ce.Unknown, k)
		case c.locked(d):
			ce.Locked = append(ce.Locked, k)
		}
	}
	if len(ce.Unknown) > 0 || len(ce.Locked) > 0 {
		return ce
	}
	return nil
}

// Change returns a copy of c with the settings in set stored (each as its text) and those in reset removed from the
// table, so they fall back to their defaults. The change is all or nothing: on any error c is unchanged, no copy is
// returned, and the error is a *ChangeError that names every key it refuses.
func (c *Config) Change(set map[string]string, reset []string) (*Config, error) {
	changed := slices.Sorted(maps.Keys(set))
	for _, k := range reset {
		if !slices.Contains(changed, k) {
			changed = append(changed, k)
		}
	}
	slices.Sort(changed)
	if err := c.CheckKeys(changed); err != nil {
		return nil, err
	}
	ce := &ChangeError{}
	rows := maps.Clone(c.stored)
	if rows == nil {
		rows = map[string]string{}
	}
	for _, k := range reset {
		delete(rows, k)
	}
	maps.Copy(rows, set)
	next, problems := c.build(rows, changed)
	if len(problems) > 0 {
		ce.Problems = problems
		return nil, ce
	}
	return next, nil
}

// StoredApplied reports whether ApplyStored has run on c, or on the configuration c was changed from.
func (c *Config) StoredApplied() bool { return c.applied }

// ApplyStored applies the settings table's rows to c, in place. A row for an unknown key, for a key that needs a
// restart, or for a key the file or the environment writes is not applied, and skip is told why. The rows that are
// valid together are applied together; if they are not, each is tried alone in key order, and one that fails is skipped
// and named. A row never stops the server: the file can always override it. The pictures' rows are left out without a
// word: the server reads them itself (decision 141).
func (c *Config) ApplyStored(rows map[string]string, skip func(key, reason string)) {
	defer func() { c.applied = true }()
	if skip == nil {
		skip = func(string, string) {}
	}
	usable := map[string]string{}
	for _, k := range slices.Sorted(maps.Keys(rows)) {
		d, ok := lookupDef(k)
		switch {
		case IsPicture(k):
		case !ok:
			skip(k, "not a setting of this version")
		case d.restart:
			skip(k, "read at start, so it is set in the config file only")
		case c.locked(d):
			skip(k, "the config file or the environment sets it, and wins")
		default:
			usable[k] = rows[k]
		}
	}
	keys := slices.Sorted(maps.Keys(usable))
	if next, problems := c.build(usable, keys); len(problems) == 0 {
		*c = *next
		return
	}
	cur, problems := c.build(nil, nil)
	if len(problems) > 0 {
		for _, k := range keys {
			skip(k, problems[0].Message)
		}
		return
	}
	for _, k := range keys {
		try := maps.Clone(cur.stored)
		try[k] = usable[k]
		next, problems := c.build(try, []string{k})
		if len(problems) > 0 {
			msgs := make([]string, len(problems))
			for i, p := range problems {
				msgs[i] = p.Message
			}
			skip(k, strings.Join(msgs, "; "))
			continue
		}
		cur = next
	}
	*c = *cur
}

// build returns c with exactly rows applied from the table: each key c stores now goes back to its default first. Each
// row is parsed and bounded, and the result is checked with Validate. changed are the keys a Validate message is matched
// against, to name them in its problem.
func (c *Config) build(rows map[string]string, changed []string) (*Config, []Problem) {
	next := *c
	next.stored = map[string]string{}
	cur, def := reflect.ValueOf(&next).Elem(), reflect.ValueOf(Defaults()).Elem()
	for k := range c.stored {
		if d, ok := lookupDef(k); ok {
			mustField(cur, d).Set(mustField(def, d))
		}
	}
	var problems []Problem
	for _, k := range slices.Sorted(maps.Keys(rows)) {
		d, ok := lookupDef(k)
		if !ok {
			problems = append(problems, Problem{Keys: []string{k}, Message: "config: " + k + " is not a setting"})
			continue
		}
		fv := mustField(cur, d)
		v := reflect.New(fv.Type()).Elem()
		if err := setFromString(v, rows[k]); err != nil {
			problems = append(problems, Problem{Keys: []string{k}, Message: fmt.Sprintf("config: %s: %v", k, err)})
			continue
		}
		if err := d.check(v); err != nil {
			problems = append(problems, Problem{Keys: []string{k}, Message: err.Error()})
			continue
		}
		fv.Set(v)
		next.stored[k] = valueText(v, d.typ)
	}
	if err := next.Validate(); err != nil {
		for _, e := range unjoin(err) {
			problems = append(problems, Problem{Keys: keysNamed(e.Error(), changed), Message: e.Error()})
		}
	}
	if len(problems) > 0 {
		return nil, problems
	}
	return &next, nil
}

// check holds a value set through the settings to d's floor and cap.
func (d settingDef) check(v reflect.Value) error {
	if d.min == 0 && d.max == 0 {
		return nil
	}
	n := v.Int()
	if d.zero && n == 0 {
		return nil
	}
	if n >= d.min && n <= d.max {
		return nil
	}
	lo, hi := d.unit(d.min), d.unit(d.max)
	if d.zero {
		return fmt.Errorf("config: %s must be 0, for no limit, or from %s to %s", d.key, lo, hi)
	}
	return fmt.Errorf("config: %s must be from %s to %s", d.key, lo, hi)
}

// unit writes n in d's unit.
func (d settingDef) unit(n int64) string {
	switch d.typ {
	case TypeDuration:
		return Duration(n).String()
	case TypeBytes:
		return ByteSize(n).String()
	}
	return strconv.FormatInt(n, 10)
}

// keysNamed returns the keys of changed that msg names as whole keys.
func keysNamed(msg string, changed []string) []string {
	var out []string
	for _, k := range changed {
		for i := 0; ; {
			j := strings.Index(msg[i:], k)
			if j < 0 {
				break
			}
			start, end := i+j, i+j+len(k)
			if (start == 0 || !keyByte(msg[start-1])) && (end == len(msg) || !keyByte(msg[end])) {
				out = append(out, k)
				break
			}
			i = end
		}
	}
	return out
}

// keyByte reports whether b may be part of a YAML path.
func keyByte(b byte) bool {
	return b == '_' || b == '.' || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}

// unjoin returns the errors an errors.Join made, or err alone.
func unjoin(err error) []error {
	var j interface{ Unwrap() []error }
	if errors.As(err, &j) {
		return j.Unwrap()
	}
	return []error{err}
}

// jsonValue is v as the settings API shows a value of type t.
func jsonValue(v reflect.Value, t SettingType) any {
	switch t {
	case TypeBool:
		return v.Bool()
	case TypeInt:
		return v.Int()
	case TypeString:
		return v.String()
	}
	return valueText(v, t)
}

// valueText is v as the text the settings table holds for a value of type t.
func valueText(v reflect.Value, t SettingType) string {
	switch t {
	case TypeBool:
		return strconv.FormatBool(v.Bool())
	case TypeInt:
		return strconv.FormatInt(v.Int(), 10)
	case TypeDuration:
		return Duration(v.Int()).String()
	case TypeBytes:
		return ByteSize(v.Int()).String()
	}
	return v.String()
}

// mustField returns the field d names in the Config value v, and panics when the field is missing or of a kind d's type
// cannot hold: a programming error the tests find.
func mustField(v reflect.Value, d settingDef) reflect.Value {
	fv, ok := fieldByPath(v, d.key)
	if !ok {
		panic("config: setting " + d.key + " names no field")
	}
	var fits bool
	switch d.typ {
	case TypeBool:
		fits = fv.Kind() == reflect.Bool
	case TypeInt:
		fits = fv.Type() == reflect.TypeFor[int]() || fv.Type() == reflect.TypeFor[int64]()
	case TypeDuration:
		fits = fv.Type() == reflect.TypeFor[Duration]()
	case TypeBytes:
		fits = fv.Type() == reflect.TypeFor[ByteSize]()
	case TypeString:
		fits = fv.Kind() == reflect.String
	}
	if !fits {
		panic("config: setting " + d.key + " has type " + string(d.typ) + " but its field is " + fv.Type().String())
	}
	return fv
}

// fieldByPath returns the field of the struct value v at the YAML path key.
func fieldByPath(v reflect.Value, key string) (reflect.Value, bool) {
	for part := range strings.SplitSeq(key, ".") {
		if v.Kind() != reflect.Struct {
			return reflect.Value{}, false
		}
		t := v.Type()
		found := false
		for i := range t.NumField() {
			if tag, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ","); tag == part && tag != "-" {
				v, found = v.Field(i), true
				break
			}
		}
		if !found {
			return reflect.Value{}, false
		}
	}
	return v, true
}
