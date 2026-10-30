// SPDX-License-Identifier: AGPL-3.0-or-later

package webhook

import (
	"crypto/rand"
	"encoding/base64"
	"slices"
	"strings"
)

// Events is the fixed list of event types an endpoint may subscribe to (design section 10). The store's
// schema holds the same list as a CHECK, so the database refuses any other name too.
var Events = []string{
	"file.created", "file.deleted", "file.moved", "file.updated",
	"quota.exceeded", "quota.warning",
	"upload.completed",
	"user.created", "user.verified",
	"webhook.test",
}

// ValidEvent reports whether name is in Events.
func ValidEvent(name string) bool { return slices.Contains(Events, name) }

// secretPrefix starts the secret as it is shown to the caller, so it is recognisable in a configuration file.
const secretPrefix = "whsec_"

// NewSecret returns 32 random bytes, the secret of a new endpoint or of a rotation.
func NewSecret() []byte {
	b := make([]byte, SecretLen)
	rand.Read(b) // crypto/rand.Read never returns an error.
	return b
}

// FormatSecret is the form in which a secret is shown once: the prefix, then 43 unpadded base64url
// characters for the 32 bytes.
func FormatSecret(raw []byte) string {
	return secretPrefix + base64.RawURLEncoding.EncodeToString(raw)
}

// ParseSecret reverses FormatSecret. It reports false for anything else.
func ParseSecret(shown string) ([]byte, bool) {
	rest, ok := strings.CutPrefix(shown, secretPrefix)
	if !ok {
		return nil, false
	}
	b, err := base64.RawURLEncoding.DecodeString(rest)
	if err != nil || len(b) != SecretLen {
		return nil, false
	}
	return b, true
}
