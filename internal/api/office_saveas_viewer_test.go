// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"strings"
	"testing"
)

// A browser view of a file by a viewer, who may not add files to the file's folder, is refused Save As: CheckFileInfo
// says UserCanNotWriteRelative, PUT_RELATIVE answers 501, and nothing is made. This is the live run's shape: the view-mode
// editor offers no Save As, and the server must refuse one anyway. Not a no-download file (TestWOPISaveAsNoDownloadAnswers501),
// and not a delegated token (TestWOPISaveAsDelegatedTokenIsRefused).
func TestWOPISaveAsViewerOfAFileAnswers501(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.grant(t, fx.doc.ID, fx.ann.ID, "viewer")
	// The viewer may see the folder, so the only thing that refuses the copy is the rule that a copy needs a contributor.
	fx.grant(t, fx.fld.RootNodeID, fx.ann.ID, "viewer")
	view := decode[officeJSON](t, fx.open(t, fx.annSess, fx.doc.ID, "view")).Token
	if info := decodeInfo(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), view, nil, nil)); info["UserCanNotWriteRelative"] != true {
		t.Errorf("UserCanNotWriteRelative = %v for a viewer's view token, want true", info["UserCanNotWriteRelative"])
	}
	before := rootChildren(t, fx)
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), view, strings.NewReader("copy"),
		saveAsHdr("", ".txt", false)), 501)
	if len(rootChildren(t, fx)) != len(before) {
		t.Error("a refused Save As made a file")
	}
	if got := fx.fileText(t, fx.doc.ID); got != "hello" {
		t.Errorf("the file reads %q after a refused Save As, want hello", got)
	}
}
