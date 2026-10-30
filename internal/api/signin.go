// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

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

// newSession makes a session for u, begun now by r's client. It returns the cookie value and the
// session to store. Only the hash of the value is stored.
func (s *Server) newSession(r *http.Request, u *store.User, now time.Time) (string, *store.Session) {
	secret, hash := auth.NewToken()
	csrf, _ := auth.NewToken()
	info := infoFrom(r.Context())
	sess := &store.Session{IDHash: hash, UserID: u.ID, AccountID: u.AccountID, CSRFToken: csrf,
		CreatedAt: now, LastSeenAt: now, ExpiresAt: s.sessionExpiry(now, now),
		UserAgent: truncate(r.UserAgent(), 256)}
	if info != nil && info.ip.IsValid() {
		sess.IP = info.ip.String()
	}
	return secret, sess
}

// setSessionCookie sets the session cookie. HttpOnly keeps script away from it; SameSite=Lax
// keeps it off cross-site requests; Path=/ and the __Host- name (https only) pin it to this site.
func (s *Server) setSessionCookie(w http.ResponseWriter, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.sessionCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   int(s.cfg.Sessions.MaxAge.D().Seconds()),
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
	s.setSessionCookie(w, secret)
	writeJSON(w, http.StatusOK, signedIn{User: toUserJSON(u), CSRFToken: sess.CSRFToken})
}

// handleVerify completes a verification link: the user sets a password (if they have none) and
// becomes active, and the link stops working. The answer signs the user in.
func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) error {
	if err := s.rateLimit("verify.ip", clientKey(r)); err != nil {
		return err
	}
	var in struct {
		Token    string  `json:"token"`
		Password *string `json:"password"`
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
		secret, sess = s.newSession(r, u, at)
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
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	email := strings.TrimSpace(in.Email)
	if email == "" || in.Password == "" {
		return apierr.New(apierr.InvalidRequest, "email and password are required")
	}
	key := emailKey(email)
	if err := s.rateLimit("login.ip", clientKey(r)); err != nil {
		return err
	}
	if err := s.rateLimit("login.email", key); err != nil {
		return err
	}
	ctx := r.Context()
	candidates, err := s.store.FindUsersByEmail(ctx, key, loginScope(in.Account), maxLoginCandidates)
	if err != nil {
		return err
	}
	var matches []*store.User
	for _, c := range candidates {
		ok, err := s.matchPassword(ctx, c.PasswordHash, in.Password)
		if err != nil {
			return err
		}
		if ok {
			matches = append(matches, c)
		}
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
		secret, sess = s.newSession(r, u, store.Now())
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
	s.writeSignedIn(w, secret, u, sess)
	return nil
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
	accountID, actor := "", ""
	if len(candidates) == 1 {
		accountID, actor = candidates[0].AccountID, candidates[0].ID
	}
	err := s.store.Write(r.Context(), func(tx *store.Tx) error {
		e := s.auditEntry(r, accountID, "user.login_failed", actor, nil)
		e.ActorKind, e.ActorID, e.Outcome = string(auth.KindUser), actor, "failed"
		if actor == "" {
			e.ActorKind = "anonymous"
		}
		return tx.InsertAudit(r.Context(), e)
	})
	if err != nil {
		return err
	}
	return apierr.New(apierr.Unauthenticated, "wrong email or password")
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
		return tx.InsertAudit(r.Context(), s.auditEntry(r, p.AccountID, "user.logout", p.UserID, nil))
	})
	if err != nil {
		return err
	}
	s.pwFails.clear(sess.IDHash)
	s.clearSessionCookie(w)
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
	if err := s.rateLimit("forgot.ip", clientKey(r)); err != nil {
		return err
	}
	if err := s.rateLimit("forgot.email", emailKey(email)); err != nil {
		return err
	}
	ctx := r.Context()
	// Over the per-address or per-IP forgot budget, nothing is sent, and the answer stays 202: the
	// budget must not show whether the address is known.
	if email != "" && s.mailer != nil && s.chargeAll(charge{"mail.forgot.ip", clientKey(r)}) == nil {
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
	if err := s.rateLimit("reset.ip", clientKey(r)); err != nil {
		return err
	}
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
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
		at := store.Now()
		found.PasswordHash = hashed
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
		if err := tx.DeleteUserSessions(ctx, found.ID); err != nil {
			return err
		}
		u = found
		secret, sess = s.newSession(r, u, at)
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
	s.writeSignedIn(w, secret, u, sess)
	return nil
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
	writeJSON(w, http.StatusOK, out)
	return nil
}

// meOf is the answer for the signed-in user u: the user, the account (none for an admin) and the
// session's CSRF token. GET /me and PATCH /me both answer with it.
func (s *Server) meOf(r *http.Request, u *store.User) (meResponse, error) {
	out := meResponse{User: toUserJSON(u), CSRFToken: sessionOf(r).CSRFToken}
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
	if err := s.rateLimit("changepw.ip", clientKey(r)); err != nil {
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
		found.UpdatedAt = now
		if err := tx.UpdateUser(ctx, found); err != nil {
			return err
		}
		if err := tx.DeleteUserSessions(ctx, found.ID); err != nil {
			return err
		}
		secret, sess = s.newSession(r, found, now)
		if err := tx.InsertSession(ctx, sess); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, p.AccountID, "user.password_change", p.UserID, nil))
	})
	if err != nil {
		return err
	}
	s.setSessionCookie(w, secret)
	w.WriteHeader(http.StatusNoContent)
	return nil
}
