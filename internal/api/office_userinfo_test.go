// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/store"
)

// TestWOPIPutUserInfoIsReturnedToItsOwnerOnly: the string a person sends comes back in CheckFileInfo for that person, and
// never for another person who opens the same file.
func TestWOPIPutUserInfoIsReturnedToItsOwnerOnly(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	annTok := fx.openToken(t, "edit")
	fx.grant(t, fx.doc.ID, fx.bob.ID, "viewer")
	bobTok := decode[officeJSON](t, fx.open(t, fx.bobSess, fx.doc.ID, "view")).Token

	before := decodeInfo(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), annTok, nil, nil))
	if before["SupportsUserInfo"] != true {
		t.Errorf("SupportsUserInfo = %v, want true", before["SupportsUserInfo"])
	}
	if _, ok := before["UserInfo"]; ok {
		t.Errorf("UserInfo is present before anything was stored: %v", before["UserInfo"])
	}
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), annTok, strings.NewReader("ann-settings"),
		map[string]string{"X-WOPI-Override": "PUT_USER_INFO"}), 200)
	if got := decodeInfo(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), annTok, nil, nil))["UserInfo"]; got != "ann-settings" {
		t.Errorf("ann's UserInfo = %v, want ann-settings", got)
	}
	if got, ok := decodeInfo(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), bobTok, nil, nil))["UserInfo"]; ok {
		t.Errorf("bob's CheckFileInfo shows ann's UserInfo: %v", got)
	}
}

// TestWOPIPutUserInfoLimitAndEncoding: 1024 bytes are kept, more are refused, and the text must be UTF-8.
func TestWOPIPutUserInfoLimitAndEncoding(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tok := fx.openToken(t, "edit")
	hdr := map[string]string{"X-WOPI-Override": "PUT_USER_INFO"}
	full := strings.Repeat("a", 1024)
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader(full), hdr), 200)
	if got := decodeInfo(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), tok, nil, nil))["UserInfo"]; got != full {
		t.Errorf("a 1024-byte UserInfo was not kept whole (%d bytes back)", len(got.(string)))
	}
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader(full+"a"), hdr), 400)
	if got := decodeInfo(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), tok, nil, nil))["UserInfo"]; got != full {
		t.Error("a refused UserInfo replaced the stored one")
	}
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("\xff\xfe"), hdr), 400)
}

// TestWOPIPutUserInfoWorksOnAViewToken: the string is the person's own, so a read-only token may set it, and an empty
// body clears it.
func TestWOPIPutUserInfoWorksOnAViewToken(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.grant(t, fx.doc.ID, fx.ann.ID, "viewer")
	view := decode[officeJSON](t, fx.open(t, fx.annSess, fx.doc.ID, "view")).Token
	hdr := map[string]string{"X-WOPI-Override": "PUT_USER_INFO"}
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), view, strings.NewReader("viewed"), hdr), 200)
	if got := decodeInfo(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), view, nil, nil))["UserInfo"]; got != "viewed" {
		t.Errorf("UserInfo on a view token = %v, want viewed", got)
	}
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), view, strings.NewReader(""), hdr), 200)
	if _, ok := decodeInfo(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), view, nil, nil))["UserInfo"]; ok {
		t.Error("an empty PUT_USER_INFO did not clear the stored string")
	}
}

// TestWOPIUserInfoIsGoneWithTheUser: the user's UserInfo is deleted with the user, as the person's other data is.
func TestWOPIUserInfoIsGoneWithTheUser(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tok := fx.openToken(t, "edit")
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, strings.NewReader("gone soon"),
		map[string]string{"X-WOPI-Override": "PUT_USER_INFO"}), 200)
	ctx := context.Background()
	if info, err := fx.s.store.WOPIUserInfo(ctx, fx.ann.ID); err != nil || info != "gone soon" {
		t.Fatalf("the UserInfo is not stored: %q, %v", info, err)
	}
	// The user is removed through the API, as an account owner removes one.
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.ann.ID, fx.key, nil), 204)
	if _, err := fx.s.store.WOPIUserInfo(ctx, fx.ann.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the UserInfo of a deleted user is still there: %v", err)
	}
}
