// SPDX-License-Identifier: AGPL-3.0-or-later

package wopi

import "testing"

// X-WOPI-RequestedName is UTF-7 encoded (Microsoft's RenameFile page, read from its public mirror: "A UTF-7 encoded
// string that's a file name, not including the file extension"). The cases follow RFC 2152.
func TestDecodeUTF7(t *testing.T) {
	t.Parallel()
	ok := map[string]string{
		"plain":              "plain",
		"a+-b":               "a+b",
		"+-":                 "+",
		"+ZeVnLIqe-":         "日本語",
		"+BB8EQAQ4BDIENQRC-": "Привет",
		"+BA4EQwQ5-":         "Ўуй",
		"+Bek-":              "ש",
		"Report +ZeU-":       "Report 日",
		"+2D3eAA-":           "😀",
		"+ZeVnLIqe":          "日本語", // the shift may end at the end of the string
		"":                   "",
	}
	for in, want := range ok {
		got, err := DecodeUTF7(in)
		if err != nil {
			t.Errorf("DecodeUTF7(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("DecodeUTF7(%q) = %q, want %q", in, got, want)
		}
	}
	// The refused shapes (review of decision 73): a non-ASCII byte; a lone surrogate; padding bits that are not zero
	// ("+ZeX-": 日 and two set bits); a whole character more than the padding ("+ZeUA-"); a shift with no 16-bit unit
	// ("+" at the end, "+!x", "+AG-").
	for _, in := range []string{"café", "+ZeVnLIqé-", "+2D3e-", "+3AA-", "+ZeX-", "+ZeUA-", "+", "+!x", "+AG-"} {
		if got, err := DecodeUTF7(in); err == nil {
			t.Errorf("DecodeUTF7(%q) = %q, want an error", in, got)
		}
	}
}

// Every name round-trips through the encoder, including Hebrew, Arabic, CJK and an emoji.
func TestUTF7RoundTrip(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"plain name", "a+b", "שלום עולם", "مرحبا", "日本語のファイル", "😀 smile", "x-y_z (1)"} {
		enc := EncodeUTF7(name)
		got, err := DecodeUTF7(enc)
		if err != nil || got != name {
			t.Errorf("round trip of %q through %q = %q, %v", name, enc, got, err)
		}
	}
}
