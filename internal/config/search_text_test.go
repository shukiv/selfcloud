// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"strings"
	"testing"
)

// TestSearchTextLimitsAreChecked: the text limit is at least a kibibyte, and the text kept per file may not exceed the
// largest file that is read (decision 46). Mutation: the second check removed lets a text limit above the source limit
// pass.
func TestSearchTextLimitsAreChecked(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		text     ByteSize
		fullText ByteSize
		wantBad  string
	}{
		{"defaults", 1 << 20, 50 << 20, ""},
		{"text limit below a kibibyte", 512, 50 << 20, "search.text_max_bytes must be at least 1 KiB"},
		{"text limit above the source limit", 2 << 20, 1 << 20, "search.fulltext_max_bytes (1048576) must not be below search.text_max_bytes (2097152)"},
	}
	for _, c := range cases {
		var msgs []string
		validateSearch(SearchConfig{Enabled: true, MaxQueryBytes: 200, MaxTerms: 8,
			Timeout: Duration(2e9), RatePerMinute: 60, TextMaxBytes: c.text, FullTextMaxBytes: c.fullText},
			func(f string, a ...any) { msgs = append(msgs, f) })
		got := strings.Join(msgs, ";")
		if c.wantBad == "" && got != "" {
			t.Errorf("%s: refused: %s", c.name, got)
		}
		if c.wantBad != "" && !strings.Contains(got, strings.SplitN(c.wantBad, " (", 2)[0]) {
			t.Errorf("%s: messages %q, want %q", c.name, got, c.wantBad)
		}
	}
}
