// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// L5: a HEAD request reads a thumbnail and starts no work. It answers 404 when nothing is made, and queues nothing;
// the GET that follows is what starts it.
func TestHeadStartsNoWork(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	runThumbnails(t, s)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	fld := newFolder(t, s, key, acc.ID, "Pictures")
	pic := putImage(t, s, key, fld.ID, "/pic.png", "image/png", pngBytes(t, 300, 200))

	rec := rawCall(t, s, http.MethodHead, thumbPath(pic.ID, 0), key, "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("HEAD with nothing made: %d, want 404", rec.Code)
	}
	pending, err := s.store.DerivedCount(context.Background(), store.DerivedPending)
	if err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Fatalf("a HEAD queued %d thumbnail(s)", pending)
	}
	wantStatus(t, thumbAs(t, s, key, nil, pic.ID, 0), http.StatusAccepted)
	waitThumb(t, s, key, nil, pic.ID, 0)
	if rec := rawCall(t, s, http.MethodHead, thumbPath(pic.ID, 0), key, "", nil); rec.Code != http.StatusOK {
		t.Errorf("HEAD of a made thumbnail: %d, want 200", rec.Code)
	}
}
