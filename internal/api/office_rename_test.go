// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/wopi"
)

// renameHdr is the header set of a RenameFile call: the override, the name UTF-7 encoded without its extension, and
// the lock the caller holds ("" for none).
func renameHdr(requested, lockID string) map[string]string {
	h := map[string]string{"X-WOPI-Override": "RENAME_FILE", "X-WOPI-RequestedName": wopi.EncodeUTF7(requested)}
	if lockID != "" {
		h["X-WOPI-Lock"] = lockID
	}
	return h
}

// nodeNameOf reads a node's name from the API, as the key sees it.
func nodeNameOf(t *testing.T, fx *officeFixture, nodeID string) string {
	t.Helper()
	return decode[nodeJSON](t, call(t, fx.s, "GET", "/api/v1/nodes/"+nodeID, fx.key, nil)).Name
}

// renameAnswer is the JSON body of a successful RenameFile.
func renameAnswer(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct {
		Name string `json:"Name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("RenameFile body %q: %v", rec.Body.String(), err)
	}
	return out.Name
}

// wantInvalidName: a refused name answers 400 with X-WOPI-InvalidFileNameError, and the file keeps its name.
func wantInvalidName(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	wantStatus(t, rec, 400)
	if rec.Header().Get("X-WOPI-InvalidFileNameError") == "" {
		t.Errorf("a 400 for a bad name has no X-WOPI-InvalidFileNameError header")
	}
}

// TestWOPIRenameFileRenamesAndKeepsTheExtension: the editor's rename changes the name, answers the JSON Name without
// the extension, keeps the file's ID, and is audited as a rename. Non-Latin names come through UTF-7 intact.
func TestWOPIRenameFileRenamesAndKeepsTheExtension(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tok := fx.openToken(t, "edit")
	rt := nodeRoute(fx.doc.ID)

	rec := fx.wopiCall(t, "POST", rt, tok, nil, renameHdr("quarterly report", ""))
	wantStatus(t, rec, 200)
	if got := renameAnswer(t, rec); got != "quarterly report" {
		t.Errorf("Name = %q, want %q (no extension)", got, "quarterly report")
	}
	if got := nodeNameOf(t, fx, fx.doc.ID); got != "quarterly report.docx" {
		t.Errorf("stored name = %q, want quarterly report.docx", got)
	}

	// A change of case only is a rename of the file to its own name, not a clash with itself (decision 73).
	wantStatus(t, fx.wopiCall(t, "POST", rt, tok, nil, renameHdr("Quarterly Report", "")), 200)
	if got := nodeNameOf(t, fx, fx.doc.ID); got != "Quarterly Report.docx" {
		t.Errorf("stored name = %q, want Quarterly Report.docx", got)
	}

	rec = fx.wopiCall(t, "POST", rt, tok, nil, renameHdr("שלום עולם", ""))
	wantStatus(t, rec, 200)
	if got := nodeNameOf(t, fx, fx.doc.ID); got != "שלום עולם.docx" {
		t.Errorf("stored name = %q, want the Hebrew name with .docx", got)
	}
	if got := renameAnswer(t, rec); got != "שלום עולם" {
		t.Errorf("Name = %q, want the Hebrew name", got)
	}

	// The file's ID and WOPISrc do not change, so the same token still reaches the file.
	wantStatus(t, fx.wopiCall(t, "GET", rt, tok, nil, nil), 200)

	// Each rename is audited with the names before and after it.
	var got []string
	for _, e := range auditOf(t, fx) {
		if e.Action == "node.rename" && e.Target == fx.doc.ID {
			got = append(got, fmt.Sprintf("%v -> %v", e.Details["from"], e.Details["to"]))
		}
	}
	for _, want := range []string{"letter.docx -> quarterly report.docx", "quarterly report.docx -> Quarterly Report.docx",
		"Quarterly Report.docx -> שלום עולם.docx"} {
		if !slices.Contains(got, want) {
			t.Errorf("no node.rename audit entry %q; the entries are %q", want, got)
		}
	}
}

// TestWOPIRenameFileRefusesAnInvalidName: a name the rules refuse answers 400 with the reason, and nothing changes. A
// name that clashes with a sibling is refused the same way, since the rename cannot be made under that name.
func TestWOPIRenameFileRefusesAnInvalidName(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tok := fx.openToken(t, "edit")
	rt := nodeRoute(fx.doc.ID)
	for _, bad := range []string{"", "a/b", "back\\slash"} {
		wantInvalidName(t, fx.wopiCall(t, "POST", rt, tok, nil, renameHdr(bad, "")))
	}
	putNode(t, fx.s, fx.key, fx.fld.ID, "/taken.docx", "other")
	wantInvalidName(t, fx.wopiCall(t, "POST", rt, tok, nil, renameHdr("taken", "")))
	if got := nodeNameOf(t, fx, fx.doc.ID); got != "letter.docx" {
		t.Errorf("a refused rename changed the name to %q", got)
	}
	// A bad UTF-7 name is refused as a bad request, not stored.
	wantStatus(t, fx.wopiCall(t, "POST", rt, tok, nil,
		map[string]string{"X-WOPI-Override": "RENAME_FILE", "X-WOPI-RequestedName": "café"}), 400)
}

// TestWOPIRenameFileAgainstLocks: an unlocked file is renamed; the holder of the lock may rename its locked file; a
// lock held by another editor, or a missing lock ID on a locked file, answers 409 with the current lock.
func TestWOPIRenameFileAgainstLocks(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tok := fx.openToken(t, "edit")
	rt := nodeRoute(fx.doc.ID)

	fx.lockFor(t, tok, "HOLD")
	// A missing lock ID on a locked file is a mismatch.
	wantLockAnswer(t, fx.wopiCall(t, "POST", rt, tok, nil, renameHdr("missing", "")), "HOLD")
	wantLockAnswer(t, fx.wopiCall(t, "POST", rt, tok, nil, renameHdr("wrong", "OTHER")), "HOLD")
	if got := nodeNameOf(t, fx, fx.doc.ID); got != "letter.docx" {
		t.Fatalf("a refused rename changed the name to %q", got)
	}
	// The holder renames its own locked file, and the lock survives the rename.
	wantStatus(t, fx.wopiCall(t, "POST", rt, tok, nil, renameHdr("held", "HOLD")), 200)
	if got := nodeNameOf(t, fx, fx.doc.ID); got != "held.docx" {
		t.Errorf("the holder's rename stored %q, want held.docx", got)
	}
	if got := liveLockID(t, fx.s, fx.acc.ID, fx.doc.ID); got != "HOLD" {
		t.Errorf("the lock after the holder's rename = %q, want HOLD", got)
	}
	// A rename made through the API while the editor holds the file is refused by the store's lock rule.
	rec := call(t, fx.s, "PATCH", "/api/v1/nodes/"+fx.doc.ID, fx.key, map[string]any{"name": "api name"})
	wantStatus(t, rec, 409)

	wantStatus(t, fx.wopiCall(t, "POST", rt, tok, nil, wopiLockHdr("UNLOCK", "HOLD")), 200)
	wantStatus(t, fx.wopiCall(t, "POST", rt, tok, nil, renameHdr("free", "")), 200)
	if got := nodeNameOf(t, fx, fx.doc.ID); got != "free.docx" {
		t.Errorf("rename of an unlocked file stored %q, want free.docx", got)
	}
}

// TestWOPIRenameFileNeedsAWritableToken: a viewer's token, and a no-download viewer's, cannot rename: 404 as for PutFile.
// A no-download editor keeps its right to rename, as it keeps its right to save.
func TestWOPIRenameFileNeedsAWritableToken(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.grant(t, fx.doc.ID, fx.bob.ID, "viewer")
	viewRec := fx.open(t, fx.bobSess, fx.doc.ID, "view")
	wantStatus(t, viewRec, 200)
	viewTok := decode[officeJSON](t, viewRec).Token
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), viewTok, nil, renameHdr("viewer", "")), 404)

	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+fx.doc.ID, fx.key, map[string]any{"no_download": true}), 200)
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), viewTok, nil, renameHdr("viewer", "")), 404)
	if got := nodeNameOf(t, fx, fx.doc.ID); got != "letter.docx" {
		t.Fatalf("a viewer renamed the file to %q", got)
	}

	fx.grant(t, fx.doc.ID, fx.ann.ID, "editor")
	editTok := decode[officeJSON](t, fx.open(t, fx.annSess, fx.doc.ID, "edit")).Token
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), editTok, nil, renameHdr("editor", "")), 200)
}

// TestWOPIRenameFileAfterAFolderAboveMovedMeanwhile: a folder above the file moved while the editor is open. A locked
// file's folder cannot be moved (the lock rule); an unlocked file's token no longer reaches the file in its old place,
// so the rename answers 404 until the editor is opened again (decision 31).
func TestWOPIRenameFileAfterAFolderAboveMovedMeanwhile(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	boxDir := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Box")
	box := putNode(t, fx.s, fx.key, fx.fld.ID, "/Box/boxed.docx", "inside")
	other := newFolder(t, fx.s, fx.key, fx.acc.ID, "Other")
	fx.grant(t, box.ID, fx.ann.ID, "editor")
	editRec := fx.open(t, fx.annSess, box.ID, "edit")
	wantStatus(t, editRec, 200)
	tok := decode[officeJSON](t, editRec).Token
	boxFolder := boxDir.ID

	// Locked: the folder above it cannot move.
	fx.lockNodeFor(t, box.ID, tok, "BOX")
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+boxFolder+"/move", fx.key,
		map[string]any{"folder": other.ID, "parent": other.RootNodeID}), 409)
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(box.ID), tok, nil, renameHdr("still here", "BOX")), 200)
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(box.ID), tok, nil, wopiLockHdr("UNLOCK", "BOX")), 200)

	// Unlocked: the folder moves, and the old token is refused for the rename.
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+boxFolder+"/move", fx.key,
		map[string]any{"folder": other.ID, "parent": other.RootNodeID}), 200)
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(box.ID), tok, nil, renameHdr("moved", "")), 404)
	if got := nodeNameOf(t, fx, box.ID); strings.HasPrefix(got, "moved") {
		t.Errorf("a refused rename after the move changed the name to %q", got)
	}
}

// auditOf returns the account's audit entries, newest first.
func auditOf(t *testing.T, fx *officeFixture) []auditJSON {
	t.Helper()
	rec := call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/audit", fx.key, nil)
	wantStatus(t, rec, 200)
	return decode[struct {
		Items []auditJSON `json:"items"`
	}](t, rec).Items
}

// TestWOPICheckFileInfoSaysWhetherTheUserCanRename: SupportsRename is true, and UserCanRename follows the write right.
func TestWOPICheckFileInfoSaysWhetherTheUserCanRename(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tok := fx.openToken(t, "edit")
	info := decodeInfo(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), tok, nil, nil))
	if info["SupportsRename"] != true || info["UserCanRename"] != true {
		t.Errorf("editor: SupportsRename = %v, UserCanRename = %v; want true, true", info["SupportsRename"], info["UserCanRename"])
	}
	fx.grant(t, fx.doc.ID, fx.bob.ID, "viewer")
	viewTok := decode[officeJSON](t, fx.open(t, fx.bobSess, fx.doc.ID, "view")).Token
	info = decodeInfo(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), viewTok, nil, nil))
	if info["SupportsRename"] != true || info["UserCanRename"] != false {
		t.Errorf("viewer: SupportsRename = %v, UserCanRename = %v; want true, false", info["SupportsRename"], info["UserCanRename"])
	}
}
