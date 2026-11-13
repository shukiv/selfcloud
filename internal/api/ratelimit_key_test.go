// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
)

// fu2: every per-address limit counts an IPv6 client by its /64, as the link guard does (decision 74). Two
// addresses of one /64 share one budget, so a host cannot shed a limit by changing its low bits. An IPv4 client
// keeps its own address, so two IPv4 addresses never share.
func TestPerAddressLimitsCountAnIPv6PrefixTogether(t *testing.T) {
	t.Parallel()
	const burst = 2
	cases := []struct {
		name   string
		method string
		path   string
		body   map[string]any
		spend  int // requests from the first address before the shared budget is spent
	}{
		{name: "verify", method: "POST", path: "/api/v1/auth/verify", body: map[string]any{"token": "x"}, spend: burst},
		{name: "login", method: "POST", path: "/api/v1/auth/login",
			body: map[string]any{"email": "ann@example.com", "password": "wrong password here"}, spend: burst},
		{name: "forgot", method: "POST", path: "/api/v1/auth/password/forgot",
			body: map[string]any{"email": "nobody@example.com"}, spend: burst},
		{name: "reset", method: "POST", path: "/api/v1/auth/password/reset",
			body: map[string]any{"token": "x", "password": "a new password here"}, spend: burst},
		{name: "sign-in-link", method: "POST", path: "/api/v1/auth/sign-in-link",
			body: map[string]any{"token": "x"}, spend: burst},
		// Fixed at 600 a minute, not configured: the budget is spent by the first address alone.
		{name: "link serve", method: "GET", path: "/api/v1/links/shl_nothing", spend: 600},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s := newTestServerWith(t, &mail.Recorder{}, func(cfg *config.Config) { cfg.RateLimit.PerIPPerMinute = burst })
			send := func(addr string) int {
				return remoteCall(t, s, c.method, c.path, addr, c.body).Code
			}
			for i := 0; i < c.spend; i++ {
				if code := send("[2001:db8:1:1::1]:4000"); code == http.StatusTooManyRequests {
					t.Fatalf("request %d of the first address was limited before its budget was spent", i+1)
				}
			}
			// A second address of the same /64 has no budget of its own: the budget is shared.
			if code := send("[2001:db8:1:1::2]:4000"); code != http.StatusTooManyRequests {
				t.Errorf("second address of the same /64: status %d, want 429 (the /64 has spent its budget)", code)
			}
			// Another /64 is a different client.
			if code := send("[2001:db8:1:2::1]:4000"); code == http.StatusTooManyRequests {
				t.Errorf("address of another /64: limited, want its own budget")
			}
		})
	}
}

// fu2, IPv4 unchanged: each IPv4 address has its own budget.
func TestPerAddressLimitsKeepIPv4AddressesApart(t *testing.T) {
	t.Parallel()
	const burst = 2
	s := newTestServerWith(t, &mail.Recorder{}, func(cfg *config.Config) { cfg.RateLimit.PerIPPerMinute = burst })
	body := map[string]any{"token": "x"}
	for i := 0; i < burst; i++ {
		if code := remoteCall(t, s, "POST", "/api/v1/auth/verify", "192.0.2.10:4000", body).Code; code == http.StatusTooManyRequests {
			t.Fatalf("request %d from 192.0.2.10 was limited before its budget was spent", i+1)
		}
	}
	if code := remoteCall(t, s, "POST", "/api/v1/auth/verify", "192.0.2.10:4000", body).Code; code != http.StatusTooManyRequests {
		t.Errorf("third request from 192.0.2.10: status %d, want 429", code)
	}
	if code := remoteCall(t, s, "POST", "/api/v1/auth/verify", "192.0.2.11:4000", body).Code; code == http.StatusTooManyRequests {
		t.Errorf("request from 192.0.2.11: limited, want its own budget")
	}
}

// remoteCall sends one request from remoteAddr, as call does from the test's default address.
func remoteCall(t *testing.T, s *Server, method, path, remoteAddr string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != nil {
		req = jsonRequest(t, method, path, body)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(""))
	}
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}
