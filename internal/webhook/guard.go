// SPDX-License-Identifier: AGPL-3.0-or-later

package webhook

import (
	"net/netip"
)

// The deny table (design section 10, decision 28). A webhook never connects to an address in it, unless the operator
// allows that network (webhooks.allow_networks). Addresses are compared as parsed values, never as text, after the
// IPv4-mapped and embedded forms are unwrapped, so "::ffff:127.0.0.1" and "127.0.0.1" are the same address.

// denied4 is the IPv4 part of the deny table.
var denied4 = prefixes(
	"0.0.0.0/8",       // unspecified and "this network"
	"10.0.0.0/8",      // RFC 1918
	"100.64.0.0/10",   // carrier-grade NAT
	"127.0.0.0/8",     // loopback
	"169.254.0.0/16",  // link-local, which holds the cloud metadata address
	"172.16.0.0/12",   // RFC 1918
	"192.0.0.0/24",    // IETF protocol assignments
	"192.0.2.0/24",    // documentation
	"192.168.0.0/16",  // RFC 1918
	"198.18.0.0/15",   // benchmarking
	"198.51.100.0/24", // documentation
	"192.88.99.0/24",  // 6to4 relay anycast (RFC 7526)
	"203.0.113.0/24",  // documentation
	"224.0.0.0/4",     // multicast
	"240.0.0.0/4",     // reserved, and the broadcast address
)

// globalUnicast is the only IPv6 block a webhook may reach (review finding 7). Everything outside it is denied: the
// unspecified and loopback addresses, link-local, site-local, unique local, multicast, the IPv4-compatible,
// IPv4-translated (SIIT) and NAT64 prefixes, and the rest of the space. A form that embeds an IPv4 address and sits
// inside the block (6to4) is checked as the IPv4 address it embeds.
var globalUnicast = netip.MustParsePrefix("2000::/3")

// denied6 is the part of globalUnicast that is not public: Teredo, the benchmarking and ORCHID blocks, documentation.
var denied6 = prefixes(
	"2001::/32",     // Teredo tunnelling (RFC 4380)
	"2001:2::/48",   // benchmarking
	"2001:10::/28",  // ORCHID (deprecated)
	"2001:20::/28",  // ORCHIDv2
	"2001:db8::/32", // documentation
	"3fff::/20",     // documentation (RFC 9637)
)

// embedded6to4 is the prefix whose bytes 2 to 5 are an IPv4 address.
var embedded6to4 = netip.MustParsePrefix("2002::/16")

func prefixes(list ...string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(list))
	for _, s := range list {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}

// Denied reports whether addr is in the deny table, before any allow list. An IPv4-mapped address is checked as its
// IPv4 address. Any other IPv6 address outside 2000::/3 is denied, and a 6to4 address (2002::/16) is checked as the
// IPv4 address in bytes 2 to 5.
func Denied(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.Zone() != "" {
		return true
	}
	if addr.Is4() {
		return inAny(denied4, addr)
	}
	if !globalUnicast.Contains(addr) {
		return true
	}
	if embedded6to4.Contains(addr) {
		return Denied(embedded(addr, 2))
	}
	return inAny(denied6, addr)
}

// embedded returns the IPv4 address held in the four bytes of addr that start at offset.
func embedded(addr netip.Addr, offset int) netip.Addr {
	b := addr.As16()
	return netip.AddrFrom4([4]byte{b[offset], b[offset+1], b[offset+2], b[offset+3]})
}

func inAny(list []netip.Prefix, addr netip.Addr) bool {
	for _, p := range list {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// Refused reports whether a connection to addr must not be made: addr is denied and no allowed network holds it.
// An allowed network holds an address by parsed value, after the same unwrapping Denied does.
func Refused(addr netip.Addr, allow []netip.Prefix) bool {
	if !Denied(addr) {
		return false
	}
	return !inAny(allow, addr.Unmap())
}
