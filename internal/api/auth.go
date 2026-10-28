// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// keyTouchInterval limits how often a key's last-used time is written. sessionTouchInterval does
// the same for a session's last-seen time.
const (
	keyTouchInterval     = time.Minute
	sessionTouchInterval = time.Minute
)

var errBadKey = apierr.New(apierr.Unauthenticated, "the key is not valid")

// authenticate identifies the caller. A Bearer credential in the Authorization header wins;
// without that header, the session cookie identifies a signed-in user. The session is returned
// only for the cookie case.
func (s *Server) authenticate(r *http.Request) (*auth.Principal, *store.Session, error) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return s.authenticateSession(r)
	}
	scheme, cred, ok := strings.Cut(h, " ")
	cred = strings.TrimSpace(cred)
	if !ok || !strings.EqualFold(scheme, "Bearer") || cred == "" {
		return nil, nil, apierr.New(apierr.Unauthenticated, "use Authorization: Bearer <credential>")
	}
	var (
		p   *auth.Principal
		err error
	)
	switch {
	case strings.HasPrefix(cred, auth.AdminKeyPrefix):
		p, err = s.authenticateKey(r.Context(), auth.AdminKeyPrefix, cred)
	case strings.HasPrefix(cred, auth.AccountKeyPrefix):
		p, err = s.authenticateKey(r.Context(), auth.AccountKeyPrefix, cred)
	default:
		return nil, nil, apierr.New(apierr.Unauthenticated, "unrecognised credential")
	}
	return p, nil, err
}

// authenticateSession identifies a signed-in user from the session cookie. An unsafe method
// (POST, PUT, PATCH, DELETE) must also come from this site's origin and carry the session's CSRF
// token, which stops another site from making the browser act for the user.
func (s *Server) authenticateSession(r *http.Request) (*auth.Principal, *store.Session, error) {
	ctx := r.Context()
	c, err := r.Cookie(s.sessionCookie)
	if err != nil || !auth.ValidToken(c.Value) {
		return nil, nil, apierr.New(apierr.Unauthenticated, "credentials are required")
	}
	sess, err := s.store.SessionByHash(ctx, auth.HashToken(c.Value))
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, apierr.New(apierr.Unauthenticated, "the session has ended; sign in again")
	}
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	if !now.Before(sess.ExpiresAt) {
		return nil, nil, apierr.New(apierr.Unauthenticated, "the session has expired; sign in again")
	}
	u, err := s.store.User(ctx, sess.AccountID, sess.UserID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, apierr.New(apierr.Unauthenticated, "the session has ended; sign in again")
	}
	if err != nil {
		return nil, nil, err
	}
	switch u.Status {
	case store.UserPending:
		return nil, nil, apierr.New(apierr.Unauthenticated, "the session has ended; sign in again")
	case store.UserDisabled:
		return nil, nil, apierr.New(apierr.UserDisabled, "the user is disabled")
	}
	// An installation admin has no account to check; its user status checks above still apply.
	if u.AccountID != "" {
		acc, err := s.store.Account(ctx, sess.AccountID)
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil, apierr.New(apierr.Unauthenticated, "the session has ended; sign in again")
		}
		if err != nil {
			return nil, nil, err
		}
		switch acc.Status {
		case store.AccountSuspended:
			return nil, nil, apierr.New(apierr.AccountSuspended, "the account is suspended")
		case store.AccountDeleting:
			return nil, nil, apierr.New(apierr.Unauthenticated, "the session has ended; sign in again")
		}
	}
	if isUnsafe(r.Method) {
		if err := s.checkCSRF(r, sess); err != nil {
			return nil, nil, err
		}
	}
	if now.Sub(sess.LastSeenAt) > sessionTouchInterval {
		if err := s.touchSession(ctx, sess, now); err != nil {
			s.log.Warn("cannot record session use", "request_id", requestID(r), "error", err)
		}
	}
	p := &auth.Principal{Kind: auth.KindUser, ID: u.ID, AccountID: u.AccountID, UserID: u.ID, Role: u.Role}
	return p, sess, nil
}

// isUnsafe reports whether a method changes state, and so needs the CSRF check.
func isUnsafe(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// checkCSRF requires the request's Origin to be this site and X-CSRF-Token to match the
// session's token. The token comparison is constant-time.
func (s *Server) checkCSRF(r *http.Request, sess *store.Session) error {
	if r.Header.Get("Origin") != s.origin {
		return apierr.New(apierr.Forbidden, "the request must come from this site")
	}
	got := r.Header.Get("X-CSRF-Token")
	if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(sess.CSRFToken)) != 1 {
		return apierr.New(apierr.Forbidden, "the CSRF token is missing or wrong")
	}
	return nil
}

// touchSession records a use of the session, and pushes its expiry out to the idle limit (never
// past its maximum age).
func (s *Server) touchSession(ctx context.Context, sess *store.Session, now time.Time) error {
	return s.store.Write(ctx, func(tx *store.Tx) error {
		return tx.TouchSession(ctx, sess.IDHash, now, s.sessionExpiry(sess.CreatedAt, now))
	})
}

// sessionExpiry returns when a session that began at created, and was last used at now, ends.
func (s *Server) sessionExpiry(created, now time.Time) time.Time {
	idle := now.Add(s.cfg.Sessions.IdleTimeout.D())
	if abs := created.Add(s.cfg.Sessions.MaxAge.D()); abs.Before(idle) {
		return abs
	}
	return idle
}

func (s *Server) authenticateKey(ctx context.Context, prefix, secret string) (*auth.Principal, error) {
	if !auth.ValidKeyFormat(prefix, secret) {
		return nil, errBadKey
	}
	k, err := s.store.APIKeyByHash(ctx, auth.HashKey(secret))
	if errors.Is(err, store.ErrNotFound) {
		return nil, errBadKey
	}
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if k.RevokedAt != nil {
		return nil, errBadKey
	}
	if k.ExpiresAt != nil && !now.Before(*k.ExpiresAt) {
		return nil, apierr.New(apierr.Unauthenticated, "the key has expired")
	}

	p := &auth.Principal{Kind: auth.KindAdminKey, ID: k.ID}
	switch {
	case prefix == auth.AdminKeyPrefix && k.AccountID == "":
	case prefix == auth.AccountKeyPrefix && k.AccountID != "":
		acc, err := s.store.Account(ctx, k.AccountID)
		if errors.Is(err, store.ErrNotFound) {
			return nil, errBadKey
		}
		if err != nil {
			return nil, err
		}
		switch acc.Status {
		case store.AccountSuspended:
			return nil, apierr.New(apierr.AccountSuspended, "the account is suspended")
		case store.AccountDeleting:
			return nil, errBadKey
		}
		p = &auth.Principal{Kind: auth.KindAccountKey, ID: k.ID, AccountID: k.AccountID}
	default:
		return nil, errBadKey
	}

	if k.LastUsedAt == nil || now.Sub(*k.LastUsedAt) > keyTouchInterval {
		if err := s.store.TouchAPIKey(ctx, k.ID, store.Now()); err != nil {
			s.log.Warn("cannot record key use", "key", k.ID, "error", err)
		}
	}
	return p, nil
}

func principalOf(r *http.Request) *auth.Principal {
	if info := infoFrom(r.Context()); info != nil {
		return info.principal
	}
	return nil
}

func requireAdmin(r *http.Request) error {
	if !principalOf(r).IsAdmin() {
		return apierr.New(apierr.Forbidden, "an installation admin is required")
	}
	return nil
}

// managedAccount loads the account named in the path, if the caller may manage it. Callers
// that may not are told it does not exist, so account IDs cannot be probed.
func (s *Server) managedAccount(r *http.Request) (*store.Account, error) {
	accID := r.PathValue("acc")
	if !principalOf(r).ManagesAccount(accID) {
		return nil, apierr.New(apierr.NotFound, "no such account")
	}
	acc, err := s.store.Account(r.Context(), accID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, apierr.New(apierr.NotFound, "no such account")
	}
	return acc, err
}

// auditEntry describes an action by the request's caller, for the audit log.
func (s *Server) auditEntry(r *http.Request, accountID, action, target string, details map[string]any) *store.AuditEntry {
	e := &store.AuditEntry{
		Time:      store.Now(),
		AccountID: accountID,
		Action:    action,
		Target:    target,
		UserAgent: truncate(r.UserAgent(), 256),
		Details:   details,
	}
	if info := infoFrom(r.Context()); info != nil {
		if info.ip.IsValid() {
			e.IP = info.ip.String()
		}
		if p := info.principal; p != nil {
			e.ActorKind, e.ActorID = string(p.Kind), p.ID
		}
	}
	return e
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Cut on a rune boundary.
	for n > 0 && n < len(s) && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}
