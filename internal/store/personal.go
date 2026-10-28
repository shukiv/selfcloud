// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"

	"github.com/shukiv/selfcloud/internal/id"
)

// ensurePersonalFolders gives every account user that has no personal folder one, each in its own
// transaction. Users made since migration 0009 already have one, since the API creates it with the
// user; this covers the users that existed when the migration ran. The migration runner is SQL only,
// and folder IDs come from the Go side, so the backfill runs here, after migrations, on every start.
// It is idempotent: a user with a folder is skipped, and the unique index on owner_user_id refuses a
// second.
func (s *Store) ensurePersonalFolders(ctx context.Context) error {
	rows, err := s.r.QueryContext(ctx, `SELECT u.id, u.account_id FROM users u
		WHERE u.account_id IS NOT NULL AND NOT EXISTS (
			SELECT 1 FROM folders f WHERE f.kind = 'personal' AND f.owner_user_id = u.id)`)
	if err != nil {
		return err
	}
	type pending struct{ userID, accountID string }
	var todo []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.userID, &p.accountID); err != nil {
			rows.Close()
			return err
		}
		todo = append(todo, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, p := range todo {
		err := s.Write(ctx, func(tx *Tx) error {
			now := Now()
			f := &Folder{ID: id.New("fld"), AccountID: p.accountID, Name: PersonalFolderName,
				NameKey: PersonalNameKey(p.userID), Kind: KindPersonal, OwnerUserID: p.userID,
				CreatedAt: now, UpdatedAt: now}
			return tx.InsertFolder(ctx, f, p.userID)
		})
		if err != nil {
			return err
		}
	}
	return nil
}
