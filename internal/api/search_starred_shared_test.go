// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
)

// TestSearchStarredSharedParams: starred and shared are empty or true; anything else names the parameter. Both are
// filters, so either runs without a query.
func TestSearchStarredSharedParams(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	for _, c := range []struct{ query, message string }{
		{"starred=false", "starred must be true"},
		{"starred=1", "starred must be true"},
		{"shared=false", "shared must be true"},
		{"shared=yes", "shared must be true"},
	} {
		rec := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/search?"+c.query, nil)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), c.message) {
			t.Errorf("%s: %d %s; want 400 naming %q", c.query, rec.Code, rec.Body, c.message)
		}
		wantError(t, rec, 400, apierr.InvalidRequest)
	}
	wantStatus(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/search?starred=true", nil), 200)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/search?shared=true", nil), 200)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/search?starred=true&shared=true", nil), 200)
}

// TestSearchStarredSharedNeedAUser: an account key and a token it minted have no user of their own, so starred and
// shared are forbidden for them, as on the starred and shared pages. A user's delegated token passes.
func TestSearchStarredSharedNeedAUser(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	for _, query := range []string{"starred=true", "shared=true"} {
		wantError(t, call(t, fx.s, "GET", "/api/v1/search?"+query, fx.key, nil), 403, apierr.Forbidden)
		keyTok := mintToken(t, fx.s, fx.key, fx.acc.ID, fx.fld.ID, "")
		wantError(t, call(t, fx.s, "GET", "/api/v1/search?"+query, keyTok, nil), 403, apierr.Forbidden)
	}
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "editor")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/star", nil), 204)
	annTok := mintToken(t, fx.s, fx.key, fx.acc.ID, fx.fld.ID, fx.ann.ID)
	if got := hitNames(t, call(t, fx.s, "GET", "/api/v1/search?starred=true", annTok, nil)); strings.Join(got, "|") != "file.txt" {
		t.Fatalf("the user's token found %q, want its user's star", got)
	}
	// The token is confined to its folders: a star outside them never shows, and neither does a shared item.
	other := newFolder(t, fx.s, fx.key, fx.acc.ID, "Other")
	otherTok := mintToken(t, fx.s, fx.key, fx.acc.ID, other.ID, fx.ann.ID)
	if got := hitNames(t, call(t, fx.s, "GET", "/api/v1/search?starred=true", otherTok, nil)); len(got) != 0 {
		t.Fatalf("a token outside the star's folder found %q", got)
	}
	if got := hitNames(t, call(t, fx.s, "GET", "/api/v1/search?shared=true", otherTok, nil)); len(got) != 0 {
		t.Fatalf("a token with no shared folder found %q", got)
	}
}

// TestSearchStarredIsTheCallersOwn: only the caller's own stars decide a hit. Another user's star on the same node does
// not count, and removing that other star changes nothing for the caller.
func TestSearchStarredIsTheCallersOwn(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "editor")
	fx.setPerm(t, fx.b.ID, fx.bob.ID, "viewer")
	wantStatus(t, callSession(t, fx.s, fx.bobSess, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/star", nil), 204)
	if got := hitNames(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/search?starred=true", nil)); len(got) != 0 {
		t.Fatalf("another user's star gave ann %q", got)
	}
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/star", nil), 204)
	if got := hitNames(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/search?starred=true", nil)); strings.Join(got, "|") != "file.txt" {
		t.Fatalf("ann's own star found %q, want file.txt", got)
	}
	if got := hitNames(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/search?q=file&starred=true", nil)); strings.Join(got, "|") != "file.txt" {
		t.Fatalf("starred with a query found %q, want file.txt", got)
	}
	wantStatus(t, callSession(t, fx.s, fx.bobSess, "DELETE", "/api/v1/nodes/"+fx.deep.ID+"/star", nil), 204)
	if got := hitNames(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/search?starred=true", nil)); strings.Join(got, "|") != "file.txt" {
		t.Fatalf("after bob unstarred, ann found %q; her own star stands", got)
	}
}

// TestSearchStarredNeverShowsTheUnreachable: a star on a node the caller no longer reaches, or one in the trash, stays
// in the database but never shows as a hit.
func TestSearchStarredNeverShowsTheUnreachable(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "editor")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/star", nil), 204)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.b.ID+"/permissions/"+fx.ann.ID, fx.key, nil), 204)
	if got := hitNames(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/search?starred=true", nil)); len(got) != 0 {
		t.Fatalf("a star on an unreachable node showed: %q", got)
	}
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "editor")
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.deep.ID, fx.key, nil), 204)
	if got := hitNames(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/search?starred=true", nil)); len(got) != 0 {
		t.Fatalf("a star on a trashed node showed: %q", got)
	}
}

// sharedNames runs shared=true as a session and returns the hit names, sorted.
func sharedNames(t *testing.T, fx *sharingFixture, sess testSession, extra string) []string {
	t.Helper()
	path := "/api/v1/search?shared=true"
	if extra != "" {
		path += "&" + extra
	}
	got := hitNames(t, callSession(t, fx.s, sess, "GET", path, nil))
	sort.Strings(got)
	return got
}

// TestSearchSharedFromAnotherMembersMyFiles: a member sees an item shared to them from another member's My files, and a
// file inside a shared folder; their own My files never show.
func TestSearchSharedFromAnotherMembersMyFiles(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	pfAnn := fx.personalFolder(t, fx.annSess)
	putNode(t, fx.s, fx.key, pfAnn, "/ann-private.txt", "x")
	pfBob := fx.personalFolder(t, fx.bobSess)
	gift := putNode(t, fx.s, fx.key, pfBob, "/gift.txt", "x")
	dir := mkdirs(t, fx.s, fx.key, pfBob, "/shared-dir")
	putNode(t, fx.s, fx.key, pfBob, "/shared-dir/inside.txt", "x")
	fx.setPerm(t, gift.ID, fx.ann.ID, "viewer")
	fx.setPerm(t, dir.ID, fx.ann.ID, "viewer")
	want := []string{"gift.txt", "inside.txt", "shared-dir"}
	if got := sharedNames(t, fx, fx.annSess, ""); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("ann's shared items = %q, want %q", got, want)
	}
	if got := sharedNames(t, fx, fx.annSess, "q=gift"); strings.Join(got, "|") != "gift.txt" {
		t.Fatalf("shared with a query = %q, want gift.txt", got)
	}
	// Bob, who shared the items, does not see them as shared with him: they are his own.
	if got := sharedNames(t, fx, fx.bobSess, ""); len(got) != 0 {
		t.Fatalf("the sharer's own items showed as shared: %q", got)
	}
}

// TestSearchSharedTeamRootGrantExcludesTheSpace: a member granted on a team space's root, directly or through a group,
// gets no shared hits from it: the space is reached already. A grant on one subfolder inside the space gives that
// subfolder and the files below it, and nothing beside it.
func TestSearchSharedTeamRootGrantExcludesTheSpace(t *testing.T) {
	t.Parallel()
	t.Run("direct root grant", func(t *testing.T) {
		t.Parallel()
		fx := newSharingFixture(t)
		fx.setPerm(t, fx.rootOf(t, fx.fld.ID), fx.ann.ID, "viewer")
		if got := sharedNames(t, fx, fx.annSess, ""); len(got) != 0 {
			t.Fatalf("a root-granted member's shared hits = %q, want none from the space", got)
		}
	})
	t.Run("group root grant", func(t *testing.T) {
		t.Parallel()
		fx := newSharingFixture(t)
		grp := fx.group(t, "Staff")
		fx.inGroup(t, grp, fx.ann.ID, true)
		fx.setPerm(t, fx.rootOf(t, fx.fld.ID), grp, "viewer")
		if got := sharedNames(t, fx, fx.annSess, ""); len(got) != 0 {
			t.Fatalf("a group root-granted member's shared hits = %q, want none from the space", got)
		}
	})
	t.Run("subfolder grant", func(t *testing.T) {
		t.Parallel()
		fx := newSharingFixture(t)
		fx.setPerm(t, fx.b.ID, fx.ann.ID, "editor")
		want := []string{"b", "file.txt"}
		if got := sharedNames(t, fx, fx.annSess, ""); strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("a subfolder-granted member's shared hits = %q, want %q", got, want)
		}
	})
}

// TestSearchSharedExpiredRootGrantNoLongerExcludes: an expired grant on the space's root no longer excludes the space's
// items: a member who also holds a live subfolder grant sees that subfolder and its files again.
func TestSearchSharedExpiredRootGrantNoLongerExcludes(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	root := fx.rootOf(t, fx.fld.ID)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "editor")
	wantStatus(t, call(t, fx.s, "PUT", "/api/v1/nodes/"+root+"/permissions/"+fx.ann.ID, fx.key,
		map[string]any{"role": "viewer", "notify": false,
			"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}), 200)
	if got := sharedNames(t, fx, fx.annSess, ""); len(got) != 0 {
		t.Fatalf("a live root grant still excludes nothing: %q", got)
	}
	rawExec(t, fx.s, "UPDATE permissions SET expires_at = 1 WHERE node_id = ? AND subject_kind = 'user' AND subject_id = ?",
		root, fx.ann.ID)
	want := []string{"b", "file.txt"}
	if got := sharedNames(t, fx, fx.annSess, ""); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("after the root grant expired, shared hits = %q, want %q", got, want)
	}
}

// TestSearchSharedOwnerGetsNoTeamItems: an account owner reaches every team space already, so shared gives them no
// team-space item, even one they hold an explicit grant on; their own My files never show either.
func TestSearchSharedOwnerGetsNoTeamItems(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	owner, ownerSess := ownerUser(t, fx.s, fx.key, fx.acc.ID, "owner@example.com")
	fx.setPerm(t, fx.b.ID, owner.ID, "editor")
	pf := fx.personalFolder(t, ownerSess)
	putNode(t, fx.s, fx.key, pf, "/owner-private.txt", "x")
	if got := sharedNames(t, fx, ownerSess, ""); len(got) != 0 {
		t.Fatalf("the owner's shared hits = %q, want none", got)
	}
}
