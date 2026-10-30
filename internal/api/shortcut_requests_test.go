// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/mail"
)

// namedShortcut makes a shortcut with an explicit name, so a test can tell its name from its target's.
func namedShortcut(t *testing.T, fx *sharingFixture, dirID, target, name string) string {
	t.Helper()
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+dirID+"/shortcuts", fx.key, map[string]any{"target": target, "name": name})
	wantStatus(t, rec, 201)
	return decodeMap(t, rec)["id"].(string)
}

// requestOn is a requester's ask for a role on a node: the answer it gets.
func requestOn(t *testing.T, fx *sharingFixture, node, role string) *httptest.ResponseRecorder {
	t.Helper()
	return callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+node+"/access-requests", map[string]any{"role": role})
}

// requestsOn lists the requests filed on a node, as its manager sees them.
func requestsOn(t *testing.T, fx *sharingFixture, node string) []accessRequestJSON {
	t.Helper()
	rec := call(t, fx.s, "GET", "/api/v1/nodes/"+node+"/access-requests", fx.key, nil)
	wantStatus(t, rec, 200)
	return decode[listResponse[accessRequestJSON]](t, rec).Items
}

// firstRequestOn returns the ID of the one request filed on a node, failing the test when there is none.
func firstRequestOn(t *testing.T, fx *sharingFixture, node string) string {
	t.Helper()
	reqs := requestsOn(t, fx, node)
	if len(reqs) == 0 {
		t.Fatal("no request is filed on the target")
	}
	return reqs[0].ID
}

// lastMailTo returns the body of the newest mail sent to the address, or "" when none was.
func lastMailTo(t *testing.T, fx *sharingFixture, to string) string {
	t.Helper()
	body := ""
	for _, m := range fx.sent(t) {
		if m.To == to {
			body = m.Body
		}
	}
	return body
}

// TestRequestOnShortcutIsFiledOnItsTarget: a request that names a shortcut is recorded on the target, so its managers
// receive it, and approving it grants the target. The requester reaches the shortcut and not the target.
func TestRequestOnShortcutIsFiledOnItsTarget(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	sc := namedShortcut(t, fx, fx.c.ID, fx.deep.ID, "Shortcut-X")
	fx.setPerm(t, fx.c.ID, fx.ann.ID, "viewer")

	wantStatus(t, requestOn(t, fx, sc, "viewer"), 202)
	reqs := requestsOn(t, fx, fx.deep.ID)
	if len(reqs) != 1 || reqs[0].NodeID != fx.deep.ID || reqs[0].UserID != fx.ann.ID {
		t.Fatalf("requests on the target = %+v, want one from ann on the target", reqs)
	}
	if n := requestsOn(t, fx, sc); len(n) != 0 {
		t.Errorf("requests filed on the shortcut itself = %+v, want none", n)
	}
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/access-requests/"+reqs[0].ID+"/approve", fx.key, nil), 200)
	wantStatus(t, fx.stat(t, fx.annSess, fx.deep.ID), 200)
}

// TestRequestOnShortcutMailsNameTheShortcut: an approval mail to the requester names the shortcut and links to it, while
// the requester still reaches it. The target's name and ID stay out of it.
func TestRequestOnShortcutMailsNameTheShortcut(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	sc := namedShortcut(t, fx, fx.c.ID, fx.deep.ID, "Shortcut-X")
	fx.setPerm(t, fx.c.ID, fx.ann.ID, "viewer")
	wantStatus(t, requestOn(t, fx, sc, "viewer"), 202)
	reqID := firstRequestOn(t, fx, fx.deep.ID)
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/access-requests/"+reqID+"/approve", fx.key, nil), 200)

	body := lastMailTo(t, fx, "ann@example.com")
	if !strings.Contains(body, "Shortcut-X") || !strings.Contains(body, sc) {
		t.Errorf("the approval mail does not name the shortcut and link to it: %q", body)
	}
	if strings.Contains(body, fx.deep.ID) || strings.Contains(body, "file.txt") {
		t.Errorf("the approval mail names the target: %q", body)
	}
}

// TestRequestOnShortcutDenialMailIsGeneric: a denial names nothing and links to nothing, because nothing was granted and
// a contributor chose the shortcut's name. The mail is the generic wording, with no link and no empty line where one
// would be.
func TestRequestOnShortcutDenialMailIsGeneric(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	sc := namedShortcut(t, fx, fx.c.ID, fx.deep.ID, "Shortcut-X")
	fx.setPerm(t, fx.c.ID, fx.ann.ID, "viewer")
	wantStatus(t, requestOn(t, fx, sc, "viewer"), 202)
	reqID := firstRequestOn(t, fx, fx.deep.ID)
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/access-requests/"+reqID+"/deny", fx.key, nil), 200)

	body := lastMailTo(t, fx, "ann@example.com")
	if !strings.Contains(body, mail.GenericItemLabel("en")) {
		t.Errorf("the denial mail is not the generic wording: %q", body)
	}
	for _, leak := range []string{"Shortcut-X", sc, fx.deep.ID, "file.txt", "/n/"} {
		if strings.Contains(body, leak) {
			t.Errorf("the denial mail contains %q: %q", leak, body)
		}
	}
	if strings.Contains(body, "\n\n\n") {
		t.Errorf("the denial mail has an empty line where the link would be: %q", body)
	}
}

// TestRequestOnShortcutApprovalMailChecksReachNow: an approval mail names the shortcut only while the requester still
// reaches it. Trashing it, or moving it somewhere private and renaming it, between filing and decision leaves the
// generic wording with no link, and no trace of the old or the new name.
func TestRequestOnShortcutApprovalMailChecksReachNow(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		alter func(t *testing.T, fx *sharingFixture, sc string)
	}{
		{"trashed", func(t *testing.T, fx *sharingFixture, sc string) {
			wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+sc, fx.key, nil), 204)
		}},
		{"moved and renamed", func(t *testing.T, fx *sharingFixture, sc string) {
			wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+sc+"/move", fx.key, map[string]any{"parent": fx.a.ID}), 200)
			wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+sc, fx.key, map[string]any{"name": "Renamed-Y"}), 200)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx := newSharingFixture(t)
			sc := namedShortcut(t, fx, fx.c.ID, fx.deep.ID, "Shortcut-X")
			fx.setPerm(t, fx.c.ID, fx.ann.ID, "viewer")
			wantStatus(t, requestOn(t, fx, sc, "viewer"), 202)
			reqID := firstRequestOn(t, fx, fx.deep.ID)
			tc.alter(t, fx, sc)
			wantStatus(t, call(t, fx.s, "POST", "/api/v1/access-requests/"+reqID+"/approve", fx.key, nil), 200)

			body := lastMailTo(t, fx, "ann@example.com")
			if body == "" {
				t.Fatal("no decision mail reached the requester")
			}
			for _, leak := range []string{"Shortcut-X", "Renamed-Y", sc, fx.deep.ID, "file.txt", "/n/"} {
				if strings.Contains(body, leak) {
					t.Errorf("the approval mail contains %q though the requester no longer reaches the shortcut: %q", leak, body)
				}
			}
			if !strings.Contains(body, mail.GenericItemLabel("en")) {
				t.Errorf("the approval mail is not the generic wording: %q", body)
			}
		})
	}
}

// TestRequestOnDeletedShortcutMailsNoName: when the shortcut is gone, the decision mail falls back to generic
// wording with no link, never to the target's name or link.
func TestRequestOnDeletedShortcutMailsNoName(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	sc := namedShortcut(t, fx, fx.c.ID, fx.deep.ID, "Shortcut-X")
	fx.setPerm(t, fx.c.ID, fx.ann.ID, "viewer")
	wantStatus(t, requestOn(t, fx, sc, "viewer"), 202)
	reqID := firstRequestOn(t, fx, fx.deep.ID)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+sc+"?permanent=true", fx.key, nil), 204)
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/access-requests/"+reqID+"/deny", fx.key, nil), 200)

	body := lastMailTo(t, fx, "ann@example.com")
	if body == "" {
		t.Fatal("no decision mail reached the requester")
	}
	for _, leak := range []string{"Shortcut-X", "file.txt", fx.deep.ID, sc} {
		if strings.Contains(body, leak) {
			t.Errorf("the decision mail for a deleted shortcut contains %q: %q", leak, body)
		}
	}
}

// TestRequestOnShortcutAnswersAsEveryOtherRequest: a request on a shortcut gets the same answer as a request on a
// node that does not exist, whether the target is trashed, purged, already reachable, or the requester does not reach
// the shortcut. Status and body are compared. A duplicate of a live request answers the same way too.
func TestRequestOnShortcutAnswersAsEveryOtherRequest(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.c.ID, fx.ann.ID, "viewer")

	baseline := requestOn(t, fx, id.New("nod"), "viewer") // a node that does not exist
	wantStatus(t, baseline, 202)
	want := baseline.Body.String()

	// A live target the requester does not reach: filed, and the answer is the same.
	live := namedShortcut(t, fx, fx.c.ID, fx.deep.ID, "Live")
	liveRec := requestOn(t, fx, live, "viewer")
	wantStatus(t, liveRec, 202)
	if liveRec.Body.String() != want {
		t.Errorf("a live target answers %q, a missing node %q", liveRec.Body.String(), want)
	}
	// A duplicate of the live request collapses and answers the same.
	dup := requestOn(t, fx, live, "viewer")
	wantStatus(t, dup, 202)
	if dup.Body.String() != want {
		t.Errorf("a duplicate answers %q, a missing node %q", dup.Body.String(), want)
	}

	// A target in the trash.
	trashed := namedShortcut(t, fx, fx.c.ID, fx.sib.ID, "Trashed")
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.sib.ID, fx.key, nil), 204)
	if rec := requestOn(t, fx, trashed, "viewer"); rec.Code != 202 || rec.Body.String() != want {
		t.Errorf("a trashed target answers %d %q, want %q", rec.Code, rec.Body.String(), want)
	}

	// A target purged after the shortcut was made: the shortcut has no live target.
	gone := putNode(t, fx.s, fx.key, fx.fld.ID, "/gone.txt", "g")
	purged := namedShortcut(t, fx, fx.c.ID, gone.ID, "Purged")
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+gone.ID+"?permanent=true", fx.key, nil), 204)
	if rec := requestOn(t, fx, purged, "viewer"); rec.Code != 202 || rec.Body.String() != want {
		t.Errorf("a purged target answers %d %q, want %q", rec.Code, rec.Body.String(), want)
	}

	// A target the requester already reaches: nothing is filed, and the answer is the same.
	fx.setPerm(t, fx.deep.ID, fx.ann.ID, "viewer")
	reach := namedShortcut(t, fx, fx.c.ID, fx.deep.ID, "Reached")
	if rec := requestOn(t, fx, reach, "viewer"); rec.Code != 202 || rec.Body.String() != want {
		t.Errorf("an already reachable target answers %d %q, want %q", rec.Code, rec.Body.String(), want)
	}

	// A shortcut the requester does not reach (in a directory it cannot see): the same answer.
	other := putNode(t, fx.s, fx.key, fx.fld.ID, "/other.txt", "o")
	hidden := namedShortcut(t, fx, fx.a.ID, other.ID, "Hidden")
	if rec := requestOn(t, fx, hidden, "viewer"); rec.Code != 202 || rec.Body.String() != want {
		t.Errorf("a shortcut out of reach answers %d %q, want %q", rec.Code, rec.Body.String(), want)
	}
}

// TestRequestOnShortcutAuditHidesTarget: the request's audit entry names the request, not the target. Someone who reads
// the audit of the shortcut's folder must not learn the target's ID from it.
func TestRequestOnShortcutAuditHidesTarget(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	sc := namedShortcut(t, fx, fx.c.ID, fx.deep.ID, "Shortcut-X")
	fx.setPerm(t, fx.c.ID, fx.ann.ID, "viewer")
	wantStatus(t, requestOn(t, fx, sc, "viewer"), 202)
	rec := call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/audit", fx.key, nil)
	wantStatus(t, rec, 200)
	found := false
	for _, it := range decodeMap(t, rec)["items"].([]any) {
		m := it.(map[string]any)
		if m["action"] != "access.request" {
			continue
		}
		found = true
		if strings.Contains(fmt.Sprint(m["details"]), fx.deep.ID) {
			t.Errorf("the access.request audit entry names the target: %v", m["details"])
		}
	}
	if !found {
		t.Fatal("no access.request audit entry")
	}
}

// TestRequestsOnShortcutsNeverShowTheShortcut: the manager's list names the target and nothing of the shortcut.
func TestRequestsOnShortcutsNeverShowTheShortcut(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	sc := namedShortcut(t, fx, fx.c.ID, fx.deep.ID, "Shortcut-X")
	fx.setPerm(t, fx.c.ID, fx.ann.ID, "viewer")
	wantStatus(t, requestOn(t, fx, sc, "viewer"), 202)
	rec := call(t, fx.s, "GET", "/api/v1/nodes/"+fx.deep.ID+"/access-requests", fx.key, nil)
	wantStatus(t, rec, 200)
	if strings.Contains(rec.Body.String(), sc) || strings.Contains(rec.Body.String(), "Shortcut-X") {
		t.Errorf("the manager's list shows the shortcut: %s", rec.Body.String())
	}
}
