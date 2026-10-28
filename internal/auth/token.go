// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"crypto/rand"
	"encoding/base64"
)

// NewToken returns a new random token: 32 bytes in unpadded base64url. Verification links, reset
// links and session cookies use it. Only hash is stored; secret goes to the one who needs it.
func NewToken() (secret string, hash []byte) {
	var b [32]byte
	rand.Read(b[:]) // crypto/rand.Read never returns an error.
	secret = base64.RawURLEncoding.EncodeToString(b[:])
	return secret, HashToken(secret)
}

// HashToken returns the stored form of a token. A token holds 256 random bits, so a fast hash is
// enough, as it is for API keys.
func HashToken(secret string) []byte { return HashKey(secret) }

// ValidToken reports whether s has the shape of a token made by NewToken. Callers check it
// before hashing or looking anything up, so malformed input never reaches the database.
func ValidToken(s string) bool {
	if len(s) != keySecretLen {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(s)
	return err == nil
}
