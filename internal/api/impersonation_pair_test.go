// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
)

// TestImpersonatedBrowserLoadsTheApp checks that an impersonated browser still gets the web interface's index and its
// files: the catch-all route is classified as allowed, so only the API refusals reach an impersonation.
func TestImpersonatedBrowserLoadsTheApp(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	imp, _ := fx.signedInAsMember(t)
	for _, path := range []string{"/", "/files", "/assets/app.js"} {
		rec := callSession(t, fx.s, imp, "GET", path, nil)
		if rec.Code == http.StatusForbidden {
			t.Errorf("GET %s while impersonating: 403 %s", path, rec.Body.String())
		}
	}
}

// TestGoBackWithAnotherParkedCookieEndsIt checks that Go back restores only the admin session the impersonation
// parked. A valid cookie that is not the parked one ends the impersonation and restores nothing.
func TestGoBackWithAnotherParkedCookieEndsIt(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	imp, _ := fx.signedInAsMember(t)
	other, _ := auth.NewToken()
	wantError(t, goBack(t, fx.s, imp, other), 401, apierr.Unauthenticated)
	wantError(t, callSession(t, fx.s, imp, "GET", "/api/v1/me", nil), 401, apierr.Unauthenticated)
	wantStatus(t, callSession(t, fx.s, fx.adminSess, "GET", "/api/v1/me", nil), 200)
}
