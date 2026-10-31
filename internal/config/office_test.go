// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"strings"
	"testing"
)

// TestOfficeBlockIsValidated checks each office rule at start-up. The block is validated even while office is off,
// so that a bad address is found before anyone turns the editor on.
func TestOfficeBlockIsValidated(t *testing.T) {
	t.Parallel()
	const good = "https://office.example.com/hosting/discovery"
	cases := []struct {
		name   string
		mutate func(*OfficeConfig)
		bad    string // a fragment of the error; "" when the config must be valid
	}{
		{"defaults, off", func(o *OfficeConfig) {}, ""},
		{"on with a discovery URL", func(o *OfficeConfig) { o.Enabled = true; o.DiscoveryURL = good }, ""},
		{"on without a discovery URL", func(o *OfficeConfig) { o.Enabled = true }, "discovery_url is required"},
		{"discovery over http", func(o *OfficeConfig) { o.DiscoveryURL = strings.Replace(good, "https", "http", 1) }, "must be https"},
		{"discovery without the discovery path", func(o *OfficeConfig) { o.DiscoveryURL = "https://office.example.com/" }, "/hosting/discovery"},
		{"discovery with a query", func(o *OfficeConfig) { o.DiscoveryURL = good + "?x=1" }, "no query"},
		{"discovery with user information", func(o *OfficeConfig) {
			o.DiscoveryURL = "https://u:p@office.example.com/hosting/discovery"
		}, "no query, fragment or user"},
		{"wopi URL over http", func(o *OfficeConfig) { o.WOPIURL = "http://selfcloud.example.com" }, "must be https"},
		{"wopi URL with a path", func(o *OfficeConfig) { o.WOPIURL = "https://selfcloud.example.com/drive" }, "wopi_url"},
		{"wopi URL with a host", func(o *OfficeConfig) { o.WOPIURL = "https://selfcloud.example.com" }, ""},
		{"lock minutes zero", func(o *OfficeConfig) { o.LockMinutes = 0 }, "lock_minutes"},
		{"token minutes above a day", func(o *OfficeConfig) { o.TokenMinutes = 1441 }, "token_minutes"},
		{"no file size", func(o *OfficeConfig) { o.MaxFileBytes = 0 }, "max_file_bytes"},
	}
	for _, tc := range cases {
		c := Defaults()
		tc.mutate(&c.Office)
		err := c.Validate()
		if tc.bad == "" {
			if err != nil {
				t.Errorf("%s: unexpected error: %v", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.bad) {
			t.Errorf("%s: error = %v, want one mentioning %q", tc.name, err, tc.bad)
		}
	}
}
