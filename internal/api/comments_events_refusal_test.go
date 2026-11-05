// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"strings"
	"testing"
)

// TestNotFoundRefusalsQueueNothing: an action answered not_found queues no event. That covers an unknown comment, a
// tombstone (a deleted comment with replies, which has no body to change, resolve or delete again), and a shortcut, which
// has no comments of its own.
func TestNotFoundRefusalsQueueNothing(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.subscribeComments(t)
	parent := fx.say(t, fx.annSess, fx.file, "parent with a reply")
	fx.reply(t, fx.annSess, fx.file, parent.ID, "kept reply")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/comments/"+parent.ID, nil), 204)
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.root+"/shortcuts", fx.key, map[string]any{"target": fx.file})
	wantStatus(t, rec, 201)
	sc := decode[nodeJSON](t, rec)

	unknown := "cmt_" + strings.Repeat("a", 26)
	seen := len(outboxOf(t, fx.s, fx.acc.ID))
	refusals := []struct {
		name, method, path string
		body               any
	}{
		{"edit an unknown comment", "PATCH", "/api/v1/comments/" + unknown, map[string]any{"body": "x"}},
		{"delete an unknown comment", "DELETE", "/api/v1/comments/" + unknown, nil},
		{"resolve an unknown comment", "POST", "/api/v1/comments/" + unknown + "/resolve", map[string]any{"resolved": true}},
		{"edit a tombstone", "PATCH", "/api/v1/comments/" + parent.ID, map[string]any{"body": "x"}},
		{"resolve a tombstone", "POST", "/api/v1/comments/" + parent.ID + "/resolve", map[string]any{"resolved": true}},
		{"delete a tombstone again", "DELETE", "/api/v1/comments/" + parent.ID, nil},
		{"comment on a shortcut", "POST", "/api/v1/nodes/" + sc.ID + "/comments", map[string]any{"body": "x"}},
	}
	for _, r := range refusals {
		rec := callSession(t, fx.s, fx.annSess, r.method, r.path, r.body)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status %d, want 404 not_found: %s", r.name, rec.Code, rec.Body.String())
		}
	}
	if evs := newCommentEvents(t, fx, seen); len(evs) != 0 {
		t.Fatalf("not_found refusals wrote %d events: %v", len(evs), evs)
	}
}

// TestViewerRefusedDeleteAndResolveQueueNothing: a viewer may not delete another person's comment, nor resolve one; both
// are refused, and neither writes an event.
func TestViewerRefusedDeleteAndResolveQueueNothing(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.subscribeComments(t)
	c := fx.say(t, fx.annSess, fx.file, "ann's comment")
	seen := len(outboxOf(t, fx.s, fx.acc.ID))

	rec := callSession(t, fx.s, fx.bobSess, "DELETE", "/api/v1/comments/"+c.ID, nil)
	if e := wantError(t, rec, http.StatusForbidden, "forbidden"); e.Details["reason"] != "role" {
		t.Fatalf("viewer delete refusal reason = %v, want role", e.Details["reason"])
	}
	rec = callSession(t, fx.s, fx.bobSess, "POST", "/api/v1/comments/"+c.ID+"/resolve", map[string]any{"resolved": true})
	if e := wantError(t, rec, http.StatusForbidden, "forbidden"); e.Details["reason"] != "role" {
		t.Fatalf("viewer resolve refusal reason = %v, want role", e.Details["reason"])
	}
	if evs := newCommentEvents(t, fx, seen); len(evs) != 0 {
		t.Fatalf("viewer refusals wrote %d events: %v", len(evs), evs)
	}
}

// TestDeletedAuthorResolvedByManagerThroughTheRoute: a comment whose author was deleted, then resolved by a manager, queues
// comment.resolved with author_id null and actor_id set to the manager, as the route answers.
func TestDeletedAuthorResolvedByManagerThroughTheRoute(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.subscribeComments(t)
	dan, danSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "dan@example.com")
	fx.setPerm(t, fx.root, dan.ID, "manager")
	c := fx.say(t, fx.annSess, fx.file, "still open")
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.ann.ID, fx.key, nil), 204)

	seen := len(outboxOf(t, fx.s, fx.acc.ID))
	wantStatus(t, callSession(t, fx.s, danSess, "POST", "/api/v1/comments/"+c.ID+"/resolve",
		map[string]any{"resolved": true}), 200)
	ev := wantOneEvent(t, fx, seen, "comment.resolved")
	wantKeys(t, ev.Data, "id", "reply_to_id", "node_id", "folder_id", "author_id", "actor_id", "mentions")
	if ev.Data["author_id"] != nil || ev.Data["actor_id"] != dan.ID || ev.Data["id"] != c.ID {
		t.Fatalf("payload = %v, want author_id null, actor_id %s, id %s", ev.Data, dan.ID, c.ID)
	}
	noCommentText(t, ev.Raw, "still open", "ann@example.com")
}

// TestResolvingAResolvedCommentQueuesAgain pins the documented repeat behaviour (decision 83): resolving a comment that is
// already resolved answers 200 and queues a second comment.resolved, as the audit records a second entry.
func TestResolvingAResolvedCommentQueuesAgain(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.subscribeComments(t)
	c := fx.say(t, fx.annSess, fx.file, "resolve me twice")
	for i := 0; i < 2; i++ {
		seen := len(outboxOf(t, fx.s, fx.acc.ID))
		wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/comments/"+c.ID+"/resolve",
			map[string]any{"resolved": true}), 200)
		ev := wantOneEvent(t, fx, seen, "comment.resolved")
		if ev.Data["id"] != c.ID {
			t.Fatalf("resolve %d: payload id %v, want %s", i+1, ev.Data["id"], c.ID)
		}
	}
}
