// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"testing"
)

// setTemplateNoDownload sets no_download on a node, as the account key does.
func setTemplateNoDownload(t *testing.T, fx *officeFixture, nodeID string) {
	t.Helper()
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+nodeID, fx.key, map[string]any{"no_download": true}), 200)
}

// A viewer may not make a copy of a no-download template, as the copy route refuses it: the same status and the same
// code, download_restricted. A template inherits the rule from its folder, as the copy route applies it too.
func TestTemplateUnderNoDownloadIsRefusedToAViewerLikeACopy(t *testing.T) {
	t.Parallel()
	for _, inherited := range []bool{false, true} {
		name := "own flag"
		if inherited {
			name = "inherited from the templates folder"
		}
		t.Run(name, func(t *testing.T) {
			fx := newOfficeFixture(t)
			// ann is a viewer of the team folder, so of the templates folder and the template; she is an editor of work,
			// the folder the copy goes to (the template route needs editor on the target, decision 91).
			fx.grant(t, fx.fld.RootNodeID, fx.ann.ID, "viewer")
			work := newDirectoryAt(t, fx, "/Work")
			fx.grant(t, work.ID, fx.ann.ID, "editor")
			dir := newDirectoryAt(t, fx, "/Templates")
			tmpl := nodeOf(t, newDocumentCall(t, fx.s, fx.key, dir.ID, map[string]any{"type": "docx"}))
			if inherited {
				setTemplateNoDownload(t, fx, dir.ID)
			} else {
				setTemplateNoDownload(t, fx, tmpl.ID)
			}
			copyRec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+tmpl.ID+"/copy",
				map[string]any{"parent": work.ID})
			tplRec := newDocumentAs(t, fx.s, fx.annSess, work.ID, map[string]any{"template": tmpl.ID})
			if copyRec.Code != http.StatusForbidden || decodeError(t, copyRec).Code != "download_restricted" {
				t.Fatalf("the copy route answered %d %s; the test expects the refusal it gives",
					copyRec.Code, decodeError(t, copyRec).Code)
			}
			if tplRec.Code != copyRec.Code || decodeError(t, tplRec).Code != "download_restricted" {
				t.Errorf("the template route answered %d %s, the copy route %d download_restricted",
					tplRec.Code, decodeError(t, tplRec).Code, copyRec.Code)
			}
			if _, ok := childNamed(t, fx, "Untitled document.docx"); ok {
				t.Error("a refused template copy was made")
			}
		})
	}
}

// A contributor copies a no-download template, and the copy keeps the flag, as a copy of the file does (copyTree).
func TestTemplateUnderNoDownloadIsCopiedByAContributorAndKeepsTheFlag(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.grant(t, fx.fld.RootNodeID, fx.ann.ID, "contributor")
	work := newDirectoryAt(t, fx, "/Work")
	fx.grant(t, work.ID, fx.ann.ID, "editor")
	dir := newDirectoryAt(t, fx, "/Templates")
	tmpl := nodeOf(t, newDocumentCall(t, fx.s, fx.key, dir.ID, map[string]any{"type": "docx", "name": "Restricted"}))
	setTemplateNoDownload(t, fx, tmpl.ID)
	n := nodeOf(t, newDocumentAs(t, fx.s, fx.annSess, work.ID, map[string]any{"template": tmpl.ID}))
	if !n.NoDownload {
		t.Error("the copy does not keep no_download from its template")
	}
}

// A viewer does not see a no-download template in the listing, since it cannot make a copy of it; a contributor does.
func TestNoDownloadTemplateIsHiddenFromAViewerInTheListing(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.grant(t, fx.fld.RootNodeID, fx.ann.ID, "viewer")
	dir := newDirectoryAt(t, fx, "/Templates")
	tmpl := nodeOf(t, newDocumentCall(t, fx.s, fx.key, dir.ID, map[string]any{"type": "docx"}))
	setTemplatesFolder(t, fx, dir.ID)
	setTemplateNoDownload(t, fx, tmpl.ID)
	hasTmpl := func(items []listedTemplate) bool {
		for _, it := range items {
			if it.NodeID == tmpl.ID {
				return true
			}
		}
		return false
	}
	var viewer templateList
	viewer = decode[templateList](t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/templates?kind=document", nil))
	if hasTmpl(viewer.Items) {
		t.Error("a viewer sees a no-download template it cannot copy")
	}
	fx.grant(t, fx.fld.RootNodeID, fx.ann.ID, "contributor")
	var contributor templateList
	contributor = decode[templateList](t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/templates?kind=document", nil))
	if !hasTmpl(contributor.Items) {
		t.Error("a contributor does not see the no-download template it can copy")
	}
}
