// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/url"
	"strings"
	"testing"
)

// TestNumericHostsAreNotNames: a host whose last label is all digits is not a DNS name. Some resolvers read "127.1" and
// "2130706433" as loopback, so such a host is refused at registration whatever its resolution would be (decision 28).
func TestNumericHostsAreNotNames(t *testing.T) {
	t.Parallel()
	for _, host := range []string{"127.1", "2130706433", "0x7f.1", "0177.0.0.1", "1.2.3.4.5"} {
		if parseHostLiteral(host) {
			t.Errorf("%s is accepted as a host", host)
		}
	}
	for _, host := range []string{"hooks.example.com", "localhost", "h1.example", "203.0.113.9", "2001:db8::1"} {
		if !parseHostLiteral(host) {
			t.Errorf("%s is refused as a host", host)
		}
	}
}

// FuzzCleanWebhookURL: an arbitrary string is never a panic, and an accepted URL is always HTTPS (or HTTP where allowed),
// with a host, no user name, no fragment, a host by a name with a letter or an IP literal, and at most 2048 characters.
func FuzzCleanWebhookURL(f *testing.F) {
	for _, seed := range []string{
		"https://hooks.example.com/in", "https://u:p@hooks.example.com/in", "https://127.1/in",
		"https://[::1]:99999/", "http://hooks.example.com/#frag", "https://a..b/", "https://0x7f.1/",
		"https://hooks.example.com:443/a?b=c", "ftp://x", "", "https:///in", "https://[2001:db8::1]/in",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		for _, allowHTTP := range []bool{false, true} {
			got, err := cleanWebhookURL(raw, allowHTTP)
			if err != nil {
				continue
			}
			u, perr := url.Parse(got)
			if perr != nil {
				t.Fatalf("accepted %q, which does not parse", got)
			}
			if u.User != nil {
				t.Fatalf("accepted a user name: %q", got)
			}
			if strings.Contains(got, "#") {
				t.Fatalf("accepted a fragment: %q", got)
			}
			if u.Scheme != "https" && !(allowHTTP && u.Scheme == "http") {
				t.Fatalf("accepted scheme %q with allowHTTP=%v: %q", u.Scheme, allowHTTP, got)
			}
			if u.Hostname() == "" || !parseHostLiteral(u.Hostname()) {
				t.Fatalf("accepted host %q in %q", u.Hostname(), got)
			}
			if len(got) > webhookURLMax {
				t.Fatalf("accepted %d characters", len(got))
			}
		}
	})
}
