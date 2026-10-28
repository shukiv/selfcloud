// Package auth makes and checks credentials and describes who is making a request.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

// Key prefixes. The prefix says what a key is for, so a leaked key is easy to recognise and a
// key of the wrong kind is refused before any lookup.
const (
	AdminKeyPrefix   = "sc_admin_"
	AccountKeyPrefix = "sc_acct_"
)

// keySecretLen is the length of 32 random bytes in unpadded base64url.
const keySecretLen = 43

// displayLen is how many characters of the random part are kept for display.
const displayLen = 6

// Kind is the kind of caller.
type Kind string

// Caller kinds. They also appear in the audit log.
const (
	KindAdminKey   Kind = "admin_key"
	KindAccountKey Kind = "account_key"
	KindUser       Kind = "user"   // a signed-in user, by session cookie
	KindSystem     Kind = "system" // the server itself, or the admin command line
)

// RoleOwner is the role of a user who manages their account in the web interface. The other
// role is "member". The names match the users.role column.
const RoleOwner = "owner"

// Principal is who is making a request.
type Principal struct {
	Kind      Kind
	ID        string // the key's ID, or the user's ID
	AccountID string // the account a key or user belongs to; "" for installation admins
	UserID    string // set for KindUser
	Role      string // set for KindUser: RoleOwner or "member"
}

// IsAdmin reports whether p is an installation admin.
func (p *Principal) IsAdmin() bool { return p != nil && p.Kind == KindAdminKey }

// ManagesAccount reports whether p may manage the account: its users, folders, grants, keys
// and audit log. An owner session manages only its own account.
func (p *Principal) ManagesAccount(accountID string) bool {
	if p == nil {
		return false
	}
	switch p.Kind {
	case KindAdminKey:
		return true
	case KindAccountKey:
		return p.AccountID == accountID
	case KindUser:
		return p.Role == RoleOwner && p.AccountID == accountID
	}
	return false
}

// NewKey returns a new random API key with the given prefix, the start of it that is safe to
// display, and the hash to store.
func NewKey(prefix string) (secret, display string, hash []byte) {
	var b [32]byte
	rand.Read(b[:]) // crypto/rand.Read never returns an error.
	secret = prefix + base64.RawURLEncoding.EncodeToString(b[:])
	return secret, secret[:len(prefix)+displayLen], HashKey(secret)
}

// HashKey returns the stored form of a key. A key holds 256 random bits, so a fast hash is
// enough: there is nothing to guess, and a slow hash would only slow every request.
func HashKey(secret string) []byte {
	h := sha256.Sum256([]byte(secret))
	return h[:]
}

// ValidKeyFormat reports whether s has the shape of a key made by NewKey(prefix).
func ValidKeyFormat(prefix, s string) bool {
	rest, ok := strings.CutPrefix(s, prefix)
	if !ok || len(rest) != keySecretLen {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(rest)
	return err == nil
}

// DisplayPrefix returns the displayable start of a key.
func DisplayPrefix(prefix, secret string) string {
	return secret[:min(len(secret), len(prefix)+displayLen)]
}
