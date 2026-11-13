// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"testing"

	"github.com/shukiv/selfcloud/internal/mail"
)

// TestInfoNamesTheEditorWhileOfficeIsOn: the page learns from /api/v1/info that the editor is offered (office_edit), and
// no feature is named while office is off.
func TestInfoNamesTheEditorWhileOfficeIsOn(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	rec := call(t, fx.s, "GET", "/api/v1/info", "", nil)
	wantStatus(t, rec, 200)
	got := decode[struct {
		Features []string `json:"features"`
	}](t, rec).Features
	if !containsString(got, "office_edit") {
		t.Errorf("features %v, want office_edit listed while office is on", got)
	}
	off := newAuthServer(t, &mail.Recorder{})
	rec = call(t, off, "GET", "/api/v1/info", "", nil)
	if got := decode[struct {
		Features []string `json:"features"`
	}](t, rec).Features; containsString(got, "office_edit") {
		t.Errorf("features %v, want no office_edit while office is off", got)
	}
}

// TestNodeShowsTheLockAndWhoHoldsIt: a file an editor holds reads editing true in its stat and its listing, with the
// holder's display name (editing_by). A file no editor holds carries neither. The lock is read for the caller's own
// view only: any caller who sees the node sees whether it is being edited.
// TestVisitorSeesNoEditingMark: a visitor of a share link is not told that someone is editing a file (decision
// 73). The mark is for the account's members, who may open the file in the editor; a visitor cannot, and the mark would
// tell anyone who holds the link when a member is at work.
func TestVisitorSeesNoEditingMark(t *testing.T) {
	t.Parallel()
	fx := newAnyoneFixture(t)
	_, secret := fx.makeAnyone(t, fx.b.ID, nil)
	tok := fx.visitAnyone(t, secret, "")
	lockRowFor(t, fx.s, fx.acc.ID, fx.deep.ID, "L-V", fx.ann.ID)
	stat := decode[nodeJSON](t, fx.visitor(t, tok, "GET", "/api/v1/nodes/"+fx.deep.ID))
	if stat.Editing || stat.EditingBy != "" {
		t.Errorf("a visitor's stat of a held file: editing=%v editing_by=%q, want none", stat.Editing, stat.EditingBy)
	}
	kids := decode[listResponse[nodeJSON]](t, fx.visitor(t, tok, "GET", "/api/v1/nodes/"+fx.b.ID+"/children"))
	if len(kids.Items) != 1 || kids.Items[0].ID != fx.deep.ID {
		t.Fatalf("the visitor's listing = %+v, want the held file", kids.Items)
	}
	if kids.Items[0].Editing || kids.Items[0].EditingBy != "" {
		t.Errorf("a visitor's listing of a held file: editing=%v editing_by=%q, want none", kids.Items[0].Editing, kids.Items[0].EditingBy)
	}
	// The account's own callers still see it.
	if member := decode[nodeJSON](t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.deep.ID, fx.key, nil)); !member.Editing {
		t.Errorf("the account key's stat of a held file has no editing mark")
	}
}

func TestNodeShowsTheLockAndWhoHoldsIt(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	rec := call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users", fx.key,
		map[string]any{"email": "cy@example.com", "display_name": "Cy Rivera", "verification": "link"})
	wantStatus(t, rec, 201)
	cy := decode[userResponse](t, rec)

	stat := decode[nodeJSON](t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.doc.ID, fx.key, nil))
	if stat.Editing || stat.EditingBy != "" {
		t.Fatalf("a free file reads editing=%v editing_by=%q", stat.Editing, stat.EditingBy)
	}

	lockRowFor(t, fx.s, fx.acc.ID, fx.doc.ID, "L-CY", cy.ID)
	stat = decode[nodeJSON](t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.doc.ID, fx.key, nil))
	if !stat.Editing || stat.EditingBy != "Cy Rivera" {
		t.Errorf("stat of a held file: editing=%v editing_by=%q; want true, Cy Rivera", stat.Editing, stat.EditingBy)
	}
	listed := decode[listResponse[nodeJSON]](t, call(t, fx.s, "GET", "/api/v1/folders/"+fx.fld.ID+"/children", fx.key, nil))
	var seen bool
	for _, it := range listed.Items {
		if it.ID == fx.doc.ID {
			seen = true
			if !it.Editing || it.EditingBy != "Cy Rivera" {
				t.Errorf("listing of a held file: editing=%v editing_by=%q", it.Editing, it.EditingBy)
			}
		}
	}
	if !seen {
		t.Fatalf("the held file is not in its folder's listing")
	}

	wantStatus(t, call(t, fx.s, "POST", breakPath(fx.doc.ID), fx.key, nil), 204)
	stat = decode[nodeJSON](t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.doc.ID, fx.key, nil))
	if stat.Editing || stat.EditingBy != "" {
		t.Errorf("after the break: editing=%v editing_by=%q, want none", stat.Editing, stat.EditingBy)
	}
}
