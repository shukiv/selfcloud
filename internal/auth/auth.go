// SPDX-License-Identifier: AGPL-3.0-or-later

// Package auth makes and checks credentials and describes who is making a request.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"slices"
	"strings"
	"time"
)

// Key prefixes. The prefix says what a key is for, so a leaked key is easy to recognise and a
// key of the wrong kind is refused before any lookup.
const (
	AdminKeyPrefix   = "sc_admin_"
	AccountKeyPrefix = "sc_acct_"
	// TokenPrefix starts a delegated token. It differs from the key prefixes, so a token is never
	// mistaken for a key.
	TokenPrefix = "sct_"
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

// Roles of a signed-in user. The names match the users.role column. An admin has no account;
// an owner manages its account in the web interface; a member reaches only granted folders.
const (
	RoleAdmin  = "admin"
	RoleOwner  = "owner"
	RoleMember = "member"
)

// Principal is who is making a request.
type Principal struct {
	Kind      Kind
	ID        string // the key's ID, or the user's ID
	AccountID string // the account a key or user belongs to; "" for installation admins
	UserID    string // set for KindUser
	Role      string // set for KindUser: RoleOwner or "member"
	// Token is set when the caller presents a delegated token. The token then narrows what the
	// caller may reach; see TokenScope.
	Token *TokenScope
}

// TokenScope is what a delegated token allows. A token acts as its user, when it names one, or
// as the account key that minted it. It never manages anything, and it reaches only Folders, at
// most at Write.
type TokenScope struct {
	Folders []string `json:"folders"`
	// All is set for a token that names no folders: it reaches every folder its user reaches, and
	// Folders is empty. Only a token with a user may be All. The user's own access decides what it
	// reaches; the token adds only its role cap. A token without the field keeps its folder list.
	All      bool      `json:"all,omitempty"`
	Node     string    `json:"node,omitempty"` // a directory of one of Folders; "" for the whole folders
	Write    bool      `json:"write"`          // false: read only
	Audience string    `json:"audience"`       // "api"; the only audience in use
	MintedBy string    `json:"minted_by"`
	IssuedAt time.Time `json:"issued_at"`
	// ExpiresAt bounds anything the token creates that outlives the request, such as a signed link.
	// A queued job does not check it: the job was authorised when it was queued.
	ExpiresAt time.Time `json:"expires_at"`
}

// Reaches reports whether the token may reach folder folderID at all. An All token reaches every
// folder, so the caller's own access check decides; a folder-list token reaches only its list.
func (t *TokenScope) Reaches(folderID string) bool {
	return t.All || slices.Contains(t.Folders, folderID)
}

// IsAdmin reports whether p is an installation admin: an admin key, or a signed-in admin user.
func (p *Principal) IsAdmin() bool {
	if p == nil {
		return false
	}
	return p.Kind == KindAdminKey || (p.Kind == KindUser && p.Role == RoleAdmin)
}

// ManagesAccount reports whether p may manage the account: its users, folders, grants, keys
// and audit log. An admin manages every account; an owner session manages only its own.
func (p *Principal) ManagesAccount(accountID string) bool {
	if p == nil || p.Token != nil {
		// A delegated token never manages an account, even when it acts for an owner or a key.
		return false
	}
	switch p.Kind {
	case KindAdminKey:
		return true
	case KindAccountKey:
		return p.AccountID == accountID
	case KindUser:
		if p.Role == RoleAdmin {
			return true
		}
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
