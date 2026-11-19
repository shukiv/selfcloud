// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"errors"
	"io"
	"net/http"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// handleEndUserSessions ends the web sessions a user holds: those a sign-in link named session_ref started, or all of
// them when no ref is given (decision 211). Only the account's own key may call it: an admin key, a session and a
// delegated or device token are refused. It touches no token, so the user's device and application tokens keep working
// and tokens_not_before does not move; an impersonation of the user is the admin's, not the user's, and stays.
func (s *Server) handleEndUserSessions(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if p == nil || p.Kind != auth.KindAccountKey || p.Token != nil {
		return apierr.New(apierr.Forbidden, "only the account's own key may end a user's sessions")
	}
	acc, err := s.managedAccount(r)
	if err != nil {
		return err
	}
	// The body is optional: no body, or {}, ends all the user's own sessions.
	var in struct {
		SessionRef string `json:"session_ref"`
	}
	if r.ContentLength != 0 {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxJSONBody))
		if err != nil {
			return apierr.Newf(apierr.InvalidRequest, "the body could not be read, or is larger than %d bytes", maxJSONBody)
		}
		if len(bytes.TrimSpace(body)) > 0 {
			r.Body = io.NopCloser(bytes.NewReader(body))
			if err := decodeJSON(w, r, &in); err != nil {
				return err
			}
		}
	}
	ref, err := cleanSessionRef(in.SessionRef)
	if err != nil {
		return err
	}
	if err := s.rateLimit("sessions.end", acc.ID); err != nil {
		return err
	}
	ctx := r.Context()
	usrID := r.PathValue("usr")
	var ended int64
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		u, err := tx.User(ctx, acc.ID, usrID)
		if errors.Is(err, store.ErrNotFound) || (err == nil && u.Role == store.RoleAdmin) {
			return notFound("user")
		}
		if err != nil {
			return err
		}
		n, err := tx.EndUserSessions(ctx, u.ID, ref)
		if err != nil {
			return err
		}
		ended = n
		// The mount credentials end with the sessions they were made alongside (decision 236).
		if err := tx.DeleteAppPasswords(ctx, acc.ID, u.ID); err != nil {
			return err
		}
		// The audit names the user, whether a ref was given and how many sessions ended; it never holds the ref.
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "user.sessions_end", u.ID,
			map[string]any{"ref_given": ref != "", "ended": n}))
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, struct {
		Ended int64 `json:"ended"`
	}{Ended: ended})
	return nil
}
