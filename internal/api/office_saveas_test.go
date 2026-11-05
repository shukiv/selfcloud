// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/wopi"
)

// saveAsHdr is the header set of a PutRelativeFile, with the target given as a relative name or a suggestion.
func saveAsHdr(relative, suggested string, overwrite bool) map[string]string {
	h := map[string]string{"X-WOPI-Override": "PUT_RELATIVE"}
	if relative != "" {
		h["X-WOPI-RelativeTarget"] = relative
	}
	if suggested != "" {
		h["X-WOPI-SuggestedTarget"] = suggested
	}
	if overwrite {
		h["X-WOPI-OverwriteRelativeTarget"] = "true"
	}
	return h
}

// saveAsAnswer reads a PutRelativeFile answer: its Name, Url, HostEditUrl and HostViewUrl.
func saveAsAnswer(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	wantStatus(t, rec, 200)
	var out map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("PutRelativeFile body %q: %v", rec.Body.String(), err)
	}
	return out
}

// rootChildren lists the names in the team folder's root.
func rootChildren(t *testing.T, fx *officeFixture) map[string]nodeJSON {
	t.Helper()
	out := map[string]nodeJSON{}
	for _, c := range decode[listResponse[nodeJSON]](t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.fld.RootNodeID+"/children", fx.key, nil)).Items {
		out[c.Name] = c
	}
	return out
}

// saveAsFixture is the office fixture with ann allowed to add files to the team folder, and an editing token for letter.docx.
func saveAsFixture(t *testing.T, mutate ...func(*config.Config)) (*officeFixture, string) {
	t.Helper()
	fx := newOfficeFixture(t, mutate...)
	fx.grant(t, fx.fld.RootNodeID, fx.ann.ID, "contributor")
	return fx, fx.openToken(t, "edit")
}

func TestWOPIPutRelativeSuggestedExtensionTakesTheSourceName(t *testing.T) {
	t.Parallel()
	fx, tok := saveAsFixture(t)
	rec := fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("copied"), saveAsHdr("", ".txt", false))
	out := saveAsAnswer(t, rec)
	if out["Name"] != "letter.txt" {
		t.Errorf("Name = %q, want letter.txt", out["Name"])
	}
	if got := rootChildren(t, fx)["letter.txt"]; got.ID == "" {
		t.Fatal("the copy is not in the source's folder")
	}
	if src := call(t, fx.s, "GET", "/api/v1/nodes/"+fx.doc.ID+"/content", fx.key, nil).Body.String(); src != "hello" {
		t.Errorf("the source reads %q after a Save As, want hello", src)
	}
}

func TestWOPIPutRelativeSuggestedFullNamePicksAFreeNameWhenTaken(t *testing.T) {
	t.Parallel()
	fx, tok := saveAsFixture(t)
	// letter.docx is the source, so a suggestion of its own name is taken and the host picks the next free one.
	first := saveAsAnswer(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("one"),
		saveAsHdr("", "letter.docx", false)))
	if first["Name"] != "letter (1).docx" {
		t.Errorf("first Name = %q, want letter (1).docx", first["Name"])
	}
	second := saveAsAnswer(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("two"),
		saveAsHdr("", "letter.docx", false)))
	if second["Name"] != "letter (2).docx" {
		t.Errorf("second Name = %q, want letter (2).docx", second["Name"])
	}
}

func TestWOPIPutRelativeExactNameFreeTakenAndOverwritten(t *testing.T) {
	t.Parallel()
	fx, tok := saveAsFixture(t)
	free := saveAsAnswer(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("first"),
		saveAsHdr("new.docx", "", false)))
	if free["Name"] != "new.docx" {
		t.Fatalf("a free exact name answered %q, want new.docx", free["Name"])
	}
	// The name is taken now: without overwrite the answer is 409 with a free name the editor can retry with.
	taken := fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("second"), saveAsHdr("new.docx", "", false))
	wantStatus(t, taken, 409)
	if valid := utf7Header(t, taken.Header().Get("X-WOPI-ValidRelativeTarget")); valid != "new (1).docx" {
		t.Errorf("X-WOPI-ValidRelativeTarget = %q, want new (1).docx", valid)
	}
	if got := rootChildren(t, fx)["new.docx"]; got.Size != int64(len("first")) {
		t.Errorf("a refused Save As changed new.docx to %d bytes", got.Size)
	}
	// With overwrite the file is replaced in place: the same node, the new content. Replacing needs editor on the file.
	fx.grant(t, rootChildren(t, fx)["new.docx"].ID, fx.ann.ID, "editor")
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("replaced"),
		saveAsHdr("new.docx", "", true)), 200)
	now := rootChildren(t, fx)["new.docx"]
	if now.ID == "" {
		t.Fatal("new.docx is gone after an overwrite")
	}
	if body := call(t, fx.s, "GET", "/api/v1/nodes/"+now.ID+"/content", fx.key, nil).Body.String(); body != "replaced" {
		t.Errorf("new.docx reads %q after an overwrite, want replaced", body)
	}
}

func TestWOPIPutRelativeLockedTargetAnswers409WithTheLock(t *testing.T) {
	t.Parallel()
	fx, tok := saveAsFixture(t)
	busy := putNode(t, fx.s, fx.key, fx.fld.ID, "/busy.docx", "busy")
	fx.grant(t, busy.ID, fx.ann.ID, "editor")
	btok := decode[officeJSON](t, fx.open(t, fx.annSess, busy.ID, "edit")).Token
	fx.lockNodeFor(t, busy.ID, btok, "editor-busy")
	// Without overwrite the taken name answers with a free name before any lock is looked at; see
	// TestWOPISaveAsExactNameTakenWithoutOverwriteSaysTheFreeName. With overwrite the lock is the answer.
	rec := fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("x"), saveAsHdr("busy.docx", "", true))
	wantStatus(t, rec, 409)
	if got := rec.Header().Get("X-WOPI-Lock"); got != "editor-busy" {
		t.Errorf("overwrite: X-WOPI-Lock = %q, want editor-busy", got)
	}
	if body := call(t, fx.s, "GET", "/api/v1/nodes/"+busy.ID+"/content", fx.key, nil).Body.String(); body != "busy" {
		t.Errorf("a locked target changed to %q", body)
	}
}

func TestWOPIPutRelativeOverwriteNeedsEditorOnTheTarget(t *testing.T) {
	t.Parallel()
	fx, tok := saveAsFixture(t)
	// ann may add files to the folder (contributor), but may not replace one: the specification answers 501 then.
	other := putNode(t, fx.s, fx.key, fx.fld.ID, "/other.docx", "other")
	fx.grant(t, other.ID, fx.ann.ID, "viewer")
	rec := fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("x"), saveAsHdr("other.docx", "", true))
	wantStatus(t, rec, 501)
	if body := call(t, fx.s, "GET", "/api/v1/nodes/"+other.ID+"/content", fx.key, nil).Body.String(); body != "other" {
		t.Errorf("an unauthorized overwrite changed the file to %q", body)
	}
}

func TestWOPIPutRelativeUTF7Names(t *testing.T) {
	t.Parallel()
	fx, tok := saveAsFixture(t)
	// "+AOk-" is the UTF-7 of e with an acute accent (U+00E9).
	rel := saveAsAnswer(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("a"),
		saveAsHdr("+AOk-.docx", "", false)))
	if rel["Name"] != "é.docx" {
		t.Errorf("relative UTF-7 Name = %q, want the e with an acute accent and .docx", rel["Name"])
	}
	sug := saveAsAnswer(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("b"),
		saveAsHdr("", "+AOk-.pdf", false)))
	if sug["Name"] != "é.pdf" {
		t.Errorf("suggested UTF-7 Name = %q, want the e with an acute accent and .pdf", sug["Name"])
	}
	// A relative name that is not valid UTF-7 is refused, and nothing is made.
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("c"),
		saveAsHdr("+/w-.docx", "", false)), 400)
}

func TestWOPIPutRelativeNeedsTheTargetHeaderChoice(t *testing.T) {
	t.Parallel()
	fx, tok := saveAsFixture(t)
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("x"),
		saveAsHdr("a.docx", ".pdf", false)), 400)
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("x"),
		saveAsHdr("", "", false)), 400)
}

// TestWOPIPutRelativeNeedsContributorOnTheDirectory: a caller who may edit the file but may not add files to its folder
// is refused, and CheckFileInfo says Save As is not allowed (UserCanNotWriteRelative true). Granting contributor on the
// folder turns both answers round.
func TestWOPIPutRelativeNeedsContributorOnTheDirectory(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tok := fx.openToken(t, "edit")
	if info := decodeInfo(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), tok, nil, nil)); info["UserCanNotWriteRelative"] != true {
		t.Errorf("UserCanNotWriteRelative = %v for a caller with no right to add files, want true", info["UserCanNotWriteRelative"])
	}
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("x"),
		saveAsHdr("", ".txt", false)), 501)
	if _, ok := rootChildren(t, fx)["letter.txt"]; ok {
		t.Error("a refused Save As made a file")
	}
	fx.grant(t, fx.fld.RootNodeID, fx.ann.ID, "contributor")
	if info := decodeInfo(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), tok, nil, nil)); info["UserCanNotWriteRelative"] != false {
		t.Errorf("UserCanNotWriteRelative = %v once the caller may add files, want false", info["UserCanNotWriteRelative"])
	}
	saveAsAnswer(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("x"), saveAsHdr("", ".txt", false)))
}

// TestWOPIPutRelativeNoDownloadSourceIsRefused: a copy is a download, so a no-download source is not copied to a viewer or
// a commenter. Such a caller holds no contributor right on the folder, so the directory check refuses it too; CheckFileInfo
// says Save As is not allowed. An editor is not bound by no-download (decision 20), and a copy by an editor is allowed.
func TestWOPIPutRelativeNoDownloadSourceIsRefused(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+fx.doc.ID, fx.key, map[string]any{"no_download": true}), 200)
	fx.grant(t, fx.doc.ID, fx.ann.ID, "viewer")
	view := decode[officeJSON](t, fx.open(t, fx.annSess, fx.doc.ID, "view")).Token
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), view, strings.NewReader("x"), saveAsHdr("", ".txt", false)), 501)
	if info := decodeInfo(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), view, nil, nil)); info["UserCanNotWriteRelative"] != true {
		t.Errorf("UserCanNotWriteRelative = %v for a viewer of a no-download file, want true", info["UserCanNotWriteRelative"])
	}
	fx.grant(t, fx.fld.RootNodeID, fx.ann.ID, "contributor")
	fx.grant(t, fx.doc.ID, fx.ann.ID, "editor")
	edit := fx.openToken(t, "edit")
	saveAsAnswer(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), edit, strings.NewReader("x"), saveAsHdr("", ".txt", false)))
}

// TestWOPIPutRelativeRefusesBeforeAnyByteLands covers the limits: the size the caller declares, the body over the
// office limit, and the account quota. Each refusal leaves the folder as it was.
func TestWOPIPutRelativeRefusesBeforeAnyByteLands(t *testing.T) {
	t.Parallel()
	// state is what a refused copy must leave as it was: the folder's entries, and the account's files in the blob store.
	state := func(fx *officeFixture) [2]int { return [2]int{len(rootChildren(t, fx)), saveAsBlobFiles(t, fx)} }
	t.Run("size", func(t *testing.T) {
		t.Parallel()
		// The file is opened under the default limit; the limit is lowered after, since the open itself is limited too.
		fx, tok := saveAsFixture(t)
		fx.s.cfg.Office.MaxFileBytes = 4
		n := state(fx)
		rec := fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("too long for four bytes"),
			saveAsHdr("", ".txt", false))
		wantStatus(t, rec, 413)
		if state(fx) != n {
			t.Errorf("an over-limit copy changed the folder or the blob store: %v, was %v", state(fx), n)
		}
	})
	t.Run("declared size", func(t *testing.T) {
		t.Parallel()
		fx, tok := saveAsFixture(t)
		fx.s.cfg.Office.MaxFileBytes = 4
		n := state(fx)
		hdr := saveAsHdr("", ".txt", false)
		hdr["X-WOPI-Size"] = "5000"
		wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("x"), hdr), 413)
		if state(fx) != n {
			t.Errorf("a copy refused by its declared size changed the folder or the blob store: %v, was %v", state(fx), n)
		}
	})
	t.Run("quota", func(t *testing.T) {
		t.Parallel()
		fx, tok := saveAsFixture(t)
		// The account holds the 5 bytes of letter.docx; a 6-byte copy would make 11 of the 10 allowed.
		wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID, fx.admin,
			map[string]any{"quota_bytes": 10}), 200)
		n := state(fx)
		wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("six by"),
			saveAsHdr("", ".txt", false)), 413)
		if state(fx) != n {
			t.Errorf("a copy over the quota changed the folder or the blob store: %v, was %v", state(fx), n)
		}
	})
}

// TestWOPIPutRelativeNewFileIsItsOwnersAndWorksWithItsToken: the copy's Url opens CheckFileInfo and GetFile for the same
// person; the token does not open the source, and the copy's host pages carry the copy's ID.
func TestWOPIPutRelativeNewFileIsItsOwnersAndWorksWithItsToken(t *testing.T) {
	t.Parallel()
	fx, tok := saveAsFixture(t)
	out := saveAsAnswer(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("copy body"),
		saveAsHdr("", ".txt", false)))
	newID := rootChildren(t, fx)["letter.txt"].ID
	newTok, ok := strings.CutPrefix(out["Url"], wopiFileURLOf(t, fx, newID)+"?access_token=")
	if !ok || newTok == "" {
		t.Fatalf("Url %q is not the copy's WOPI address with a token", out["Url"])
	}
	info := decodeInfo(t, fx.wopiCall(t, "GET", nodeRoute(newID), newTok, nil, nil))
	if info["BaseFileName"] != "letter.txt" || info["UserId"] != fx.ann.ID {
		t.Errorf("CheckFileInfo of the copy = %v, want letter.txt for ann", info)
	}
	if got := fx.wopiCall(t, "GET", nodeRoute(newID)+"/contents", newTok, nil, nil); got.Body.String() != "copy body" {
		t.Errorf("GetFile of the copy = %q, want the body", got.Body.String())
	}
	// The copy's token names the copy only.
	wantStatus(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), newTok, nil, nil), 404)
	if !strings.Contains(out["HostEditUrl"], "/edit/"+newID+"?") || !strings.Contains(out["HostViewUrl"], "mode=view") {
		t.Errorf("host pages = %q and %q, want the copy's editor page in each mode", out["HostEditUrl"], out["HostViewUrl"])
	}
}

// wopiFileURLOf is the WOPI address of a node, as the server builds it.
func wopiFileURLOf(t *testing.T, fx *officeFixture, nodeID string) string {
	t.Helper()
	return fx.s.wopiFileURL(nodeID)
}

// TestWOPIPutRelativeEmitsFileCreated: a copy is a new file, so it reports file.created, as an upload of a new file does.
func TestWOPIPutRelativeEmitsFileCreated(t *testing.T) {
	t.Parallel()
	fx, tok := saveAsFixture(t)
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/webhooks", fx.key,
		map[string]any{"url": "https://hooks.example.com/in", "events": []string{"file.created", "file.updated"}}), 201)
	before := outboxCount(t, fx, "file.created")
	saveAsAnswer(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("copy"), saveAsHdr("", ".txt", false)))
	if got := outboxCount(t, fx, "file.created") - before; got != 1 {
		t.Errorf("the copy reported %d file.created events, want 1", got)
	}
	// An overwrite is a replacement: it reports file.updated, not file.created. Replacing needs editor on the file.
	fx.grant(t, rootChildren(t, fx)["letter.txt"].ID, fx.ann.ID, "editor")
	beforeUpdated := outboxCount(t, fx, "file.updated")
	createdAgain := outboxCount(t, fx, "file.created")
	saveAsAnswer(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("again"), saveAsHdr("letter.txt", "", true)))
	if got := outboxCount(t, fx, "file.created") - createdAgain; got != 0 {
		t.Errorf("an overwrite reported %d file.created events, want 0", got)
	}
	if got := outboxCount(t, fx, "file.updated") - beforeUpdated; got != 1 {
		t.Errorf("an overwrite reported %d file.updated events, want 1", got)
	}
}

// outboxCount counts the outbox events of one type in the fixture's account.
func outboxCount(t *testing.T, fx *officeFixture, typ string) int {
	t.Helper()
	n := 0
	for _, e := range outboxOf(t, fx.s, fx.acc.ID) {
		if e.Type == typ {
			n++
		}
	}
	return n
}

// TestWOPIPutRelativeCopyIsAuditedAsACreate: the copy's audit entry is node.create, with the source named.
func TestWOPIPutRelativeCopyIsAuditedAsACreate(t *testing.T) {
	t.Parallel()
	fx, tok := saveAsFixture(t)
	saveAsAnswer(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("copy"), saveAsHdr("", ".txt", false)))
	found := false
	for _, a := range auditOf(t, fx) {
		if a.Action == "node.create" && a.Target == rootChildren(t, fx)["letter.txt"].ID {
			found = true
		}
	}
	if !found {
		t.Error("the copy has no node.create audit entry")
	}
}

// utf7Header reads a UTF-7 header value the host sent, failing the test when it is not valid UTF-7.
func utf7Header(t *testing.T, v string) string {
	t.Helper()
	out, err := wopi.DecodeUTF7(v)
	if err != nil {
		t.Fatalf("header %q is not valid UTF-7: %v", v, err)
	}
	return out
}
