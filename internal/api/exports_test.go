// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/derived"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/office"
	"github.com/shukiv/selfcloud/internal/store"
)

// useConverter points the server's conversion client at the fake Collabora, which trusts its own certificate.
func (fx *officeFixture) useConverter(t *testing.T) {
	t.Helper()
	c, err := office.New(fx.col.DiscoveryURL(), fx.col.Client(), office.Options{Concurrent: 2,
		Timeout: 10 * time.Second, MaxOutput: int64(fx.s.cfg.Derived.MaxConvertBytes)})
	if err != nil {
		t.Fatal(err)
	}
	fx.s.conv = c
}

// runConversions runs the server's conversion workers for the test, and stops them when it ends.
func runConversions(t *testing.T, s *Server) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.convertPool.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
}

// previewGet asks for a preview of a node as the account key, and returns the answer.
func previewGet(t *testing.T, fx *officeFixture, nodeID, query string) *httptest.ResponseRecorder {
	t.Helper()
	return call(t, fx.s, "GET", "/api/v1/nodes/"+nodeID+"/preview"+query, fx.key, nil)
}

// waitPreview polls a preview until it is ready, and returns the 200 answer. A 202 is pending; anything else fails.
func waitPreview(t *testing.T, fx *officeFixture, nodeID, query string) *httptest.ResponseRecorder {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		rec := previewGet(t, fx, nodeID, query)
		switch rec.Code {
		case http.StatusOK:
			return rec
		case http.StatusAccepted:
		default:
			t.Fatalf("preview answered %d while waiting: %s", rec.Code, rec.Body.String())
		}
		if time.Now().After(deadline) {
			t.Fatal("the preview was never ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// exportPost queues an export of a node as the account key.
func exportPost(t *testing.T, fx *officeFixture, nodeID, format string) *httptest.ResponseRecorder {
	t.Helper()
	return call(t, fx.s, "POST", "/api/v1/nodes/"+nodeID+"/exports", fx.key, map[string]any{"format": format})
}

// waitExport polls an export by its ID until it is ready, and returns the 200 answer.
func waitExport(t *testing.T, fx *officeFixture, nodeID, drvID string) *httptest.ResponseRecorder {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		rec := call(t, fx.s, "GET", "/api/v1/nodes/"+nodeID+"/exports/"+drvID, fx.key, nil)
		switch rec.Code {
		case http.StatusOK:
			return rec
		case http.StatusAccepted:
		default:
			t.Fatalf("export answered %d while waiting: %s", rec.Code, rec.Body.String())
		}
		if time.Now().After(deadline) {
			t.Fatal("the export was never ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// rowFor returns the derived row of a node's blob for a kind, under the converter the server makes now.
func rowFor(t *testing.T, fx *officeFixture, n nodeJSON, kind string) *store.Derived {
	t.Helper()
	key, err := fx.s.convertKey(context.Background(), fileExt(n.Name))
	if err != nil {
		t.Fatal(err)
	}
	d, err := fx.s.convertPool.Cache().Lookup(context.Background(), fx.acc.ID, blobOf(t, fx, n), kind, key)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// blobOf returns the blob ID a node points at.
func blobOf(t *testing.T, fx *officeFixture, n nodeJSON) string {
	t.Helper()
	node, err := fx.s.store.Node(context.Background(), fx.acc.ID, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	return node.BlobID
}

// TestConversionRoutesAreOffWhileOfficeIsOff: with office.enabled false, every conversion route answers not_found.
func TestConversionRoutesAreOffWhileOfficeIsOff(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	fld := newFolder(t, s, key, acc.ID, "Team")
	doc := putNode(t, s, key, fld.ID, "/letter.docx", "hello")
	wantError(t, call(t, s, "GET", "/api/v1/nodes/"+doc.ID+"/preview", key, nil), 404, apierr.NotFound)
	wantError(t, call(t, s, "POST", "/api/v1/nodes/"+doc.ID+"/exports", key, map[string]any{"format": "pdf"}), 404, apierr.NotFound)
	wantError(t, call(t, s, "GET", "/api/v1/nodes/"+doc.ID+"/exports/drv_x", key, nil), 404, apierr.NotFound)
}

// TestPreviewIsMadeOnceAndServedWithItsHeaders: the first request queues the work and answers 202, the next one answers the
// PDF, the file is served with the content policy, nosniff and an inline disposition, and the converter is asked once.
func TestPreviewIsMadeOnceAndServedWithItsHeaders(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.useConverter(t)
	runConversions(t, fx.s)
	wantStatus(t, previewGet(t, fx, fx.doc.ID, ""), http.StatusAccepted)
	rec := waitPreview(t, fx, fx.doc.ID, "")
	if ct := rec.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("Content-Type %q, want application/pdf", ct)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options %q, want nosniff", got)
	}
	if got := rec.Header().Get("Content-Security-Policy"); got != contentCSP {
		t.Errorf("Content-Security-Policy %q, want %q", got, contentCSP)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "inline;") || !strings.Contains(got, "letter.pdf") {
		t.Errorf("Content-Disposition %q, want inline with the name letter.pdf", got)
	}
	if !strings.HasPrefix(rec.Body.String(), "%PDF-") {
		t.Errorf("body does not start with the PDF signature")
	}
	wantStatus(t, previewGet(t, fx, fx.doc.ID, ""), http.StatusOK)
	if n := fx.col.ConvertHits(); n != 1 {
		t.Errorf("the converter was asked %d times, want once", n)
	}
	if format, name := fx.col.LastConvert(); format != "pdf" || name != "source.docx" {
		t.Errorf("converter got format %q name %q, want pdf and the fixed source.docx", format, name)
	}
}

// TestConvertHeadStartsNoWork: a HEAD of a preview answers from the rows and queues nothing.
func TestConvertHeadStartsNoWork(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.useConverter(t)
	runConversions(t, fx.s)
	rec := call(t, fx.s, "HEAD", "/api/v1/nodes/"+fx.doc.ID+"/preview", fx.key, nil)
	wantStatus(t, rec, http.StatusNotFound)
	time.Sleep(50 * time.Millisecond)
	if n := fx.col.ConvertHits(); n != 0 {
		t.Errorf("a HEAD asked the converter %d times, want none", n)
	}
}

// TestExportIsQueuedAndResumesWithRange: an export answers 202 with its ID and Location, its file is served as an attachment,
// and a Range request gets the rest of it.
func TestExportIsQueuedAndResumesWithRange(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.useConverter(t)
	runConversions(t, fx.s)
	fx.col.SetConvertSize(64)
	rec := exportPost(t, fx, fx.doc.ID, "odt")
	wantStatus(t, rec, http.StatusAccepted)
	id := decode[map[string]string](t, rec)["id"]
	if !strings.HasPrefix(id, "drv_") {
		t.Fatalf("export id %q, want a drv_ ID", id)
	}
	if loc := rec.Header().Get("Location"); loc != "/api/v1/nodes/"+fx.doc.ID+"/exports/"+id {
		t.Errorf("Location %q", loc)
	}
	full := waitExport(t, fx, fx.doc.ID, id)
	if ct := full.Header().Get("Content-Type"); ct != "application/vnd.oasis.opendocument.text" {
		t.Errorf("Content-Type %q, want the ODT type", ct)
	}
	if got := full.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment;") || !strings.Contains(got, "letter.odt") {
		t.Errorf("Content-Disposition %q, want attachment letter.odt", got)
	}
	rr := rawCall(t, fx.s, "GET", "/api/v1/nodes/"+fx.doc.ID+"/exports/"+id, fx.key, "", map[string]string{"Range": "bytes=2-5"})
	wantStatus(t, rr, http.StatusPartialContent)
	if got := rr.Body.Len(); got != 4 {
		t.Errorf("a range of 4 bytes returned %d", got)
	}
}

// TestExportAnUnlistedFormatIsRefusedBeforeAnyRequest: a format outside the fixed list, and the preview-only png, answer
// invalid_request and send nothing.
func TestExportAnUnlistedFormatIsRefusedBeforeAnyRequest(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.useConverter(t)
	runConversions(t, fx.s)
	for _, f := range []string{"exe", "png", "../pdf", "PDF", ""} {
		wantError(t, exportPost(t, fx, fx.doc.ID, f), 400, "invalid_request")
	}
	time.Sleep(20 * time.Millisecond)
	if n := fx.col.ConvertHits(); n != 0 {
		t.Errorf("%d conversions were asked for refused formats", n)
	}
}

// TestConvertibleTypesAreTheDiscoveryOnes: a file whose type discovery does not list for conversion is refused with
// unsupported_media_type. The fake lists docx on its own host, and xlsx only on another host, which is dropped.
func TestConvertibleTypesAreTheDiscoveryOnes(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.useConverter(t)
	runConversions(t, fx.s)
	odt := putNode(t, fx.s, fx.key, fx.fld.ID, "/notes.odt", "hi")
	xlsx := putNode(t, fx.s, fx.key, fx.fld.ID, "/sheet.xlsx", "hi")
	wantError(t, previewGet(t, fx, odt.ID, ""), 415, apierr.UnsupportedMedia)
	wantError(t, previewGet(t, fx, xlsx.ID, ""), 415, apierr.UnsupportedMedia)
	wantError(t, exportPost(t, fx, odt.ID, "pdf"), 415, apierr.UnsupportedMedia)
	if n := fx.col.ConvertHits(); n != 0 {
		t.Errorf("%d conversions were asked for types discovery does not list", n)
	}
}

// TestConversionRefusesSourcesOverTheLimit: a source over derived.max_source_bytes is refused before it is sent.
func TestConversionRefusesSourcesOverTheLimit(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t, func(c *config.Config) { c.Derived.MaxSourceBytes = 4 })
	fx.useConverter(t)
	runConversions(t, fx.s)
	wantError(t, previewGet(t, fx, fx.doc.ID, ""), 413, apierr.TooLarge)
	wantError(t, exportPost(t, fx, fx.doc.ID, "pdf"), 413, apierr.TooLarge)
	if n := fx.col.ConvertHits(); n != 0 {
		t.Errorf("an oversize source was sent %d times", n)
	}
}

// TestNoDownloadRefusesPreviewAndExport: a viewer of a no-download file gets neither a preview nor an export, and an editor
// gets both (the rule binds viewers and commenters only, decision 20).
func TestNoDownloadRefusesPreviewAndExport(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.useConverter(t)
	runConversions(t, fx.s)
	fx.grant(t, fx.doc.ID, fx.ann.ID, "viewer")
	fx.grant(t, fx.doc.ID, fx.bob.ID, "editor")
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+fx.doc.ID, fx.key, map[string]any{"no_download": true}), 200)

	annGet := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.doc.ID+"/preview", nil)
	wantError(t, annGet, 403, apierr.DownloadRestricted)
	annExp := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.doc.ID+"/exports", map[string]any{"format": "pdf"})
	wantError(t, annExp, 403, apierr.DownloadRestricted)
	if n := fx.col.ConvertHits(); n != 0 {
		t.Errorf("a restricted viewer caused %d conversions", n)
	}
	wantStatus(t, callSession(t, fx.s, fx.bobSess, "POST", "/api/v1/nodes/"+fx.doc.ID+"/exports", map[string]any{"format": "pdf"}), 202)
	// A viewer gets no export by its ID either: the rule is checked on the download as well (the ID need not exist).
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.doc.ID+"/exports/drv_none", nil), 403, apierr.DownloadRestricted)
}

// TestConversionIsNotAvailableToAnotherAccountOrOutsideTheTokenFolder: another account's key gets not_found; a token limited to
// another folder gets not_found; neither can read an export of this document by its ID.
func TestConversionIsNotAvailableToAnotherAccountOrOutsideTheTokenFolder(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.useConverter(t)
	runConversions(t, fx.s)
	rec := exportPost(t, fx, fx.doc.ID, "pdf")
	wantStatus(t, rec, http.StatusAccepted)
	drv := decode[map[string]string](t, rec)["id"]
	waitExport(t, fx, fx.doc.ID, drv)

	other, otherKey := newAccount(t, fx.s, fx.admin, "Other")
	_ = other
	wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.doc.ID+"/preview", otherKey, nil), 404, apierr.NotFound)
	wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.doc.ID+"/exports/"+drv, otherKey, nil), 404, apierr.NotFound)

	elsewhere := newFolder(t, fx.s, fx.key, fx.acc.ID, "Elsewhere")
	tok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{elsewhere.ID}})
	wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.doc.ID+"/preview", tok, nil), 404, apierr.NotFound)
	wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.doc.ID+"/exports/"+drv, tok, nil), 404, apierr.NotFound)
}

// TestConverterRefusalIsStickyAndItsTextIsKept: a refusal by Collabora fails the row for its key, with the server's own
// text, and the converter's text reaches neither the response nor the row.
func TestConverterRefusalIsStickyAndItsTextIsKept(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.useConverter(t)
	runConversions(t, fx.s)
	fx.col.SetConvertMode("refuse")
	wantStatus(t, previewGet(t, fx, fx.doc.ID, ""), http.StatusAccepted)
	waitRowState(t, fx, fx.doc, "pdf", store.DerivedFailed)
	wantStatus(t, previewGet(t, fx, fx.doc.ID, ""), http.StatusNotFound)
	row := rowFor(t, fx, fx.doc, "pdf")
	if span := row.RetryAt.Sub(row.FailedAt); span < 23*time.Hour || span > 25*time.Hour {
		t.Errorf("a refusal is retried %v after it failed, want a day (decision 37)", span)
	}
	if strings.Contains(row.Error, "SECRET") || strings.Contains(row.Error, "letter") {
		t.Errorf("the converter's text reached the row: %q", row.Error)
	}
	if row.Error != "the converter refused the document" {
		t.Errorf("row error %q, want the server's %q", row.Error, "the converter refused the document")
	}
}

// TestConverterOutageIsRetried: a 503 is a transient failure, retried after a wait, so the row keeps a retry time.
func TestConverterOutageIsRetried(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.useConverter(t)
	runConversions(t, fx.s)
	fx.col.SetConvertMode("fail")
	wantStatus(t, previewGet(t, fx, fx.doc.ID, ""), http.StatusAccepted)
	waitRowState(t, fx, fx.doc, "pdf", store.DerivedFailed)
	row := rowFor(t, fx, fx.doc, "pdf")
	if row.RetryAt.IsZero() {
		t.Errorf("an outage has no retry time; it must be retried")
	}
	if strings.Contains(row.Error, "SECRET") {
		t.Errorf("the converter's text reached the row: %q", row.Error)
	}
}

// waitRowState waits until the row of a node for a kind is in state, and fails if it never is.
func waitRowState(t *testing.T, fx *officeFixture, n nodeJSON, kind, state string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if d := rowFor(t, fx, n, kind); d != nil && d.State == state {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the %s row never reached %s", kind, state)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestConversionOutputOverTheLimitFailsAndKeepsNothing: an answer over derived.max_convert_bytes fails the row, and no file
// is kept.
func TestConversionOutputOverTheLimitFailsAndKeepsNothing(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t, func(c *config.Config) { c.Derived.MaxConvertBytes = 1 << 10 })
	fx.useConverter(t)
	runConversions(t, fx.s)
	fx.col.SetConvertSize(64 << 10)
	wantStatus(t, previewGet(t, fx, fx.doc.ID, ""), http.StatusAccepted)
	waitRowState(t, fx, fx.doc, "pdf", store.DerivedFailed)
	if files := convDerivedFilesOf(t, fx); files != 0 {
		t.Errorf("%d derived files kept for an over-limit answer, want none", files)
	}
}

// convDerivedFilesOf counts the files the account holds under the derived directory.
func convDerivedFilesOf(t *testing.T, fx *officeFixture) int {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(fx.s.cfg.DataDir, "derived", fx.acc.ID))
	if errors.Is(err, fs.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(ents)
}

// TestCallerRevokedBeforeTheConversionKeepsNothing: a viewer who loses access between the request and the work gets no file,
// the converter is never asked, and the pending row is gone, so a later request queues it again.
func TestCallerRevokedBeforeTheConversionKeepsNothing(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.useConverter(t)
	fx.grant(t, fx.doc.ID, fx.ann.ID, "viewer")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.doc.ID+"/preview", nil), http.StatusAccepted)
	// Access goes before the pool runs.
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.doc.ID+"/permissions/"+fx.ann.ID, fx.key, nil), 204)
	runConversions(t, fx.s)
	deadline := time.Now().Add(5 * time.Second)
	for {
		key, _ := fx.s.convertKey(context.Background(), "docx")
		d, _ := fx.s.convertPool.Cache().Lookup(context.Background(), fx.acc.ID, blobOf(t, fx, fx.doc), "pdf", key)
		if d == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the revoked caller's row is still there: state %s", d.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := fx.col.ConvertHits(); n != 0 {
		t.Errorf("the converter was asked %d times for a revoked caller", n)
	}
}

// TestNewBuildMakesNewRowsAndOldOnesAreNotServed: a change of the converter's build makes a new key, so a file made by the old
// build is not served as current.
func TestNewBuildMakesNewRowsAndOldOnesAreNotServed(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.useConverter(t)
	runConversions(t, fx.s)
	fx.col.SetCapabilities(true, "old-build")
	rec := exportPost(t, fx, fx.doc.ID, "docx")
	wantStatus(t, rec, http.StatusAccepted)
	old := decode[map[string]string](t, rec)["id"]
	waitExport(t, fx, fx.doc.ID, old)

	// The client keeps its capabilities for an hour, as decision 37 says, so the new build is seen by a fresh client, as it
	// would be after a restart or the hour.
	fx.col.SetCapabilities(true, "new-build")
	fx.useConverter(t)
	wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.doc.ID+"/exports/"+old, fx.key, nil), 404, apierr.NotFound)
	rec = exportPost(t, fx, fx.doc.ID, "docx")
	wantStatus(t, rec, http.StatusAccepted)
	if id := decode[map[string]string](t, rec)["id"]; id == old {
		t.Errorf("the new build reused the old row %s", id)
	}
}

// TestConverterWithConversionOffIsUnavailable: a capabilities answer that does not offer conversion makes every route answer
// unavailable, and nothing is sent.
func TestConverterWithConversionOffIsUnavailable(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.useConverter(t)
	runConversions(t, fx.s)
	fx.col.SetCapabilities(false, "x")
	wantError(t, previewGet(t, fx, fx.doc.ID, ""), 503, apierr.Unavailable)
	if n := fx.col.ConvertHits(); n != 0 {
		t.Errorf("%d conversions were sent with conversion off", n)
	}
}

// TestPreviewFormatPngIsItsOwnKind: the first page as PNG is a row of its own kind, served as an image.
func TestPreviewFormatPngIsItsOwnKind(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.useConverter(t)
	runConversions(t, fx.s)
	wantStatus(t, previewGet(t, fx, fx.doc.ID, "?format=png"), http.StatusAccepted)
	rec := waitPreview(t, fx, fx.doc.ID, "?format=png")
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type %q, want image/png", ct)
	}
	if row := rowFor(t, fx, fx.doc, "png_page1"); row == nil || row.State != store.DerivedReady {
		t.Errorf("no ready png_page1 row")
	}
	if row := rowFor(t, fx, fx.doc, "pdf"); row != nil {
		t.Errorf("a png preview made a pdf row")
	}
	wantError(t, previewGet(t, fx, fx.doc.ID, "?format=gif"), 400, apierr.InvalidRequest)
}

// TestPreviewOnTheContentOriginIsASignedLink: with content_url set, the main origin answers a ready preview with a 303 to a
// signed link, and never shows the PDF inline itself (inlinePolicy, decision 33).
func TestPreviewOnTheContentOriginIsASignedLink(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t, func(c *config.Config) { c.ContentURL = "https://files.example.test" })
	fx.useConverter(t)
	runConversions(t, fx.s)
	wantStatus(t, previewGet(t, fx, fx.doc.ID, ""), http.StatusAccepted)
	deadline := time.Now().Add(10 * time.Second)
	for {
		rec := previewGet(t, fx, fx.doc.ID, "")
		if rec.Code == http.StatusSeeOther {
			loc := rec.Header().Get("Location")
			if !strings.HasPrefix(loc, "https://files.example.test/") {
				t.Fatalf("Location %q is not on the content origin", loc)
			}
			return
		}
		if rec.Code != http.StatusAccepted {
			t.Fatalf("answered %d: %s", rec.Code, rec.Body.String())
		}
		if time.Now().After(deadline) {
			t.Fatal("the preview was never ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestDerivedKindsStayWithinTheLimits pins the kind and converter widths the rows hold (derived table, migration 0028).
func TestDerivedKindsStayWithinTheLimits(t *testing.T) {
	t.Parallel()
	for _, k := range []string{"pdf", "png_page1", exportKindPrefix + "odp", exportKindPrefix + "pptx"} {
		if len(k) > 32 {
			t.Errorf("kind %q is longer than the 32 the row allows", k)
		}
	}
	key := derived.ConverterKey(office.Caps{Available: true, Version: "24.04.7.2", VersionHash: "c6c73bdf"}, 1, 2)
	if len(key) > 64 {
		t.Errorf("converter key %q is longer than the 64 the row allows", key)
	}
}

// TestExportIDOfAnotherFileIsNotFound: an export of one file is not served under the ID of another file in the same account,
// since the row must belong to this node's blob.
func TestExportIDOfAnotherFileIsNotFound(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.useConverter(t)
	runConversions(t, fx.s)
	rec := exportPost(t, fx, fx.doc.ID, "docx")
	wantStatus(t, rec, http.StatusAccepted)
	drv := decode[map[string]string](t, rec)["id"]
	waitExport(t, fx, fx.doc.ID, drv)
	other := putNode(t, fx.s, fx.key, fx.fld.ID, "/other.docx", "another document")
	wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+other.ID+"/exports/"+drv, fx.key, nil), 404, apierr.NotFound)
}

// TestCallerRevokedDuringTheConversionKeepsNothing: a viewer revoked while Collabora is converting gets no file. The pool asks
// the caller again before it keeps the file, so the answer is refused and the row is dropped.
func TestCallerRevokedDuringTheConversionKeepsNothing(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.useConverter(t)
	runConversions(t, fx.s)
	fx.grant(t, fx.doc.ID, fx.ann.ID, "viewer")
	fx.col.SetConvertStall(400 * time.Millisecond)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.doc.ID+"/preview", nil), http.StatusAccepted)
	deadline := time.Now().Add(5 * time.Second)
	for fx.col.ConvertHits() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the conversion never started")
		}
		time.Sleep(time.Millisecond)
	}
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.doc.ID+"/permissions/"+fx.ann.ID, fx.key, nil), 204)
	deadline = time.Now().Add(5 * time.Second)
	for {
		key, _ := fx.s.convertKey(context.Background(), "docx")
		d, _ := fx.s.convertPool.Cache().Lookup(context.Background(), fx.acc.ID, blobOf(t, fx, fx.doc), "pdf", key)
		if d == nil {
			return
		}
		if d.State == store.DerivedReady {
			t.Fatal("a file was kept for a caller revoked during the conversion")
		}
		if time.Now().After(deadline) {
			t.Fatalf("the row is still there in state %s", d.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
