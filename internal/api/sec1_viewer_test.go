// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
)

// sec1 item 11 (decision 84): a viewer may open a no-download PDF inline when content_url is configured. The design
// (docs/design.md, the content origin) says a PDF loads from a signed inline link on the content origin, and the
// main origin never shows a file inline when content_url is set. So the viewer's path is the signed link.
func TestViewerOpensANoDownloadPDFInlineWithContentURL(t *testing.T) {
	t.Parallel()
	const contentHost = "files.example.test"
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.ContentURL = "https://" + contentHost })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	pdf := putTyped(t, s, key, f.ID, "doc.pdf", "application/pdf", "%PDF-1.4 test")
	wantStatus(t, call(t, s, "PATCH", "/api/v1/nodes/"+pdf.ID, key, map[string]any{"no_download": true}), 200)

	vee, veeSess := verifiedUser(t, s, key, acc.ID, "vee@example.com")
	wantStatus(t, call(t, s, "PUT", "/api/v1/nodes/"+pdf.ID+"/permissions/"+vee.ID, key,
		map[string]any{"role": "viewer", "notify": false}), 200)

	// The viewer's signed inline link, and the file read on the content origin.
	rec := callSession(t, s, veeSess, "POST", "/api/v1/nodes/"+pdf.ID+"/links", map[string]any{"disposition": "inline"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("viewer's inline link: status %d, want 201: %s", rec.Code, rec.Body)
	}
	var link struct {
		URL string `json:"url"`
	}
	link.URL = decode[struct {
		URL string `json:"url"`
	}](t, rec).URL
	if !strings.HasPrefix(link.URL, "https://"+contentHost+"/api/v1/links/") {
		t.Fatalf("link URL = %q", link.URL)
	}
	got := fromHost(t, s, contentHost, "GET", urlPath(t, link.URL), nil, nil)
	wantStatus(t, got, 200)
	if !strings.HasPrefix(got.Header().Get("Content-Disposition"), "inline") {
		t.Errorf("viewer's PDF on the content origin: disposition %q, want inline", got.Header().Get("Content-Disposition"))
	}
}
