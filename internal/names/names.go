// Package names checks the names of folders and files. Slice e uses the same rules for file names.
//
// A name is stored in Unicode NFC, so the same visible name typed in composed or decomposed form
// is one name. Its key is the case-folded NFC form, and two names in one directory must have
// different keys, so "Report" and "report" cannot both exist.
package names

import (
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

	"selfcloud/internal/apierr"
)

// maxBytes is the longest name, in bytes after normalising to NFC.
const maxBytes = 255

// Clean checks s as a name and returns its NFC form and its key. Nothing is trimmed: a name with
// a leading or trailing space is refused, so the name shown is the name given.
func Clean(s string) (name, key string, err error) {
	if !utf8.ValidString(s) {
		return "", "", invalid("the name is not valid UTF-8")
	}
	name = norm.NFC.String(s)
	switch {
	case name == "":
		return "", "", invalid("the name is required")
	case len(name) > maxBytes:
		return "", "", invalid("the name is longer than 255 bytes")
	case name == "." || name == "..":
		return "", "", invalid("the name may not be . or ..")
	}
	first, _ := utf8.DecodeRuneInString(name)
	last, _ := utf8.DecodeLastRuneInString(name)
	if unicode.IsSpace(first) || unicode.IsSpace(last) {
		return "", "", invalid("the name may not start or end with a space")
	}
	for _, r := range name {
		switch {
		case r == '/' || r == '\\':
			return "", "", invalid("the name may not contain / or \\")
		case r == 0 || unicode.IsControl(r):
			return "", "", invalid("the name contains a control character")
		}
	}
	key = norm.NFC.String(cases.Fold().String(name))
	return name, key, nil
}

// Valid reports whether s is an acceptable name. It is Clean without the results.
func Valid(s string) bool {
	_, _, err := Clean(s)
	return err == nil
}

func invalid(msg string) error {
	return apierr.New(apierr.InvalidName, msg)
}
