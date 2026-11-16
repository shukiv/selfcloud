// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
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

// cleanSessionRef checks the application's own name for the session a sign-in link starts (decision 211). Empty
// means none; otherwise it is 1 to 128 letters, digits, - or _, so it is safe to store and to match a session by.
func cleanSessionRef(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	bad := apierr.New(apierr.InvalidRequest, "session_ref must be 1 to 128 letters, digits, - or _")
	if len(raw) > 128 {
		return "", bad
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return "", bad
		}
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
		Next             *string `json:"next"`
		Mode             string  `json:"mode"`
		SessionRef       string  `json:"session_ref"`
		SessionExpiresAt *string `json:"session_expires_at"`
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
	// mode tab (the default) is the link as before; mode frame makes a link for a host application's frame
	// (decision 197). A frame link needs allowed origins and a secure public address, checked below.
	frame := false
	switch in.Mode {
	case "", "tab":
	case "frame":
		frame = true
		if !s.framePublicURLAllowed() {
			return apierr.New(apierr.InvalidRequest, "a frame sign-in link needs an https public_url")
		}
	default:
		return apierr.New(apierr.InvalidRequest, "mode must be tab or frame")
	}
	now := store.Now()
	// The application may name the session the link starts, and say when it must end (decision 211). Both are
	// optional; the redeemed session copies them.
	ref, err := cleanSessionRef(in.SessionRef)
	if err != nil {
		return err
	}
	var sessionEndsAt time.Time
	if in.SessionExpiresAt != nil {
		t, err := time.Parse(time.RFC3339, *in.SessionExpiresAt)
		if err != nil || !t.After(now) {
			return apierr.New(apierr.InvalidRequest, "session_expires_at must be an RFC 3339 time after now")
		}
		sessionEndsAt = t
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
		if frame {
			acct, err := tx.EmbedOrigins(ctx, acc.ID)
			if err != nil {
				return err
			}
			origins, err := s.unionEmbedOrigins(acct)
			if err != nil {
				return err
			}
			if len(origins) == 0 {
				return apierr.New(apierr.InvalidRequest, "the account has no allowed embed origins")
			}
		}
		if err := tx.InsertSignInLink(ctx, &store.SignInLink{ID: id.New("sil"), AccountID: acc.ID,
			UserID: u.ID, TokenHash: hash, Next: next, ExpiresAt: expires, CreatedAt: now, Frame: frame,
			SessionRef: ref, SessionEndsAt: sessionEndsAt}); err != nil {
			return err
		}
		// The audit entry names the user. It never holds the token, the URL or the path.
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "user.sign_in_link", u.ID, nil))
	})
	if err != nil {
		return err
	}
	link := s.tokenLink(s.cfg.Users.SignInURLTemplate, secret)
	if frame {
		link = frameLinkURL(link, acc.ID)
	}
	writeJSON(w, http.StatusCreated, signInLinkJSON{
		URL:       link,
		ExpiresAt: expires,
	})
	return nil
}

// signedInUser returns the user of the live session this browser holds, or nil when the browser has no
// cookie, the cookie maps to no session, the session has expired, or its user is gone.
func (s *Server) signedInUser(ctx context.Context, tx *store.Tx, r *http.Request, now time.Time) (*store.User, error) {
	c, err := r.Cookie(s.sessionCookie)
	if err != nil || !auth.ValidToken(c.Value) {
		return nil, nil
	}
	sess, err := tx.SessionByHash(ctx, auth.HashToken(c.Value))
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !s.sessionLive(sess, now) {
		return nil, nil
	}
	u, err := tx.UserByID(ctx, sess.UserID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	return u, err
}

// userNameOf is the name a user is shown by: its display name, or its email when it has none.
func userNameOf(u *store.User) map[string]string {
	name := u.DisplayName
	if name == "" {
		name = u.Email
	}
	return map[string]string{"name": name, "email": u.Email}
}

type signInLinkJSON struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// signedInLink is a sign-in answer with the path to open next.
type signedInLink struct {
	signedIn
	Next string `json:"next"`
	// Frame is true when the link started a frame session: the page is embedded in a host application
	// (decision 197).
	Frame bool `json:"frame"`
	// FrameOrigins are the origins that may frame the account's pages, the installation's list and the
	// account's together (decision 214). Present only for a frame session.
	FrameOrigins []string `json:"frame_origins,omitempty"`
}

// handleRedeemSignInLink completes a sign-in link: it consumes the link, ends any session this
// browser already had, and starts a normal session, as a password sign-in does. Every way the link
// can fail, other than a refused user or account, answers the same invalid_request as a verification
// link, so the answer reveals nothing about the link's history.
func (s *Server) handleRedeemSignInLink(w http.ResponseWriter, r *http.Request) error {
	if err := s.rateLimit("signlink.ip", addressKey(r)); err != nil {
		return err
	}
	var in struct {
		Token   string `json:"token"`
		Confirm bool   `json:"confirm"`
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
		// A browser signed in as another user is asked before it switches. Nothing is written here, so
		// the link stays live for the prompt's second answer, which carries confirm.
		if !in.Confirm {
			cur, err := s.signedInUser(ctx, tx, r, now)
			if err != nil {
				return err
			}
			if cur != nil && cur.ID != l.UserID {
				stateErr = apierr.New(apierr.Conflict, "this browser is signed in as another user").
					With("reason", "signed_in_as_other").
					With("current", userNameOf(cur)).
					With("target", userNameOf(found))
				return nil
			}
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
		// An application's sign-in link decides the lifetime, as before: the session is kept (decision 75).
		secret, sess = s.newSession(r, u, at, false)
		// A frame link starts a frame session: its cookie is partitioned (decision 197). An admin user is
		// refused above, so an admin session never becomes a frame session.
		sess.Frame = l.Frame
		// The session copies the link's ref and end (decision 211), and its first expiry is the ordinary one
		// capped at the end, so it never outlives the moment the application named.
		sess.SessionRef = l.SessionRef
		sess.EndsAt = l.SessionEndsAt
		sess.ExpiresAt = s.sessionEnd(sess, at)
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
	s.setSessionCookie(w, secret, sess)
	// A frame session's answer carries the origins that may frame the account's pages, so the page can
	// address its host (decision 214). A tab session's answer carries frame false and no origins.
	var frameOrigins []string
	if sess.Frame {
		origins, err := s.allowedEmbedOrigins(ctx, sess.AccountID)
		if err != nil {
			return err
		}
		frameOrigins = origins
	}
	writeJSON(w, http.StatusOK, signedInLink{
		signedIn:     signedIn{User: toUserJSON(u), CSRFToken: sess.CSRFToken},
		Next:         next,
		Frame:        sess.Frame,
		FrameOrigins: frameOrigins,
	})
	return nil
}
