// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"errors"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
)

// TestImpersonatedInboxRefused: while an admin is signed in as a user, the user's notifications are not read, marked
// read or changed, and a comment is not written in the user's name (decision 41). Each is refused with the reason.
func TestImpersonatedInboxRefused(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	imp, _ := fx.signedInAsMember(t)
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/notifications"},
		{"GET", "/api/v1/notifications/unread"},
		{"POST", "/api/v1/notifications/ntf_none/read"},
		{"GET", "/api/v1/notifications/settings"},
		{"PATCH", "/api/v1/notifications/settings"},
		{"POST", "/api/v1/nodes/nod_none/comments"},
		{"PATCH", "/api/v1/comments/cmt_none"},
		{"DELETE", "/api/v1/comments/cmt_none"},
		{"POST", "/api/v1/comments/cmt_none/resolve"},
	} {
		e := wantError(t, callSession(t, fx.s, imp, c.method, c.path, map[string]any{}), 403, apierr.Forbidden)
		if e.Details["reason"] != reasonImpersonation {
			t.Errorf("%s %s: reason %v, want %s", c.method, c.path, e.Details["reason"], reasonImpersonation)
		}
	}
}

// TestNotifyCallerRefusesImpersonation checks the handler's own guard, below the route table: an impersonated
// principal that reaches the notification handlers is refused there too.
func TestNotifyCallerRefusesImpersonation(t *testing.T) {
	t.Parallel()
	p := &auth.Principal{Kind: auth.KindUser, UserID: "usr_member", AccountID: "acc_x", Impersonator: "usr_admin"}
	err := ownNotifyCaller(p)
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Code != apierr.Forbidden {
		t.Fatalf("impersonated caller: got %v, want forbidden", err)
	}
	if ae.Details["reason"] != reasonImpersonation {
		t.Errorf("reason %v, want %s", ae.Details["reason"], reasonImpersonation)
	}
	if err := ownNotifyCaller(&auth.Principal{Kind: auth.KindUser, UserID: "usr_member", AccountID: "acc_x"}); err != nil {
		t.Errorf("the user's own caller refused: %v", err)
	}
}
