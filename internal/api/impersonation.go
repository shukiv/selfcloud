// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/recovery"
	"github.com/shukiv/selfcloud/internal/store"
)

// Installation admins and tenant users (design section 8, decision 41). An admin reaches a tenant user three
// ways, each of them only when admin.tenant_access is on: a reset link shown to the admin, a password the admin
// types, and sign-in-as, which starts a session of the user for impersonationTTL. The email reset is not behind
// the setting, since it reaches the user.

// impersonationTTL is the hard limit of a sign-in-as session. It is not renewed, and it is not configurable.
const impersonationTTL = time.Hour

// impersonationPath is where the admin's own session cookie is parked. The browser sends it only to the Go back
// route, so the impersonated pages never carry it.
const impersonationPath = "/api/v1/auth/impersonation"

// Reasons a refusal carries in its details, so the web interface can say why.
const (
	reasonImpersonation          = "impersonation"
	reasonPasswordChangeRequired = "password_change_required"
	reasonTenantAccessOff        = "tenant_access_off"
)

// impersonationRefused is the answer to a route an impersonated session may not reach.
func impersonationRefused() error {
	return apierr.New(apierr.Forbidden,
		"this is not allowed while you are signed in as another person; go back to the admin").
		With("reason", reasonImpersonation)
}

// checkImpersonation applies the classes of decision 41 to an authenticated caller: an impersonated session reaches
// only the routes classified as allowed, and a user whose password an admin typed reaches only the routes that
// admit it.
func (s *Server) checkImpersonation(rt route, p *auth.Principal) error {
	if p == nil {
		return nil
	}
	if p.Impersonator != "" && rt.imp != impAllow {
		return impersonationRefused()
	}
	if p.MustChangePassword && !rt.mustChange {
		return apierr.New(apierr.Forbidden, "change your password first").With("reason", reasonPasswordChangeRequired)
	}
	return nil
}

// signedInAsOther reports whether the browser's session cookie is an impersonation. A route that sets a session
// calls this, so it cannot replace the impersonation without ending it. A store error counts as yes.
func (s *Server) signedInAsOther(r *http.Request) bool {
	c, err := r.Cookie(s.sessionCookie)
	if err != nil || !auth.ValidToken(c.Value) {
		return false
	}
	sess, err := s.store.SessionByHash(r.Context(), auth.HashToken(c.Value))
	if errors.Is(err, store.ErrNotFound) {
		return false
	}
	return err != nil || sess.ImpersonatorID != ""
}

// tenantAccessOff is the refusal of the three ways when admin.tenant_access is off.
func (s *Server) tenantAccessGate() error {
	if s.settings().Admin.TenantAccess {
		return nil
	}
	return apierr.New(apierr.Forbidden, "an installation admin may not sign in as a user, set its password or give a "+
		"reset link: admin.tenant_access is off in the configuration").With("reason", reasonTenantAccessOff)
}

// requireAdminSession refuses any caller that is not an installation admin signed in with a session. A key does not
// make these: the admin's own password is part of each one.
func requireAdminSession(p *auth.Principal) error {
	if p == nil || p.Kind != auth.KindUser || p.Role != auth.RoleAdmin || p.Impersonator != "" {
		return apierr.New(apierr.Forbidden, "an installation admin signed in on its own session is required")
	}
	return nil
}

// The ways an admin's password is asked for, as the audit log names a refused attempt (admin.password_refused).
const (
	waySignInAs      = "sign_in_as"
	wayTypedPassword = "typed_password"
	wayResetLink     = "reset_link"
	wayCreateKey     = "create_key"
	wayOwnerLink     = "owner_link"
	wayEmailChange   = "email_change"
)

// confirmAdminPassword checks the admin's own password for a sign-in-as or a typed password. A wrong one is
// audited as admin.password_refused with its way (sign_in_as, typed_password or reset_link), and counted like a wrong
// current password: the fifth in a row ends the admin's session. A refused attempt is never a sign-in or a set password.
func (s *Server) confirmAdminPassword(w http.ResponseWriter, r *http.Request, p *auth.Principal, sess *store.Session,
	way, target, accountID, password string) error {
	// The wrong passwords are counted per signed-in session. A delegated token has none, so it is refused here rather
	// than counted under an empty key or dereferenced (sec1, decision 84).
	if sess == nil {
		return apierr.New(apierr.Forbidden, "this needs a signed-in session; a delegated token cannot confirm the admin's password").
			With("reason", "session_required")
	}
	ctx := r.Context()
	admin, err := s.store.User(ctx, "", p.ID)
	if errors.Is(err, store.ErrNotFound) {
		return apierr.New(apierr.Unauthenticated, "the session has ended; sign in again")
	}
	if err != nil {
		return err
	}
	ok, err := s.matchPassword(ctx, admin.PasswordHash, password)
	if err != nil {
		return err
	}
	if ok {
		s.pwFails.clear(sess.IDHash)
		return nil
	}
	ended := s.pwFails.add(sess.IDHash) >= maxPasswordFails
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		if ended {
			if err := tx.DeleteSession(ctx, sess.IDHash); err != nil {
				return err
			}
			if err := s.endAdminImpersonations(ctx, tx, p.ID, "admin_session_ended"); err != nil {
				return err
			}
		}
		e := s.auditEntry(r, accountID, "admin.password_refused", target, map[string]any{"way": way})
		e.Outcome = "failed"
		return tx.InsertAudit(ctx, e)
	})
	if err != nil {
		return err
	}
	if ended {
		s.pwFails.clear(sess.IDHash)
		s.clearSessionCookie(w)
		return apierr.New(apierr.Unauthenticated, "too many wrong passwords; the session has ended, sign in again")
	}
	return apierr.New(apierr.Forbidden, "your password is not correct")
}

// handleSetUserPassword sets a tenant user's password, typed by an installation admin. The user must change it at
// the next sign-in, every session of the user ends, and the audit entry holds no value (decision 41).
func (s *Server) handleSetUserPassword(w http.ResponseWriter, r *http.Request) error {
	p, sess := principalOf(r), sessionOf(r)
	if err := requireAdminSession(p); err != nil {
		return err
	}
	if err := s.tenantAccessGate(); err != nil {
		return err
	}
	acc, err := s.managedAccount(r)
	if err != nil {
		return err
	}
	var in struct {
		Password      string `json:"password"`
		AdminPassword string `json:"admin_password"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if err := checkPassword(in.Password); err != nil {
		return err
	}
	if err := s.confirmAdminPassword(w, r, p, sess, wayTypedPassword, r.PathValue("usr"), acc.ID,
		in.AdminPassword); err != nil {
		return err
	}
	ctx := r.Context()
	hashed, err := auth.HashPassword(ctx, in.Password)
	if err != nil {
		return err
	}
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		u, err := tx.User(ctx, acc.ID, r.PathValue("usr"))
		if errors.Is(err, store.ErrNotFound) {
			return notFound("user")
		}
		if err != nil {
			return err
		}
		if u.Status != store.UserActive {
			return apierr.New(apierr.Conflict, "the user must be verified and not disabled to set its password")
		}
		now := store.Now()
		u.PasswordHash = hashed
		u.MustChangePassword = true
		u.UpdatedAt = now
		if err := tx.UpdateUser(ctx, u); err != nil {
			return err
		}
		if err := tx.DeleteVerifications(ctx, u.ID, store.PurposeReset, ""); err != nil {
			return err
		}
		if err := s.endImpersonations(ctx, tx, u.ID, "password_set"); err != nil {
			return err
		}
		if err := tx.DeleteUserSessions(ctx, u.ID); err != nil {
			return err
		}
		if _, err := endUserTokens(ctx, tx, u.AccountID, u.ID); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "user.password_set", u.ID,
			map[string]any{"must_change_password": true}))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// handleSignInAs starts a session of a tenant user for the admin (decision 41). The admin's own session cookie is
// parked in the Go back cookie and the browser takes the new session. The new session is refused what the user may
// not have while impersonated: see the route classes in server.go.
func (s *Server) handleSignInAs(w http.ResponseWriter, r *http.Request) error {
	p, sess := principalOf(r), sessionOf(r)
	if err := requireAdminSession(p); err != nil {
		return err
	}
	if err := s.tenantAccessGate(); err != nil {
		return err
	}
	acc, err := s.managedAccount(r)
	if err != nil {
		return err
	}
	if acc.Status != store.AccountActive {
		return apierr.New(apierr.Conflict, "the account is suspended; resume it first")
	}
	var in struct {
		AdminPassword string `json:"admin_password"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if err := s.confirmAdminPassword(w, r, p, sess, waySignInAs, r.PathValue("usr"), acc.ID,
		in.AdminPassword); err != nil {
		return err
	}
	ctx := r.Context()
	now := time.Now()
	// The sign-in-as takes the admin's choice of box: an unticked admin's sign-in-as is a browser-session one too.
	secret, next := s.newSession(r, &store.User{ID: r.PathValue("usr"), AccountID: acc.ID}, now, sess.Short)
	var target *store.User
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		u, err := tx.User(ctx, acc.ID, r.PathValue("usr"))
		if errors.Is(err, store.ErrNotFound) {
			return notFound("user")
		}
		if err != nil {
			return err
		}
		switch {
		case u.Status != store.UserActive:
			return apierr.New(apierr.Conflict, "the user must be verified and not disabled to sign in as them")
		case u.MustChangePassword:
			return apierr.New(apierr.Conflict, "the user must change the password it was given first").
				With("reason", reasonPasswordChangeRequired)
		}
		admin, err := tx.User(ctx, "", p.ID)
		if err != nil {
			return err
		}
		// The session is the user's own, carrying the admin who began it. Its expiry is the earlier of the usual
		// idle and absolute limits and the impersonation's hard limit, and it is never renewed past that.
		next.UserID = u.ID
		next.AccountID = u.AccountID
		next.ImpersonatorID = admin.ID
		next.ImpersonatorSessionHash = sess.IDHash
		if hard := now.Add(impersonationTTL); next.ExpiresAt.After(hard) {
			next.ExpiresAt = hard
		}
		if err := tx.InsertSession(ctx, next); err != nil {
			return err
		}
		target = u
		e := s.auditEntry(r, acc.ID, "impersonation.start", u.ID, map[string]any{"admin": admin.Email})
		return tx.InsertAudit(ctx, e)
	})
	if err != nil {
		return err
	}
	s.pwFails.clear(sess.IDHash)
	parked, _ := r.Cookie(s.sessionCookie)
	s.setReturnCookie(w, parked.Value, sess.Short)
	s.setSessionCookie(w, secret, next.Short)
	writeJSON(w, http.StatusOK, signedIn{User: toUserJSON(target), CSRFToken: next.CSRFToken})
	return nil
}

// handleEndImpersonation ends the impersonation the browser is in and restores the admin's own session (decision 41).
// It is the only route an impersonated session reaches that changes the session. It checks its own CSRF token,
// because it must also work once the impersonation has passed its hour. A broken restore still ends the impersonation.
func (s *Server) handleEndImpersonation(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	c, err := r.Cookie(s.sessionCookie)
	if err != nil || !auth.ValidToken(c.Value) {
		return apierr.New(apierr.Unauthenticated, "the session has ended; sign in again")
	}
	if r.Header.Get("Origin") != s.origin {
		return apierr.New(apierr.Forbidden, "the request must come from this site")
	}
	hash := auth.HashToken(c.Value)
	sess, err := s.store.SessionByHash(ctx, hash)
	if errors.Is(err, store.ErrNotFound) {
		s.clearSessionCookie(w)
		s.clearReturnCookie(w)
		return apierr.New(apierr.Unauthenticated, "the session has ended; sign in again")
	}
	if err != nil {
		return err
	}
	if sess.ImpersonatorID == "" {
		return apierr.New(apierr.Conflict, "you are not signed in as another person")
	}
	got := r.Header.Get("X-CSRF-Token")
	if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(sess.CSRFToken)) != 1 {
		return apierr.New(apierr.Forbidden, "the CSRF token is missing or wrong")
	}
	reason := "returned"
	if !time.Now().Before(sess.CreatedAt.Add(impersonationTTL)) {
		reason = "expired"
	}
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		if _, err := tx.DeleteImpersonation(ctx, hash); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, impersonationEnd(sess, reason))
	})
	if err != nil {
		return err
	}
	s.pwFails.clear(hash)
	// The admin's session comes back only if the parked cookie is the one this impersonation parked.
	rc, rerr := r.Cookie(s.returnCookieName())
	s.clearReturnCookie(w)
	if rerr != nil || !auth.ValidToken(rc.Value) ||
		subtle.ConstantTimeCompare(auth.HashToken(rc.Value), sess.ImpersonatorSessionHash) != 1 {
		s.clearSessionCookie(w)
		return apierr.New(apierr.Unauthenticated, "the admin's session cannot be restored; sign in again")
	}
	admin, err := s.restoreAdminSession(ctx, sess.ImpersonatorSessionHash)
	if err != nil {
		s.clearSessionCookie(w)
		return err
	}
	s.setSessionCookie(w, rc.Value, admin.session.Short)
	writeJSON(w, http.StatusOK, signedIn{User: toUserJSON(admin.user), CSRFToken: admin.session.CSRFToken})
	return nil
}

// restoredAdmin is the admin's session and user as they are when the admin is restored.
type restoredAdmin struct {
	user    *store.User
	session *store.Session
}

// restoreAdminSession loads the admin's parked session, and refuses it unless it is still live and its user is an
// active admin.
func (s *Server) restoreAdminSession(ctx context.Context, hash []byte) (*restoredAdmin, error) {
	sess, err := s.store.SessionByHash(ctx, hash)
	if errors.Is(err, store.ErrNotFound) {
		return nil, apierr.New(apierr.Unauthenticated, "the admin's session has ended; sign in again")
	}
	if err != nil {
		return nil, err
	}
	if !s.sessionLive(sess, s.sessionClock()) || sess.ImpersonatorID != "" {
		return nil, apierr.New(apierr.Unauthenticated, "the admin's session has ended; sign in again")
	}
	u, err := s.store.User(ctx, "", sess.UserID)
	if err != nil || u.Role != auth.RoleAdmin || u.Status != store.UserActive {
		return nil, apierr.New(apierr.Unauthenticated, "the admin's session has ended; sign in again")
	}
	return &restoredAdmin{user: u, session: sess}, nil
}

// impersonated adds to an audit entry's details the admin who signed in as the person. The actor stays the person.
func impersonated(p *auth.Principal, details map[string]any) map[string]any {
	out := make(map[string]any, len(details)+1)
	for k, v := range details {
		out[k] = v
	}
	out["impersonated_by"] = p.Impersonator
	return out
}

// impersonationEnd is the audit entry for an impersonation's end. See recovery.ImpersonationEnd.
func impersonationEnd(sess *store.Session, reason string) *store.AuditEntry {
	return recovery.ImpersonationEnd(sess, reason)
}

// endImpersonations ends each impersonation in which userID is the person signed in as. See recovery.EndImpersonations.
func (s *Server) endImpersonations(ctx context.Context, tx *store.Tx, userID, reason string) error {
	return recovery.EndImpersonations(ctx, tx, userID, reason)
}

// endAccountImpersonations ends every impersonation of a member of the account, when the account is deleted (fu2,
// decision 74). It runs inside tx, as endImpersonations does, so a deleting account keeps no sign-in-as for the admin's
// Go back to restore from. Each end is audited under the admin who began it; the entry is the account's, so the
// deletion job removes it with the account's other entries (decision 48).
func (s *Server) endAccountImpersonations(ctx context.Context, tx *store.Tx, accountID, accountName, reason string) error {
	rows, err := tx.ImpersonationsIn(ctx, accountID)
	if err != nil {
		return err
	}
	for _, sess := range rows {
		if err := tx.DeleteSession(ctx, sess.IDHash); err != nil {
			return err
		}
		if err := tx.InsertAudit(ctx, impersonationEnd(sess, reason)); err != nil {
			return err
		}
		// The account's entries go with it at the job (decision 48), so the installation keeps this copy, named by
		// the account as account.delete is (decision 74).
		kept := impersonationEnd(sess, reason)
		kept.AccountID, kept.Target = "", accountName
		if err := tx.InsertAudit(ctx, kept); err != nil {
			return err
		}
	}
	return nil
}

// endAdminImpersonations ends every impersonation the admin began. See recovery.EndAdminImpersonations.
func (s *Server) endAdminImpersonations(ctx context.Context, tx *store.Tx, adminID, reason string) error {
	return recovery.EndAdminImpersonations(ctx, tx, adminID, reason)
}

// returnCookieName is the name of the parked admin session. A __Secure- prefix, not __Host-, because __Host- forbids a
// Path and this cookie has one.
func (s *Server) returnCookieName() string {
	if s.sessionCookie == "selfcloud_session" {
		return "selfcloud_admin"
	}
	return "__Secure-selfcloud_admin"
}

// setReturnCookie parks the admin's session cookie value. HttpOnly keeps script away from it, SameSite=Strict keeps
// it off every cross-site request, and the path sends it only to the Go back route.
func (s *Server) setReturnCookie(w http.ResponseWriter, value string, short bool) {
	var maxAge int
	if !short {
		maxAge = int(s.settings().Sessions.MaxAge.D().Seconds())
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.returnCookieName(),
		Value:    value,
		Path:     impersonationPath,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   s.sessionCookie != "selfcloud_session",
		SameSite: http.SameSiteStrictMode,
	})
}

// clearReturnCookie removes the parked admin cookie.
func (s *Server) clearReturnCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.returnCookieName(),
		Value:    "",
		Path:     impersonationPath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.sessionCookie != "selfcloud_session",
		SameSite: http.SameSiteStrictMode,
	})
}
