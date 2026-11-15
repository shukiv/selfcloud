// SPDX-License-Identifier: AGPL-3.0-or-later

package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

// TestEscapedPathKeepsItsEscaping: a path the caller escaped (an email address holding a slash, say) and marked
// EscapedPath goes on the wire with its escapes kept, so one segment stays one segment.
func TestEscapedPathKeepsItsEscaping(t *testing.T) {
	t.Parallel()
	var gotPath, gotRaw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotRaw = r.URL.EscapedPath()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := newClient(t, srv.URL)
	for _, cse := range []struct{ segment, raw, decoded string }{
		{url.PathEscape("a/b@x.org"), "/api/v1/x/a%2Fb@x.org", "/api/v1/x/a/b@x.org"},
		{url.PathEscape("a+b@x.org"), "/api/v1/x/a+b@x.org", "/api/v1/x/a+b@x.org"},
		{url.PathEscape("a?b@x.org"), "/api/v1/x/a%3Fb@x.org", "/api/v1/x/a?b@x.org"},
		{"plain", "/api/v1/x/plain", "/api/v1/x/plain"},
		{"100%25@x.org", "/api/v1/x/100%25@x.org", "/api/v1/x/100%@x.org"},
	} {
		if _, err := c.Do(context.Background(), Request{Method: "DELETE", Path: "/api/v1/x/" + cse.segment, EscapedPath: true}); err != nil {
			t.Fatalf("%s: %v", cse.segment, err)
		}
		if gotRaw != cse.raw {
			t.Errorf("%s: wire path %q, want %q", cse.segment, gotRaw, cse.raw)
		}
		if gotPath != cse.decoded {
			t.Errorf("%s: decoded path %q, want %q", cse.segment, gotPath, cse.decoded)
		}
	}
}

// TestPlainPathWithPercentIsSentAsBefore: without EscapedPath a literal percent is the client's to escape, as today.
func TestPlainPathWithPercentIsSentAsBefore(t *testing.T) {
	t.Parallel()
	var gotPath, gotRaw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotRaw = r.URL.EscapedPath()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := newClient(t, srv.URL)
	if _, err := c.Do(context.Background(), Request{Method: "DELETE", Path: "/api/v1/x/100%@x.org"}); err != nil {
		t.Fatal(err)
	}
	if gotRaw != "/api/v1/x/100%25@x.org" {
		t.Errorf("wire path %q, want the percent escaped as today", gotRaw)
	}
	if gotPath != "/api/v1/x/100%@x.org" {
		t.Errorf("decoded path %q, want the literal percent", gotPath)
	}
}

// TestBadEscapedPathIsRefused: an escaped path that does not unescape, or does not escape back to itself, is refused
// before any request is sent.
func TestBadEscapedPathIsRefused(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := newClient(t, srv.URL)
	for _, path := range []string{
		"/api/v1/x/a%zzb@x.org", // not an escape
		"/api/v1/x/a%2",         // an escape cut short
		"/api/v1/x/a%2Fb c",     // unescapes, but does not escape back to itself
	} {
		if _, err := c.Do(context.Background(), Request{Method: "DELETE", Path: path, EscapedPath: true}); err == nil {
			t.Errorf("%s: no refusal", path)
		}
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("%d requests were sent; want none", n)
	}
}
