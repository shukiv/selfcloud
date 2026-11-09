// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/recovery"
	"github.com/shukiv/selfcloud/internal/store"
)

// Verification methods, as the API names them.
const (
	methodEmail = "email"
	methodLink  = "link"
	methodNone  = "none"
)

var errMailNotConfigured = errors.New("mail is not configured")

// issuedToken is a verification or reset token made for a user. token is the secret itself. It is
// returned to a key holder only when the method is "link", and otherwise lives only in the mail.
type issuedToken struct {
	purpose string
	token   string
	link    string
	expires time.Time
}

// verificationJSON reports how a verification or reset was delivered. For "link" it carries the
// link and token; for "email" only whether the mail went out.
type verificationJSON struct {
	Method    string     `json:"method"`
	URL       string     `json:"url,omitempty"`
	Token     string     `json:"token,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Sent      *bool      `json:"sent,omitempty"`
}

// linkTarget describes the user a verification is for, which decides what an admin session may
// ask for.
type linkTarget struct {
	role   string // the user's role; "" when the user is new and the role is decided by the caller
	status string // the user's status
	reset  bool   // true for a password reset
	// existing is true when the user already exists (a resend or a reset), false when the call creates the user. A
	// key may create a user and mail its link, but may not take a link to a user that exists (decision 41).
	existing bool
}

// linkAllowed reports whether a caller may ask for a link. A link hands a token to the caller, so
// a key may always ask. An admin session may ask only to hand the first owner of a new account its
// link: a pending owner, never a member, an admin or a password reset. Every other session gets
// email only, since a session that could mint a link could take over the account it reaches.
func linkAllowed(p *auth.Principal, t linkTarget) bool {
	if p == nil {
		return false
	}
	if p.Kind == auth.KindUser {
		return p.Role == auth.RoleAdmin && !t.reset && t.role == store.RoleOwner && t.status == store.UserPending
	}
	return true
}

// checkVerificationMethod checks a requested method against the caller and the configuration.
// "none" verifies a user without any check, so only a key may use it. "link" follows linkAllowed.
// "email" needs mail to be configured, and any caller may use it.
func (s *Server) checkVerificationMethod(r *http.Request, method string, t linkTarget) error {
	p := principalOf(r)
	switch method {
	case methodEmail:
		if s.mailer == nil {
			return apierr.New(apierr.InvalidRequest, "mail is not configured; an API key may use the method \"link\" or \"none\"")
		}
	case methodLink:
		// An installation admin's key is never handed a link to a user that already exists: the key reaches every
		// account, so the link would take over that user. Decision 41 gives the admin's ways in through an admin session
		// only, whatever admin.tenant_access says. A new user, which the key creates, is not an existing one.
		if p != nil && p.Kind == auth.KindAdminKey && (t.reset || t.existing) {
			return apierr.New(apierr.Forbidden, "an installation admin's key may not make a link to an existing user; use email")
		}
		// A reset link from an installation admin's session is decided by admin.tenant_access (decision 41), not by
		// linkAllowed: the email reset stays open either way, since it reaches the user.
		if p != nil && p.Kind == auth.KindUser && p.Role == auth.RoleAdmin && t.reset {
			return s.tenantAccessGate()
		}
		// An admin session's link for a pending owner is an admin's way in, so admin.tenant_access gates it like the
		// other ways (sec1, decision 84).
		if p != nil && p.Kind == auth.KindUser && p.Role == auth.RoleAdmin && linkAllowed(p, t) {
			if err := s.tenantAccessGate(); err != nil {
				return err
			}
		}
		if !linkAllowed(p, t) {
			return apierr.New(apierr.Forbidden, "only an API key may use this method; use email")
		}
	case methodNone:
		if p == nil || p.Kind == auth.KindUser {
			return apierr.New(apierr.Forbidden, "only an API key may use this method; use email")
		}
	default:
		return apierr.New(apierr.InvalidRequest, "verification must be email, link or none")
	}
	return nil
}

// issueToken makes a token of the given purpose for u, inside tx. It voids the user's earlier
// tokens of that purpose, so only the newest one works.
func (s *Server) issueToken(ctx context.Context, tx *store.Tx, u *store.User, purpose string, now time.Time) (*issuedToken, error) {
	ttl, tmpl := recovery.DefaultLifetime(s.cfg.Users, purpose)
	t, err := recovery.Issue(ctx, tx, u, purpose, ttl, tmpl, s.cfg.PublicURL, now)
	if err != nil {
		return nil, err
	}
	return &issuedToken{purpose: purpose, token: t.Secret, link: t.Link, expires: t.Expires}, nil
}

// tokenLink fills a link template. See recovery.TokenLink.
func (s *Server) tokenLink(tmpl, token string) string {
	return recovery.TokenLink(tmpl, s.cfg.PublicURL, token)
}

// sendToken mails a token's link to u, in u's locale.
func (s *Server) sendToken(ctx context.Context, u *store.User, it *issuedToken) error {
	if s.mailer == nil {
		return errMailNotConfigured
	}
	subject, body, err := s.templates.Render(it.purpose, u.Locale, struct{ URL, ExpiresAt string }{
		URL:       it.link,
		ExpiresAt: it.expires.UTC().Format("2006-01-02 15:04 UTC"),
	})
	if err != nil {
		return err
	}
	return s.mailer.Send(ctx, mail.Message{To: u.Email, Subject: subject, Body: body})
}

// deliver hands a new token to its user. For "link" the link goes back to the caller, and the
// audit entry was already written with the token. For "email" the mail is audited and sent here,
// outside any transaction. The audit log is append-only, so a mail is two rows: an "attempted" row
// written before the send (a crash after it leaves an attempt on record, never a sent mail), and a
// result row after the send, "ok" or "failed". A write that fails before the send stops the send.
// A failed send does not fail the request: the user exists and can be sent a new token later.
func (s *Server) deliver(ctx context.Context, r *http.Request, u *store.User, it *issuedToken, method string) *verificationJSON {
	if method == methodLink {
		// Defence in depth: a member or owner session never receives a token, whatever the method
		// check said. An admin session may, since linkAllowed let it ask for a pending owner.
		if p := principalOf(r); p.Kind == auth.KindUser && p.Role != auth.RoleAdmin {
			return &verificationJSON{Method: methodLink, ExpiresAt: &it.expires}
		}
		return &verificationJSON{Method: methodLink, URL: it.link, Token: it.token, ExpiresAt: &it.expires}
	}
	// The attempt is written before the mail leaves (fu2, decision 74; review fix 1), so no mail goes out with no row.
	// A write that fails stops the send, and the answer says the mail was not sent, as for a send that failed.
	details := map[string]any{"purpose": it.purpose, "method": methodEmail}
	attempt := s.auditEntry(r, u.AccountID, "verification.send", u.ID, details)
	attempt.Outcome = "attempted"
	if err := s.store.Write(ctx, func(tx *store.Tx) error { return tx.InsertAudit(ctx, attempt) }); err != nil {
		s.log.Error("cannot audit mail; not sent", "request_id", requestID(r), "error", err)
		sent := false
		return &verificationJSON{Method: methodEmail, Sent: &sent}
	}
	sent := true
	result := s.auditEntry(r, u.AccountID, "verification.send", u.ID, details)
	result.Outcome = "ok"
	if err := s.sendToken(ctx, u, it); err != nil {
		sent = false
		s.log.Warn("cannot send mail", "request_id", requestID(r), "purpose", it.purpose, "error", err)
		result.Outcome = "failed"
	}
	if err := s.store.Write(ctx, func(tx *store.Tx) error { return tx.InsertAudit(ctx, result) }); err != nil {
		s.log.Error("cannot audit the mail's result", "request_id", requestID(r), "error", err)
	}
	return &verificationJSON{Method: methodEmail, Sent: &sent}
}

// checkMailBudget charges one message to accountID and to address against the mail limits, before
// anything is written, so a refused request creates nothing. Both buckets are charged together or
// not at all: a refused address does not spend the account's budget. Only method "email" sends
// mail. The forgot-password path has its own buckets and does not use this.
func (s *Server) checkMailBudget(accountID, address string) error {
	return s.chargeAll(charge{"mail.account", accountID}, charge{"mail.target", emailKey(address)})
}

// reserveNotice charges the notice to the old address of an account's change of address: the account's notice budget and the
// one for that address (mail.notice.account and mail.notice, decision 89). Neither is shared with other mail, so another tenant
// or an anonymous caller cannot spend them. The tokens are returned, so the caller can refund them if the change fails.
func (s *Server) reserveNotice(accountID, oldAddress string) (*chargeHold, error) {
	return s.reserveAll(charge{"mail.notice.account", accountID}, charge{"mail.notice", accountID + "|" + emailKey(oldAddress)})
}

// addressKey is the rate-limit key for a request's client address, used by every limit and lockout that counts one
// address. An IPv6 client counts by its /64, since a host holds the whole /64 and could otherwise change address at
// will; an IPv4 client counts by its address (fu2, decision 74).
func addressKey(r *http.Request) string {
	info := infoFrom(r.Context())
	if info == nil || !info.ip.IsValid() {
		return "unknown"
	}
	ip := info.ip.Unmap()
	if ip.Is6() {
		if p, err := ip.Prefix(64); err == nil {
			return p.String()
		}
	}
	return ip.String()
}

// emailKey is the form of an email address used to look users up: lowercased.
func emailKey(email string) string { return strings.ToLower(email) }
