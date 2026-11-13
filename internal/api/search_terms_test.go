// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Terms with punctuation (decision 46). A name search of three or more
// runes reads the name literally on the trigram index, so "1.2" and "a-b" find the names that contain them. A term that
// takes the word index must have a token of two letters or digits: "a." and "a.." are refused, and "1.2" is a phrase of
// two tokens, exact, which matches "version 1.2" and not "1.25".

// TestNameSearchFindsPunctuationOnTheSubstringPath: "1.2" and "a-b" are substrings of names, found as they are.
func TestNameSearchFindsPunctuationOnTheSubstringPath(t *testing.T) {
	t.Parallel()
	fx := newSearchFixture(t)
	putTyped(t, fx.s, fx.key, fx.fld.ID, "report-1.2.pdf", "text/plain", "x")
	putTyped(t, fx.s, fx.key, fx.fld.ID, "a-b.txt", "text/plain", "x")
	for q, want := range map[string]string{"1.2": "report-1.2.pdf", "a-b": "a-b.txt"} {
		got := hitNames(t, fx.search(t, "q="+url.QueryEscape(q)))
		if !containsString(got, want) {
			t.Errorf("name search %q: %v, want it to find %q", q, got, want)
		}
	}
}

// TestNameSearchRefusesAOneLetterTerm: "a." on the word index is one letter, so it is refused, as a short term is.
func TestNameSearchRefusesAOneLetterTerm(t *testing.T) {
	t.Parallel()
	fx := newSearchFixture(t)
	wantError(t, fx.search(t, "q=a."), http.StatusBadRequest, "invalid_request")
}

// TestContentSearchRefusesOneLetterWithPunctuation: "a.." is one letter; content search refuses it, and so does a term with
// no letter or digit.
func TestContentSearchRefusesOneLetterWithPunctuation(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	fx.file(t, "one.txt", "a word")
	for _, q := range []string{"a..", "...", "--"} {
		rec := callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/search?in=content&q="+url.QueryEscape(q), nil)
		wantError(t, rec, http.StatusBadRequest, "invalid_request")
	}
}

// TestContentPhraseOfAVersionNumber: "1.2" is the words 1 and 2 next to each other. It finds "version 1.2", and not
// "version 1.25", whose second word is 25.
func TestContentPhraseOfAVersionNumber(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	fx.file(t, "exact.txt", "version 1.2")
	fx.file(t, "other.txt", "version 1.25")
	drainText(t, fx.s.textWorker)
	got := contentNames(fx.find(t, fx.bobSess, "1.2"))
	if strings.Join(got, "|") != "exact.txt" {
		t.Fatalf("content search 1.2: %v, want exact.txt only", got)
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
