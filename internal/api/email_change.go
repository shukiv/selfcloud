// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// A change of a user's address (decision 89). An account key or an installation admin may ask for it, and an
// installation admin only in a session with its own password. An account owner and a member may not: an owner who
// could change a member's address could reset the member's password and read the member's files.

// reasonEmailCaller is the reason a caller that may not change an address is refused.
const reasonEmailCaller = "email_caller"

// userDetailResponse is a user as PATCH answers it: with the limit that applies, and the confirmation made for a change of
// address when one was (decision 89).
type userDetailResponse struct {
	userDetailJSON
	Verification *verificationJSON `json:"verification,omitempty"`
}

// emailRequest is a change of address the caller asks for, checked before anything is written.
type emailRequest struct {
	email  string // cleaned, as user create cleans it
	key    string // emailKey(email)
	method string // none, email or link
}

// emailEffect is what a change of address did inside its transaction. applied is set when the new address is in use
// at once, and oldAddress is then the one that is told. issued is the confirmation made for a pending change, and
// target is the user addressed to the new address, which deliver mails or answers with.
type emailEffect struct {
	applied    bool
	oldAddress string
	issued     *issuedToken
	target     *store.User
	// notice is the charge of the old address's notice, held until the transaction commits; a failure after the charge
	// refunds it.
	notice chargeHold
}

// checkEmailChange decides whether the caller may change the address of user usr of accID, and how it is proved, before
// anything is written. It returns nil when the body gives no email. An admin session's password is checked here, after
// the cheaper checks, so a refused request does not count against it.
func (s *Server) checkEmailChange(w http.ResponseWriter, r *http.Request, accID, usr string, email *string,
	method, adminPassword string) (*emailRequest, error) {
	if email == nil {
		if method != "" || adminPassword != "" {
			return nil, apierr.New(apierr.InvalidRequest, "verification and admin_password go with email")
		}
		return nil, nil
	}
	// The one guard for the caller's kind. An owner's session reaches it, since managedAccount lets an owner manage its
	// account, and it is refused here. A member is refused by managedAccount as not found. The checks for a delegated token
	// and for an impersonating session are defence in depth: the route refuses both today (managedAccount, impRefuse), and
	// these keep a route change from opening the change to them.
	p := principalOf(r)
	session := p != nil && p.Kind == auth.KindUser
	if p == nil || p.Token != nil || (p.Kind != auth.KindAccountKey && p.Kind != auth.KindAdminKey && !session) ||
		(session && p.Role != auth.RoleAdmin) {
		return nil, apierr.New(apierr.Forbidden, "only an account key or an installation admin may change a user's email").
			With("reason", reasonEmailCaller)
	}
	if session {
		if err := requireAdminSession(p); err != nil {
			return nil, err
		}
	}
	// An installation admin, by its key or its session, reaches every account. A changed address followed by a password
	// reset by mail is a way into the account, as a reset link is (decision 41), so admin.tenant_access gates it. An
	// account key is not gated: it may already mint sign-in links for its own users.
	if p.Kind == auth.KindAdminKey || session {
		if err := s.tenantAccessGate(); err != nil {
			return nil, err
		}
	}
	cleaned, err := cleanEmail(*email)
	if err != nil {
		return nil, err
	}
	if method == "" {
		method = methodNone
		if session {
			method = methodEmail
		}
	}
	// No target is given, so a link is never made for a pending owner here: linkAllowed needs the role, which the
	// link for an existing user does not have. An admin session therefore gets email only.
	if err := s.checkVerificationMethod(r, method, linkTarget{existing: true}); err != nil {
		return nil, err
	}
	if session {
		if err := s.confirmAdminPassword(w, r, p, sessionOf(r), wayEmailChange, usr, accID, adminPassword); err != nil {
			return nil, err
		}
	}
	return &emailRequest{email: cleaned, key: emailKey(cleaned), method: method}, nil
}

// changeEmail applies a checked change of address to u inside tx. The same address as now is a no-op. A taken address is
// a conflict. With verification none the new address is in use at once, and every token that names the old address stops
// working. With email or link it waits in users.pending_email until its confirmation link is used, and a newer request
// replaces it. The caller writes the rest of u, and reads the effect after the commit.
func (s *Server) changeEmail(ctx context.Context, r *http.Request, tx *store.Tx, u *store.User, req *emailRequest,
	now time.Time) (*emailEffect, error) {
	if req.key == u.EmailKey {
		return &emailEffect{}, nil
	}
	if _, err := tx.UserByEmail(ctx, u.AccountID, req.key); err == nil {
		return nil, apierr.New(apierr.Conflict, "a user with this email already exists in the account")
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if req.method == methodEmail {
		if err := s.checkMailBudget(u.AccountID, req.email); err != nil {
			return nil, err
		}
	}
	u.UpdatedAt = now
	if req.method == methodNone {
		old := u.Email
		u.Email, u.EmailKey, u.PendingEmail = req.email, req.key, ""
		// Create with none makes its user active and verified (users.go handleCreateUser). A none change of a user who never
		// verified does the same: the caller vouches for the address, as it may for create (decision 89).
		details := map[string]any{"email": u.Email, "verification": methodNone}
		if u.Status == store.UserPending {
			at := now
			u.Status, u.VerifiedAt = store.UserActive, &at
			details["activated"] = true
		}
		if err := writeEmailTx(ctx, tx, u); err != nil {
			return nil, err
		}
		if err := voidAddressTokens(ctx, tx, u.ID); err != nil {
			return nil, err
		}
		if err := tx.InsertAudit(ctx, s.auditEntry(r, u.AccountID, "user.email_changed", u.ID, details)); err != nil {
			return nil, err
		}
		// The notice is charged last, after the change's own writes. A refusal returns an error, so the transaction rolls
		// back and nothing applies (429, decision 89). A failure after this point gives the tokens back through the effect
		// (refund in handleUpdateUser). The notice is sent after the commit, without a second charge.
		var notice chargeHold
		if s.mailer != nil {
			h, err := s.reserveNotice(u.AccountID, old)
			if err != nil {
				return nil, err
			}
			notice = h
		}
		return &emailEffect{applied: true, oldAddress: old, notice: notice}, nil
	}
	u.PendingEmail = req.email
	if err := writeEmailTx(ctx, tx, u); err != nil {
		return nil, err
	}
	it, err := s.issueToken(ctx, tx, u, store.PurposeEmailChange, now)
	if err != nil {
		return nil, err
	}
	if req.method == methodLink {
		if err := tx.InsertAudit(ctx, s.auditEntry(r, u.AccountID, "verification.send", u.ID,
			map[string]any{"purpose": store.PurposeEmailChange, "method": methodLink})); err != nil {
			return nil, err
		}
	}
	// deliver mails the token to the address it is given, so the confirmation goes to the new one.
	target := *u
	target.Email = req.email
	return &emailEffect{issued: it, target: &target}, nil
}

// handleConfirmEmailChange applies a pending change of address. Its link carries the token, which names the pending
// address; the link works once. The answer is the user, and the old address is told by a notice with no link.
func (s *Server) handleConfirmEmailChange(w http.ResponseWriter, r *http.Request) error {
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
	var (
		u    *store.User
		old  string
		held chargeHold // the notice's charge, given back if the write fails
	)
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		v, err := tx.VerificationByHash(ctx, auth.HashToken(in.Token))
		if errors.Is(err, store.ErrNotFound) {
			return errBadLink
		}
		if err != nil {
			return err
		}
		if v.Purpose != store.PurposeEmailChange || v.UsedAt != nil || !now.Before(v.ExpiresAt) {
			return errBadLink
		}
		found, err := tx.UserByID(ctx, v.UserID)
		if errors.Is(err, store.ErrNotFound) {
			return errBadLink
		}
		if err != nil {
			return err
		}
		if found.Status == store.UserDisabled {
			return apierr.New(apierr.UserDisabled, "the user is disabled")
		}
		// A suspended account is refused by the session and key rules in auth.go, so the check here is a second refusal for the
		// same account. An account being deleted takes no change, which is the refusal writableAccount gives it for writes
		// (decision 89).
		if found.AccountID != "" {
			acc, err := tx.Account(ctx, found.AccountID)
			if err != nil {
				return err
			}
			switch acc.Status {
			case store.AccountSuspended:
				return apierr.New(apierr.AccountSuspended, "the account is suspended")
			case store.AccountDeleting:
				return apierr.New(apierr.Conflict, "the account is being deleted")
			}
		}
		// A link whose change was replaced or applied has no pending address to apply.
		if found.PendingEmail == "" {
			return errBadLink
		}
		if _, err := tx.UserByEmail(ctx, found.AccountID, emailKey(found.PendingEmail)); err == nil {
			return apierr.New(apierr.Conflict, "a user with this email already exists in the account").With("reason", "email_taken")
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		at := store.Now()
		old = found.Email
		found.Email, found.EmailKey, found.PendingEmail = found.PendingEmail, emailKey(found.PendingEmail), ""
		found.UpdatedAt = at
		if err := writeEmailTx(ctx, tx, found); err != nil {
			return err
		}
		if err := tx.UseVerification(ctx, v.ID, at); errors.Is(err, store.ErrNotFound) {
			return errBadLink
		} else if err != nil {
			return err
		}
		if err := voidAddressTokens(ctx, tx, found.ID); err != nil {
			return err
		}
		// The notice is charged after the change's own writes, as for verification none: a refusal rolls the transaction back
		// (429, and the link is not used up). A failure after this point gives the tokens back (refund below, decision 89).
		if s.mailer != nil {
			h, err := s.reserveNotice(found.AccountID, old)
			if err != nil {
				return err
			}
			held = h
		}
		u = found
		e := s.auditEntry(r, found.AccountID, "user.email_changed", found.ID,
			map[string]any{"email": found.Email, "verification": "confirmation"})
		e.ActorKind, e.ActorID = string(auth.KindUser), found.ID
		return tx.InsertAudit(ctx, e)
	})
	if err != nil {
		s.refund(held)
		return err
	}
	if s.mailer != nil {
		s.noticeEmailChange(ctx, r, u, old)
	}
	writeJSON(w, http.StatusOK, toUserJSON(u))
	return nil
}

// noticeEmailChange tells oldAddress that the address of u was changed. It carries no link and names neither address. It
// is not audited of its own: the change is, as user.email_changed, and the notice is only its courtesy. The notice was
// charged to the mail budgets (mail.account and mail.target) before the change was written, so a change whose notice could
// not be charged was refused and never reaches here (decision 89). A failed send is logged and does not undo the change.
func (s *Server) noticeEmailChange(ctx context.Context, r *http.Request, u *store.User, oldAddress string) {
	subject, body, err := s.templates.Render(mail.PurposeEmailChanged, u.Locale, struct{}{})
	if err != nil {
		s.log.Error("cannot render the notice of an email change", "request_id", requestID(r), "error", err)
		return
	}
	if err := s.mailer.Send(ctx, mail.Message{To: oldAddress, Subject: subject, Body: body}); err != nil {
		s.log.Warn("cannot send the notice of an email change", "request_id", requestID(r), "error", err)
	}
}

// writeEmailTx writes the address columns of u, and turns a taken address into a conflict.
func writeEmailTx(ctx context.Context, tx *store.Tx, u *store.User) error {
	err := tx.WriteUserEmail(ctx, u)
	if errors.Is(err, store.ErrConflict) {
		return apierr.New(apierr.Conflict, "a user with this email already exists in the account")
	}
	return err
}

// voidAddressTokens ends the user's tokens that name the old address: password resets, verifications and any pending
// confirmation of a new one (decision 89). Sessions, device cookies and delegated tokens are kept, since it is the same
// person.
func voidAddressTokens(ctx context.Context, tx *store.Tx, userID string) error {
	for _, purpose := range []string{store.PurposeReset, store.PurposeVerify, store.PurposeEmailChange} {
		if err := tx.DeleteVerifications(ctx, userID, purpose, ""); err != nil {
			return err
		}
	}
	return nil
}
