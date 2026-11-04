// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// fu2 item 3: an installation admin signed in as a member of an account that is then deleted. Decision 41 ends the
// sign-ins-as of a disabled or reset user at once and audits each end under the admin; decision 48 keeps the record of
// a deletion at installation level and removes the account's own audit rows with it. This test pins what the admin's
// Go back answers in the two windows the deletion has: while the account is deleting (its rows are still there), and
// after the job has removed it (the impersonation's session row went with the member's rows, by cascade).

// Window 1: the account is deleting, the job has not run. Its sign-ins-as end with the deletion (fu2, decision 74), as a
// disabled user's do: the session row goes, the end is audited under the admin with the reason account_deleted, and Go
// back answers 401 with the session-ended message, the same as after any end. The admin's own session is untouched.
func TestDeletingAnAccountEndsItsSignInsAs(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	imp, par := fx.signedInAsMember(t)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/accounts/"+fx.acc.ID, fx.admin, nil), http.StatusAccepted)
	if _, err := fx.s.store.SessionByHash(context.Background(), auth.HashToken(imp.cookie.Value)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the sign-in-as's session while the account is deleting: %v, want it ended", err)
	}
	ends := auditActionsOf(t, fx.s, fx.acc.ID, "impersonation.end")
	if len(ends) != 1 || ends[0].Details["reason"] != "account_deleted" || ends[0].ActorID != fx.adminUser.ID {
		t.Fatalf("impersonation.end entries = %v, want one by the admin with reason account_deleted", ends)
	}
	if e := wantError(t, goBack(t, fx.s, imp, par), http.StatusUnauthorized, apierr.Unauthenticated); e.Message == "" {
		t.Error("the refusal has no message")
	}
	wantStatus(t, callSession(t, fx.s, fx.adminSess, "GET", "/api/v1/me", nil), http.StatusOK)
}

// Window 2: the job has removed the account. The sign-in-as's session went with the member's rows, so Go back has no
// session to restore from. It answers 401 with the session-ended message and clears the browser's two cookies: no
// 500, and no restore of a deleted account's session. The admin's own session is untouched by the deletion.
func TestGoBackAfterTheAccountIsDeletedAnswersCleanly(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	imp, par := fx.signedInAsMember(t)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/accounts/"+fx.acc.ID, fx.admin, nil), http.StatusAccepted)
	if err := fx.s.jobs.RunPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.s.store.SessionByHash(context.Background(), auth.HashToken(imp.cookie.Value)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the sign-in-as's session after the deletion: %v, want it gone", err)
	}

	rec := goBack(t, fx.s, imp, par)
	if e := wantError(t, rec, http.StatusUnauthorized, apierr.Unauthenticated); e.Message == "" {
		t.Error("the refusal has no message")
	}
	cleared := 0
	for _, c := range rec.Result().Cookies() {
		if (c.Name == fx.s.sessionCookie || c.Name == fx.s.returnCookieName()) && c.MaxAge < 0 {
			cleared++
		}
	}
	if cleared != 2 {
		t.Errorf("cookies cleared = %d, want the session and the parked admin cookie", cleared)
	}
	// The admin's own session is still live: the deletion did not touch it.
	wantStatus(t, callSession(t, fx.s, fx.adminSess, "GET", "/api/v1/me", nil), http.StatusOK)
}
