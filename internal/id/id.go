// Package id makes opaque, prefixed, random identifiers such as "acc_7k2m9q...".
//
// IDs carry 128 random bits, so they can be made anywhere without coordination and
// reveal nothing about creation order or count.
package id

import (
	"crypto/rand"
	"encoding/base32"
	"strings"
)

// Lowercase Crockford alphabet: no i, l, o or u, so IDs survive being read aloud or retyped.
var enc = base32.NewEncoding("0123456789abcdefghjkmnpqrstvwxyz").WithPadding(base32.NoPadding)

// encodedLen is the length of 16 bytes in base32 without padding.
const encodedLen = 26

// New returns prefix + "_" + 26 base32 characters.
func New(prefix string) string {
	var b [16]byte
	rand.Read(b[:]) // crypto/rand.Read never returns an error.
	return prefix + "_" + enc.EncodeToString(b[:])
}

// Valid reports whether s has the shape of an ID that New(prefix) would return.
func Valid(prefix, s string) bool {
	rest, ok := strings.CutPrefix(s, prefix+"_")
	if !ok || len(rest) != encodedLen {
		return false
	}
	_, err := enc.DecodeString(rest)
	return err == nil
}
