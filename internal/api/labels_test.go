// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/store"
)

// asCaller makes one request as a given caller: a key or a token by its bearer credential, or a signed-in user by its
// session. It returns the recorded answer.
type asCaller func(method, path string, body any) *httptest.ResponseRecorder

// byCred calls as a bearer credential.
func byCred(t *testing.T, s *Server, cred string) asCaller {
	return func(method, path string, body any) *httptest.ResponseRecorder {
		return call(t, s, method, path, cred, body)
	}
}

// bySession calls as a signed-in user.
func bySession(t *testing.T, s *Server, sess testSession) asCaller {
	return func(method, path string, body any) *httptest.ResponseRecorder {
		return callSession(t, s, sess, method, path, body)
	}
}

// labelFixture is a team space with a file, an editor, a viewer and an owner, plus a second account. Its fields:
// Note (text), Cost (number), Due (date), Lead (user, restricted) and Status (choice: Open, Done).
type labelFixture struct {
	*sharingFixture
	owner     userResponse
	ownerSess testSession
	other     accountJSON
	otherKey  string
	note      string
	cost      string
	due       string
	lead      string
	status    string
	openID    string
	doneID    string
	keyDo     asCaller // the account key
	annDo     asCaller // the editor on /a/b
	bobDo     asCaller // the viewer on /a/b
	ownerDo   asCaller // the owner
}

func newLabelFixture(t *testing.T) *labelFixture {
	t.Helper()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "editor")
	fx.setPerm(t, fx.b.ID, fx.bob.ID, "viewer")
	owner, ownerSess := ownerUser(t, fx.s, fx.key, fx.acc.ID, "owner@example.com")
	other, otherKey := newAccount(t, fx.s, fx.admin, "Other")
	lf := &labelFixture{sharingFixture: fx, owner: owner, ownerSess: ownerSess, other: other, otherKey: otherKey,
		keyDo: byCred(t, fx.s, fx.key), annDo: bySession(t, fx.s, fx.annSess), bobDo: bySession(t, fx.s, fx.bobSess),
		ownerDo: bySession(t, fx.s, ownerSess)}
	lf.note = labelField(t, fx, fx.key, fx.acc.ID, map[string]any{"name": "Note", "type": "text"})
	lf.cost = labelField(t, fx, fx.key, fx.acc.ID, map[string]any{"name": "Cost", "type": "number"})
	lf.due = labelField(t, fx, fx.key, fx.acc.ID, map[string]any{"name": "Due", "type": "date"})
	lf.lead = labelField(t, fx, fx.key, fx.acc.ID, map[string]any{"name": "Lead", "type": "user", "restricted": true})
	lf.status = labelField(t, fx, fx.key, fx.acc.ID, map[string]any{"name": "Status", "type": "choice",
		"choices": []map[string]any{{"name": "Open"}, {"name": "Done"}}})
	for _, f := range listFields(t, lf.keyDo) {
		if f.ID == lf.status {
			lf.openID, lf.doneID = f.Choices[0].ID, f.Choices[1].ID
		}
	}
	return lf
}

// labelField defines a field with the given body, as cred, and returns its ID.
func labelField(t *testing.T, fx *sharingFixture, cred, accID string, body map[string]any) string {
	t.Helper()
	rec := call(t, fx.s, "POST", "/api/v1/accounts/"+accID+"/label-fields", cred, body)
	wantStatus(t, rec, 201)
	return decode[labelFieldJSON](t, rec).ID
}

// listFields lists the fields the caller sees.
func listFields(t *testing.T, do asCaller) []labelFieldJSON {
	t.Helper()
	rec := do("GET", "/api/v1/label-fields", nil)
	wantStatus(t, rec, 200)
	return decode[listResponse[labelFieldJSON]](t, rec).Items
}

// nodeLabels lists a node's labels as the caller sees them, by field ID.
func nodeLabels(t *testing.T, do asCaller, nodeID string) map[string]nodeLabelJSON {
	t.Helper()
	rec := do("GET", "/api/v1/nodes/"+nodeID+"/labels", nil)
	wantStatus(t, rec, 200)
	out := map[string]nodeLabelJSON{}
	for _, it := range decode[listResponse[nodeLabelJSON]](t, rec).Items {
		out[it.FieldID] = it
	}
	return out
}

// setLabel sets a node's value and wants the status.
func setLabel(t *testing.T, do asCaller, nodeID, fieldID string, value any, want int) {
	t.Helper()
	rec := do("PUT", "/api/v1/nodes/"+nodeID+"/labels/"+fieldID, map[string]any{"value": value})
	wantStatus(t, rec, want)
}

// auditActionsOf lists the audit entries of an account with the given action, newest first.
func auditActionsOf(t *testing.T, s *Server, accID, action string) []*store.AuditEntry {
	t.Helper()
	entries, _, err := s.store.AuditLogFiltered(context.Background(), accID, "", true,
		store.AuditFilter{Action: action}, store.Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestLabelFieldsAreDefinedByTheOwnerOrTheKey(t *testing.T) {
	t.Parallel()
	lf := newLabelFixture(t)
	body := map[string]any{"name": "Extra", "type": "text"}
	path := "/api/v1/accounts/" + lf.acc.ID + "/label-fields"
	annTok := mintFor(t, lf.s, lf.key, lf.acc.ID, map[string]any{"user": lf.ann.ID, "folders": []string{lf.fld.ID}, "permissions": "write"})

	// A member, a delegated token acting for a member, an installation admin and another account's key may not define.
	wantError(t, lf.annDo("POST", path, body), 404, apierr.NotFound)
	wantError(t, call(t, lf.s, "POST", path, annTok, body), 404, apierr.NotFound)
	wantError(t, call(t, lf.s, "POST", path, lf.admin, body), 403, apierr.Forbidden)
	wantError(t, call(t, lf.s, "POST", path, lf.otherKey, body), 404, apierr.NotFound)

	// The owner's session and the key may.
	wantStatus(t, lf.ownerDo("POST", path, body), 201)
	wantStatus(t, lf.keyDo("POST", path, map[string]any{"name": "Extra two", "type": "text"}), 201)

	// Every reader of the account lists the fields, restricted ones included; only the owner and the key see counts.
	if n := len(listFields(t, lf.annDo)); n != 7 {
		t.Errorf("a member lists %d fields, want 7", n)
	}
	if n := len(listFields(t, byCred(t, lf.s, annTok))); n != 7 {
		t.Errorf("a token lists %d fields, want 7", n)
	}
	if strings.Contains(lf.annDo("GET", "/api/v1/label-fields", nil).Body.String(), `"values"`) {
		t.Error("a member sees a value count")
	}
	if f := listFields(t, lf.keyDo)[0]; f.Values == nil {
		t.Error("the key sees no value count")
	}
	// An installation admin reaches no tenant's fields, and another account sees none of these.
	wantError(t, call(t, lf.s, "GET", "/api/v1/label-fields", lf.admin, nil), 403, apierr.Forbidden)
	if n := len(listFields(t, byCred(t, lf.s, lf.otherKey))); n != 0 {
		t.Errorf("another account lists %d of this account's fields", n)
	}
}

func TestLabelFieldValidationAndLimits(t *testing.T) {
	t.Parallel()
	lf := newLabelFixture(t)
	path := "/api/v1/accounts/" + lf.acc.ID + "/label-fields"
	wantError(t, lf.keyDo("POST", path, map[string]any{"name": "X", "type": "bogus"}), 400, apierr.InvalidRequest)
	wantError(t, lf.keyDo("POST", path, map[string]any{"name": "X", "type": "text",
		"choices": []map[string]any{{"name": "a"}}}), 400, apierr.InvalidRequest)
	wantError(t, lf.keyDo("POST", path, map[string]any{"name": "a/b", "type": "text"}), 400, "invalid_name")
	wantError(t, lf.keyDo("POST", path, map[string]any{"name": "  ", "type": "text"}), 400, "invalid_name")
	e := wantError(t, lf.keyDo("POST", path, map[string]any{"name": "NOTE", "type": "number"}), 409, apierr.Conflict)
	if e.Details["reason"] != "name_taken" {
		t.Errorf("a name in use: reason %v, want name_taken", e.Details["reason"])
	}
	dup := []map[string]any{{"name": "A"}, {"name": "a"}}
	e = wantError(t, lf.keyDo("POST", path, map[string]any{"name": "Dups", "type": "choice", "choices": dup}), 409, apierr.Conflict)
	if e.Details["reason"] != "name_taken" {
		t.Errorf("two choices with one name: reason %v, want name_taken", e.Details["reason"])
	}
	many := make([]map[string]any, store.MaxLabelChoices+1)
	for i := range many {
		many[i] = map[string]any{"name": fmt.Sprintf("c%03d", i)}
	}
	e = wantError(t, lf.keyDo("POST", path, map[string]any{"name": "Many", "type": "choice", "choices": many}), 409, apierr.Conflict)
	if e.Details["reason"] != "limit" {
		t.Errorf("101 choices: reason %v, want limit", e.Details["reason"])
	}
	// Five fields exist; 45 more reach the limit of 50, and the next is refused.
	for i := 5; i < store.MaxLabelFields; i++ {
		wantStatus(t, lf.keyDo("POST", path, map[string]any{"name": fmt.Sprintf("F%02d", i), "type": "text"}), 201)
	}
	e = wantError(t, lf.keyDo("POST", path, map[string]any{"name": "Over", "type": "text"}), 409, apierr.Conflict)
	if e.Details["reason"] != "limit" {
		t.Errorf("the 51st field: reason %v, want limit", e.Details["reason"])
	}
}

func TestLabelFieldUpdates(t *testing.T) {
	t.Parallel()
	lf := newLabelFixture(t)
	path := "/api/v1/accounts/" + lf.acc.ID + "/label-fields/"
	// The type is locked once a field holds a value.
	setLabel(t, lf.keyDo, lf.deep.ID, lf.status, lf.openID, 204)
	e := wantError(t, lf.keyDo("PATCH", path+lf.status, map[string]any{"type": "text"}), 409, apierr.Conflict)
	if e.Details["reason"] != "type_locked" {
		t.Errorf("type change on a valued field: reason %v, want type_locked", e.Details["reason"])
	}
	// Archive a choice, and add one. The archived choice keeps its value and takes no new one.
	wantStatus(t, lf.keyDo("PATCH", path+lf.status, map[string]any{"choices": []map[string]any{
		{"id": lf.openID, "name": "Open", "archived": true},
		{"name": "Blocked"},
	}}), 200)
	blocked := ""
	for _, f := range listFields(t, lf.keyDo) {
		if f.ID == lf.status {
			for _, ch := range f.Choices {
				if ch.Name == "Blocked" {
					blocked = ch.ID
				}
			}
		}
	}
	if blocked == "" {
		t.Fatal("the added choice is not listed")
	}
	setLabel(t, lf.keyDo, lf.deep.ID, lf.status, blocked, 204)
	wantError(t, lf.keyDo("PUT", "/api/v1/nodes/"+lf.deep.ID+"/labels/"+lf.status, map[string]any{"value": lf.openID}),
		400, apierr.InvalidRequest)
	if got := nodeLabels(t, lf.keyDo, lf.deep.ID)[lf.status].Value; got == nil {
		t.Error("the archived choice's value disappeared")
	}
	// A field with no value changes type, and a field that stops being a choice field loses its choices.
	tmp := labelField(t, lf.sharingFixture, lf.key, lf.acc.ID, map[string]any{"name": "Temp", "type": "choice",
		"choices": []map[string]any{{"name": "x"}}})
	wantStatus(t, lf.keyDo("PATCH", path+tmp, map[string]any{"type": "number"}), 200)
	for _, f := range listFields(t, lf.keyDo) {
		if f.ID == tmp && (f.Type != "number" || len(f.Choices) != 0) {
			t.Errorf("after the change: type %s with %d choices, want number with none", f.Type, len(f.Choices))
		}
	}
	// A member may not change a field; a rename into a name in use is refused.
	wantError(t, lf.annDo("PATCH", path+tmp, map[string]any{"name": "Renamed"}), 404, apierr.NotFound)
	e = wantError(t, lf.keyDo("PATCH", path+tmp, map[string]any{"name": "Note"}), 409, apierr.Conflict)
	if e.Details["reason"] != "name_taken" {
		t.Errorf("rename into a name in use: reason %v, want name_taken", e.Details["reason"])
	}
	// An unknown choice ID is refused, as is a choice on a field that is not a choice field.
	wantError(t, lf.keyDo("PATCH", path+lf.status, map[string]any{"choices": []map[string]any{
		{"id": "lbc_nothere", "name": "Nope"}}}), 400, apierr.InvalidRequest)
	wantError(t, lf.keyDo("PATCH", path+lf.note, map[string]any{"choices": []map[string]any{{"name": "x"}}}),
		400, apierr.InvalidRequest)
}

func TestLabelValuesByType(t *testing.T) {
	t.Parallel()
	lf := newLabelFixture(t)
	// The editor sets each kind of value on the file.
	setLabel(t, lf.annDo, lf.deep.ID, lf.note, "Ask Dana", 204)
	setLabel(t, lf.annDo, lf.deep.ID, lf.cost, 3.5, 204)
	setLabel(t, lf.annDo, lf.deep.ID, lf.due, "2026-02-03", 204)
	setLabel(t, lf.annDo, lf.deep.ID, lf.status, lf.doneID, 204)
	got := nodeLabels(t, lf.annDo, lf.deep.ID)
	if got[lf.note].Value != "Ask Dana" || got[lf.cost].Value != 3.5 || got[lf.due].Value != "2026-02-03" {
		t.Errorf("values = %+v", got)
	}
	if _, ok := got[lf.lead]; ok {
		t.Error("an editor sees the restricted field")
	}
	// Each refusal is a 400, and the stored value stays.
	refused := []struct {
		field string
		value any
	}{
		{lf.cost, "3.5"},
		{lf.due, "2026-02-30"},
		{lf.due, "2026/02/03"},
		{lf.note, "bad‮order"},
		{lf.note, "line\nbreak"},
		{lf.note, "   "},
		{lf.note, strings.Repeat("x", 501)},
		{lf.status, "lbc_nothere"},
		{lf.note, nil},
	}
	for _, c := range refused {
		setLabel(t, lf.annDo, lf.deep.ID, c.field, c.value, 400)
	}
	if v := nodeLabels(t, lf.keyDo, lf.deep.ID)[lf.note].Value; v != "Ask Dana" {
		t.Errorf("a refused write changed the text to %v", v)
	}
	// Text of 500 characters is kept. A member of this account is a valid user value; another account's member is not.
	setLabel(t, lf.annDo, lf.deep.ID, lf.note, strings.Repeat("é", 500), 204)
	setLabel(t, lf.keyDo, lf.deep.ID, lf.lead, lf.ann.ID, 204)
	otherUser, _ := verifiedUser(t, lf.s, lf.otherKey, lf.other.ID, "stranger@example.com")
	setLabel(t, lf.keyDo, lf.deep.ID, lf.lead, otherUser.ID, 400)
	setLabel(t, lf.keyDo, lf.deep.ID, lf.lead, "usr_nothere", 400)
}

func TestLabelReadAndSetFollowRoles(t *testing.T) {
	t.Parallel()
	lf := newLabelFixture(t)
	// The key sets the restricted field, and so does the owner, who manages the team space.
	setLabel(t, lf.keyDo, lf.deep.ID, lf.lead, lf.ann.ID, 204)
	if _, ok := nodeLabels(t, lf.keyDo, lf.deep.ID)[lf.lead]; !ok {
		t.Error("the key does not see the restricted field")
	}
	if _, ok := nodeLabels(t, lf.ownerDo, lf.deep.ID)[lf.lead]; !ok {
		t.Error("the owner does not see the restricted field on a team space")
	}
	// An editor may not set it; the refusal names the restriction.
	e := wantError(t, lf.annDo("PUT", "/api/v1/nodes/"+lf.deep.ID+"/labels/"+lf.lead, map[string]any{"value": lf.ann.ID}),
		403, apierr.Forbidden)
	if e.Details["reason"] != "restricted_label" {
		t.Errorf("reason %v, want restricted_label", e.Details["reason"])
	}
	// A viewer reads without the restricted field, and may not set or clear anything.
	if _, ok := nodeLabels(t, lf.bobDo, lf.deep.ID)[lf.lead]; ok {
		t.Error("a viewer sees the restricted field")
	}
	wantError(t, lf.bobDo("PUT", "/api/v1/nodes/"+lf.deep.ID+"/labels/"+lf.note, map[string]any{"value": "x"}), 403, apierr.Forbidden)
	wantError(t, lf.bobDo("DELETE", "/api/v1/nodes/"+lf.deep.ID+"/labels/"+lf.note, nil), 403, apierr.Forbidden)
	// A member with no reach gets not_found for a node it may not see.
	wantError(t, lf.bobDo("GET", "/api/v1/nodes/"+lf.sib.ID+"/labels", nil), 404, apierr.NotFound)
	// Another account's key reaches none of this account's nodes or fields.
	wantError(t, call(t, lf.s, "GET", "/api/v1/nodes/"+lf.deep.ID+"/labels", lf.otherKey, nil), 404, apierr.NotFound)
	wantError(t, call(t, lf.s, "PUT", "/api/v1/nodes/"+lf.deep.ID+"/labels/"+lf.note, lf.otherKey,
		map[string]any{"value": "x"}), 404, apierr.NotFound)
	// An installation admin reaches none of it.
	wantError(t, call(t, lf.s, "GET", "/api/v1/nodes/"+lf.deep.ID+"/labels", lf.admin, nil), 403, apierr.Forbidden)
}

func TestLabelClearAndAudit(t *testing.T) {
	t.Parallel()
	lf := newLabelFixture(t)
	setLabel(t, lf.annDo, lf.deep.ID, lf.note, "a private remark", 204)
	wantStatus(t, lf.annDo("DELETE", "/api/v1/nodes/"+lf.deep.ID+"/labels/"+lf.note, nil), 204)
	// Clearing again changes nothing and writes no entry.
	wantStatus(t, lf.annDo("DELETE", "/api/v1/nodes/"+lf.deep.ID+"/labels/"+lf.note, nil), 204)
	if v := nodeLabels(t, lf.keyDo, lf.deep.ID)[lf.note].Value; v != nil {
		t.Errorf("value after clear = %v, want null", v)
	}
	if n := len(auditActionsOf(t, lf.s, lf.acc.ID, "node.label_set")); n != 1 {
		t.Errorf("node.label_set entries = %d, want 1", n)
	}
	if n := len(auditActionsOf(t, lf.s, lf.acc.ID, "node.label_clear")); n != 1 {
		t.Errorf("node.label_clear entries = %d, want 1", n)
	}
	// The entries name the field, never the value.
	for _, e := range auditActionsOf(t, lf.s, lf.acc.ID, "node.label_set") {
		if strings.Contains(fmt.Sprint(e.Details), "private remark") || e.Details["field"] != lf.note {
			t.Errorf("label_set details = %v, want the field and no value", e.Details)
		}
	}
}

func TestLabelFieldDeleteRemovesItsValues(t *testing.T) {
	t.Parallel()
	lf := newLabelFixture(t)
	setLabel(t, lf.keyDo, lf.deep.ID, lf.cost, 2, 204)
	setLabel(t, lf.keyDo, lf.sib.ID, lf.cost, 4, 204)
	wantError(t, call(t, lf.s, "DELETE", "/api/v1/accounts/"+lf.acc.ID+"/label-fields/"+lf.cost, lf.admin, nil), 403, apierr.Forbidden)
	wantError(t, call(t, lf.s, "DELETE", "/api/v1/accounts/"+lf.acc.ID+"/label-fields/"+lf.cost, lf.otherKey, nil), 404, apierr.NotFound)
	wantStatus(t, lf.keyDo("DELETE", "/api/v1/accounts/"+lf.acc.ID+"/label-fields/"+lf.cost, nil), 204)
	if _, ok := nodeLabels(t, lf.keyDo, lf.deep.ID)[lf.cost]; ok {
		t.Error("a deleted field still shows on a node")
	}
	entries := auditActionsOf(t, lf.s, lf.acc.ID, "label_field.delete")
	if len(entries) != 1 || fmt.Sprint(entries[0].Details["values"]) != "2" {
		t.Errorf("label_field.delete entries = %v, want one with values 2", entries)
	}
}

// TestLabelVisitorIsRefused: a visitor of a link that anyone can open gets no label, on any route, the same way it gets
// no other route of an item outside the link.
func TestLabelVisitorIsRefused(t *testing.T) {
	t.Parallel()
	fx := newAnyoneFixture(t)
	note := labelField(t, fx.sharingFixture, fx.key, fx.acc.ID, map[string]any{"name": "Note", "type": "text"})
	setLabel(t, byCred(t, fx.s, fx.key), fx.deep.ID, note, "shared", 204)
	_, secret := fx.makeAnyone(t, fx.b.ID, nil)
	tok := fx.visitAnyone(t, secret, "")
	visitor := byCred(t, fx.s, tok)
	// The route table refuses a visitor on any route that does not admit links, as it does for any item.
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/nodes/" + fx.deep.ID + "/labels"},
		{"PUT", "/api/v1/nodes/" + fx.deep.ID + "/labels/" + note},
		{"DELETE", "/api/v1/nodes/" + fx.deep.ID + "/labels/" + note},
		{"GET", "/api/v1/label-fields"},
	} {
		e := wantError(t, visitor(c.method, c.path, map[string]any{"value": "x"}), 403, apierr.Forbidden)
		if e.Details["reason"] != "link_route" {
			t.Errorf("%s %s: reason %v, want link_route", c.method, c.path, e.Details["reason"])
		}
	}
}

func TestLabelTokens(t *testing.T) {
	t.Parallel()
	lf := newLabelFixture(t)
	writer := mintFor(t, lf.s, lf.key, lf.acc.ID, map[string]any{"user": lf.ann.ID, "folders": []string{lf.fld.ID}, "permissions": "write"})
	reader := mintFor(t, lf.s, lf.key, lf.acc.ID, map[string]any{"user": lf.bob.ID, "folders": []string{lf.fld.ID}})
	writerDo, readerDo := byCred(t, lf.s, writer), byCred(t, lf.s, reader)

	// A write token sets an unrestricted value, and never a restricted one: it has editor at most.
	setLabel(t, writerDo, lf.deep.ID, lf.note, "by token", 204)
	wantError(t, writerDo("PUT", "/api/v1/nodes/"+lf.deep.ID+"/labels/"+lf.lead, map[string]any{"value": lf.ann.ID}),
		403, apierr.Forbidden)
	// A read token reads the unrestricted fields and cannot write.
	got := nodeLabels(t, readerDo, lf.deep.ID)
	if got[lf.note].Value != "by token" {
		t.Errorf("a read token sees %v, want the text", got[lf.note].Value)
	}
	if _, ok := got[lf.lead]; ok {
		t.Error("a read token sees the restricted field")
	}
	wantError(t, readerDo("PUT", "/api/v1/nodes/"+lf.deep.ID+"/labels/"+lf.note, map[string]any{"value": "x"}), 403, apierr.Forbidden)
	// A token defines no field, and a token of another account reads none of this account's.
	wantError(t, writerDo("POST", "/api/v1/accounts/"+lf.acc.ID+"/label-fields", map[string]any{"name": "Nope", "type": "text"}),
		404, apierr.NotFound)
	wantError(t, writerDo("DELETE", "/api/v1/accounts/"+lf.acc.ID+"/label-fields/"+lf.note, nil), 404, apierr.NotFound)
	if n := len(listFields(t, writerDo)); n != 5 {
		t.Errorf("a token lists %d fields, want 5", n)
	}
}

// TestCopyAndMoveKeepLabels: a copy takes the labels of its source, and a move keeps them on the moved node.
func TestCopyAndMoveKeepLabels(t *testing.T) {
	t.Parallel()
	lf := newLabelFixture(t)
	setLabel(t, lf.keyDo, lf.deep.ID, lf.note, "travels", 204)
	setLabel(t, lf.keyDo, lf.deep.ID, lf.status, lf.openID, 204)

	rec := lf.keyDo("POST", "/api/v1/nodes/"+lf.deep.ID+"/copy", map[string]any{"parent": lf.c.ID})
	wantStatus(t, rec, 201)
	copied := decode[nodeJSON](t, rec)
	got := nodeLabels(t, lf.keyDo, copied.ID)
	if got[lf.note].Value != "travels" || got[lf.status].Value == nil {
		t.Errorf("copy labels = %+v, want the text and the status", got)
	}
	// The source keeps its own.
	if nodeLabels(t, lf.keyDo, lf.deep.ID)[lf.note].Value != "travels" {
		t.Error("the source lost its label")
	}
	// A move keeps the labels on the moved node.
	wantStatus(t, lf.keyDo("POST", "/api/v1/nodes/"+lf.deep.ID+"/move", map[string]any{"parent": lf.a.ID}), 200)
	if nodeLabels(t, lf.keyDo, lf.deep.ID)[lf.note].Value != "travels" {
		t.Error("a moved node lost its label")
	}
}

// TestLabelRouteRefusalsByAccount: a field of this account reached through another account's path or key is not_found,
// and a malformed field or node ID is refused before anything is read.
func TestLabelRouteRefusalsByAccount(t *testing.T) {
	t.Parallel()
	lf := newLabelFixture(t)
	wantError(t, lf.keyDo("PATCH", "/api/v1/accounts/"+lf.other.ID+"/label-fields/"+lf.note, map[string]any{"name": "Stolen"}),
		404, apierr.NotFound)
	wantError(t, call(t, lf.s, "PATCH", "/api/v1/accounts/"+lf.acc.ID+"/label-fields/"+lf.note, lf.otherKey,
		map[string]any{"name": "Stolen"}), 404, apierr.NotFound)
	wantError(t, lf.keyDo("PUT", "/api/v1/nodes/"+lf.deep.ID+"/labels/lbf_nothere", map[string]any{"value": "x"}),
		404, apierr.NotFound)
	wantError(t, lf.keyDo("PUT", "/api/v1/nodes/"+lf.deep.ID+"/labels/"+lf.note, map[string]any{}), 400, apierr.InvalidRequest)
	if got := listFields(t, lf.keyDo); len(got) != 5 || got[0].Name != "Note" {
		t.Errorf("fields after the refusals = %+v", got)
	}
}
