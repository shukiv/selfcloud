// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestClientGoneThroughTheMux: a request whose client went away, served by the whole handler with a canceled context,
// is logged as a disconnect below ERROR and counted as no server failure. This is the real path, not a unit call.
func TestClientGoneThroughTheMux(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	buf := &bytes.Buffer{}
	fx.s.log = slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	req := httptest.NewRequest("GET", "/api/v1/nodes/"+fx.c.ID+"/children", nil)
	req.Header.Set("Authorization", "Bearer "+fx.key)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(ctx)
	fx.s.Handler().ServeHTTP(httptest.NewRecorder(), req)

	log := buf.String()
	if strings.Contains(log, "level=ERROR") {
		t.Errorf("a request whose client went away logged at ERROR: %s", log)
	}
	if !strings.Contains(log, "client disconnected") {
		t.Errorf("the request is not logged as a client disconnect: %s", log)
	}
	if n := serverFailures(t, fx.s); n != 0 {
		t.Errorf("a request whose client went away counted as %d server failure(s)", n)
	}
}

// TestVisitorRefusalsOnAShortcutMatchAnOutsideItem: a link visitor's answer to a shortcut inside the link is the answer
// to an item outside it, on every route a visitor may call with a shortcut ID. A different status or code would tell
// the visitor that the shortcut exists.
func TestVisitorRefusalsOnAShortcutMatchAnOutsideItem(t *testing.T) {
	t.Parallel()
	fx := newAnyoneFixture(t)
	sc := makeShortcut(t, fx.sharingFixture, fx.c.ID, fx.deep.ID)
	scID := sc["id"].(string)
	outside := putNode(t, fx.s, fx.key, fx.fld.ID, "/a/elsewhere.txt", "outside")
	_, secret := fx.makeAnyone(t, fx.c.ID, nil)
	tok := fx.visitAnyone(t, secret, "")

	routes := []struct {
		method, suffix string
		body           any
	}{
		{"GET", "", nil},
		{"GET", "/children", nil},
		{"GET", "/content", nil},
		{"HEAD", "/content", nil},
		{"GET", "/versions", nil},
		{"POST", "/links", map[string]any{"disposition": "inline", "ttl": 60}},
		{"POST", "/share-links", map[string]any{"audience": "anyone"}},
		{"POST", "/shortcuts", map[string]any{"target": fx.deep.ID}},
		{"PATCH", "", map[string]any{"color": "red"}},
		{"POST", "/copy", map[string]any{}},
		{"DELETE", "", nil},
	}
	for _, rt := range routes {
		inside := call(t, fx.s, rt.method, "/api/v1/nodes/"+scID+rt.suffix, tok, rt.body)
		elsewhere := call(t, fx.s, rt.method, "/api/v1/nodes/"+outside.ID+rt.suffix, tok, rt.body)
		if inside.Code != elsewhere.Code {
			t.Errorf("%s %s: a shortcut answers %d, an item outside the link answers %d", rt.method, rt.suffix, inside.Code, elsewhere.Code)
			continue
		}
		if rt.method == "HEAD" || inside.Code < 400 {
			continue
		}
		if a, b := decodeError(t, inside), decodeError(t, elsewhere); !sameRefusal(a, b) {
			t.Errorf("%s %s: a shortcut answers %s %q, an item outside the link answers %s %q",
				rt.method, rt.suffix, a.Code, a.Message, b.Code, b.Message)
		}
	}
}
