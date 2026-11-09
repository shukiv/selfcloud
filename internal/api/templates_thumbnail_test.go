// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"
)

// The thumbnail of a built-in template (nt2, decision 105): a PNG, cached privately for an hour, behind the same gate as the
// listing: auth, office editing on, and no share link (link_route).

var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// thumbnailPath is the route for a template ID. The web encodes the ID, as the colon in builtin:letter is a reserved character.
func thumbnailPath(id string) string {
	return "/api/v1/templates/" + url.PathEscape(id) + "/thumbnail"
}

// A built-in's picture is a PNG, sent as image/png with the private cache rule.
func TestTemplateThumbnailIsAPNGWithCacheRule(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	for _, id := range []string{"builtin:letter", "builtin:budget", "builtin:lo-beehive", "builtin:lo-letter-serif"} {
		rec := call(t, fx.s, "GET", thumbnailPath(id), fx.key, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d, want 200", id, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
			t.Errorf("%s: Content-Type %q, want image/png", id, ct)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "private, max-age=3600" {
			t.Errorf("%s: Cache-Control %q, want private, max-age=3600", id, cc)
		}
		if !bytes.HasPrefix(rec.Body.Bytes(), pngSignature) {
			t.Errorf("%s: the body is not a PNG", id)
		}
	}
}

// The web sends the ID with encodeURIComponent, so the colon arrives as %3A. The route must still find the picture.
func TestTemplateThumbnailAcceptsTheEncodedColon(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	rec := call(t, fx.s, "GET", "/api/v1/templates/builtin%3Aletter/thumbnail", fx.key, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("encoded colon: status %d, want 200", rec.Code)
	}
	if !bytes.HasPrefix(rec.Body.Bytes(), pngSignature) {
		t.Errorf("encoded colon: the body is not a PNG")
	}
}

// An unknown built-in, an empty ID and an account's template have no picture: not_found.
func TestTemplateThumbnailIsNotFoundForUnknownAndAccountTemplates(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	dir := newDirectoryAt(t, fx, "/Templates")
	account := nodeOf(t, newDocumentCall(t, fx.s, fx.key, dir.ID, map[string]any{"type": "docx"}))
	setTemplatesFolder(t, fx, dir.ID)
	for _, id := range []string{"builtin:nope", "builtin:", account.ID, fx.doc.ID} {
		wantStatus(t, call(t, fx.s, "GET", thumbnailPath(id), fx.key, nil), 404)
	}
}

// The picture is answered only with office editing on; otherwise the route is not found, as the listing is.
func TestTemplateThumbnailIsNotFoundWithOfficeOff(t *testing.T) {
	t.Parallel()
	s, _, key, _, _ := quotaSetup(t, 0)
	wantStatus(t, call(t, s, "GET", thumbnailPath("builtin:letter"), key, nil), 404)
}

// A person who is not signed in is refused by the route, as the listing is.
func TestTemplateThumbnailNeedsAuth(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	wantStatus(t, call(t, fx.s, "GET", thumbnailPath("builtin:letter"), "", nil), 401)
}

// A share-link visitor is refused by the route with link_route, before the handler runs (phase 1 API contract).
func TestTemplateThumbnailRefusesAShareLinkVisitor(t *testing.T) {
	t.Parallel()
	fx := newAnyoneFixture(t)
	root := fx.rootOf(t, fx.fld.ID)
	_, secret := fx.makeAnyone(t, root, nil)
	visitor := fx.visitAnyone(t, secret, "")
	rec := fx.visitor(t, visitor, "GET", thumbnailPath("builtin:letter"))
	if e := wantError(t, rec, http.StatusForbidden, "forbidden"); e.Details["reason"] != "link_route" {
		t.Errorf("thumbnail: reason %v, want link_route", e.Details["reason"])
	}
}
