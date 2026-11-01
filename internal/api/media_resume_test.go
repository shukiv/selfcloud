// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
)

// resumeIn is the body of a PUT to the resume route.
type resumeIn struct {
	PositionSeconds float64 `json:"position_seconds"`
	DurationSeconds float64 `json:"duration_seconds"`
}

func TestMediaResumeRoundTrip(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	path := "/api/v1/nodes/" + fx.deep.ID + "/resume"

	// Nothing stored: zero, with no updated_at.
	got := decode[resumeAnswer](t, callSession(t, fx.s, fx.annSess, "GET", path, nil))
	if got.PositionSeconds != 0 || got.UpdatedAt != nil {
		t.Fatalf("no place: got %+v, want zero and no updated_at", got)
	}
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", path, resumeIn{PositionSeconds: 12.5, DurationSeconds: 100}), 204)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", path, resumeIn{PositionSeconds: 42.25, DurationSeconds: 100}), 204)
	got = decode[resumeAnswer](t, callSession(t, fx.s, fx.annSess, "GET", path, nil))
	if got.PositionSeconds != 42.25 || got.DurationSeconds != 100 || got.UpdatedAt == nil {
		t.Fatalf("after two writes: got %+v, want 42.25 of 100 with updated_at", got)
	}
}

// TestMediaResumeIsPrivate: another user, even one who reaches the same file, never reads the place of the first user.
func TestMediaResumeIsPrivate(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	path := "/api/v1/nodes/" + fx.deep.ID + "/resume"
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", path, resumeIn{PositionSeconds: 30, DurationSeconds: 100}), 204)

	// Bob does not reach the file: not_found, the same as for a file that does not exist.
	wantError(t, callSession(t, fx.s, fx.bobSess, "GET", path, nil), 404, apierr.NotFound)
	wantError(t, callSession(t, fx.s, fx.bobSess, "PUT", path, resumeIn{PositionSeconds: 1, DurationSeconds: 2}), 404, apierr.NotFound)

	// Once Bob reaches it, he reads his own place, which is none.
	fx.setPerm(t, fx.b.ID, fx.bob.ID, "viewer")
	got := decode[resumeAnswer](t, callSession(t, fx.s, fx.bobSess, "GET", path, nil))
	if got.PositionSeconds != 0 || got.UpdatedAt != nil {
		t.Fatalf("bob reads %+v; want no place of his own, and not ann's 30", got)
	}
}

// TestMediaResumeIsRateLimited: a user's writes of a place are charged to a bucket of their own, so a spent bucket answers
// 429 and another user is not refused by it.
func TestMediaResumeIsRateLimited(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	fx.setPerm(t, fx.b.ID, fx.bob.ID, "viewer")
	fx.s.limits[resumeWriteRate] = newLimiter(2)
	path := "/api/v1/nodes/" + fx.deep.ID + "/resume"
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", path, resumeIn{PositionSeconds: 1, DurationSeconds: 100}), 204)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", path, resumeIn{PositionSeconds: 2, DurationSeconds: 100}), 204)
	wantError(t, callSession(t, fx.s, fx.annSess, "PUT", path, resumeIn{PositionSeconds: 3, DurationSeconds: 100}), 429, apierr.RateLimited)
	wantStatus(t, callSession(t, fx.s, fx.bobSess, "PUT", path, resumeIn{PositionSeconds: 4, DurationSeconds: 100}), 204)
}

// TestMediaResumeRefusesBadInput: numbers that are negative, past the bound or past the duration are invalid_request; a
// folder has no place; an account key is not a user and has none.
func TestMediaResumeRefusesBadInput(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	path := "/api/v1/nodes/" + fx.deep.ID + "/resume"
	bad := []resumeIn{
		{PositionSeconds: -1, DurationSeconds: 100},
		{PositionSeconds: 1, DurationSeconds: -5},
		{PositionSeconds: 101, DurationSeconds: 100},
		{PositionSeconds: 1, DurationSeconds: 10_000_001},
		{PositionSeconds: 10_000_001, DurationSeconds: 0},
	}
	for _, in := range bad {
		wantError(t, callSession(t, fx.s, fx.annSess, "PUT", path, in), 400, apierr.InvalidRequest)
	}
	// A duration of 0 is unknown, so a position within the bound is kept.
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", path, resumeIn{PositionSeconds: 5, DurationSeconds: 0}), 204)

	// A folder has no place.
	wantError(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+fx.b.ID+"/resume", resumeIn{PositionSeconds: 1, DurationSeconds: 2}), 404, apierr.NotFound)
	// An account key is not a user: it has no place.
	wantError(t, call(t, fx.s, "GET", path, fx.key, nil), 403, apierr.Forbidden)
	wantError(t, call(t, fx.s, "PUT", path, fx.key, resumeIn{PositionSeconds: 1, DurationSeconds: 2}), 403, apierr.Forbidden)
}

// TestSubtitlesBesideAFile: the .vtt and .srt files with the file's name are listed, vtt first; another name is not listed,
// and a caller who does not reach the file gets not_found.
func TestSubtitlesBesideAFile(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	vtt := putNode(t, fx.s, fx.key, fx.fld.ID, "/a/b/file.vtt", "WEBVTT")
	srt := putNode(t, fx.s, fx.key, fx.fld.ID, "/a/b/file.srt", "1")
	putNode(t, fx.s, fx.key, fx.fld.ID, "/a/b/other.vtt", "WEBVTT")
	path := "/api/v1/nodes/" + fx.deep.ID + "/subtitles"

	got := decode[struct {
		Items []subtitleJSON `json:"items"`
	}](t, callSession(t, fx.s, fx.annSess, "GET", path, nil))
	if len(got.Items) != 2 || got.Items[0].ID != vtt.ID || got.Items[0].Format != "vtt" ||
		got.Items[1].ID != srt.ID || got.Items[1].Format != "srt" {
		t.Fatalf("subtitles = %+v, want file.vtt then file.srt", got.Items)
	}
	wantError(t, callSession(t, fx.s, fx.bobSess, "GET", path, nil), 404, apierr.NotFound)
}

// TestSubtitlesForAViewerWithoutDownload: a viewer lists the subtitle beside a file whose subtitle may not be downloaded (the
// list names files, and the listing is not refused), and the subtitle's attachment route refuses the read. The inline read
// of the subtitle depends on the host it comes from (inlinePolicy), so it is checked in the browser run, not here.
func TestSubtitlesForAViewerWithoutDownload(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	vtt := putNode(t, fx.s, fx.key, fx.fld.ID, "/a/b/file.vtt", "WEBVTT")
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+vtt.ID, fx.key, map[string]any{"no_download": true}), 200)
	path := "/api/v1/nodes/" + fx.deep.ID + "/subtitles"

	got := decode[struct {
		Items []subtitleJSON `json:"items"`
	}](t, callSession(t, fx.s, fx.annSess, "GET", path, nil))
	if len(got.Items) != 1 || got.Items[0].ID != vtt.ID {
		t.Fatalf("subtitles for a viewer = %+v, want the one .vtt listed", got.Items)
	}
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+vtt.ID+"/content?disposition=attachment", nil),
		403, apierr.DownloadRestricted)
}
