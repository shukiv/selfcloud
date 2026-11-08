// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
)

// The JSON the templates routes answer, as the tests read it (decision 105).
type listedTemplate struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Source       string `json:"source"`
	NodeID       string `json:"node_id"`
	HasThumbnail *bool  `json:"has_thumbnail"`
}

type templateList struct {
	Items      []listedTemplate `json:"items"`
	NextCursor string           `json:"next_cursor"`
}

type templateFolder struct {
	NodeID *string `json:"node_id"`
}

// templatesPath is the account's templates setting.
func templatesPath(fx *officeFixture) string {
	return "/api/v1/accounts/" + fx.acc.ID + "/templates"
}

// setTemplatesFolder names dirID as the account's templates folder, as the manager's key does.
func setTemplatesFolder(t *testing.T, fx *officeFixture, dirID string) {
	t.Helper()
	wantStatus(t, call(t, fx.s, "PUT", templatesPath(fx), fx.key, map[string]any{"node_id": dirID}), 200)
}

// templatesFolderOf reads the account's templates setting as the manager's key.
func templatesFolderOf(t *testing.T, fx *officeFixture) *string {
	t.Helper()
	return decode[templateFolder](t, call(t, fx.s, "GET", templatesPath(fx), fx.key, nil)).NodeID
}

// listTemplates answers the templates of a kind, as the person behind cred sees them.
func listTemplates(t *testing.T, fx *officeFixture, query string) templateList {
	t.Helper()
	return decode[templateList](t, call(t, fx.s, "GET", "/api/v1/templates?"+query, fx.key, nil))
}

// newDirectoryAt makes a folder in the team folder and returns its node.
func newDirectoryAt(t *testing.T, fx *officeFixture, path string) nodeJSON {
	t.Helper()
	return nodeOf(t, call(t, fx.s, "POST", "/api/v1/folders/"+fx.fld.ID+"/directories", fx.key, map[string]any{"path": path}))
}

// The setting is changed by the account's owner or a manager only; a member with an editor's role on the folder is not
// told the setting exists.
func TestTemplatesFolderIsSetByAManagerOnly(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.grant(t, fx.fld.RootNodeID, fx.ann.ID, "editor")
	dir := newDirectoryAt(t, fx, "/Templates")
	body := map[string]any{"node_id": dir.ID}
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", templatesPath(fx), body), 404)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "GET", templatesPath(fx), nil), 404)
	setTemplatesFolder(t, fx, dir.ID)
	if got := templatesFolderOf(t, fx); got == nil || *got != dir.ID {
		t.Errorf("templates folder = %v, want %s", got, dir.ID)
	}
}

// The setting names a directory the caller can read: a file, a node that is not there, and nothing else. null clears it.
func TestTemplatesFolderRefusesAFileAndClears(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	wantStatus(t, call(t, fx.s, "PUT", templatesPath(fx), fx.key, map[string]any{"node_id": fx.doc.ID}), 400)
	// A well-formed node ID that names nothing is not found; a malformed one is a bad request.
	wantStatus(t, call(t, fx.s, "PUT", templatesPath(fx), fx.key, map[string]any{"node_id": id.New("nod")}), 404)
	wantStatus(t, call(t, fx.s, "PUT", templatesPath(fx), fx.key, map[string]any{"node_id": "nod_x"}), 400)
	wantStatus(t, call(t, fx.s, "PUT", templatesPath(fx), fx.key, map[string]any{}), 400)
	dir := newDirectoryAt(t, fx, "/Templates")
	setTemplatesFolder(t, fx, dir.ID)
	wantStatus(t, call(t, fx.s, "PUT", templatesPath(fx), fx.key, map[string]any{"node_id": nil}), 200)
	if got := templatesFolderOf(t, fx); got != nil {
		t.Errorf("after null the templates folder is %v, want none", *got)
	}
}

// A templates folder that is later trashed is none: the setting reads none, and the listing has no account files.
func TestTemplatesFolderTrashedIsNone(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	dir := newDirectoryAt(t, fx, "/Templates")
	if rec := newDocumentCall(t, fx.s, fx.key, dir.ID, map[string]any{"type": "docx"}); rec.Code != http.StatusCreated {
		t.Fatalf("the document in the templates folder: status %d", rec.Code)
	}
	setTemplatesFolder(t, fx, dir.ID)
	if rec := call(t, fx.s, "DELETE", "/api/v1/nodes/"+dir.ID, fx.key, nil); rec.Code >= 300 {
		t.Fatalf("trash the folder: status %d", rec.Code)
	}
	if got := templatesFolderOf(t, fx); got != nil {
		t.Errorf("a trashed templates folder reads as %v, want none", *got)
	}
	for _, it := range listTemplates(t, fx, "kind=document").Items {
		if it.Source == "account" {
			t.Errorf("the listing shows an account file %s from a trashed folder", it.Name)
		}
	}
}

// The listing answers the built-in starter set of the kind, in its order, and needs a kind it knows.
func TestListTemplatesAnswersTheBuiltIns(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	for _, q := range []string{"", "kind=pdf", "kind=drawing"} {
		wantStatus(t, call(t, fx.s, "GET", "/api/v1/templates?"+q, fx.key, nil), 400)
	}
	got := listTemplates(t, fx, "kind=document")
	var names []string
	for _, it := range got.Items {
		if it.Source != "builtin" || it.Kind != "document" || it.NodeID != "" || it.HasThumbnail != nil {
			t.Errorf("item %+v is not a built-in document template without a node", it)
		}
		names = append(names, it.ID+" "+it.Name)
	}
	want := "builtin:letter Letter.docx,builtin:resume Resume.docx,builtin:meeting-notes Meeting notes.docx"
	if strings.Join(names, ",") != want {
		t.Errorf("document templates = %s, want %s", strings.Join(names, ","), want)
	}
	if slides := listTemplates(t, fx, "kind=presentation"); len(slides.Items) != 1 || slides.Items[0].Name != "Slide deck.pptx" {
		t.Errorf("presentation templates = %+v", slides.Items)
	}
	wantStatus(t, call(t, fx.s, "GET", "/api/v1/templates?kind=document&limit=0", fx.key, nil), 400)
	wantStatus(t, call(t, fx.s, "GET", "/api/v1/templates?kind=document&limit=101", fx.key, nil), 400)
}

// With office editing off the listing is not found, as the office kinds are.
func TestListTemplatesIsNotFoundWithOfficeOff(t *testing.T) {
	t.Parallel()
	s, _, key, _, _ := quotaSetup(t, 0)
	wantStatus(t, call(t, s, "GET", "/api/v1/templates?kind=document", key, nil), 404)
}

// The account's templates folder adds its files of the kind's extensions (OOXML and ODF): a docx and an odt for a
// document, an xlsx for a spreadsheet, and nothing else. The files are the account's, with their node and no thumbnail,
// since an office file has none.
func TestListTemplatesShowsTheAccountFolderFiles(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	dir := newDirectoryAt(t, fx, "/Templates")
	docx := nodeOf(t, newDocumentCall(t, fx.s, fx.key, dir.ID, map[string]any{"type": "docx"}))
	odt := nodeOf(t, newDocumentCall(t, fx.s, fx.key, dir.ID, map[string]any{"type": "odt"}))
	sheet := nodeOf(t, newDocumentCall(t, fx.s, fx.key, dir.ID, map[string]any{"type": "xlsx"}))
	_ = nodeOf(t, newDocumentCall(t, fx.s, fx.key, dir.ID, map[string]any{"type": "odg"}))
	setTemplatesFolder(t, fx, dir.ID)
	docs := listTemplates(t, fx, "kind=document")
	if len(docs.Items) != 5 {
		t.Fatalf("document templates = %d items, want 3 built-in and 2 account files", len(docs.Items))
	}
	for _, it := range docs.Items[3:] {
		if it.Source != "account" || it.HasThumbnail == nil || *it.HasThumbnail {
			t.Errorf("account item %+v: want source account and has_thumbnail false", it)
		}
		if it.ID != it.NodeID {
			t.Errorf("account item id %s, node %s: want the node id", it.ID, it.NodeID)
		}
		if it.ID != docx.ID && it.ID != odt.ID {
			t.Errorf("account item %s is neither the docx nor the odt", it.Name)
		}
	}
	sheets := listTemplates(t, fx, "kind=spreadsheet")
	if len(sheets.Items) != 3 || sheets.Items[2].NodeID != sheet.ID {
		t.Errorf("spreadsheet templates = %+v, want 2 built-in and the xlsx", sheets.Items)
	}
}

// Account files are paged by a cursor. The built-ins are on the first page only, and the limit counts the account files.
func TestListTemplatesPagesTheAccountFiles(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	dir := newDirectoryAt(t, fx, "/Templates")
	for i := 0; i < 3; i++ {
		_ = nodeOf(t, newDocumentCall(t, fx.s, fx.key, dir.ID, map[string]any{"type": "docx"}))
	}
	setTemplatesFolder(t, fx, dir.ID)
	first := listTemplates(t, fx, "kind=document&limit=2")
	if len(first.Items) != 5 || first.NextCursor == "" {
		t.Fatalf("first page = %d items, next %q; want 3 built-in and 2 account, with a cursor", len(first.Items), first.NextCursor)
	}
	second := listTemplates(t, fx, "kind=document&limit=2&after="+first.NextCursor)
	if len(second.Items) != 1 || second.Items[0].Source != "account" || second.NextCursor != "" {
		t.Errorf("second page = %+v, next %q; want one account file and no cursor", second.Items, second.NextCursor)
	}
	wantStatus(t, call(t, fx.s, "GET", "/api/v1/templates?kind=document&after=not-a-cursor", fx.key, nil), 400)
}

// A document made from a built-in template is the template's file, named from the template, with the rights, the quota
// and the audit a new document has. The name given replaces the default and gets the extension.
func TestCreateFromBuiltinTemplate(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	// The team folder already holds letter.docx (the fixture's), so the copy goes to a folder of its own.
	dir := newDirectoryAt(t, fx, "/Letters")
	n := nodeOf(t, newDocumentCall(t, fx.s, fx.key, dir.ID, map[string]any{"template": "builtin:letter"}))
	if n.Name != "Letter.docx" || n.MimeType != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" {
		t.Errorf("Name = %q, MimeType = %q", n.Name, n.MimeType)
	}
	files := zipParts(t, call(t, fx.s, "GET", "/api/v1/nodes/"+n.ID+"/content", fx.key, nil).Body.Bytes())
	if !strings.Contains(string(files["word/document.xml"]), "Dear ") {
		t.Error("the made document is not the letter")
	}
	named := nodeOf(t, newDocumentCall(t, fx.s, fx.key, fx.fld.RootNodeID, map[string]any{"template": "builtin:letter", "name": "Plan"}))
	if named.Name != "Plan.docx" {
		t.Errorf("given name: Name = %q, want Plan.docx", named.Name)
	}
	var row *auditJSON
	for _, a := range auditOf(t, fx) {
		if a.Action == "node.create" && a.Target == n.ID {
			row = &a
		}
	}
	if row == nil || row.Details["via"] != "template" || row.Details["template"] != "builtin:letter" {
		t.Errorf("audit row = %+v, want node.create via template builtin:letter", row)
	}
}

// From an account template the copy shares the template's bytes, takes the quota of its size, and is audited with the
// template's node ID. The person needs editor on the folder shown and read on the template.
func TestCreateFromAccountTemplateSharesItsBytes(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	dir := newDirectoryAt(t, fx, "/Templates")
	work := newDirectoryAt(t, fx, "/Work")
	tmpl := nodeOf(t, newDocumentCall(t, fx.s, fx.key, dir.ID, map[string]any{"type": "docx", "name": "Proposal"}))
	before := usageOf(t, fx.s, fx.key, fx.acc.ID).UsedBytes
	n := nodeOf(t, newDocumentCall(t, fx.s, fx.key, work.ID, map[string]any{"template": tmpl.ID}))
	if n.Name != "Proposal.docx" || n.ParentID == nil || *n.ParentID != work.ID {
		t.Errorf("copy = %q in %v, want Proposal.docx in the work folder", n.Name, n.ParentID)
	}
	orig := call(t, fx.s, "GET", "/api/v1/nodes/"+tmpl.ID+"/content", fx.key, nil).Body.Bytes()
	copied := call(t, fx.s, "GET", "/api/v1/nodes/"+n.ID+"/content", fx.key, nil).Body.Bytes()
	if string(orig) != string(copied) {
		t.Error("the copy's bytes are not the template's")
	}
	if after := usageOf(t, fx.s, fx.key, fx.acc.ID).UsedBytes; after-before != tmpl.Size {
		t.Errorf("usage grew by %d, want the template's size %d", after-before, tmpl.Size)
	}
	var row *auditJSON
	for _, a := range auditOf(t, fx) {
		if a.Action == "node.create" && a.Target == n.ID {
			row = &a
		}
	}
	if row == nil || row.Details["via"] != "template" || row.Details["template"] != tmpl.ID {
		t.Errorf("audit row = %+v, want node.create via template %s", row, tmpl.ID)
	}
}

// A person who cannot read the template is told it is not found, as for a missing node.
func TestCreateFromAccountTemplateNeedsReadOnIt(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.grant(t, fx.fld.RootNodeID, fx.ann.ID, "editor")
	other := newFolder(t, fx.s, fx.key, fx.acc.ID, "Other")
	tmpl := nodeOf(t, newDocumentCall(t, fx.s, fx.key, other.RootNodeID, map[string]any{"type": "docx"}))
	wantStatus(t, newDocumentAs(t, fx.s, fx.annSess, fx.fld.RootNodeID, map[string]any{"template": tmpl.ID}), 404)
}

// The body names a type or a template, not both and not neither; a built-in must exist; an account node must be a
// template file of the kind (a document, spreadsheet or presentation file), not a folder or a text file.
func TestCreateFromTemplateRefusesBadInput(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	dir := newDirectoryAt(t, fx, "/Templates")
	txt := nodeOf(t, newDocumentCall(t, fx.s, fx.key, dir.ID, map[string]any{"type": "txt"}))
	for _, body := range []map[string]any{
		{"type": "docx", "template": "builtin:letter"},
		{"template": "builtin:nope"},
		{"template": txt.ID},
		{"template": dir.ID},
		{"template": "x"},
	} {
		wantStatus(t, newDocumentCall(t, fx.s, fx.key, fx.fld.RootNodeID, body), 400)
	}
}

// With office editing off a built-in template is not found, and nothing is made.
func TestCreateFromTemplateIsNotFoundWithOfficeOff(t *testing.T) {
	t.Parallel()
	s, _, key, _, f := quotaSetup(t, 0)
	wantStatus(t, newDocumentCall(t, s, key, f.RootNodeID, map[string]any{"template": "builtin:letter"}), 404)
}

// Over the quota a template copy is refused, and nothing is left behind.
func TestCreateFromTemplateOverQuotaMakesNothing(t *testing.T) {
	t.Parallel()
	fx := newDocumentQuotaFixture(t, 100)
	wantStatus(t, newDocumentCall(t, fx.s, fx.key, fx.fld.RootNodeID, map[string]any{"template": "builtin:letter"}), http.StatusInsufficientStorage)
	if _, ok := childNamed(t, fx, "Letter.docx"); ok {
		t.Error("a refused template document was made")
	}
}

// The setting's rights and its audit: a change is audited, and an unknown account is not found for a plain member.
func TestTemplatesFolderChangeIsAudited(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	dir := newDirectoryAt(t, fx, "/Templates")
	setTemplatesFolder(t, fx, dir.ID)
	found := false
	for _, a := range auditOf(t, fx) {
		if a.Action == "templates.folder" && a.Target == fx.acc.ID && a.Details["node"] == dir.ID {
			found = true
		}
	}
	if !found {
		t.Error("no templates.folder audit row for the change")
	}
}
