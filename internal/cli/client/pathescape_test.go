// SPDX-License-Identifier: AGPL-3.0-or-later

package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestEscapedPathKeepsItsEscaping: a path the caller escaped (an email address holding a slash, say) goes on the wire
// with its escapes kept, so one segment stays one segment; a path with no escape is sent as before.
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
		{"100%@x.org", "/api/v1/x/100%25@x.org", "/api/v1/x/100%@x.org"},
	} {
		if _, err := c.Do(context.Background(), Request{Method: "DELETE", Path: "/api/v1/x/" + cse.segment}); err != nil {
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
