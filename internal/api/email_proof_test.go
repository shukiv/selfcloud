// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// proofAnswer is the answer to a request for a proof mail.
type proofAnswer struct {
	Proved bool `json:"proved"`
	Sent   bool `json:"sent"`
}

// meProofFields are the proof fields of GET /me.
type meProofFields struct {
	EmailProved            bool `json:"email_proved"`
	AccountExternalSharing bool `json:"account_external_sharing"`
}

// meProof reads the proof fields of /me.
func meProof(t *testing.T, s *Server, sess testSession) meProofFields {
	t.Helper()
	return decode[meProofFields](t, callSession(t, s, sess, "GET", "/api/v1/me", nil))
}

// proveByMail asks for a proof mail for sess and returns the token its link carries.
func proveByMail(t *testing.T, s *Server, sess testSession, rec *mail.Recorder) string {
	t.Helper()
	before := len(rec.Messages())
	r := callSession(t, s, sess, "POST", "/api/v1/me/email-proof", nil)
	wantStatus(t, r, 202)
	if ans := decode[proofAnswer](t, r); ans.Proved || !ans.Sent {
		t.Fatalf("proof request answered %+v", ans)
	}
	msgs := rec.Messages()
	if len(msgs) != before+1 {
		t.Fatalf("proof request sent %d mails, want one more", len(msgs)-before)
	}
	m := msgs[len(msgs)-1]
	if !strings.Contains(m.Body, "/prove#token=") {
		t.Fatalf("proof mail carries no prove link: %q", m.Body)
	}
	return tokenFromMail(t, m)
}

// redeemProof uses a proof token.
func redeemProof(t *testing.T, s *Server, token string) *httptest.ResponseRecorder {
	t.Helper()
	return call(t, s, "POST", "/api/v1/auth/email-proof", "", map[string]any{"token": token})
}

// TestEmailProofRequestAndRedeem: a user whose verification link was returned, not mailed, is not
// proved. A proof mail proves the address once its link is used, and a proved user is told so at once.
func TestEmailProofRequestAndRedeem(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	if me := meProof(t, s, sess); me.EmailProved {
		t.Fatal("a link verification proved the address")
	}
	token := proveByMail(t, s, sess, rec)
	if me := meProof(t, s, sess); me.EmailProved {
		t.Fatal("the address is proved before the link is used")
	}
	wantStatus(t, redeemProof(t, s, token), 204)
	if me := meProof(t, s, sess); !me.EmailProved {
		t.Fatal("the address is not proved after the link is used")
	}
	before := len(rec.Messages())
	r := callSession(t, s, sess, "POST", "/api/v1/me/email-proof", nil)
	wantStatus(t, r, 200)
	if ans := decode[proofAnswer](t, r); !ans.Proved || ans.Sent {
		t.Fatalf("a proved user's request answered %+v", ans)
	}
	if len(rec.Messages()) != before {
		t.Fatal("a proved user's request sent mail")
	}
}

// TestEmailProofVerifyMailProves: a verification whose link was mailed proves the address when it is
// used, since the mail reached the address it names.
func TestEmailProofVerifyMailProves(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	r := call(t, s, "POST", "/api/v1/accounts/"+acc.ID+"/users", key,
		map[string]any{"email": "ann@example.com", "verification": "email"})
	wantStatus(t, r, 201)
	token := tokenFromMail(t, rec.Messages()[0])
	r = call(t, s, "POST", "/api/v1/auth/verify", "", map[string]any{"token": token, "password": testPassword})
	wantStatus(t, r, 200)
	sess := sessionFrom(t, s, r)
	if me := meProof(t, s, sess); !me.EmailProved {
		t.Fatal("a mailed verification did not prove the address")
	}
}

// TestEmailProofResetMailProves: a reset link that was mailed proves the address when it is used.
func TestEmailProofResetMailProves(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	if me := meProof(t, s, sess); me.EmailProved {
		t.Fatal("a link verification proved the address")
	}
	wantStatus(t, call(t, s, "POST", "/api/v1/auth/password/forgot", "",
		map[string]any{"email": "ann@example.com", "account": acc.ID}), 202)
	s.bg.Wait()
	token := tokenFromMail(t, rec.Messages()[0])
	r := call(t, s, "POST", "/api/v1/auth/password/reset", "",
		map[string]any{"token": token, "password": testPassword})
	wantStatus(t, r, 200)
	sess = sessionFrom(t, s, r)
	if me := meProof(t, s, sess); !me.EmailProved {
		t.Fatal("a mailed reset did not prove the address")
	}
}

// TestEmailProofEmailChangeMailProves: a confirmation mailed to the new address proves that address
// when it is used.
func TestEmailProofEmailChangeMailProves(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	ann, _ := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	r := call(t, s, "PATCH", "/api/v1/accounts/"+acc.ID+"/users/"+ann.ID, key,
		map[string]any{"email": "new@example.com", "verification": "email"})
	wantStatus(t, r, 200)
	var token string
	for _, m := range rec.Messages() {
		if m.To == "new@example.com" {
			token = tokenFromMail(t, m)
		}
	}
	if token == "" {
		t.Fatal("no confirmation mail went to the new address")
	}
	wantStatus(t, call(t, s, "POST", "/api/v1/auth/email-change/confirm", "", map[string]any{"token": token}), 200)
	r = login(t, s, "new@example.com", testPassword, acc.ID)
	wantStatus(t, r, 200)
	sess := sessionFrom(t, s, r)
	if me := meProof(t, s, sess); !me.EmailProved {
		t.Fatal("a mailed confirmation did not prove the new address")
	}
}

// TestEmailProofChangedAddressVoidsProof: a proof names the address it was mailed to. Once the user
// moves to another address, an unused proof of the old one no longer works, and the user is unproved.
func TestEmailProofChangedAddressVoidsProof(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	ann, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	token := proveByMail(t, s, sess, rec)
	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+acc.ID+"/users/"+ann.ID, key,
		map[string]any{"email": "new@example.com", "verification": "none"}), 200)
	wantError(t, redeemProof(t, s, token), 400, apierr.InvalidRequest)
	r := login(t, s, "new@example.com", testPassword, acc.ID)
	wantStatus(t, r, 200)
	sess = sessionFrom(t, s, r)
	if me := meProof(t, s, sess); me.EmailProved {
		t.Fatal("the new address is proved without a proof of its own")
	}
}

// TestEmailProofNewRequestVoidsOld: only the newest proof mail of a user works.
func TestEmailProofNewRequestVoidsOld(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	first := proveByMail(t, s, sess, rec)
	second := proveByMail(t, s, sess, rec)
	wantError(t, redeemProof(t, s, first), 400, apierr.InvalidRequest)
	wantStatus(t, redeemProof(t, s, second), 204)
}

// TestEmailProofBadTokens: a proof link that is malformed, unknown, used or expired answers with the
// one message that covers them all.
func TestEmailProofBadTokens(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	ann, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	wantError(t, redeemProof(t, s, "short"), 400, apierr.InvalidRequest)
	unknown, _ := auth.NewToken()
	wantError(t, redeemProof(t, s, unknown), 400, apierr.InvalidRequest)
	token := proveByMail(t, s, sess, rec)
	wantStatus(t, redeemProof(t, s, token), 204)
	wantError(t, redeemProof(t, s, token), 400, apierr.InvalidRequest)
	// An expired proof: the row is written directly, since the API only makes live ones.
	expired, hash := auth.NewToken()
	err := s.store.Write(context.Background(), func(tx *store.Tx) error {
		return tx.InsertEmailProof(context.Background(), &store.EmailProof{ID: id.New("prf"), UserID: ann.ID,
			EmailKey: "ann@example.com", TokenHash: hash, ExpiresAt: store.Now().Add(-time.Hour), CreatedAt: store.Now()})
	})
	if err != nil {
		t.Fatal(err)
	}
	wantError(t, redeemProof(t, s, expired), 400, apierr.InvalidRequest)
}

// TestEmailProofNoMailer: with no mail configured the request is accepted, and says nothing was sent.
func TestEmailProofNoMailer(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, nil)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	r := callSession(t, s, sess, "POST", "/api/v1/me/email-proof", nil)
	wantStatus(t, r, 202)
	if ans := decode[proofAnswer](t, r); ans.Proved || ans.Sent {
		t.Fatalf("request without a mailer answered %+v", ans)
	}
}

// TestEmailProofRateLimited: a user may ask for a proof mail three times an hour.
func TestEmailProofRateLimited(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	for i := 0; i < 3; i++ {
		wantStatus(t, callSession(t, s, sess, "POST", "/api/v1/me/email-proof", nil), 202)
	}
	wantError(t, callSession(t, s, sess, "POST", "/api/v1/me/email-proof", nil), 429, apierr.RateLimited)
}

// TestEmailProofImpersonationRefused: an admin signed in as a member does not ask for proof mails.
func TestEmailProofImpersonationRefused(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	imp, _ := fx.signedInAsMember(t)
	e := wantError(t, callSession(t, fx.s, imp, "POST", "/api/v1/me/email-proof", nil), 403, apierr.Forbidden)
	if e.Details["reason"] != reasonImpersonation {
		t.Fatalf("reason = %v, want %s", e.Details["reason"], reasonImpersonation)
	}
}

// TestEmailProofSessionOnly: the request is a browser's; a key does not reach it.
func TestEmailProofSessionOnly(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	verifiedUser(t, s, key, acc.ID, "ann@example.com")
	wantError(t, call(t, s, "POST", "/api/v1/me/email-proof", key, nil), 401, apierr.Unauthenticated)
}

// TestMeExternalSharingField: /me carries the caller's proof and its account's external-sharing
// switch. An installation admin has neither.
func TestMeExternalSharingField(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	if me := meProof(t, s, sess); me.EmailProved || me.AccountExternalSharing {
		t.Fatalf("new member's fields = %+v", me)
	}
	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+acc.ID+"/sharing-policy", key,
		map[string]any{"external_sharing": true}), 200)
	if me := meProof(t, s, sess); !me.AccountExternalSharing {
		t.Fatal("the member's /me does not show the account's switch on")
	}
	_, adminSess := newAdminUser(t, s, admin, "admin@example.com")
	if me := meProof(t, s, adminSess); me.EmailProved || me.AccountExternalSharing {
		t.Fatalf("admin's fields = %+v", me)
	}
}

// TestEmailProofLinkVerificationDoesNotProve: a user created with verification "link" and verified
// with the returned token proved nothing — no mail went through Selfcloud (decision 187).
func TestEmailProofLinkVerificationDoesNotProve(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	// verifiedUser creates with verification "link" and redeems the returned token.
	_, sess := verifiedUser(t, s, key, acc.ID, "link@example.com")
	if me := meProof(t, s, sess); me.EmailProved {
		t.Fatal("a link-verified user reads as proved")
	}
}
