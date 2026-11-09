// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"strings"
	"testing"
)

// largestFilterFixture puts four files into Ann's My files (one of them trashed) for the Largest route tests.
func largestFilterFixture(t *testing.T) *p1Fixture {
	t.Helper()
	fx := newP1Fixture(t)
	put := func(p, mt string, n int) nodeJSON {
		t.Helper()
		return nodeOf(t, rawCall(t, fx.s, "POST", "/api/v1/folders/"+fx.annPF+"/files?path="+p, fx.key, bytesOf(n),
			map[string]string{"Content-Type": mt}))
	}
	put("/song.mp3", "audio/mpeg", 30)
	put("/clip.mp4", "video/mp4", 25)
	put("/cover.png", "image/png", 20)
	put("/report.pdf", "application/pdf", 15)
	gone := put("/old.mp3", "audio/mpeg", 5)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+gone.ID, fx.key, nil), 204)
	return fx
}

// TestMyLargestTypeAndSource: ?type= keeps the files of one group, and ?source=trash keeps the trashed ones. The legacy
// kind still works. A folder or shortcut type, a source that is not trash, and kind with type or source are invalid.
func TestMyLargestTypeAndSource(t *testing.T) {
	t.Parallel()
	fx := largestFilterFixture(t)
	list := func(query string) (int, string) {
		t.Helper()
		rec := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/me/storage/largest?"+query, nil)
		return rec.Code, rec.Body.String()
	}
	if code, body := list("type=pdf"); code != http.StatusOK || !strings.Contains(body, "report.pdf") || strings.Contains(body, "song.mp3") {
		t.Errorf("type=pdf: %d %s; want report.pdf only", code, body)
	}
	if code, body := list("type=audio"); code != http.StatusOK || !strings.Contains(body, "song.mp3") || strings.Contains(body, "clip.mp4") {
		t.Errorf("type=audio: %d %s; want song.mp3 and the trashed old.mp3", code, body)
	}
	if code, body := list("type=audio&source=trash"); code != http.StatusOK || !strings.Contains(body, "old.mp3") || strings.Contains(body, "song.mp3") {
		t.Errorf("type=audio&source=trash: %d %s; want old.mp3 only", code, body)
	}
	if code, body := list("kind=audio"); code != http.StatusOK || !strings.Contains(body, "song.mp3") {
		t.Errorf("legacy kind=audio: %d %s", code, body)
	}
	if code, body := list("type=pdf&modified_before=1"); code != http.StatusOK || strings.Contains(body, "report.pdf") {
		t.Errorf("type=pdf before 1 ms: %d %s; want no rows", code, body)
	}
	for _, q := range []string{
		"type=folder",
		"type=shortcut",
		"type=documents",
		"type=pdf&kind=audio",
		"kind=audio&type=audio",
		"source=sheets",
		"kind=trash&source=trash",
		"type=pdf&modified_after=5&modified_before=5",
	} {
		if code, body := list(q); code != http.StatusBadRequest {
			t.Errorf("%s: %d %s; want 400", q, code, body)
		}
	}
}

// TestAccountLargestTypeAndSource: the account's Largest takes the same type and source, and the owner's key lists the
// files of every folder the account holds.
func TestAccountLargestTypeAndSource(t *testing.T) {
	t.Parallel()
	fx := largestFilterFixture(t)
	base := "/api/v1/accounts/" + fx.acc.ID + "/storage/largest"
	rec := call(t, fx.s, "GET", base+"?type=pdf", fx.key, nil)
	wantStatus(t, rec, http.StatusOK)
	if body := rec.Body.String(); !strings.Contains(body, "report.pdf") || strings.Contains(body, "song.mp3") {
		t.Errorf("account type=pdf: %s; want report.pdf only", body)
	}
	rec = call(t, fx.s, "GET", base+"?type=audio&source=trash", fx.key, nil)
	wantStatus(t, rec, http.StatusOK)
	if body := rec.Body.String(); !strings.Contains(body, "old.mp3") || strings.Contains(body, "song.mp3") {
		t.Errorf("account type=audio&source=trash: %s; want old.mp3 only", body)
	}
	wantStatus(t, call(t, fx.s, "GET", base+"?type=folder", fx.key, nil), http.StatusBadRequest)
	wantStatus(t, call(t, fx.s, "GET", base+"?source=sheets", fx.key, nil), http.StatusBadRequest)
}
