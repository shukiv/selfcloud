// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// activityFixture is the label fixture (a team space, ann an editor and bob a viewer on /a/b, an owner, the Lead field
// that is restricted, and a second account) with carol, who holds no role anywhere, and display names for the people.
type activityFixture struct {
	*labelFixture
	carol     userResponse
	carolSess testSession
	carolDo   asCaller
}

func newActivityFixture(t *testing.T) *activityFixture {
	t.Helper()
	lf := newLabelFixture(t)
	lf.s.limits[commentWriteRate] = newLimiter(1000)
	carol, carolSess := verifiedUser(t, lf.s, lf.key, lf.acc.ID, "carol@example.com")
	fx := &activityFixture{labelFixture: lf, carol: carol, carolSess: carolSess, carolDo: bySession(t, lf.s, carolSess)}
	for usr, name := range map[string]string{lf.ann.ID: "Ann Cohen", lf.bob.ID: "Bob Levi", carol.ID: "Carol Katz", lf.owner.ID: "Olga Owner"} {
		wantStatus(t, lf.keyDo("PATCH", "/api/v1/accounts/"+lf.acc.ID+"/users/"+usr, map[string]any{"display_name": name}), 200)
	}
	return fx
}

// activityPage is one answer of the route, with the raw body for checks on what it must not contain.
type activityPage struct {
	Items []activityJSON
	Next  string
	Raw   string
}

// get reads the activity of nodeID as do, with query appended (for example "?kind=access"), and wants 200.
func (fx *activityFixture) get(t *testing.T, do asCaller, nodeID, query string) activityPage {
	t.Helper()
	rec := do("GET", "/api/v1/nodes/"+nodeID+"/activity"+query, nil)
	wantStatus(t, rec, 200)
	l := decode[listResponse[activityJSON]](t, rec)
	return activityPage{Items: l.Items, Next: l.NextCursor, Raw: rec.Body.String()}
}

// actions lists the action of each entry, newest first.
func (p activityPage) actions() []string {
	out := make([]string, len(p.Items))
	for i, it := range p.Items {
		out[i] = it.Action
	}
	return out
}

// overwrite replaces the contents of a file as the account key, with the ETag it reads first.
func (fx *activityFixture) overwrite(t *testing.T, nodeID, body string) {
	t.Helper()
	etag := decode[nodeJSON](t, fx.keyDo("GET", "/api/v1/nodes/"+nodeID, nil)).ETag
	rec := rawCall(t, fx.s, "PUT", "/api/v1/nodes/"+nodeID+"/content", fx.key, body, map[string]string{"If-Match": etag})
	wantStatus(t, rec, 200)
}

// rename renames a node as do.
func (fx *activityFixture) rename(t *testing.T, do asCaller, nodeID, name string) {
	t.Helper()
	wantStatus(t, do("PATCH", "/api/v1/nodes/"+nodeID, map[string]any{"name": name}), 200)
}

// TestActivityRoleTiersAndKinds: a viewer reads the content and comment kinds and none of the access and label kinds; a
// manager of the node and the account key read all four. Asking a viewer for a manager kind gives an empty page, not an
// error, so the answer does not say whether such events exist.
func TestActivityRoleTiersAndKinds(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	f := fx.deep.ID
	fx.rename(t, fx.annDo, f, "renamed.txt")
	fx.overwrite(t, f, "new text")
	fx.setPerm(t, f, fx.carol.ID, "viewer") // a permission.set entry
	wantStatus(t, fx.annDo("POST", "/api/v1/nodes/"+f+"/comments", map[string]any{"body": "hello"}), 201)
	setLabel(t, fx.ownerDo, f, fx.note, "x", 204)

	bob := fx.get(t, fx.bobDo, f, "")
	for _, a := range []string{"node.create", "node.rename", "node.write", "comment.create"} {
		if !slices.Contains(bob.actions(), a) {
			t.Errorf("a viewer does not see %s: %v", a, bob.actions())
		}
	}
	for _, a := range []string{"permission.set", "node.label_set"} {
		if slices.Contains(bob.actions(), a) {
			t.Errorf("a viewer sees %s: %v", a, bob.actions())
		}
	}
	for _, kind := range []string{"access", "label"} {
		if p := fx.get(t, fx.bobDo, f, "?kind="+kind); len(p.Items) != 0 || p.Next != "" {
			t.Errorf("a viewer asking for kind=%s gets %v", kind, p.actions())
		}
	}
	for name, do := range map[string]asCaller{"the account key": fx.keyDo, "a manager": fx.ownerDo} {
		all := fx.get(t, do, f, "")
		for _, a := range []string{"node.create", "permission.set", "node.label_set", "comment.create"} {
			if !slices.Contains(all.actions(), a) {
				t.Errorf("%s does not see %s: %v", name, a, all.actions())
			}
		}
		if p := fx.get(t, do, f, "?kind=access"); !slices.Equal(p.actions(), []string{"permission.set"}) {
			t.Errorf("%s with kind=access sees %v, want the one permission.set", name, p.actions())
		}
		if p := fx.get(t, do, f, "?kind=label"); !slices.Equal(p.actions(), []string{"node.label_set"}) {
			t.Errorf("%s with kind=label sees %v", name, p.actions())
		}
	}
	// An editor is not a manager.
	if ann := fx.get(t, fx.annDo, f, ""); slices.Contains(ann.actions(), "permission.set") || slices.Contains(ann.actions(), "node.label_set") {
		t.Errorf("an editor sees manager kinds: %v", ann.actions())
	}
}

// TestActivityShowsNoNameOrIDOfWhatTheCallerCannotReach: carol holds a role on one file and on none of its parents. The
// file was created in one directory and moved to another, and renamed twice. Her answer names neither directory, neither
// directory's ID, no earlier name of the file, and no other person's ID. The same answer is given to a viewer who reaches
// the parents.
func TestActivityShowsNoNameOrIDOfWhatTheCallerCannotReach(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	f := fx.sib.ID // /a/c/x.txt
	fx.rename(t, fx.keyDo, f, "oldsecret-name.txt")
	wantStatus(t, fx.keyDo("POST", "/api/v1/nodes/"+f+"/move", map[string]any{"parent": fx.b.ID}), 200)
	fx.rename(t, fx.keyDo, f, "final.txt")
	fx.setPerm(t, f, fx.carol.ID, "viewer")
	// carol reaches the file and no directory above it.
	wantError(t, fx.carolDo("GET", "/api/v1/nodes/"+fx.b.ID, nil), 404, apierr.NotFound)

	for name, do := range map[string]asCaller{"carol": fx.carolDo, "the key": fx.keyDo} {
		p := fx.get(t, do, f, "")
		for _, a := range []string{"node.create", "node.move", "node.rename"} {
			if !slices.Contains(p.actions(), a) {
				t.Fatalf("%s: %s is missing from %v", name, a, p.actions())
			}
		}
		for _, bad := range []string{fx.a.ID, fx.b.ID, fx.c.ID, fx.s.cfg.Store.Path, "oldsecret", "x.txt", `"parent"`, `"from"`, `"to"`,
			fx.key, fx.acc.ID, fx.ann.ID, fx.bob.ID, fx.carol.ID, fx.owner.ID, "usr_", "nod_", "fld_"} {
			if bad != "" && strings.Contains(p.Raw, bad) {
				t.Errorf("%s: the answer contains %q: %s", name, bad, p.Raw)
			}
		}
	}
}

// TestActivityProjectionKeepsOnlyListedKeys: whatever the entry holds, only the keys listed for its action pass, and a
// string passes only when it is one of the listed words. A name, an ID or a free text cannot get through.
func TestActivityProjectionKeepsOnlyListedKeys(t *testing.T) {
	hostile := map[string]any{
		"kind": "Quarterly Plan", "name": "Quarterly Plan", "from": "old", "to": "new", "parent": "nod_p", "replaced_by": "nod_r",
		"source": "nod_s", "job": "job_1", "subject_id": "usr_1", "role": "owner", "version": "ver_1", "comment": "cmt_1",
		"token_key": "key_1", "via": "delegated_token", "size": float64(7), "previous_size": float64(3), "nodes": float64(2),
		"bytes": float64(9), "reply": true, "mentions": float64(4), "to_root": true, "renamed": true, "by_request": true,
		"expires_at": "2030-01-02T03:04:05Z", "type": "Secret Words", "field": "lbf_x", "impersonated_by": "usr_admin",
	}
	// Of a hostile entry only a bool that the web sentence reads gets through; the strings are not in their lists.
	allowed := map[string][]string{"comment.create": {"reply"}}
	for _, k := range activityKinds {
		for _, a := range k.actions {
			var got []string
			for key := range activityDetails(a, hostile) {
				got = append(got, key)
			}
			slices.Sort(got)
			if want := allowed[a]; !slices.Equal(got, want) && !(len(got) == 0 && len(want) == 0) {
				t.Errorf("%s keeps %v, want %v", a, got, want)
			}
		}
	}
	// The words the table and the web sentences need pass, and nothing else of an entry does.
	good := map[string]any{"kind": "dir", "via": "share_link", "role": "editor", "size": float64(7), "previous_size": float64(1),
		"nodes": float64(2), "bytes": float64(3), "by_request": true, "expires_at": "2030-01-02T03:04:05Z", "to_root": true,
		"renamed": true, "type": "text"}
	for action, want := range map[string][]string{
		"node.create": {"kind", "via"}, "permission.set": {"role"}, "permission.expired": {"role"}, "node.write": nil,
		"node.copy": nil, "node.trash": nil, "node.restore": nil, "node.version.restore": nil, "node.label_set": nil,
	} {
		var got []string
		for key := range activityDetails(action, good) {
			got = append(got, key)
		}
		slices.Sort(got)
		if !slices.Equal(got, want) && !(len(got) == 0 && len(want) == 0) {
			t.Errorf("%s keeps %v, want %v", action, got, want)
		}
	}
}

// TestActivityRefusals: a user with no role, a node that does not exist, a node in the trash, a node of another account,
// a link visitor and an installation admin are all refused, and the first four give the same answer.
func TestActivityRefusals(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	f := fx.deep.ID
	ghost := "/api/v1/nodes/nod_" + strings.Repeat("a", 26) + "/activity"
	var messages []string
	for name, rec := range map[string]func() *httptest.ResponseRecorder{
		"carol has no role": func() *httptest.ResponseRecorder { return fx.carolDo("GET", "/api/v1/nodes/"+f+"/activity", nil) },
		"missing":           func() *httptest.ResponseRecorder { return fx.annDo("GET", ghost, nil) },
		"another account's node": func() *httptest.ResponseRecorder {
			return call(t, fx.s, "GET", "/api/v1/nodes/"+f+"/activity", fx.otherKey, nil)
		},
		"another account's user asks about a node of this one": func() *httptest.ResponseRecorder {
			return call(t, fx.s, "GET", "/api/v1/nodes/"+fx.sib.ID+"/activity", fx.otherKey, nil)
		},
	} {
		r := rec()
		e := wantError(t, r, 404, apierr.NotFound)
		messages = append(messages, name+": "+e.Message)
	}
	// The wording is the same for each of them, so the answer does not say which case it was.
	for _, m := range messages {
		if _, msg, _ := strings.Cut(m, ": "); msg != "no such node" {
			t.Errorf("answer %q, want the wording of a missing node", m)
		}
	}

	// The trash: a trashed node and a node below a trashed directory give not_found, and the history is back after a restore.
	wantStatus(t, fx.keyDo("DELETE", "/api/v1/nodes/"+f, nil), 204)
	wantError(t, fx.bobDo("GET", "/api/v1/nodes/"+f+"/activity", nil), 404, apierr.NotFound)
	wantError(t, fx.keyDo("GET", "/api/v1/nodes/"+f+"/activity", nil), 404, apierr.NotFound)
	wantStatus(t, fx.keyDo("POST", "/api/v1/nodes/"+f+"/restore", nil), 200)
	if p := fx.get(t, fx.bobDo, f, ""); !slices.Contains(p.actions(), "node.trash") || !slices.Contains(p.actions(), "node.restore") {
		t.Errorf("after the restore the history is %v, want trash and restore in it", p.actions())
	}
	wantStatus(t, fx.keyDo("DELETE", "/api/v1/nodes/"+fx.b.ID, nil), 204)
	wantError(t, fx.bobDo("GET", "/api/v1/nodes/"+f+"/activity", nil), 404, apierr.NotFound)

	// An installation admin reads no file.
	wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+f+"/activity", fx.admin, nil), 403, apierr.Forbidden)
}

// TestActivityRefusesALinkVisitor: a visitor of a link that anyone can open is refused by the route table.
func TestActivityRefusesALinkVisitor(t *testing.T) {
	t.Parallel()
	fx := newAnyoneFixture(t)
	root := fx.rootOf(t, fx.fld.ID)
	_, secret := fx.makeAnyone(t, root, nil)
	visitor := fx.visitAnyone(t, secret, "")
	rec := fx.visitor(t, visitor, "GET", "/api/v1/nodes/"+root+"/activity")
	if e := wantError(t, rec, http.StatusForbidden, "forbidden"); e.Details["reason"] != "link_route" {
		t.Fatalf("reason %v, want link_route", e.Details["reason"])
	}
}

// TestActivityTokens: a token reads the activity of what it reaches and of nothing else. One confined to /a/c reads a
// file in /a/c and gets not_found for a file in /a/b. A token never holds more than editor, so it reads no manager kind.
func TestActivityTokens(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	fx.setPerm(t, fx.deep.ID, fx.carol.ID, "viewer")
	tok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}, "node": fx.c.ID})
	asTok := byCred(t, fx.s, tok)
	if p := fx.get(t, asTok, fx.sib.ID, ""); !slices.Contains(p.actions(), "node.create") {
		t.Errorf("a token reads %v in its own directory, want node.create", p.actions())
	}
	wantError(t, asTok("GET", "/api/v1/nodes/"+fx.deep.ID+"/activity", nil), 404, apierr.NotFound)
	wantError(t, asTok("GET", "/api/v1/nodes/"+fx.a.ID+"/activity", nil), 404, apierr.NotFound)
	// The token's user is a manager of nothing here, and the key's token is capped at editor in any case.
	wide := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}, "permissions": "write"})
	asWide := byCred(t, fx.s, wide)
	fx.setPerm(t, fx.sib.ID, fx.carol.ID, "viewer")
	if p := fx.get(t, asWide, fx.sib.ID, ""); slices.Contains(p.actions(), "permission.set") {
		t.Errorf("a token sees a manager kind: %v", p.actions())
	}
	if p := fx.get(t, asWide, fx.sib.ID, "?kind=access"); len(p.Items) != 0 {
		t.Errorf("a token with kind=access sees %v", p.actions())
	}
	// A user's token acts as the user: the user's own role, capped.
	usrTok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}, "user": fx.bob.ID})
	if p := fx.get(t, byCred(t, fx.s, usrTok), fx.deep.ID, ""); !slices.Contains(p.actions(), "node.create") {
		t.Errorf("a user's token reads %v", p.actions())
	}
	wantError(t, byCred(t, fx.s, usrTok)("GET", "/api/v1/nodes/"+fx.sib.ID+"/activity", nil), 404, apierr.NotFound)
}

// TestActivityPrivateEntries: an entry about a personal folder is private to its owner (audit.go). The activity of a file
// that is in that personal folder now is read by whoever the owner shared it with, and by the account key. A file the owner
// moved out to a team space keeps its earlier entries private to the owner: the people of the team space read what
// happened since.
func TestActivityPrivateEntries(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	pf := fx.personalFolder(t, fx.annSess)
	rec := rawSession(t, fx.s, fx.annSess, "POST", "/api/v1/folders/"+pf+"/files?"+q("/mine.txt"), "hello", nil)
	wantStatus(t, rec, 201)
	f := decode[nodeJSON](t, rec).ID
	wantStatus(t, fx.annDo("PUT", "/api/v1/nodes/"+f+"/permissions/"+fx.carol.ID, map[string]any{"role": "viewer"}), 200)

	// In the personal folder: the owner (its manager), the person it was shared with and the key read the creation, and the
	// manager and the key read the share as well. A user with no role reads nothing.
	for name, c := range map[string]struct {
		do   asCaller
		want []string
	}{
		"the owner":                     {fx.annDo, []string{"permission.set", "node.create"}},
		"the key":                       {fx.keyDo, []string{"permission.set", "node.create"}},
		"the person it was shared with": {fx.carolDo, []string{"node.create"}},
	} {
		if p := fx.get(t, c.do, f, ""); !slices.Equal(p.actions(), c.want) {
			t.Errorf("%s reads %v in the personal folder, want %v", name, p.actions(), c.want)
		}
	}
	wantError(t, fx.bobDo("GET", "/api/v1/nodes/"+f+"/activity", nil), 404, apierr.NotFound)

	// Moved to the team space (ann is an editor on /a/b), then renamed there.
	wantStatus(t, fx.annDo("POST", "/api/v1/nodes/"+f+"/move", map[string]any{"folder": fx.fld.ID, "parent": fx.b.ID}), 200)
	fx.rename(t, fx.annDo, f, "team.txt")
	if p := fx.get(t, fx.bobDo, f, ""); !slices.Equal(p.actions(), []string{"node.rename"}) {
		t.Errorf("a viewer of the team space reads %v, want only the rename made in the team space", p.actions())
	}
	if p := fx.get(t, fx.annDo, f, ""); !slices.Equal(p.actions(), []string{"node.rename", "node.move", "node.create"}) {
		t.Errorf("the owner reads %v, want everything", p.actions())
	}
	if p := fx.get(t, fx.keyDo, f, ""); !slices.Equal(p.actions(), []string{"node.rename", "node.move", "permission.set", "node.create"}) {
		t.Errorf("the key reads %v, want everything", p.actions())
	}
}

// TestActivityRestrictedAndDeletedLabels: the restricted field's events and a deleted field's events are read by a
// manager and the key, with the field named while it exists and marked gone when it does not. The value is in no entry.
func TestActivityRestrictedAndDeletedLabels(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	f := fx.deep.ID
	setLabel(t, fx.ownerDo, f, fx.lead, fx.ann.ID, 204)
	setLabel(t, fx.ownerDo, f, fx.cost, 12345, 204)
	for name, do := range map[string]asCaller{"the key": fx.keyDo, "the owner": fx.ownerDo} {
		p := fx.get(t, do, f, "?kind=label")
		if len(p.Items) != 2 {
			t.Fatalf("%s reads %d label entries, want 2: %s", name, len(p.Items), p.Raw)
		}
		names := []any{p.Items[0].Details["field_name"], p.Items[1].Details["field_name"]}
		if !slices.Contains(names, any("Lead")) || !slices.Contains(names, any("Cost")) {
			t.Errorf("%s: fields named %v", name, names)
		}
		if strings.Contains(p.Raw, fx.ann.ID) || strings.Contains(p.Raw, "12345") || strings.Contains(p.Raw, fx.lead) {
			t.Errorf("%s: a value or an ID is in the answer: %s", name, p.Raw)
		}
	}
	for name, do := range map[string]asCaller{"an editor": fx.annDo, "a viewer": fx.bobDo} {
		if p := fx.get(t, do, f, ""); strings.Contains(strings.Join(p.actions(), " "), "label") || strings.Contains(p.Raw, "Lead") {
			t.Errorf("%s reads label entries: %s", name, p.Raw)
		}
	}
	wantStatus(t, fx.keyDo("DELETE", "/api/v1/accounts/"+fx.acc.ID+"/label-fields/"+fx.lead, nil), 204)
	p := fx.get(t, fx.keyDo, f, "?kind=label")
	gone := 0
	for _, it := range p.Items {
		if it.Details["field_gone"] == true && it.Details["field_name"] == nil {
			gone++
		}
	}
	if gone != 1 {
		t.Errorf("%d entries marked field_gone, want 1: %s", gone, p.Raw)
	}
}

// TestActivityActorsAndSubjects: a person is named while the person reaches the node. A person who lost the role is "gone",
// with no name, in the entries made before. The account key, the system and the caller are told apart without an ID.
func TestActivityActorsAndSubjects(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	f := fx.deep.ID
	fx.rename(t, fx.annDo, f, "by-ann.txt")
	fx.setPerm(t, f, fx.carol.ID, "viewer")
	grp := fx.group(t, "Finance team")
	wantStatus(t, fx.keyDo("PUT", "/api/v1/nodes/"+f+"/permissions/"+grp, map[string]any{"role": "editor"}), 200)

	var rename, toCarol, toGroup activityJSON
	load := func() {
		for _, it := range fx.get(t, fx.keyDo, f, "").Items {
			switch {
			case it.Action == "node.rename":
				rename = it
			case it.Action == "permission.set" && it.Details["subject_kind"] == "user":
				toCarol = it
			case it.Action == "permission.set" && it.Details["subject_kind"] == "group":
				toGroup = it
			}
		}
	}
	load()
	if rename.Actor.Kind != "user" || rename.Actor.Name != "Ann Cohen" || rename.Actor.Gone {
		t.Errorf("rename actor = %+v", rename.Actor)
	}
	if toCarol.Actor.Kind != "account_key" || toCarol.Actor.Name != "" {
		t.Errorf("permission actor = %+v, want the account key and no name", toCarol.Actor)
	}
	if toCarol.Details["subject_name"] != "Carol Katz" || toGroup.Details["subject_name"] != "Finance team" {
		t.Errorf("subjects = %v and %v", toCarol.Details, toGroup.Details)
	}
	for _, it := range fx.get(t, fx.annDo, f, "").Items {
		if it.Action == "node.rename" && !it.Actor.Mine {
			t.Error("the caller's own entry is not marked mine")
		}
	}
	for _, it := range fx.get(t, fx.bobDo, f, "").Items {
		if it.Actor.Mine {
			t.Errorf("another person's entry is marked mine: %+v", it)
		}
	}

	// Ann loses her role, and carol hers: both are gone from the entries, and no name of either is in the answer.
	wantStatus(t, fx.keyDo("DELETE", "/api/v1/nodes/"+fx.b.ID+"/permissions/"+fx.ann.ID, nil), 204)
	wantStatus(t, fx.keyDo("DELETE", "/api/v1/nodes/"+f+"/permissions/"+fx.carol.ID, nil), 204)
	load()
	if !rename.Actor.Gone || rename.Actor.Name != "" {
		t.Errorf("a person who lost the role is %+v, want gone and no name", rename.Actor)
	}
	if toCarol.Details["subject_gone"] != true || toCarol.Details["subject_name"] != nil {
		t.Errorf("a subject who lost the role is %v, want gone and no name", toCarol.Details)
	}
	raw := fx.get(t, fx.keyDo, f, "").Raw
	raw += fx.get(t, fx.bobDo, f, "").Raw
	for _, name := range []string{"Ann Cohen", "Carol Katz"} {
		if strings.Contains(raw, name) {
			t.Errorf("%s is named after the role was lost", name)
		}
	}
	// A disabled user is gone as well.
	dave, daveSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "dave@example.com")
	wantStatus(t, fx.keyDo("PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+dave.ID, map[string]any{"display_name": "Dave Dahan"}), 200)
	fx.setPerm(t, fx.b.ID, dave.ID, "editor")
	fx.rename(t, bySession(t, fx.s, daveSess), f, "by-dave.txt")
	if p := fx.get(t, fx.bobDo, f, ""); p.Items[0].Actor.Name != "Dave Dahan" {
		t.Fatalf("dave is %+v", p.Items[0].Actor)
	}
	wantStatus(t, fx.keyDo("PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+dave.ID, map[string]any{"status": "disabled"}), 200)
	if p := fx.get(t, fx.bobDo, f, ""); !p.Items[0].Actor.Gone || p.Items[0].Actor.Name != "" {
		t.Errorf("a disabled user is %+v, want gone", p.Items[0].Actor)
	}
}

// TestActivityActorKinds: the actor kinds that carry no person, and an admin who acted while signed in as a member, which is
// shown as an admin and not as the member.
func TestActivityActorKinds(t *testing.T) {
	people := &activityPeople{reach: map[string]bool{"usr_a": true}, names: map[string]string{"usr_a": "Ann"}}
	p := &auth.Principal{Kind: auth.KindUser, UserID: "usr_a", AccountID: "acc_1"}
	cases := []struct {
		row  store.ActivityRow
		want activityActor
	}{
		{store.ActivityRow{ActorKind: "user", ActorID: "usr_a"}, activityActor{Kind: "user", Name: "Ann", Mine: true}},
		// An admin acting as a member is not shown to a member as an admin, nor as the member.
		{store.ActivityRow{ActorKind: "user", ActorID: "usr_a", Details: map[string]any{"impersonated_by": "usr_admin"}}, activityActor{Kind: "other"}},
		{store.ActivityRow{ActorKind: "user", ActorID: "usr_z"}, activityActor{Kind: "user", Gone: true}},
		{store.ActivityRow{ActorKind: "account_key", ActorID: "key_1"}, activityActor{Kind: "account_key"}},
		{store.ActivityRow{ActorKind: "system", ActorID: "sweep"}, activityActor{Kind: "system"}},
		{store.ActivityRow{ActorKind: "share_link", ActorID: "lnk_1"}, activityActor{Kind: "visitor"}},
		{store.ActivityRow{ActorKind: "admin_key", ActorID: "key_9"}, activityActor{Kind: "other"}},
	}
	for _, c := range cases {
		row := c.row
		if got := activityActorOf(p, &row, people); got != c.want {
			t.Errorf("%s %v: %+v, want %+v", row.ActorKind, row.Details, got, c.want)
		}
	}
	// A caller who manages the account sees the admin; a token the key minted does not.
	imp := store.ActivityRow{ActorKind: "user", ActorID: "usr_a", Details: map[string]any{"impersonated_by": "usr_admin"}}
	key := &auth.Principal{Kind: auth.KindAccountKey, ID: "key_1", AccountID: "acc_1"}
	owner := &auth.Principal{Kind: auth.KindUser, UserID: "usr_o", Role: auth.RoleOwner, AccountID: "acc_1"}
	keyToken := &auth.Principal{Kind: auth.KindAccountKey, ID: "key_1", AccountID: "acc_1", Token: &auth.TokenScope{All: true}}
	for name, c := range map[string]struct {
		p    *auth.Principal
		want string
	}{"key": {key, "admin"}, "owner": {owner, "admin"}, "token of the key": {keyToken, "other"}} {
		if got := activityActorOf(c.p, &imp, people); got.Kind != c.want || got.Name != "" {
			t.Errorf("%s sees %+v, want kind %s", name, got, c.want)
		}
	}
}

// TestActivityCommentsFollowTheSwitch: a comment is an entry with no text and no mention name; with the account's comments
// off the entries are left out and the route still answers.
func TestActivityCommentsFollowTheSwitch(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	f := fx.deep.ID
	c := decode[commentJSON](t, fx.annDo("POST", "/api/v1/nodes/"+f+"/comments", map[string]any{"body": "launch codes 4711"}))
	wantStatus(t, fx.annDo("POST", "/api/v1/nodes/"+f+"/comments", map[string]any{"body": "re", "parent_id": c.ID}), 201)
	wantStatus(t, fx.annDo("DELETE", "/api/v1/comments/"+c.ID, nil), 204)
	p := fx.get(t, fx.bobDo, f, "?kind=comment")
	if !slices.Equal(p.actions(), []string{"comment.delete", "comment.create", "comment.create"}) {
		t.Fatalf("comment entries %v", p.actions())
	}
	if strings.Contains(p.Raw, "4711") || strings.Contains(p.Raw, c.ID) {
		t.Errorf("the comment's text or ID is in the answer: %s", p.Raw)
	}
	if p.Items[1].Details["reply"] != true || p.Items[2].Details["reply"] != false {
		t.Errorf("reply flags %v %v", p.Items[1].Details, p.Items[2].Details)
	}
	wantStatus(t, fx.keyDo("PATCH", "/api/v1/accounts/"+fx.acc.ID+"/sharing-policy", map[string]any{"comments": false}), 200)
	if p := fx.get(t, fx.bobDo, f, ""); slices.Contains(p.actions(), "comment.create") || !slices.Contains(p.actions(), "node.create") {
		t.Errorf("with comments off the entries are %v", p.actions())
	}
	if p := fx.get(t, fx.bobDo, f, "?kind=comment"); len(p.Items) != 0 {
		t.Errorf("with comments off kind=comment gives %v", p.actions())
	}
}

// TestActivityCursor: a cursor continues without a repeat or a gap, a page is exact whatever the filters drop, the cursor
// shows no audit ID, and it opens only for the node, the caller and the kind it was made for.
func TestActivityCursor(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	f := fx.deep.ID
	for i := 0; i < 6; i++ {
		fx.rename(t, fx.keyDo, f, fmt.Sprintf("n%d.txt", i))
		fx.setPerm(t, f, fx.carol.ID, []string{"viewer", "editor"}[i%2]) // entries a viewer must not count
	}
	all := fx.get(t, fx.bobDo, f, "?limit=500")
	if len(all.Items) != 7 || all.Next != "" {
		t.Fatalf("the whole list is %d entries with next %q, want 7 and none", len(all.Items), all.Next)
	}
	var got []activityJSON
	cursor := ""
	for pages := 0; pages < 10; pages++ {
		q := "?limit=2"
		if cursor != "" {
			q += "&cursor=" + cursor
		}
		p := fx.get(t, fx.bobDo, f, q)
		if p.Next != "" && len(p.Items) != 2 {
			t.Fatalf("page %d has %d entries and a next cursor, want 2", pages, len(p.Items))
		}
		got = append(got, p.Items...)
		if cursor = p.Next; cursor == "" {
			break
		}
	}
	if len(got) != len(all.Items) {
		t.Fatalf("pages gave %d entries, want %d", len(got), len(all.Items))
	}
	for i := range got {
		if got[i].ID != all.Items[i].ID {
			t.Fatalf("entry %d differs between paging and one read", i)
		}
	}
	// An entry's ID is opaque, and the cursor carries no number.
	first := fx.get(t, fx.bobDo, f, "?limit=1")
	if _, err := strconv.ParseInt(first.Items[0].ID, 10, 64); err == nil {
		t.Errorf("entry ID %q is a number", first.Items[0].ID)
	}
	raw, err := base64.RawURLEncoding.DecodeString(first.Next)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Trim(string(raw), "0123456789\x00") == "" {
		t.Errorf("the cursor %q is a plain number", first.Next)
	}

	// The cursor opens for its own node, caller and kind only.
	same := "/api/v1/nodes/" + f + "/activity?limit=1&cursor=" + first.Next
	wantStatus(t, fx.bobDo("GET", same, nil), 200)
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"another caller": fx.annDo("GET", same, nil),
		"another node":   fx.keyDo("GET", "/api/v1/nodes/"+fx.sib.ID+"/activity?limit=1&cursor="+first.Next, nil),
		"another kind":   fx.bobDo("GET", same+"&kind=content", nil),
		"garbage":        fx.bobDo("GET", "/api/v1/nodes/"+f+"/activity?cursor=not-a-cursor", nil),
		"a plain number": fx.bobDo("GET", "/api/v1/nodes/"+f+"/activity?cursor="+
			base64.RawURLEncoding.EncodeToString([]byte("5")), nil),
		"empty bytes": fx.bobDo("GET", "/api/v1/nodes/"+f+"/activity?cursor=AAAA", nil),
	} {
		// The key and the others reach the nodes they ask about, so each of these is a cursor refusal and not a not_found.
		if e := wantError(t, rec, 400, apierr.InvalidRequest); e.Message == "" {
			t.Errorf("%s: no message", name)
		}
	}
	// A cursor from another account does not open either.
	otherTok := call(t, fx.s, "GET", "/api/v1/nodes/"+f+"/activity?cursor="+first.Next, fx.otherKey, nil)
	wantError(t, otherTok, 404, apierr.NotFound)
}

// TestActivityBoundaryErrors: limit and kind are checked with the codes the other lists use, before the node is read, so a
// caller with no role learns nothing from them.
func TestActivityBoundaryErrors(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	f := fx.deep.ID
	for _, q := range []string{"?limit=0", "?limit=501", "?limit=-1", "?limit=abc", "?limit=1.5", "?kind=everything", "?kind=Content",
		"?kind=content,comment", "?kind=%00", "?kind=" + strings.Repeat("x", 5000)} {
		e := wantError(t, fx.bobDo("GET", "/api/v1/nodes/"+f+"/activity"+q, nil), 400, apierr.InvalidRequest)
		_ = e
	}
	for _, q := range []string{"?limit=1", "?limit=500", "?kind=content", "?kind=comment", "?kind=access", "?kind=label", "?kind="} {
		wantStatus(t, fx.bobDo("GET", "/api/v1/nodes/"+f+"/activity"+q, nil), 200)
	}
	wantError(t, fx.bobDo("GET", "/api/v1/nodes/nod_short/activity", nil), 404, apierr.NotFound)
}

// TestActivityDeletionRecordsAreNotNodeHistory: the record of a deleted account is kept at installation level, with the
// account's name as its target (decision 48). No node's activity reaches it.
func TestActivityDeletionRecordsAreNotNodeHistory(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	f := fx.deep.ID
	wantStatus(t, fx.keyDo("PATCH", "/api/v1/nodes/"+f, map[string]any{"name": "keep.txt"}), 200)
	// A record at installation level whose target is this node's ID, as an unlucky name could make it.
	if err := fx.s.store.Write(t.Context(), func(tx *store.Tx) error {
		return tx.InsertAudit(t.Context(), &store.AuditEntry{Time: time.Now(), ActorKind: "system", ActorID: "job",
			Action: "node.write", Target: f, Details: map[string]any{"size": 424242}})
	}); err != nil {
		t.Fatal(err)
	}
	p := fx.get(t, fx.keyDo, f, "")
	if strings.Contains(p.Raw, "424242") {
		t.Errorf("an installation-level record is in a node's activity: %s", p.Raw)
	}
}

// TestActivityActionsAreReviewed pins the list of actions the pane shows. An action is added to it only after its details
// are read for names and IDs, and after the role that may read it is chosen. The actions the code writes that are not on
// this list are not shown: that is the safe side.
func TestActivityActionsAreReviewed(t *testing.T) {
	var all []string
	for _, k := range activityKinds {
		all = append(all, k.name+":"+strings.Join(k.actions, ","))
	}
	want := []string{
		"content:node.create,node.write,node.rename,node.move,node.trash,node.restore,node.version.restore,node.copy",
		"comment:comment.create,comment.delete",
		"access:permission.set,permission.expired",
		"label:node.label_set,node.label_clear",
	}
	if !slices.Equal(all, want) {
		b, _ := json.Marshal(all)
		t.Errorf("the activity kinds changed: %s", b)
	}
	for _, k := range activityKinds {
		if k.manager != (k.name == "access" || k.name == "label") {
			t.Errorf("kind %s has manager %v", k.name, k.manager)
		}
	}
}

// TestActivityKeyMintedTokenIsNotTheKey: a token the account key minted has the key's Kind and a Token. It must not skip
// the private-entry rule the key skips: it reads what a viewer of the node reads. The file was created in a member's
// personal folder and moved to the team space, so its earlier entries are private to the member.
func TestActivityKeyMintedTokenIsNotTheKey(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	pf := fx.personalFolder(t, fx.annSess)
	rec := rawSession(t, fx.s, fx.annSess, "POST", "/api/v1/folders/"+pf+"/files?"+q("/mine.txt"), "hello", nil)
	wantStatus(t, rec, 201)
	f := decode[nodeJSON](t, rec).ID
	wantStatus(t, fx.annDo("POST", "/api/v1/nodes/"+f+"/move", map[string]any{"folder": fx.fld.ID, "parent": fx.b.ID}), 200)
	fx.rename(t, fx.annDo, f, "team.txt")

	tok := byCred(t, fx.s, mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}}))
	if p := fx.get(t, tok, f, ""); !slices.Equal(p.actions(), []string{"node.rename"}) {
		t.Errorf("a token the key minted reads %v, want only what a viewer reads", p.actions())
	}
	if p := fx.get(t, fx.keyDo, f, ""); !slices.Equal(p.actions(), []string{"node.rename", "node.move", "node.create"}) {
		t.Errorf("the key reads %v, want everything", p.actions())
	}
}

// TestActivityIsRateLimited: a caller has its own bucket, and so does an account. A spent bucket answers 429 with the
// code the other limits use, and another caller of the account is not affected until the account's bucket is spent.
func TestActivityIsRateLimited(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	f := fx.deep.ID
	fx.s.limits["activity"] = newLimiter(3)
	for i := 0; i < 3; i++ {
		wantStatus(t, fx.bobDo("GET", "/api/v1/nodes/"+f+"/activity", nil), 200)
	}
	wantError(t, fx.bobDo("GET", "/api/v1/nodes/"+f+"/activity", nil), 429, apierr.RateLimited)
	wantStatus(t, fx.annDo("GET", "/api/v1/nodes/"+f+"/activity", nil), 200)
	fx.s.limits["activity"] = newLimiter(100)
	fx.s.limits["activity.account"] = newLimiter(2)
	wantStatus(t, fx.annDo("GET", "/api/v1/nodes/"+f+"/activity", nil), 200)
	wantStatus(t, fx.bobDo("GET", "/api/v1/nodes/"+f+"/activity", nil), 200)
	wantError(t, fx.keyDo("GET", "/api/v1/nodes/"+f+"/activity", nil), 429, apierr.RateLimited)
	// A refused request spends nothing from the other bucket, and a node that is not reached is charged like any other.
	wantError(t, fx.carolDo("GET", "/api/v1/nodes/"+f+"/activity", nil), 429, apierr.RateLimited)
}

// TestActivityBoundsTheReachWalks: a page walks at most the bound of distinct people. The rest get no name and are not
// called gone either: nobody checked them.
func TestActivityBoundsTheReachWalks(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	f := fx.deep.ID
	dave, daveSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "dave@example.com")
	wantStatus(t, fx.keyDo("PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+dave.ID, map[string]any{"display_name": "Dave Dahan"}), 200)
	fx.setPerm(t, fx.b.ID, dave.ID, "editor")
	fx.rename(t, bySession(t, fx.s, daveSess), f, "d.txt")
	fx.rename(t, fx.annDo, f, "a.txt")
	named := func() (n, gone int) {
		for _, it := range fx.get(t, fx.bobDo, f, "").Items {
			if it.Actor.Kind != "user" {
				continue
			}
			if it.Actor.Name != "" {
				n++
			}
			if it.Actor.Gone {
				gone++
			}
		}
		return
	}
	if n, gone := named(); n != 2 || gone != 0 {
		t.Fatalf("unbounded: %d named and %d gone, want 2 and 0", n, gone)
	}
	fx.s.activityWalks = 1
	if n, gone := named(); n != 1 || gone != 0 {
		t.Fatalf("bound of 1: %d named and %d gone, want 1 and 0: a person nobody checked is not gone", n, gone)
	}
}

// TestActivityScanCeilingThroughTheRoute: with a small ceiling, a rare kind on a node with a long history is found by
// following the cursors, and a page may be short or empty and still carry one.
func TestActivityScanCeilingThroughTheRoute(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	f := fx.deep.ID
	fx.setPerm(t, f, fx.carol.ID, "viewer") // the one permission.set, oldest after the creation
	for i := 0; i < 12; i++ {
		fx.rename(t, fx.keyDo, f, fmt.Sprintf("r%d.txt", i))
	}
	fx.s.activityCeiling = 5
	first := fx.get(t, fx.keyDo, f, "?kind=access")
	if len(first.Items) != 0 || first.Next == "" {
		t.Fatalf("first page has %d entries and next %q, want an empty page with a cursor", len(first.Items), first.Next)
	}
	var got []string
	next := first.Next
	for i := 0; next != "" && i < 20; i++ {
		p := fx.get(t, fx.keyDo, f, "?kind=access&cursor="+next)
		got = append(got, p.actions()...)
		next = p.Next
	}
	if !slices.Equal(got, []string{"permission.set"}) {
		t.Fatalf("following the cursors found %v, want the one permission.set", got)
	}
}

// TestActivityImpersonationIsShownToWhoManagesTheAccount: an installation admin signed in as a member creates a folder in
// the member's My files. The account key reads the entry as an admin's, named by no one. The member reads it as an actor
// of kind other: not the member, who did not act, and with no sign that an admin did.
func TestActivityImpersonationIsShownToWhoManagesTheAccount(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	imp, _ := fx.signedInAsMember(t)
	me := decode[meResponse](t, callSession(t, fx.s, imp, "GET", "/api/v1/me", nil))
	dir := decode[nodeJSON](t, callSession(t, fx.s, imp, "POST", "/api/v1/folders/"+*me.PersonalFolderID+"/directories",
		map[string]any{"path": "Impersonated"}))
	read := func(do func() *httptest.ResponseRecorder) activityPage {
		rec := do()
		wantStatus(t, rec, 200)
		l := decode[listResponse[activityJSON]](t, rec)
		return activityPage{Items: l.Items, Raw: rec.Body.String()}
	}
	path := "/api/v1/nodes/" + dir.ID + "/activity"
	asKey := read(func() *httptest.ResponseRecorder { return call(t, fx.s, "GET", path, fx.accKey, nil) })
	asMember := read(func() *httptest.ResponseRecorder { return callSession(t, fx.s, fx.memSess, "GET", path, nil) })
	if len(asKey.Items) != 1 || asKey.Items[0].Actor.Kind != "admin" || asKey.Items[0].Actor.Name != "" {
		t.Errorf("the key reads %+v, want one entry by an admin, unnamed", asKey.Items)
	}
	if len(asMember.Items) != 1 || asMember.Items[0].Actor.Kind != "other" || asMember.Items[0].Actor.Name != "" {
		t.Errorf("the member reads %+v, want one entry by kind other", asMember.Items)
	}
	for _, bad := range []string{"admin", fx.adminUser.ID, "ann@example.com", "Ann", "impersonat"} {
		if strings.Contains(asMember.Raw, bad) {
			t.Errorf("the member's answer contains %q: %s", bad, asMember.Raw)
		}
	}
}

// TestActivityPastTheBoundNobodyIsCalledGone: a page of 41 distinct current members and one real former member, with the
// default bound of 40. The former member, who is within the bound, is gone. The members past the bound have no name and
// are not gone: they are of kind other, a state that means nobody checked. Nobody else is called gone.
func TestActivityPastTheBoundNobodyIsCalledGone(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	f := fx.deep.ID
	var members []userResponse
	for i := 0; i < 42; i++ {
		u, _ := verifiedUser(t, fx.s, fx.key, fx.acc.ID, fmt.Sprintf("m%d@example.com", i))
		wantStatus(t, fx.keyDo("PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+u.ID, map[string]any{"display_name": fmt.Sprintf("Member %d", i)}), 200)
		fx.setPerm(t, f, u.ID, "editor")
		members = append(members, u)
	}
	// Member 41 loses the role after acting; the entries are written oldest first, member 41 last, so it is the newest.
	err := fx.s.store.Write(t.Context(), func(tx *store.Tx) error {
		for _, u := range members {
			e := &store.AuditEntry{Time: time.Now(), AccountID: fx.acc.ID, ActorKind: "user", ActorID: u.ID, Action: "node.write", Target: f}
			if err := tx.InsertAudit(t.Context(), e); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	wantStatus(t, fx.keyDo("DELETE", "/api/v1/nodes/"+f+"/permissions/"+members[41].ID, nil), 204)
	var named, gone, other int
	var goneName string
	for _, it := range fx.get(t, fx.bobDo, f, "?limit=500&kind=content").Items {
		if it.Action != "node.write" || it.Actor.Kind == "account_key" {
			continue
		}
		switch {
		case it.Actor.Gone:
			gone++
			goneName = it.Actor.Name
		case it.Actor.Kind == "other":
			other++
			if it.Actor.Name != "" {
				t.Errorf("an unchecked member has a name: %+v", it.Actor)
			}
		case it.Actor.Name != "":
			named++
		}
	}
	if gone != 1 || goneName != "" || named != 39 || other != 2 {
		t.Fatalf("%d gone (name %q), %d named, %d unchecked; want 1 gone, 39 named, 2 unchecked", gone, goneName, named, other)
	}
}

// TestActivityPastTheBoundSubjectsAreNotCalledGone: with a bound of one, the second person and the second group named by
// access entries are not looked up. They have no name and no subject_gone flag, though they still hold the role.
func TestActivityPastTheBoundSubjectsAreNotCalledGone(t *testing.T) {
	t.Parallel()
	fx := newActivityFixture(t)
	f := fx.deep.ID
	g1, g2 := fx.group(t, "Group One"), fx.group(t, "Group Two")
	fx.setPerm(t, f, fx.carol.ID, "viewer")
	fx.setPerm(t, f, fx.bob.ID, "viewer")
	fx.setPerm(t, f, g1, "viewer")
	fx.setPerm(t, f, g2, "viewer")
	fx.s.activityWalks = 1
	var plain, named int
	for _, it := range fx.get(t, fx.keyDo, f, "?kind=access").Items {
		if it.Details["subject_gone"] == true {
			t.Errorf("a subject who holds the role is called gone: %v", it.Details)
		}
		if it.Details["subject_name"] != nil {
			named++
		} else {
			plain++
		}
	}
	// Of four subjects, one person and one group are looked up (and named); the other person and group are not.
	if named != 2 || plain != 2 {
		t.Errorf("%d named and %d unchecked, want 2 and 2", named, plain)
	}
}
