// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"testing"
)

// A manager of the templates folder who is not a manager of the account may not name the folder: the setting is the
// account's, and it is changed by the account's managers (ManagesAccount, as the archive switch checks).
func TestFolderManagerCannotSetTheTemplatesFolder(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	dir := newDirectoryAt(t, fx, "/Templates")
	fx.grant(t, dir.ID, fx.ann.ID, "manager")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", templatesPath(fx), map[string]any{"node_id": dir.ID}), 404)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "GET", templatesPath(fx), nil), 404)
	if got := templatesFolderOf(t, fx); got != nil {
		t.Errorf("the setting is %v after a folder manager's refused PUT, want none", *got)
	}
}

// A copy of an account template is charged its size in full, as a blank file is, and a copy over the quota makes nothing.
// The template itself fits the quota; a second file of its size does not.
func TestCreateFromAccountTemplateOverQuotaMakesNothing(t *testing.T) {
	t.Parallel()
	fx := newDocumentQuotaFixture(t, 1500)
	dir := newDirectoryAt(t, fx, "/Templates")
	tmpl := nodeOf(t, newDocumentCall(t, fx.s, fx.key, dir.ID, map[string]any{"type": "docx", "name": "Proposal"}))
	before := usageOf(t, fx.s, fx.key, fx.acc.ID)
	wantStatus(t, newDocumentCall(t, fx.s, fx.key, fx.fld.RootNodeID, map[string]any{"template": tmpl.ID}), http.StatusInsufficientStorage)
	if after := usageOf(t, fx.s, fx.key, fx.acc.ID); after.UsedBytes != before.UsedBytes || after.Files != before.Files {
		t.Errorf("usage changed from %+v to %+v after a refused copy", before, after)
	}
	if _, ok := childNamed(t, fx, "Proposal.docx"); ok {
		t.Error("a refused template copy was made")
	}
}

// A share-link visitor is refused by the two routes that list and copy templates, as the other routes that take no link
// are: 403 with reason link_route, before the route runs.
func TestTemplatesRoutesRefuseAShareLinkVisitor(t *testing.T) {
	t.Parallel()
	fx := newAnyoneFixture(t)
	root := fx.rootOf(t, fx.fld.ID)
	_, secret := fx.makeAnyone(t, root, nil)
	visitor := fx.visitAnyone(t, secret, "")
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/templates?kind=document"},
		{"POST", "/api/v1/nodes/" + root + "/documents"},
	} {
		rec := fx.visitor(t, visitor, c.method, c.path)
		if e := wantError(t, rec, http.StatusForbidden, "forbidden"); e.Details["reason"] != "link_route" {
			t.Errorf("%s %s: reason %v, want link_route", c.method, c.path, e.Details["reason"])
		}
	}
}

// A listing whose page holds exactly the files left carries no cursor: a cursor is given only when a further file exists.
func TestTemplatesListingHasNoCursorAfterTheLastFile(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	dir := newDirectoryAt(t, fx, "/Templates")
	for i := 0; i < 2; i++ {
		_ = nodeOf(t, newDocumentCall(t, fx.s, fx.key, dir.ID, map[string]any{"type": "docx"}))
	}
	setTemplatesFolder(t, fx, dir.ID)
	page := listTemplates(t, fx, "kind=document&limit=2")
	// Deliberate change (nt2): five built-in documents now, so seven items with the two account files.
	if len(page.Items) != 7 || page.NextCursor != "" {
		t.Errorf("page = %d items, next %q; want 5 built-in and 2 account files, and no cursor", len(page.Items), page.NextCursor)
	}
}
