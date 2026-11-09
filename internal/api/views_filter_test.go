// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/store"
)

// TestRecentStarredSharedTypeFilter: the recent, starred and shared lists take ?type= and the modified range. Each keeps
// the entries whose node has the type, and refuses a bad value as invalid_request.
func TestRecentStarredSharedTypeFilter(t *testing.T) {
	t.Parallel()
	fx := newP1Fixture(t)
	pdf := nodeOf(t, rawCall(t, fx.s, "POST", "/api/v1/folders/"+fx.annPF+"/files?path=/report.pdf", fx.key, bytesOf(15),
		map[string]string{"Content-Type": "application/pdf"}))
	mp3 := nodeOf(t, rawCall(t, fx.s, "POST", "/api/v1/folders/"+fx.annPF+"/files?path=/song.mp3", fx.key, bytesOf(30),
		map[string]string{"Content-Type": "audio/mpeg"}))
	// Ann views both (recent) and stars both; the team file is shared with her.
	if err := fx.s.store.Write(t.Context(), func(tx *store.Tx) error {
		for _, id := range []string{pdf.ID, mp3.ID} {
			if err := tx.RecordEdit(t.Context(), fx.acc.ID, fx.ann.ID, id, time.Now()); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{pdf.ID, mp3.ID} {
		wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+id+"/star", nil), 204)
	}
	shared := nodeOf(t, writeFile(t, fx.s, fx.key, fx.fld.ID, "/team.pdf", bytesOf(9)))
	fx.setPerm(t, shared.ID, fx.ann.ID, "viewer")

	get := func(path string) (int, string) {
		t.Helper()
		rec := callSession(t, fx.s, fx.annSess, "GET", path, nil)
		return rec.Code, rec.Body.String()
	}
	for _, path := range []string{"/api/v1/recent", "/api/v1/starred"} {
		if code, body := get(path + "?type=pdf"); code != 200 || !strings.Contains(body, "report.pdf") || strings.Contains(body, "song.mp3") {
			t.Errorf("%s?type=pdf: %d %s; want report.pdf only", path, code, body)
		}
		if code, body := get(path + "?type=audio"); code != 200 || !strings.Contains(body, "song.mp3") || strings.Contains(body, "report.pdf") {
			t.Errorf("%s?type=audio: %d %s; want song.mp3 only", path, code, body)
		}
		for _, q := range []string{"type=documents", "modified_after=5&modified_before=5"} {
			if code, _ := get(path + "?" + q); code != 400 {
				t.Errorf("%s?%s: %d, want 400", path, q, code)
			}
		}
	}
	if code, body := get("/api/v1/shared?type=pdf"); code != 200 || !strings.Contains(body, "team.pdf") || strings.Contains(body, "song.mp3") {
		t.Errorf("shared?type=pdf: %d %s; want team.pdf", code, body)
	}
	if code, body := get("/api/v1/shared?type=audio"); code != 200 || strings.Contains(body, "team.pdf") {
		t.Errorf("shared?type=audio: %d %s; want no team.pdf", code, body)
	}
	if code, _ := get("/api/v1/shared?type=documents"); code != 400 {
		t.Errorf("shared?type=documents: %d, want 400", code)
	}
}
