// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/store"
)

// The quota-warning level (design section 10, slice j4, decision 30). An account sets the share of its quota, and of
// each user's limit, at which quota.warning is sent. quota.exceeded is always at 100 percent. Only the account key
// sets it, as it manages the other webhook settings.

// quotaWarningJSON is the level an account now warns at.
type quotaWarningJSON struct {
	Percent int64 `json:"percent"`
}

// handleGetQuotaWarning answers the level the account warns at. The account key and an owner read it; a member does not.
func (s *Server) handleGetQuotaWarning(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.webhookReader(r)
	if err != nil {
		return err
	}
	p, err := s.store.QuotaWarningPercent(r.Context(), acc.ID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, quotaWarningJSON{Percent: p})
	return nil
}

// handlePutQuotaWarning sets the account's warning level to percent, a whole number from 1 to 99.
func (s *Server) handlePutQuotaWarning(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.webhookWriter(r)
	if err != nil {
		return err
	}
	var in struct {
		Percent *int64 `json:"percent"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if in.Percent == nil || *in.Percent < 1 || *in.Percent > 99 {
		return apierr.New(apierr.InvalidRequest, "percent must be a whole number from 1 to 99; quota.exceeded is at 100")
	}
	return s.setQuotaWarning(w, r, acc.ID, *in.Percent)
}

// handleDeleteQuotaWarning sets the account's warning level back to the default.
func (s *Server) handleDeleteQuotaWarning(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.webhookWriter(r)
	if err != nil {
		return err
	}
	return s.setQuotaWarning(w, r, acc.ID, store.WebhookQuotaWarningDefault)
}

// setQuotaWarning writes the level and audits it, then answers with the level now in force.
func (s *Server) setQuotaWarning(w http.ResponseWriter, r *http.Request, accountID string, percent int64) error {
	ctx := r.Context()
	s.atWrite()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.SetQuotaWarningPercent(ctx, accountID, percent, store.Now()); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, accountID, "account.quota_warning", accountID,
			map[string]any{"percent": percent}))
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, quotaWarningJSON{Percent: percent})
	return nil
}
