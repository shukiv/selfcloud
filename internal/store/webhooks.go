// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"
)

// Webhook endpoint states. A disabled endpoint carries a reason; the others carry none.
const (
	WebhookActive   = "active"
	WebhookPaused   = "paused"
	WebhookDisabled = "disabled"
)

// WebhookEndpoint is a URL of an account that receives its events. The secret is only ever held
// encrypted (internal/webhook); the store never sees the plain form.
type WebhookEndpoint struct {
	ID                  string
	AccountID           string
	URL                 string
	Description         string // "" when unset
	Events              []string
	State               string
	DisabledReason      string // "" unless State is WebhookDisabled
	SecretEnc           []byte
	PreviousSecretEnc   []byte     // nil when no rotation is in its grace period
	PreviousSecretUntil *time.Time // set exactly when PreviousSecretEnc is
	ConsecutiveFailures int
	LastSuccessAt       *time.Time
	LastFailureAt       *time.Time
	CreatedBy           string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

const webhookCols = "id, account_id, url, description, state, disabled_reason, secret_enc, " +
	"previous_secret_enc, previous_secret_until, consecutive_failures, last_success_at, last_failure_at, " +
	"created_by, created_at, updated_at"

func scanWebhookEndpoint(row interface{ Scan(...any) error }) (*WebhookEndpoint, error) {
	var (
		e                           WebhookEndpoint
		desc, reason                sql.NullString
		prevEnc                     []byte
		prevUntil, success, failure sql.NullInt64
		created, updated            int64
	)
	err := row.Scan(&e.ID, &e.AccountID, &e.URL, &desc, &e.State, &reason, &e.SecretEnc,
		&prevEnc, &prevUntil, &e.ConsecutiveFailures, &success, &failure, &e.CreatedBy, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	e.Description = desc.String
	e.DisabledReason = reason.String
	e.PreviousSecretEnc = prevEnc
	e.PreviousSecretUntil = fromNullMillis(prevUntil)
	e.LastSuccessAt = fromNullMillis(success)
	e.LastFailureAt = fromNullMillis(failure)
	e.CreatedAt = fromMillis(created)
	e.UpdatedAt = fromMillis(updated)
	return &e, nil
}

// webhookEvents returns the event list of each endpoint of the account, sorted by name.
func webhookEvents(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, accountID string) (map[string][]string, error) {
	rows, err := q.QueryContext(ctx,
		"SELECT endpoint_id, event FROM webhook_endpoint_events WHERE account_id = ? ORDER BY endpoint_id, event",
		accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var endpoint, event string
		if err := rows.Scan(&endpoint, &event); err != nil {
			return nil, err
		}
		out[endpoint] = append(out[endpoint], event)
	}
	return out, rows.Err()
}

// WebhookEndpoint returns the endpoint of accountID with the given ID. Another account's endpoint is
// ErrNotFound.
func (s *Store) WebhookEndpoint(ctx context.Context, accountID, id string) (*WebhookEndpoint, error) {
	e, err := scanWebhookEndpoint(s.r.QueryRowContext(ctx,
		"SELECT "+webhookCols+" FROM webhook_endpoints WHERE account_id = ? AND id = ?", accountID, id))
	if err != nil {
		return nil, err
	}
	evs, err := webhookEvents(ctx, s.r, accountID)
	if err != nil {
		return nil, err
	}
	e.Events = evs[e.ID]
	return e, nil
}

// WebhookEndpoint returns the endpoint of accountID with the given ID, read inside the transaction.
func (tx *Tx) WebhookEndpoint(ctx context.Context, accountID, id string) (*WebhookEndpoint, error) {
	e, err := scanWebhookEndpoint(tx.q.QueryRowContext(ctx,
		"SELECT "+webhookCols+" FROM webhook_endpoints WHERE account_id = ? AND id = ?", accountID, id))
	if err != nil {
		return nil, err
	}
	evs, err := webhookEvents(ctx, tx.q, accountID)
	if err != nil {
		return nil, err
	}
	e.Events = evs[e.ID]
	return e, nil
}

// WebhookEndpoints lists the endpoints of accountID, oldest first, one page at a time.
func (s *Store) WebhookEndpoints(ctx context.Context, accountID string, p Page) ([]*WebhookEndpoint, string, error) {
	where := "account_id = ?"
	args := []any{accountID}
	if p.Cursor != "" {
		parts, err := decodeCursor(p.Cursor, 2)
		if err != nil {
			return nil, "", err
		}
		ms, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, "", ErrBadCursor
		}
		where += " AND (created_at, id) > (?, ?)"
		args = append(args, ms, parts[1])
	}
	args = append(args, p.Limit+1)
	rows, err := s.r.QueryContext(ctx, "SELECT "+webhookCols+" FROM webhook_endpoints WHERE "+where+
		" ORDER BY created_at, id LIMIT ?", args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []*WebhookEndpoint
	for rows.Next() {
		e, err := scanWebhookEndpoint(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	rows.Close()
	evs, err := webhookEvents(ctx, s.r, accountID)
	if err != nil {
		return nil, "", err
	}
	for _, e := range out {
		e.Events = evs[e.ID]
	}
	next := ""
	if len(out) > p.Limit {
		out = out[:p.Limit]
		last := out[len(out)-1]
		next = encodeCursor(strconv.FormatInt(millis(last.CreatedAt), 10), last.ID)
	}
	return out, next, nil
}

// CountWebhookEndpoints returns how many endpoints accountID holds, read inside the transaction.
func (tx *Tx) CountWebhookEndpoints(ctx context.Context, accountID string) (int, error) {
	var n int
	err := tx.q.QueryRowContext(ctx, "SELECT COUNT(*) FROM webhook_endpoints WHERE account_id = ?", accountID).Scan(&n)
	return n, err
}

// InsertWebhookEndpoint adds e and its event list.
func (tx *Tx) InsertWebhookEndpoint(ctx context.Context, e *WebhookEndpoint) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO webhook_endpoints (`+webhookCols+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.AccountID, e.URL, nullString(e.Description), e.State, nullString(e.DisabledReason),
		e.SecretEnc, e.PreviousSecretEnc, nullMillis(e.PreviousSecretUntil), e.ConsecutiveFailures,
		nullMillis(e.LastSuccessAt), nullMillis(e.LastFailureAt), e.CreatedBy, millis(e.CreatedAt), millis(e.UpdatedAt))
	if err != nil {
		return err
	}
	if err := tx.writeEvents(ctx, e); err != nil {
		return err
	}
	return tx.syncQuotaWatch(ctx, e.AccountID)
}

// UpdateWebhookEndpoint rewrites the endpoint named by e.ID within e.AccountID, with its event list. It
// is ErrNotFound for an endpoint of another account.
func (tx *Tx) UpdateWebhookEndpoint(ctx context.Context, e *WebhookEndpoint) error {
	err := rowsAffected(tx.q.ExecContext(ctx, `UPDATE webhook_endpoints SET url = ?, description = ?, state = ?,
		disabled_reason = ?, secret_enc = ?, previous_secret_enc = ?, previous_secret_until = ?,
		consecutive_failures = ?, last_success_at = ?, last_failure_at = ?, updated_at = ?
		WHERE id = ? AND account_id = ?`,
		e.URL, nullString(e.Description), e.State, nullString(e.DisabledReason), e.SecretEnc,
		e.PreviousSecretEnc, nullMillis(e.PreviousSecretUntil), e.ConsecutiveFailures,
		nullMillis(e.LastSuccessAt), nullMillis(e.LastFailureAt), millis(e.UpdatedAt), e.ID, e.AccountID))
	if err != nil {
		return err
	}
	if _, err := tx.q.ExecContext(ctx,
		"DELETE FROM webhook_endpoint_events WHERE endpoint_id = ? AND account_id = ?", e.ID, e.AccountID); err != nil {
		return err
	}
	if err := tx.writeEvents(ctx, e); err != nil {
		return err
	}
	return tx.syncQuotaWatch(ctx, e.AccountID)
}

func (tx *Tx) writeEvents(ctx context.Context, e *WebhookEndpoint) error {
	for _, ev := range e.Events {
		if _, err := tx.q.ExecContext(ctx,
			"INSERT INTO webhook_endpoint_events (endpoint_id, account_id, event) VALUES (?, ?, ?)",
			e.ID, e.AccountID, ev); err != nil {
			return err
		}
	}
	return nil
}

// DeleteWebhookEndpoint removes an endpoint of accountID with its events. Another account's endpoint
// is ErrNotFound.
func (tx *Tx) DeleteWebhookEndpoint(ctx context.Context, accountID, id string) error {
	if err := rowsAffected(tx.q.ExecContext(ctx,
		"DELETE FROM webhook_endpoints WHERE account_id = ? AND id = ?", accountID, id)); err != nil {
		return err
	}
	return tx.syncQuotaWatch(ctx, accountID)
}
