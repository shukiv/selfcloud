// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// App passwords are a person's credentials for the read-only WebDAV mount (decision 236). They are made, listed and
// revoked on the person's own session only: a key, a delegated token or a terminal's sign-in does not manage them, and
// an installation admin signed in as the person does not either. The secret is shown once, when it is made; only its
// hash is stored, as an API key's is.

// appPasswordWriteRate names the limiter that bounds making app passwords, per user. Revoking is never charged.
const appPasswordWriteRate = "app_passwords.write"

// appPasswordNameMax is the longest label an app password may carry.
const appPasswordNameMax = 64

type appPasswordJSON struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Access     string     `json:"access"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

func toAppPasswordJSON(a *store.AppPassword) appPasswordJSON {
	return appPasswordJSON{ID: a.ID, Name: a.Name, Access: a.Access, Prefix: a.Prefix,
		CreatedAt: a.CreatedAt, RevokedAt: a.RevokedAt, LastUsedAt: a.LastUsedAt}
}

// davEnabled gates the app-password routes and the mount on dav.enabled. While it is off they answer not_found.
func (s *Server) davEnabled() error {
	if !s.settings().Dav.Enabled {
		return apierr.New(apierr.NotFound, "WebDAV is turned off")
	}
	return nil
}

func (s *Server) handleListAppPasswords(w http.ResponseWriter, r *http.Request) error {
	if err := s.davEnabled(); err != nil {
		return err
	}
	p := principalOf(r)
	items, err := s.store.AppPasswords(r.Context(), p.AccountID, p.UserID)
	if err != nil {
		return storeErr(err)
	}
	out := listResponse[appPasswordJSON]{Items: []appPasswordJSON{}}
	for _, a := range items {
		out.Items = append(out.Items, toAppPasswordJSON(a))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) handleCreateAppPassword(w http.ResponseWriter, r *http.Request) error {
	if err := s.davEnabled(); err != nil {
		return err
	}
	var in struct {
		Name   string `json:"name"`
		Access string `json:"access"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	name, err := cleanLabel("name", in.Name, appPasswordNameMax)
	if err != nil {
		return err
	}
	access := in.Access
	if access == "" {
		access = store.AppAccessRead
	}
	if access != store.AppAccessRead && access != store.AppAccessWrite {
		return apierr.New(apierr.InvalidRequest, "access must be read or write")
	}
	p := principalOf(r)
	ctx := r.Context()
	// The cap is checked before the write budget is spent, so a user at the cap gets the cap's answer, not a rate limit.
	n, err := s.store.CountAppPasswords(ctx, p.AccountID, p.UserID)
	if err != nil {
		return storeErr(err)
	}
	if n >= store.MaxAppPasswords {
		return appPasswordCapErr()
	}
	if err := s.rateLimit(appPasswordWriteRate, p.UserID); err != nil {
		return err
	}

	apwID := id.New("apw")
	secret, prefix, hash := newAppPasswordSecret(apwID)
	a := &store.AppPassword{ID: apwID, AccountID: p.AccountID, UserID: p.UserID, Name: name,
		Access: access, Prefix: prefix, SecretHash: hash, CreatedAt: store.Now()}
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		// Count again in the write, so two creates at once cannot pass the cap together.
		n, err := tx.CountAppPasswords(ctx, p.AccountID, p.UserID)
		if err != nil {
			return err
		}
		if n >= store.MaxAppPasswords {
			return appPasswordCapErr()
		}
		if err := tx.InsertAppPassword(ctx, a); err != nil {
			return err
		}
		e := s.auditEntry(r, p.AccountID, "app_password.create", a.ID, map[string]any{"name": name, "access": access})
		e.PrivateUserID = p.UserID
		return tx.InsertAudit(ctx, e)
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, struct {
		appPasswordJSON
		Secret string `json:"secret"`
	}{toAppPasswordJSON(a), secret})
	return nil
}

func (s *Server) handleRevokeAppPassword(w http.ResponseWriter, r *http.Request) error {
	if err := s.davEnabled(); err != nil {
		return err
	}
	p := principalOf(r)
	apwID := r.PathValue("apw")
	ctx := r.Context()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.RevokeAppPassword(ctx, p.AccountID, p.UserID, apwID, store.Now()); errors.Is(err, store.ErrNotFound) {
			return notFound("app password")
		} else if err != nil {
			return err
		}
		e := s.auditEntry(r, p.AccountID, "app_password.revoke", apwID, nil)
		e.PrivateUserID = p.UserID
		return tx.InsertAudit(ctx, e)
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func appPasswordCapErr() error {
	return apierr.New(apierr.Conflict, "you have reached the limit of app passwords").
		With("reason", "limit").With("limit", store.MaxAppPasswords)
}

// newAppPasswordSecret makes the secret scp_<id>_<random>, the start of it that is safe to display, and its hash.
func newAppPasswordSecret(apwID string) (secret, prefix string, hash []byte) {
	var b [32]byte
	rand.Read(b[:]) // crypto/rand.Read never returns an error.
	p := auth.AppPasswordPrefix + apwID + "_"
	secret = p + base64.RawURLEncoding.EncodeToString(b[:])
	return secret, auth.DisplayPrefix(p, secret), auth.HashKey(secret)
}

// appPasswordIDOf parses the app password ID out of a presented secret, if it has the shape of one. It is used to key
// the failed-sign-in budget per app password as well as per address.
func appPasswordIDOf(secret string) (string, bool) {
	rest, ok := strings.CutPrefix(secret, auth.AppPasswordPrefix)
	if !ok || len(rest) < 31 || rest[30] != '_' {
		return "", false
	}
	apwID := rest[:30]
	if !id.Valid("apw", apwID) {
		return "", false
	}
	return apwID, true
}
