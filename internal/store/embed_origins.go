// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"time"
)

// EmbedOrigins returns the account's allowed host origins, sorted. An account with none gets an empty
// list. The installation's own list is the config's embed.origins; the caller unions the two (decision 197).
func (s *Store) EmbedOrigins(ctx context.Context, accountID string) ([]string, error) {
	return embedOrigins(ctx, s.r, accountID)
}

// EmbedOrigins is Store.EmbedOrigins read inside the transaction.
func (tx *Tx) EmbedOrigins(ctx context.Context, accountID string) ([]string, error) {
	return embedOrigins(ctx, tx.q, accountID)
}

func embedOrigins(ctx context.Context, q querier, accountID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, "SELECT origin FROM account_embed_origins WHERE account_id = ? ORDER BY origin", accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var o string
		if err := rows.Scan(&o); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ReplaceEmbedOrigins replaces the account's allowed host origins with origins, in one transaction. An
// empty list clears them. The caller normalises and deduplicates the list before it reaches here (decision 197).
func (tx *Tx) ReplaceEmbedOrigins(ctx context.Context, accountID string, origins []string, at time.Time) error {
	if _, err := tx.q.ExecContext(ctx, "DELETE FROM account_embed_origins WHERE account_id = ?", accountID); err != nil {
		return err
	}
	for _, o := range origins {
		if _, err := tx.q.ExecContext(ctx,
			"INSERT INTO account_embed_origins (account_id, origin, created_at) VALUES (?, ?, ?)",
			accountID, o, millis(at)); err != nil {
			return err
		}
	}
	return nil
}
