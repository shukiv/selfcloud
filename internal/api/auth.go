package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"selfcloud/internal/apierr"
	"selfcloud/internal/auth"
	"selfcloud/internal/store"
)

// keyTouchInterval limits how often a key's last-used time is written.
const keyTouchInterval = time.Minute

var errBadKey = apierr.New(apierr.Unauthenticated, "the key is not valid")

// authenticate identifies the caller from the Authorization header.
func (s *Server) authenticate(r *http.Request) (*auth.Principal, error) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return nil, apierr.New(apierr.Unauthenticated, "credentials are required")
	}
	scheme, cred, ok := strings.Cut(h, " ")
	cred = strings.TrimSpace(cred)
	if !ok || !strings.EqualFold(scheme, "Bearer") || cred == "" {
		return nil, apierr.New(apierr.Unauthenticated, "use Authorization: Bearer <credential>")
	}
	switch {
	case strings.HasPrefix(cred, auth.AdminKeyPrefix):
		return s.authenticateKey(r.Context(), auth.AdminKeyPrefix, cred)
	case strings.HasPrefix(cred, auth.AccountKeyPrefix):
		return s.authenticateKey(r.Context(), auth.AccountKeyPrefix, cred)
	}
	return nil, apierr.New(apierr.Unauthenticated, "unrecognised credential")
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
		return apierr.New(apierr.Forbidden, "an installation admin key is required")
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
