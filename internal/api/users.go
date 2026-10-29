// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/names"
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
		if err := insertPersonalFolder(ctx, tx, u, now); err != nil {
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
		// An active owner may not stop being one, or be disabled, while it is the last active owner.
		wasActiveOwner := u.Role == store.RoleOwner && u.Status == store.UserActive
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
			// Shares are for members only. Leaving the member role ends the user's own permissions,
			// so a later return to member does not bring old shares back. Group memberships stay.
			if u.Role != store.RoleMember {
				n, err := tx.DeleteUserTeamPermissions(ctx, acc.ID, u.ID)
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
		if wasActiveOwner && (u.Role != store.RoleOwner || u.Status != store.UserActive) {
			if err := ownerGuard(ctx, tx, acc.ID, u.ID); err != nil {
				return err
			}
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

// ownerGuard refuses a change that would leave accountID without an active owner. Only an active owner
// counts, since a pending or disabled owner cannot sign in. The conflict's details give the reason, by code.
func ownerGuard(ctx context.Context, tx *store.Tx, accountID, excludeID string) error {
	others, err := tx.CountActiveOwners(ctx, accountID, excludeID)
	if err != nil {
		return err
	}
	if others == 0 {
		return apierr.New(apierr.Conflict, "the account needs an active owner").With("reason", "last_owner")
	}
	return nil
}

// handleDeleteUser removes a user with its permissions, group memberships, pending access requests and
// personal folder. A personal folder that holds files is refused, unless the caller says what happens to
// them: delete_files=true removes them for good, and transfer_to=usr_... moves them into a new directory
// in the recipient's My files, named after the deleted user. Both are not allowed together.
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.writableAccount(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	usr := r.PathValue("usr")
	deleteFiles := false
	if v := r.URL.Query().Get("delete_files"); v != "" {
		if deleteFiles, err = strconv.ParseBool(v); err != nil {
			return apierr.New(apierr.InvalidRequest, "delete_files must be true or false")
		}
	}
	transferTo := r.URL.Query().Get("transfer_to")
	if transferTo != "" {
		if err := validID("usr", transferTo); err != nil {
			return apierr.New(apierr.InvalidRequest, "transfer_to must be the ID of a user (usr_...)")
		}
		if deleteFiles {
			return apierr.New(apierr.InvalidRequest, "give transfer_to or delete_files=true, not both")
		}
		if transferTo == usr {
			return apierr.New(apierr.InvalidRequest, "a user cannot take over its own files")
		}
	}
	p := principalOf(r)
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		u, err := tx.User(ctx, acc.ID, usr)
		if errors.Is(err, store.ErrNotFound) {
			return notFound("user")
		} else if err != nil {
			return err
		}
		// A user may not remove itself, and the last active owner may not be removed. Both would leave the
		// account without a way to manage it.
		if p.Kind == auth.KindUser && p.UserID == u.ID {
			return apierr.New(apierr.Conflict, "you cannot remove your own user").With("reason", "self")
		}
		if u.Role == store.RoleOwner && u.Status == store.UserActive {
			if err := ownerGuard(ctx, tx, acc.ID, u.ID); err != nil {
				return err
			}
		}
		if transferTo != "" {
			to, err := s.activeUser(ctx, tx, acc.ID, transferTo)
			if err != nil {
				return err
			}
			// An owner session controls the accounts it creates, so it must not be able to take a deleted
			// user's private files for itself or for another owner. The account key is unrestricted.
			if p.Kind == auth.KindUser && (to.ID == p.UserID || to.Role == store.RoleOwner) {
				return apierr.New(apierr.Forbidden, "files can be transferred only to an active member, not to an owner or to the caller")
			}
		}
		// The personal folder goes with its user. Its files are not kept by default: the caller must say
		// so, since they are gone for good, or name a user to take them. The folder goes through
		// DeleteFolder, so its usage and reservations are adjusted; the foreign key refuses a user delete
		// that skips it.
		pf, err := tx.PersonalFolder(ctx, acc.ID, usr)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err == nil {
			entries, err := tx.CountFolderEntries(ctx, acc.ID, pf.ID)
			if err != nil {
				return err
			}
			switch {
			case transferTo != "" && entries > 0:
				if err := s.moveDeletedFiles(ctx, tx, r, acc.ID, u, pf, transferTo); err != nil {
					return err
				}
			case entries > 0 && !deleteFiles:
				// The answer does not say how many entries: that would show an owner how large another
				// user's My files is.
				return apierr.New(apierr.Conflict, "the user's personal folder is not empty; pass delete_files=true or transfer_to")
			}
			if err := tx.DeleteFolder(ctx, acc.ID, pf.ID); err != nil {
				return err
			}
			if err := tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "folder.delete", pf.ID,
				map[string]any{"reason": "user.delete"}, pf.ID)); err != nil {
				return err
			}
		}
		if _, err := tx.DeleteSubjectPermissions(ctx, acc.ID, store.SubjectUser, usr); err != nil {
			return err
		}
		if err := tx.DeleteUserAccessRequests(ctx, acc.ID, usr); err != nil {
			return err
		}
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

// activeUser returns the user of accountID with the given ID, which must be active. It is the check for
// a recipient of files or of ownership.
func (s *Server) activeUser(ctx context.Context, tx *store.Tx, accountID, userID string) (*store.User, error) {
	u, err := tx.User(ctx, accountID, userID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound("user")
	}
	if err != nil {
		return nil, err
	}
	if u.Status != store.UserActive {
		return nil, apierr.New(apierr.InvalidRequest, "transfer_to must be an active user of the account")
	}
	return u, nil
}

// moveDeletedFiles moves the files of a user being deleted into a new directory in the recipient's My
// files, inside the deletion's transaction. The directory is named after the deleted user, and renamed
// if the recipient already has that name. The move keeps the files' contents, permissions and usage.
func (s *Server) moveDeletedFiles(ctx context.Context, tx *store.Tx, r *http.Request, accountID string,
	u *store.User, pf *store.Folder, to string) error {
	recipient, err := s.activeUser(ctx, tx, accountID, to)
	if err != nil {
		return err
	}
	dest, err := tx.PersonalFolder(ctx, accountID, recipient.ID)
	if errors.Is(err, store.ErrNotFound) {
		return apierr.New(apierr.Conflict, "the recipient has no My files to receive the files")
	}
	if err != nil {
		return err
	}
	root, err := tx.RootNode(ctx, accountID, dest.ID)
	if err != nil {
		return err
	}
	name, key, err := deletedFilesName(u)
	if err != nil {
		return err
	}
	plan := &transferPlan{folderID: dest.ID, parent: root, name: name, key: key}
	taken, err := nameTakenBy(tx.Child(ctx, accountID, root.ID, key))
	if err != nil {
		return err
	}
	if taken != nil {
		if err := freeName(ctx, tx, plan, store.KindDir); err != nil {
			return err
		}
	}
	p := principalOf(r)
	now := store.Now()
	dir := &store.Node{ID: id.New("nod"), AccountID: accountID, FolderID: dest.ID, ParentID: root.ID,
		Name: plan.name, NameKey: plan.key, Kind: store.KindDir, CreatedAt: now, ModifiedAt: now,
		CreatedBy: p.ID, ModifiedBy: p.ID, OwnerID: recipient.ID}
	if err := tx.InsertNode(ctx, dir); err != nil {
		return storeNameErr(err)
	}
	if err := tx.MoveFolderContents(ctx, accountID, pf.ID, dest.ID, dir.ID, recipient.ID); err != nil {
		return err
	}
	// The deleting owner does not manage the recipient's My files, so editors_can_share does not come with
	// the files: the flag comes off everything moved into the new directory.
	if err := tx.ClearSubtreeEditorsCanShare(ctx, accountID, dir.ID); err != nil {
		return err
	}
	// The entry is not private to the recipient, so owners can see that a move was made and to whom. It
	// therefore names the recipient only: not the count of entries, which would show how large the deleted
	// user's My files was, nor the recipient's personal folder or the new directory.
	return tx.InsertAudit(ctx, s.auditEntry(r, accountID, "user.files_moved", u.ID,
		map[string]any{"to": recipient.ID}))
}

// deletedFilesName is the name of the directory that takes a deleted user's files: the user's display
// name, or its email, then " (files)". A name the store refuses falls back to a plain one.
func deletedFilesName(u *store.User) (string, string, error) {
	base := u.DisplayName
	if base == "" {
		base = u.Email
	}
	base = strings.NewReplacer("/", "-", "\r", " ", "\n", " ").Replace(base)
	const suffix = " (files)"
	name, key, err := names.Clean(truncateBytes(base, maxNameBytes-len(suffix)) + suffix)
	if err != nil {
		return names.Clean("deleted user (files)")
	}
	return name, key, nil
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
	if err := insertPersonalFolder(ctx, tx, u, now); err != nil {
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

// insertPersonalFolder makes the personal folder of an account user, in the caller's transaction, so
// that the folder exists exactly when the user does. An installation admin has no account and so no
// folder.
func insertPersonalFolder(ctx context.Context, tx *store.Tx, u *store.User, now time.Time) error {
	if u.AccountID == "" {
		return nil
	}
	pf := &store.Folder{ID: id.New("fld"), AccountID: u.AccountID, Name: store.PersonalFolderName,
		NameKey: store.PersonalNameKey(u.ID), Kind: store.KindPersonal, OwnerUserID: u.ID, CreatedAt: now, UpdatedAt: now}
	return tx.InsertFolder(ctx, pf, u.ID)
}
