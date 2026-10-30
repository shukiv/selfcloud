// SPDX-License-Identifier: AGPL-3.0-or-later

package webhook

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"testing"
)

func testKey(t *testing.T, fill byte) []byte {
	t.Helper()
	return bytes.Repeat([]byte{fill}, KeyLen)
}

func testSealer(t *testing.T, cur, prev []byte) *Sealer {
	t.Helper()
	s, err := NewSealer(cur, prev)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

var (
	bindA = Binding{AccountID: "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa", EndpointID: "whk_aaaaaaaaaaaaaaaaaaaaaaaaaa", Field: FieldSecret}
	bindB = Binding{AccountID: "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa", EndpointID: "whk_bbbbbbbbbbbbbbbbbbbbbbbbbb", Field: FieldSecret}
)

func TestSealOpenRoundTrip(t *testing.T) {
	t.Parallel()
	s := testSealer(t, testKey(t, 1), nil)
	plain := []byte("0123456789abcdef0123456789abcdef")
	blob, err := s.Seal(plain, bindA)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Open(blob, bindA)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("round trip gave %x, want %x", got, plain)
	}
}

func TestNonceIsFresh(t *testing.T) {
	t.Parallel()
	s := testSealer(t, testKey(t, 1), nil)
	a, _ := s.Seal([]byte("same"), bindA)
	b, _ := s.Seal([]byte("same"), bindA)
	if bytes.Equal(a, b) {
		t.Fatal("two seals of the same secret are identical: the nonce is not random")
	}
}

// A ciphertext copied to another endpoint's row must not open there.
func TestCiphertextFailsOnAnotherEndpoint(t *testing.T) {
	t.Parallel()
	s := testSealer(t, testKey(t, 1), nil)
	blob, _ := s.Seal([]byte("secret-of-A"), bindA)
	if _, err := s.Open(blob, bindB); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("opened on another endpoint: err = %v, want ErrUnreadable", err)
	}
}

func TestCiphertextFailsOnAnotherAccount(t *testing.T) {
	t.Parallel()
	s := testSealer(t, testKey(t, 1), nil)
	blob, _ := s.Seal([]byte("secret-of-A"), bindA)
	other := bindA
	other.AccountID = "acc_bbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := s.Open(blob, other); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("opened under another account: err = %v, want ErrUnreadable", err)
	}
}

// The current secret moved into the previous-secret column of the same row must not open there.
func TestCiphertextFailsInAnotherColumn(t *testing.T) {
	t.Parallel()
	s := testSealer(t, testKey(t, 1), nil)
	blob, _ := s.Seal([]byte("secret"), bindA)
	prev := bindA
	prev.Field = FieldPrevious
	if _, err := s.Open(blob, prev); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("opened in the previous column: err = %v, want ErrUnreadable", err)
	}
}

func TestWrongKeyFails(t *testing.T) {
	t.Parallel()
	blob, _ := testSealer(t, testKey(t, 1), nil).Seal([]byte("secret"), bindA)
	if _, err := testSealer(t, testKey(t, 2), nil).Open(blob, bindA); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("opened with a wrong key: err = %v, want ErrUnreadable", err)
	}
}

// After a rotation of the links key, a secret sealed under the previous key still opens.
func TestPreviousKeyOpens(t *testing.T) {
	t.Parallel()
	oldKey, newKey := testKey(t, 1), testKey(t, 2)
	blob, _ := testSealer(t, oldKey, nil).Seal([]byte("secret"), bindA)
	got, err := testSealer(t, newKey, oldKey).Open(blob, bindA)
	if err != nil || string(got) != "secret" {
		t.Fatalf("previous key: got %q, %v", got, err)
	}
}

func TestTamperedBlobFails(t *testing.T) {
	t.Parallel()
	s := testSealer(t, testKey(t, 1), nil)
	blob, _ := s.Seal([]byte("secret"), bindA)
	for i := range blob {
		bad := bytes.Clone(blob)
		bad[i] ^= 0x01
		if _, err := s.Open(bad, bindA); !errors.Is(err, ErrUnreadable) {
			t.Fatalf("byte %d flipped still opens", i)
		}
	}
	if _, err := s.Open(blob[:NonceLen], bindA); !errors.Is(err, ErrUnreadable) {
		t.Fatal("a blob cut to its nonce still opens")
	}
	if _, err := s.Open(nil, bindA); !errors.Is(err, ErrUnreadable) {
		t.Fatal("an empty blob opens")
	}
}

// The key is derived, not used as is: AES-GCM under the raw key cannot open the blob.
func TestKeyIsDerived(t *testing.T) {
	t.Parallel()
	raw := testKey(t, 7)
	blob, _ := testSealer(t, raw, nil).Seal([]byte("secret"), bindA)
	block, _ := aes.NewCipher(raw)
	gcm, _ := cipher.NewGCM(block)
	if _, err := gcm.Open(nil, blob[:NonceLen], blob[NonceLen:], nil); err == nil {
		t.Fatal("the raw signing key opens the blob: the key was not derived")
	}
}

func TestNewSealerRefusesBadKeys(t *testing.T) {
	t.Parallel()
	if _, err := NewSealer(make([]byte, 31), nil); err == nil {
		t.Fatal("a 31-byte key is accepted")
	}
	if _, err := NewSealer(testKey(t, 1), make([]byte, 33)); err == nil {
		t.Fatal("a 33-byte previous key is accepted")
	}
}
