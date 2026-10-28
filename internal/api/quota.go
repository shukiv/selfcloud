// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/store"
)

// Quotas (design section 8). An account's used bytes and its reserved bytes (the declared length of
// each unfinished upload) together must fit in its quota. Every write that grows them goes through
// applyUsage, in the transaction that makes the change, so a refused write leaves nothing behind.
//
// Only growth is checked. A change whose net effect is zero or negative always passes, so a delete,
// a shrink, or a completion that moves a reservation into usage is never refused, even when the
// quota has been lowered below current usage.
//
// An account with no quota has no quota to check. Its reservations are still capped at
// upload.max_reserved_per_account, so the reservation counter stays bounded.

// Refusal scopes, reported in details.scope of a quota_exceeded error.
const (
	scopeQuota        = "quota"
	scopeReservations = "account_reservations"
)

// checkQuota refuses c when it grows the account past its quota, or when it reserves past the cap
// of an account that has no quota. acc is the account's state before c.
func (s *Server) checkQuota(acc *store.Account, c store.UsageChange) error {
	growth := c.Bytes + c.Reserved
	if growth > 0 && acc.QuotaBytes > 0 {
		held := acc.UsedBytes + acc.ReservedBytes
		if held+growth > acc.QuotaBytes {
			return quotaRefusal(growth, max(0, acc.QuotaBytes-held), scopeQuota)
		}
	}
	if c.Reserved > 0 && acc.QuotaBytes == 0 {
		limit := int64(s.cfg.Upload.MaxReservedPerAccount)
		if acc.ReservedBytes+c.Reserved > limit {
			return quotaRefusal(c.Reserved, max(0, limit-acc.ReservedBytes), scopeReservations)
		}
	}
	return nil
}

// quotaRefusal is the 507 for a change that does not fit: the bytes it needs and the bytes free.
func quotaRefusal(required, available int64, scope string) error {
	return apierr.Newf(apierr.QuotaExceeded, "the change needs %d bytes; the account has %d free", required, available).
		With("required_bytes", required).With("available_bytes", available).With("scope", scope)
}

// applyUsage checks c against the account's quota and then applies it, inside tx. Call it in the
// same transaction as the change it counts.
func (s *Server) applyUsage(ctx context.Context, tx *store.Tx, accountID string, c store.UsageChange) error {
	acc, err := tx.Account(ctx, accountID)
	if err != nil {
		return err
	}
	if err := s.checkQuota(acc, c); err != nil {
		return err
	}
	return tx.AdjustUsage(ctx, accountID, c)
}
