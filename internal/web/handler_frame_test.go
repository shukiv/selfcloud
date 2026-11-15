// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"strings"
	"testing"
)

// TestFrameAncestorsHook: the hook's origins reach the page's frame-ancestors and drop X-Frame-Options;
// assets and the not-built page keep 'none' and DENY; a nil or empty hook changes nothing (decision 197).
func TestFrameAncestorsHook(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t, testFiles(), Options{FrameAncestors: func(r *http.Request) []string {
		if r.URL.Query().Get("frame") == "yes" {
			return []string{"https://host.example"}
		}
		return nil
	}})

	rec := get(t, h, http.MethodGet, "/?frame=yes")
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "frame-ancestors 'self' https://host.example") {
		t.Errorf("page CSP %q lacks the host origin", csp)
	}
	if rec.Header().Get("X-Frame-Options") != "" {
		t.Errorf("framable page has X-Frame-Options %q", rec.Header().Get("X-Frame-Options"))
	}

	// The hook says none: the page keeps the strict policy.
	rec = get(t, h, http.MethodGet, "/")
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") ||
		rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("unframable page headers %v", rec.Header())
	}

	// An asset never frames, even when the hook would allow it.
	rec = get(t, h, http.MethodGet, "/assets/app-abc123.js?frame=yes")
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") ||
		rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("asset headers %v", rec.Header())
	}

	// A nil hook leaves every page strict.
	plain := newTestHandler(t, testFiles(), Options{})
	rec = get(t, plain, http.MethodGet, "/?frame=yes")
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") ||
		rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("nil-hook page headers %v", rec.Header())
	}
}
