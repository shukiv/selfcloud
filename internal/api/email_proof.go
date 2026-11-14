// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/recovery"
	"github.com/shukiv/selfcloud/internal/store"
)

// emailProofTTL is how long a proof link works (decision 172).
const emailProofTTL = 24 * time.Hour

// emailProofJSON is the answer to a request for a proof mail: whether the address is already proved,
// and whether a mail was sent.
type emailProofJSON struct {
	Proved bool `json:"proved"`
	Sent   bool `json:"sent"`
}

// handleRequestEmailProof mails the signed-in user a link that proves control of the address in use.
// A proved user is told so at once and gets no mail. The request is a browser's: a key or a token
// does not reach it, and an impersonation is refused (decision 172).
func (s *Server) handleRequestEmailProof(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	ctx := r.Context()
	u, err := s.store.User(ctx, p.AccountID, p.UserID)
	if errors.Is(err, store.ErrNotFound) {
		return apierr.New(apierr.Unauthenticated, "the session has ended; sign in again")
	}
	if err != nil {
		return err
	}
	if u.EmailProved() {
		writeJSON(w, http.StatusOK, emailProofJSON{Proved: true})
		return nil
	}
	if err := s.rateLimit("email.proof", p.UserID); err != nil {
		return err
	}
	if s.mailer == nil {
		writeJSON(w, http.StatusAccepted, emailProofJSON{})
		return nil
	}
	if err := s.checkMailBudget(u.AccountID, u.Email); err != nil {
		return err
	}
	now := store.Now()
	secret, hash := auth.NewToken()
	proof := &store.EmailProof{ID: id.New("prf"), UserID: u.ID, EmailKey: u.EmailKey, TokenHash: hash,
		ExpiresAt: now.Add(emailProofTTL), CreatedAt: now}
	link := recovery.TokenLink(s.settings().Users.ProveURLTemplate, s.cfg.PublicURL, secret)
	// The attempt is written with the token, before the mail leaves, as deliver does (fu2, decision 74).
	// A new request voids the user's older proofs: one live link at a time.
	details := map[string]any{"purpose": mail.PurposeEmailProof, "method": methodEmail}
	attempt := s.auditEntry(r, u.AccountID, "verification.send", u.ID, details)
	attempt.Outcome = "attempted"
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.DeleteEmailProofs(ctx, u.ID, proof.ID); err != nil {
			return err
		}
		if err := tx.InsertEmailProof(ctx, proof); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, attempt)
	})
	if err != nil {
		return err
	}
	sent := true
	result := s.auditEntry(r, u.AccountID, "verification.send", u.ID, details)
	result.Outcome = "ok"
	if err := s.sendProof(ctx, u, link, proof.ExpiresAt); err != nil {
		sent = false
		s.log.Warn("cannot send mail", "request_id", requestID(r), "purpose", mail.PurposeEmailProof, "error", err)
		result.Outcome = "failed"
	}
	if err := s.store.Write(ctx, func(tx *store.Tx) error { return tx.InsertAudit(ctx, result) }); err != nil {
		s.log.Error("cannot audit the mail's result", "request_id", requestID(r), "error", err)
	}
	writeJSON(w, http.StatusAccepted, emailProofJSON{Sent: sent})
	return nil
}

// sendProof mails a proof link to u, in u's locale.
func (s *Server) sendProof(ctx context.Context, u *store.User, link string, expires time.Time) error {
	subject, body, err := s.templates.Render(mail.PurposeEmailProof, u.Locale, struct{ URL, ExpiresAt string }{
		URL:       link,
		ExpiresAt: expires.UTC().Format("2006-01-02 15:04 UTC"),
	})
	if err != nil {
		return err
	}
	return s.mailer.Send(ctx, mail.Message{To: u.Email, Subject: subject, Body: body})
}

// handleRedeemEmailProof uses a proof link. Every failure answers with the one message that covers
// them all, so a link reveals nothing about its history (decision 172). A proof of an address the
// user no longer holds proves nothing.
func (s *Server) handleRedeemEmailProof(w http.ResponseWriter, r *http.Request) error {
	if err := s.rateLimit("verify.ip", addressKey(r)); err != nil {
		return err
	}
	var in struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if !auth.ValidToken(in.Token) {
		return errBadLink
	}
	ctx := r.Context()
	now := time.Now()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		proof, err := tx.EmailProofByHash(ctx, auth.HashToken(in.Token))
		if errors.Is(err, store.ErrNotFound) {
			return errBadLink
		}
		if err != nil {
			return err
		}
		if proof.UsedAt != nil || !now.Before(proof.ExpiresAt) {
			return errBadLink
		}
		u, err := tx.UserByID(ctx, proof.UserID)
		if errors.Is(err, store.ErrNotFound) {
			return errBadLink
		}
		if err != nil {
			return err
		}
		if u.Status == store.UserDisabled {
			return apierr.New(apierr.UserDisabled, "the user is disabled")
		}
		if u.EmailKey != proof.EmailKey {
			return errBadLink
		}
		at := store.Now()
		if err := tx.UseEmailProof(ctx, proof.ID, at); errors.Is(err, store.ErrNotFound) {
			return errBadLink
		} else if err != nil {
			return err
		}
		if err := tx.SetEmailProof(ctx, u.ID, proof.EmailKey, at); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, u.AccountID, "user.email_proved", u.ID, nil))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
