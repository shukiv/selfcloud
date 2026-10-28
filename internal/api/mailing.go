package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"selfcloud/internal/apierr"
	"selfcloud/internal/auth"
	"selfcloud/internal/id"
	"selfcloud/internal/mail"
	"selfcloud/internal/store"
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
	ttl, tmpl := s.cfg.Users.VerificationTTL.D(), s.cfg.Users.VerifyURLTemplate
	if purpose == store.PurposeReset {
		ttl, tmpl = s.cfg.Users.ResetTTL.D(), s.cfg.Users.ResetURLTemplate
	}
	if err := tx.DeleteVerifications(ctx, u.ID, purpose, ""); err != nil {
		return nil, err
	}
	secret, hash := auth.NewToken()
	it := &issuedToken{purpose: purpose, token: secret, link: s.tokenLink(tmpl, secret), expires: now.Add(ttl)}
	err := tx.InsertVerification(ctx, &store.Verification{ID: id.New("vrf"), UserID: u.ID,
		Purpose: purpose, TokenHash: hash, ExpiresAt: it.expires, CreatedAt: now})
	if err != nil {
		return nil, err
	}
	return it, nil
}

// tokenLink fills a link template. The token is URL-escaped; the templates put it in the fragment,
// so it is never sent to a server or written to a log.
func (s *Server) tokenLink(tmpl, token string) string {
	return strings.NewReplacer("{public_url}", s.cfg.PublicURL, "{token}", url.QueryEscape(token)).Replace(tmpl)
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
// audit entry was already written with the token. For "email" the mail is sent now, outside any
// transaction, and the outcome is audited in a transaction of its own. A failed send does not fail
// the request: the user exists and can be sent a new token later.
func (s *Server) deliver(ctx context.Context, r *http.Request, u *store.User, it *issuedToken, method string) *verificationJSON {
	if method == methodLink {
		// Defence in depth: a member or owner session never receives a token, whatever the method
		// check said. An admin session may, since linkAllowed let it ask for a pending owner.
		if p := principalOf(r); p.Kind == auth.KindUser && p.Role != auth.RoleAdmin {
			return &verificationJSON{Method: methodLink, ExpiresAt: &it.expires}
		}
		return &verificationJSON{Method: methodLink, URL: it.link, Token: it.token, ExpiresAt: &it.expires}
	}
	sent := true
	outcome := "ok"
	if err := s.sendToken(ctx, u, it); err != nil {
		sent = false
		outcome = "failed"
		s.log.Warn("cannot send mail", "request_id", requestID(r), "purpose", it.purpose, "error", err)
	}
	entry := s.auditEntry(r, u.AccountID, "verification.send", u.ID,
		map[string]any{"purpose": it.purpose, "method": methodEmail})
	entry.Outcome = outcome
	if err := s.store.Write(ctx, func(tx *store.Tx) error { return tx.InsertAudit(ctx, entry) }); err != nil {
		s.log.Error("cannot audit mail", "request_id", requestID(r), "error", err)
	}
	return &verificationJSON{Method: methodEmail, Sent: &sent}
}

// checkMailBudget charges one message to accountID and to address against the mail limits, before
// anything is written, so a refused request creates nothing. Only method "email" sends mail.
func (s *Server) checkMailBudget(accountID, address string) error {
	if err := s.rateLimit("mail.account", accountID); err != nil {
		return err
	}
	return s.rateLimit("mail.target", emailKey(address))
}

// clientKey is the rate-limit key for a request's client address.
func clientKey(r *http.Request) string {
	if info := infoFrom(r.Context()); info != nil && info.ip.IsValid() {
		return info.ip.String()
	}
	return "unknown"
}

// emailKey is the form of an email address used to look users up: lowercased.
func emailKey(email string) string { return strings.ToLower(email) }
