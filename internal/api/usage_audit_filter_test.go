// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"testing"

	"github.com/shukiv/selfcloud/internal/mail"
)

// TestUsageCountsPeopleByStatus checks that the usage answer counts the account's active and pending
// users, which the tenant overview shows.
func TestUsageCountsPeopleByStatus(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	usage := "/api/v1/accounts/" + acc.ID + "/usage"
	if got := decode[usageJSON](t, call(t, s, "GET", usage, key, nil)); got.UsersActive != 0 || got.UsersPending != 0 {
		t.Fatalf("a new account counts %d active and %d pending users", got.UsersActive, got.UsersPending)
	}
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	wantStatus(t, call(t, s, "POST", "/api/v1/accounts/"+acc.ID+"/users", key,
		map[string]any{"email": "bob@example.com", "verification": "email"}), 201)
	got := decode[usageJSON](t, call(t, s, "GET", usage, key, nil))
	if got.UsersActive != 1 || got.UsersPending != 1 {
		t.Errorf("usage counts %d active and %d pending, want 1 and 1", got.UsersActive, got.UsersPending)
	}
}

// TestAuditFilterByActionAndActor checks GET /accounts/{acc}/audit?action= and ?actor=: each returns only
// the entries with that action, or by that actor, and the paging still works under a filter.
func TestAuditFilterByActionAndActor(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	audit := "/api/v1/accounts/" + acc.ID + "/audit"
	newFolder(t, s, key, acc.ID, "One")
	newFolder(t, s, key, acc.ID, "Two")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")

	byAction := decode[listResponse[auditJSON]](t, call(t, s, "GET", audit+"?action=folder.create&limit=1", key, nil))
	if len(byAction.Items) != 1 || byAction.Items[0].Action != "folder.create" || byAction.NextCursor == "" {
		t.Fatalf("first page of action=folder.create = %+v", byAction)
	}
	next := decode[listResponse[auditJSON]](t, call(t, s, "GET", audit+"?action=folder.create&limit=1&cursor="+byAction.NextCursor, key, nil))
	if len(next.Items) != 1 || next.Items[0].Action != "folder.create" || next.Items[0].ID == byAction.Items[0].ID {
		t.Errorf("second page of action=folder.create = %+v", next)
	}
	all := decode[listResponse[auditJSON]](t, call(t, s, "GET", audit+"?action=folder.create", key, nil))
	if len(all.Items) != 2 {
		t.Errorf("action=folder.create has %d entries, want 2", len(all.Items))
	}

	everything := decode[listResponse[auditJSON]](t, call(t, s, "GET", audit, key, nil))
	if len(everything.Items) == 0 {
		t.Fatal("the account has no audit entries")
	}
	actor := everything.Items[0].Actor
	byActor := decode[listResponse[auditJSON]](t, call(t, s, "GET", audit+"?actor="+actor.ID, key, nil))
	if len(byActor.Items) == 0 {
		t.Fatalf("actor=%s matched nothing", actor.ID)
	}
	for _, e := range byActor.Items {
		if e.Actor.ID != actor.ID {
			t.Errorf("actor filter returned %+v", e)
		}
	}
	none := decode[listResponse[auditJSON]](t, call(t, s, "GET", audit+"?actor=usr_nobody", key, nil))
	if len(none.Items) != 0 {
		t.Errorf("an unknown actor matched %d entries", len(none.Items))
	}
}
