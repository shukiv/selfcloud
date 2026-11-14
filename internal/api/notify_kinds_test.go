// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"fmt"
	"testing"
)

// The in-app notifications of a share and of an access request (decision 171). They are written in the transaction that
// writes the permission or the request, to the people the matching mail goes to by the same rule, never to the actor or
// the requester, and whether or not mail is configured. They queue no mail of their own. The kind strings are the wire
// values the store keeps, so the tests name them as they are stored.

// TestShareNotifiesTheSubjectNotTheActor: a user's share notifies the person it names, with the actor's and the item's
// names, and does not notify the actor about their own change. The share mail still goes, once, to the subject.
func TestShareNotifiesTheSubjectNotTheActor(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	dan, danSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "dan@example.com")
	fx.named(t, fx.carol, "Carol Example")
	fx.grant(t, fx.root, fx.carol.ID, "manager") // silent: the actor's own grant notifies no one
	fx.mark()
	wantStatus(t, callSession(t, fx.s, fx.carolSess, "PUT", "/api/v1/nodes/"+fx.file+"/permissions/"+dan.ID,
		map[string]any{"role": "viewer"}), 200)
	got := fx.inbox(t, danSess)
	if len(got) != 1 || got[0].Kind != "share" || got[0].ItemName != "file.txt" || got[0].ActorName != "Carol Example" {
		t.Fatalf("dan's inbox = %+v, want one share of file.txt by Carol", got)
	}
	if actor := fx.inbox(t, fx.carolSess); len(actor) != 0 {
		t.Fatalf("the actor's inbox = %+v, want none", actor)
	}
	if u := fx.unread(t, danSess); u.Count != 1 {
		t.Fatalf("dan's bell counts %d, want 1", u.Count)
	}
	msgs, _ := fx.newMessages(0)
	if len(msgs) != 1 || msgs[0].To != dan.Email {
		t.Fatalf("mail = %+v, want one to dan", msgs)
	}
}

// TestShareNotifiesWithoutMailConfigured: with no mail configured the share still notifies its subject in the app, and
// nothing is queued for mail.
func TestShareNotifiesWithoutMailConfigured(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	dan, danSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "dan@example.com")
	fx.grant(t, fx.root, fx.carol.ID, "manager")
	fx.s.mailer = nil
	wantStatus(t, callSession(t, fx.s, fx.carolSess, "PUT", "/api/v1/nodes/"+fx.file+"/permissions/"+dan.ID,
		map[string]any{"role": "viewer"}), 200)
	if got := fx.inbox(t, danSess); len(got) != 1 || got[0].Kind != "share" {
		t.Fatalf("dan's inbox without mail = %+v, want one share", got)
	}
	if n := fx.outboxCount(t, ""); n != 0 {
		t.Fatalf("mail rows without mail configured = %d, want 0", n)
	}
}

// TestShareWithNotifyOffNotifiesNoOne: a share made with notify off notifies no one in the app, as it mails no one.
func TestShareWithNotifyOffNotifiesNoOne(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	dan, danSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "dan@example.com")
	fx.grant(t, fx.root, fx.carol.ID, "manager")
	fx.mark()
	wantStatus(t, callSession(t, fx.s, fx.carolSess, "PUT", "/api/v1/nodes/"+fx.file+"/permissions/"+dan.ID,
		map[string]any{"role": "viewer", "notify": false}), 200)
	if got := fx.inbox(t, danSess); len(got) != 0 {
		t.Fatalf("dan's inbox after a silent share = %+v, want none", got)
	}
	if msgs, _ := fx.newMessages(0); len(msgs) != 0 {
		t.Fatalf("a silent share mailed %+v, want none", msgs)
	}
}

// TestShareNotifiesGroupMembersUpToCap: a share to a group notifies each active member, and not the actor.
func TestShareNotifiesGroupMembersUpToCap(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	dan, danSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "dan@example.com")
	eve, eveSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "eve@example.com")
	grp := fx.group(t, "team")
	fx.inGroup(t, grp, dan.ID, true)
	fx.inGroup(t, grp, eve.ID, true)
	fx.grant(t, fx.root, fx.carol.ID, "manager")
	fx.mark()
	wantStatus(t, callSession(t, fx.s, fx.carolSess, "PUT", "/api/v1/nodes/"+fx.file+"/permissions/"+grp,
		map[string]any{"role": "viewer"}), 200)
	for _, c := range []struct {
		sess testSession
		name string
	}{{danSess, "dan"}, {eveSess, "eve"}} {
		if got := fx.inbox(t, c.sess); len(got) != 1 || got[0].Kind != "share" {
			t.Fatalf("%s's inbox = %+v, want one share", c.name, got)
		}
	}
	if actor := fx.inbox(t, fx.carolSess); len(actor) != 0 {
		t.Fatalf("the actor's inbox = %+v, want none", actor)
	}
}

// TestShareNotifiesNoOneInAGroupOverTheCap: a group over maxGroupMail is a broadcast the mail rule refuses, so it
// notifies no one in the app either, and mails no one.
func TestShareNotifiesNoOneInAGroupOverTheCap(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	grp := fx.group(t, "everyone")
	for i := 0; i < 51; i++ {
		u := fx.activeUser(t, fmt.Sprintf("member%02d@example.com", i), "", "")
		fx.inGroup(t, grp, u.ID, true)
	}
	fx.grant(t, fx.root, fx.carol.ID, "manager")
	fx.mark()
	wantStatus(t, callSession(t, fx.s, fx.carolSess, "PUT", "/api/v1/nodes/"+fx.file+"/permissions/"+grp,
		map[string]any{"role": "viewer"}), 200)
	if n := fx.dbCount(t, "SELECT COUNT(*) FROM notifications WHERE kind = 'share'"); n != 0 {
		t.Fatalf("share notifications for an over-cap group = %d, want 0", n)
	}
	if msgs, _ := fx.newMessages(0); len(msgs) != 0 {
		t.Fatalf("mail for an over-cap group = %+v, want none", msgs)
	}
}

// TestAccessRequestNotifiesManagersNotRequester: an access request notifies the people its mail goes to (the space's
// managers), naming the requester and the item, and never the requester. The request mail still goes.
func TestAccessRequestNotifiesManagersNotRequester(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	dan, danSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "dan@example.com")
	eve, eveSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "eve@example.com")
	fx.named(t, eve, "Eve Example")
	fx.grant(t, fx.root, dan.ID, "manager")
	fx.mark()
	wantStatus(t, callSession(t, fx.s, eveSess, "POST", "/api/v1/nodes/"+fx.file+"/access-requests",
		map[string]any{"role": "editor", "message": "please"}), 202)
	got := fx.inbox(t, danSess)
	if len(got) != 1 || got[0].Kind != "access_request" || got[0].ItemName != "file.txt" || got[0].ActorName != "Eve Example" {
		t.Fatalf("dan's inbox = %+v, want one access_request of file.txt by Eve", got)
	}
	if req := fx.inbox(t, eveSess); len(req) != 0 {
		t.Fatalf("the requester's inbox = %+v, want none", req)
	}
	msgs, _ := fx.newMessages(0)
	if len(msgs) != 1 || msgs[0].To != dan.Email {
		t.Fatalf("mail = %+v, want one to dan", msgs)
	}
}

// TestAccessRequestNotifiesWithoutMailConfigured: with no mail configured the request still notifies the managers in the
// app, and nothing is queued for mail.
func TestAccessRequestNotifiesWithoutMailConfigured(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	dan, danSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "dan@example.com")
	_, eveSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "eve@example.com")
	fx.grant(t, fx.root, dan.ID, "manager")
	fx.s.mailer = nil
	wantStatus(t, callSession(t, fx.s, eveSess, "POST", "/api/v1/nodes/"+fx.file+"/access-requests",
		map[string]any{"role": "editor"}), 202)
	if got := fx.inbox(t, danSess); len(got) != 1 || got[0].Kind != "access_request" {
		t.Fatalf("dan's inbox without mail = %+v, want one access_request", got)
	}
	if n := fx.outboxCount(t, ""); n != 0 {
		t.Fatalf("mail rows without mail configured = %d, want 0", n)
	}
}

// TestCommentsOffHidesMentionsButNotSharesOrRequests: while the account's comments are off, a mention is hidden but a
// share and an access request still show, since they are not about comments.
func TestCommentsOffHidesMentionsButNotSharesOrRequests(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	dan, danSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "dan@example.com")
	_, eveSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "eve@example.com")
	fay, faySess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "fay@example.com")
	fx.named(t, dan, "Dan Example")
	dan.DisplayName = "Dan Example"
	fx.grant(t, fx.root, dan.ID, "viewer")  // silent, so dan's only notice is the mention
	fx.grant(t, fx.root, fay.ID, "manager") // silent; fay decides requests
	fx.grant(t, fx.root, fx.ann.ID, "manager")
	fx.mark()
	fx.mentionOf(t, fx.annSess, dan, "look here")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+fx.file+"/permissions/"+fx.carol.ID,
		map[string]any{"role": "viewer"}), 200)
	wantStatus(t, callSession(t, fx.s, eveSess, "POST", "/api/v1/nodes/"+fx.file+"/access-requests",
		map[string]any{"role": "editor"}), 202)
	fx.setComments(t, false)
	if got := fx.inbox(t, danSess); len(got) != 0 {
		t.Fatalf("dan's mention shows while comments are off: %+v", got)
	}
	if got := fx.inbox(t, fx.carolSess); len(got) != 1 || got[0].Kind != "share" {
		t.Fatalf("carol's share while comments are off = %+v, want one share", got)
	}
	if got := fx.inbox(t, faySess); len(got) != 1 || got[0].Kind != "access_request" {
		t.Fatalf("fay's request while comments are off = %+v, want one access_request", got)
	}
}

// TestShareNoticeGoneWhenReachLost: a share notice is kept but hidden once its reader no longer reaches the item.
func TestShareNoticeGoneWhenReachLost(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	dan, danSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "dan@example.com")
	fx.grant(t, fx.root, fx.carol.ID, "manager")
	wantStatus(t, callSession(t, fx.s, fx.carolSess, "PUT", "/api/v1/nodes/"+fx.file+"/permissions/"+dan.ID,
		map[string]any{"role": "viewer"}), 200)
	if got := fx.inbox(t, danSess); len(got) != 1 {
		t.Fatalf("dan's inbox = %+v, want the share", got)
	}
	wantStatus(t, callSession(t, fx.s, fx.carolSess, "DELETE", "/api/v1/nodes/"+fx.file+"/permissions/"+dan.ID, nil), 204)
	if got := fx.inbox(t, danSess); len(got) != 0 {
		t.Fatalf("dan sees %+v after losing reach, want none", got)
	}
	if u := fx.unread(t, danSess); u.Count != 0 {
		t.Fatalf("dan's bell counts %d after losing reach, want 0", u.Count)
	}
}

// TestRequestNoticeGoneWhenNoLongerManager: an access-request notice shows only while its reader manages the item. A
// reader who still reaches it at a lower role no longer sees it.
func TestRequestNoticeGoneWhenNoLongerManager(t *testing.T) {
	t.Parallel()
	fx := newNotifyFixture(t)
	dan, danSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "dan@example.com")
	_, eveSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "eve@example.com")
	fx.grant(t, fx.root, dan.ID, "manager")
	wantStatus(t, callSession(t, fx.s, eveSess, "POST", "/api/v1/nodes/"+fx.file+"/access-requests",
		map[string]any{"role": "editor"}), 202)
	if got := fx.inbox(t, danSess); len(got) != 1 {
		t.Fatalf("dan's inbox = %+v, want the request", got)
	}
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.root+"/permissions/"+dan.ID, fx.key, nil), 204)
	fx.grant(t, fx.root, dan.ID, "editor") // still reaches the item, but no longer manages it
	if got := fx.inbox(t, danSess); len(got) != 0 {
		t.Fatalf("dan sees %+v as an editor, want none (a request is a manager's)", got)
	}
}
