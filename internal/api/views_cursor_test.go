// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/url"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/store"
)

// TestRecentStarredSharedCursorTag: a page of the filtered recent, starred or shared list continues only under the same
// filter (decision 110). Without the filter the cursor is invalid_request; with it, the next page answers 200.
func TestRecentStarredSharedCursorTag(t *testing.T) {
	t.Parallel()
	fx := newP1Fixture(t)
	var ids []string
	for _, name := range []string{"/a.pdf", "/b.pdf"} {
		n := nodeOf(t, rawCall(t, fx.s, "POST", "/api/v1/folders/"+fx.annPF+"/files?path="+name, fx.key, bytesOf(15),
			map[string]string{"Content-Type": "application/pdf"}))
		ids = append(ids, n.ID)
	}
	if err := fx.s.store.Write(t.Context(), func(tx *store.Tx) error {
		for _, id := range ids {
			if err := tx.RecordEdit(t.Context(), fx.acc.ID, fx.ann.ID, id, time.Now()); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+id+"/star", nil), 204)
	}
	for _, name := range []string{"/team-a.pdf", "/team-b.pdf"} {
		shared := nodeOf(t, writeFile(t, fx.s, fx.key, fx.fld.ID, name, bytesOf(9)))
		fx.setPerm(t, shared.ID, fx.ann.ID, "viewer")
	}

	for _, path := range []string{"/api/v1/recent", "/api/v1/starred", "/api/v1/shared"} {
		rec := callSession(t, fx.s, fx.annSess, "GET", path+"?type=pdf&limit=1", nil)
		wantStatus(t, rec, 200)
		cursor := decode[listResponse[nodeJSON]](t, rec).NextCursor
		if cursor == "" {
			t.Fatalf("%s?type=pdf&limit=1: no next cursor", path)
		}
		wantError(t, callSession(t, fx.s, fx.annSess, "GET", path+"?limit=1&cursor="+url.QueryEscape(cursor), nil),
			400, apierr.InvalidRequest)
		wantStatus(t, callSession(t, fx.s, fx.annSess, "GET", path+"?type=pdf&limit=1&cursor="+url.QueryEscape(cursor), nil), 200)
	}
}
