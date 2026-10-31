// SPDX-License-Identifier: AGPL-3.0-or-later

package webhook

import (
	"net/netip"
	"testing"
)

// The deny table, one row per range the design names, and the forms that embed an IPv4 address. Each address is
// compared as a parsed value, so the text form of a row does not matter.
func TestDeniedAddresses(t *testing.T) {
	t.Parallel()
	denied := []string{
		// IPv4: unspecified, loopback, link-local (with the metadata address), RFC 1918, CGNAT, the IETF block,
		// documentation, benchmarking, multicast and the reserved 240/4 with the broadcast address.
		"0.0.0.0", "0.1.2.3", "127.0.0.1", "127.255.255.254", "169.254.169.254", "169.254.0.1",
		"10.0.0.1", "10.255.255.255", "172.16.0.1", "172.31.255.255", "192.168.1.1",
		"100.64.0.1", "100.127.255.255", "192.0.0.5", "192.0.2.7", "198.18.0.1", "198.19.255.255",
		"198.51.100.9", "203.0.113.9", "224.0.0.1", "239.255.255.255", "240.0.0.1", "255.255.255.255",
		// IPv6: unspecified, loopback, link-local, unique local, multicast and documentation.
		"::", "::1", "fe80::1", "febf::1", "fc00::1", "fd12:3456::1", "ff02::1", "2001:db8::1",
		// IPv4-mapped, checked as the IPv4 address they hold.
		"::ffff:127.0.0.1", "::ffff:10.1.2.3", "::ffff:169.254.169.254",
		// IPv4-compatible (::/96), checked as the IPv4 address held in the last 32 bits.
		"::127.0.0.1", "::10.0.0.1", "::0.0.0.1",
		// NAT64 (64:ff9b::/96), checked as the IPv4 address it embeds.
		"64:ff9b::a00:1", "64:ff9b::7f00:1", "64:ff9b::a9fe:a9fe",
		// 6to4 (2002::/16), checked as the IPv4 address in bytes 2 to 5.
		"2002:7f00:1::1", "2002:0a00:0001::", "2002:c0a8:0101::1", "2002:a9fe:a9fe::",
	}
	for _, s := range denied {
		if !Denied(netip.MustParseAddr(s)) {
			t.Errorf("%s is allowed, want denied", s)
		}
	}
}

// Review finding 7: the forms outside 2000::/3 are denied, as the whole block is (IPv4-mapped and 6to4 aside), and the
// blocks inside it that are not public are denied.
func TestDenyTableRestOfIPv6AndSIIT(t *testing.T) {
	t.Parallel()
	denied := []string{
		"::ffff:0:a00:1",    // SIIT translation (::ffff:0:0:0/96) of 10.0.0.1
		"::ffff:0:7f00:1",   // SIIT of 127.0.0.1
		"fec0::1",           // site-local, outside 2000::/3
		"64:ff9b:1::a00:1",  // local-use NAT64 of 10.0.0.1
		"64:ff9b::808:808",  // NAT64 of 8.8.8.8: outside 2000::/3, so denied
		"100::1",            // discard prefix
		"2001::1",           // Teredo (2001::/32)
		"2001:2::1",         // benchmarking (2001:2::/48)
		"2001:10::1",        // ORCHID (2001:10::/28)
		"2001:20::1",        // ORCHIDv2 (2001:20::/28)
		"3fff::1",           // documentation (3fff::/20)
		"::8.8.8.8",         // IPv4-compatible, outside 2000::/3
		"192.88.99.1",       // 6to4 relay anycast (192.88.99.0/24)
		"2002:c058:6301::1", // 6to4 of 192.88.99.1
	}
	for _, s := range denied {
		if !Denied(netip.MustParseAddr(s)) {
			t.Errorf("%s is allowed, want denied", s)
		}
	}
	allowed := []string{"2001:4860:4860::8888", "2606:4700:4700::1111", "2002:5db8:d822::1", "8.8.4.4"}
	for _, s := range allowed {
		if Denied(netip.MustParseAddr(s)) {
			t.Errorf("%s is denied, want allowed", s)
		}
	}
	// The operator's allow list still opens its own network, whatever form it is written in.
	if Refused(netip.MustParseAddr("fd00::5"), []netip.Prefix{netip.MustParsePrefix("fd00::/8")}) {
		t.Error("an allowed unique-local address is refused")
	}
}

// Public addresses, including the public forms of the embedding prefixes, are allowed.
func TestAllowedAddresses(t *testing.T) {
	t.Parallel()
	allowed := []string{
		"93.184.216.34", "8.8.8.8", "1.1.1.1", "100.63.255.255", "172.15.255.255", "172.32.0.1",
		"192.169.0.1", "198.17.255.255", "223.255.255.255",
		"2606:4700:4700::1111", "2a00:1450:4001::200e",
		"::ffff:93.184.216.34", "2002:5db8:d822::1",
	}
	for _, s := range allowed {
		if Denied(netip.MustParseAddr(s)) {
			t.Errorf("%s is denied, want allowed", s)
		}
	}
}

// A zoned address is refused whatever it is: its zone is not a network the table knows.
func TestZonedAddressRefused(t *testing.T) {
	t.Parallel()
	if !Denied(netip.MustParseAddr("2606:4700:4700::1111%eth0")) {
		t.Fatal("a zoned address is allowed")
	}
}

// The allow list lets an operator's network through, by parsed value. It does not open the rest of the table.
func TestAllowListBypassesOnlyItsNetwork(t *testing.T) {
	t.Parallel()
	allow := []netip.Prefix{netip.MustParsePrefix("10.20.0.0/16"), netip.MustParsePrefix("127.0.0.1/32")}
	cases := map[string]bool{
		"10.20.3.4":        false, // allowed network
		"::ffff:10.20.3.4": false, // the same address in its mapped form
		"10.21.0.1":        true,  // private, outside the allowed network
		"127.0.0.1":        false, // the one loopback address the operator allowed
		"127.0.0.2":        true,  // other loopback
		"::ffff:127.0.0.2": true,
		"169.254.169.254":  true,  // metadata address, not allowed
		"93.184.216.34":    false, // public, never refused
	}
	for s, want := range cases {
		if got := Refused(netip.MustParseAddr(s), allow); got != want {
			t.Errorf("Refused(%s) = %v, want %v", s, got, want)
		}
	}
}

// Origin keeps the scheme, the host and the port. Everything else is dropped, since a token may sit in any of it.
func TestOrigin(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"https://hooks.example.com/services/T/B?token=Q": "https://hooks.example.com",
		"https://hooks.example.com:8443/in#frag":         "https://hooks.example.com:8443",
		"https://user:pw@hooks.example.com/in":           "https://hooks.example.com",
		"http://[2606:4700::1111]:9/in?x=1":              "http://[2606:4700::1111]:9",
		"not a url":                                      "",
		"/relative/path":                                 "",
	}
	for raw, want := range cases {
		if got := Origin(raw); got != want {
			t.Errorf("Origin(%q) = %q, want %q", raw, got, want)
		}
	}
}
