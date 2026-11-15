// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/store"
)

// playedItemJSON is one item of the played list as the route answers it.
type playedItemJSON struct {
	ID              string  `json:"id"`
	Type            string  `json:"type"`
	Role            string  `json:"role"`
	Place           string  `json:"place"`
	PositionSeconds float64 `json:"position_seconds"`
	DurationSeconds float64 `json:"duration_seconds"`
	PlayedAt        string  `json:"played_at"`
}

// playedFixture is a sharing fixture with five media files in /a/b that ann reaches as viewer, and the places she
// stopped at, newest first: v1.mp4 6 s of 60 s at 5000, a1.mp3 6 s of an unknown length at 4000, v2.mp4 3 s of 60 s
// at 3000, a2.mp3 55 s of 60 s at 2000 and v3.mp4 30 s of 60 s at 1000.
type playedFixture struct {
	*sharingFixture
	v1, v2, v3, a1, a2 nodeJSON
}

func newPlayedFixture(t *testing.T) *playedFixture {
	t.Helper()
	fx := newSharingFixture(t)
	p := &playedFixture{sharingFixture: fx}
	p.v1 = putNode(t, fx.s, fx.key, fx.fld.ID, "/a/b/v1.mp4", "v1")
	p.a1 = putNode(t, fx.s, fx.key, fx.fld.ID, "/a/b/a1.mp3", "a1")
	p.v2 = putNode(t, fx.s, fx.key, fx.fld.ID, "/a/b/v2.mp4", "v2")
	p.a2 = putNode(t, fx.s, fx.key, fx.fld.ID, "/a/b/a2.mp3", "a2")
	p.v3 = putNode(t, fx.s, fx.key, fx.fld.ID, "/a/b/v3.mp4", "v3")
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	p.seed(t, fx.ann.ID, p.v1.ID, 6000, 60000, 5000)
	p.seed(t, fx.ann.ID, p.a1.ID, 6000, 0, 4000)
	p.seed(t, fx.ann.ID, p.v2.ID, 3000, 60000, 3000)
	p.seed(t, fx.ann.ID, p.a2.ID, 55000, 60000, 2000)
	p.seed(t, fx.ann.ID, p.v3.ID, 30000, 60000, 1000)
	return p
}

// seed stores one place of userID in nodeID at the given Unix millisecond, as the resume route writes it.
func (p *playedFixture) seed(t *testing.T, userID, nodeID string, posMS, durMS, at int64) {
	t.Helper()
	err := p.s.store.Write(context.Background(), func(tx *store.Tx) error {
		return tx.SetMediaResume(context.Background(), p.acc.ID, userID, nodeID,
			store.MediaResume{PositionMS: posMS, DurationMS: durMS}, time.UnixMilli(at))
	})
	if err != nil {
		t.Fatal(err)
	}
}

// playedList reads one page of the played list.
func playedList(t *testing.T, p *playedFixture, sess testSession, query string) listResponse[playedItemJSON] {
	t.Helper()
	path := "/api/v1/media/played"
	if query != "" {
		path += "?" + query
	}
	return decode[listResponse[playedItemJSON]](t, callSession(t, p.s, sess, "GET", path, nil))
}

// playedIDsOf reads the node IDs of a page.
func playedIDsOf(page listResponse[playedItemJSON]) []string {
	ids := make([]string, len(page.Items))
	for i, it := range page.Items {
		ids[i] = it.ID
	}
	return ids
}

// TestPlayedListNewestFirst: the places come back latest first, each the node's JSON with the caller's role and place,
// the position and duration in seconds, and played_at the RFC 3339 time the place was stored.
func TestPlayedListNewestFirst(t *testing.T) {
	t.Parallel()
	p := newPlayedFixture(t)
	got := playedList(t, p, p.annSess, "")
	want := []string{p.v1.ID, p.a1.ID, p.v2.ID, p.a2.ID, p.v3.ID}
	if strings.Join(playedIDsOf(got), ",") != strings.Join(want, ",") {
		t.Fatalf("played = %v, want %v", playedIDsOf(got), want)
	}
	if got.NextCursor != "" {
		t.Errorf("next_cursor = %q on a page that holds every place", got.NextCursor)
	}
	at := []int64{5000, 4000, 3000, 2000, 1000}
	pos := []float64{6, 6, 3, 55, 30}
	dur := []float64{60, 0, 60, 60, 60}
	for i, it := range got.Items {
		if it.Role != "viewer" || it.Place != "b" {
			t.Errorf("item %d: role %q place %q, want viewer and b", i, it.Role, it.Place)
		}
		if it.PositionSeconds != pos[i] || it.DurationSeconds != dur[i] {
			t.Errorf("item %d: %v of %v, want %v of %v", i, it.PositionSeconds, it.DurationSeconds, pos[i], dur[i])
		}
		when, err := time.Parse(time.RFC3339, it.PlayedAt)
		if err != nil || !when.Equal(time.UnixMilli(at[i]).UTC()) {
			t.Errorf("item %d: played_at %q, want the RFC 3339 of %d (%v)", i, it.PlayedAt, at[i], err)
		}
	}
	if got.Items[0].Type != "video" || got.Items[1].Type != "audio" {
		t.Errorf("types = %q, %q, want video and audio", got.Items[0].Type, got.Items[1].Type)
	}
}

// TestPlayedListPaging: a limit of 2 over five places gives every place once, in order, and stops.
func TestPlayedListPaging(t *testing.T) {
	t.Parallel()
	p := newPlayedFixture(t)
	var got []string
	cursor := ""
	for i := 0; i < 10; i++ {
		q := url.Values{"limit": {"2"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		page := playedList(t, p, p.annSess, q.Encode())
		got = append(got, playedIDsOf(page)...)
		if page.NextCursor == "" {
			break
		}
		if len(page.Items) != 2 {
			t.Fatalf("page %d holds %d items with a next_cursor", i, len(page.Items))
		}
		cursor = page.NextCursor
	}
	want := []string{p.v1.ID, p.a1.ID, p.v2.ID, p.a2.ID, p.v3.ID}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("paged = %v, want %v", got, want)
	}
}

// TestPlayedTypeFilter: type=video and type=audio keep their kind; any other type value, another valid group included,
// is invalid_request naming type.
func TestPlayedTypeFilter(t *testing.T) {
	t.Parallel()
	p := newPlayedFixture(t)
	got := playedList(t, p, p.annSess, "type=video")
	if strings.Join(playedIDsOf(got), ",") != strings.Join([]string{p.v1.ID, p.v2.ID, p.v3.ID}, ",") {
		t.Errorf("video = %v", playedIDsOf(got))
	}
	got = playedList(t, p, p.annSess, "type=audio")
	if strings.Join(playedIDsOf(got), ",") != strings.Join([]string{p.a1.ID, p.a2.ID}, ",") {
		t.Errorf("audio = %v", playedIDsOf(got))
	}
	for _, typ := range []string{"image", "bogus", "folder", "shortcut", "pdf"} {
		e := wantError(t, callSession(t, p.s, p.annSess, "GET", "/api/v1/media/played?type="+typ, nil), 400, apierr.InvalidRequest)
		if !strings.Contains(e.Message, "type") {
			t.Errorf("type=%s: message %q does not name type", typ, e.Message)
		}
	}
}

// TestPlayedUnfinished: unfinished=true keeps the places worth resuming (6 s of 60 s, 6 s of an unknown length, 30 s
// mid-file) and drops 3 s and 55 s of 60 s; any other value than absent or true is invalid_request.
func TestPlayedUnfinished(t *testing.T) {
	t.Parallel()
	p := newPlayedFixture(t)
	got := playedList(t, p, p.annSess, "unfinished=true")
	if strings.Join(playedIDsOf(got), ",") != strings.Join([]string{p.v1.ID, p.a1.ID, p.v3.ID}, ",") {
		t.Errorf("unfinished = %v", playedIDsOf(got))
	}
	for _, val := range []string{"1", "false", "yes", "True"} {
		wantError(t, callSession(t, p.s, p.annSess, "GET", "/api/v1/media/played?unfinished="+val, nil), 400, apierr.InvalidRequest)
	}
	wantError(t, callSession(t, p.s, p.annSess, "GET", "/api/v1/media/played?unfinished=true&unfinished=true", nil), 400, apierr.InvalidRequest)
}

// TestPlayedIsPrivate: another user's places never appear, even when he reaches the same files; his own do.
func TestPlayedIsPrivate(t *testing.T) {
	t.Parallel()
	p := newPlayedFixture(t)
	p.setPerm(t, p.b.ID, p.bob.ID, "viewer")
	got := playedList(t, p, p.bobSess, "")
	if len(got.Items) != 0 {
		t.Fatalf("bob sees ann's places: %v", playedIDsOf(got))
	}
	p.seed(t, p.bob.ID, p.v1.ID, 1000, 60000, 9000)
	got = playedList(t, p, p.bobSess, "")
	if strings.Join(playedIDsOf(got), ",") != p.v1.ID || got.Items[0].PositionSeconds != 1 {
		t.Fatalf("bob sees %v, want only his own place in v1", got.Items)
	}
	got = playedList(t, p, p.annSess, "")
	if len(got.Items) != 5 {
		t.Errorf("ann's list changed: %v", playedIDsOf(got))
	}
}

// TestPlayedDropsUnreachableAndTrashed: a place whose node is trashed, or whose share was taken back, is left out, and
// the page is not refilled: it comes back shorter, and its cursor still opens the rest.
func TestPlayedDropsUnreachableAndTrashed(t *testing.T) {
	t.Parallel()
	p := newPlayedFixture(t)
	// One more place, in a file ann reaches through a share of /a/c that is then taken back.
	x1 := putNode(t, p.s, p.key, p.fld.ID, "/a/c/x1.mp4", "x1")
	p.setPerm(t, p.c.ID, p.ann.ID, "viewer")
	p.seed(t, p.ann.ID, x1.ID, 7000, 60000, 6000)
	wantStatus(t, call(t, p.s, "DELETE", "/api/v1/nodes/"+p.c.ID+"/permissions/"+p.ann.ID, p.key, nil), 204)
	// The newest place's file goes to the trash.
	wantStatus(t, call(t, p.s, "DELETE", "/api/v1/nodes/"+p.v1.ID, p.key, nil), 204)

	got := playedList(t, p, p.annSess, "limit=6")
	want := []string{p.a1.ID, p.v2.ID, p.a2.ID, p.v3.ID}
	if strings.Join(playedIDsOf(got), ",") != strings.Join(want, ",") {
		t.Fatalf("played = %v, want %v (the trashed and the unshared left out)", playedIDsOf(got), want)
	}
	if got.NextCursor == "" {
		t.Fatal("the short page lost its cursor: the page was refilled or the cursor dropped")
	}
	rest := playedList(t, p, p.annSess, "limit=6&cursor="+url.QueryEscape(got.NextCursor))
	if len(rest.Items) != 0 || rest.NextCursor != "" {
		t.Errorf("the rest = %+v, want an empty last page", rest)
	}
}

// TestPlayedCursorBindsTypeAndUnfinished: a cursor made under one type or unfinished flag is refused under another.
func TestPlayedCursorBindsTypeAndUnfinished(t *testing.T) {
	t.Parallel()
	p := newPlayedFixture(t)
	bad := func(query string) {
		t.Helper()
		wantError(t, callSession(t, p.s, p.annSess, "GET", "/api/v1/media/played?"+query, nil), 400, apierr.InvalidRequest)
	}
	vc := playedList(t, p, p.annSess, "type=video&limit=1").NextCursor
	if vc == "" {
		t.Fatal("no video cursor")
	}
	bad("type=audio&limit=1&cursor=" + url.QueryEscape(vc))
	bad("limit=1&cursor=" + url.QueryEscape(vc))
	next := playedList(t, p, p.annSess, "type=video&limit=1&cursor="+url.QueryEscape(vc))
	if strings.Join(playedIDsOf(next), ",") != p.v2.ID {
		t.Errorf("the video cursor continued with %v, want v2", playedIDsOf(next))
	}
	uc := playedList(t, p, p.annSess, "unfinished=true&limit=1").NextCursor
	if uc == "" {
		t.Fatal("no unfinished cursor")
	}
	bad("limit=1&cursor=" + url.QueryEscape(uc))
	bad("type=video&limit=1&cursor=" + url.QueryEscape(uc))
	next = playedList(t, p, p.annSess, "unfinished=true&limit=1&cursor="+url.QueryEscape(uc))
	if strings.Join(playedIDsOf(next), ",") != p.a1.ID {
		t.Errorf("the unfinished cursor continued with %v, want a1", playedIDsOf(next))
	}
	vuc := playedList(t, p, p.annSess, "type=video&unfinished=true&limit=1").NextCursor
	bad("type=video&limit=1&cursor=" + url.QueryEscape(vuc))
	bad("type=audio&unfinished=true&limit=1&cursor=" + url.QueryEscape(vuc))
}

// TestPlayedRefusesOtherCallers: a delegated token, a device token, an account key and an installation admin get the
// same refusal the resume routes give them; no credentials get the same 401.
func TestPlayedRefusesOtherCallers(t *testing.T) {
	t.Parallel()
	p := newPlayedFixture(t)
	read := mintFor(t, p.s, p.key, p.acc.ID, map[string]any{"user": p.ann.ID, "folders": []string{p.fld.ID},
		"node": p.b.ID, "permissions": "read"})
	write := mintFor(t, p.s, p.key, p.acc.ID, map[string]any{"user": p.ann.ID, "folders": []string{p.fld.ID},
		"node": p.b.ID, "permissions": "write"})
	_, dtok := approvedToken(t, p.s, p.annSess)
	resumePath := "/api/v1/nodes/" + p.v1.ID + "/resume"
	for name, cred := range map[string]string{"read token": read, "write token": write, "device token": dtok,
		"account key": p.key, "admin": p.admin} {
		want := wantError(t, call(t, p.s, "GET", resumePath, cred, nil), 403, apierr.Forbidden)
		got := wantError(t, call(t, p.s, "GET", "/api/v1/media/played", cred, nil), 403, apierr.Forbidden)
		if got.Message != want.Message {
			t.Errorf("%s: played answers %q, the resume route %q", name, got.Message, want.Message)
		}
	}
	wantError(t, call(t, p.s, "GET", "/api/v1/media/played", "", nil), 401, apierr.Unauthenticated)
}

// TestPlayedRefusesImpersonation: an admin signed in as a user gets the impersonation refusal, as on the resume routes.
func TestPlayedRefusesImpersonation(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	imp, _ := fx.signedInAsMember(t)
	e := wantError(t, callSession(t, fx.s, imp, "GET", "/api/v1/media/played", nil), 403, apierr.Forbidden)
	if e.Details["reason"] != reasonImpersonation {
		t.Errorf("reason = %v, want %s", e.Details["reason"], reasonImpersonation)
	}
}

// TestPlayedReadsWhatResumeWrote: a place stored through the resume route appears in the played list.
func TestPlayedReadsWhatResumeWrote(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	clip := putNode(t, fx.s, fx.key, fx.fld.ID, "/a/b/clip.mp4", "clip")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+clip.ID+"/resume",
		resumeIn{PositionSeconds: 12.5, DurationSeconds: 100}), 204)
	got := decode[listResponse[playedItemJSON]](t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/media/played", nil))
	if len(got.Items) != 1 || got.Items[0].ID != clip.ID {
		t.Fatalf("played = %+v, want the one clip", got.Items)
	}
	it := got.Items[0]
	if it.PositionSeconds != 12.5 || it.DurationSeconds != 100 || it.Role != "viewer" || it.Place != "b" {
		t.Errorf("item = %+v, want 12.5 of 100 at viewer in b", it)
	}
	if _, err := time.Parse(time.RFC3339, it.PlayedAt); err != nil {
		t.Errorf("played_at %q is not RFC 3339: %v", it.PlayedAt, err)
	}
}
