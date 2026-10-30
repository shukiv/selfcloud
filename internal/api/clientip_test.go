// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/shukiv/selfcloud/internal/config"
)

func TestClientIP(t *testing.T) {
	t.Parallel()
	trusted, err := config.ParsePrefixes([]string{"10.0.0.0/8", "2001:db8::/32"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		peer    string
		header  string
		headers map[string][]string
		want    string
	}{
		{"untrusted peer ignores header", "203.0.113.9:1234", "X-Forwarded-For",
			map[string][]string{"X-Forwarded-For": {"198.51.100.1"}}, "203.0.113.9"},
		{"trusted peer, one hop", "10.0.0.2:1234", "X-Forwarded-For",
			map[string][]string{"X-Forwarded-For": {"198.51.100.1"}}, "198.51.100.1"},
		{"spoofed left entries are skipped", "10.0.0.2:1234", "X-Forwarded-For",
			map[string][]string{"X-Forwarded-For": {"1.2.3.4, 198.51.100.1, 10.0.0.3"}}, "198.51.100.1"},
		{"several header lines", "10.0.0.2:1234", "X-Forwarded-For",
			map[string][]string{"X-Forwarded-For": {"1.2.3.4", "198.51.100.1"}}, "198.51.100.1"},
		{"all hops trusted", "10.0.0.2:1234", "X-Forwarded-For",
			map[string][]string{"X-Forwarded-For": {"10.0.0.5, 10.0.0.3"}}, "10.0.0.5"},
		{"garbage hop stops the walk", "10.0.0.2:1234", "X-Forwarded-For",
			map[string][]string{"X-Forwarded-For": {"198.51.100.1, not-an-ip"}}, "10.0.0.2"},
		{"single-address header", "10.0.0.2:1234", "CF-Connecting-IP",
			map[string][]string{"Cf-Connecting-Ip": {"198.51.100.7"}}, "198.51.100.7"},
		{"single-address header, garbage", "10.0.0.2:1234", "CF-Connecting-IP",
			map[string][]string{"Cf-Connecting-Ip": {"x"}}, "10.0.0.2"},
		{"no header configured", "10.0.0.2:1234", "",
			map[string][]string{"X-Forwarded-For": {"198.51.100.1"}}, "10.0.0.2"},
		{"IPv6 peer", "[2001:db8::1]:443", "X-Forwarded-For",
			map[string][]string{"X-Forwarded-For": {"2001:db9::5"}}, "2001:db9::5"},
		{"IPv4-mapped peer", "[::ffff:10.0.0.2]:443", "X-Forwarded-For",
			map[string][]string{"X-Forwarded-For": {"198.51.100.1"}}, "198.51.100.1"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.peer
		for k, vs := range tc.headers {
			for _, v := range vs {
				r.Header.Add(k, v)
			}
		}
		got := clientIP(r, trusted, tc.header)
		if got != netip.MustParseAddr(tc.want) {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}
