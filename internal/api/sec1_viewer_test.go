// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
)

// sec1 (decision 84): an inline link to a no-download file is served only on the content origin. Opened
// on the main origin it is refused with the answer of a bad link, and no bytes are sent. Before, the main origin turned
// it into an attachment, which is a download the rule forbids.
func TestRestrictedInlineLinkIsRefusedOnTheMainOrigin(t *testing.T) {
	t.Parallel()
	const contentHost = "files.example.test"
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.ContentURL = "https://" + contentHost })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	pdf := putTyped(t, s, key, f.ID, "doc.pdf", "application/pdf", "%PDF-1.4 secret")
	wantStatus(t, call(t, s, "PATCH", "/api/v1/nodes/"+pdf.ID, key, map[string]any{"no_download": true}), 200)
	vee, veeSess := verifiedUser(t, s, key, acc.ID, "vee@example.com")
	wantStatus(t, call(t, s, "PUT", "/api/v1/nodes/"+pdf.ID+"/permissions/"+vee.ID, key,
		map[string]any{"role": "viewer", "notify": false}), 200)
	rec := callSession(t, s, veeSess, "POST", "/api/v1/nodes/"+pdf.ID+"/links", map[string]any{"disposition": "inline"})
	wantStatus(t, rec, http.StatusCreated)
	link := decode[struct {
		URL string `json:"url"`
	}](t, rec).URL

	got := call(t, s, "GET", urlPath(t, link), "", nil)
	if got.Code == http.StatusOK || strings.Contains(got.Body.String(), "secret") {
		t.Fatalf("main origin served the no-download PDF: status %d, Content-Disposition %q", got.Code, got.Header().Get("Content-Disposition"))
	}
	wantError(t, got, 400, apierr.InvalidRequest)
}

// sec1 (decision 84): a restricted viewer's inline link is minted only for a type a browser shows inline.
// A zip is refused at mint, since the content origin would download it.
func TestRestrictedInlineLinkForAZipIsRefusedAtMint(t *testing.T) {
	t.Parallel()
	const contentHost = "files.example.test"
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.ContentURL = "https://" + contentHost })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	zip := putTyped(t, s, key, f.ID, "pack.zip", "application/zip", "PK\x03\x04")
	wantStatus(t, call(t, s, "PATCH", "/api/v1/nodes/"+zip.ID, key, map[string]any{"no_download": true}), 200)
	vee, veeSess := verifiedUser(t, s, key, acc.ID, "vee@example.com")
	wantStatus(t, call(t, s, "PUT", "/api/v1/nodes/"+zip.ID+"/permissions/"+vee.ID, key,
		map[string]any{"role": "viewer", "notify": false}), 200)
	wantError(t, callSession(t, s, veeSess, "POST", "/api/v1/nodes/"+zip.ID+"/links",
		map[string]any{"disposition": "inline"}), 403, apierr.DownloadRestricted)
}

// sec1 (decision 84): a viewer may open a no-download PDF inline when content_url is configured. The design
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

// sec1 (decision 84): the main-origin refusal of an inline link to a no-download file applies to every
// caller. An unrestricted caller's inline link, minted with content_url set, points at the content host, so it is not
// refused and still opens inline there. The account key stands for the owner, who mints the same way.
func TestUnrestrictedInlineLinkWithContentURLStillOpens(t *testing.T) {
	t.Parallel()
	const contentHost = "files.example.test"
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.ContentURL = "https://" + contentHost })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	pdf := putTyped(t, s, key, f.ID, "doc.pdf", "application/pdf", "%PDF-1.4 test")
	wantStatus(t, call(t, s, "PATCH", "/api/v1/nodes/"+pdf.ID, key, map[string]any{"no_download": true}), 200)

	rec := call(t, s, "POST", "/api/v1/nodes/"+pdf.ID+"/links", key, map[string]any{"disposition": "inline"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("unrestricted inline link: status %d, want 201: %s", rec.Code, rec.Body)
	}
	url := decode[struct {
		URL string `json:"url"`
	}](t, rec).URL
	if !strings.HasPrefix(url, "https://"+contentHost+"/api/v1/links/") {
		t.Fatalf("link URL = %q, want the content host", url)
	}
	got := fromHost(t, s, contentHost, "GET", urlPath(t, url), nil, nil)
	wantStatus(t, got, 200)
	if !strings.HasPrefix(got.Header().Get("Content-Disposition"), "inline") {
		t.Errorf("unrestricted PDF on the content origin: disposition %q, want inline", got.Header().Get("Content-Disposition"))
	}
}
