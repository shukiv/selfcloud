// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"time"
)

// The rewrap of endpoint secrets (design section 10, slice j4, decision 30). The rule for what to do with each secret
// is in the api package; these are the writes it makes, one batch at a time. Every secret write names the blob it
// replaces, so a secret that a rotation changed meanwhile is not overwritten with an older copy.

// WebhookEndpointsAfter returns up to limit endpoints of every account, in ID order, after afterID, with what a secret
// rewrap reads. The event lists are not read.
func (tx *Tx) WebhookEndpointsAfter(ctx context.Context, afterID string, limit int) ([]*WebhookEndpoint, error) {
	rows, err := tx.q.QueryContext(ctx, "SELECT "+webhookCols+" FROM webhook_endpoints WHERE id > ? ORDER BY id LIMIT ?",
		afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*WebhookEndpoint
	for rows.Next() {
		e, err := scanWebhookEndpoint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SetWebhookSecret replaces the current secret of an endpoint (previous false), or the secret of its rotation's grace
// period (previous true), but only while the stored blob is still old. It reports whether it wrote.
func (tx *Tx) SetWebhookSecret(ctx context.Context, accountID, endpointID string, previous bool, old, replacement []byte) (bool, error) {
	column := "secret_enc"
	if previous {
		column = "previous_secret_enc"
	}
	// The column name is one of the two constants above, never input.
	n, err := rowsOf(tx.q.ExecContext(ctx, "UPDATE webhook_endpoints SET "+column+" = ? WHERE id = ? AND account_id = ? AND "+column+" = ?",
		replacement, endpointID, accountID, old))
	return n == 1, err
}

// DisableUnreadableSecret disables an endpoint whose secret cannot be read, with reason secret_unreadable. An active or
// paused endpoint is disabled, as disableInTx does, so its queue is failed by the cleanup; a disabled one keeps its
// reason. It reports whether it disabled the endpoint.
func (tx *Tx) DisableUnreadableSecret(ctx context.Context, accountID, endpointID string, at time.Time, url string) (bool, error) {
	return tx.disableFrom(ctx, accountID, endpointID, WebhookDisabledSecret, at, url, WebhookActive, WebhookPaused)
}

// WebhookEndpointCount returns how many webhook endpoints the database holds, of every account and state.
func (s *Store) WebhookEndpointCount(ctx context.Context) (int, error) {
	var n int
	err := s.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM webhook_endpoints").Scan(&n)
	return n, err
}

// RestoreUnreadableSecret moves an endpoint disabled as secret_unreadable to paused, with its reason cleared, once its
// secret opens again. It stays paused: its owner sets it active. It reports whether it changed the endpoint. A paused
// endpoint is not counted as a watcher (webhook_quota.go), and a disabled one was not either, so no quota mark moves.
func (tx *Tx) RestoreUnreadableSecret(ctx context.Context, accountID, endpointID string, at time.Time) (bool, error) {
	n, err := rowsOf(tx.q.ExecContext(ctx, `UPDATE webhook_endpoints SET state = ?, disabled_reason = NULL, updated_at = ?
		WHERE id = ? AND account_id = ? AND state = ? AND disabled_reason = ?`,
		WebhookPaused, millis(at), endpointID, accountID, WebhookDisabled, WebhookDisabledSecret))
	return n == 1, err
}
