// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"strings"
	"testing"
)

// sec1 (decision 84): a name in a share mail goes through mail.Name, as the comment mail does, so a dotted
// name is not turned into a link by a mail client. The rule also strips bidi marks and line breaks, but the API refuses
// those in a name when it is written (invalid_name), so the reachable effect is the link one, and that is what is
// tested here.
func TestShareMailNamesUseTheMailNameRule(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.flag(t, fx.deep.ID, map[string]any{"name": "Plan example.com"})
	base := len(fx.sent(t))

	rec := call(t, fx.s, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/permissions/"+fx.bob.ID, fx.key,
		map[string]any{"role": "viewer"})
	if got := decode[setPermissionJSON](t, rec); !got.Notified {
		t.Fatalf("notified = false, reason %q", got.Reason)
	}
	msgs := fx.sent(t)[base:]
	if len(msgs) != 1 {
		t.Fatalf("mails = %d, want 1", len(msgs))
	}
	if !strings.Contains(msgs[0].Body, "example‌.com") {
		t.Errorf("the mail does not carry the item name with its dot made unlinkable: %q", msgs[0].Body)
	}
}

// sec1 (decision 84): the access-request mail to managers uses the same rule for the requester and the item.
func TestAccessRequestMailNamesUseTheMailNameRule(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	dan := newMember(t, fx, "dan@example.com")
	fx.grant(t, fx.rootOf(t, fx.fld.ID), dan.user.ID, "manager")
	fx.flag(t, fx.deep.ID, map[string]any{"name": "Plan example.com"})
	base := len(fx.sent(t))

	ask := "/api/v1/nodes/" + fx.deep.ID + "/access-requests"
	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", ask, map[string]any{"role": "editor"}), 202)
	msgs := fx.sent(t)[base:]
	if len(msgs) != 1 {
		t.Fatalf("mails to managers = %d, want 1", len(msgs))
	}
	if !strings.Contains(msgs[0].Body, "example‌.com") {
		t.Errorf("the access-request mail does not carry the item name made unlinkable: %q", msgs[0].Body)
	}
}
