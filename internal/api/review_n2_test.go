// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// victimWithFiles makes a member whose My files holds one file, so deleting it must move or drop that file.
func victimWithFiles(t *testing.T, fx *sharingFixture, email string) member {
	t.Helper()
	m := newMember(t, fx, email)
	pf := fx.personalFolder(t, m.sess)
	putNode(t, fx.s, fx.key, pf, "/private.txt", "private")
	return m
}

// TestOwnerSessionCannotTakeADeletedUsersFiles: an owner session may move a deleted user's files only to an
// active member. An owner that names itself or another owner is refused. The account key is unrestricted.
func TestOwnerSessionCannotTakeADeletedUsersFiles(t *testing.T) {
	t.Parallel()
	del := func(fx *sharingFixture, victim member, to string, sess testSession, key bool) int {
		path := "/api/v1/accounts/" + fx.acc.ID + "/users/" + victim.user.ID + "?transfer_to=" + to
		if key {
			return call(t, fx.s, "DELETE", path, fx.key, nil).Code
		}
		return callSession(t, fx.s, sess, "DELETE", path, nil).Code
	}
	t.Run("owner to itself is refused", func(t *testing.T) {
		fx := newSharingFixture(t)
		wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.bob.ID, fx.key,
			map[string]any{"role": "owner"}), 200)
		victim := victimWithFiles(t, fx, "v1@example.com")
		if code := del(fx, victim, fx.bob.ID, fx.bobSess, false); code != http.StatusForbidden {
			t.Errorf("owner to itself = %d, want 403", code)
		}
	})
	t.Run("owner to another owner is refused", func(t *testing.T) {
		fx := newSharingFixture(t)
		wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.bob.ID, fx.key,
			map[string]any{"role": "owner"}), 200)
		carl := newMember(t, fx, "carl-owner@example.com")
		wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+carl.user.ID, fx.key,
			map[string]any{"role": "owner"}), 200)
		victim := victimWithFiles(t, fx, "v2@example.com")
		if code := del(fx, victim, carl.user.ID, fx.bobSess, false); code != http.StatusForbidden {
			t.Errorf("owner to another owner = %d, want 403", code)
		}
	})
	t.Run("owner to a member is allowed", func(t *testing.T) {
		fx := newSharingFixture(t)
		wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.bob.ID, fx.key,
			map[string]any{"role": "owner"}), 200)
		victim := victimWithFiles(t, fx, "v3@example.com")
		if code := del(fx, victim, fx.ann.ID, fx.bobSess, false); code != http.StatusNoContent {
			t.Errorf("owner to a member = %d, want 204", code)
		}
	})
	t.Run("key to an owner is allowed", func(t *testing.T) {
		fx := newSharingFixture(t)
		wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.bob.ID, fx.key,
			map[string]any{"role": "owner"}), 200)
		victim := victimWithFiles(t, fx, "v4@example.com")
		if code := del(fx, victim, fx.bob.ID, testSession{}, true); code != http.StatusNoContent {
			t.Errorf("key to an owner = %d, want 204", code)
		}
	})
}

// TestAccessRequestMailHasItsOwnBudget: a burst of access requests spends only the request buckets. After
// it, a verification mail still goes out, since the shared account and address buckets were not touched.
func TestAccessRequestMailHasItsOwnBudget(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	dan := newMember(t, fx, "dan-budget@example.com")
	fx.grant(t, fx.rootOf(t, fx.fld.ID), dan.user.ID, "manager")
	var nodes []nodeJSON
	for i := 0; i < 15; i++ {
		nodes = append(nodes, putNode(t, fx.s, fx.key, fx.fld.ID, "/n"+string(rune('a'+i))+".txt", "x"))
	}
	for _, n := range nodes {
		wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+n.ID+"/access-requests",
			map[string]any{"role": "viewer"}), 202)
		wantStatus(t, callSession(t, fx.s, fx.bobSess, "POST", "/api/v1/nodes/"+n.ID+"/access-requests",
			map[string]any{"role": "viewer"}), 202)
	}
	// The managers' address is capped at five request mails an hour, so at most five arrived.
	toDan := 0
	for _, m := range fx.sent(t) {
		if m.To == dan.user.Email {
			toDan++
		}
	}
	if toDan == 0 || toDan > 5 {
		t.Fatalf("request mails to the manager = %d, want between 1 and 5", toDan)
	}
	// A new user's verification mail still goes out after the burst.
	base := len(fx.sent(t))
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users", fx.key,
		map[string]any{"email": "newcomer@example.com", "verification": "email"}), 201)
	msgs := fx.sent(t)[base:]
	if len(msgs) == 0 || msgs[0].To != "newcomer@example.com" {
		t.Errorf("verification mail after the burst = %+v, want one to the newcomer", msgs)
	}
}

// TestApprovalRespectsMembersOnly: approving a request from an outsider below the root of a members-only
// space is refused with the same conflict a direct grant gets.
func TestApprovalRespectsMembersOnly(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.settings(t, fx.fld.ID, map[string]any{"members_only": true})
	carl := newMember(t, fx, "carl-mo@example.com")
	wantStatus(t, callSession(t, fx.s, carl.sess, "POST", "/api/v1/nodes/"+fx.deep.ID+"/access-requests",
		map[string]any{"role": "viewer"}), 202)
	reqs := decode[listResponse[accessRequestJSON]](t, call(t, fx.s, "GET", "/api/v1/access-requests", fx.key, nil)).Items
	if len(reqs) != 1 {
		t.Fatalf("pending requests = %d, want 1", len(reqs))
	}
	wantError(t, call(t, fx.s, "POST", "/api/v1/access-requests/"+reqs[0].ID+"/approve", fx.key, nil), 409, apierr.Conflict)
}

// TestCopyByContributorDropsEditorsCanShare: a contributor's copy of a flagged node is unflagged, since the
// caller does not manage the destination. A manager's copy keeps the flag.
func TestCopyByContributorDropsEditorsCanShare(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.flag(t, fx.deep.ID, map[string]any{"editors_can_share": true})
	fx.grant(t, fx.a.ID, fx.ann.ID, "contributor")

	rec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.deep.ID+"/copy", map[string]any{"parent": fx.c.ID, "on_conflict": "rename"})
	wantStatus(t, rec, 201)
	if got := decode[nodeJSON](t, rec); got.EditorsCanShare {
		t.Errorf("a contributor's copy kept editors_can_share")
	}

	rec = call(t, fx.s, "POST", "/api/v1/nodes/"+fx.deep.ID+"/copy", fx.key, map[string]any{"parent": fx.c.ID, "on_conflict": "rename"})
	wantStatus(t, rec, 201)
	if got := decode[nodeJSON](t, rec); !got.EditorsCanShare {
		t.Errorf("a manager's copy lost editors_can_share")
	}
}

// TestRepeatRequestKeepsItsRole: a repeat of a pending request collapses into it and keeps the role it was
// made with. To ask for more, the requester waits for a decision.
func TestRepeatRequestKeepsItsRole(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	ask := "/api/v1/nodes/" + fx.deep.ID + "/access-requests"
	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", ask, map[string]any{"role": "editor"}), 202)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", ask, map[string]any{"role": "viewer"}), 202)
	list := decode[listResponse[accessRequestJSON]](t, call(t, fx.s, "GET", ask, fx.key, nil)).Items
	if len(list) != 1 || list[0].Role != "editor" {
		t.Errorf("requests = %+v, want one pending request still at editor", list)
	}
}

// TestDecidedRoleIsStored: an approval stores the role granted, so a listing shows what was actually given,
// even when the approver granted less than the requester asked for.
func TestDecidedRoleIsStored(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	ask := "/api/v1/nodes/" + fx.deep.ID + "/access-requests"
	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", ask, map[string]any{"role": "editor"}), 202)
	reqs := decode[listResponse[accessRequestJSON]](t, call(t, fx.s, "GET", "/api/v1/access-requests", fx.key, nil)).Items
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/access-requests/"+reqs[0].ID+"/approve", fx.key,
		map[string]any{"role": "viewer"}), 200)
	got := decode[listResponse[accessRequestJSON]](t, call(t, fx.s, "GET", ask, fx.key, nil)).Items
	if len(got) != 1 || got[0].Status != store.RequestApproved || got[0].Role != "viewer" {
		t.Errorf("decided request = %+v, want approved at viewer", got)
	}
}

// TestDenialMailNamesNoItem: a denial mail says that an item was involved, and does not name it.
func TestDenialMailNamesNoItem(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	ask := "/api/v1/nodes/" + fx.deep.ID + "/access-requests"
	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", ask, map[string]any{"role": "viewer"}), 202)
	reqs := decode[listResponse[accessRequestJSON]](t, call(t, fx.s, "GET", "/api/v1/access-requests", fx.key, nil)).Items
	base := len(fx.sent(t))
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/access-requests/"+reqs[0].ID+"/deny", fx.key, nil), 200)
	msgs := fx.sent(t)[base:]
	if len(msgs) != 1 {
		t.Fatalf("denial mails = %d, want 1", len(msgs))
	}
	if strings.Contains(msgs[0].Subject+msgs[0].Body, "file.txt") {
		t.Errorf("denial mail names the item: %q / %q", msgs[0].Subject, msgs[0].Body)
	}
}

// TestFolderSettingsMissingAndUnreachableAlike: a folder the caller cannot reach answers exactly as a
// missing folder does.
func TestFolderSettingsMissingAndUnreachableAlike(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	body := map[string]any{"members_only": true}
	missing := decodeError(t, callSession(t, fx.s, fx.annSess, "PATCH", "/api/v1/folders/"+id.New("fld")+"/settings", body))
	unreachable := decodeError(t, callSession(t, fx.s, fx.annSess, "PATCH", "/api/v1/folders/"+fx.fld.ID+"/settings", body))
	if missing.Code != apierr.NotFound || unreachable.Code != apierr.NotFound {
		t.Fatalf("codes = %q and %q, want not_found for both", missing.Code, unreachable.Code)
	}
	if missing.Message != unreachable.Message {
		t.Errorf("messages differ: %q and %q", missing.Message, unreachable.Message)
	}
}

// TestAccessRequestListIsRateLimited: the pending list is limited per caller, so one caller cannot make the
// server scan the account's requests again and again.
func TestAccessRequestListIsRateLimited(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	for i := 0; i < 60; i++ {
		wantStatus(t, call(t, fx.s, "GET", "/api/v1/access-requests", fx.key, nil), 200)
	}
	wantError(t, call(t, fx.s, "GET", "/api/v1/access-requests", fx.key, nil), 429, apierr.RateLimited)
}

// TestCollapsedRequestWritesNoAuditRow: a repeat that collapses into a pending request writes no audit entry.
func TestCollapsedRequestWritesNoAuditRow(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	ask := "/api/v1/nodes/" + fx.deep.ID + "/access-requests"
	count := func() int {
		n := 0
		for _, e := range fx.auditSeenBy(t, "") {
			if e.Action == "access.request" {
				n++
			}
		}
		return n
	}
	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", ask, map[string]any{"role": "viewer"}), 202)
	if n := count(); n != 1 {
		t.Fatalf("audit rows after a new request = %d, want 1", n)
	}
	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", ask, map[string]any{"role": "viewer"}), 202)
	if n := count(); n != 1 {
		t.Errorf("audit rows after a collapsed repeat = %d, want still 1", n)
	}
}

// TestRerequestWithinADayIsRecordedWithoutMail: after a decision, the same user's new request on the same
// node is recorded, but managers are not mailed again.
func TestRerequestWithinADayIsRecordedWithoutMail(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	dan := newMember(t, fx, "dan-rerequest@example.com")
	fx.grant(t, fx.rootOf(t, fx.fld.ID), dan.user.ID, "manager")
	ask := "/api/v1/nodes/" + fx.deep.ID + "/access-requests"
	toDan := func() int {
		n := 0
		for _, m := range fx.sent(t) {
			if m.To == dan.user.Email {
				n++
			}
		}
		return n
	}
	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", ask, map[string]any{"role": "viewer"}), 202)
	first := toDan()
	reqs := decode[listResponse[accessRequestJSON]](t, call(t, fx.s, "GET", "/api/v1/access-requests", fx.key, nil)).Items
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/access-requests/"+reqs[0].ID+"/deny", fx.key, nil), 200)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", ask, map[string]any{"role": "viewer"}), 202)
	if n := len(decode[listResponse[accessRequestJSON]](t, call(t, fx.s, "GET", ask, fx.key, nil)).Items); n != 2 {
		t.Errorf("requests on the node = %d, want 2 (the decided one and the new one)", n)
	}
	if toDan() != first {
		t.Errorf("a re-request within a day mailed the managers again")
	}
}
