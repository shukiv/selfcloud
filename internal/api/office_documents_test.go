// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/wopi/wopitest"
)

// newDocumentCall asks for a blank document in the folder or directory nodeID; body is the JSON request.
func newDocumentCall(t *testing.T, s *Server, cred, nodeID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	return call(t, s, "POST", "/api/v1/nodes/"+nodeID+"/documents", cred, body)
}

// newDocumentAs is newDocumentCall as a signed-in person.
func newDocumentAs(t *testing.T, s *Server, sess testSession, nodeID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	return callSession(t, s, sess, "POST", "/api/v1/nodes/"+nodeID+"/documents", body)
}

// wellFormedXML fails when data is not XML that decodes to its end.
func wellFormedXML(data []byte) error {
	d := xml.NewDecoder(bytes.NewReader(data))
	for {
		_, err := d.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// zipParts reads an OOXML package into its parts, by name, and fails the test when it is not a zip.
func zipParts(t *testing.T, body []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("the file is not a zip package: %v", err)
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("part %s: %v", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("part %s: %v", f.Name, err)
		}
		files[f.Name] = data
	}
	return files
}

// wantOOXMLPackage checks a blank document: the parts every package needs and the ones its kind needs are present and
// are well-formed XML, [Content_Types].xml declares the main part with its media type, and _rels/.rels points at it.
func wantOOXMLPackage(t *testing.T, body []byte, main, mainType string, parts ...string) {
	t.Helper()
	files := zipParts(t, body)
	for _, p := range append([]string{"[Content_Types].xml", "_rels/.rels", main}, parts...) {
		data, ok := files[p]
		if !ok {
			t.Errorf("the package has no part %s", p)
			continue
		}
		if err := wellFormedXML(data); err != nil {
			t.Errorf("part %s is not well-formed XML: %v", p, err)
		}
	}
	if ct := string(files["[Content_Types].xml"]); !strings.Contains(ct, `PartName="/`+main+`"`) || !strings.Contains(ct, mainType) {
		t.Errorf("[Content_Types].xml does not declare /%s as %s", main, mainType)
	}
	if rels := string(files["_rels/.rels"]); !strings.Contains(rels, `Target="`+main+`"`) {
		t.Errorf("_rels/.rels does not point at %s", main)
	}
}

// newDocumentQuotaFixture is the office fixture with the account's quota set (0 is unlimited): the editor is on, and the
// team folder is the account's.
func newDocumentQuotaFixture(t *testing.T, quota int64) *officeFixture {
	t.Helper()
	col := wopitest.New(t)
	on := func(c *config.Config) {
		c.Office.Enabled = true
		c.Office.DiscoveryURL = col.DiscoveryURL()
	}
	s := newAuthServer(t, &mail.Recorder{}, on)
	fx := &officeFixture{s: s, col: col}
	fx.admin = adminKey(t, s)
	acc, key := newQuotaAccount(t, s, fx.admin, "Acme", quota)
	fx.acc, fx.key = acc, key
	fx.fld = newFolder(t, s, fx.key, fx.acc.ID, "Team")
	fx.setDiscovery(col.Discovery())
	return fx
}

// childNamed returns the child of the folder's root with the name, and whether there is one.
func childNamed(t *testing.T, fx *officeFixture, name string) (nodeJSON, bool) {
	t.Helper()
	c, ok := rootChildren(t, fx)[name]
	return c, ok
}

// The blank document of each kind is a valid package with the parts its kind needs, and is named and typed as the kind says.
func TestNewDocumentMakesEachKindAsAValidPackage(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	cases := []struct {
		kind, name, mime, main, mainType string
		parts                            []string
	}{
		{"docx", "Untitled document.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
			"word/document.xml", "wordprocessingml.document.main+xml", nil},
		{"xlsx", "Untitled spreadsheet.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
			"xl/workbook.xml", "spreadsheetml.sheet.main+xml", []string{"xl/worksheets/sheet1.xml", "xl/_rels/workbook.xml.rels"}},
		{"pptx", "Untitled presentation.pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation",
			"ppt/presentation.xml", "presentationml.presentation.main+xml", []string{
				"ppt/_rels/presentation.xml.rels", "ppt/slideMasters/slideMaster1.xml", "ppt/slideMasters/_rels/slideMaster1.xml.rels",
				"ppt/slideLayouts/slideLayout1.xml", "ppt/slideLayouts/_rels/slideLayout1.xml.rels",
				"ppt/slides/slide1.xml", "ppt/slides/_rels/slide1.xml.rels", "ppt/theme/theme1.xml"}},
	}
	for _, c := range cases {
		rec := newDocumentCall(t, fx.s, fx.key, fx.fld.RootNodeID, map[string]any{"type": c.kind})
		n := nodeOf(t, rec)
		if n.Name != c.name || n.Kind != "file" || n.MimeType != c.mime {
			t.Errorf("%s: node = name %q kind %q mime %q; want %q file %q", c.kind, n.Name, n.Kind, n.MimeType, c.name, c.mime)
		}
		body := call(t, fx.s, "GET", "/api/v1/nodes/"+n.ID+"/content", fx.key, nil).Body.Bytes()
		if int64(len(body)) != n.Size || n.Size == 0 {
			t.Errorf("%s: the content is %d bytes, the node says %d", c.kind, len(body), n.Size)
		}
		wantOOXMLPackage(t, body, c.main, c.mainType, c.parts...)
	}
}

// A name taken in the folder gets the next free "name (n).ext", as an upload's copy does; a given name gets its extension.
func TestNewDocumentTakesTheNextFreeName(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	root := fx.fld.RootNodeID
	if first := nodeOf(t, newDocumentCall(t, fx.s, fx.key, root, map[string]any{"type": "docx"})); first.Name != "Untitled document.docx" {
		t.Errorf("first Name = %q, want Untitled document.docx", first.Name)
	}
	second := nodeOf(t, newDocumentCall(t, fx.s, fx.key, root, map[string]any{"type": "docx"}))
	if second.Name != "Untitled document (1).docx" {
		t.Errorf("second Name = %q, want Untitled document (1).docx", second.Name)
	}
	third := nodeOf(t, newDocumentCall(t, fx.s, fx.key, root, map[string]any{"type": "docx"}))
	if third.Name != "Untitled document (2).docx" {
		t.Errorf("third Name = %q, want Untitled document (2).docx", third.Name)
	}
	// A given name gets the type's extension when it has none, and keeps it when it has it.
	if got := nodeOf(t, newDocumentCall(t, fx.s, fx.key, root, map[string]any{"type": "xlsx", "name": "Budget"})); got.Name != "Budget.xlsx" {
		t.Errorf("Name = %q, want Budget.xlsx", got.Name)
	}
	if got := nodeOf(t, newDocumentCall(t, fx.s, fx.key, root, map[string]any{"type": "xlsx", "name": "Budget.xlsx"})); got.Name != "Budget (1).xlsx" {
		t.Errorf("Name = %q, want Budget (1).xlsx", got.Name)
	}
	if got := nodeOf(t, newDocumentCall(t, fx.s, fx.key, root, map[string]any{"type": "pptx", "name": "Plan.pdf"})); got.Name != "Plan.pdf.pptx" {
		t.Errorf("Name = %q, want Plan.pdf.pptx (a different extension is kept and the type's is added)", got.Name)
	}
}

// The default names follow the signed-in person's language, and fall back to English for a key.
func TestNewDocumentDefaultNameFollowsTheUsersLanguage(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.grant(t, fx.fld.RootNodeID, fx.ann.ID, "contributor")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PATCH", "/api/v1/me", map[string]any{"locale": "he"}), 200)
	got := nodeOf(t, newDocumentAs(t, fx.s, fx.annSess, fx.fld.RootNodeID, map[string]any{"type": "docx"}))
	if got.Name != "מסמך ללא שם.docx" {
		t.Errorf("Hebrew Name = %q, want מסמך ללא שם.docx", got.Name)
	}
	if en := nodeOf(t, newDocumentCall(t, fx.s, fx.key, fx.fld.RootNodeID, map[string]any{"type": "docx"})); en.Name != "Untitled document.docx" {
		t.Errorf("a key's Name = %q, want Untitled document.docx", en.Name)
	}
}

// A document goes into a subfolder by its directory's node ID, with the audit row a file upload writes plus via "document".
func TestNewDocumentInASubfolderIsAuditedLikeAnUpload(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	dir := nodeOf(t, call(t, fx.s, "POST", "/api/v1/folders/"+fx.fld.ID+"/directories", fx.key, map[string]any{"path": "/Sub"}))
	n := nodeOf(t, newDocumentCall(t, fx.s, fx.key, dir.ID, map[string]any{"type": "pptx"}))
	if n.ParentID == nil || *n.ParentID != dir.ID {
		t.Fatalf("the document's parent = %v, want %s", n.ParentID, dir.ID)
	}
	var row *auditJSON
	for _, a := range auditOf(t, fx) {
		if a.Action == "node.create" && a.Target == n.ID {
			row = &a
		}
	}
	if row == nil {
		t.Fatal("no node.create audit row for the document")
	}
	if row.Details["kind"] != "file" || row.Details["name"] != n.Name || row.Details["parent"] != dir.ID ||
		row.Details["size"] != float64(n.Size) || row.Details["via"] != "document" {
		t.Errorf("audit details = %v", row.Details)
	}
}

// A person who may not add files to the folder is refused, and nothing is made.
func TestNewDocumentNeedsContributorOnTheFolder(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.grant(t, fx.fld.RootNodeID, fx.ann.ID, "viewer")
	rec := newDocumentAs(t, fx.s, fx.annSess, fx.fld.RootNodeID, map[string]any{"type": "docx"})
	wantStatus(t, rec, 403)
	// bob holds nothing on the folder: the folder is not reached, as for any write.
	wantStatus(t, newDocumentAs(t, fx.s, fx.bobSess, fx.fld.RootNodeID, map[string]any{"type": "docx"}), 404)
	if _, ok := childNamed(t, fx, "Untitled document.docx"); ok {
		t.Error("a refused document was made")
	}
}

// Over the account's quota the document is refused before anything is written, as an upload is.
func TestNewDocumentOverQuotaMakesNothing(t *testing.T) {
	t.Parallel()
	fx := newDocumentQuotaFixture(t, 100)
	rec := newDocumentCall(t, fx.s, fx.key, fx.fld.RootNodeID, map[string]any{"type": "docx"})
	wantStatus(t, rec, http.StatusInsufficientStorage)
	if u := usageOf(t, fx.s, fx.key, fx.acc.ID); u.UsedBytes != 0 || u.Files != 0 {
		t.Errorf("usage after a refusal = %+v; the refused document must leave nothing behind", u)
	}
	if _, ok := childNamed(t, fx, "Untitled document.docx"); ok {
		t.Error("a refused document was made")
	}
}

// With the editor off, the route is not found, as the other office routes are.
func TestNewDocumentIsNotFoundWithOfficeOff(t *testing.T) {
	t.Parallel()
	s, _, key, _, f := quotaSetup(t, 0)
	wantStatus(t, newDocumentCall(t, s, key, f.RootNodeID, map[string]any{"type": "docx"}), 404)
}

// The answer is refused for a bad type, a name with a slash, a file in place of a folder, and a body with no type.
func TestNewDocumentRefusesBadInput(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	root := fx.fld.RootNodeID
	for _, body := range []map[string]any{
		{"type": "pdf"},
		{},
		{"type": "docx", "name": "a/b"},
	} {
		wantStatus(t, newDocumentCall(t, fx.s, fx.key, root, body), 400)
	}
	wantStatus(t, newDocumentCall(t, fx.s, fx.key, fx.doc.ID, map[string]any{"type": "docx"}), 400)
	if got := len(rootChildren(t, fx)); got != 1 {
		t.Errorf("the folder has %d children after refusals, want only letter.docx", got)
	}
}
