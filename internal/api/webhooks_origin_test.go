// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import "testing"

// TestURLOrigin: the origin keeps scheme, host and port, and drops user name, path, query and fragment.
func TestURLOrigin(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"https://hooks.example.com/services/T/B?token=Q#frag": "https://hooks.example.com",
		"https://h.example.com:8443/a":                        "https://h.example.com:8443",
		"https://[2001:db8::1]:9/x?y":                         "https://[2001:db8::1]:9",
		"https://u:p@h.example.com/x":                         "https://h.example.com",
		"https:///x":                                          "",
		"://bad":                                              "",
		"":                                                    "",
	} {
		if got := urlOrigin(in); got != want {
			t.Errorf("urlOrigin(%q) = %q, want %q", in, got, want)
		}
	}
}
