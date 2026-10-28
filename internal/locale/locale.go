// SPDX-License-Identifier: AGPL-3.0-or-later

// Package locale chooses which of Selfcloud's languages to use: for the text of emails now, and
// for the web interface later.
//
// A requested tag is resolved by its exact match, then its base language, then English. So pt-BR
// and pt-PT both get Portuguese (the European text, the only Portuguese there is), he-IL gets
// Hebrew, and an unknown tag gets English. Users may store any BCP 47-style tag; only rendering
// resolves it.
package locale

import (
	"strings"

	"golang.org/x/text/language"
)

// Default is the language used when nothing else matches. It is also the fallback for every
// missing string or template.
const Default = "en"

// Supported lists the languages Selfcloud ships, in the order the language picker shows them.
var Supported = []string{"en", "es", "fr", "pt", "ru", "ar", "he"}

// rtl lists the languages laid out right to left.
var rtl = map[string]bool{"ar": true, "he": true}

var supported = func() map[string]bool {
	m := make(map[string]bool, len(Supported))
	for _, c := range Supported {
		m[c] = true
	}
	return m
}()

// Resolve maps a tag to a supported language: the lower-cased tag if it is supported, else its
// base language (the part before the first hyphen) if that is, else Default. An empty tag is
// Default.
func Resolve(tag string) string {
	t := strings.ToLower(strings.TrimSpace(tag))
	if supported[t] {
		return t
	}
	if base, _, found := strings.Cut(t, "-"); found && supported[base] {
		return base
	}
	return Default
}

// IsRTL reports whether the language of tag is laid out right to left. The tag is resolved first,
// so "he-IL" is right to left and "xx" is not.
func IsRTL(tag string) bool {
	return rtl[Resolve(tag)]
}

// matcher picks the best supported language for a list of preferences. Supported is the
// first entry, so it is the match when nothing else fits.
var matcher = func() language.Matcher {
	tags := make([]language.Tag, len(Supported))
	for i, c := range Supported {
		tags[i] = language.MustParse(c)
	}
	return language.NewMatcher(tags)
}()

// FromAcceptLanguage returns the supported language that best fits an Accept-Language header,
// using the preferences in their quality order. An empty or unreadable header gives Default.
func FromAcceptLanguage(header string) string {
	if strings.TrimSpace(header) == "" {
		return Default
	}
	prefs, _, err := language.ParseAcceptLanguage(header)
	if err != nil || len(prefs) == 0 {
		return Default
	}
	_, idx, _ := matcher.Match(prefs...)
	if idx < 0 || idx >= len(Supported) {
		return Default
	}
	return Supported[idx]
}
