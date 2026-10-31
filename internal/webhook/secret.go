// SPDX-License-Identifier: AGPL-3.0-or-later

// Package webhook holds the parts of webhook delivery that do not touch the request path: the
// encryption of endpoint secrets at rest (design section 10, slice j).
//
// A secret is 32 random bytes. It is stored encrypted with AES-256-GCM under a key derived from the
// links signing key with HKDF-SHA256, under a label of its own, so the key is never the signing key
// itself. The nonce is random and 12 bytes, and the blob is nonce then ciphertext. The additional
// authenticated data names the account, the endpoint and the column, so a blob copied to another
// row, or to another column of the same row, does not open.
package webhook

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// KeyLen is the length of a signing key and of a derived key.
	KeyLen = 32
	// NonceLen is the length of the GCM nonce that starts every blob.
	NonceLen = 12
	// SecretLen is the length of an endpoint secret before encryption.
	SecretLen = 32

	// secretLabel is the HKDF label of the encryption key. It differs from the labels of the link and
	// token keys, so no key of one use works for another.
	secretLabel = "selfcloud webhook secrets v1"
	// aadLabel leads the additional data, so a blob of another use cannot be fed in here either.
	aadLabel = "selfcloud webhook secret v1"

	// FieldSecret names the column holding the current secret.
	FieldSecret = "secret"
	// FieldPrevious names the column holding the secret of the rotation still in its grace period.
	FieldPrevious = "previous_secret"
)

// ErrUnreadable is the answer to a blob that does not open: a wrong key, a changed row, a damaged
// blob. It says nothing about which, so a caller cannot learn the key from it.
var ErrUnreadable = errors.New("webhook: the secret cannot be read")

// Binding is what a blob is bound to: the account, the endpoint and the column it is stored in.
type Binding struct {
	AccountID  string
	EndpointID string
	Field      string
}

// aad encodes the binding. Each part is length-prefixed, so no two bindings encode alike.
func (b Binding) aad() []byte {
	out := make([]byte, 0, 128)
	for _, part := range []string{aadLabel, b.AccountID, b.EndpointID, b.Field} {
		var n [2]byte
		binary.BigEndian.PutUint16(n[:], uint16(len(part)))
		out = append(out, n[:]...)
		out = append(out, part...)
	}
	return out
}

// Sealer encrypts and opens secrets. current seals and opens; previous, when set, only opens, so a
// secret sealed before a rotation of the links key still reads until it is rewrapped.
type Sealer struct {
	current  cipher.AEAD
	previous cipher.AEAD // nil when there is no previous key
}

// NewSealer derives the encryption keys from the links signing keys. Both keys are 32 bytes;
// previous may be nil.
func NewSealer(current, previous []byte) (*Sealer, error) {
	cur, err := newAEAD(current)
	if err != nil {
		return nil, err
	}
	s := &Sealer{current: cur}
	if previous != nil {
		if s.previous, err = newAEAD(previous); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func newAEAD(signing []byte) (cipher.AEAD, error) {
	if len(signing) != KeyLen {
		return nil, fmt.Errorf("webhook: a signing key must be %d bytes", KeyLen)
	}
	key, err := hkdf.Key(sha256.New, signing, nil, secretLabel, KeyLen)
	if err != nil {
		return nil, fmt.Errorf("webhook: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("webhook: %w", err)
	}
	return cipher.NewGCM(block)
}

// Seal encrypts plain for the row and column named by b. The result is nonce then ciphertext.
func (s *Sealer) Seal(plain []byte, b Binding) ([]byte, error) {
	var nonce [NonceLen]byte
	rand.Read(nonce[:]) // crypto/rand.Read never returns an error.
	out := make([]byte, NonceLen, NonceLen+len(plain)+s.current.Overhead())
	copy(out, nonce[:])
	return s.current.Seal(out, nonce[:], plain, b.aad()), nil
}

// Rewrap returns blob sealed under the current key for the row and column named by b. changed is false,
// and blob is returned as it is, when the current key opens it already. A blob that only the previous key
// opens is opened and sealed again under the current key, with a new nonce, and changed is true. Any blob
// that neither key opens is ErrUnreadable.
func (s *Sealer) Rewrap(blob []byte, b Binding) (out []byte, changed bool, err error) {
	if len(blob) < NonceLen+s.current.Overhead() {
		return nil, false, ErrUnreadable
	}
	nonce, body := blob[:NonceLen], blob[NonceLen:]
	if _, err := s.current.Open(nil, nonce, body, b.aad()); err == nil {
		return blob, false, nil
	}
	if s.previous == nil {
		return nil, false, ErrUnreadable
	}
	plain, err := s.previous.Open(nil, nonce, body, b.aad())
	if err != nil {
		return nil, false, ErrUnreadable
	}
	out, err = s.Seal(plain, b)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

// Open decrypts blob for the row and column named by b, under the current key or else the previous
// one. Any failure is ErrUnreadable.
func (s *Sealer) Open(blob []byte, b Binding) ([]byte, error) {
	if len(blob) < NonceLen+s.current.Overhead() {
		return nil, ErrUnreadable
	}
	nonce, body := blob[:NonceLen], blob[NonceLen:]
	if plain, err := s.current.Open(nil, nonce, body, b.aad()); err == nil {
		return plain, nil
	}
	if s.previous != nil {
		if plain, err := s.previous.Open(nil, nonce, body, b.aad()); err == nil {
			return plain, nil
		}
	}
	return nil, ErrUnreadable
}
