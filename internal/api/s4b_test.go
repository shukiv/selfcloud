// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// TestFailedExportAnswersItsReasonOnTheFirstRead: an export the converter refused is a failure the page can show. Its ID
// answers 415 with the reason "failed" on the first read, as the POST answers it on a repeat (decision 59). Before decision
// 59 the read answered 404, and the reason came only from a repeat POST.
func TestFailedExportAnswersItsReasonOnTheFirstRead(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.useConverter(t)
	runConversions(t, fx.s)
	fx.col.SetConvertMode("refuse")
	rec := exportPost(t, fx, fx.doc.ID, "odt")
	wantStatus(t, rec, http.StatusAccepted)
	id := decode[map[string]string](t, rec)["id"]
	waitRowState(t, fx, fx.doc, exportKindPrefix+"odt", store.DerivedFailed)
	e := wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.doc.ID+"/exports/"+id, fx.key, nil), 415, apierr.UnsupportedMedia)
	if e.Details["reason"] != "failed" {
		t.Errorf("reason %v, want failed", e.Details["reason"])
	}
	// A repeat POST says the same, so the two reads agree.
	e = wantError(t, exportPost(t, fx, fx.doc.ID, "odt"), 415, apierr.UnsupportedMedia)
	if e.Details["reason"] != "failed" {
		t.Errorf("repeat POST reason %v, want failed", e.Details["reason"])
	}
}

// TestFailedPreviewAnswersItsReason: a preview the converter refused answers 415 with the reason "failed", on the read and
// on a HEAD, not 404: a 404 says there is nothing to show, and the page could not tell a failure from a missing file.
func TestFailedPreviewAnswersItsReason(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.useConverter(t)
	runConversions(t, fx.s)
	fx.col.SetConvertMode("refuse")
	wantStatus(t, previewGet(t, fx, fx.doc.ID, ""), http.StatusAccepted)
	waitRowState(t, fx, fx.doc, "pdf", store.DerivedFailed)
	e := wantError(t, previewGet(t, fx, fx.doc.ID, ""), 415, apierr.UnsupportedMedia)
	if e.Details["reason"] != "failed" {
		t.Errorf("reason %v, want failed", e.Details["reason"])
	}
	wantStatus(t, call(t, fx.s, "HEAD", "/api/v1/nodes/"+fx.doc.ID+"/preview", fx.key, nil), http.StatusUnsupportedMediaType)
}

// TestInfoNamesTheConversionWhileOfficeIsOn: the page learns from /api/v1/info whether conversion is offered, so it shows no
// preview or export button while office is off. The list holds "office_convert" only while office.enabled is on.
func TestInfoNamesTheConversionWhileOfficeIsOn(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	rec := call(t, fx.s, "GET", "/api/v1/info", "", nil)
	wantStatus(t, rec, http.StatusOK)
	if got := decode[struct {
		Features []string `json:"features"`
	}](t, rec).Features; len(got) != 1 || got[0] != "office_convert" {
		t.Errorf("features %v, want [office_convert]", got)
	}
	off := newAuthServer(t, &mail.Recorder{})
	rec = call(t, off, "GET", "/api/v1/info", "", nil)
	if got := decode[struct {
		Features []string `json:"features"`
	}](t, rec).Features; len(got) != 0 {
		t.Errorf("features %v with office off, want none", got)
	}
}
