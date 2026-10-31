// SPDX-License-Identifier: AGPL-3.0-or-later

// Package id makes opaque, prefixed, random identifiers such as "acc_7k2m9q...".
//
// IDs carry 128 random bits, so they can be made anywhere without coordination and
// reveal nothing about creation order or count.
package id

import (
	"crypto/rand"
	"crypto/sha256"
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

// Derive returns the ID of prefix that seed names. The same seed always gives the same ID. A chunked copy derives
// each new node's ID from its job and the source's ID, so a batch that runs again names the same nodes.
func Derive(prefix, seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return prefix + "_" + enc.EncodeToString(sum[:16])
}
