// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"strings"
	"testing"
)

// Slice st (decision 79): the Storage page's breakdown of My files (old versions, the trash), the kind filter of the
// largest-files list, and the folder each file sits in.

// TestMeStorageBreakdown: GET /me?storage=breakdown adds the old versions and the trash to the caller's own storage.
// A plain GET /me answer carries no breakdown key (the test checks the answer, not how the server computes it).
func TestMeStorageBreakdown(t *testing.T) {
	t.Parallel()
	fx := newP1Fixture(t)
	a := nodeOf(t, writeFile(t, fx.s, fx.key, fx.annPF, "/a.bin", bytesOf(60)))
	replaceContent(t, fx.sharingFixture, a.ID, a.ETag, bytesOf(20))
	b := nodeOf(t, writeFile(t, fx.s, fx.key, fx.annPF, "/b.bin", bytesOf(10)))
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+b.ID, fx.key, nil), 204)

	rec := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/me?storage=breakdown", nil)
	wantStatus(t, rec, 200)
	body := rec.Body.String()
	for _, want := range []string{`"versions_bytes":60`, `"versions_files":1`, `"trash_bytes":10`, `"trash_files":1`} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /me?storage=breakdown = %s; want %s", body, want)
		}
	}
	rec = callSession(t, fx.s, fx.annSess, "GET", "/api/v1/me", nil)
	wantStatus(t, rec, 200)
	if strings.Contains(rec.Body.String(), "breakdown") {
		t.Errorf("plain GET /me carries the breakdown: %s", rec.Body.String())
	}
}

// TestMyLargestKindAndFolder: ?kind= narrows the list by the media type, and ?kind=trash to the trash, and an unknown kind
// is refused. A row in a folder names the folder directly above it (parent_name). The test checks that one audio row
// carries its parent_name and mime_type, and that a file at the top of My files has no parent_name.
func TestMyLargestKindAndFolder(t *testing.T) {
	t.Parallel()
	fx := newP1Fixture(t)
	mkdirs(t, fx.s, fx.key, fx.annPF, "/Music")
	put := func(p, mt, body string) nodeJSON {
		t.Helper()
		return nodeOf(t, rawCall(t, fx.s, "POST", "/api/v1/folders/"+fx.annPF+"/files?path="+p, fx.key, body,
			map[string]string{"Content-Type": mt}))
	}
	put("/Music/song.mp3", "audio/mpeg", bytesOf(30))
	put("/clip.mp4", "video/mp4", bytesOf(25))
	put("/cover.png", "image/png", bytesOf(20))
	put("/report.pdf", "application/pdf", bytesOf(15))
	gone := put("/old.mp3", "audio/mpeg", bytesOf(5))
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+gone.ID, fx.key, nil), 204)

	list := func(kind string) string {
		t.Helper()
		rec := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/me/storage/largest?kind="+kind, nil)
		wantStatus(t, rec, 200)
		return rec.Body.String()
	}
	// A trashed audio file stays in the audio kind, marked in_trash; the trash kind is the list of trashed files.
	if body := list("audio"); !strings.Contains(body, "song.mp3") || strings.Contains(body, "clip.mp4") ||
		!strings.Contains(body, "old.mp3") {
		t.Errorf("kind=audio = %s; want song.mp3 and the trashed old.mp3, not clip.mp4", body)
	}
	if body := list("video"); !strings.Contains(body, "clip.mp4") || strings.Contains(body, "song.mp3") {
		t.Errorf("kind=video = %s; want clip.mp4 only", body)
	}
	if body := list("image"); !strings.Contains(body, "cover.png") || strings.Contains(body, "report.pdf") {
		t.Errorf("kind=image = %s; want cover.png only", body)
	}
	if body := list("document"); !strings.Contains(body, "report.pdf") || strings.Contains(body, "cover.png") {
		t.Errorf("kind=document = %s; want report.pdf only", body)
	}
	if body := list("trash"); !strings.Contains(body, "old.mp3") || strings.Contains(body, "song.mp3") ||
		!strings.Contains(body, `"in_trash":true`) {
		t.Errorf("kind=trash = %s; want old.mp3 only, in the trash", body)
	}
	if body := list(""); !strings.Contains(body, "clip.mp4") || !strings.Contains(body, "song.mp3") {
		t.Errorf("no kind = %s; want every file", body)
	}
	if body := list("audio"); !strings.Contains(body, `"parent_name":"Music"`) || !strings.Contains(body, `"mime_type":"audio/mpeg"`) {
		t.Errorf("kind=audio rows = %s; want parent_name Music and the media type", body)
	}
	if body := list("video"); strings.Contains(body, "parent_name") {
		t.Errorf("a file at the top of My files = %s; want no parent_name", body)
	}
	rec := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/me/storage/largest?kind=sheets", nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("kind=sheets = %d; want 400", rec.Code)
	}
}
