// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// maxLoginCandidates is how many accounts one email may be in and still be looked up at sign-in.
const maxLoginCandidates = 10

// adminChoiceID is the account value that selects an installation admin at sign-in, and the ID
// the account choice lists for an admin match.
const adminChoiceID = "admin"

// loginScope turns the account a sign-in names into a store scope. "" means any account.
func loginScope(account string) string {
	if account == adminChoiceID {
		return store.AdminScope
	}
	return account
}

// errBadLink answers a verification or reset link that is unknown, used, expired or malformed.
// One message covers all of them, so a link reveals nothing about its history.
var errBadLink = apierr.New(apierr.InvalidRequest, "the link is not valid or has expired")

// checkPassword checks a password someone is setting.
func checkPassword(p string) error {
	if err := auth.ValidPassword(p); err != nil {
		return apierr.New(apierr.InvalidRequest, err.Error())
	}
	return nil
}

// newSession makes a session for u, begun now by r's client. short marks a session the browser did not keep
// (decision 75). It returns the cookie value and the session to store. Only the hash of the value is stored.
func (s *Server) newSession(r *http.Request, u *store.User, now time.Time, short bool) (string, *store.Session) {
	secret, hash := auth.NewToken()
	csrf, _ := auth.NewToken()
	info := infoFrom(r.Context())
	sess := &store.Session{IDHash: hash, UserID: u.ID, AccountID: u.AccountID, CSRFToken: csrf,
		CreatedAt: now, LastSeenAt: now, ExpiresAt: s.sessionExpiry(now, now, short, u.AccountID == ""), Short: short,
		UserAgent: truncate(r.UserAgent(), 256)}
	if info != nil && info.ip.IsValid() {
		sess.IP = info.ip.String()
	}
	return secret, sess
}

// setSessionCookie sets the session cookie. HttpOnly keeps script away from it; SameSite=Lax
// keeps it off cross-site requests; Path=/ and the __Host- name (https only) pin it to this site.
// A kept session's cookie lasts sessions.max_age; a session the browser did not keep (short) is a
// browser-session cookie, with neither Max-Age nor Expires, so the browser drops it when it closes.
func (s *Server) setSessionCookie(w http.ResponseWriter, value string, short bool) {
	var maxAge int
	if !short {
		maxAge = int(s.cfg.Sessions.MaxAge.D().Seconds())
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.sessionCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   s.sessionCookie != "selfcloud_session",
		SameSite: http.SameSiteLaxMode,
	})
}

// clearSessionCookie expires the session cookie in the browser.
func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.sessionCookie != "selfcloud_session",
		SameSite: http.SameSiteLaxMode,
	})
}

// signedIn is the answer when a session begins: the user and the CSRF token to send back.
type signedIn struct {
	User      userJSON `json:"user"`
	CSRFToken string   `json:"csrf_token"`
}

// writeSignedIn sets the cookie and answers with the user. The CSRF token is in the body, so
// the page can make unsafe requests.
func (s *Server) writeSignedIn(w http.ResponseWriter, secret string, u *store.User, sess *store.Session) {
	s.setSessionCookie(w, secret, sess.Short)
	writeJSON(w, http.StatusOK, signedIn{User: toUserJSON(u), CSRFToken: sess.CSRFToken})
}

// handleVerify completes a verification link: the user sets a password (if they have none) and
// becomes active, and the link stops working. The answer signs the user in.
func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) error {
	if err := s.rateLimit("verify.ip", addressKey(r)); err != nil {
		return err
	}
	var in struct {
		Token    string  `json:"token"`
		Password *string `json:"password"`
		Confirm  bool    `json:"confirm"` // the browser's answer to signed_in_as_other (fu2, decision 74)
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if !auth.ValidToken(in.Token) {
		return errBadLink
	}
	password := ""
	if in.Password != nil {
		password = *in.Password
	}
	ctx := r.Context()
	var hashed string
	if password != "" {
		if err := checkPassword(password); err != nil {
			return err
		}
		var err error
		if hashed, err = auth.HashPassword(ctx, password); err != nil {
			return err
		}
	}
	now := time.Now()
	var (
		u      *store.User
		secret string
		sess   *store.Session
	)
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		v, err := tx.VerificationByHash(ctx, auth.HashToken(in.Token))
		if errors.Is(err, store.ErrNotFound) {
			return errBadLink
		}
		if err != nil {
			return err
		}
		if v.Purpose != store.PurposeVerify || v.UsedAt != nil || !now.Before(v.ExpiresAt) {
			return errBadLink
		}
		found, err := tx.UserByID(ctx, v.UserID)
		if errors.Is(err, store.ErrNotFound) {
			return errBadLink
		}
		if err != nil {
			return err
		}
		switch found.Status {
		case store.UserDisabled:
			return apierr.New(apierr.UserDisabled, "the user is disabled")
		case store.UserActive:
			return errBadLink
		}
		if hashed != "" {
			found.PasswordHash = hashed
		}
		if found.PasswordHash == "" {
			return apierr.New(apierr.InvalidRequest, "a password is required to verify this account")
		}
		if err := s.switchRefusal(ctx, tx, r, found, now, in.Confirm); err != nil {
			return err
		}
		at := store.Now()
		found.Status = store.UserActive
		if found.VerifiedAt == nil {
			found.VerifiedAt = &at
		}
		found.UpdatedAt = at
		if err := tx.UpdateUser(ctx, found); err != nil {
			return err
		}
		if err := tx.UseVerification(ctx, v.ID, at); errors.Is(err, store.ErrNotFound) {
			return errBadLink
		} else if err != nil {
			return err
		}
		if err := tx.DeleteVerifications(ctx, found.ID, store.PurposeVerify, v.ID); err != nil {
			return err
		}
		u = found
		if err := s.endBrowserSession(r, tx); err != nil {
			return err
		}
		// A verification link is the person's own choice of nothing: the session is not kept (decision 75).
		secret, sess = s.newSession(r, u, at, true)
		if err := tx.InsertSession(ctx, sess); err != nil {
			return err
		}
		e := s.auditEntry(r, u.AccountID, "user.verify", u.ID, map[string]any{"method": methodLink})
		e.ActorKind, e.ActorID = string(auth.KindUser), u.ID
		return tx.InsertAudit(ctx, e)
	})
	if err != nil {
		return err
	}
	s.writeSignedIn(w, secret, u, sess)
	return nil
}

// handleLogin signs a user in with email and password. The password is checked against every
// active, pending or disabled user with the email, so the answer takes about as long whether or
// not the email is known. An email in several accounts gets a 409 that lists them, but only once
// the password has matched; the client then retries with account.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Account  string `json:"account"`
		// Remember is the "Keep me signed in" box. Left out, the session is kept, as API clients have always had it.
		Remember *bool `json:"remember"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	email := strings.TrimSpace(in.Email)
	if email == "" || in.Password == "" {
		return apierr.New(apierr.InvalidRequest, "email and password are required")
	}
	key := emailKey(email)
	if err := s.rateLimit("login.ip", addressKey(r)); err != nil {
		return err
	}
	if err := s.rateLimit("login.email", emailAddressKey(r, email)); err != nil {
		return err
	}
	ctx := r.Context()
	now := s.limitClock()
	candidates, err := s.store.FindUsersByEmail(ctx, key, loginScope(in.Account), maxLoginCandidates)
	if err != nil {
		return err
	}
	// The holds of a sign-in (decision 90, as revised in round 2). A genuine device cookie's budget (login.device, 10 an hour
	// per cookie) covers only the user the cookie names, D, and D's password alone. With a trusted device the email's hold
	// (login.failed, decision 84) is taken too, whether or not other users have the email, and it covers every candidate
	// tested under it. A failure spends each hold taken. A hold is given back at its own time only when every candidate
	// tested under it matched; the device hold when D matched. Without a trusted device, or with its budget spent, D is an
	// ordinary candidate and the whole email is under the cap, as before.
	var deviceUser *store.User
	var deviceHold, failedHold *rate.Reservation
	if nonce, d, ok := s.trustedDevice(r, candidates); ok {
		if res, err := s.limits["login.device"].reserve(nonce, now); err == nil {
			deviceUser, deviceHold = d, res
		}
	}
	// tested is who the password is checked against. capSpent is set only when the cap is spent while D holds the device:
	// then D alone is tested, and the others (possibly none) get the cap's answer below (decision 90, corners 3 and 4).
	tested := candidates
	capSpent := false
	var others []*store.User
	if deviceUser == nil {
		res, err := s.limits["login.failed"].reserve(key, now)
		if err != nil {
			return s.cappedAnswer(w, r, candidates, email, in.Password, in.Remember)
		}
		failedHold = res
	} else if res, err := s.limits["login.failed"].reserve(key, now); err == nil {
		failedHold = res
	} else {
		capSpent = true
		tested = []*store.User{deviceUser}
		others = withoutUser(candidates, deviceUser)
	}
	var matches []*store.User
	for _, c := range tested {
		ok, err := s.matchPassword(ctx, c.PasswordHash, in.Password)
		if err != nil {
			return err
		}
		if ok {
			matches = append(matches, c)
		}
	}
	// Each hold is given back at the time it was taken (decision 89 (k); a later time restores nothing). The device hold
	// covers D alone, so it goes back when D matched. The email's hold goes back only when every candidate tested under it
	// matched, so a match on one user does not refund a failed test of another (round 2, finding 1).
	if deviceHold != nil && slices.Contains(matches, deviceUser) {
		deviceHold.CancelAt(now)
	}
	if failedHold != nil && len(matches) > 0 && len(matches) == len(tested) {
		failedHold.CancelAt(now)
	}
	if capSpent && len(matches) == 0 {
		// D's failure is audited against D, as a failed sign-in is (round 2, finding 3). The others get the cap's answer,
		// which writes its own row. Their passwords are not tested here, and the device's budget stays spent by D's failure.
		if err := s.auditFailedLogin(r, []*store.User{deviceUser}); err != nil {
			return err
		}
		return s.cappedAnswer(w, r, others, email, in.Password, in.Remember)
	}
	if len(candidates) == 0 {
		if err := auth.DummyVerify(ctx, in.Password); err != nil {
			return err
		}
	}
	if len(matches) == 0 {
		return s.failedLogin(r, candidates)
	}
	if len(matches) > 1 {
		return s.accountChoice(ctx, matches)
	}
	u := matches[0]

	var (
		stateErr error
		secret   string
		sess     *store.Session
		// issuedAt is the time the sign-in's transaction makes the session: the device cookie is stamped with it, so a
		// cut-off committed after this transaction is later and voids the cookie (sec1, decision 84, review F9).
		issuedAt time.Time
	)
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		// Re-read the user under the write lock. A reset, a disable or a deletion that committed
		// after the password check must not be undone by a session made from stale data.
		fresh, err := tx.User(ctx, u.AccountID, u.ID)
		switch {
		case errors.Is(err, store.ErrNotFound) || (err == nil && fresh.PasswordHash != u.PasswordHash):
			stateErr = apierr.New(apierr.Unauthenticated, "wrong email or password")
		case err != nil:
			return err
		default:
			u = fresh
		}
		// An admin has no account; a tenant user's account must be usable.
		var acc *store.Account
		if u.AccountID != "" {
			acc, err = tx.Account(ctx, u.AccountID)
			if errors.Is(err, store.ErrNotFound) {
				stateErr = apierr.New(apierr.Unauthenticated, "wrong email or password")
			} else if err != nil {
				return err
			}
		}
		switch {
		case stateErr != nil:
		case u.Status == store.UserPending:
			stateErr = apierr.New(apierr.UserNotVerified, "the user has not verified the email address yet")
		case u.Status == store.UserDisabled:
			stateErr = apierr.New(apierr.UserDisabled, "the user is disabled")
		case acc != nil && acc.Status == store.AccountSuspended:
			stateErr = apierr.New(apierr.AccountSuspended, "the account is suspended")
		case acc != nil && acc.Status == store.AccountDeleting:
			stateErr = apierr.New(apierr.Unauthenticated, "wrong email or password")
		}
		if stateErr != nil {
			// The password was right, but the account cannot be used. Audit the refusal.
			e := s.auditEntry(r, u.AccountID, "user.login_failed", u.ID, nil)
			e.ActorKind, e.ActorID, e.Outcome = string(auth.KindUser), u.ID, "failed"
			return tx.InsertAudit(ctx, e)
		}
		// Signing in replaces any session this browser already had.
		if c, err := r.Cookie(s.sessionCookie); err == nil && auth.ValidToken(c.Value) {
			if err := tx.DeleteSession(ctx, auth.HashToken(c.Value)); err != nil {
				return err
			}
		}
		short := in.Remember != nil && !*in.Remember
		issuedAt = store.Now()
		secret, sess = s.newSession(r, u, issuedAt, short)
		if err := tx.InsertSession(ctx, sess); err != nil {
			return err
		}
		e := s.auditEntry(r, u.AccountID, "user.login", u.ID, nil)
		e.ActorKind, e.ActorID = string(auth.KindUser), u.ID
		return tx.InsertAudit(ctx, e)
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
	s.writeSignedIn(w, secret, u, sess)
	return nil
}

// cappedAnswer answers a sign-in whose email's cap is spent, for the candidates the cap covers. With a mailer the browser is
// confirmed by email instead of refused (sec2, decision 86). Without one there is no link, so the refusal stays (sec1,
// decision 84).
func (s *Server) cappedAnswer(w http.ResponseWriter, r *http.Request, candidates []*store.User, email, password string,
	remember *bool) error {
	if s.mailer != nil {
		return s.confirmByEmail(w, r, candidates, email, password, remember)
	}
	return apierr.New(apierr.RateLimited, cappedRefusal)
}

// withoutUser returns users without drop, in their order (decision 90).
func withoutUser(users []*store.User, drop *store.User) []*store.User {
	out := make([]*store.User, 0, len(users))
	for _, u := range users {
		if u.ID != drop.ID {
			out = append(out, u)
		}
	}
	return out
}

// emailAddressKey is the key of the per-email sign-in and reset limits: the email and the client
// address. One address's tries use up only its own budget for the email, so a guesser elsewhere
// cannot lock the user out (sec1, decision 84). The address never contains "|", so the key is
// unambiguous.
func emailAddressKey(r *http.Request, email string) string {
	return emailKey(email) + "|" + addressKey(r)
}

// matchPassword verifies password against a stored hash. A user with no password yet, which
// includes every pending user who has not set one, matches nothing; it still costs a hash, so
// the timing does not show it.
func (s *Server) matchPassword(ctx context.Context, hash, password string) (bool, error) {
	if hash == "" {
		return false, auth.DummyVerify(ctx, password)
	}
	return auth.VerifyPassword(ctx, hash, password)
}

// failedLogin answers a sign-in whose password matched no one. It is audited against the account
// when exactly one user has that email.
func (s *Server) failedLogin(r *http.Request, candidates []*store.User) error {
	if err := s.auditFailedLogin(r, candidates); err != nil {
		return err
	}
	return apierr.New(apierr.Unauthenticated, "wrong email or password")
}

// auditFailedLogin writes the audit entry of a sign-in whose password matched no one.
func (s *Server) auditFailedLogin(r *http.Request, candidates []*store.User) error {
	accountID, actor := "", ""
	if len(candidates) == 1 {
		accountID, actor = candidates[0].AccountID, candidates[0].ID
	}
	return s.store.Write(r.Context(), func(tx *store.Tx) error {
		e := s.auditEntry(r, accountID, "user.login_failed", actor, nil)
		e.ActorKind, e.ActorID, e.Outcome = string(auth.KindUser), actor, "failed"
		if actor == "" {
			e.ActorKind = "anonymous"
		}
		return tx.InsertAudit(r.Context(), e)
	})
}

// accountChoice answers a sign-in whose email is in several accounts, and whose password
// matched each of them. The client shows the accounts and signs in again with one.
func (s *Server) accountChoice(ctx context.Context, matches []*store.User) error {
	type choice struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	choices := []choice{}
	for _, m := range matches {
		if m.AccountID == "" {
			choices = append(choices, choice{ID: adminChoiceID, Name: "Installation admin"})
			continue
		}
		acc, err := s.store.Account(ctx, m.AccountID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		choices = append(choices, choice{ID: acc.ID, Name: acc.Name})
	}
	return apierr.New(apierr.Conflict, "the email is in several accounts; sign in again with one of them").
		With("accounts", choices)
}

// handleLogout ends the session and clears the cookie.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	sess := sessionOf(r)
	err := s.store.Write(r.Context(), func(tx *store.Tx) error {
		if err := tx.DeleteSession(r.Context(), sess.IDHash); err != nil {
			return err
		}
		// An admin who signs out ends the sign-ins-as it began (decision 41).
		if err := s.endAdminImpersonations(r.Context(), tx, p.UserID, "admin_signed_out"); err != nil {
			return err
		}
		return tx.InsertAudit(r.Context(), s.auditEntry(r, p.AccountID, "user.logout", p.UserID, nil))
	})
	if err != nil {
		return err
	}
	s.pwFails.clear(sess.IDHash)
	s.clearSessionCookie(w)
	// A pending confirmation link cannot be redeemed in this browser after sign-out (round 3).
	s.clearConfirmCookie(w)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// handleForgotPassword mails a reset link to the active users with the email. It always answers
// 202 and sends nothing that depends on whether the email is known. The mail goes out in the
// background, so its speed cannot show either.
func (s *Server) handleForgotPassword(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email   string `json:"email"`
		Account string `json:"account"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	email := strings.TrimSpace(in.Email)
	if err := s.rateLimit("forgot.ip", addressKey(r)); err != nil {
		return err
	}
	if err := s.rateLimit("forgot.email", emailAddressKey(r, email)); err != nil {
		return err
	}
	ctx := r.Context()
	// Over the per-address or per-IP forgot budget, nothing is sent, and the answer stays 202: the
	// budget must not show whether the address is known.
	if email != "" && s.mailer != nil && s.chargeAll(charge{"mail.forgot.ip", addressKey(r)}) == nil {
		if err := s.sendResetLinks(ctx, r, emailKey(email), loginScope(in.Account)); err != nil {
			return err
		}
	}
	w.WriteHeader(http.StatusAccepted)
	return nil
}

// sendResetLinks makes a reset link for each active user with the email key, and sends each one
// in the background.
func (s *Server) sendResetLinks(ctx context.Context, r *http.Request, key, accountID string) error {
	users, err := s.store.FindUsersByEmail(ctx, key, accountID, maxLoginCandidates)
	if err != nil {
		return err
	}
	type delivery struct {
		u  *store.User
		it *issuedToken
	}
	var out []delivery
	now := store.Now()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		for _, u := range users {
			if u.Status != store.UserActive {
				continue
			}
			// Over the mail budget, this address gets no mail. The answer is the same 202, so the
			// limit cannot show whether the address is known.
			// The first reset an address gets in an hour is reserved: it is charged to its own
			// address buckets only. Repeats are also charged to the account (admins have no account
			// and share the "admin" key), so an account's users share a cap on repeats, and one caller
			// with many addresses cannot use the cap up and block every user's first reset.
			if err := s.chargeAll(charge{"mail.forgot.first", emailKey(u.Email)},
				charge{"mail.forgot.target", emailKey(u.Email)}); err != nil {
				accountKey := u.AccountID
				if accountKey == "" {
					accountKey = "admin"
				}
				if err := s.chargeAll(charge{"mail.forgot.target", emailKey(u.Email)},
					charge{"mail.forgot.account", accountKey}); err != nil {
					continue
				}
			}
			it, err := s.issueToken(ctx, tx, u, store.PurposeReset, now)
			if err != nil {
				return err
			}
			e := s.auditEntry(r, u.AccountID, "verification.send", u.ID,
				map[string]any{"purpose": store.PurposeReset, "method": methodEmail})
			if err := tx.InsertAudit(ctx, e); err != nil {
				return err
			}
			out = append(out, delivery{u, it})
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, d := range out {
		s.bg.Add(1)
		go func() {
			defer s.bg.Done()
			mctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
			defer cancel()
			if err := s.sendToken(mctx, d.u, d.it); err != nil {
				s.log.Warn("cannot send reset mail", "user", d.u.ID, "error", err)
			}
		}()
	}
	return nil
}

// handleResetPassword completes a reset link: the password changes, every session of the user
// ends, and the caller gets a new session.
func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) error {
	if err := s.rateLimit("reset.ip", addressKey(r)); err != nil {
		return err
	}
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
		Confirm  bool   `json:"confirm"` // the browser's answer to signed_in_as_other (fu2, decision 74)
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if !auth.ValidToken(in.Token) {
		return errBadLink
	}
	if err := checkPassword(in.Password); err != nil {
		return err
	}
	ctx := r.Context()
	hashed, err := auth.HashPassword(ctx, in.Password)
	if err != nil {
		return err
	}
	now := time.Now()
	var (
		u      *store.User
		secret string
		sess   *store.Session
		// cut is the user's cut-off this reset sets; the device cookie it makes is stamped just after it (review F9).
		cut time.Time
	)
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		v, err := tx.VerificationByHash(ctx, auth.HashToken(in.Token))
		if errors.Is(err, store.ErrNotFound) {
			return errBadLink
		}
		if err != nil {
			return err
		}
		if v.Purpose != store.PurposeReset || v.UsedAt != nil || !now.Before(v.ExpiresAt) {
			return errBadLink
		}
		found, err := tx.UserByID(ctx, v.UserID)
		if errors.Is(err, store.ErrNotFound) {
			return errBadLink
		}
		if err != nil {
			return err
		}
		switch found.Status {
		case store.UserDisabled:
			return apierr.New(apierr.UserDisabled, "the user is disabled")
		case store.UserPending:
			return errBadLink
		}
		if err := s.switchRefusal(ctx, tx, r, found, now, in.Confirm); err != nil {
			return err
		}
		at := store.Now()
		found.PasswordHash = hashed
		found.MustChangePassword = false
		found.UpdatedAt = at
		if err := tx.UpdateUser(ctx, found); err != nil {
			return err
		}
		if err := tx.UseVerification(ctx, v.ID, at); errors.Is(err, store.ErrNotFound) {
			return errBadLink
		} else if err != nil {
			return err
		}
		if err := tx.DeleteVerifications(ctx, found.ID, store.PurposeReset, ""); err != nil {
			return err
		}
		if err := s.endImpersonations(ctx, tx, found.ID, "password_reset"); err != nil {
			return err
		}
		if err := s.endAdminImpersonations(ctx, tx, found.ID, "admin_sessions_ended"); err != nil {
			return err
		}
		if err := tx.DeleteUserSessions(ctx, found.ID); err != nil {
			return err
		}
		c, err := endUserTokens(ctx, tx, found.AccountID, found.ID)
		if err != nil {
			return err
		}
		cut = c
		u = found
		if err := s.endBrowserSession(r, tx); err != nil {
			return err
		}
		// A reset link is not the person's choice of a box, so the session is not kept (decision 75).
		secret, sess = s.newSession(r, u, at, true)
		if err := tx.InsertSession(ctx, sess); err != nil {
			return err
		}
		e := s.auditEntry(r, u.AccountID, "user.password_reset", u.ID, nil)
		e.ActorKind, e.ActorID = string(auth.KindUser), u.ID
		return tx.InsertAudit(ctx, e)
	})
	if err != nil {
		return err
	}
	// The reset's own cut-off voids the cookies made before it; the new cookie is stamped a millisecond after it, in the
	// reset's transaction, and nothing is read after the commit (sec1, decision 84, review F9).
	if err := s.setDeviceCookie(w, u.ID, cut.Add(time.Millisecond)); err != nil {
		return err
	}
	s.writeSignedIn(w, secret, u, sess)
	return nil
}

// switchRefusal is the 409 that verify and reset give when the browser holds a session of another user and has not
// confirmed the switch: the answer a sign-in link gives too (fu2, decision 74). Nothing is written here, so the link
// stays live for the prompt's second answer, which carries confirm.
func (s *Server) switchRefusal(ctx context.Context, tx *store.Tx, r *http.Request, target *store.User, now time.Time,
	confirm bool) error {
	if confirm {
		return nil
	}
	cur, err := s.signedInUser(ctx, tx, r, now)
	if err != nil {
		return err
	}
	if cur != nil && cur.ID != target.ID {
		return apierr.New(apierr.Conflict, "this browser is signed in as another user").
			With("reason", "signed_in_as_other").
			With("current", userNameOf(cur)).
			With("target", userNameOf(target))
	}
	return nil
}

// endBrowserSession ends the session this browser holds, if it holds one, as the sign-in that replaces it does: a
// password sign-in and a sign-in link end it the same way (fu2, decision 74).
func (s *Server) endBrowserSession(r *http.Request, tx *store.Tx) error {
	c, err := r.Cookie(s.sessionCookie)
	if err != nil || !auth.ValidToken(c.Value) {
		return nil
	}
	return tx.DeleteSession(r.Context(), auth.HashToken(c.Value))
}

// meResponse is the signed-in user, their account and the CSRF token.
type meResponse struct {
	User      userJSON    `json:"user"`
	Account   *accountRef `json:"account"` // null for an installation admin
	CSRFToken string      `json:"csrf_token"`
	// Storage is the caller's My files against its limit. Absent for an installation admin.
	Storage *storageJSON `json:"storage,omitempty"`
	// PersonalFolderID is the caller's "My files" folder. Null for an installation admin.
	PersonalFolderID *string `json:"personal_folder_id"`
	// PublicLinks is the account's switch for links that anyone can open. False for an installation admin.
	PublicLinks bool `json:"public_links"`
	// MustChangePassword is set when an admin typed this user's password. Until the user changes it, only the
	// change-password route and this one are open to the user (decision 41).
	MustChangePassword bool `json:"must_change_password"`
	// Impersonation is set while an installation admin is signed in as this user. EndsAt is the hard limit.
	Impersonation *impersonationJSON `json:"impersonation,omitempty"`
	// TenantAccess and MailConfigured are set for an installation admin only. The admin pages read them to show the
	// ways in to a tenant user; nobody else learns them (decision 41).
	TenantAccess   *bool `json:"tenant_access,omitempty"`
	MailConfigured *bool `json:"mail_configured,omitempty"`
}

// impersonationJSON describes a sign-in-as session to the person it signed in as.
type impersonationJSON struct {
	EndsAt time.Time `json:"ends_at"`
}

type accountRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	ctx := r.Context()
	u, err := s.store.User(ctx, p.AccountID, p.UserID)
	if errors.Is(err, store.ErrNotFound) {
		return apierr.New(apierr.Unauthenticated, "the session has ended; sign in again")
	}
	if err != nil {
		return err
	}
	out, err := s.meOf(r, u)
	if err != nil {
		return err
	}
	// The Storage page asks for the breakdown of My files (decision 79); other readers of /me do not pay for it.
	if r.URL.Query().Get("storage") == "breakdown" && out.Storage != nil && out.PersonalFolderID != nil {
		b, err := s.store.PersonalBreakdown(ctx, p.AccountID, *out.PersonalFolderID)
		if err != nil {
			return err
		}
		out.Storage.Breakdown = &breakdownJSON{VersionsBytes: b.VersionsBytes, VersionsFiles: b.VersionsFiles,
			TrashBytes: b.TrashBytes, TrashFiles: b.TrashFiles}
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// meOf is the answer for the signed-in user u: the user, the account (none for an admin) and the
// session's CSRF token. GET /me and PATCH /me both answer with it.
func (s *Server) meOf(r *http.Request, u *store.User) (meResponse, error) {
	sess := sessionOf(r)
	out := meResponse{User: toUserJSON(u), CSRFToken: sess.CSRFToken, MustChangePassword: u.MustChangePassword}
	if sess.ImpersonatorID != "" {
		out.Impersonation = &impersonationJSON{EndsAt: sess.CreatedAt.Add(impersonationTTL)}
	}
	if u.AccountID == "" && u.Role == store.RoleAdmin {
		tenantAccess, mail := s.cfg.Admin.TenantAccess, s.mailer != nil
		out.TenantAccess, out.MailConfigured = &tenantAccess, &mail
	}
	if u.AccountID != "" {
		acc, err := s.store.Account(r.Context(), u.AccountID)
		if err != nil {
			return meResponse{}, err
		}
		out.Account = &accountRef{ID: acc.ID, Name: acc.Name}
		on, err := s.store.SharingPolicy(r.Context(), acc.ID)
		if err != nil {
			return meResponse{}, err
		}
		out.PublicLinks = on
		pf, err := s.store.PersonalFolder(r.Context(), u.AccountID, u.ID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return meResponse{}, err
		}
		if err == nil {
			out.PersonalFolderID = &pf.ID
		}
		st, err := s.myStorage(r, u.AccountID, pfOrNil(pf, err))
		if err != nil {
			return meResponse{}, err
		}
		out.Storage = &st
	}
	return out, nil
}

// handleUpdateMe changes the signed-in user's own display name and locale. Each field is optional,
// and each is checked as it is on users. The change is audited as user.update. The answer has the
// same shape as GET /me.
func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	var in struct {
		DisplayName *string `json:"display_name"`
		Locale      *string `json:"locale"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	var out *store.User
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		u, err := tx.User(ctx, p.AccountID, p.UserID)
		if errors.Is(err, store.ErrNotFound) {
			return apierr.New(apierr.Unauthenticated, "the session has ended; sign in again")
		}
		if err != nil {
			return err
		}
		changed := map[string]any{}
		if in.DisplayName != nil {
			if u.DisplayName, err = cleanOptionalLabel("display_name", *in.DisplayName, 200); err != nil {
				return err
			}
			changed["display_name"] = u.DisplayName
		}
		if in.Locale != nil {
			if u.Locale, err = cleanLocale(*in.Locale); err != nil {
				return err
			}
			changed["locale"] = u.Locale
		}
		u.UpdatedAt = store.Now()
		if err := tx.UpdateUser(ctx, u); err != nil {
			return err
		}
		out = u
		return tx.InsertAudit(ctx, s.auditEntry(r, p.AccountID, "user.update", p.UserID, changed))
	})
	if err != nil {
		return err
	}
	me, err := s.meOf(r, out)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, me)
	return nil
}

// maxPasswordFails is how many wrong current passwords in a row end the session that gave them.
const maxPasswordFails = 5

// passwordFailWindow is how long a session's count is kept after its last wrong password. Counts
// of sessions that ended by reset, disable, deletion or expiry are dropped once this old, so the
// map does not grow without bound.
const passwordFailWindow = 15 * time.Minute

// passwordFails counts wrong current passwords per session. A right one clears the count. The
// count lives in memory, so a restart clears it, as it clears the rate limits.
type passwordFails struct {
	mu        sync.Mutex
	n         map[string]passwordFail // by session hash
	lastSweep time.Time
}

type passwordFail struct {
	count int
	last  time.Time
}

// add records one wrong password for the session and returns the count so far. A count older
// than passwordFailWindow starts again at one.
func (f *passwordFails) add(hash []byte) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	if f.n == nil {
		f.n = map[string]passwordFail{}
	}
	if now.Sub(f.lastSweep) > time.Minute {
		for k, v := range f.n {
			if now.Sub(v.last) > passwordFailWindow {
				delete(f.n, k)
			}
		}
		f.lastSweep = now
	}
	e := f.n[string(hash)]
	if now.Sub(e.last) > passwordFailWindow {
		e.count = 0
	}
	e.count++
	e.last = now
	f.n[string(hash)] = e
	return e.count
}

// clear forgets the session's count.
func (f *passwordFails) clear(hash []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.n, string(hash))
}

// failedPasswordChange answers a wrong current password. It is audited. The fifth in a row ends
// the session and clears its cookie, so a stolen session cannot keep guessing.
func (s *Server) failedPasswordChange(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	sess := sessionOf(r)
	ended := s.pwFails.add(sess.IDHash) >= maxPasswordFails
	err := s.store.Write(r.Context(), func(tx *store.Tx) error {
		if ended {
			if err := tx.DeleteSession(r.Context(), sess.IDHash); err != nil {
				return err
			}
			if err := s.endAdminImpersonations(r.Context(), tx, p.UserID, "admin_session_ended"); err != nil {
				return err
			}
		}
		e := s.auditEntry(r, p.AccountID, "user.password_change", p.UserID, nil)
		e.ActorKind, e.ActorID, e.Outcome = string(auth.KindUser), p.UserID, "failed"
		return tx.InsertAudit(r.Context(), e)
	})
	if err != nil {
		return err
	}
	if ended {
		s.pwFails.clear(sess.IDHash)
		s.clearSessionCookie(w)
		return apierr.New(apierr.Unauthenticated, "too many wrong passwords; the session has ended, sign in again")
	}
	return apierr.New(apierr.Forbidden, "the current password is not correct")
}

// handleChangePassword sets a new password for the signed-in user after checking the current
// one. Every session of the user ends, and the caller gets a new one. The answer is 204, so the
// page reads the new CSRF token from GET /me.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	var in struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if in.CurrentPassword == "" {
		return apierr.New(apierr.InvalidRequest, "current_password is required")
	}
	if err := checkPassword(in.NewPassword); err != nil {
		return err
	}
	// The limits come before any hash, so guessing the current password is slowed per user and
	// per address, whichever session makes the attempt.
	if err := s.rateLimit("changepw.user", p.UserID); err != nil {
		return err
	}
	if err := s.rateLimit("changepw.ip", addressKey(r)); err != nil {
		return err
	}
	ctx := r.Context()
	u, err := s.store.User(ctx, p.AccountID, p.UserID)
	if errors.Is(err, store.ErrNotFound) {
		return apierr.New(apierr.Unauthenticated, "the session has ended; sign in again")
	}
	if err != nil {
		return err
	}
	ok, err := s.matchPassword(ctx, u.PasswordHash, in.CurrentPassword)
	if err != nil {
		return err
	}
	if !ok {
		return s.failedPasswordChange(w, r)
	}
	s.pwFails.clear(sessionOf(r).IDHash)
	hashed, err := auth.HashPassword(ctx, in.NewPassword)
	if err != nil {
		return err
	}
	var (
		secret string
		sess   *store.Session
	)
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		// The password was checked outside this transaction. Under the write lock, the user must
		// still be the one checked, with the same hash and active, or the change is refused.
		found, err := tx.User(ctx, p.AccountID, p.UserID)
		if errors.Is(err, store.ErrNotFound) || (err == nil && (found.PasswordHash != u.PasswordHash ||
			found.Status != store.UserActive)) {
			return apierr.New(apierr.Unauthenticated, "the session has ended; sign in again")
		}
		if err != nil {
			return err
		}
		now := store.Now()
		found.PasswordHash = hashed
		found.MustChangePassword = false
		found.UpdatedAt = now
		if err := tx.UpdateUser(ctx, found); err != nil {
			return err
		}
		if err := s.endImpersonations(ctx, tx, found.ID, "password_change"); err != nil {
			return err
		}
		// An admin who changes its own password signs out its sign-ins-as, too (decision 41).
		if err := s.endAdminImpersonations(ctx, tx, found.ID, "admin_password_change"); err != nil {
			return err
		}
		if err := tx.DeleteUserSessions(ctx, found.ID); err != nil {
			return err
		}
		if _, err := endUserTokens(ctx, tx, found.AccountID, found.ID); err != nil {
			return err
		}
		// The new session keeps the choice of the one it replaces (decision 75).
		secret, sess = s.newSession(r, found, now, sessionOf(r).Short)
		if err := tx.InsertSession(ctx, sess); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, p.AccountID, "user.password_change", p.UserID, nil))
	})
	if err != nil {
		return err
	}
	s.setSessionCookie(w, secret, sess.Short)
	w.WriteHeader(http.StatusNoContent)
	return nil
}
