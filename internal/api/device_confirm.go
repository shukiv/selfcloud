// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"time"

	"golang.org/x/time/rate"

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

const (
	// deviceConfirmMailCap is how many confirmation mails one address gets in deviceConfirmMailWindow. Over it, no mail
	// is sent, and the answer is the same.
	deviceConfirmMailCap    = 3
	deviceConfirmMailWindow = 15 * time.Minute
	// deviceConfirmSlots bounds the confirmation mails being written or sent at once (round 2 M2). When every slot is
	// busy a job is dropped and logged; the answer is the same.
	deviceConfirmSlots = 8
)

// cappedRefusal is the sec1 refusal of a sign-in whose email's cap is spent and which gets no confirmation mail: an
// installation admin's, or any sign-in when no mailer is configured.
const cappedRefusal = "too many failed sign-ins to this email; use Forgot password, " +
	"or sign in from a device you have used before"

// deviceConfirmAnswer is the answer to a sign-in that waits for the browser to be confirmed by email.
type deviceConfirmAnswer struct {
	Confirm string `json:"confirm"`
}

// confirmJob is the work for one matched user, prepared while the request is still in hand. It holds no request, so it
// runs after the answer has gone out.
type confirmJob struct {
	accountID, userID string
	nonceHash, hash   []byte
	secret            string
	short             bool
	now, expires      time.Time
	failed, capped    *store.AuditEntry
	attempt, result   *store.AuditEntry
}

// confirmCookieName is the nonce cookie's name. The __Host- prefix follows Secure: it is used over https, where the
// cookie is Secure, and plain http keeps the plain name, as it cannot carry a Secure cookie.
func (s *Server) confirmCookieName() string {
	if s.sessionCookie == "selfcloud_session" {
		return "selfcloud_confirm"
	}
	return "__Host-selfcloud_confirm"
}

// countVerify tells the test hook, if one is set, of a password check made for a device confirmation.
func (s *Server) countVerify(dummy bool) {
	if s.verifyHook != nil {
		s.verifyHook(dummy)
	}
}

// clockNow is the store's time, in the form the store keeps times: UTC, to the millisecond. Tests move it.
func (s *Server) clockNow() time.Time {
	return s.store.Clock().UTC().Truncate(time.Millisecond)
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
// sent. The answer is 202 with the confirmation whatever the password, admins included (round 2 M3). Past the per-email limit on password checks (login.capped) no password is checked and no
// mail is sent. Otherwise a matching password queues its mail, and the answer does not wait for it.
func (s *Server) confirmByEmail(w http.ResponseWriter, r *http.Request, candidates []*store.User, email, password string,
	remember *bool) error {
	ctx := r.Context()
	nonce := s.setConfirmCookie(w, r)
	short := remember != nil && !*remember
	// Two budgets are taken together, before any password check (round 4), or neither is taken. The first is per email
	// and client address (round 3): an attacker who spends it from its own address does not stop another address's mail.
	// The address is read as login.ip reads it, so proxy headers are trusted the same way. The NUL separates the two parts:
	// an address key never holds one. The second is the email's ceiling from all addresses together: it bounds the hashing
	// that many addresses could make, and the price is that an attacker with enough addresses spends it and blocks the real
	// user's mail for the hour. The device cookie still works meanwhile.
	if err := s.chargeAll(
		charge{"login.capped", addressKey(r) + "\x00" + emailKey(email)},
		charge{"login.capped.email", emailKey(email)},
	); err != nil {
		// Past either budget no password is checked. One dummy check and one audit row, as a miss writes, keep the answer
		// and its cost the same.
		s.countVerify(true)
		if err := auth.DummyVerify(ctx, password); err != nil {
			return err
		}
		if err := s.auditFailedLogin(r, candidates); err != nil {
			return err
		}
		writeJSON(w, http.StatusAccepted, deviceConfirmAnswer{Confirm: methodEmail})
		return nil
	}
	// An installation admin's password is checked as anyone's is, but an admin is never mailed a link (round 2 M3): a
	// link would tell the attacker the address is an admin's, and nothing else in the answer does. Its right password
	// is audited as a failed sign-in, as a miss is, since no sign-in completes here.
	var matches []*store.User
	for _, c := range candidates {
		s.countVerify(false)
		ok, err := s.matchPassword(ctx, c.PasswordHash, password)
		if err != nil {
			return err
		}
		if ok && c.AccountID != "" {
			matches = append(matches, c)
		}
	}
	if len(candidates) == 0 {
		s.countVerify(true)
		if err := auth.DummyVerify(ctx, password); err != nil {
			return err
		}
	}
	// Every answer writes exactly one audit row inside the request, before the answer (round 3): a miss as a failed
	// sign-in, a match as a confirmation requested, whatever the number of matched accounts. The mail work is queued and
	// is not in the request, so no path waits for a mail. Both writes are one transaction with one row.
	if len(matches) == 0 {
		if err := s.auditFailedLogin(r, candidates); err != nil {
			return err
		}
	} else if err := s.auditConfirmRequested(r, matches); err != nil {
		return err
	}
	for _, u := range matches {
		s.queueConfirmMail(r, u, nonce, short)
	}
	writeJSON(w, http.StatusAccepted, deviceConfirmAnswer{Confirm: methodEmail})
	return nil
}

// auditConfirmRequested writes the one synchronous audit row of a sign-in whose password matched, so that it costs what a
// miss costs (round 3). The row names the account when exactly one account matched, as a miss's does.
func (s *Server) auditConfirmRequested(r *http.Request, matches []*store.User) error {
	accountID, actor := "", ""
	if len(matches) == 1 {
		accountID, actor = matches[0].AccountID, matches[0].ID
	}
	return s.store.Write(r.Context(), func(tx *store.Tx) error {
		e := s.auditEntry(r, accountID, "user.login_confirm_requested", actor, nil)
		e.ActorKind, e.ActorID, e.Outcome = string(auth.KindUser), actor, "requested"
		if actor == "" {
			e.ActorKind = "anonymous"
		}
		return tx.InsertAudit(r.Context(), e)
	})
}

// queueConfirmMail prepares the link and its audit rows for a matched user and hands them to a bounded background job.
// When no slot is free the job is dropped and logged, and nothing is written: the answer is the same. At most one job
// is in flight per user (round 3): a second for the same user is dropped as a duplicate, with the same answer, so one
// caller cannot hold every slot through slow sends.
func (s *Server) queueConfirmMail(r *http.Request, u *store.User, nonce string, short bool) {
	select {
	case s.deviceSlots <- struct{}{}:
	default:
		s.log.Warn("device confirmation mail dropped: every slot is busy", "user", u.ID)
		return
	}
	s.confirmMu.Lock()
	if s.confirmBusy[u.ID] {
		s.confirmMu.Unlock()
		<-s.deviceSlots
		s.log.Warn("device confirmation mail dropped: one is already in flight for this user", "user", u.ID)
		return
	}
	s.confirmBusy[u.ID] = true
	s.confirmMu.Unlock()
	// The job gets what it needs from the request now, as a plain value, and never reads the request (round 5). Its
	// context is detached from the request's too: nothing in the job reads a request value through it.
	src := auditSourceOf(r)
	accountID, userID := u.AccountID, u.ID
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		defer func() {
			s.confirmMu.Lock()
			delete(s.confirmBusy, userID)
			s.confirmMu.Unlock()
			<-s.deviceSlots
		}()
		jctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		s.runConfirmJob(jctx, s.prepareConfirmJob(src, accountID, userID, nonce, short))
	}()
}

// prepareConfirmJob makes a confirmation's token, its link values and its audit rows. It runs in the job, not in the
// request, so the request does no match-only work (round 4). It takes the copied request source, not the request.
func (s *Server) prepareConfirmJob(src auditSource, accountID, userID, nonce string, short bool) *confirmJob {
	secret, hash := auth.NewToken()
	details := map[string]any{"purpose": mail.PurposeDeviceConfirm, "method": methodEmail}
	now := s.clockNow()
	// The action names are literals at each call, so the audit guards (decision 84) can find them in the source.
	return &confirmJob{
		accountID: accountID,
		userID:    userID,
		nonceHash: auth.HashToken(nonce),
		hash:      hash,
		secret:    secret,
		short:     short,
		now:       now,
		expires:   now.Add(s.cfg.Users.DeviceConfirmTTL.D()),
		failed:    actor(s.auditEntryFrom(src, accountID, "user.login_failed", userID, nil), userID, "failed"),
		capped:    actor(s.auditEntryFrom(src, accountID, "user.device_confirm_sent", userID, details), userID, "capped"),
		attempt:   actor(s.auditEntryFrom(src, accountID, "user.device_confirm_sent", userID, details), userID, "attempted"),
		result:    actor(s.auditEntryFrom(src, accountID, "user.device_confirm_sent", userID, details), userID, ""),
	}
}

// actor sets the actor and outcome of an audit entry made for a user's own sign-in attempt.
func actor(e *store.AuditEntry, userID, outcome string) *store.AuditEntry {
	e.ActorKind, e.ActorID, e.Outcome = string(auth.KindUser), userID, outcome
	return e
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

// runConfirmJob writes the link for a matched user and mails it. The link row and its attempted audit entry are written
// before the mail leaves, so no mail goes out without a row. A user who cannot sign in, or who is over the per-address
// mail cap, gets no link. The mail's token is given back when the write fails, so a failed write spends nothing from the
// cap (round 2 L1). The limit is in memory, as the other limits are.
func (s *Server) runConfirmJob(ctx context.Context, j *confirmJob) {
	var (
		held *rate.Reservation
		// heldAt is the time the token was reserved at. A refund must use it: rate.CancelAt restores nothing when the
		// time is later than the reservation's, so a fresh clock read would lose the token (round 8).
		heldAt time.Time
		target *store.User
	)
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		found, err := tx.User(ctx, j.accountID, j.userID)
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
			return tx.InsertAudit(ctx, j.failed)
		}
		// The cap counts the address, not each account that matched (round 2 L4).
		at := s.limitClock()
		res, err := s.limits["mail.device.address"].reserve(emailKey(found.Email), at)
		if err != nil {
			return tx.InsertAudit(ctx, j.capped)
		}
		held, heldAt = res, at
		row := &store.DeviceConfirm{ID: id.New("dcf"), UserID: found.ID, TokenHash: j.hash, NonceHash: j.nonceHash,
			Short: j.short, ExpiresAt: j.expires, CreatedAt: j.now}
		if err := tx.InsertDeviceConfirm(ctx, row); err != nil {
			return err
		}
		if err := tx.InsertAudit(ctx, j.attempt); err != nil {
			return err
		}
		target = found
		return nil
	})
	if err != nil {
		if held != nil {
			held.CancelAt(heldAt)
		}
		s.log.Error("cannot write the device confirmation", "user", j.userID, "error", err)
		return
	}
	if target == nil {
		return
	}
	it := &issuedToken{purpose: mail.PurposeDeviceConfirm, token: j.secret,
		link: s.tokenLink(s.cfg.Users.DeviceConfirmURLTemplate, j.secret), expires: j.expires}
	j.result.Outcome = "ok"
	if err := s.sendToken(ctx, target, it); err != nil {
		j.result.Outcome = "failed"
		s.log.Warn("cannot send device confirmation mail", "user", target.ID, "error", err)
	}
	if err := s.store.Write(ctx, func(tx *store.Tx) error { return tx.InsertAudit(ctx, j.result) }); err != nil {
		s.log.Error("cannot audit the device confirmation mail's result", "user", target.ID, "error", err)
	}
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
	now := s.clockNow()
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
		issuedAt = s.clockNow()
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
