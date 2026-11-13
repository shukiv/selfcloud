// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/auth"
)

// hookReader runs once, before its first read. A test uses it to change the source while the body is still arriving, and
// records whether any of the body was read.
type hookReader struct {
	r    io.Reader
	once func()
	read bool
}

func (h *hookReader) Read(p []byte) (int, error) {
	h.read = true
	if h.once != nil {
		f := h.once
		h.once = nil
		f()
	}
	return h.r.Read(p)
}

// saveAsBlobFiles counts the files the account holds in the blob store: its committed blobs and its uploads in progress. A
// refused write must leave none (TestWOPIPutRelativeRefusesBeforeAnyByteLands).
func saveAsBlobFiles(t *testing.T, fx *officeFixture) int {
	t.Helper()
	n := 0
	for _, sub := range []string{"blobs", "uploads"} {
		root := filepath.Join(fx.s.cfg.DataDir, sub, fx.acc.ID)
		err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				n++
			}
			return nil
		})
		if err != nil {
			t.Fatalf("cannot count the blob store under %s: %v", root, err)
		}
	}
	return n
}

// wopiCallSized is wopiCall with the body's length declared, as a client that sends Content-Length does.
func wopiCallSized(t *testing.T, fx *officeFixture, path, token string, body io.Reader, n int64, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := fx.wopiReq("POST", path, token, body, hdr)
	req.ContentLength = n
	rec := httptest.NewRecorder()
	fx.s.Handler().ServeHTTP(rec, req)
	return rec
}

// answerCanWrite reads CheckFileInfo for the node a PutRelativeFile answer names, with the token in its Url, and reports
// whether that token may write.
func answerCanWrite(t *testing.T, fx *officeFixture, out map[string]string, nodeID string) bool {
	t.Helper()
	tok, ok := strings.CutPrefix(out["Url"], wopiFileURLOf(t, fx, nodeID)+"?access_token=")
	if !ok || tok == "" {
		t.Fatalf("Url %q is not the WOPI address of node %s with a token", out["Url"], nodeID)
	}
	return decodeInfo(t, fx.wopiCall(t, "GET", nodeRoute(nodeID), tok, nil, nil))["UserCanWrite"] == true
}

// delegatedWOPIToken mints a token for ann on the document with the given permission, opens the editor with it in the
// given mode, and returns the WOPI token the editor was given. Its origin is the delegated key, not a browser session.
func delegatedWOPIToken(t *testing.T, fx *officeFixture, permission, mode string) string {
	t.Helper()
	dtok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"user": fx.ann.ID, "folders": []string{fx.fld.ID},
		"permissions": permission})
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.doc.ID+"/office", dtok, map[string]any{"mode": mode})
	wantStatus(t, rec, 200)
	return decode[officeJSON](t, rec).Token
}

// TestWOPISaveAsDelegatedTokenIsRefused: a WOPI token minted by a delegated key never gets Save As, whatever its
// permission. CheckFileInfo says UserCanNotWriteRelative, and PUT_RELATIVE answers 501 for a copy and for an overwrite.
// Nothing is made or changed (decision 87, the rule for delegated origins).
func TestWOPISaveAsDelegatedTokenIsRefused(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ permission, mode string }{{"read", "view"}, {"write", "edit"}} {
		t.Run(c.permission, func(t *testing.T) {
			t.Parallel()
			fx, _ := saveAsFixture(t)
			fx.grant(t, fx.doc.ID, fx.ann.ID, "editor")
			other := putNode(t, fx.s, fx.key, fx.fld.ID, "/other.docx", "other")
			fx.grant(t, other.ID, fx.ann.ID, "editor")
			tok := delegatedWOPIToken(t, fx, c.permission, c.mode)
			if info := decodeInfo(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), tok, nil, nil)); info["UserCanNotWriteRelative"] != true {
				t.Errorf("UserCanNotWriteRelative = %v for a delegated %s token, want true", info["UserCanNotWriteRelative"], c.permission)
			}
			before := rootChildren(t, fx)
			wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("copy"),
				saveAsHdr("", ".txt", false)), 501)
			wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("over"),
				saveAsHdr("other.docx", "", true)), 501)
			after := rootChildren(t, fx)
			if len(after) != len(before) {
				t.Errorf("a refused Save As changed the folder from %d to %d entries", len(before), len(after))
			}
			if _, ok := after["letter.txt"]; ok {
				t.Error("a refused Save As made letter.txt")
			}
			if body := fx.fileText(t, other.ID); body != "other" {
				t.Errorf("a refused overwrite changed other.docx to %q", body)
			}
		})
	}
}

// TestWOPISaveAsViewTokenMakesACopyButNeverOverwrites: a browser view token may make a new copy, since the person may add
// files to the folder, and the copy's token is read-only as the copy's role is. It may not overwrite a file the person may
// edit: that answers 501, and the file is unchanged (decision 87).
func TestWOPISaveAsViewTokenMakesACopyButNeverOverwrites(t *testing.T) {
	t.Parallel()
	fx, _ := saveAsFixture(t)
	view := fx.openToken(t, "view")
	other := putNode(t, fx.s, fx.key, fx.fld.ID, "/other.docx", "other")
	fx.grant(t, other.ID, fx.ann.ID, "editor")
	if info := decodeInfo(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), view, nil, nil)); info["UserCanNotWriteRelative"] != false {
		t.Errorf("UserCanNotWriteRelative = %v for a browser view token of a contributor, want false", info["UserCanNotWriteRelative"])
	}
	out := saveAsAnswer(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), view, strings.NewReader("copy"),
		saveAsHdr("", ".txt", false)))
	if out["Name"] != "letter.txt" {
		t.Errorf("Name = %q, want letter.txt", out["Name"])
	}
	if answerCanWrite(t, fx, out, rootChildren(t, fx)["letter.txt"].ID) {
		t.Error("the copy's token can write, but ann only contributes to the folder")
	}
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), view, strings.NewReader("over"),
		saveAsHdr("other.docx", "", true)), 501)
	if body := fx.fileText(t, other.ID); body != "other" {
		t.Errorf("a view token overwrote other.docx with %q", body)
	}
}

// TestWOPISaveAsCopyTokenFollowsItsOwnRole: the copy's token is set by the caller's role on the copy's own node. An
// overwrite of a file the caller may edit gives a writable token for that file, although the caller is only a contributor
// on its folder. A new copy, which the caller holds only as a contributor, gets a read-only token.
func TestWOPISaveAsCopyTokenFollowsItsOwnRole(t *testing.T) {
	t.Parallel()
	fx, tok := saveAsFixture(t)
	other := putNode(t, fx.s, fx.key, fx.fld.ID, "/other.docx", "other")
	fx.grant(t, other.ID, fx.ann.ID, "editor")
	over := saveAsAnswer(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("replaced"),
		saveAsHdr("other.docx", "", true)))
	if !answerCanWrite(t, fx, over, other.ID) {
		t.Error("the token for an overwritten file the caller may edit is read-only")
	}
	fresh := saveAsAnswer(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("copy"),
		saveAsHdr("", ".txt", false)))
	if answerCanWrite(t, fx, fresh, rootChildren(t, fx)["letter.txt"].ID) {
		t.Error("the token for a new copy is writable, but the caller is only a contributor")
	}
}

// TestWOPISaveAsNoDownloadAnswers501 is the no-download case: a viewer of a no-download file gets 501 for Save As,
// since UserCanNotWriteRelative is true for it, and nothing is made.
func TestWOPISaveAsNoDownloadAnswers501(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+fx.doc.ID, fx.key, map[string]any{"no_download": true}), 200)
	fx.grant(t, fx.doc.ID, fx.ann.ID, "viewer")
	view := decode[officeJSON](t, fx.open(t, fx.annSess, fx.doc.ID, "view")).Token
	before := rootChildren(t, fx)
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), view, strings.NewReader("x"), saveAsHdr("", ".txt", false)), 501)
	if len(rootChildren(t, fx)) != len(before) {
		t.Error("a no-download copy was made")
	}
}

// TestWOPISaveAsQuotaIsCheckedBeforeTheUpload: an overwrite is charged its whole new size, since the file it replaces is
// kept as a version, so a body the quota will refuse is refused before any of its bytes are read.
func TestWOPISaveAsQuotaIsCheckedBeforeTheUpload(t *testing.T) {
	t.Parallel()
	fx, tok := saveAsFixture(t)
	// letter.docx and target.docx hold 10 of the 12 bytes allowed. An overwrite of target.docx with 6 bytes is 6 more bytes
	// for the account, not the 1 byte by which the file grows, so it is refused.
	target := putNode(t, fx.s, fx.key, fx.fld.ID, "/target.docx", "abcde")
	fx.grant(t, target.ID, fx.ann.ID, "editor")
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID, fx.admin,
		map[string]any{"quota_bytes": 12}), 200)
	body := &hookReader{r: strings.NewReader("sixsix")}
	wantStatus(t, wopiCallSized(t, fx, nodeRoute(fx.doc.ID), tok, body, 6, saveAsHdr("target.docx", "", true)), 413)
	if body.read {
		t.Error("the body was read before the quota refused the overwrite")
	}
	if got := fx.fileText(t, target.ID); got != "abcde" {
		t.Errorf("a refused overwrite changed target.docx to %q", got)
	}
}

// TestWOPISaveAsSourceTrashedDuringTheUploadIsRefused: the source is read again inside the write. A source trashed while
// its copy's body arrived is refused with 404, and no copy is made in its folder.
func TestWOPISaveAsSourceTrashedDuringTheUploadIsRefused(t *testing.T) {
	t.Parallel()
	fx, tok := saveAsFixture(t)
	body := &hookReader{r: strings.NewReader("copy"), once: func() {
		wantStatus2xx(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.doc.ID, fx.key, nil))
	}}
	wantStatus(t, wopiCallSized(t, fx, nodeRoute(fx.doc.ID), tok, body, 4, saveAsHdr("", ".txt", false)), 404)
	if _, ok := rootChildren(t, fx)["letter.txt"]; ok {
		t.Error("a copy of a trashed file was made")
	}
}

// TestWOPISaveAsAllNamesTakenIs500: with every suggested name taken, the host has no free name. The answer is 500 with the
// reason logged, never 409: the specification says a suggested target never gets 409 (decision 87).
func TestWOPISaveAsAllNamesTakenIs500(t *testing.T) {
	t.Parallel()
	fx, tok := saveAsFixture(t)
	// The 1000 names are made through the one-request write path, which allows 300 a minute: the test lifts that limit for
	// its setup only.
	fx.s.limits[oneRequestWriteRate] = newLimiter(100000)
	for n := 1; n <= 1000; n++ {
		putNode(t, fx.s, fx.key, fx.fld.ID, fmt.Sprintf("/letter (%d).docx", n), "x")
	}
	var logs bytes.Buffer
	fx.s.log = slog.New(slog.NewTextHandler(&logs, nil))
	rec := fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("copy"), saveAsHdr("", ".docx", false))
	wantStatus(t, rec, 500)
	if !strings.Contains(logs.String(), "no free name") {
		t.Errorf("the reason was not logged:\n%s", logs.String())
	}
}

// TestWOPISaveAsExactNameTakenWithoutOverwriteSaysTheFreeName: a taken exact name without overwrite is 409 with a free name
// in X-WOPI-ValidRelativeTarget, even when the taken file is locked; the lock is for an overwrite (decision 87).
func TestWOPISaveAsExactNameTakenWithoutOverwriteSaysTheFreeName(t *testing.T) {
	t.Parallel()
	fx, tok := saveAsFixture(t)
	busy := putNode(t, fx.s, fx.key, fx.fld.ID, "/busy.docx", "busy")
	fx.grant(t, busy.ID, fx.ann.ID, "editor")
	btok := decode[officeJSON](t, fx.open(t, fx.annSess, busy.ID, "edit")).Token
	fx.lockNodeFor(t, busy.ID, btok, "editor-busy")
	rec := fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("x"), saveAsHdr("busy.docx", "", false))
	wantStatus(t, rec, 409)
	if valid := utf7Header(t, rec.Header().Get("X-WOPI-ValidRelativeTarget")); valid != "busy (1).docx" {
		t.Errorf("X-WOPI-ValidRelativeTarget = %q, want busy (1).docx", valid)
	}
}

// TestWOPIPutUserInfoRefusedForDelegatedToken: a delegated key's token may not set the person's UserInfo, since the string
// is the person's own and the key is not the person (decision 87). A browser view token may.
func TestWOPIPutUserInfoRefusedForDelegatedToken(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.grant(t, fx.doc.ID, fx.ann.ID, "editor")
	dtok := delegatedWOPIToken(t, fx, "write", "edit")
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), dtok, strings.NewReader("from a key"),
		map[string]string{"X-WOPI-Override": "PUT_USER_INFO"}), 501)
	if info := decodeInfo(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), dtok, nil, nil)); info["UserInfo"] != nil {
		t.Errorf("a delegated token stored UserInfo %v", info["UserInfo"])
	}
	view := fx.openToken(t, "view")
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), view, strings.NewReader("mine"),
		map[string]string{"X-WOPI-Override": "PUT_USER_INFO"}), 200)
}

// TestCopyPrincipalNeverLiftsADelegatedToken: copyPrincipal clears the node scope of a browser-origin token only. A token
// minted by a delegated key keeps its scope, so a later reorder of the write cannot widen it (decision 87).
func TestCopyPrincipalNeverLiftsADelegatedToken(t *testing.T) {
	t.Parallel()
	delegated := &auth.Principal{Kind: auth.KindUser, ID: "usr_test", AccountID: "acc_test", UserID: "usr_test",
		Token: &auth.TokenScope{Node: "nod_source", Write: true, MintedBy: "key_test"}}
	if got := copyPrincipal(delegated).Token.Node; got != "nod_source" {
		t.Errorf("copyPrincipal cleared the node scope of a delegated token: Node = %q, want nod_source", got)
	}
	browser := &auth.Principal{Kind: auth.KindUser, ID: "usr_test", AccountID: "acc_test", UserID: "usr_test",
		Token: &auth.TokenScope{Node: "nod_source", Write: true}}
	if got := copyPrincipal(browser).Token.Node; got != "" {
		t.Errorf("copyPrincipal kept the node scope of a browser token: Node = %q, want none", got)
	}
}

// TestWOPISaveAsOverwriteOfATrashedNameIsFree: trashing a file frees its name (the store gives the name key to the trash), so
// an overwrite of that name finds no file to replace and makes the copy under the name. The trashed file is not touched.
// At first this looked like a 409 case; the store's rule makes it a plain free name, so this pins that behaviour.
func TestWOPISaveAsOverwriteOfATrashedNameIsFree(t *testing.T) {
	t.Parallel()
	fx, tok := saveAsFixture(t)
	busy := putNode(t, fx.s, fx.key, fx.fld.ID, "/busy.docx", "busy")
	fx.grant(t, busy.ID, fx.ann.ID, "editor")
	wantStatus2xx(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+busy.ID, fx.key, nil))
	out := saveAsAnswer(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("x"), saveAsHdr("busy.docx", "", true)))
	if out["Name"] != "busy.docx" {
		t.Errorf("Name = %q, want busy.docx", out["Name"])
	}
	if got := rootChildren(t, fx)["busy.docx"]; got.ID == busy.ID {
		t.Error("the trashed file was overwritten")
	}
}

// TestWOPISaveAsNamesTheCopyFromTheSourceAsItIsWritten: the source is renamed while the copy's body arrives. The copy takes its
// suggested name from the source's name as the write reads it (the nit), so it is named from the new name.
func TestWOPISaveAsNamesTheCopyFromTheSourceAsItIsWritten(t *testing.T) {
	t.Parallel()
	fx, tok := saveAsFixture(t)
	body := &hookReader{r: strings.NewReader("copy"), once: func() {
		wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+fx.doc.ID, fx.key, map[string]any{"name": "renamed.docx"}), 200)
	}}
	out := saveAsAnswer(t, wopiCallSized(t, fx, nodeRoute(fx.doc.ID), tok, body, 4, saveAsHdr("", ".txt", false)))
	if out["Name"] != "renamed.txt" {
		t.Errorf("Name = %q, want renamed.txt, the source's name as it was written", out["Name"])
	}
}
