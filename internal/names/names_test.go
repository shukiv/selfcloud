// SPDX-License-Identifier: AGPL-3.0-or-later

package names

import (
	"errors"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
)

func TestCleanAcceptsAndKeys(t *testing.T) {
	name, key, err := Clean("Report")
	if err != nil || name != "Report" || key != "report" {
		t.Errorf("Clean(Report) = %q, %q, %v", name, key, err)
	}
	// Hebrew and Arabic names are kept as given, and their keys equal their names (no case).
	for _, s := range []string{"דוח שנתי", "تقرير السنة", "日本語.txt", "résumé v2"} {
		name, key, err := Clean(s)
		if err != nil || name != s || key == "" {
			t.Errorf("Clean(%q) = %q, %q, %v", s, name, key, err)
		}
	}
}

func TestCleanNormalisesNFD(t *testing.T) {
	composed := "café"    // é as one code point
	decomposed := "café" // e followed by a combining acute accent
	nc, kc, err := Clean(composed)
	if err != nil {
		t.Fatal(err)
	}
	nd, kd, err := Clean(decomposed)
	if err != nil {
		t.Fatal(err)
	}
	if nc != nd || kc != kd {
		t.Errorf("NFC forms differ: %q/%q vs %q/%q", nc, kc, nd, kd)
	}
	if nd != composed {
		t.Errorf("stored form %q is not NFC", nd)
	}
	// Case folding applies to the NFC form, so the decomposed capital matches the composed lower.
	_, k1, _ := Clean("CAFÉ")
	_, k2, _ := Clean("café")
	if k1 != k2 {
		t.Errorf("keys %q and %q should match", k1, k2)
	}
	// Hebrew and Arabic names with decomposed marks normalise too.
	if n, _, _ := Clean("é"); n != "é" {
		t.Errorf("e+acute normalised to %q", n)
	}
}

func TestCleanCaseFoldAcrossScripts(t *testing.T) {
	_, a, _ := Clean("Straße")
	_, b, _ := Clean("STRASSE")
	if a != b {
		t.Errorf("Straße key %q, STRASSE key %q; full case folding should match", a, b)
	}
	_, k1, _ := Clean("Report")
	_, k2, _ := Clean("REPORT")
	if k1 != k2 {
		t.Errorf("ASCII keys differ: %q %q", k1, k2)
	}
}

func TestCleanRefuses(t *testing.T) {
	long := strings.Repeat("a", 256)
	for name, in := range map[string]string{
		"empty":             "",
		"too long":          long,
		"dot":               ".",
		"dot dot":           "..",
		"slash":             "a/b",
		"backslash":         "a\\b",
		"nul":               "a\x00b",
		"control":           "a\tb",
		"newline":           "a\nb",
		"DEL":               "a\x7fb",
		"leading space":     " a",
		"trailing space":    "a ",
		"only spaces":       "   ",
		"invalid utf-8":     "a\xffb",
		"over 255 composed": strings.Repeat("é", 128), // 256 bytes after NFC
		"zero width space":  "a\u200bb",
		"zero width joiner": "a\u200db",
		"byte order mark":   "\ufeffa",
		"bidi override":     "a\u202eb",
		"line separator":    "a\u2028b",
		"paragraph sep":     "a\u2029b",
		"only zero width":   "\u200b",
		"only format chars": "\u200b\u200c\u2066",
	} {
		if _, _, err := Clean(in); err == nil {
			t.Errorf("%s: Clean(%q) accepted", name, in)
		} else {
			var e *apierr.Error
			if !errors.As(err, &e) || e.Code != apierr.InvalidName {
				t.Errorf("%s: error %v is not invalid_name", name, err)
			}
		}
	}
	// Exactly 255 bytes is fine, including a Hebrew name of that length.
	if _, _, err := Clean(strings.Repeat("a", 255)); err != nil {
		t.Errorf("255 bytes refused: %v", err)
	}
	if !Valid("מסמך") {
		t.Error("Hebrew name refused")
	}
	if Valid("x/y") {
		t.Error("Valid accepted a slash")
	}
}

func TestCleanAcceptsVisibleScripts(t *testing.T) {
	// Visible text in Hebrew, Arabic and Latin with no format characters still passes.
	for _, s := range []string{"מסמך 2", "وثيقة ٢", "plain.txt"} {
		if !Valid(s) {
			t.Errorf("Valid(%q) = false", s)
		}
	}
}
