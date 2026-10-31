// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/store"
)

// TestExportRepeatPostAnswersTheSameID: a second POST for the same document and format, while the first is pending and after
// it is ready, answers the export's real ID and its Location, not an empty ID.
func TestExportRepeatPostAnswersTheSameID(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.useConverter(t)
	runConversions(t, fx.s)
	first := exportPost(t, fx, fx.doc.ID, "pdf")
	wantStatus(t, first, http.StatusAccepted)
	id := decode[map[string]string](t, first)["id"]
	if !strings.HasPrefix(id, "drv_") {
		t.Fatalf("first answer id %q, want a drv_ ID", id)
	}
	second := exportPost(t, fx, fx.doc.ID, "pdf")
	wantStatus(t, second, http.StatusAccepted)
	if got := decode[map[string]string](t, second)["id"]; got != id {
		t.Errorf("repeat POST answered id %q, want %q", got, id)
	}
	if loc := second.Header().Get("Location"); loc != "/api/v1/nodes/"+fx.doc.ID+"/exports/"+id {
		t.Errorf("repeat POST Location %q", loc)
	}
	waitExport(t, fx, fx.doc.ID, id)
	third := exportPost(t, fx, fx.doc.ID, "pdf")
	wantStatus(t, third, http.StatusAccepted)
	if got := decode[map[string]string](t, third)["id"]; got != id {
		t.Errorf("POST after the export was ready answered id %q, want %q", got, id)
	}
	if n := fx.col.ConvertHits(); n != 1 {
		t.Errorf("the converter was asked %d times for one export, want once", n)
	}
}

// TestPreviewLinkIsCrossOriginWhenFollowed: a signed preview link on the content origin is served cross-origin, as a signed
// content link is (links.go), so other sites can embed it.
func TestPreviewLinkIsCrossOriginWhenFollowed(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t, func(c *config.Config) { c.ContentURL = "https://files.example.test" })
	fx.useConverter(t)
	runConversions(t, fx.s)
	wantStatus(t, previewGet(t, fx, fx.doc.ID, ""), http.StatusAccepted)
	var loc string
	deadline := time.Now().Add(10 * time.Second)
	for loc == "" {
		rec := previewGet(t, fx, fx.doc.ID, "")
		switch rec.Code {
		case http.StatusSeeOther:
			loc = rec.Header().Get("Location")
		case http.StatusAccepted:
		default:
			t.Fatalf("preview answered %d while waiting", rec.Code)
		}
		if time.Now().After(deadline) {
			t.Fatal("the preview was never ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	got := rawCall(t, fx.s, "GET", loc, "", "", nil)
	wantStatus(t, got, http.StatusOK)
	if cor := got.Header().Get("Cross-Origin-Resource-Policy"); cor != "cross-origin" {
		t.Errorf("link answer Cross-Origin-Resource-Policy %q, want cross-origin", cor)
	}
	if ct := got.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("link answer Content-Type %q, want application/pdf", ct)
	}
	if !strings.HasPrefix(got.Body.String(), "%PDF-") {
		t.Errorf("the link does not serve the PDF")
	}
}

// TestConvertedLinkIsNotFoundWhileOfficeIsOff: a preview link followed while office is off answers not_found, not a panic
// (decision 37): the client is nil then.
func TestConvertedLinkIsNotFoundWhileOfficeIsOff(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.useConverter(t)
	runConversions(t, fx.s)
	// A preview is ready first, so a link that ignored the office switch would find a file and serve it.
	waitPreview(t, fx, fx.doc.ID, "")
	fx.s.conv = nil
	node, err := fx.s.store.Node(context.Background(), fx.acc.ID, fx.doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	err = fx.s.serveConvertedLink(rec, httptest.NewRequest("GET", "/", nil), node, "preview_pdf")
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Code != apierr.NotFound {
		t.Fatalf("got %v, want not_found while office is off", err)
	}
}

// TestConverterKeyCarriesTheExtension: the key of a conversion names the source's extension, so copies with other extensions
// keep their own rows and cannot fail one another (decision 37).
func TestConverterKeyCarriesTheExtension(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.useConverter(t)
	docx, err := fx.s.convertKey(context.Background(), "docx")
	if err != nil {
		t.Fatal(err)
	}
	xlsx, err := fx.s.convertKey(context.Background(), "xlsx")
	if err != nil {
		t.Fatal(err)
	}
	if docx == xlsx {
		t.Fatal("two extensions share a converter key")
	}
	if !strings.HasSuffix(docx, "+docx") || !strings.HasSuffix(xlsx, "+xlsx") {
		t.Errorf("keys %q and %q do not end in their extensions", docx, xlsx)
	}
	if len(docx) > 64 {
		t.Errorf("key %q is longer than the 64 the row allows", docx)
	}
}

// TestConversionIsUnavailableWhenDiscoveryListsNoConversion: a discovery document with no conversion action at all makes the
// converter unavailable, not the type unsupported, and nothing is sent.
func TestConversionIsUnavailableWhenDiscoveryListsNoConversion(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.useConverter(t)
	runConversions(t, fx.s)
	fx.col.SetConvertListed(false)
	wantError(t, previewGet(t, fx, fx.doc.ID, ""), 503, apierr.Unavailable)
	wantError(t, exportPost(t, fx, fx.doc.ID, "pdf"), 503, apierr.Unavailable)
	if n := fx.col.ConvertHits(); n != 0 {
		t.Errorf("%d conversions were sent to a converter that lists none", n)
	}
}

// TestReadyFilesAreServedWhileCapabilitiesAreDown: a file already made is still served when the converter's build cannot be
// had, the cold start after a restart with Collabora down (decision 37). A new conversion is refused.
func TestReadyFilesAreServedWhileCapabilitiesAreDown(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.useConverter(t)
	runConversions(t, fx.s)
	rec := exportPost(t, fx, fx.doc.ID, "docx")
	wantStatus(t, rec, http.StatusAccepted)
	id := decode[map[string]string](t, rec)["id"]
	waitExport(t, fx, fx.doc.ID, id)
	wantStatus(t, previewGet(t, fx, fx.doc.ID, ""), http.StatusAccepted)
	waitPreview(t, fx, fx.doc.ID, "")

	// A fresh client has no copy of the capabilities, and Collabora does not answer them.
	fx.useConverter(t)
	fx.col.SetCapabilitiesDown(true)
	got := call(t, fx.s, "GET", "/api/v1/nodes/"+fx.doc.ID+"/exports/"+id, fx.key, nil)
	wantStatus(t, got, http.StatusOK)
	if !strings.HasPrefix(got.Body.String(), "PK") {
		t.Errorf("the export is not served while the converter is down")
	}
	wantStatus(t, previewGet(t, fx, fx.doc.ID, ""), http.StatusOK)
	wantError(t, exportPost(t, fx, fx.doc.ID, "odt"), 503, apierr.Unavailable)
}

// TestExportRangeIsCappedAsTheContentRouteCapsIt: more ranges than maxRanges are refused with range_not_satisfiable, as the
// content route and a zip refuse them, and the cap's own count is served.
func TestExportRangeIsCappedAsTheContentRouteCapsIt(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.useConverter(t)
	runConversions(t, fx.s)
	fx.col.SetConvertSize(64)
	rec := exportPost(t, fx, fx.doc.ID, "docx")
	wantStatus(t, rec, http.StatusAccepted)
	id := decode[map[string]string](t, rec)["id"]
	waitExport(t, fx, fx.doc.ID, id)
	ranges := func(n int) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = fmt.Sprintf("%d-%d", i, i)
		}
		return "bytes=" + strings.Join(parts, ",")
	}
	path := "/api/v1/nodes/" + fx.doc.ID + "/exports/" + id
	wantStatus(t, rawCall(t, fx.s, "GET", path, fx.key, "", map[string]string{"Range": ranges(maxRanges)}), http.StatusPartialContent)
	over := rawCall(t, fx.s, "GET", path, fx.key, "", map[string]string{"Range": ranges(maxRanges + 1)})
	wantError(t, over, http.StatusRequestedRangeNotSatisfiable, apierr.RangeNotSatisfiable)
}

// TestExportHasNoRowOfAnotherExtension: a conversion made for one extension is not the answer for another, even on the same
// blob: a copy named with another extension gets its own row.
func TestExportHasNoRowOfAnotherExtension(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.useConverter(t)
	runConversions(t, fx.s)
	rec := exportPost(t, fx, fx.doc.ID, "pdf")
	wantStatus(t, rec, http.StatusAccepted)
	id := decode[map[string]string](t, rec)["id"]
	waitExport(t, fx, fx.doc.ID, id)
	row, err := fx.s.store.DerivedByID(context.Background(), fx.acc.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	if row.State != store.DerivedReady || !strings.HasSuffix(row.Converter, "+docx") {
		t.Errorf("export row %+v: want ready, keyed by the docx extension", row)
	}
}
