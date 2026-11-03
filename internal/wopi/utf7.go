// SPDX-License-Identifier: AGPL-3.0-or-later

package wopi

// UTF-7 (RFC 2152) for X-WOPI-RequestedName, the only UTF-7 text WOPI sends. A "+" starts a run of UTF-16 code units
// in base64, which ends at a "-" (consumed) or at any other character. "+-" is a plus sign. Input that is not ASCII
// is refused, as is a shift that does not end on a whole code unit, that holds a lone surrogate, or that is empty.

import (
	"errors"
	"strings"
	"unicode/utf16"
)

const b64Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// errUTF7 is a name that is not valid UTF-7.
var errUTF7 = errors.New("wopi: the name is not valid UTF-7")

// b64Value is the value of a base64 character, or -1 when it is not one.
func b64Value(c byte) int {
	switch {
	case c >= 'A' && c <= 'Z':
		return int(c - 'A')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 26
	case c >= '0' && c <= '9':
		return int(c-'0') + 52
	case c == '+':
		return 62
	case c == '/':
		return 63
	}
	return -1
}

// DecodeUTF7 decodes a UTF-7 name as RFC 2152 defines it.
func DecodeUTF7(s string) (string, error) {
	var out strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		if c >= 0x80 {
			return "", errUTF7
		}
		if c != '+' {
			out.WriteByte(c)
			i++
			continue
		}
		i++
		if i < len(s) && s[i] == '-' {
			out.WriteByte('+')
			i++
			continue
		}
		var units []uint16
		var acc uint32
		bits := 0
		for ; i < len(s) && b64Value(s[i]) >= 0; i++ {
			acc = acc<<6 | uint32(b64Value(s[i]))
			bits += 6
			if bits >= 16 {
				bits -= 16
				units = append(units, uint16(acc>>bits))
				acc &= 1<<bits - 1
			}
		}
		// The bits left over must be less than one character's worth of padding, and all zero.
		if bits >= 6 || acc != 0 || len(units) == 0 {
			return "", errUTF7
		}
		if i < len(s) && s[i] == '-' {
			i++
		}
		runes := utf16.Decode(units)
		for _, r := range runes {
			if r == 0xFFFD {
				return "", errUTF7 // a lone surrogate
			}
		}
		out.WriteString(string(runes))
	}
	return out.String(), nil
}

// EncodeUTF7 encodes a name as UTF-7 (RFC 2152). Printable ASCII other than "+" and "\" is written as it is; every
// other run is written in base64 between "+" and "-".
func EncodeUTF7(s string) string {
	var out strings.Builder
	var run []uint16
	flush := func() {
		if len(run) == 0 {
			return
		}
		var acc uint32
		bits := 0
		out.WriteByte('+')
		for _, u := range run {
			acc = acc<<16 | uint32(u)
			bits += 16
			for bits >= 6 {
				bits -= 6
				out.WriteByte(b64Alphabet[(acc>>bits)&0x3F])
			}
			acc &= 1<<bits - 1
		}
		if bits > 0 {
			out.WriteByte(b64Alphabet[(acc<<(6-bits))&0x3F])
		}
		out.WriteByte('-')
		run = run[:0]
	}
	for _, r := range s {
		if r >= 0x20 && r < 0x7F && r != '+' && r != '\\' {
			flush()
			out.WriteByte(byte(r))
			continue
		}
		if r == '+' {
			flush()
			out.WriteString("+-")
			continue
		}
		u1, u2 := utf16.EncodeRune(r)
		if u1 == 0xFFFD {
			run = append(run, uint16(r))
		} else {
			run = append(run, uint16(u1), uint16(u2))
		}
	}
	flush()
	return out.String()
}
