// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
)

// lifeRequest is a request whose context carries the principal, as the route's authentication leaves it.
func lifeRequest(p *auth.Principal) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	return r.WithContext(context.WithValue(r.Context(), ctxKey{}, &reqInfo{principal: p}))
}

// fu2 review fix 3 (decision 74). signedLinkLife takes the least of the token's, the visitor's and the impersonation's
// remaining life. These tests pin the token branch and the visitor branch: each fails when its branch is removed.
func TestSignedLinkLifeFollowsTheToken(t *testing.T) {
	t.Parallel()
	p := &auth.Principal{Kind: auth.KindUser, UserID: "usr_t", Token: &auth.TokenScope{ExpiresAt: time.Now().Add(2 * time.Minute)}}
	life, err := signedLinkLife(lifeRequest(p))
	if err != nil {
		t.Fatal(err)
	}
	if life > 2*time.Minute+time.Second || life < time.Minute {
		t.Errorf("a token with 2 minutes left gives a link life of %v, want about 2 minutes", life)
	}
	expired := &auth.Principal{Kind: auth.KindUser, UserID: "usr_t", Token: &auth.TokenScope{ExpiresAt: time.Now().Add(-time.Minute)}}
	_, err = signedLinkLife(lifeRequest(expired))
	if e := apierr.From(err); e == nil || e.Code != apierr.TokenExpired {
		t.Errorf("an expired token: %v, want token_expired", err)
	}
}

func TestSignedLinkLifeFollowsTheVisitorsCredential(t *testing.T) {
	t.Parallel()
	p := &auth.Principal{Kind: auth.KindShareLink, Link: &auth.LinkScope{ExpiresAt: time.Now().Add(2 * time.Minute)}}
	life, err := signedLinkLife(lifeRequest(p))
	if err != nil {
		t.Fatal(err)
	}
	if life > 2*time.Minute+time.Second || life < time.Minute {
		t.Errorf("a visitor whose credential has 2 minutes left gives a link life of %v, want about 2 minutes", life)
	}
	ended := &auth.Principal{Kind: auth.KindShareLink, Link: &auth.LinkScope{ExpiresAt: time.Now().Add(-time.Minute)}}
	_, err = signedLinkLife(lifeRequest(ended))
	if e := apierr.From(err); e == nil || e.Code != apierr.Unauthenticated {
		t.Errorf("a visitor whose credential has ended: %v, want unauthenticated", err)
	}
}

// A caller with neither a token nor a visitor credential is capped by nothing here.
func TestSignedLinkLifeIsUncappedForAPlainCaller(t *testing.T) {
	t.Parallel()
	life, err := signedLinkLife(lifeRequest(&auth.Principal{Kind: auth.KindUser, UserID: "usr_t"}))
	if err != nil || life <= 24*time.Hour {
		t.Errorf("a plain caller: life %v, err %v; want no cap", life, err)
	}
}
