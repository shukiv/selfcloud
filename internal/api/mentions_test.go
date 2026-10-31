// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
)

// suggest asks the people list for q on the node as sess, and wants it answered.
func (fx *commentFixture) suggest(t *testing.T, sess testSession, nodeID, q string) []personJSON {
	t.Helper()
	rec := callSession(t, fx.s, sess, "GET", "/api/v1/nodes/"+nodeID+"/people?q="+q, nil)
	wantStatus(t, rec, 200)
	return decode[listResponse[personJSON]](t, rec).Items
}

// TestMentionOfAReachingPersonIsNamed: a person found in the people list, who reaches the node, is named by the comment.
// The answer and the later reads show the name; the stored row is one mention.
func TestMentionOfAReachingPersonIsNamed(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.named(t, fx.bob, "Bob Example")
	items := fx.suggest(t, fx.annSess, fx.file, "Bob")
	if len(items) != 1 || items[0].Name != "Bob Example" || items[0].Ref == "" {
		t.Fatalf("suggestions for Bob = %+v, want Bob with a reference", items)
	}
	rec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.file+"/comments",
		map[string]any{"body": "@Bob please look", "mentions": []string{items[0].Ref}})
	wantStatus(t, rec, 201)
	got := decode[commentJSON](t, rec)
	if len(got.Mentions) != 1 || got.Mentions[0].Name != "Bob Example" {
		t.Fatalf("mentions in the answer = %+v, want Bob", got.Mentions)
	}
	if n := fx.dbCount(t, "SELECT COUNT(*) FROM mentions WHERE comment_id = ? AND user_id = ?", got.ID, fx.bob.ID); n != 1 {
		t.Fatalf("stored mention rows for Bob = %d, want 1", n)
	}
	for _, c := range fx.listed(t, fx.bobSess, fx.file) {
		if c.ID == got.ID && (len(c.Mentions) != 1 || c.Mentions[0].Name != "Bob Example") {
			t.Fatalf("bob's read of the mention = %+v", c.Mentions)
		}
	}
}

// TestMentionEnumerationGivesOneAnswer: a mention of a person who does not reach the node, a person who is disabled, a
// person who does not exist, and the author itself get the same answer: a saved comment with no mention in it, and the
// same status and body shape as each other. Only the ID and the time differ.
func TestMentionEnumerationGivesOneAnswer(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.named(t, fx.carol, "Carol Example")
	disabled, _ := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "dan@example.com")
	fx.setPerm(t, fx.root, disabled.ID, "viewer")
	fx.named(t, disabled, "Dan Example")
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+disabled.ID, fx.key,
		map[string]any{"status": "disabled"}), 200)

	refs := map[string]string{}
	for label, usr := range map[string]string{
		"no access":   fx.carol.ID,
		"disabled":    disabled.ID,
		"nonexistent": "usr_" + strings.Repeat("a", 26),
		"self":        fx.ann.ID,
	} {
		ref, err := fx.s.sealMention(fx.acc.ID, fx.file, usr)
		if err != nil {
			t.Fatal(err)
		}
		refs[label] = ref
	}

	answers := map[string]map[string]any{}
	for label, ref := range refs {
		rec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.file+"/comments",
			map[string]any{"body": "to " + label, "mentions": []string{ref}})
		wantStatus(t, rec, 201)
		var m map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		delete(m, "id")
		delete(m, "created_at")
		delete(m, "body")
		answers[label] = m
	}
	want := answers["no access"]
	for label, got := range answers {
		if !jsonEqual(got, want) {
			t.Errorf("answer for %s differs from answer for no access:\n  %v\n  %v", label, got, want)
		}
	}
	if n := fx.dbCount(t, "SELECT COUNT(*) FROM mentions WHERE account_id = ?", fx.acc.ID); n != 0 {
		t.Fatalf("%d mention rows were stored for people who may not be named", n)
	}
}

// jsonEqual compares two decoded answers by their JSON.
func jsonEqual(a, b map[string]any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// TestMentionDroppedWhenReachIsGoneInTheWrite: a person who reaches the node when the reference is opened, and loses it
// before the write, is dropped; the comment is still saved.
func TestMentionDroppedWhenReachIsGoneInTheWrite(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.named(t, fx.bob, "Bob Example")
	ref, err := fx.s.sealMention(fx.acc.ID, fx.file, fx.bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	fx.s.beforeWrite = func() { fx.revoke(t, fx.root, fx.bob.ID) }
	t.Cleanup(func() { fx.s.beforeWrite = nil })
	rec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.file+"/comments",
		map[string]any{"body": "too late", "mentions": []string{ref}})
	wantStatus(t, rec, 201)
	if got := decode[commentJSON](t, rec); len(got.Mentions) != 0 {
		t.Fatalf("a person who lost reach is named: %+v", got.Mentions)
	}
}

// TestForgedOrForeignMentionIsRefused: a reference that does not open for this node and account is invalid_request.
// That covers a made-up string, a user ID typed in place of a reference, a reference made for another node, and one made
// for another account.
func TestForgedOrForeignMentionIsRefused(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	other := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/other")
	otherRef, err := fx.s.sealMention(fx.acc.ID, other.ID, fx.bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	acc2, _ := newAccount(t, fx.s, fx.admin, "Second")
	foreignRef, err := fx.s.sealMention(acc2.ID, fx.file, fx.bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	for name, ref := range map[string]string{
		"made up":       "mnt_notareference",
		"user ID":       fx.bob.ID,
		"other node":    otherRef,
		"other account": foreignRef,
		"empty":         "",
	} {
		rec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.file+"/comments",
			map[string]any{"body": "x", "mentions": []string{ref}})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", name, rec.Code, rec.Body.String())
		}
	}
}

// TestMentionsPerCommentAreCapped: ten people may be named; eleven are refused before anything is looked up, and the same
// person given twice counts once.
func TestMentionsPerCommentAreCapped(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	var refs []string
	for i := 0; i < maxCommentMentions+1; i++ {
		ref, err := fx.s.sealMention(fx.acc.ID, fx.file, id.New("usr"))
		if err != nil {
			t.Fatal(err)
		}
		refs = append(refs, ref)
	}
	rec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.file+"/comments",
		map[string]any{"body": "eleven", "mentions": refs})
	if e := wantError(t, rec, http.StatusBadRequest, "invalid_request"); e.Details["reason"] != "mentions" {
		t.Fatalf("eleven mentions: reason %v", e.Details["reason"])
	}
	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.file+"/comments",
		map[string]any{"body": "ten", "mentions": refs[:maxCommentMentions]}), 201)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.file+"/comments",
		map[string]any{"body": "dup", "mentions": []string{refs[0], refs[0], refs[0]}}), 201)
}

// padAlphabet is a distinct 26-character suffix for each i, for an e-mail address.
func padAlphabet(i int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz234567"
	b := []byte(strings.Repeat("a", 26))
	for p := len(b) - 1; p >= 0 && i > 0; p-- {
		b[p] = alphabet[i%32]
		i /= 32
	}
	return string(b)
}

// TestPeopleListAnswersOnlyReachingUsers: the suggestions are active users who reach the node, at most five, matched by
// name. A user with no access, a disabled one and a user of another name are not listed.
func TestPeopleListAnswersOnlyReachingUsers(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	for _, n := range []string{"Pat One", "Pat Two", "Pat Three", "Pat Four", "Pat Five", "Pat Six"} {
		u, _ := verifiedUser(t, fx.s, fx.key, fx.acc.ID, strings.ToLower(strings.ReplaceAll(n, " ", ""))+"@example.com")
		fx.named(t, u, n)
		if n != "Pat Six" && n != "Pat Four" {
			fx.setPerm(t, fx.root, u.ID, "viewer")
		}
		if n == "Pat Five" {
			wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+u.ID, fx.key,
				map[string]any{"status": "disabled"}), 200)
		}
	}
	items := fx.suggest(t, fx.annSess, fx.file, "pat")
	var names []string
	for _, it := range items {
		names = append(names, it.Name)
	}
	if strings.Join(names, ",") != "Pat One,Pat Three,Pat Two" {
		t.Fatalf("suggestions = %v, want the three reaching active Pats", names)
	}
	if len(fx.suggest(t, fx.annSess, fx.file, "carol")) != 0 {
		t.Fatal("a user with no access to the node is suggested")
	}
}

// TestPeopleListCapsAtFive: a name shared by more reaching users than the cap answers five.
func TestPeopleListCapsAtFive(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	for i := 0; i < maxPeopleSuggestions+2; i++ {
		u, _ := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "sam"+padAlphabet(i)+"@example.com")
		fx.named(t, u, "Sam")
		fx.setPerm(t, fx.root, u.ID, "viewer")
	}
	if got := len(fx.suggest(t, fx.annSess, fx.file, "Sam")); got != maxPeopleSuggestions {
		t.Fatalf("suggestions = %d, want %d", got, maxPeopleSuggestions)
	}
}

// TestPeopleListGivesNothingToNonReachers: a caller with no access gets not_found; a viewer, who reaches the node but may
// not comment, gets forbidden with reason role; a token and an account key are refused.
func TestPeopleListGivesNothingToNonReachers(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	wantError(t, callSession(t, fx.s, fx.carolSess, "GET", "/api/v1/nodes/"+fx.file+"/people?q=ann", nil), http.StatusNotFound, "not_found")
	if e := wantError(t, callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/nodes/"+fx.file+"/people?q=ann", nil),
		http.StatusForbidden, "forbidden"); e.Details["reason"] != "role" {
		t.Fatalf("viewer asks for people: reason %v", e.Details["reason"])
	}
	wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.file+"/people?q=ann", fx.key, nil), http.StatusForbidden, "forbidden")
	// A read token, and a write token that acts for ann, are both refused: a token never asks for people.
	read := mintToken(t, fx.s, fx.key, fx.acc.ID, fx.fld.ID, fx.ann.ID)
	wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.file+"/people?q=ann", read, nil), http.StatusForbidden, "forbidden")
	write := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}, "user": fx.ann.ID, "permissions": "write"})
	wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.file+"/people?q=ann", write, nil), http.StatusForbidden, "forbidden")
}

// TestPeopleListQueryRules: the query is 2 to 100 characters, and its wildcards match nothing but themselves.
func TestPeopleListQueryRules(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.file+"/people?q=a", nil), http.StatusBadRequest, "invalid_request")
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.file+"/people?q="+strings.Repeat("a", 101), nil),
		http.StatusBadRequest, "invalid_request")
	if got := fx.suggest(t, fx.annSess, fx.file, "%25%25"); len(got) != 0 {
		t.Fatalf("a wildcard matched %d people", len(got))
	}
}

// TestPeopleListIsRateLimited: the people list is limited per user, and the next call is refused.
func TestPeopleListIsRateLimited(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.s.limits[peopleSearchRate] = newLimiter(2)
	fx.suggest(t, fx.annSess, fx.file, "ann")
	fx.suggest(t, fx.annSess, fx.file, "ann")
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.file+"/people?q=ann", nil),
		http.StatusTooManyRequests, "rate_limited")
}

// TestMentionReferencesAreNodeBound: a reference opens for its own node and account only, and it is never the user's ID.
func TestMentionReferencesAreNodeBound(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	ref, err := fx.s.sealMention(fx.acc.ID, fx.file, fx.bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ref, fx.bob.ID) || !strings.HasPrefix(ref, mentionRefPrefix) {
		t.Fatalf("reference %q shows the user ID or lacks its prefix", ref)
	}
	if u, ok := fx.s.openMention(fx.acc.ID, fx.file, ref); !ok || u != fx.bob.ID {
		t.Fatalf("own node: open = %q, %v", u, ok)
	}
	if _, ok := fx.s.openMention(fx.acc.ID, fx.root, ref); ok {
		t.Fatal("a reference for one node opened for another")
	}
	if _, ok := fx.s.openMention("acc_"+strings.Repeat("a", 26), fx.file, ref); ok {
		t.Fatal("a reference opened for another account")
	}
	if !id.Valid("usr", fx.bob.ID) {
		t.Fatal("test fixture user has no valid ID")
	}
}

// TestTwoReferencesToOnePersonMakeOneMention: two references to one person, from two people-list calls, name that person
// once. The answer is the same whether or not the person still reaches the node: the same status, no error, and one mention
// row when they reach it.
func TestTwoReferencesToOnePersonMakeOneMention(t *testing.T) {
	t.Parallel()
	fx := newCommentFixture(t)
	fx.named(t, fx.bob, "Bob Example")
	first := fx.suggest(t, fx.annSess, fx.file, "Bob")
	second := fx.suggest(t, fx.annSess, fx.file, "Bob")
	if len(first) != 1 || len(second) != 1 || first[0].Ref == second[0].Ref {
		t.Fatalf("two calls gave %+v and %+v, want two different references to Bob", first, second)
	}
	refs := []string{first[0].Ref, second[0].Ref}
	rec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.file+"/comments",
		map[string]any{"body": "to Bob, twice", "mentions": refs})
	wantStatus(t, rec, 201)
	got := decode[commentJSON](t, rec)
	if len(got.Mentions) != 1 || got.Mentions[0].Name != "Bob Example" {
		t.Fatalf("mentions = %+v, want Bob once", got.Mentions)
	}
	if n := fx.dbCount(t, "SELECT COUNT(*) FROM mentions WHERE comment_id = ?", got.ID); n != 1 {
		t.Fatalf("mention rows = %d, want 1", n)
	}
	fx.revoke(t, fx.root, fx.bob.ID)
	rec = callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.file+"/comments",
		map[string]any{"body": "to Bob, who lost access", "mentions": refs})
	wantStatus(t, rec, 201)
	if after := decode[commentJSON](t, rec); len(after.Mentions) != 0 {
		t.Fatalf("after losing access, mentions = %+v, want none", after.Mentions)
	}
}
