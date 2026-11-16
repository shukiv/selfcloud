// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// bmPath is the bookmarks route of a node; bmOne is one bookmark's route.
func bmPath(node string) string     { return "/api/v1/nodes/" + node + "/bookmarks" }
func bmOne(node, bmk string) string { return bmPath(node) + "/" + bmk }
func bmCreate(t *testing.T, fx *sharingFixture, sess testSession, node string, pos float64, label string) bookmarkJSON {
	t.Helper()
	rec := callSession(t, fx.s, sess, "POST", bmPath(node), map[string]any{"position_seconds": pos, "label": label})
	wantStatus(t, rec, 201)
	return decode[bookmarkJSON](t, rec)
}

// TestMediaBookmarkCRUD: the four routes' happy paths and shapes. An empty list is [] and never null; a create answers
// the bookmark; a relabel keeps its ID and position; a delete leaves an empty list.
func TestMediaBookmarkCRUD(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	path := bmPath(fx.deep.ID)

	empty := decode[bookmarkListJSON](t, callSession(t, fx.s, fx.annSess, "GET", path, nil))
	if empty.Bookmarks == nil || len(empty.Bookmarks) != 0 {
		t.Fatalf("empty list = %+v, want a non-nil empty slice", empty.Bookmarks)
	}
	b := bmCreate(t, fx, fx.annSess, fx.deep.ID, 12.5, "intro")
	if !strings.HasPrefix(b.ID, "bmk_") || b.PositionSeconds != 12.5 || b.Label != "intro" || b.CreatedAt.IsZero() {
		t.Fatalf("created bookmark = %+v, want a bmk_ ID, 12.5 s, label intro and a creation time", b)
	}
	list := decode[bookmarkListJSON](t, callSession(t, fx.s, fx.annSess, "GET", path, nil))
	if len(list.Bookmarks) != 1 || list.Bookmarks[0].ID != b.ID {
		t.Fatalf("list = %+v, want the one created bookmark", list.Bookmarks)
	}
	rec := callSession(t, fx.s, fx.annSess, "PATCH", bmOne(fx.deep.ID, b.ID), map[string]any{"label": "renamed"})
	wantStatus(t, rec, 200)
	got := decode[bookmarkJSON](t, rec)
	if got.ID != b.ID || got.PositionSeconds != 12.5 || got.Label != "renamed" {
		t.Fatalf("relabelled = %+v, want the same ID and position with label renamed", got)
	}
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", bmOne(fx.deep.ID, b.ID), nil), 204)
	after := decode[bookmarkListJSON](t, callSession(t, fx.s, fx.annSess, "GET", path, nil))
	if len(after.Bookmarks) != 0 {
		t.Fatalf("after delete, list = %+v, want empty", after.Bookmarks)
	}
	// An empty label is allowed, and spaces around a label are trimmed.
	blank := bmCreate(t, fx, fx.annSess, fx.deep.ID, 0, "   ")
	if blank.Label != "" {
		t.Fatalf("blank label = %q, want empty", blank.Label)
	}
	trimmed := bmCreate(t, fx, fx.annSess, fx.deep.ID, 1, "  chorus  ")
	if trimmed.Label != "chorus" {
		t.Fatalf("trimmed label = %q, want chorus", trimmed.Label)
	}
}

// TestMediaBookmarkIsPrivate: another member of the account cannot list, relabel or delete my bookmark, and sees only
// their own list once they reach the file.
func TestMediaBookmarkIsPrivate(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	mine := bmCreate(t, fx, fx.annSess, fx.deep.ID, 30, "mine")

	// Bob does not reach the file: every route is not_found, the same as for a file that does not exist.
	wantError(t, callSession(t, fx.s, fx.bobSess, "GET", bmPath(fx.deep.ID), nil), 404, apierr.NotFound)
	wantError(t, callSession(t, fx.s, fx.bobSess, "PATCH", bmOne(fx.deep.ID, mine.ID), map[string]any{"label": "x"}), 404, apierr.NotFound)
	wantError(t, callSession(t, fx.s, fx.bobSess, "DELETE", bmOne(fx.deep.ID, mine.ID), nil), 404, apierr.NotFound)

	// Once Bob reaches it, he reads his own list (none of ann's), and ann's bookmark is still not his to change.
	fx.setPerm(t, fx.b.ID, fx.bob.ID, "viewer")
	bobList := decode[bookmarkListJSON](t, callSession(t, fx.s, fx.bobSess, "GET", bmPath(fx.deep.ID), nil))
	if len(bobList.Bookmarks) != 0 {
		t.Fatalf("bob lists %+v; want none of his own, and not ann's", bobList.Bookmarks)
	}
	wantError(t, callSession(t, fx.s, fx.bobSess, "PATCH", bmOne(fx.deep.ID, mine.ID), map[string]any{"label": "stolen"}), 404, apierr.NotFound)
	wantError(t, callSession(t, fx.s, fx.bobSess, "DELETE", bmOne(fx.deep.ID, mine.ID), nil), 404, apierr.NotFound)
	his := bmCreate(t, fx, fx.bobSess, fx.deep.ID, 40, "his")
	bobList = decode[bookmarkListJSON](t, callSession(t, fx.s, fx.bobSess, "GET", bmPath(fx.deep.ID), nil))
	if len(bobList.Bookmarks) != 1 || bobList.Bookmarks[0].ID != his.ID {
		t.Fatalf("bob lists %+v, want only his own bookmark", bobList.Bookmarks)
	}
	annList := decode[bookmarkListJSON](t, callSession(t, fx.s, fx.annSess, "GET", bmPath(fx.deep.ID), nil))
	if len(annList.Bookmarks) != 1 || annList.Bookmarks[0].ID != mine.ID || annList.Bookmarks[0].Label != "mine" {
		t.Fatalf("ann lists %+v, want only her own untouched bookmark", annList.Bookmarks)
	}
}

// TestMediaBookmarkFolderAndUnreachable: a folder has no bookmarks; a file the caller cannot reach is not_found; and a
// reach revoked during the write stops the create, as the resume place is stopped.
func TestMediaBookmarkFolderAndUnreachable(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	// A folder is not a file: not_found on the write routes.
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", bmPath(fx.b.ID), map[string]any{"position_seconds": 1}), 404, apierr.NotFound)
	// A file the caller does not reach: not_found.
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", bmPath(fx.deep.ID), nil), 404, apierr.NotFound)
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", bmPath(fx.deep.ID), map[string]any{"position_seconds": 1}), 404, apierr.NotFound)

	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	b := bmCreate(t, fx, fx.annSess, fx.deep.ID, 5, "keep")
	// A revoked reach stops the next write.
	fx.revoke(t, fx.b.ID, fx.ann.ID)
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", bmPath(fx.deep.ID), map[string]any{"position_seconds": 2}), 404, apierr.NotFound)
	wantError(t, callSession(t, fx.s, fx.annSess, "PATCH", bmOne(fx.deep.ID, b.ID), map[string]any{"label": "x"}), 404, apierr.NotFound)
	wantError(t, callSession(t, fx.s, fx.annSess, "DELETE", bmOne(fx.deep.ID, b.ID), nil), 404, apierr.NotFound)

	// The reach is checked inside the write: a grant revoked between the body read and the transaction stops the create.
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	onceBefore(fx.s, func() { fx.revoke(t, fx.b.ID, fx.ann.ID) })
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", bmPath(fx.deep.ID), map[string]any{"position_seconds": 3}), 404, apierr.NotFound)
}

// TestMediaBookmarkTokensAndKeysHaveNoPart: a delegated token, even one that acts for the user with write, and the
// account key, are refused on every bookmark route.
func TestMediaBookmarkTokensAndKeysHaveNoPart(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	b := bmCreate(t, fx, fx.annSess, fx.deep.ID, 10, "mine")
	tok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"user": fx.ann.ID, "folders": []string{fx.fld.ID}, "permissions": "write"})
	for _, cred := range []string{tok, fx.key} {
		wantError(t, call(t, fx.s, "GET", bmPath(fx.deep.ID), cred, nil), 403, apierr.Forbidden)
		wantError(t, call(t, fx.s, "POST", bmPath(fx.deep.ID), cred, map[string]any{"position_seconds": 1}), 403, apierr.Forbidden)
		wantError(t, call(t, fx.s, "PATCH", bmOne(fx.deep.ID, b.ID), cred, map[string]any{"label": "x"}), 403, apierr.Forbidden)
		wantError(t, call(t, fx.s, "DELETE", bmOne(fx.deep.ID, b.ID), cred, nil), 403, apierr.Forbidden)
	}
	// The user's own bookmark is still there.
	if list := decode[bookmarkListJSON](t, callSession(t, fx.s, fx.annSess, "GET", bmPath(fx.deep.ID), nil)); len(list.Bookmarks) != 1 {
		t.Errorf("user lists %d bookmarks, want 1", len(list.Bookmarks))
	}
}

// TestMediaBookmarkLinkVisitorRefused: a visitor of a link that anyone can open is refused by the route table on each
// bookmark route, before the node is looked at.
func TestMediaBookmarkLinkVisitorRefused(t *testing.T) {
	t.Parallel()
	fx := newAnyoneFixture(t)
	root := fx.rootOf(t, fx.fld.ID)
	_, secret := fx.makeAnyone(t, root, nil)
	visitor := fx.visitAnyone(t, secret, "")
	calls := []struct {
		method, path string
		body         any
	}{
		{"GET", bmPath(fx.deep.ID), nil},
		{"POST", bmPath(fx.deep.ID), map[string]any{"position_seconds": 1}},
		{"PATCH", bmOne(fx.deep.ID, id.New("bmk")), map[string]any{"label": "x"}},
		{"DELETE", bmOne(fx.deep.ID, id.New("bmk")), nil},
	}
	for _, c := range calls {
		rec := call(t, fx.s, c.method, c.path, visitor, c.body)
		e := wantError(t, rec, http.StatusForbidden, apierr.Forbidden)
		if e.Details["reason"] != "link_route" {
			t.Fatalf("%s %s by a link visitor: reason %v, want link_route", c.method, c.path, e.Details["reason"])
		}
	}
}

// TestMediaBookmarkBadInput: a position that is negative or past the bound is invalid_request; a label past 100 runes or
// holding a control character is invalid_request naming the field, while 100 runes are kept; a bad node or bookmark ID
// is not_found.
func TestMediaBookmarkBadInput(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	path := bmPath(fx.deep.ID)
	for _, pos := range []float64{-1, 10_000_001} {
		wantError(t, callSession(t, fx.s, fx.annSess, "POST", path, map[string]any{"position_seconds": pos}), 400, apierr.InvalidRequest)
	}
	long := strings.Repeat("a", 101)
	e := wantError(t, callSession(t, fx.s, fx.annSess, "POST", path, map[string]any{"position_seconds": 1, "label": long}), 400, apierr.InvalidRequest)
	if !strings.Contains(e.Message, "label") {
		t.Errorf("long label error %q does not name label", e.Message)
	}
	e = wantError(t, callSession(t, fx.s, fx.annSess, "POST", path, map[string]any{"position_seconds": 1, "label": "a\nb"}), 400, apierr.InvalidRequest)
	if !strings.Contains(e.Message, "label") {
		t.Errorf("control character error %q does not name label", e.Message)
	}
	ok := bmCreate(t, fx, fx.annSess, fx.deep.ID, 1, strings.Repeat("a", 100))
	if len([]rune(ok.Label)) != 100 {
		t.Fatalf("100-rune label was not kept: %d runes", len([]rune(ok.Label)))
	}
	// A relabel obeys the same label rules.
	wantError(t, callSession(t, fx.s, fx.annSess, "PATCH", bmOne(fx.deep.ID, ok.ID), map[string]any{"label": long}), 400, apierr.InvalidRequest)
	// Bad IDs are not_found.
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", bmPath("nod_bad"), nil), 404, apierr.NotFound)
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", bmPath("nod_bad"), map[string]any{"position_seconds": 1}), 404, apierr.NotFound)
	wantError(t, callSession(t, fx.s, fx.annSess, "PATCH", bmOne(fx.deep.ID, "bmk_bad"), map[string]any{"label": "x"}), 404, apierr.NotFound)
	wantError(t, callSession(t, fx.s, fx.annSess, "DELETE", bmOne(fx.deep.ID, "bmk_bad"), nil), 404, apierr.NotFound)
	// A well-formed bookmark ID that is not this person's in this file is not_found too.
	wantError(t, callSession(t, fx.s, fx.annSess, "DELETE", bmOne(fx.deep.ID, id.New("bmk")), nil), 404, apierr.NotFound)
}

// TestMediaBookmarkCap: a person keeps at most 200 bookmarks in a file; the 201st is conflict with reason limit.
func TestMediaBookmarkCap(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	fx.s.limits[bookmarkWriteRate] = newLimiter(1000)
	ctx := context.Background()
	// Filled in the store: the route's own rate limit is not the subject here.
	if err := fx.s.store.Write(ctx, func(tx *store.Tx) error {
		for i := 0; i < maxBookmarksPerFile; i++ {
			if err := tx.AddMediaBookmark(ctx, fx.acc.ID, fx.ann.ID, fx.deep.ID,
				store.MediaBookmark{ID: id.New("bmk"), PositionMS: int64(i), Label: "x"}, store.Now()); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if list := decode[bookmarkListJSON](t, callSession(t, fx.s, fx.annSess, "GET", bmPath(fx.deep.ID), nil)); len(list.Bookmarks) != maxBookmarksPerFile {
		t.Fatalf("list holds %d bookmarks, want %d", len(list.Bookmarks), maxBookmarksPerFile)
	}
	e := wantError(t, callSession(t, fx.s, fx.annSess, "POST", bmPath(fx.deep.ID), map[string]any{"position_seconds": 1}), 409, apierr.Conflict)
	if e.Details["reason"] != "limit" || e.Details["limit"] != float64(maxBookmarksPerFile) {
		t.Fatalf("cap details = %v, want reason limit and limit %d", e.Details, maxBookmarksPerFile)
	}
	// Another file of the same user is not capped by this one.
	fx.setPerm(t, fx.c.ID, fx.ann.ID, "viewer")
	bmCreate(t, fx, fx.annSess, fx.sib.ID, 1, "other file")
}

// TestMediaBookmarkIsRateLimited: a user's bookmark writes are charged to a bucket of their own before the body is read,
// so a spent bucket answers 429 (even for a bad body) and another user is not refused by it.
func TestMediaBookmarkIsRateLimited(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	fx.setPerm(t, fx.b.ID, fx.bob.ID, "viewer")
	fx.s.limits[bookmarkWriteRate] = newLimiter(60)
	path := bmPath(fx.deep.ID)
	for i := 0; i < 60; i++ {
		wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", path, map[string]any{"position_seconds": float64(i)}), 201)
	}
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", path, map[string]any{"position_seconds": 1}), 429, apierr.RateLimited)
	// The bucket is spent before the body is read: a bad body is refused by the limit, not by validation.
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", path, map[string]any{"position_seconds": -1}), 429, apierr.RateLimited)
	// Bob's own bucket is untouched.
	wantStatus(t, callSession(t, fx.s, fx.bobSess, "POST", path, map[string]any{"position_seconds": 1}), 201)
}
