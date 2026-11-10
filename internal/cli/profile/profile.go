// SPDX-License-Identifier: AGPL-3.0-or-later

// Package profile keeps the CLI's profiles: named servers with their credentials, in one private file. It also reads a
// credential from a file or a pipe, and says what kind of key it is. It never prints a credential.
package profile

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
)

// Kinds of credential a profile can hold. The kind comes from the credential's prefix.
const (
	KindAdmin   = "admin"   // an installation admin key, sc_admin_
	KindAccount = "account" // an account key, sc_acct_
	KindDevice  = "device"  // a device token from device sign-in, scd_ (cli4)
)

const (
	fileVersion = 1
	fileName    = "profiles.json"
	// maxCredential bounds a credential read from a file or a pipe. A key is under 100 bytes.
	maxCredential = 4096
)

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// Profile is one named server. Token is the credential; the file is private, so it is stored as it is.
type Profile struct {
	URL    string `json:"url"`
	Kind   string `json:"kind"`
	Token  string `json:"token"`
	CAFile string `json:"ca_file,omitempty"`
}

// File is the whole profiles file.
type File struct {
	Version  int                `json:"version"`
	Current  string             `json:"current,omitempty"`
	Profiles map[string]Profile `json:"profiles"`
}

// Store reads and writes the profiles file in one directory.
type Store struct {
	Dir string

	// beforeRename is a test seam: a non-nil error from it stands for a crash between the write and the rename.
	beforeRename func() error
}

// Open returns the store for dir. Nothing is read or created until it is used.
func Open(dir string) *Store { return &Store{Dir: dir} }

// DefaultDir is SELFCLOUD_PROFILES_DIR when it is set, and otherwise selfcloud under the user's configuration directory.
func DefaultDir() (string, error) {
	if d := os.Getenv("SELFCLOUD_PROFILES_DIR"); d != "" {
		return d, nil
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", errors.New("no profiles directory: set SELFCLOUD_PROFILES_DIR")
	}
	return filepath.Join(cfg, "selfcloud"), nil
}

func (s *Store) path() string { return filepath.Join(s.Dir, fileName) }

// ValidName checks a profile name: lower-case letters, digits, "-" and "_", starting with a letter or digit, up to 32.
func ValidName(name string) error {
	if !nameRE.MatchString(name) {
		return exitcode.Usagef("a profile name is 1 to 32 lower-case letters, digits, '-' or '_', starting with a letter or digit")
	}
	return nil
}

// KindOf returns the kind of a credential by its prefix. It refuses anything that is not a key or a device token, and
// any value with a space or a control character. The value is never repeated in the refusal.
func KindOf(tok string) (string, error) {
	if len(tok) > maxCredential {
		return "", exitcode.Usagef("the credential is too long")
	}
	for i := 0; i < len(tok); i++ {
		if tok[i] <= ' ' || tok[i] == 0x7f {
			return "", exitcode.Usagef("the credential must be one line with no spaces")
		}
	}
	switch {
	case strings.HasPrefix(tok, auth.AdminKeyPrefix) && len(tok) > len(auth.AdminKeyPrefix):
		return KindAdmin, nil
	case strings.HasPrefix(tok, auth.AccountKeyPrefix) && len(tok) > len(auth.AccountKeyPrefix):
		return KindAccount, nil
	case strings.HasPrefix(tok, deviceTokenPrefix) && len(tok) > len(deviceTokenPrefix):
		return KindDevice, nil
	}
	return "", exitcode.Usagef("the credential is not an admin key (sc_admin_), an account key (sc_acct_) or a device token (scd_)")
}

// deviceTokenPrefix starts a device token. internal/auth does not define it yet; cli4 adds the kind.
const deviceTokenPrefix = "scd_"

func validKind(k string) bool { return k == KindAdmin || k == KindAccount || k == KindDevice }

// Load reads the file. A missing file is no profiles. A file or directory that any group or other user can read or
// write is refused, and the message gives the chmod that fixes it. A symbolic link is refused.
func (s *Store) Load() (File, error) {
	empty := File{Version: fileVersion, Profiles: map[string]Profile{}}
	if err := s.checkDir(); err != nil {
		return File{}, err
	}
	path := s.path()
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return File{}, fmt.Errorf("cannot read the profiles file: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return File{}, exitcode.Usagef("%s is not a regular file; the profiles file must be one", path)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		return File{}, exitcode.Usagef("%s has permissions %04o; the profiles file holds a credential, so only you may "+
			"read it: run chmod 600 %s", path, perm, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("cannot read the profiles file: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f File
	if err := dec.Decode(&f); err != nil {
		return File{}, exitcode.Usagef("%s is not a valid profiles file", path)
	}
	if dec.More() {
		return File{}, exitcode.Usagef("%s is not a valid profiles file", path)
	}
	if f.Version != fileVersion {
		return File{}, exitcode.Usagef("%s has version %d; this CLI reads version %d", path, f.Version, fileVersion)
	}
	if f.Profiles == nil {
		f.Profiles = map[string]Profile{}
	}
	for name, p := range f.Profiles {
		if err := ValidName(name); err != nil {
			return File{}, exitcode.Usagef("%s holds a profile name that is not valid", path)
		}
		if !validKind(p.Kind) {
			return File{}, exitcode.Usagef("%s holds a profile of an unknown kind", path)
		}
	}
	return f, nil
}

// Save writes the file atomically: a random temporary file in the same directory, created with O_EXCL and mode 0600, is
// written, synced and renamed over the old file. A failure before the rename leaves the old file whole.
func (s *Store) Save(f File) error {
	f.Version = fileVersion
	if f.Profiles == nil {
		f.Profiles = map[string]Profile{}
	}
	if err := s.ensureDir(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	suffix, err := randomHex(8)
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.Dir, ".profiles-"+suffix+".tmp")
	w, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	if err == nil {
		err = w.Sync()
	}
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err == nil && s.beforeRename != nil {
		err = s.beforeRename()
	}
	if err == nil {
		err = os.Rename(tmp, s.path())
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// checkDir refuses a profiles directory that is not a directory or that any other user can read or enter.
func (s *Store) checkDir() error {
	fi, err := os.Stat(s.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot read the profiles directory: %w", err)
	}
	if !fi.IsDir() {
		return exitcode.Usagef("%s is not a directory", s.Dir)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		return exitcode.Usagef("%s has permissions %04o; the profiles directory must be private: run chmod 700 %s",
			s.Dir, perm, s.Dir)
	}
	return nil
}

// ensureDir creates the directory with mode 0700 when it is missing, and checks it when it is there.
func (s *Store) ensureDir() error {
	if err := s.checkDir(); err != nil {
		return err
	}
	return os.MkdirAll(s.Dir, 0o700)
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ReadCredentialFile reads a credential from a file the caller owns. The file must be regular and private (no group or
// other bits), and no larger than maxCredential.
func ReadCredentialFile(path string) (string, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return "", exitcode.Usagef("cannot read the token file")
	}
	if !fi.Mode().IsRegular() {
		return "", exitcode.Usagef("the token file must be a regular file")
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		return "", exitcode.Usagef("the token file has permissions %04o; run chmod 600 %s", perm, path)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", exitcode.Usagef("cannot read the token file")
	}
	defer f.Close()
	return ReadCredential(f)
}

// ReadCredential reads one line from r, which is a file or a pipe: standard input, for profile add. It returns the
// credential, which must be a key or a device token.
func ReadCredential(r io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxCredential+1))
	if err != nil {
		return "", exitcode.Usagef("cannot read the credential")
	}
	if len(data) > maxCredential {
		return "", exitcode.Usagef("the credential is too long")
	}
	tok := strings.TrimSpace(string(data))
	if tok == "" {
		return "", exitcode.Usagef("no credential was given")
	}
	if _, err := KindOf(tok); err != nil {
		return "", err
	}
	return tok, nil
}
