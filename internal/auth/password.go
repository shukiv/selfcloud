// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters: the second recommended option in RFC 9106, section 4. A hash costs
// 64 MiB, so the number running at once is capped (see hashSlots).
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

// Limits on the parameters read back from a stored hash. They are generous, so any hash this
// package wrote verifies, but a corrupt or hostile value cannot make verification exhaust memory.
const (
	maxArgonTime    = 10
	maxArgonMemory  = 1 << 20 // KiB, 1 GiB
	maxArgonThreads = 16
)

// Password length limits, in characters.
const (
	MinPasswordLen = 8
	MaxPasswordLen = 256
)

// hashSlots caps how many argon2 computations run at once, so a burst of sign-ins cannot use
// up memory.
var hashSlots = make(chan struct{}, 4)

// ValidPassword reports whether p is an acceptable new password.
func ValidPassword(p string) error {
	n := utf8.RuneCountInString(p)
	switch {
	case n < MinPasswordLen:
		return fmt.Errorf("the password must be at least %d characters", MinPasswordLen)
	case n > MaxPasswordLen:
		return fmt.Errorf("the password may be at most %d characters", MaxPasswordLen)
	}
	return nil
}

// HashPassword returns the argon2id hash of password as a PHC string:
// $argon2id$v=19$m=65536,t=3,p=4$<salt>$<key>, with unpadded base64 for salt and key.
func HashPassword(ctx context.Context, password string) (string, error) {
	if err := acquireSlot(ctx); err != nil {
		return "", err
	}
	defer releaseSlot()
	salt := make([]byte, argonSaltLen)
	rand.Read(salt) // crypto/rand.Read never returns an error.
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory,
		argonTime, argonThreads, base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword reports whether password matches the stored PHC string. The parameters come
// from the string, so hashes made with other settings still verify. An error means the stored
// string is malformed, not that the password is wrong.
func VerifyPassword(ctx context.Context, encoded, password string) (bool, error) {
	p, err := parsePHC(encoded)
	if err != nil {
		return false, err
	}
	if err := acquireSlot(ctx); err != nil {
		return false, err
	}
	defer releaseSlot()
	got := argon2.IDKey([]byte(password), p.salt, p.time, p.memory, p.threads, uint32(len(p.key)))
	return subtle.ConstantTimeCompare(got, p.key) == 1, nil
}

// dummyHash is a hash of a password nobody knows. Sign-in checks against it when there is no
// password to check, so an unknown email takes as long to answer as a known one.
var dummyHash = sync.OnceValues(func() (string, error) {
	return HashPassword(context.Background(), "no password matches this hash")
})

// DummyVerify does the work of VerifyPassword for a password that has nothing to be checked
// against. Its result is ignored.
func DummyVerify(ctx context.Context, password string) error {
	h, err := dummyHash()
	if err != nil {
		return err
	}
	_, err = VerifyPassword(ctx, h, password)
	return err
}

type phc struct {
	time, memory uint32
	threads      uint8
	salt, key    []byte
}

func parsePHC(s string) (phc, error) {
	bad := errors.New("auth: stored password hash is malformed")
	// "", "argon2id", "v=19", "m=..,t=..,p=..", salt, key
	parts := strings.Split(s, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return phc{}, bad
	}
	var p phc
	for _, kv := range strings.Split(parts[3], ",") {
		name, value, ok := strings.Cut(kv, "=")
		n, err := strconv.ParseUint(value, 10, 32)
		if !ok || err != nil {
			return phc{}, bad
		}
		switch name {
		case "m":
			p.memory = uint32(n)
		case "t":
			p.time = uint32(n)
		case "p":
			p.threads = uint8(min(n, 255))
		default:
			return phc{}, bad
		}
	}
	if p.time < 1 || p.time > maxArgonTime || p.threads < 1 || p.threads > maxArgonThreads ||
		p.memory < 8*uint32(p.threads) || p.memory > maxArgonMemory {
		return phc{}, bad
	}
	var err error
	if p.salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil || len(p.salt) < 8 || len(p.salt) > 64 {
		return phc{}, bad
	}
	if p.key, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil || len(p.key) < 16 || len(p.key) > 64 {
		return phc{}, bad
	}
	return p, nil
}

func acquireSlot(ctx context.Context) error {
	select {
	case hashSlots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func releaseSlot() { <-hashSlots }
