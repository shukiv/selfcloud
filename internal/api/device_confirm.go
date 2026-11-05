// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// Device confirmation (sec2, decision 86). Once the email's cap on failed sign-ins is spent, and the browser holds no
// trusted device cookie, a sign-in is not refused when a mail can be sent. Every attempt gets the same answer, whatever
// the password, so it shows nothing about the password or the email. Only a right password mails a single-use link to
// the user. The attempt sets a nonce cookie in its browser, and the link signs in only the browser that holds that
// nonce.

// deviceConfirmMailCap is how many confirmation mails one user gets in deviceConfirmMailWindow. Over it, no mail is sent,
// and the answer is the same.
const (
	deviceConfirmMailCap    = 3
	deviceConfirmMailWindow = 15 * time.Minute
)

// deviceConfirmAnswer is the answer to a sign-in that waits for the browser to be confirmed by email.
type deviceConfirmAnswer struct {
	Confirm string `json:"confirm"`
}

// confirmCookieName is the nonce cookie's name. It has the __Host- prefix when the session cookie does (https only).
func (s *Server) confirmCookieName() string {
	if s.sessionCookie == "selfcloud_session" {
		return "selfcloud_confirm"
	}
	return "__Host-selfcloud_confirm"
}

// setConfirmCookie sets the nonce cookie on the attempt's answer, and returns its value. A browser that already holds a
// valid nonce keeps it, so its attempts and their links share one. It is set on every answer of the confirmation, so a
// right password and a wrong one get the same headers.
func (s *Server) setConfirmCookie(w http.ResponseWriter, r *http.Request) string {
	name := s.confirmCookieName()
	var nonce string
	if c, err := r.Cookie(name); err == nil && auth.ValidToken(c.Value) {
		nonce = c.Value
	} else {
		nonce, _ = auth.NewToken()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    nonce,
		Path:     "/",
		MaxAge:   int(s.cfg.Users.DeviceConfirmTTL.D().Seconds()),
		HttpOnly: true,
		Secure:   name != "selfcloud_confirm",
		SameSite: http.SameSiteLaxMode,
	})
	return nonce
}

// clearConfirmCookie ends the nonce cookie once its link has been used.
func (s *Server) clearConfirmCookie(w http.ResponseWriter) {
	name := s.confirmCookieName()
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   name != "selfcloud_confirm",
		SameSite: http.SameSiteLaxMode,
	})
}

// confirmByEmail answers a sign-in whose email's cap is spent, from a browser with no trusted device, when a mail can be
// sent. The answer is the same whatever the password: 202 with the confirmation. Only a user whose password matches gets
// a mail, and only if the user can sign in and is under the mail cap.
func (s *Server) confirmByEmail(w http.ResponseWriter, r *http.Request, candidates []*store.User, password string,
	remember *bool) error {
	ctx := r.Context()
	nonce := s.setConfirmCookie(w, r)
	var matches []*store.User
	for _, c := range candidates {
		ok, err := s.matchPassword(ctx, c.PasswordHash, password)
		if err != nil {
			return err
		}
		if ok {
			matches = append(matches, c)
		}
	}
	if len(candidates) == 0 {
		if err := auth.DummyVerify(ctx, password); err != nil {
			return err
		}
	}
	// A password that matched none of them is audited as a failed sign-in, as a refused one is. It is one write, as a
	// matching password's is, so the two take about the same time.
	if len(matches) == 0 {
		if err := s.auditFailedLogin(r, candidates); err != nil {
			return err
		}
	}
	short := remember != nil && !*remember
	for _, u := range matches {
		if err := s.confirmBrowserMail(r, u, nonce, short); err != nil {
			return err
		}
	}
	writeJSON(w, http.StatusAccepted, deviceConfirmAnswer{Confirm: methodEmail})
	return nil
}

// usableNow reports whether a user may sign in now: active, and in an account that is not suspended or being deleted.
// It is read inside tx, so a change committed before the write holds is seen.
func usableNow(ctx context.Context, tx *store.Tx, u *store.User) (bool, error) {
	if u.Status != store.UserActive {
		return false, nil
	}
	if u.AccountID == "" {
		return true, nil
	}
	acc, err := tx.Account(ctx, u.AccountID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return acc.Status == store.AccountActive, nil
}

// confirmBrowserMail makes the confirmation link for a user whose password matched, and mails it in the background, as a
// reset does. The link row and its attempted audit entry are written before the mail leaves, so no mail goes out
// without a row. A user who cannot sign in, or who is over the mail cap, gets no link: the answer is the same.
func (s *Server) confirmBrowserMail(r *http.Request, u *store.User, nonce string, short bool) error {
	ctx := r.Context()
	secret, hash := auth.NewToken()
	now := store.Now()
	expires := now.Add(s.cfg.Users.DeviceConfirmTTL.D())
	var (
		target *store.User
		it     *issuedToken
		result *store.AuditEntry
	)
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		found, err := tx.User(ctx, u.AccountID, u.ID)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		ok, err := usableNow(ctx, tx, found)
		if err != nil {
			return err
		}
		if !ok {
			e := s.auditEntry(r, found.AccountID, "user.login_failed", found.ID, nil)
			e.ActorKind, e.ActorID, e.Outcome = string(auth.KindUser), found.ID, "failed"
			return tx.InsertAudit(ctx, e)
		}
		details := map[string]any{"purpose": mail.PurposeDeviceConfirm, "method": methodEmail}
		if s.chargeAll(charge{"mail.device.user", found.ID}) != nil {
			e := s.auditEntry(r, found.AccountID, "user.device_confirm_sent", found.ID, details)
			e.ActorKind, e.ActorID, e.Outcome = string(auth.KindUser), found.ID, "capped"
			return tx.InsertAudit(ctx, e)
		}
		row := &store.DeviceConfirm{ID: id.New("dcf"), UserID: found.ID, TokenHash: hash, NonceHash: auth.HashToken(nonce),
			Short: short, ExpiresAt: expires, CreatedAt: now}
		if err := tx.InsertDeviceConfirm(ctx, row); err != nil {
			return err
		}
		attempt := s.auditEntry(r, found.AccountID, "user.device_confirm_sent", found.ID, details)
		attempt.ActorKind, attempt.ActorID, attempt.Outcome = string(auth.KindUser), found.ID, "attempted"
		if err := tx.InsertAudit(ctx, attempt); err != nil {
			return err
		}
		target = found
		it = &issuedToken{purpose: mail.PurposeDeviceConfirm, token: secret,
			link: s.tokenLink(s.cfg.Users.DeviceConfirmURLTemplate, secret), expires: expires}
		result = s.auditEntry(r, found.AccountID, "user.device_confirm_sent", found.ID, details)
		result.ActorKind, result.ActorID = string(auth.KindUser), found.ID
		return nil
	})
	if err != nil {
		return err
	}
	if target == nil || it == nil {
		return nil
	}
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		mctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		result.Outcome = "ok"
		if err := s.sendToken(mctx, target, it); err != nil {
			result.Outcome = "failed"
			s.log.Warn("cannot send device confirmation mail", "user", target.ID, "error", err)
		}
		if err := s.store.Write(mctx, func(tx *store.Tx) error { return tx.InsertAudit(mctx, result) }); err != nil {
			s.log.Error("cannot audit the device confirmation mail's result", "user", target.ID, "error", err)
		}
	}()
	return nil
}

// handleDeviceConfirm completes a device confirmation link. In the browser that asked, it signs the user in as a sign-in
// does, and sets the device cookie. In another browser it signs nothing in, and the link stays usable for the browser
// that asked. The link is single use and expires; a password change, reset, admin set-password or disable after the
// mail voids it.
func (s *Server) handleDeviceConfirm(w http.ResponseWriter, r *http.Request) error {
	if err := s.rateLimit("device.confirm.ip", addressKey(r)); err != nil {
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
	var nonceHash []byte
	if c, err := r.Cookie(s.confirmCookieName()); err == nil && auth.ValidToken(c.Value) {
		nonceHash = auth.HashToken(c.Value)
	}
	ctx := r.Context()
	now := time.Now()
	var (
		stateErr error
		u        *store.User
		secret   string
		sess     *store.Session
		// issuedAt is the time the transaction makes the session: the device cookie is stamped with it (sec1, F9).
		issuedAt time.Time
	)
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		c, err := tx.DeviceConfirmByHash(ctx, auth.HashToken(in.Token))
		if errors.Is(err, store.ErrNotFound) {
			return errBadLink
		}
		if err != nil {
			return err
		}
		if c.UsedAt != nil || !now.Before(c.ExpiresAt) {
			return errBadLink
		}
		found, err := tx.UserByID(ctx, c.UserID)
		if errors.Is(err, store.ErrNotFound) {
			return errBadLink
		}
		if err != nil {
			return err
		}
		// Another browser signs nothing in, and the link is not used up: the browser that asked can still use it.
		if nonceHash == nil || !bytes.Equal(nonceHash, c.NonceHash) {
			return apierr.New(apierr.Forbidden, "open this link in the browser where you signed in").
				With("reason", "other_browser")
		}
		// A change to the password, a reset, an admin's set-password or a disable after the mail voids the link, as it
		// voids a device cookie (sec1, decision 84, F9). The cut-off is in milliseconds, as the link's creation is.
		cut, err := tx.TokensNotBeforeIn(ctx, found.AccountID, found.ID)
		if err != nil {
			return err
		}
		if !cut.IsZero() && cut.UnixMilli() >= c.CreatedAt.UnixMilli() {
			return errBadLink
		}
		// The user and the account are checked again under the write lock. A refusal is audited and the link is kept.
		switch found.Status {
		case store.UserPending:
			stateErr = apierr.New(apierr.UserNotVerified, "the user has not verified the email address yet")
		case store.UserDisabled:
			stateErr = apierr.New(apierr.UserDisabled, "the user is disabled")
		}
		if stateErr == nil && found.AccountID != "" {
			acc, err := tx.Account(ctx, found.AccountID)
			switch {
			case errors.Is(err, store.ErrNotFound):
				stateErr = errBadLink
			case err != nil:
				return err
			case acc.Status == store.AccountSuspended:
				stateErr = apierr.New(apierr.AccountSuspended, "the account is suspended")
			case acc.Status == store.AccountDeleting:
				stateErr = errBadLink
			}
		}
		if stateErr != nil {
			e := s.auditEntry(r, found.AccountID, "user.login_failed", found.ID, nil)
			e.ActorKind, e.ActorID, e.Outcome = string(auth.KindUser), found.ID, "failed"
			return tx.InsertAudit(ctx, e)
		}
		issuedAt = store.Now()
		if err := tx.UseDeviceConfirm(ctx, c.ID, issuedAt); errors.Is(err, store.ErrNotFound) {
			return errBadLink
		} else if err != nil {
			return err
		}
		// Signing in replaces any session this browser already had, as a password sign-in does.
		if err := s.endBrowserSession(r, tx); err != nil {
			return err
		}
		secret, sess = s.newSession(r, found, issuedAt, c.Short)
		if err := tx.InsertSession(ctx, sess); err != nil {
			return err
		}
		u = found
		login := s.auditEntry(r, found.AccountID, "user.login", found.ID, nil)
		login.ActorKind, login.ActorID = string(auth.KindUser), found.ID
		if err := tx.InsertAudit(ctx, login); err != nil {
			return err
		}
		confirmed := s.auditEntry(r, found.AccountID, "user.device_confirmed", found.ID, nil)
		confirmed.ActorKind, confirmed.ActorID = string(auth.KindUser), found.ID
		return tx.InsertAudit(ctx, confirmed)
	})
	if err != nil {
		return err
	}
	if stateErr != nil {
		return stateErr
	}
	if s.afterLoginCommit != nil {
		s.afterLoginCommit(u.ID)
	}
	if err := s.setDeviceCookie(w, u.ID, issuedAt); err != nil {
		return err
	}
	s.clearConfirmCookie(w)
	s.writeSignedIn(w, secret, u, sess)
	return nil
}
