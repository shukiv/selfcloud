// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// Sign-in links let an application that holds its account key send its own signed-in user straight
// into Selfcloud's web interface. The client vouches for the user, so a sign-in by link skips TOTP
// (decided 2026-10-29; design section 8).

// defaultSignInNext is where a sign-in link goes when it names no path.
const defaultSignInNext = "/files"

// maxSignInNext is the longest path a sign-in link may name, in bytes.
const maxSignInNext = 2048

// cleanNext checks the path a sign-in link opens after sign-in. It must be a path on this site:
// one leading slash, no backslash, no scheme or host, no control character or space, and it must
// not resolve to another host (dot segments can turn "/.//host" into "//host"). A percent-encoded
// dot, slash or backslash is refused too: a browser reads "%2e" as a dot, so "/%2e%2e//host"
// becomes "//host" there, although Go's resolver keeps it as a plain path. The web interface's
// paths never need one.
func cleanNext(raw string) (string, error) {
	bad := apierr.New(apierr.InvalidRequest, "next must be a path on this site, such as /files/x")
	if len(raw) > maxSignInNext || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return "", bad
	}
	if strings.Contains(raw, `\`) {
		return "", bad
	}
	if lower := strings.ToLower(raw); strings.Contains(lower, "%2e") || strings.Contains(lower, "%2f") ||
		strings.Contains(lower, "%5c") {
		return "", bad
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] <= 0x20 || raw[i] == 0x7f {
			return "", bad
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return "", bad
	}
	base := &url.URL{Scheme: "http", Host: "selfcloud.invalid"}
	if res := base.ResolveReference(u); res.Host != base.Host || strings.HasPrefix(res.EscapedPath(), "//") {
		return "", bad
	}
	return raw, nil
}

// handleCreateSignInLink makes a single-use, short-lived sign-in link for one user. Only the
// account's own key may call it: an admin key, a session and a delegated token are refused.
func (s *Server) handleCreateSignInLink(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if p == nil || p.Kind != auth.KindAccountKey || p.Token != nil {
		return apierr.New(apierr.Forbidden, "only the account's own key may create a sign-in link")
	}
	acc, err := s.managedAccount(r)
	if err != nil {
		return err
	}
	// The body is optional: a request with no body means the default path.
	var in struct {
		Next *string `json:"next"`
	}
	if r.ContentLength != 0 {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxJSONBody))
		if err != nil {
			return apierr.Newf(apierr.InvalidRequest, "the body could not be read, or is larger than %d bytes", maxJSONBody)
		}
		if len(bytes.TrimSpace(body)) > 0 {
			r.Body = io.NopCloser(bytes.NewReader(body))
			if err := decodeJSON(w, r, &in); err != nil {
				return err
			}
		}
	}
	next := defaultSignInNext
	if in.Next != nil && *in.Next != "" {
		if next, err = cleanNext(*in.Next); err != nil {
			return err
		}
	}
	switch acc.Status {
	case store.AccountSuspended:
		return apierr.New(apierr.AccountSuspended, "the account is suspended")
	case store.AccountDeleting:
		return apierr.New(apierr.AccountSuspended, "the account is being deleted")
	}
	if err := s.rateLimit("signlink.create", acc.ID); err != nil {
		return err
	}
	ctx := r.Context()
	usrID := r.PathValue("usr")
	secret, hash := auth.NewToken()
	now := store.Now()
	expires := now.Add(s.cfg.Users.SignInLinkTTL.D())
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		u, err := tx.User(ctx, acc.ID, usrID)
		if errors.Is(err, store.ErrNotFound) || (err == nil && u.Role == store.RoleAdmin) {
			return notFound("user")
		}
		if err != nil {
			return err
		}
		switch u.Status {
		case store.UserPending:
			return apierr.New(apierr.UserNotVerified, "the user is not verified yet")
		case store.UserDisabled:
			return apierr.New(apierr.UserDisabled, "the user is disabled")
		}
		if err := tx.InsertSignInLink(ctx, &store.SignInLink{ID: id.New("sil"), AccountID: acc.ID,
			UserID: u.ID, TokenHash: hash, Next: next, ExpiresAt: expires, CreatedAt: now}); err != nil {
			return err
		}
		// The audit entry names the user. It never holds the token, the URL or the path.
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "user.sign_in_link", u.ID, nil))
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, signInLinkJSON{
		URL:       s.tokenLink(s.cfg.Users.SignInURLTemplate, secret),
		ExpiresAt: expires,
	})
	return nil
}

type signInLinkJSON struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// signedInLink is a sign-in answer with the path to open next.
type signedInLink struct {
	signedIn
	Next string `json:"next"`
}

// handleRedeemSignInLink completes a sign-in link: it consumes the link, ends any session this
// browser already had, and starts a normal session, as a password sign-in does. Every way the link
// can fail, other than a refused user or account, answers the same invalid_request as a verification
// link, so the answer reveals nothing about the link's history.
func (s *Server) handleRedeemSignInLink(w http.ResponseWriter, r *http.Request) error {
	if err := s.rateLimit("signlink.ip", clientKey(r)); err != nil {
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
		stateErr error
		u        *store.User
		next     string
		secret   string
		sess     *store.Session
	)
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		l, err := tx.SignInLinkByHash(ctx, auth.HashToken(in.Token))
		if errors.Is(err, store.ErrNotFound) {
			return errBadLink
		}
		if err != nil {
			return err
		}
		found, err := tx.UserByID(ctx, l.UserID)
		if errors.Is(err, store.ErrNotFound) {
			return errBadLink
		}
		if err != nil {
			return err
		}
		if l.UsedAt != nil || !now.Before(l.ExpiresAt) {
			stateErr = errBadLink
		}
		// The user and the account are checked again under the write lock. A refusal consumes the
		// link, so one link is one try, and the refusal is audited.
		if stateErr == nil {
			switch {
			case found.AccountID == "" || found.AccountID != l.AccountID || found.Role == store.RoleAdmin:
				stateErr = errBadLink
			case found.Status == store.UserPending:
				stateErr = apierr.New(apierr.UserNotVerified, "the user is not verified yet")
			case found.Status == store.UserDisabled:
				stateErr = apierr.New(apierr.UserDisabled, "the user is disabled")
			}
		}
		if stateErr == nil {
			acc, err := tx.Account(ctx, l.AccountID)
			if errors.Is(err, store.ErrNotFound) {
				stateErr = errBadLink
			} else if err != nil {
				return err
			} else {
				switch acc.Status {
				case store.AccountSuspended:
					stateErr = apierr.New(apierr.AccountSuspended, "the account is suspended")
				case store.AccountDeleting:
					stateErr = errBadLink
				}
			}
		}
		at := store.Now()
		if stateErr != nil {
			if l.UsedAt != nil || !now.Before(l.ExpiresAt) {
				// A used or expired link changes nothing and is not audited, so replaying a spent
				// link cannot fill the audit log.
				return nil
			}
			// A refusal of a live link uses it up, so it cannot be retried after the user is fixed.
			if err := tx.UseSignInLink(ctx, l.ID, at); err != nil {
				return err
			}
			e := s.auditEntry(r, l.AccountID, "auth.sign_in_link", l.UserID, nil)
			e.ActorKind, e.ActorID, e.Outcome = string(auth.KindUser), l.UserID, "failed"
			return tx.InsertAudit(ctx, e)
		}
		if err := tx.UseSignInLink(ctx, l.ID, at); errors.Is(err, store.ErrNotFound) {
			stateErr = errBadLink
			return nil
		} else if err != nil {
			return err
		}
		// Signing in replaces any session this browser already had, as a password sign-in does.
		if c, err := r.Cookie(s.sessionCookie); err == nil && auth.ValidToken(c.Value) {
			if err := tx.DeleteSession(ctx, auth.HashToken(c.Value)); err != nil {
				return err
			}
		}
		u = found
		next = l.Next
		secret, sess = s.newSession(r, u, at)
		if err := tx.InsertSession(ctx, sess); err != nil {
			return err
		}
		e := s.auditEntry(r, u.AccountID, "auth.sign_in_link", u.ID, nil)
		e.ActorKind, e.ActorID = string(auth.KindUser), u.ID
		return tx.InsertAudit(ctx, e)
	})
	if err != nil {
		return err
	}
	if stateErr != nil {
		return stateErr
	}
	s.setSessionCookie(w, secret)
	writeJSON(w, http.StatusOK, signedInLink{
		signedIn: signedIn{User: toUserJSON(u), CSRFToken: sess.CSRFToken},
		Next:     next,
	})
	return nil
}
