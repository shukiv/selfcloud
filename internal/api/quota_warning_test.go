// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http/httptest"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/store"
)

// TestQuotaWarningSetAndReset: the account key sets the warning level, which the store keeps, and DELETE sets it back to
// the default of 80. Each change is audited with its number only.
func TestQuotaWarningSetAndReset(t *testing.T) {
	t.Parallel()
	fx := newWebhookFixture(t)
	path := "/api/v1/accounts/" + fx.acc.ID + "/quota-warning"

	rec := call(t, fx.s, "PUT", path, fx.key, map[string]any{"percent": 70})
	wantStatus(t, rec, 200)
	if got := decode[struct {
		Percent int64 `json:"percent"`
	}](t, rec); got.Percent != 70 {
		t.Fatalf("PUT answered %d, want 70", got.Percent)
	}
	if p, err := fx.s.store.QuotaWarningPercent(t.Context(), fx.acc.ID); err != nil || p != 70 {
		t.Fatalf("stored level %d (%v), want 70", p, err)
	}

	rec = call(t, fx.s, "DELETE", path, fx.key, nil)
	wantStatus(t, rec, 200)
	if got := decode[struct {
		Percent int64 `json:"percent"`
	}](t, rec); got.Percent != store.WebhookQuotaWarningDefault {
		t.Fatalf("DELETE answered %d, want the default %d", got.Percent, store.WebhookQuotaWarningDefault)
	}
	if p, err := fx.s.store.QuotaWarningPercent(t.Context(), fx.acc.ID); err != nil || p != store.WebhookQuotaWarningDefault {
		t.Fatalf("stored level %d (%v) after DELETE, want the default", p, err)
	}
}

// TestQuotaWarningPercentRange: a level is a whole number from 1 to 99. 100 is quota.exceeded and has no setting of its
// own, and a value that is not a whole number is refused. A refused change stores nothing.
func TestQuotaWarningPercentRange(t *testing.T) {
	t.Parallel()
	fx := newWebhookFixture(t)
	path := "/api/v1/accounts/" + fx.acc.ID + "/quota-warning"
	bad := []any{nil, 0, 100, -5, 150, 80.5, "80", true}
	for _, v := range bad {
		body := map[string]any{"percent": v}
		if v == nil {
			body = map[string]any{}
		}
		wantError(t, call(t, fx.s, "PUT", path, fx.key, body), 400, apierr.InvalidRequest)
	}
	if p, err := fx.s.store.QuotaWarningPercent(t.Context(), fx.acc.ID); err != nil || p != store.WebhookQuotaWarningDefault {
		t.Fatalf("a refused change stored %d (%v)", p, err)
	}
	wantStatus(t, call(t, fx.s, "PUT", path, fx.key, map[string]any{"percent": 1}), 200)
	wantStatus(t, call(t, fx.s, "PUT", path, fx.key, map[string]any{"percent": 99}), 200)
}

// TestQuotaWarningKeyOnly: the owner, an installation admin and a delegated token change nothing; another account's key
// finds nothing; no credential is 401. The level stays as it was.
func TestQuotaWarningKeyOnly(t *testing.T) {
	t.Parallel()
	fx := newWebhookFixture(t)
	path := "/api/v1/accounts/" + fx.acc.ID + "/quota-warning"
	body := map[string]any{"percent": 50}

	wantError(t, callSession(t, fx.s, fx.owner, "PUT", path, body), 403, apierr.Forbidden)
	wantError(t, callSession(t, fx.s, fx.owner, "DELETE", path, nil), 403, apierr.Forbidden)
	wantError(t, call(t, fx.s, "PUT", path, adminKey(t, fx.s), body), 403, apierr.Forbidden)

	f := newFolder(t, fx.s, fx.key, fx.acc.ID, "Mine")
	tok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{f.ID}})
	if rec := call(t, fx.s, "PUT", path, tok, body); rec.Code != 403 && rec.Code != 404 {
		t.Fatalf("a token reached the quota-warning level: %d %s", rec.Code, rec.Body)
	}

	admin := adminKey(t, fx.s)
	other, otherKey := newAccount(t, fx.s, admin, "Other")
	wantError(t, call(t, fx.s, "PUT", path, otherKey, body), 404, apierr.NotFound)
	wantError(t, call(t, fx.s, "DELETE", path, otherKey, nil), 404, apierr.NotFound)
	if p, err := fx.s.store.QuotaWarningPercent(t.Context(), other.ID); err != nil || p != store.WebhookQuotaWarningDefault {
		t.Fatalf("another account's level changed: %d (%v)", p, err)
	}

	wantError(t, call(t, fx.s, "PUT", path, "", body), 401, apierr.Unauthenticated)
	if p, err := fx.s.store.QuotaWarningPercent(t.Context(), fx.acc.ID); err != nil || p != store.WebhookQuotaWarningDefault {
		t.Fatalf("a refused change moved the level to %d (%v)", p, err)
	}
}

// TestQuotaWarningReadByKeyAndOwner: the account key and an owner read the level. A member is forbidden, a token is
// refused, another account's key finds nothing, and no credential is 401.
func TestQuotaWarningReadByKeyAndOwner(t *testing.T) {
	t.Parallel()
	fx := newWebhookFixture(t)
	path := "/api/v1/accounts/" + fx.acc.ID + "/quota-warning"
	wantStatus(t, call(t, fx.s, "PUT", path, fx.key, map[string]any{"percent": 65}), 200)

	for _, rec := range []*httptest.ResponseRecorder{
		call(t, fx.s, "GET", path, fx.key, nil),
		callSession(t, fx.s, fx.owner, "GET", path, nil),
	} {
		wantStatus(t, rec, 200)
		if got := decode[struct {
			Percent int64 `json:"percent"`
		}](t, rec); got.Percent != 65 {
			t.Fatalf("GET answered %d, want 65", got.Percent)
		}
	}

	// A member: created as a user of the account, then signed in the way ownerUser signs one in.
	created := call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users", fx.key,
		map[string]any{"email": "member@example.com", "role": "member", "verification": "link"})
	wantStatus(t, created, 201)
	mv := decode[userResponse](t, created)
	verified := call(t, fx.s, "POST", "/api/v1/auth/verify", "", map[string]any{"token": mv.Verification.Token, "password": testPassword})
	wantStatus(t, verified, 200)
	member := sessionFrom(t, fx.s, verified)
	// A member does not manage the account, so it gets the 404 every webhook route gives it (managedAccount), not 403.
	wantError(t, callSession(t, fx.s, member, "GET", path, nil), 404, apierr.NotFound)

	f := newFolder(t, fx.s, fx.key, fx.acc.ID, "Mine")
	tok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{f.ID}})
	if rec := call(t, fx.s, "GET", path, tok, nil); rec.Code != 403 && rec.Code != 404 {
		t.Fatalf("a token read the level: %d %s", rec.Code, rec.Body)
	}

	_, otherKey := newAccount(t, fx.s, adminKey(t, fx.s), "Other")
	wantError(t, call(t, fx.s, "GET", path, otherKey, nil), 404, apierr.NotFound)
	wantError(t, call(t, fx.s, "GET", path, "", nil), 401, apierr.Unauthenticated)
}
