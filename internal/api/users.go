// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

type userJSON struct {
	ID          string     `json:"id"`
	ExternalID  *string    `json:"external_id"`
	Email       string     `json:"email"`
	DisplayName string     `json:"display_name"`
	Locale      string     `json:"locale"`
	Role        string     `json:"role"`
	Status      string     `json:"status"`
	VerifiedAt  *time.Time `json:"verified_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func toUserJSON(u *store.User) userJSON {
	out := userJSON{ID: u.ID, Email: u.Email, DisplayName: u.DisplayName, Locale: u.Locale, Role: u.Role,
		Status: u.Status, VerifiedAt: u.VerifiedAt, CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt}
	if u.ExternalID != "" {
		out.ExternalID = &u.ExternalID
	}
	return out
}

// userResponse is a user, with the verification made for it when one was.
type userResponse struct {
	userJSON
	Verification *verificationJSON `json:"verification,omitempty"`
}

// localePattern matches BCP 47-style tags such as "en" and "pt-BR", loosely.
var localePattern = regexp.MustCompile(`^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$`)

// writableAccount is managedAccount for a request that changes something: an account being
// deleted takes no changes.
func (s *Server) writableAccount(r *http.Request) (*store.Account, error) {
	acc, err := s.managedAccount(r)
	if err != nil {
		return nil, err
	}
	if acc.Status == store.AccountDeleting {
		return nil, apierr.New(apierr.Conflict, "the account is being deleted")
	}
	return acc, nil
}

// cleanEmail checks an address for sign-in. It must be a bare address, so a display name is
// refused and nothing with a line break can reach a mail header.
func cleanEmail(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", apierr.New(apierr.InvalidRequest, "email is required")
	}
	if len(s) > 254 {
		return "", apierr.New(apierr.InvalidRequest, "email is longer than 254 bytes")
	}
	if err := checkText("email", s); err != nil {
		return "", err
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Address != s {
		return "", apierr.New(apierr.InvalidRequest, "email must be a bare address such as name@example.com")
	}
	return s, nil
}

// cleanLocale checks a locale tag; "" means English.
func cleanLocale(s string) (string, error) {
	if s == "" {
		return "en", nil
	}
	if len(s) > 35 || !localePattern.MatchString(s) {
		return "", apierr.New(apierr.InvalidRequest, "locale must be a tag such as en or pt-BR, at most 35 characters")
	}
	return s, nil
}

// cleanOptionalLabel is cleanLabel for a field that may be empty.
func cleanOptionalLabel(field, s string, max int) (string, error) {
	if strings.TrimSpace(s) == "" {
		return "", nil
	}
	return cleanLabel(field, s, max)
}

func cleanRole(s string) (string, error) {
	switch s {
	case store.RoleOwner, store.RoleMember:
		return s, nil
	}
	return "", apierr.New(apierr.InvalidRequest, "role must be owner or member")
}

// handleCreateUser adds a user to an account. The verification method decides how the user
// proves the address: an email, a link the caller delivers, or nothing for a key that has
// verified the person itself. With an external_id, repeating the same request returns the
// existing user (200) instead of making another.
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.writableAccount(r)
	if err != nil {
		return err
	}
	var in struct {
		Email        string `json:"email"`
		DisplayName  string `json:"display_name"`
		Locale       string `json:"locale"`
		Role         string `json:"role"`
		ExternalID   string `json:"external_id"`
		Verification string `json:"verification"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	email, err := cleanEmail(in.Email)
	if err != nil {
		return err
	}
	displayName, err := cleanOptionalLabel("display_name", in.DisplayName, 200)
	if err != nil {
		return err
	}
	locale, err := cleanLocale(in.Locale)
	if err != nil {
		return err
	}
	role := store.RoleMember
	if in.Role != "" {
		if role, err = cleanRole(in.Role); err != nil {
			return err
		}
	}
	ext, err := cleanExternalID(in.ExternalID)
	if err != nil {
		return err
	}
	if err := s.checkVerificationMethod(r, in.Verification, linkTarget{role: role, status: store.UserPending}); err != nil {
		return err
	}

	ctx := r.Context()
	now := store.Now()
	key := emailKey(email)
	var (
		u       *store.User
		created bool
		issued  *issuedToken
	)
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		if ext != "" {
			existing, err := tx.UserByExternalID(ctx, acc.ID, ext)
			switch {
			case err == nil:
				if existing.EmailKey == key && existing.DisplayName == displayName &&
					existing.Locale == locale && existing.Role == role {
					u = existing
					return nil
				}
				return apierr.New(apierr.Conflict, "a user with this external_id exists with different fields").
					With("id", existing.ID)
			case !errors.Is(err, store.ErrNotFound):
				return err
			}
		}
		if _, err := tx.UserByEmail(ctx, acc.ID, key); err == nil {
			return apierr.New(apierr.Conflict, "a user with this email already exists in the account")
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}

		if in.Verification == methodEmail {
			if err := s.checkMailBudget(acc.ID, email); err != nil {
				return err
			}
		}
		u = &store.User{ID: id.New("usr"), AccountID: acc.ID, ExternalID: ext, Email: email, EmailKey: key,
			DisplayName: displayName, Locale: locale, Role: role, Status: store.UserPending,
			CreatedAt: now, UpdatedAt: now}
		if in.Verification == methodNone {
			u.Status = store.UserActive
			u.VerifiedAt = &now
		}
		if err := tx.InsertUser(ctx, u); errors.Is(err, store.ErrConflict) {
			return apierr.New(apierr.Conflict, "a user with this email or external_id already exists in the account")
		} else if err != nil {
			return err
		}
		created = true
		if err := tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "user.create", u.ID,
			map[string]any{"email": email, "role": role, "verification": in.Verification})); err != nil {
			return err
		}
		if in.Verification == methodNone {
			return nil
		}
		if issued, err = s.issueToken(ctx, tx, u, store.PurposeVerify, now); err != nil {
			return err
		}
		if in.Verification == methodLink {
			return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "verification.send", u.ID,
				map[string]any{"purpose": store.PurposeVerify, "method": methodLink}))
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !created {
		writeJSON(w, http.StatusOK, toUserJSON(u))
		return nil
	}
	out := userResponse{userJSON: toUserJSON(u)}
	if issued != nil {
		out.Verification = s.deliver(ctx, r, u, issued, in.Verification)
	}
	w.Header().Set("Location", "/api/v1/accounts/"+acc.ID+"/users/"+u.ID)
	writeJSON(w, http.StatusCreated, out)
	return nil
}

// handleListUsers lists an account's users, oldest first, optionally by external_id or email.
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.managedAccount(r)
	if err != nil {
		return err
	}
	page, err := pageParams(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	filter := store.UserFilter{ExternalID: q.Get("external_id")}
	if e := strings.TrimSpace(q.Get("email")); e != "" {
		filter.EmailKey = emailKey(e)
	}
	users, next, err := s.store.Users(r.Context(), acc.ID, filter, page)
	if err != nil {
		return storeErr(err)
	}
	out := listResponse[userJSON]{Items: []userJSON{}, NextCursor: next}
	for _, u := range users {
		out.Items = append(out.Items, toUserJSON(u))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) handleGetUser(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.managedAccount(r)
	if err != nil {
		return err
	}
	u, err := s.store.User(r.Context(), acc.ID, r.PathValue("usr"))
	if errors.Is(err, store.ErrNotFound) {
		return notFound("user")
	}
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toUserJSON(u))
	return nil
}

// handleUpdateUser changes the fields given. The email cannot change: it is refused as an unknown
// field. Activating a user requires that the user verified; disabling one ends its sessions.
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.writableAccount(r)
	if err != nil {
		return err
	}
	var in struct {
		DisplayName *string `json:"display_name"`
		Locale      *string `json:"locale"`
		Role        *string `json:"role"`
		ExternalID  *string `json:"external_id"` // "" clears it
		Status      *string `json:"status"`      // active or disabled
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	now := store.Now()
	var out *store.User
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		u, err := tx.User(ctx, acc.ID, r.PathValue("usr"))
		if errors.Is(err, store.ErrNotFound) {
			return notFound("user")
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
		if in.Role != nil {
			if u.Role, err = cleanRole(*in.Role); err != nil {
				return err
			}
			changed["role"] = u.Role
			// Grants are for members only. Leaving the member role ends them, so a later return
			// to member does not bring old shares back.
			if u.Role != store.RoleMember {
				n, err := tx.DeleteUserGrants(ctx, u.ID)
				if err != nil {
					return err
				}
				if n > 0 {
					if err := tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "grant.remove", u.ID,
						map[string]any{"reason": "role", "role": u.Role, "count": n})); err != nil {
						return err
					}
				}
			}
		}
		if in.ExternalID != nil {
			if u.ExternalID, err = cleanExternalID(*in.ExternalID); err != nil {
				return err
			}
			changed["external_id"] = u.ExternalID
		}
		if in.Status != nil {
			switch *in.Status {
			case store.UserActive:
				if u.VerifiedAt == nil {
					return apierr.New(apierr.Conflict, "the user has not verified the email address yet")
				}
				u.Status = store.UserActive
			case store.UserDisabled:
				u.Status = store.UserDisabled
				if err := tx.DeleteUserSessions(ctx, u.ID); err != nil {
					return err
				}
			default:
				return apierr.New(apierr.InvalidRequest, "status must be active or disabled")
			}
			changed["status"] = u.Status
		}
		u.UpdatedAt = now
		if err := tx.UpdateUser(ctx, u); errors.Is(err, store.ErrConflict) {
			return apierr.New(apierr.Conflict, "another user in the account has this external_id")
		} else if err != nil {
			return err
		}
		out = u
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "user.update", u.ID, changed))
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toUserJSON(out))
	return nil
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.writableAccount(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	usr := r.PathValue("usr")
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.DeleteUser(ctx, acc.ID, usr); errors.Is(err, store.ErrNotFound) {
			return notFound("user")
		} else if err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "user.delete", usr, nil))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// pendingConflict explains why a user is not waiting for verification.
func pendingConflict(status string) error {
	if status == store.UserActive {
		return apierr.New(apierr.Conflict, "the user is already verified")
	}
	return apierr.New(apierr.Conflict, "the user is disabled")
}

// handleSendVerification sends a pending user a fresh verification, or verifies the user outright
// for method "none" (keys only). Earlier verification tokens stop working.
func (s *Server) handleSendVerification(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.writableAccount(r)
	if err != nil {
		return err
	}
	var in struct {
		Method string `json:"method"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	// The check needs the target's role and status: a session may get a link only for a pending owner.
	target := linkTarget{}
	if found, err := s.store.User(r.Context(), acc.ID, r.PathValue("usr")); err == nil {
		target = linkTarget{role: found.Role, status: found.Status}
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if err := s.checkVerificationMethod(r, in.Method, target); err != nil {
		return err
	}
	ctx := r.Context()
	now := store.Now()
	var (
		u  *store.User
		it *issuedToken
	)
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		found, err := tx.User(ctx, acc.ID, r.PathValue("usr"))
		if errors.Is(err, store.ErrNotFound) {
			return notFound("user")
		}
		if err != nil {
			return err
		}
		if found.Status != store.UserPending {
			return pendingConflict(found.Status)
		}
		u = found
		if in.Method == methodEmail {
			if err := s.checkMailBudget(acc.ID, u.Email); err != nil {
				return err
			}
		}
		if in.Method == methodNone {
			u.Status = store.UserActive
			u.VerifiedAt = &now
			u.UpdatedAt = now
			if err := tx.UpdateUser(ctx, u); err != nil {
				return err
			}
			if err := tx.DeleteVerifications(ctx, u.ID, store.PurposeVerify, ""); err != nil {
				return err
			}
			return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "user.verify", u.ID, map[string]any{"method": methodNone}))
		}
		if it, err = s.issueToken(ctx, tx, u, store.PurposeVerify, now); err != nil {
			return err
		}
		if in.Method == methodLink {
			return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "verification.send", u.ID,
				map[string]any{"purpose": store.PurposeVerify, "method": methodLink}))
		}
		return nil
	})
	if err != nil {
		return err
	}
	out := userResponse{userJSON: toUserJSON(u)}
	if it != nil {
		out.Verification = s.deliver(ctx, r, u, it, in.Method)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// handlePasswordReset makes a reset link for an active user and delivers it like a verification.
// Earlier reset links stop working.
func (s *Server) handlePasswordReset(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.writableAccount(r)
	if err != nil {
		return err
	}
	var in struct {
		Method string `json:"method"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if in.Method != methodEmail && in.Method != methodLink {
		return apierr.New(apierr.InvalidRequest, "method must be email or link")
	}
	if err := s.checkVerificationMethod(r, in.Method, linkTarget{reset: true}); err != nil {
		return err
	}
	ctx := r.Context()
	now := store.Now()
	var (
		u  *store.User
		it *issuedToken
	)
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		found, err := tx.User(ctx, acc.ID, r.PathValue("usr"))
		if errors.Is(err, store.ErrNotFound) {
			return notFound("user")
		}
		if err != nil {
			return err
		}
		if found.Status != store.UserActive {
			return apierr.New(apierr.Conflict, "the user must be verified and not disabled to reset the password")
		}
		u = found
		if in.Method == methodEmail {
			if err := s.checkMailBudget(acc.ID, u.Email); err != nil {
				return err
			}
		}
		if it, err = s.issueToken(ctx, tx, u, store.PurposeReset, now); err != nil {
			return err
		}
		if in.Method == methodLink {
			return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "verification.send", u.ID,
				map[string]any{"purpose": store.PurposeReset, "method": methodLink}))
		}
		return nil
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, s.deliver(ctx, r, u, it, in.Method))
	return nil
}

// addUser inserts u inside tx, which must be a write transaction, and audits it under action. A
// verification method of "none" makes the user active at once; "email" and "link" issue a token
// for the caller to deliver after commit (see deliver). It returns the token, or nil for "none".
// Used wherever a user is created with another record, so each path verifies the same way.
func (s *Server) addUser(ctx context.Context, r *http.Request, tx *store.Tx, u *store.User, method, action string,
	now time.Time) (*issuedToken, error) {
	u.Status = store.UserPending
	if method == methodNone {
		u.Status = store.UserActive
		u.VerifiedAt = &now
	}
	if err := tx.InsertUser(ctx, u); errors.Is(err, store.ErrConflict) {
		return nil, apierr.New(apierr.Conflict, "a user with this email or external_id already exists")
	} else if err != nil {
		return nil, err
	}
	if err := tx.InsertAudit(ctx, s.auditEntry(r, u.AccountID, action, u.ID,
		map[string]any{"email": u.Email, "role": u.Role, "verification": method})); err != nil {
		return nil, err
	}
	if method == methodNone {
		return nil, nil
	}
	it, err := s.issueToken(ctx, tx, u, store.PurposeVerify, now)
	if err != nil {
		return nil, err
	}
	if method == methodLink {
		if err := tx.InsertAudit(ctx, s.auditEntry(r, u.AccountID, "verification.send", u.ID,
			map[string]any{"purpose": store.PurposeVerify, "method": methodLink})); err != nil {
			return nil, err
		}
	}
	return it, nil
}
