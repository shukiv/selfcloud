// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// commentFixture is a team folder with a file, ann a commenter on its root, bob a viewer, and carol with no access.
type commentFixture struct {
	*sharingFixture
	root      string
	file      string
	carol     userResponse
	carolSess testSession
}

func newCommentFixture(t *testing.T) *commentFixture {
	t.Helper()
	sf := newSharingFixture(t)
	root := sf.rootOf(t, sf.fld.ID)
	sf.setPerm(t, root, sf.ann.ID, "commenter")
	sf.setPerm(t, root, sf.bob.ID, "viewer")
	sf.s.limits[commentWriteRate] = newLimiter(1000)
	carol, carolSess := verifiedUser(t, sf.s, sf.key, sf.acc.ID, "carol@example.com")
	return &commentFixture{sharingFixture: sf, root: root, file: sf.deep.ID, carol: carol, carolSess: carolSess}
}

// say posts a top-level comment as sess on nodeID and wants it created.
func (fx *commentFixture) say(t *testing.T, sess testSession, nodeID, body string) commentJSON {
	t.Helper()
	rec := callSession(t, fx.s, sess, "POST", "/api/v1/nodes/"+nodeID+"/comments", map[string]any{"body": body})
	wantStatus(t, rec, 201)
	return decode[commentJSON](t, rec)
}

// reply posts a reply to parent as sess and wants it created.
func (fx *commentFixture) reply(t *testing.T, sess testSession, nodeID, parent, body string) commentJSON {
	t.Helper()
	rec := callSession(t, fx.s, sess, "POST", "/api/v1/nodes/"+nodeID+"/comments",
		map[string]any{"body": body, "parent_id": parent})
	wantStatus(t, rec, 201)
	return decode[commentJSON](t, rec)
}

// dbCount runs a counting query on the store's file, as anyoneFixture does, and returns the count.
func (fx *commentFixture) dbCount(t *testing.T, query string, args ...any) int {
	t.Helper()
	db, err := sql.Open("sqlite", fx.s.cfg.Store.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// named sets a user's display name, as the account key.
func (fx *commentFixture) named(t *testing.T, usr userResponse, name string) {
	t.Helper()
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+usr.ID, fx.key,
		map[string]any{"display_name": name}), 200)
}

// listed returns the top-level comments of a node as sess sees them.
func (fx *commentFixture) listed(t *testing.T, sess testSession, nodeID string) []commentJSON {
	t.Helper()
	rec := callSession(t, fx.s, sess, "GET", "/api/v1/nodes/"+nodeID+"/comments", nil)
	wantStatus(t, rec, 200)
	return decode[listResponse[commentJSON]](t, rec).Items
}

// TestViewerReadsCommenterWrites: a viewer reads the thread and cannot write to it (the role is checked, and the refusal
// names the role); a commenter writes and reads its own comment back as mine.
func TestViewerReadsCommenterWrites(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	c := fx.say(t, fx.annSess, fx.file, "first")
	if !c.Mine {
		t.Fatalf("own comment is not mine: %+v", c)
	}
	if got := fx.listed(t, fx.bobSess, fx.file); len(got) != 1 || got[0].Body != "first" || got[0].Mine {
		t.Fatalf("viewer reads %+v, want the one comment, not mine", got)
	}
	rec := callSession(t, fx.s, fx.bobSess, "POST", "/api/v1/nodes/"+fx.file+"/comments", map[string]any{"body": "x"})
	e := wantError(t, rec, http.StatusForbidden, "forbidden")
	if e.Details["reason"] != "role" {
		t.Fatalf("viewer write refusal reason = %v, want role", e.Details["reason"])
	}
}

// TestCarolWithNoAccessGetsNothing: a user with no permission on the node gets not_found on every comment route, the
// same answer as for a node that does not exist.
func TestCarolWithNoAccessGetsNothing(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	c := fx.say(t, fx.annSess, fx.file, "secret talk")
	paths := []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/v1/nodes/" + fx.file + "/comments", nil},
		{"POST", "/api/v1/nodes/" + fx.file + "/comments", map[string]any{"body": "hi"}},
		{"PATCH", "/api/v1/comments/" + c.ID, map[string]any{"body": "hi"}},
		{"DELETE", "/api/v1/comments/" + c.ID, nil},
		{"POST", "/api/v1/comments/" + c.ID + "/resolve", map[string]any{"resolved": true}},
	}
	for _, p := range paths {
		rec := callSession(t, fx.s, fx.carolSess, p.method, p.path, p.body)
		wantError(t, rec, http.StatusNotFound, "not_found")
	}
	ghost := callSession(t, fx.s, fx.carolSess, "GET", "/api/v1/nodes/nod_"+strings.Repeat("a", 26)+"/comments", nil)
	wantError(t, ghost, http.StatusNotFound, "not_found")
	if strings.Contains(fx.listedRaw(t, fx.carolSess), "secret talk") {
		t.Fatal("carol read the comment text of a node she does not reach")
	}
}

// listedRaw is the body of a comment list as sess asks for it, for checks that look at the text.
func (fx *commentFixture) listedRaw(t *testing.T, sess testSession) string {
	t.Helper()
	return callSession(t, fx.s, sess, "GET", "/api/v1/nodes/"+fx.file+"/comments", nil).Body.String()
}

// TestKeyReadsButNeverWrites: an account key reads comments (it holds manager) and cannot comment, since a comment needs
// an author.
func TestKeyReadsButNeverWrites(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.say(t, fx.annSess, fx.file, "hello")
	rec := call(t, fx.s, "GET", "/api/v1/nodes/"+fx.file+"/comments", fx.key, nil)
	wantStatus(t, rec, 200)
	if n := len(decode[listResponse[commentJSON]](t, rec).Items); n != 1 {
		t.Fatalf("key reads %d comments, want 1", n)
	}
	wantError(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.file+"/comments", fx.key, map[string]any{"body": "x"}),
		http.StatusForbidden, "forbidden")
}

// TestReadTokenReadsWriteTokenComments: a read token reads and is refused on write; a write token comments as its user.
func TestReadTokenReadsWriteTokenComments(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	read := mintToken(t, fx.s, fx.key, fx.acc.ID, fx.fld.ID, fx.ann.ID)
	write := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}, "user": fx.ann.ID, "permissions": "write"})

	fx.say(t, fx.annSess, fx.file, "from the session")
	wantStatus(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.file+"/comments", read, nil), 200)
	wantError(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.file+"/comments", read, map[string]any{"body": "x"}),
		http.StatusForbidden, "forbidden")

	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.file+"/comments", write, map[string]any{"body": "by token"})
	wantStatus(t, rec, 201)
	got := decode[commentJSON](t, rec)
	if !got.Mine {
		t.Fatal("a write token's comment is not its user's")
	}
	// The author is the token's user: that user's own session sees the comment as mine.
	for _, c := range fx.listed(t, fx.annSess, fx.file) {
		if c.ID == got.ID && !c.Mine {
			t.Fatal("the token's comment is not mine to its user")
		}
	}
}

// TestTokenCannotEditDeleteOrResolve: an edit, a delete and a resolve are the signed-in user's own acts; a token of the
// author is refused on each, even with write.
func TestTokenCannotEditDeleteOrResolve(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	c := fx.say(t, fx.annSess, fx.file, "mine")
	write := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}, "user": fx.ann.ID, "permissions": "write"})
	wantError(t, call(t, fx.s, "PATCH", "/api/v1/comments/"+c.ID, write, map[string]any{"body": "x"}), http.StatusForbidden, "forbidden")
	wantError(t, call(t, fx.s, "DELETE", "/api/v1/comments/"+c.ID, write, nil), http.StatusForbidden, "forbidden")
	wantError(t, call(t, fx.s, "POST", "/api/v1/comments/"+c.ID+"/resolve", write, map[string]any{"resolved": true}),
		http.StatusForbidden, "forbidden")
}

// TestLinkVisitorReachesNoCommentRoute: a visitor of a link that anyone can open is refused by the route table on each
// comment route, before the node is looked at.
func TestLinkVisitorReachesNoCommentRoute(t *testing.T) {
	t.Parallel()
	fx := newAnyoneFixture(t)
	root := fx.rootOf(t, fx.fld.ID)
	_, secret := fx.makeAnyone(t, root, nil)
	visitor := fx.visitAnyone(t, secret, "")
	for _, m := range []string{"GET", "POST"} {
		rec := fx.visitor(t, visitor, m, "/api/v1/nodes/"+root+"/comments")
		e := wantError(t, rec, http.StatusForbidden, "forbidden")
		if e.Details["reason"] != "link_route" {
			t.Fatalf("%s by a link visitor: reason %v, want link_route", m, e.Details["reason"])
		}
	}
}

// TestRemovedPermissionStopsReadsAndWrites: a commenter whose permission is removed loses reads and writes at once.
func TestRemovedPermissionStopsReadsAndWrites(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.say(t, fx.annSess, fx.file, "before")
	fx.revoke(t, fx.root, fx.ann.ID)
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.file+"/comments", nil), http.StatusNotFound, "not_found")
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.file+"/comments", map[string]any{"body": "after"}),
		http.StatusNotFound, "not_found")
}

// TestRoleIsCheckedInsideTheWrite: a permission removed between the request's start and the write stops the write. The
// hook runs just before the write transaction, so no check before it can catch the removal: only the one inside the
// transaction does.
func TestRoleIsCheckedInsideTheWrite(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.s.beforeWrite = func() {
		fx.revoke(t, fx.root, fx.ann.ID)
	}
	t.Cleanup(func() { fx.s.beforeWrite = nil })
	rec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.file+"/comments", map[string]any{"body": "late"})
	wantError(t, rec, http.StatusNotFound, "not_found")
	if n := fx.dbCount(t, "SELECT COUNT(*) FROM comments WHERE node_id = ?", fx.file); n != 0 {
		t.Fatalf("a comment was stored after the permission went: %d rows", n)
	}
}

// TestDisabledUserCannotReadOrWrite: a disabled user's session stops at once. Its comments stay for the others.
func TestDisabledUserCannotReadOrWrite(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	c := fx.say(t, fx.annSess, fx.file, "stays")
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.ann.ID, fx.key,
		map[string]any{"status": "disabled"}), 200)
	rec := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.file+"/comments", nil)
	if rec.Code == http.StatusOK {
		t.Fatal("a disabled user read comments")
	}
	got := fx.listed(t, fx.bobSess, fx.file)
	if len(got) != 1 || got[0].ID != c.ID || got[0].AuthorName != "" {
		t.Fatalf("bob sees %+v, want the disabled user's comment still there", got)
	}
}

// TestTrashedNodeRefusesEveryCommentRoute: a trashed file answers not_found on every comment route, and its comments
// come back with a restore, unchanged.
func TestTrashedNodeRefusesEveryCommentRoute(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	c := fx.say(t, fx.annSess, fx.file, "kept")
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.file, fx.key, nil), 204)
	wantError(t, callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/nodes/"+fx.file+"/comments", nil), http.StatusNotFound, "not_found")
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.file+"/comments", map[string]any{"body": "x"}),
		http.StatusNotFound, "not_found")
	wantError(t, callSession(t, fx.s, fx.annSess, "PATCH", "/api/v1/comments/"+c.ID, map[string]any{"body": "x"}),
		http.StatusNotFound, "not_found")
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/comments/"+c.ID+"/resolve", map[string]any{"resolved": true}),
		http.StatusNotFound, "not_found")

	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.file+"/restore", fx.key, nil), 200)
	got := fx.listed(t, fx.bobSess, fx.file)
	if len(got) != 1 || got[0].Body != "kept" {
		t.Fatalf("after restore bob sees %+v, want the comment back unchanged", got)
	}
}

// TestCommentIDOfAnotherNodeIsNotFound: a comment names its own node. A comment on a node the caller does not reach
// answers not_found on every route that takes its ID, and a reply to it does too.
func TestCommentIDOfAnotherNodeIsNotFound(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	other := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/private")
	secret := fx.say(t, fx.annSess, fx.root, "root note")
	wantError(t, callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/nodes/"+other.ID+"/comments?parent="+secret.ID, nil),
		http.StatusNotFound, "not_found")
	wantError(t, callSession(t, fx.s, fx.carolSess, "POST", "/api/v1/nodes/"+other.ID+"/comments",
		map[string]any{"body": "x", "parent_id": secret.ID}), http.StatusNotFound, "not_found")
	wantError(t, callSession(t, fx.s, fx.carolSess, "PATCH", "/api/v1/comments/"+secret.ID, map[string]any{"body": "x"}),
		http.StatusNotFound, "not_found")
}

// TestCommentIDOfAnotherAccountIsNotFound: a comment of another account is not found, even with its right ID.
func TestCommentIDOfAnotherAccountIsNotFound(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	c := fx.say(t, fx.annSess, fx.file, "ours")
	other, otherKey := newAccount(t, fx.s, fx.admin, "Other")
	_, strangerSess := verifiedUser(t, fx.s, otherKey, other.ID, "zoe@example.com")
	wantError(t, callSession(t, fx.s, strangerSess, "DELETE", "/api/v1/comments/"+c.ID, nil), http.StatusNotFound, "not_found")
	wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.file+"/comments?parent="+c.ID, otherKey, nil), http.StatusNotFound, "not_found")
	// The store's own lookup is scoped to the account too, not only the walk after it.
	if _, err := fx.s.store.CommentIn(t.Context(), other.ID, c.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("store lookup of another account's comment: err %v, want not found", err)
	}
}

// TestShortcutHasNoComments: a shortcut answers not_found on the comment routes, as a link visitor sees it.
func TestShortcutHasNoComments(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.root+"/shortcuts", fx.key, map[string]any{"target": fx.file})
	wantStatus(t, rec, 201)
	sc := decode[nodeJSON](t, rec)
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+sc.ID+"/comments", nil), http.StatusNotFound, "not_found")
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+sc.ID+"/comments", map[string]any{"body": "x"}),
		http.StatusNotFound, "not_found")
}

// TestOnlyTheAuthorEdits: an edit is the author's alone, even for a commenter; the author's edit sets edited_at.
func TestOnlyTheAuthorEdits(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	c := fx.say(t, fx.annSess, fx.file, "draft")
	fx.setPerm(t, fx.root, fx.bob.ID, "commenter")
	rec := callSession(t, fx.s, fx.bobSess, "PATCH", "/api/v1/comments/"+c.ID, map[string]any{"body": "mine now"})
	if e := wantError(t, rec, http.StatusForbidden, "forbidden"); e.Details["reason"] != "author" {
		t.Fatalf("edit by a non-author: reason %v, want author", e.Details["reason"])
	}
	rec = callSession(t, fx.s, fx.annSess, "PATCH", "/api/v1/comments/"+c.ID, map[string]any{"body": "final"})
	wantStatus(t, rec, 200)
	got := decode[commentJSON](t, rec)
	if got.Body != "final" || got.EditedAt == nil {
		t.Fatalf("edit answer = %+v, want the new body and edited_at", got)
	}
}

// TestDeleteByAuthorOrManager: a commenter who is not the author cannot delete; the author can; a manager can.
func TestDeleteByAuthorOrManager(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	c := fx.say(t, fx.annSess, fx.file, "to go")
	fx.setPerm(t, fx.root, fx.bob.ID, "commenter")
	rec := callSession(t, fx.s, fx.bobSess, "DELETE", "/api/v1/comments/"+c.ID, nil)
	if e := wantError(t, rec, http.StatusForbidden, "forbidden"); e.Details["reason"] != "role" {
		t.Fatalf("delete by a commenter who is not the author: reason %v", e.Details["reason"])
	}
	manager, mgrSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "mia@example.com")
	fx.setPerm(t, fx.root, manager.ID, "manager")
	wantStatus(t, callSession(t, fx.s, mgrSess, "DELETE", "/api/v1/comments/"+c.ID, nil), 204)
	if n := fx.dbCount(t, "SELECT COUNT(*) FROM comments WHERE id = ?", c.ID); n != 0 {
		t.Fatalf("a childless comment left a row: %d", n)
	}
}

// TestDeleteKeepsRepliesAsATombstone: a comment with replies becomes a tombstone with no body and no author; the replies
// keep their thread. The tombstone goes when its last reply is deleted.
func TestDeleteKeepsRepliesAsATombstone(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	top := fx.say(t, fx.annSess, fx.file, "parent")
	r1 := fx.reply(t, fx.annSess, fx.file, top.ID, "one")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/comments/"+top.ID, nil), 204)

	got := fx.listed(t, fx.bobSess, fx.file)
	if len(got) != 1 || !got[0].Deleted || got[0].Body != "" || got[0].AuthorName != "" || got[0].AuthorGone {
		t.Fatalf("tombstone = %+v, want deleted with no body and no author", got)
	}
	if len(got[0].Replies) != 1 || got[0].Replies[0].ID != r1.ID {
		t.Fatalf("replies of the tombstone = %+v, want the reply still there", got[0].Replies)
	}
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.file+"/comments",
		map[string]any{"body": "late", "parent_id": top.ID}), http.StatusNotFound, "not_found")

	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/comments/"+r1.ID, nil), 204)
	if n := fx.dbCount(t, "SELECT COUNT(*) FROM comments WHERE node_id = ?", fx.file); n != 0 {
		t.Fatalf("the tombstone outlived its last reply: %d rows", n)
	}
}

// TestReplyRules: a reply goes on a top-level comment of the node. A reply to a reply is refused.
func TestReplyRules(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	top := fx.say(t, fx.annSess, fx.file, "top")
	r1 := fx.reply(t, fx.annSess, fx.file, top.ID, "one")
	rec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.file+"/comments",
		map[string]any{"body": "two", "parent_id": r1.ID})
	if e := wantError(t, rec, http.StatusBadRequest, "invalid_request"); e.Details["reason"] != "reply_to_reply" {
		t.Fatalf("reply to a reply: reason %v", e.Details["reason"])
	}
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.file+"/comments",
		map[string]any{"body": "x", "parent_id": "cmt_" + strings.Repeat("a", 26)}), http.StatusNotFound, "not_found")
}

// TestResolve: a commenter resolves a top-level comment, which names the resolver; only the top-level comment resolves;
// a resolve can be undone.
func TestResolve(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	top := fx.say(t, fx.annSess, fx.file, "question")
	r1 := fx.reply(t, fx.annSess, fx.file, top.ID, "answer")
	rec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/comments/"+top.ID+"/resolve", map[string]any{"resolved": true})
	wantStatus(t, rec, 200)
	if got := decode[commentJSON](t, rec); !got.Resolved || got.ResolvedAt == nil {
		t.Fatalf("resolved answer = %+v", got)
	}
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/comments/"+r1.ID+"/resolve", map[string]any{"resolved": true}),
		http.StatusBadRequest, "invalid_request")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/comments/"+top.ID+"/resolve", map[string]any{"resolved": false}), 200)
	if got := fx.listed(t, fx.annSess, fx.file); got[0].Resolved {
		t.Fatal("a reopened comment still shows resolved")
	}
}

// TestPagingTopLevelAndReplies: top-level comments page newest first. A thread shows its first 50 replies, says more
// exist, and the rest come from ?parent= a page at a time, oldest first.
func TestPagingTopLevelAndReplies(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	var tops []commentJSON
	for i := 0; i < 3; i++ {
		tops = append(tops, fx.say(t, fx.annSess, fx.file, "top "+strconv.Itoa(i)))
	}
	page1 := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.file+"/comments?limit=2", nil)
	wantStatus(t, page1, 200)
	p1 := decode[listResponse[commentJSON]](t, page1)
	if len(p1.Items) != 2 || p1.Items[0].ID != tops[2].ID || p1.NextCursor == "" {
		t.Fatalf("page 1 = %d items, next %q; want the newest two and a cursor", len(p1.Items), p1.NextCursor)
	}
	page2 := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.file+"/comments?limit=2&cursor="+p1.NextCursor, nil)
	p2 := decode[listResponse[commentJSON]](t, page2)
	if len(p2.Items) != 1 || p2.Items[0].ID != tops[0].ID || p2.NextCursor != "" {
		t.Fatalf("page 2 = %+v; want the oldest one and no cursor", p2.Items)
	}

	thread := fx.say(t, fx.annSess, fx.file, "busy")
	for i := 0; i < commentRepliesShown+5; i++ {
		fx.reply(t, fx.annSess, fx.file, thread.ID, "r"+strconv.Itoa(i))
	}
	wantStatus(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.file+"/comments?limit=500", nil), 200)
	for _, c := range fx.listed(t, fx.annSess, fx.file) {
		if c.ID != thread.ID {
			continue
		}
		if c.ReplyCount != commentRepliesShown+5 || len(c.Replies) != commentRepliesShown || !c.RepliesMore {
			t.Fatalf("thread: count %d, shown %d, more %v; want %d, %d, true", c.ReplyCount, len(c.Replies), c.RepliesMore,
				commentRepliesShown+5, commentRepliesShown)
		}
	}
	rest := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.file+"/comments?parent="+thread.ID+"&limit=500", nil)
	wantStatus(t, rest, 200)
	all := decode[listResponse[commentJSON]](t, rest)
	if len(all.Items) != commentRepliesShown+5 || all.Items[0].Body != "r0" {
		t.Fatalf("?parent= lists %d replies, first %q; want all of them, oldest first", len(all.Items), all.Items[0].Body)
	}
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.file+"/comments?limit=501", nil),
		http.StatusBadRequest, "invalid_request")
}

// TestBodyRules: a body is plain text of 1 to 2,000 characters, in NFC, with no control or bidi control character. It is
// refused, not trimmed. Markup is stored as the text it is.
func TestBodyRules(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	bad := map[string]string{
		"empty":              "",
		"blank":              "   ",
		"blank line only":    "\n\n",
		"zero-width only":    "\u200B\u200C\uFEFF",
		"braille blank":      "⠀⠀",
		"hangul filler":      "ㅤ",
		"tab":                "a\tb",
		"control":            "a\x01b",
		"bidi override":      "abc‮evil",
		"bidi isolate":       "x⁦y⁩",
		"arabic letter mark": "a؜b",
		"too long":           strings.Repeat("a", maxCommentChars+1),
		"too many lines":     strings.Repeat("a\n", maxCommentLines) + "a",
		"NUL":                "a\x00b",
	}
	for name, body := range bad {
		rec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.file+"/comments", map[string]any{"body": body})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", name, rec.Code, rec.Body.String())
		}
	}
	// A line break is kept; CR and CRLF become LF, so a body has one kind of break.
	if got := fx.say(t, fx.annSess, fx.file, "two\nlines"); got.Body != "two\nlines" {
		t.Fatalf("LF body stored as %q", got.Body)
	}
	if got := fx.say(t, fx.annSess, fx.file, "a\r\nb\rc"); got.Body != "a\nb\nc" {
		t.Fatalf("CR and CRLF stored as %q, want LF", got.Body)
	}
	if got := fx.say(t, fx.annSess, fx.file, strings.Repeat("a\n", maxCommentLines-1)+"a"); got.Body == "" {
		t.Fatal("a body of exactly the line limit was refused")
	}
	longest := strings.Repeat("é", maxCommentChars)
	if got := fx.say(t, fx.annSess, fx.file, longest); got.Body != norm.NFC.String(longest) {
		t.Fatal("a body of exactly 2,000 characters was changed")
	}
	decomposed := "é"
	if got := fx.say(t, fx.annSess, fx.file, decomposed); got.Body != "é" {
		t.Fatalf("decomposed body stored as %q, want NFC", got.Body)
	}
	markup := `<img src=x onerror="alert(1)"><b>bold</b>`
	if got := fx.say(t, fx.annSess, fx.file, markup); got.Body != markup {
		t.Fatalf("markup stored as %q, want the text as typed", got.Body)
	}
}

// FuzzCleanCommentBody: whatever is typed, an accepted body is valid UTF-8 in NFC, at most maxCommentChars characters
// and maxCommentLines lines, not blank, and holds no character other than a line feed that is a control, a bidi control,
// or U+061C. A refused one is refused with invalid_request.
func FuzzCleanCommentBody(f *testing.F) {
	for _, seed := range []string{"hello", "é", "‮", "a\x00", "\xff", strings.Repeat("x", 2001), "  ", "ok\nno", "a\r\nb", "​", "a؜"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got, err := cleanCommentBody(s)
		if err != nil {
			return
		}
		if !utf8.ValidString(got) || got != norm.NFC.String(got) {
			t.Fatalf("accepted %q, not valid NFC", got)
		}
		if utf8.RuneCountInString(got) > maxCommentChars || strings.Count(got, "\n")+1 > maxCommentLines {
			t.Fatalf("accepted %q, too long", got)
		}
		if blankText(got) {
			t.Fatalf("accepted blank %q", got)
		}
		if strings.ContainsAny(got, "\r") {
			t.Fatalf("accepted a carriage return in %q", got)
		}
		for _, r := range got {
			if r == '\n' {
				continue
			}
			if isBidiControl(r) || r == 0x061C || unicode.IsControl(r) {
				t.Fatalf("accepted %q with control %U", got, r)
			}
		}
	})
}

// TestCommentLimitPerNode: a node holds 5,000 live comments. The next write is refused with conflict and reason limit,
// and a delete makes room. The 5,000 are stored directly, since writing them one request at a time is not the rule under
// test.
func TestCommentLimitPerNode(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	seedLiveComments(t, fx, fx.file, maxCommentsPerNode, fx.ann.ID)
	rec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.file+"/comments", map[string]any{"body": "one too many"})
	if e := wantError(t, rec, http.StatusConflict, "conflict"); e.Details["reason"] != "limit" {
		t.Fatalf("over the cap: reason %v, want limit", e.Details["reason"])
	}
	var first string
	db, err := sql.Open("sqlite", fx.s.cfg.Store.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.QueryRow("SELECT id FROM comments WHERE node_id = ? LIMIT 1", fx.file).Scan(&first); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/comments/"+first, nil), 204)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.file+"/comments", map[string]any{"body": "room now"}), 201)
}

// seedLiveComments stores n top-level comments on a node by the store, as author.
func seedLiveComments(t *testing.T, fx *commentFixture, nodeID string, n int, author string) {
	t.Helper()
	ctx := t.Context()
	if err := fx.s.store.Write(ctx, func(tx *store.Tx) error {
		for i := 0; i < n; i++ {
			c := &store.Comment{ID: id.New("cmt"), AccountID: fx.acc.ID, NodeID: nodeID, AuthorID: author,
				Body: "seeded " + strconv.Itoa(i), CreatedAt: store.Now()}
			if err := tx.InsertComment(ctx, c); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestCommentWriteRateLimit: a user writes at most as many comments a minute as the limiter allows, and the next one is
// rate limited.
func TestCommentWriteRateLimit(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.s.limits[commentWriteRate] = newLimiter(2)
	fx.say(t, fx.annSess, fx.file, "one")
	fx.say(t, fx.annSess, fx.file, "two")
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.file+"/comments", map[string]any{"body": "three"}),
		http.StatusTooManyRequests, "rate_limited")
}

// TestCommentsSwitchOff: the account's comment switch, off, refuses every comment route with reason comments_off, reads
// included, and keeps the comments; on again, they show.
func TestCommentsSwitchOff(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	c := fx.say(t, fx.annSess, fx.file, "kept")
	fx.setCommentsPolicy(t, false)
	for _, p := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/v1/nodes/" + fx.file + "/comments", nil},
		{"POST", "/api/v1/nodes/" + fx.file + "/comments", map[string]any{"body": "x"}},
		{"PATCH", "/api/v1/comments/" + c.ID, map[string]any{"body": "x"}},
		{"DELETE", "/api/v1/comments/" + c.ID, nil},
		{"POST", "/api/v1/comments/" + c.ID + "/resolve", map[string]any{"resolved": true}},
		{"GET", "/api/v1/nodes/" + fx.file + "/people?q=ann", nil},
	} {
		rec := callSession(t, fx.s, fx.annSess, p.method, p.path, p.body)
		if e := wantError(t, rec, http.StatusForbidden, "forbidden"); e.Details["reason"] != "comments_off" {
			t.Fatalf("%s %s with the switch off: reason %v", p.method, p.path, e.Details["reason"])
		}
	}
	fx.setCommentsPolicy(t, true)
	if got := fx.listed(t, fx.bobSess, fx.file); len(got) != 1 || got[0].ID != c.ID {
		t.Fatalf("after switching back on, bob sees %+v, want the kept comment", got)
	}
}

// setCommentsPolicy sets the account's comment switch as the account key, and checks the answer.
func (fx *commentFixture) setCommentsPolicy(t *testing.T, on bool) {
	t.Helper()
	rec := call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/sharing-policy", fx.key, map[string]any{"comments": on})
	wantStatus(t, rec, 200)
	if got := decode[sharingPolicyJSON](t, rec); got.Comments != on {
		t.Fatalf("switch answer comments=%v, want %v", got.Comments, on)
	}
}

// TestCommentsSwitchDefaultsOn: a new account has comments on, and the policy route says so.
func TestCommentsSwitchDefaultsOn(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	rec := call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/sharing-policy", fx.key, nil)
	wantStatus(t, rec, 200)
	if got := decode[sharingPolicyJSON](t, rec); !got.Comments {
		t.Fatal("a new account has comments off")
	}
}

// TestCommentLifecycleCases: a move keeps comments; a copy does not copy them; a version restore keeps them; a purge
// removes them with their mentions; account deletion removes them.
func TestCommentLifecycleCases(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.say(t, fx.annSess, fx.file, "on the file")
	dir := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/dest")
	moved := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.file+"/move", fx.key, map[string]any{"folder": fx.fld.ID, "parent": dir.ID})
	wantStatus(t, moved, 200)
	if got := fx.listed(t, fx.annSess, fx.file); len(got) != 1 {
		t.Fatalf("after a move the file has %d comments, want 1", len(got))
	}

	copied := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.file+"/copy", fx.key, map[string]any{"name": "copy"})
	wantStatus(t, copied, 201)
	cp := decode[nodeJSON](t, copied)
	if got := fx.listed(t, fx.annSess, cp.ID); len(got) != 0 {
		t.Fatalf("a copy carries %d comments, want none", len(got))
	}
	if got := fx.listed(t, fx.annSess, fx.file); len(got) != 1 {
		t.Fatalf("the original lost its comments on a copy: %d", len(got))
	}

	// A new content write keeps a version of the old one; restoring it keeps the comments too.
	etag := decode[nodeJSON](t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.file, fx.key, nil)).ETag
	if etag == "" {
		t.Fatal("the node carries no ETag")
	}
	wantStatus(t, rawCall(t, fx.s, "PUT", "/api/v1/nodes/"+fx.file+"/content", fx.key, "new text", map[string]string{"If-Match": etag}), 200)
	versions := call(t, fx.s, "GET", "/api/v1/nodes/"+fx.file+"/versions", fx.key, nil)
	wantStatus(t, versions, 200)
	list := decode[struct {
		Items []versionJSON `json:"items"`
	}](t, versions)
	old := ""
	for _, v := range list.Items {
		if !v.Current {
			old = v.ID
		}
	}
	if old == "" {
		t.Fatal("no earlier version was kept")
	}
	cur := decode[nodeJSON](t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.file, fx.key, nil)).ETag
	wantStatus(t, rawCall(t, fx.s, "POST", "/api/v1/nodes/"+fx.file+"/versions/"+old+"/restore", fx.key, "", map[string]string{"If-Match": cur}), 200)
	if got := fx.listed(t, fx.annSess, fx.file); len(got) != 1 {
		t.Fatalf("after a version restore the file has %d comments, want 1", len(got))
	}

	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.file, fx.key, nil), 204)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/trash/"+fx.file, fx.key, nil), 204)
	if n := fx.dbCount(t, "SELECT COUNT(*) FROM comments WHERE node_id = ?", fx.file); n != 0 {
		t.Fatalf("a purged node kept %d comments", n)
	}
}

// TestDeletedAuthorShowsFormerMember: a deleted user's comments stay with their text, and the reader sees that the author
// is gone, with no name. Its mentions go with it.
func TestDeletedAuthorShowsFormerMember(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.named(t, fx.ann, "Ann Example")
	c := fx.say(t, fx.annSess, fx.file, "still here")
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.ann.ID, fx.key, nil), 204)
	got := fx.listed(t, fx.bobSess, fx.file)
	if len(got) != 1 || got[0].ID != c.ID || got[0].Body != "still here" || !got[0].AuthorGone || got[0].AuthorName != "" {
		t.Fatalf("after the author is deleted, bob sees %+v, want the text with author_gone and no name", got)
	}
}

// TestAccountDeletionRemovesComments: deleting an account removes its comments and mentions.
func TestAccountDeletionRemovesComments(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.say(t, fx.annSess, fx.file, "gone soon")
	if n := fx.dbCount(t, "SELECT COUNT(*) FROM comments WHERE account_id = ?", fx.acc.ID); n != 1 {
		t.Fatalf("before deletion: %d comments, want 1", n)
	}
	// The account's own deletion runs as a job later. The cascade under test is the row delete the job ends with.
	if err := fx.s.store.Write(t.Context(), func(tx *store.Tx) error {
		return tx.DeleteAccount(t.Context(), fx.acc.ID)
	}); err != nil {
		t.Fatal(err)
	}
	if n := fx.dbCount(t, "SELECT COUNT(*) FROM comments WHERE account_id = ?", fx.acc.ID); n != 0 {
		t.Fatalf("after deletion: %d comments", n)
	}
}

// TestCommentAuditHoldsIDsAndCountsOnly: a comment's audit entries name the node and carry IDs and counts. No text, and
// no name, is audited.
func TestCommentAuditHoldsIDsAndCountsOnly(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.named(t, fx.ann, "Ann Example")
	c := fx.say(t, fx.annSess, fx.file, "a private remark")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PATCH", "/api/v1/comments/"+c.ID, map[string]any{"body": "edited remark"}), 200)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/comments/"+c.ID, nil), 204)
	entries := fx.auditSeenBy(t, fx.ann.ID)
	var seen []string
	for _, e := range entries {
		if strings.HasPrefix(e.Action, "comment.") {
			seen = append(seen, e.Action)
			if e.Target != fx.file {
				t.Errorf("%s targets %q, want the node", e.Action, e.Target)
			}
			for _, bad := range []string{"private remark", "edited remark", "Ann Example"} {
				if strings.Contains(strings.Join(detailStrings(e.Details), " "), bad) {
					t.Errorf("%s audit carries text %q", e.Action, bad)
				}
			}
		}
	}
	sort.Strings(seen)
	if strings.Join(seen, ",") != "comment.create,comment.delete,comment.edit" {
		t.Fatalf("comment audit actions = %v", seen)
	}
}

// detailStrings returns the string values of an audit entry's details.
func detailStrings(m map[string]any) []string {
	var out []string
	for _, v := range m {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// TestCommentJSONCarriesNoUserID: a comment, a list of them and a suggestion never carry a user ID of the account.
func TestCommentJSONCarriesNoUserID(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.named(t, fx.ann, "Ann Example")
	fx.named(t, fx.bob, "Bob Example")
	fx.say(t, fx.annSess, fx.file, "no ids please")
	bodies := []string{
		fx.listedRaw(t, fx.bobSess),
		callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.file+"/people?q=Bob", nil).Body.String(),
	}
	for _, b := range bodies {
		for _, u := range []string{fx.ann.ID, fx.bob.ID, fx.carol.ID} {
			if strings.Contains(b, u) {
				t.Fatalf("a person reads the ID %s: %s", u, b)
			}
		}
	}
}

// TestEditDeleteAndResolveShareTheWriteLimit: create, edit, resolve and delete are one budget of comment writes a minute per
// user. With a budget of three, the fourth write is refused whichever route it is on.
func TestEditDeleteAndResolveShareTheWriteLimit(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.s.limits[commentWriteRate] = newLimiter(3)
	c := fx.say(t, fx.annSess, fx.file, "one")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PATCH", "/api/v1/comments/"+c.ID, map[string]any{"body": "two"}), 200)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/comments/"+c.ID+"/resolve", map[string]any{"resolved": true}), 200)
	wantError(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/comments/"+c.ID, nil), http.StatusTooManyRequests, "rate_limited")
}
