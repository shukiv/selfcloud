package api

import (
	"errors"
	"net/http"
	"time"

	"selfcloud/internal/apierr"
	"selfcloud/internal/auth"
	"selfcloud/internal/id"
	"selfcloud/internal/store"
)

type keyJSON struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

func toKeyJSON(k *store.APIKey) keyJSON {
	return keyJSON{ID: k.ID, Name: k.Name, Prefix: k.Prefix, CreatedAt: k.CreatedAt,
		ExpiresAt: k.ExpiresAt, RevokedAt: k.RevokedAt, LastUsedAt: k.LastUsedAt}
}

// handleCreateKey makes an account key. The secret is in this response and nowhere else.
func (s *Server) handleCreateKey(w http.ResponseWriter, r *http.Request) error {
	if err := requireAdmin(r); err != nil {
		return err
	}
	var in struct {
		Name      string     `json:"name"`
		ExpiresAt *time.Time `json:"expires_at"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	name, err := cleanLabel("name", in.Name, 100)
	if err != nil {
		return err
	}
	now := store.Now()
	if in.ExpiresAt != nil {
		t := in.ExpiresAt.UTC().Truncate(time.Millisecond)
		if !t.After(now) {
			return apierr.New(apierr.InvalidRequest, "expires_at must be in the future")
		}
		in.ExpiresAt = &t
	}

	ctx := r.Context()
	secret, prefix, hash := auth.NewKey(auth.AccountKeyPrefix)
	key := &store.APIKey{ID: id.New("key"), Name: name, Prefix: prefix, Hash: hash, CreatedAt: now,
		ExpiresAt: in.ExpiresAt}
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		acc, err := tx.Account(ctx, r.PathValue("acc"))
		if errors.Is(err, store.ErrNotFound) {
			return notFound("account")
		}
		if err != nil {
			return err
		}
		if acc.Status == store.AccountDeleting {
			return apierr.New(apierr.Conflict, "the account is being deleted")
		}
		key.AccountID = acc.ID
		if err := tx.InsertAPIKey(ctx, key); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "key.create", key.ID, map[string]any{"name": name}))
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, struct {
		keyJSON
		Key string `json:"key"`
	}{toKeyJSON(key), secret})
	return nil
}

func (s *Server) handleListKeys(w http.ResponseWriter, r *http.Request) error {
	if err := requireAdmin(r); err != nil {
		return err
	}
	acc, err := s.managedAccount(r)
	if err != nil {
		return err
	}
	keys, err := s.store.APIKeys(r.Context(), acc.ID)
	if err != nil {
		return err
	}
	out := listResponse[keyJSON]{Items: []keyJSON{}}
	for _, k := range keys {
		out.Items = append(out.Items, toKeyJSON(k))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) handleRevokeKey(w http.ResponseWriter, r *http.Request) error {
	if err := requireAdmin(r); err != nil {
		return err
	}
	ctx := r.Context()
	accID, keyID := r.PathValue("acc"), r.PathValue("key")
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.RevokeAPIKey(ctx, accID, keyID, store.Now()); errors.Is(err, store.ErrNotFound) {
			return notFound("key")
		} else if err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, accID, "key.revoke", keyID, nil))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
