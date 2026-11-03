// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"

	"github.com/shukiv/selfcloud/internal/auth"
)

// withEditing marks the items an office editor holds (decision 72): editing is true, and editing_by is the holder's
// display name when the caller may see people's names (peopleNamesSeen) and the holder has one in the account. One read
// of the account's live locks serves every item of a listing.
func (s *Server) withEditing(ctx context.Context, p *auth.Principal, accountID string, items ...*nodeJSON) error {
	// A share link's visitor cannot open the editor, and is not told when a member is editing (review of decision 73).
	if len(items) == 0 || (p != nil && p.Kind == auth.KindShareLink) {
		return nil
	}
	locks, err := s.store.LiveLocksOf(ctx, accountID)
	if err != nil {
		return err
	}
	var holders []string
	for _, it := range items {
		if l, ok := locks[it.ID]; ok {
			it.Editing = true
			holders = append(holders, l.Holder)
		}
	}
	if len(holders) == 0 || !peopleNamesSeen(p, accountID) {
		return nil
	}
	names, err := s.store.UserNames(ctx, accountID, holders)
	if err != nil {
		return err
	}
	for _, it := range items {
		if l, ok := locks[it.ID]; ok {
			it.EditingBy = names[l.Holder]
		}
	}
	return nil
}
