// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"
)

// commentOutbox is one event of the outbox with its data object decoded, so a test can compare its key set and its IDs.
type commentOutbox struct {
	Type string
	Raw  string
	Data map[string]any
}

// newCommentEvents returns the events of the account written after the first seen ones, in order.
func newCommentEvents(t *testing.T, fx *commentFixture, seen int) []commentOutbox {
	t.Helper()
	all := outboxOf(t, fx.s, fx.acc.ID)
	var out []commentOutbox
	for _, r := range all[seen:] {
		var env struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal([]byte(r.Payload), &env); err != nil {
			t.Fatal(err)
		}
		out = append(out, commentOutbox{Type: r.Type, Raw: r.Payload, Data: env.Data})
	}
	return out
}

// wantOneEvent checks that exactly one event was written since seen, of type typ, and returns it.
func wantOneEvent(t *testing.T, fx *commentFixture, seen int, typ string) commentOutbox {
	t.Helper()
	evs := newCommentEvents(t, fx, seen)
	if len(evs) != 1 || evs[0].Type != typ {
		var got []string
		for _, e := range evs {
			got = append(got, e.Type)
		}
		t.Fatalf("events written = %v, want exactly [%s]", got, typ)
	}
	return evs[0]
}

// wantKeys checks the key set of a payload: exactly the keys given, no more and no fewer.
func wantKeys(t *testing.T, data map[string]any, keys ...string) {
	t.Helper()
	var got []string
	for k := range data {
		got = append(got, k)
	}
	sort.Strings(got)
	want := append([]string(nil), keys...)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("payload keys = %v, want %v", got, want)
	}
}

// noCommentText checks that none of the given strings (comment text, names, addresses) appears in a payload.
func noCommentText(t *testing.T, raw string, forbidden ...string) {
	t.Helper()
	for _, f := range forbidden {
		if strings.Contains(raw, f) {
			t.Fatalf("payload carries %q: %s", f, raw)
		}
	}
}

// commentEndpoint makes an endpoint through the API, as the account key, and returns its ID.
func (fx *commentFixture) commentEndpoint(t *testing.T, events ...string) string {
	t.Helper()
	rec := call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/webhooks", fx.key,
		map[string]any{"url": "https://hooks.example.com/in", "events": events})
	wantStatus(t, rec, 201)
	return decode[struct {
		ID string `json:"id"`
	}](t, rec).ID
}

// subscribeComments makes an endpoint that takes all five comment events. The outbox holds only the events that some
// endpoint takes (EmitEvent writes no row for an event nobody takes), so each test that reads the outbox needs one. A
// test with none would pass whether or not the route emitted anything.
func (fx *commentFixture) subscribeComments(t *testing.T) {
	t.Helper()
	fx.commentEndpoint(t, "comment.created", "comment.updated", "comment.deleted", "comment.resolved", "comment.reopened")
}

// deliveries counts the deliveries of one endpoint.
func (fx *commentFixture) deliveries(t *testing.T, endpoint string) int {
	t.Helper()
	return countOf(t, fx.s, "SELECT COUNT(*) FROM webhook_deliveries WHERE endpoint_id = ?", endpoint)
}

// TestCommentCreatedPayloadIsIDsOnly: a top-level comment queues one comment.created, whose payload holds the IDs and
// nothing else: no text, no author name, no address.
func TestCommentCreatedPayloadIsIDsOnly(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.subscribeComments(t)
	fx.named(t, fx.ann, "Ann Secretname")
	seen := len(outboxOf(t, fx.s, fx.acc.ID))
	c := fx.say(t, fx.annSess, fx.file, "pineapple body text")
	ev := wantOneEvent(t, fx, seen, "comment.created")
	wantKeys(t, ev.Data, "comment", "reply_to", "node", "folder", "author", "mentions")
	if ev.Data["comment"] != c.ID || ev.Data["node"] != fx.file || ev.Data["folder"] != fx.fld.ID {
		t.Fatalf("payload IDs = %v, want comment %s, node %s, folder %s", ev.Data, c.ID, fx.file, fx.fld.ID)
	}
	if ev.Data["author"] != fx.ann.ID || ev.Data["reply_to"] != nil || ev.Data["mentions"] != float64(0) {
		t.Fatalf("payload = %v, want author %s, reply_to null, mentions 0", ev.Data, fx.ann.ID)
	}
	noCommentText(t, ev.Raw, "pineapple", "Ann Secretname", "ann@example.com")
}

// TestCommentReplyCarriesReplyTo: a reply queues comment.created with reply_to set to its parent.
func TestCommentReplyCarriesReplyTo(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.subscribeComments(t)
	parent := fx.say(t, fx.annSess, fx.file, "parent")
	seen := len(outboxOf(t, fx.s, fx.acc.ID))
	r := fx.reply(t, fx.annSess, fx.file, parent.ID, "a reply")
	ev := wantOneEvent(t, fx, seen, "comment.created")
	if ev.Data["comment"] != r.ID || ev.Data["reply_to"] != parent.ID {
		t.Fatalf("reply payload = %v, want comment %s and reply_to %s", ev.Data, r.ID, parent.ID)
	}
}

// TestCommentCreatedCountsMentions: a mention counts once in mentions, as the audit counts it.
func TestCommentCreatedCountsMentions(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.subscribeComments(t)
	fx.named(t, fx.bob, "Bob Example")
	items := fx.suggest(t, fx.annSess, fx.file, "Bob")
	if len(items) != 1 {
		t.Fatalf("suggestions for Bob = %+v, want one", items)
	}
	seen := len(outboxOf(t, fx.s, fx.acc.ID))
	rec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.file+"/comments",
		map[string]any{"body": "@Bob look", "mentions": []string{items[0].Ref}})
	wantStatus(t, rec, 201)
	ev := wantOneEvent(t, fx, seen, "comment.created")
	if ev.Data["mentions"] != float64(1) {
		t.Fatalf("mentions = %v, want 1", ev.Data["mentions"])
	}
	noCommentText(t, ev.Raw, "Bob Example", "@Bob")
}

// TestCommentEditedQueuesUpdated: an edit queues comment.updated with the same IDs and no text.
func TestCommentEditedQueuesUpdated(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.subscribeComments(t)
	c := fx.say(t, fx.annSess, fx.file, "draft text")
	seen := len(outboxOf(t, fx.s, fx.acc.ID))
	rec := callSession(t, fx.s, fx.annSess, "PATCH", "/api/v1/comments/"+c.ID, map[string]any{"body": "final words"})
	wantStatus(t, rec, 200)
	ev := wantOneEvent(t, fx, seen, "comment.updated")
	wantKeys(t, ev.Data, "comment", "reply_to", "node", "folder", "author", "mentions")
	if ev.Data["comment"] != c.ID || ev.Data["author"] != fx.ann.ID || ev.Data["mentions"] != float64(0) {
		t.Fatalf("edit payload = %v, want comment %s by %s", ev.Data, c.ID, fx.ann.ID)
	}
	noCommentText(t, ev.Raw, "final words", "draft text")
}

// TestCommentDeletedQueuesDeleted: a removed comment and a tombstone both queue comment.deleted, and the payload says
// which one it was.
func TestCommentDeletedQueuesDeleted(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.subscribeComments(t)
	leaf := fx.say(t, fx.annSess, fx.file, "leaf")
	seen := len(outboxOf(t, fx.s, fx.acc.ID))
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/comments/"+leaf.ID, nil), 204)
	ev := wantOneEvent(t, fx, seen, "comment.deleted")
	wantKeys(t, ev.Data, "comment", "reply_to", "node", "folder", "author", "mentions", "tombstone")
	if ev.Data["comment"] != leaf.ID || ev.Data["tombstone"] != false {
		t.Fatalf("leaf delete payload = %v, want comment %s, tombstone false", ev.Data, leaf.ID)
	}

	parent := fx.say(t, fx.annSess, fx.file, "parent with a reply")
	fx.reply(t, fx.annSess, fx.file, parent.ID, "kept reply")
	seen = len(outboxOf(t, fx.s, fx.acc.ID))
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/comments/"+parent.ID, nil), 204)
	ev = wantOneEvent(t, fx, seen, "comment.deleted")
	if ev.Data["comment"] != parent.ID || ev.Data["tombstone"] != true {
		t.Fatalf("tombstone delete payload = %v, want comment %s, tombstone true", ev.Data, parent.ID)
	}
}

// TestManagerDeleteNamesTheActor: a manager deletes another person's comment. The payload names the author and, as the
// actor, the manager who did it.
func TestManagerDeleteNamesTheActor(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.subscribeComments(t)
	dan, danSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "dan@example.com")
	fx.setPerm(t, fx.root, dan.ID, "manager")
	c := fx.say(t, fx.annSess, fx.file, "ann's words")
	seen := len(outboxOf(t, fx.s, fx.acc.ID))
	wantStatus(t, callSession(t, fx.s, danSess, "DELETE", "/api/v1/comments/"+c.ID, nil), 204)
	ev := wantOneEvent(t, fx, seen, "comment.deleted")
	wantKeys(t, ev.Data, "comment", "reply_to", "node", "folder", "author", "actor", "mentions", "tombstone")
	if ev.Data["author"] != fx.ann.ID || ev.Data["actor"] != dan.ID {
		t.Fatalf("payload author %v actor %v, want %s and %s", ev.Data["author"], ev.Data["actor"], fx.ann.ID, dan.ID)
	}
	noCommentText(t, ev.Raw, "ann's words", "dan@example.com")
}

// TestResolveThenReopenQueueTwoEvents: resolving queues comment.resolved, reopening queues comment.reopened, each once,
// and a resolve by someone other than the author names that person as the actor.
func TestResolveThenReopenQueueTwoEvents(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.subscribeComments(t)
	dan, danSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "dan@example.com")
	fx.setPerm(t, fx.root, dan.ID, "manager")
	c := fx.say(t, fx.annSess, fx.file, "to resolve")

	seen := len(outboxOf(t, fx.s, fx.acc.ID))
	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/comments/"+c.ID+"/resolve",
		map[string]any{"resolved": true}), 200)
	ev := wantOneEvent(t, fx, seen, "comment.resolved")
	wantKeys(t, ev.Data, "comment", "reply_to", "node", "folder", "author", "mentions")
	if ev.Data["comment"] != c.ID {
		t.Fatalf("resolved payload = %v, want comment %s", ev.Data, c.ID)
	}

	seen = len(outboxOf(t, fx.s, fx.acc.ID))
	wantStatus(t, callSession(t, fx.s, danSess, "POST", "/api/v1/comments/"+c.ID+"/resolve",
		map[string]any{"resolved": false}), 200)
	ev = wantOneEvent(t, fx, seen, "comment.reopened")
	if ev.Data["comment"] != c.ID || ev.Data["author"] != fx.ann.ID || ev.Data["actor"] != dan.ID {
		t.Fatalf("reopen payload = %v, want comment %s, author %s, actor %s", ev.Data, c.ID, fx.ann.ID, dan.ID)
	}
	noCommentText(t, ev.Raw, "to resolve")
}

// TestRefusedCommentActionsQueueNothing: an action the caller may not take, or one the request is wrong for, writes no
// event.
func TestRefusedCommentActionsQueueNothing(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.subscribeComments(t)
	dan, danSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "dan@example.com")
	fx.setPerm(t, fx.root, dan.ID, "manager")
	mine := fx.say(t, fx.annSess, fx.file, "ann's")
	theirs := fx.say(t, danSess, fx.file, "dan's")
	reply := fx.reply(t, fx.annSess, fx.file, mine.ID, "a reply")
	seen := len(outboxOf(t, fx.s, fx.acc.ID))

	refusals := []struct {
		name         string
		sess         testSession
		method, path string
		body         any
		status       int
	}{
		{"viewer comments", fx.bobSess, "POST", "/api/v1/nodes/" + fx.file + "/comments", map[string]any{"body": "x"}, 403},
		{"edit by a non-author", fx.annSess, "PATCH", "/api/v1/comments/" + theirs.ID, map[string]any{"body": "x"}, 403},
		{"delete by a commenter who is not the author", fx.annSess, "DELETE", "/api/v1/comments/" + theirs.ID, nil, 403},
		{"resolve a reply", fx.annSess, "POST", "/api/v1/comments/" + reply.ID + "/resolve", map[string]any{"resolved": true}, 400},
		{"blank body", fx.annSess, "POST", "/api/v1/nodes/" + fx.file + "/comments", map[string]any{"body": " \n "}, 400},
		{"reply to a reply", fx.annSess, "POST", "/api/v1/nodes/" + fx.file + "/comments",
			map[string]any{"body": "x", "parent_id": reply.ID}, 400},
	}
	for _, r := range refusals {
		rec := callSession(t, fx.s, r.sess, r.method, r.path, r.body)
		if rec.Code != r.status {
			t.Fatalf("%s: status %d, want %d", r.name, rec.Code, r.status)
		}
	}
	if evs := newCommentEvents(t, fx, seen); len(evs) != 0 {
		t.Fatalf("refused actions wrote %d events: %v", len(evs), evs)
	}
}

// TestCommentsSwitchOffQueuesNothing: with the account's comment switch off, every comment route is refused, and no
// event is written.
func TestCommentsSwitchOffQueuesNothing(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.subscribeComments(t)
	c := fx.say(t, fx.annSess, fx.file, "before the switch")
	fx.setCommentsPolicy(t, false)
	seen := len(outboxOf(t, fx.s, fx.acc.ID))
	refused := []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/v1/nodes/" + fx.file + "/comments", map[string]any{"body": "x"}},
		{"PATCH", "/api/v1/comments/" + c.ID, map[string]any{"body": "x"}},
		{"DELETE", "/api/v1/comments/" + c.ID, nil},
		{"POST", "/api/v1/comments/" + c.ID + "/resolve", map[string]any{"resolved": true}},
	}
	for _, r := range refused {
		rec := callSession(t, fx.s, fx.annSess, r.method, r.path, r.body)
		wantError(t, rec, http.StatusForbidden, "forbidden")
	}
	if evs := newCommentEvents(t, fx, seen); len(evs) != 0 {
		t.Fatalf("comments switched off wrote %d events: %v", len(evs), evs)
	}
}

// TestCommentEventsReachOnlySubscribers: an endpoint that takes comment.created gets the delivery; one that takes only
// file.created gets none for a comment.
func TestCommentEventsReachOnlySubscribers(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.subscribeComments(t)
	takes := fx.commentEndpoint(t, "comment.created")
	files := fx.commentEndpoint(t, "file.created")
	fx.say(t, fx.annSess, fx.file, "delivered")
	if n := fx.deliveries(t, takes); n != 1 {
		t.Fatalf("deliveries to the comment endpoint = %d, want 1", n)
	}
	if n := fx.deliveries(t, files); n != 0 {
		t.Fatalf("deliveries to the file-only endpoint = %d, want 0", n)
	}
}

// TestCommentEventNamesAreAccepted: the five comment events are subscribable by name, and an unknown name is still
// refused.
func TestCommentEventNamesAreAccepted(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.subscribeComments(t)
	fx.commentEndpoint(t, "comment.created", "comment.updated", "comment.deleted", "comment.resolved", "comment.reopened")
	rec := call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/webhooks", fx.key,
		map[string]any{"url": "https://hooks.example.com/in", "events": []string{"comment.archived"}})
	wantStatus(t, rec, 400)
}
