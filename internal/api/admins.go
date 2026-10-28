package api

import (
	"errors"
	"net/http"
	"time"

	"selfcloud/internal/apierr"
	"selfcloud/internal/id"
	"selfcloud/internal/store"
)

// Installation admins are users with no account. An admin key or a signed-in admin may manage
// them; the last active admin cannot be removed while no admin key can act either, so the
// installation always keeps a way in.

// adminResponse is an admin user, with the verification made for it when one was.
type adminResponse = userResponse

// handleCreateAdmin makes a pending admin user. Only email and link are offered: "none" would skip
// verification, which admins never do. A link may be asked for by a key, or by an admin session
// since the new admin has never verified (linkAllowed).
func (s *Server) handleCreateAdmin(w http.ResponseWriter, r *http.Request) error {
	if err := requireAdmin(r); err != nil {
		return err
	}
	var in struct {
		Email        string `json:"email"`
		DisplayName  string `json:"display_name"`
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
	if in.Verification != methodEmail && in.Verification != methodLink {
		return apierr.New(apierr.InvalidRequest, "verification must be email or link")
	}
	if err := s.checkVerificationMethod(r, in.Verification, false); err != nil {
		return err
	}

	ctx := r.Context()
	now := store.Now()
	key := emailKey(email)
	var (
		u      *store.User
		issued *issuedToken
	)
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		if _, err := tx.UserByEmail(ctx, "", key); err == nil {
			return apierr.New(apierr.Conflict, "an admin with this email already exists")
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		u = &store.User{ID: id.New("usr"), Email: email, EmailKey: key, DisplayName: displayName,
			Locale: "en", Role: store.RoleAdmin, CreatedAt: now, UpdatedAt: now}
		var err error
		issued, err = s.addUser(ctx, r, tx, u, in.Verification, "admin.create", now)
		return err
	})
	if err != nil {
		return err
	}
	out := adminResponse{userJSON: toUserJSON(u)}
	out.Verification = s.deliver(ctx, r, u, issued, in.Verification)
	w.Header().Set("Location", "/api/v1/admins/"+u.ID)
	writeJSON(w, http.StatusCreated, out)
	return nil
}

// handleListAdmins lists the installation admins, oldest first.
func (s *Server) handleListAdmins(w http.ResponseWriter, r *http.Request) error {
	if err := requireAdmin(r); err != nil {
		return err
	}
	page, err := pageParams(r)
	if err != nil {
		return err
	}
	admins, next, err := s.store.Users(r.Context(), "", store.UserFilter{}, page)
	if err != nil {
		return storeErr(err)
	}
	out := listResponse[userJSON]{Items: []userJSON{}, NextCursor: next}
	for _, u := range admins {
		out.Items = append(out.Items, toUserJSON(u))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// handleDeleteAdmin removes an admin user. Removing the last active admin is refused while no
// active admin key exists either, because then nothing could manage the installation.
func (s *Server) handleDeleteAdmin(w http.ResponseWriter, r *http.Request) error {
	if err := requireAdmin(r); err != nil {
		return err
	}
	ctx := r.Context()
	usr := r.PathValue("usr")
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		u, err := tx.User(ctx, "", usr)
		if errors.Is(err, store.ErrNotFound) || (err == nil && u.Role != store.RoleAdmin) {
			return notFound("admin")
		}
		if err != nil {
			return err
		}
		if u.Status == store.UserActive {
			others, err := tx.CountActiveAdmins(ctx, u.ID)
			if err != nil {
				return err
			}
			keys, err := tx.CountActiveAdminKeys(ctx, time.Now())
			if err != nil {
				return err
			}
			if others == 0 && keys == 0 {
				return apierr.New(apierr.Conflict, "this is the last active admin and no admin key is active")
			}
		}
		if err := tx.DeleteUser(ctx, "", u.ID); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, "", "admin.delete", u.ID, nil))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
