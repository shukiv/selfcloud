// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
)

// TestWOPIUnlockAndRelock: LOCK with X-WOPI-OldLock replaces a live lock with the same ID, and answers 409 with the
// current lock otherwise (WOPI: UnlockAndRelock).
func TestWOPIUnlockAndRelock(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tok := fx.openToken(t, "edit")
	relock := func(newID, oldID string) map[string]string {
		return map[string]string{"X-WOPI-Override": "LOCK", "X-WOPI-Lock": newID, "X-WOPI-OldLock": oldID}
	}
	saveRoute := nodeRoute(fx.doc.ID) + "/contents"

	// OLD is live: LOCK NEW with OldLock OLD replaces it, and the file is then held by NEW.
	fx.lockFor(t, tok, "OLD")
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, nil, relock("NEW", "OLD")), 200)
	if got := wopiGetLock(t, fx, tok); got != "NEW" {
		t.Fatalf("after the relock GET_LOCK = %q, want NEW", got)
	}
	wantLockAnswer(t, fx.wopiCall(t, "POST", saveRoute, tok, strings.NewReader("old"), saveHdr("OLD")), "NEW")
	wantStatus(t, fx.wopiCall(t, "POST", saveRoute, tok, strings.NewReader("new"), saveHdr("NEW")), 200)

	// Nothing is locked: a relock from a lock that never was answers 409 with no lock.
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, nil, wopiLockHdr("UNLOCK", "NEW")), 200)
	wantLockAnswer(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, nil, relock("NEW", "GHOST")), "")
	if got := wopiGetLock(t, fx, tok); got != "" {
		t.Fatalf("a refused relock took a lock: GET_LOCK = %q", got)
	}

	// OLD is live and the caller names another lock: the relock is refused and OLD keeps the file.
	fx.lockFor(t, tok, "OLD")
	wantLockAnswer(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, nil, relock("NEW", "GHOST")), "OLD")
	if got := wopiGetLock(t, fx, tok); got != "OLD" {
		t.Fatalf("a refused relock changed the lock: GET_LOCK = %q, want OLD", got)
	}

	// A malformed OldLock is refused as a bad request, as a missing lock ID is.
	bad := map[string]string{"X-WOPI-Override": "LOCK", "X-WOPI-Lock": "NEW", "X-WOPI-OldLock": "bad\tlock"}
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, nil, bad), 400)
}

// wopiGetLock asks GET_LOCK and returns the lock it names, "" for none.
func wopiGetLock(t *testing.T, fx *officeFixture, tok string) string {
	t.Helper()
	rec := fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, nil, wopiLockHdr("GET_LOCK", ""))
	wantStatus(t, rec, 200)
	return rec.Header().Get("X-WOPI-Lock")
}

// TestWOPIEmptyFileSaveChecksTheSession: a save with no lock into an empty file is refused when the editor's session holds
// an older content of the file, so an API write that emptied the file is not overwritten. A new empty file, which the
// editor opened as it is, still takes its first save (document creation).
func TestWOPIEmptyFileSaveChecksTheSession(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tok := fx.openToken(t, "edit") // the session holds "hello"
	wantStatus(t, apiPut(t, fx.s, fx.key, fx.doc.ID, fx.currentETag(t, fx.doc.ID), ""), 200)
	saveRoute := nodeRoute(fx.doc.ID) + "/contents"
	wantLockAnswer(t, fx.wopiCall(t, "POST", saveRoute, tok, strings.NewReader("editor"), saveHdr("")), "")
	if got := fx.fileText(t, fx.doc.ID); got != "" {
		t.Fatalf("an empty save over the API's empty file changed it to %q", got)
	}

	blank := putNode(t, fx.s, fx.key, fx.fld.ID, "/blank.docx", "")
	fx.grant(t, blank.ID, fx.ann.ID, "editor")
	rec := fx.open(t, fx.annSess, blank.ID, "edit")
	wantStatus(t, rec, 200)
	newTok := decode[officeJSON](t, rec).Token
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(blank.ID)+"/contents", newTok, strings.NewReader("first"), saveHdr("")), 200)
	if got := fx.fileText(t, blank.ID); got != "first" {
		t.Fatalf("document creation saved %q, want first", got)
	}
}

// TestMetadataPassesWhileAnEditorHoldsTheFile: a description or a no_download change on a locked file is stored; a rename
// of it is refused with the locked code (decision 32, the lock rule for metadata).
func TestMetadataPassesWhileAnEditorHoldsTheFile(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	lockRowFor(t, fx.s, fx.acc.ID, fx.doc.ID, "L1", "usr_holder")
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+fx.doc.ID, fx.key,
		map[string]any{"description": "the letter", "no_download": true}), 200)
	wantError(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+fx.doc.ID, fx.key, map[string]any{"name": "renamed.docx"}),
		409, apierr.Locked)
	if got := liveLockID(t, fx.s, fx.acc.ID, fx.doc.ID); got != "L1" {
		t.Fatalf("the lock changed to %q", got)
	}
}
