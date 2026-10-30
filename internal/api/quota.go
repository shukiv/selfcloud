// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// Quotas (design section 8, and slice p1). An account's used bytes and its reserved bytes (the declared length of
// each unfinished upload) together must fit in its quota. Each user's own storage, its personal folder, may also
// have a limit: the user's, or the account's default for each user (users.quota_bytes, accounts.user_quota_bytes).
// Every write that grows usage goes through applyUsage, in the transaction that makes the change, so a refused
// write leaves nothing behind.
//
// Only growth is checked. A change whose net effect is zero or negative always passes, so a delete, a shrink, or a
// completion that moves a reservation into usage is never refused, even when the quota has been lowered below
// current usage.
//
// An account with no quota has no quota to check. Its reservations are still capped at
// upload.max_reserved_per_account, so the reservation counter stays bounded.

// Refusal scopes, reported in details.scope of a quota_exceeded error.
const (
	scopeQuota        = "quota"
	scopeReservations = "account_reservations"
	scopeUser         = "user"
)

// checkQuota refuses c when it grows the account past its quota, or when it reserves past the cap
// of an account that has no quota. acc is the account's state before c; p is the caller (see
// accountQuotaSeen for who learns the numbers).
func (s *Server) checkQuota(p *auth.Principal, accountID string, acc *store.Account, c store.UsageChange) error {
	growth := c.Bytes + c.Reserved
	if growth > 0 && acc.QuotaBytes > 0 {
		held := acc.UsedBytes + acc.ReservedBytes
		if held+growth > acc.QuotaBytes {
			return accountRefusal(p, accountID, growth, max(0, acc.QuotaBytes-held), scopeQuota)
		}
	}
	if c.Reserved > 0 && acc.QuotaBytes == 0 {
		limit := int64(s.cfg.Upload.MaxReservedPerAccount)
		if acc.ReservedBytes+c.Reserved > limit {
			return accountRefusal(p, accountID, c.Reserved, max(0, limit-acc.ReservedBytes), scopeReservations)
		}
	}
	return nil
}

// accountQuotaSeen reports whether the numbers of the account's own refusal may go to p: an installation admin, the
// account's key, and the account's owner in a session. A delegated token never learns them, even when it acts for an
// owner, and neither does a member, a visitor of a link, or a caller of another account.
func accountQuotaSeen(p *auth.Principal, accountID string) bool {
	if p == nil || p.Token != nil {
		return false
	}
	if p.IsAdmin() {
		return true
	}
	switch p.Kind {
	case auth.KindAccountKey:
		return p.AccountID == accountID
	case auth.KindUser:
		return p.Role == auth.RoleOwner && p.AccountID == accountID
	}
	return false
}

// accountRefusal is the account's refusal for a caller: the numbers when accountQuotaSeen allows them, and otherwise
// the same code with no details and no number at all.
func accountRefusal(p *auth.Principal, accountID string, required, available int64, scope string) error {
	if !accountQuotaSeen(p, accountID) {
		return apierr.New(apierr.QuotaExceeded, "the account's storage limit would be passed")
	}
	return quotaRefusal(required, available, scope)
}

// quotaRefusal is the 507 for a change that does not fit: the bytes it needs and the bytes free.
func quotaRefusal(required, available int64, scope string) error {
	return apierr.Newf(apierr.QuotaExceeded, "the change needs %d bytes; the account has %d free", required, available).
		With("required_bytes", required).With("available_bytes", available).With("scope", scope)
}

// quotaSeen reports whether the numbers of a refusal on a personal folder may go to p: the user the folder
// belongs to, and the account's key. Nobody else learns how much space a user has.
func quotaSeen(p *auth.Principal, accountID, owner string) bool {
	if p == nil {
		return false
	}
	if p.Kind == auth.KindAccountKey && p.AccountID == accountID {
		return true
	}
	return owner != "" && p.Kind == auth.KindUser && p.UserID == owner
}

// checkUserLimit refuses growth bytes in a personal folder when its owner's limit would be passed. st is the
// folder's state before the change. A team space has no user limit. A limit of 0 is none, and a change that
// shrinks usage always passes.
func (s *Server) checkUserLimit(p *auth.Principal, accountID string, st store.QuotaState, growth int64) error {
	if growth <= 0 || st.FolderKind != store.KindPersonal || st.UserLimit <= 0 {
		return nil
	}
	held := st.Folder.Bytes + st.Folder.Reserved
	if held+growth <= st.UserLimit {
		return nil
	}
	available := max(0, st.UserLimit-held)
	if !quotaSeen(p, accountID, st.OwnerUserID) {
		return apierr.New(apierr.QuotaExceeded, "the storage limit of this user's files would be passed").
			With("scope", scopeUser)
	}
	return apierr.Newf(apierr.QuotaExceeded, "the change needs %d bytes; the user has %d free", growth, available).
		With("required_bytes", growth).With("available_bytes", available).With("scope", scopeUser)
}

// applyUsage checks parts against the account's quota and the limits of the users their personal folders belong
// to, then applies them, inside tx. Each part names its folder: a change is charged to the folder it lands in.
// Call it in the transaction that makes the change. p is the caller; see quotaSeen for who learns the numbers.
func (s *Server) applyUsage(ctx context.Context, tx *store.Tx, p *auth.Principal, accountID string,
	parts ...store.FolderChange) error {
	if err := s.checkUsage(ctx, tx, p, accountID, parts...); err != nil {
		return err
	}
	return tx.AdjustFolders(ctx, accountID, parts...)
}

// checkMoveUsage checks that the subtree at nodeID, moving from fromFolder into toFolder, fits the limit of the user
// the destination personal folder belongs to. A move within one folder changes nothing and passes. A move never
// changes the account's total, so only the destination user's limit applies.
func (s *Server) checkMoveUsage(ctx context.Context, tx *store.Tx, p *auth.Principal, accountID, nodeID,
	fromFolder, toFolder string) error {
	if fromFolder == toFolder {
		return nil
	}
	c, err := tx.SubtreeUsage(ctx, accountID, nodeID)
	if err != nil {
		return err
	}
	st, err := tx.QuotaStateOf(ctx, accountID, toFolder)
	if err != nil {
		return err
	}
	return s.checkUserLimit(p, accountID, st, c.Bytes+c.Reserved)
}

// checkUsage is the check in applyUsage, without the change.
func (s *Server) checkUsage(ctx context.Context, tx *store.Tx, p *auth.Principal, accountID string,
	parts ...store.FolderChange) error {
	acc, err := tx.Account(ctx, accountID)
	if err != nil {
		return err
	}
	var net store.UsageChange
	growth := map[string]store.UsageChange{}
	var folders []string
	for _, part := range parts {
		net.Bytes += part.Change.Bytes
		net.Files += part.Change.Files
		net.Reserved += part.Change.Reserved
		if _, seen := growth[part.FolderID]; !seen {
			folders = append(folders, part.FolderID)
		}
		g := growth[part.FolderID]
		g.Bytes += part.Change.Bytes
		g.Reserved += part.Change.Reserved
		growth[part.FolderID] = g
	}
	if err := s.checkQuota(p, accountID, acc, net); err != nil {
		return err
	}
	for _, f := range folders {
		g := growth[f]
		if g.Bytes+g.Reserved <= 0 {
			continue
		}
		st, err := tx.QuotaStateOf(ctx, accountID, f)
		if err != nil {
			return err
		}
		if err := s.checkUserLimit(p, accountID, st, g.Bytes+g.Reserved); err != nil {
			return err
		}
	}
	return nil
}
