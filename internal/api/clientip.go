package api

import (
	"net/http"
	"net/netip"
	"strings"
)

// clientIP returns the address of the client behind r.
//
// The header is believed only when the direct peer is a trusted proxy. For X-Forwarded-For the
// list is read from the right, skipping trusted proxies, and the first address that is not one
// is the client: entries further left were written by the client itself and prove nothing.
// Any other header is taken to hold a single address set by the proxy.
func clientIP(r *http.Request, trusted []netip.Prefix, header string) netip.Addr {
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}
	}
	addr := peer.Addr().Unmap()
	if header == "" || !inPrefixes(addr, trusted) {
		return addr
	}

	if http.CanonicalHeaderKey(header) == "X-Forwarded-For" {
		var hops []string
		for _, line := range r.Header.Values("X-Forwarded-For") {
			hops = append(hops, strings.Split(line, ",")...)
		}
		for i := len(hops) - 1; i >= 0; i-- {
			a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
			if err != nil {
				return addr
			}
			a = a.Unmap()
			if !inPrefixes(a, trusted) {
				return a
			}
			addr = a
		}
		return addr
	}

	if a, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get(header))); err == nil {
		return a.Unmap()
	}
	return addr
}

func inPrefixes(a netip.Addr, ps []netip.Prefix) bool {
	for _, p := range ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
