// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/shukiv/selfcloud/internal/webhook"
)

// The worker's queries (design section 10, slice j3, decision 28). Each write is short: a claim, one attempt's
// outcome, one batch of a cleanup, one batch of a sweep. None of them is held across an HTTP call.

// Reasons an endpoint is disabled. The schema's CHECK admits these three.
const (
	WebhookDisabledFailing  = "failing"
	WebhookDisabledOverflow = "overflow"
	WebhookDisabledSecret   = "secret_unreadable"
)

// staleDeliveryError is the last_error of a delivery that a disable left behind and the cleanup failed. The endpoint's
// reason is not kept after a re-enable, so the token names only what happened.
const staleDeliveryError = "disabled"

// WebhookJob is a delivery the worker has claimed, with what a send needs: the stored event bytes, the endpoint's
// URL and its encrypted secrets, and the attempt count. The claim sets the first attempt time.
type WebhookJob struct {
	DeliveryID          string
	AccountID           string
	EndpointID          string
	EventRowID          int64
	EventID             string
	EventType           string
	Payload             []byte
	URL                 string
	SecretEnc           []byte
	PreviousSecretEnc   []byte
	PreviousSecretUntil *time.Time
	Attempts            int
	FirstAttemptAt      time.Time
	LastSuccessAt       *time.Time
	// Lease is the claim's lease end in milliseconds. An outcome applies only while it is still the delivery's lease.
	Lease int64
}

// WebhookOutcome is the result of one send, recorded by RecordWebhookOutcome. Err is a fixed token, never an error
// text: it may hold a URL or a body (decision 28). Code is the HTTP status, or 0 when there was none.
type WebhookOutcome struct {
	DeliveryID string
	AccountID  string
	EndpointID string
	URL        string // for the audit origin only
	At         time.Time
	Code       int
	Err        string // "" on a 2xx answer
	// NextAt is when a retry is due. It is set on a failure that is not given up.
	NextAt time.Time
	// GiveUp marks the delivery failed: no retry follows.
	GiveUp bool
	// Disable, when set, disables the endpoint with that reason, in the same write.
	Disable string
	// Lease is the WebhookJob.Lease of the claim that made this outcome. An outcome whose lease has ended, because the
	// delivery was claimed again since, changes nothing (review finding 5).
	Lease int64
}

// claimWebhookJobs claims up to limit deliveries at now, in one write. A claimed delivery is the oldest pending delivery
// of its endpoint in the endpoint's current generation, and only that one: a later delivery of the endpoint waits for
// it, including while it waits on its backoff. An endpoint with any unexpired lease is not served, so an endpoint has
// one request in flight at a time, whatever order its deliveries were queued in (review finding 4). Among the endpoints
// that are due, the claim ranks by account first, so one account's backlog cannot take every slot of another's
// (review finding 1). endpointID "" serves every endpoint; a value serves that one endpoint only.
func (tx *Tx) claimWebhookJobs(ctx context.Context, at time.Time, lease time.Duration, limit int, endpointID string) ([]*WebhookJob, error) {
	now := millis(at)
	rows, err := tx.q.QueryContext(ctx, `WITH due AS (
			SELECT d.id AS did, d.next_attempt_at AS nat, d.event_row_id AS erow,
				ROW_NUMBER() OVER (PARTITION BY e.account_id ORDER BY d.next_attempt_at, d.event_row_id) AS rnk
			FROM webhook_endpoints e
			JOIN webhook_deliveries d ON d.endpoint_id = e.id AND d.state = 'pending' AND d.generation = e.generation
				AND d.event_row_id = (SELECT MIN(p.event_row_id) FROM webhook_deliveries p
					WHERE p.endpoint_id = e.id AND p.state = 'pending' AND p.generation = e.generation)
			WHERE e.state = ? AND (? = '' OR e.id = ?) AND d.next_attempt_at <= ?
				AND NOT EXISTS (SELECT 1 FROM webhook_deliveries l WHERE l.endpoint_id = e.id AND l.lease_until > ?)
		)
		SELECT d.id, d.account_id, d.endpoint_id, d.event_row_id, ev.event_id, ev.type,
			ev.payload, e.url, e.secret_enc, e.previous_secret_enc, e.previous_secret_until, d.attempts,
			d.first_attempt_at, e.last_success_at
		FROM due
		JOIN webhook_deliveries d ON d.id = due.did
		JOIN webhook_endpoints e ON e.id = d.endpoint_id
		JOIN webhook_events ev ON ev.id = d.event_row_id
		ORDER BY due.rnk, due.nat, due.erow LIMIT ?`,
		WebhookActive, endpointID, endpointID, now, now, limit)
	if err != nil {
		return nil, err
	}
	var out []*WebhookJob
	for rows.Next() {
		var (
			j                  WebhookJob
			payload            string
			prevEnc            []byte
			prevUntil, success sql.NullInt64
			firstAttempt       sql.NullInt64
		)
		if err := rows.Scan(&j.DeliveryID, &j.AccountID, &j.EndpointID, &j.EventRowID, &j.EventID, &j.EventType,
			&payload, &j.URL, &j.SecretEnc, &prevEnc, &prevUntil, &j.Attempts, &firstAttempt, &success); err != nil {
			rows.Close()
			return nil, err
		}
		j.Payload = []byte(payload)
		j.PreviousSecretEnc = prevEnc
		j.PreviousSecretUntil = fromNullMillis(prevUntil)
		j.LastSuccessAt = fromNullMillis(success)
		if firstAttempt.Valid {
			j.FirstAttemptAt = fromMillis(firstAttempt.Int64)
		} else {
			j.FirstAttemptAt = at
		}
		out = append(out, &j)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(out) == 0 {
		return nil, nil
	}
	// The lease and the first attempt are written in one statement for the whole claim.
	ids := make([]string, 0, len(out))
	for _, j := range out {
		ids = append(ids, j.DeliveryID)
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	leaseUntil := millis(at.Add(lease))
	if _, err := tx.q.ExecContext(ctx, `UPDATE webhook_deliveries SET lease_until = ?,
		first_attempt_at = COALESCE(first_attempt_at, ?) WHERE id IN (SELECT value FROM json_each(?))`,
		leaseUntil, now, string(raw)); err != nil {
		return nil, err
	}
	for _, j := range out {
		j.Lease = leaseUntil
	}
	return out, nil
}

// ClaimWebhookJobs claims up to limit deliveries, as claimWebhookJobs does, in one write. endpointID "" claims across
// every endpoint; an endpoint's own lane passes its ID to claim its next delivery.
func (s *Store) ClaimWebhookJobs(ctx context.Context, at time.Time, lease time.Duration, limit int, endpointID string) ([]*WebhookJob, error) {
	var out []*WebhookJob
	err := s.Write(ctx, func(tx *Tx) error {
		var err error
		out, err = tx.claimWebhookJobs(ctx, at, lease, limit, endpointID)
		return err
	})
	return out, err
}

// RecordWebhookOutcome records one attempt, in one write. A success marks the delivery delivered and clears the
// endpoint's failure count. A failure schedules the retry at NextAt, or gives the delivery up with GiveUp. Disable
// then disables the endpoint with its reason, in the same write. The outcome applies only to the claim it belongs to:
// a delivery that is no longer pending, or whose lease is no longer o.Lease, records nothing (review finding 5).
func (s *Store) RecordWebhookOutcome(ctx context.Context, o WebhookOutcome) error {
	return s.Write(ctx, func(tx *Tx) error {
		at := millis(o.At)
		var code sql.NullInt64
		if o.Code != 0 {
			code = sql.NullInt64{Int64: int64(o.Code), Valid: true}
		}
		if o.Err == "" {
			n, err := rowsOf(tx.q.ExecContext(ctx, `UPDATE webhook_deliveries SET state = 'delivered',
				attempts = attempts + 1, last_code = ?, last_error = NULL, lease_until = NULL, finished_at = ?
				WHERE id = ? AND account_id = ? AND state = 'pending' AND lease_until = ?`,
				code, at, o.DeliveryID, o.AccountID, o.Lease))
			if err != nil || n == 0 {
				return err
			}
			_, err = tx.q.ExecContext(ctx, `UPDATE webhook_endpoints SET consecutive_failures = 0, last_success_at = ?,
				pending_count = pending_count - 1 WHERE id = ? AND account_id = ?`, at, o.EndpointID, o.AccountID)
			return err
		}
		state, finished := "pending", sql.NullInt64{}
		if o.GiveUp {
			state, finished = "failed", sql.NullInt64{Int64: at, Valid: true}
		}
		n, err := rowsOf(tx.q.ExecContext(ctx, `UPDATE webhook_deliveries SET state = ?, attempts = attempts + 1,
			last_code = ?, last_error = ?, lease_until = NULL, next_attempt_at = ?, finished_at = ?
			WHERE id = ? AND account_id = ? AND state = 'pending' AND lease_until = ?`,
			state, code, o.Err, millis(o.NextAt), finished, o.DeliveryID, o.AccountID, o.Lease))
		if err != nil || n == 0 {
			return err
		}
		given := 0
		if o.GiveUp {
			given = 1
		}
		if _, err := tx.q.ExecContext(ctx, `UPDATE webhook_endpoints SET consecutive_failures = consecutive_failures + 1,
			last_failure_at = ?, pending_count = pending_count - ? WHERE id = ? AND account_id = ?`,
			at, given, o.EndpointID, o.AccountID); err != nil {
			return err
		}
		if o.Disable == "" {
			return nil
		}
		return tx.disableInTx(ctx, o.AccountID, o.EndpointID, o.Disable, o.At, o.URL)
	})
}

// DisableWebhookEndpoint disables an active endpoint with reason, and audits it. An endpoint that is not active is
// left as it is.
func (s *Store) DisableWebhookEndpoint(ctx context.Context, accountID, endpointID, reason string, at time.Time, url string) error {
	return s.Write(ctx, func(tx *Tx) error {
		return tx.disableInTx(ctx, accountID, endpointID, reason, at, url)
	})
}

// disableInTx makes an active endpoint disabled with reason and writes its webhook.disabled audit entry. The audit
// names the endpoint and the origin of its URL, never the path, query or user name. An endpoint that is not active
// is left as it is. The disable starts a new generation of the endpoint: the deliveries queued before it are stale,
// and the worker never sends them, even after a re-enable (decision 28). The transaction's fanout forgets the
// endpoint, so later events of the transaction skip it.
func (tx *Tx) disableInTx(ctx context.Context, accountID, endpointID, reason string, at time.Time, url string) error {
	n, err := rowsOf(tx.q.ExecContext(ctx, `UPDATE webhook_endpoints SET state = ?, disabled_reason = ?, updated_at = ?,
		generation = generation + 1, stale_queue = 1
		WHERE id = ? AND account_id = ? AND state = ?`,
		WebhookDisabled, reason, millis(at), endpointID, accountID, WebhookActive))
	if err != nil || n == 0 {
		return err
	}
	if f, ok := tx.fanouts[accountID]; ok {
		f.drop(endpointID)
	}
	return tx.InsertAudit(ctx, &AuditEntry{Time: at, AccountID: accountID, ActorKind: "system", ActorID: "webhooks",
		Action: "webhook.disabled", Target: endpointID,
		Details: map[string]any{"reason": reason, "url": webhook.Origin(url)}})
}

// FailStaleDeliveries fails up to limit pending deliveries of an earlier generation of their endpoint, in one write per
// endpoint, and returns how many it failed. It runs for every endpoint flagged stale, whatever its state now, so a
// re-enabled endpoint's old queue is failed too. Each write moves the endpoint's pending count by the rows it changed,
// and clears the flag once a write finds no more of them.
func (s *Store) FailStaleDeliveries(ctx context.Context, limit int, at time.Time) (int, error) {
	var found []string
	err := s.Write(ctx, func(tx *Tx) error {
		rows, err := tx.q.QueryContext(ctx, `SELECT id FROM webhook_endpoints WHERE stale_queue = 1 LIMIT 20`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			found = append(found, id)
		}
		return rows.Err()
	})
	if err != nil {
		return 0, err
	}
	total := 0
	for _, endpointID := range found {
		if total >= limit {
			break
		}
		err := s.Write(ctx, func(tx *Tx) error {
			room := limit - total
			// The endpoint's reason says why the queue failed while the endpoint stays disabled. A re-enable clears it, and
			// the token says only what happened.
			n, err := rowsOf(tx.q.ExecContext(ctx, `UPDATE webhook_deliveries SET state = 'failed',
				last_error = COALESCE(NULLIF((SELECT disabled_reason FROM webhook_endpoints WHERE id = ?), ''), ?),
				lease_until = NULL, finished_at = ?
				WHERE id IN (SELECT d.id FROM webhook_deliveries d WHERE d.endpoint_id = ? AND d.state = 'pending'
					AND d.generation < (SELECT generation FROM webhook_endpoints WHERE id = ?) LIMIT ?)`,
				endpointID, staleDeliveryError, millis(at), endpointID, endpointID, room))
			if err != nil {
				return err
			}
			total += n
			// A write that changed fewer rows than it could has found the last of them: the flag goes.
			_, err = tx.q.ExecContext(ctx, `UPDATE webhook_endpoints SET pending_count = pending_count - ?,
				stale_queue = CASE WHEN ? < ? THEN 0 ELSE stale_queue END WHERE id = ?`, n, n, room, endpointID)
			return err
		})
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// ClearExpiredPreviousSecrets drops the secret of a rotation whose grace period has ended, so the encrypted copy does
// not stay in the row until the next rotation (decision 26). It returns how many endpoints it cleared.
func (s *Store) ClearExpiredPreviousSecrets(ctx context.Context, now time.Time) (int64, error) {
	var n int64
	err := s.Write(ctx, func(tx *Tx) error {
		m, err := rowsOf(tx.q.ExecContext(ctx, `UPDATE webhook_endpoints SET previous_secret_enc = NULL,
			previous_secret_until = NULL WHERE previous_secret_until IS NOT NULL AND previous_secret_until <= ?`, millis(now)))
		n = int64(m)
		return err
	})
	return int64(n), err
}

// PruneWebhookDeliveries removes up to batch delivered rows finished before delivered, and up to batch failed rows
// finished before failed, and returns how many it removed. The caller repeats it, so a large backlog is removed in
// batches and the writer is held for one batch at a time.
func (s *Store) PruneWebhookDeliveries(ctx context.Context, delivered, failed time.Time, batch int) (int, error) {
	var total int
	err := s.Write(ctx, func(tx *Tx) error {
		for _, c := range []struct {
			state  string
			before time.Time
		}{{"delivered", delivered}, {"failed", failed}} {
			n, err := rowsOf(tx.q.ExecContext(ctx, `DELETE FROM webhook_deliveries WHERE id IN (
				SELECT id FROM webhook_deliveries WHERE state = ? AND finished_at < ? LIMIT ?)`,
				c.state, millis(c.before), batch))
			if err != nil {
				return err
			}
			total += n
		}
		return nil
	})
	return total, err
}

// PruneWebhookEvents removes the events that no delivery refers to, among the next batch events after the event row
// after. It returns the row to continue from (0 when the walk has reached the end of the table, so the caller starts
// again from 0), and how many events it removed. The write reads and deletes only one batch of rows by ID, so the
// writer is held for a bounded time however many events are retained (review finding 8).
func (s *Store) PruneWebhookEvents(ctx context.Context, after int64, batch int) (int64, int, error) {
	var next int64
	var total int
	err := s.Write(ctx, func(tx *Tx) error {
		var last sql.NullInt64
		var count int
		if err := tx.q.QueryRowContext(ctx, `SELECT MAX(id), COUNT(*) FROM (
			SELECT id FROM webhook_events WHERE id > ? ORDER BY id LIMIT ?)`, after, batch).Scan(&last, &count); err != nil {
			return err
		}
		n, err := rowsOf(tx.q.ExecContext(ctx, `DELETE FROM webhook_events WHERE id > ? AND id <= ?
			AND NOT EXISTS (SELECT 1 FROM webhook_deliveries d WHERE d.event_row_id = webhook_events.id)`,
			after, last.Int64))
		if err != nil {
			return err
		}
		total = n
		if count == batch {
			next = last.Int64
		}
		return nil
	})
	return next, total, err
}

// WebhookDelivery is one row of an endpoint's delivery log.
type WebhookDelivery struct {
	ID            string
	EventID       string
	EventType     string
	State         string
	Attempts      int
	LastCode      *int
	LastError     string
	NextAttemptAt time.Time
	CreatedAt     time.Time
	FinishedAt    *time.Time
	// eventRow orders the log and holds its cursor; it is the row of the event in webhook_events.
	eventRow int64
}

const deliveryCols = "d.id, ev.event_id, ev.type, d.state, d.attempts, d.last_code, d.last_error, d.next_attempt_at, d.created_at, d.finished_at, d.event_row_id"

func scanDelivery(row interface{ Scan(...any) error }) (*WebhookDelivery, error) {
	var (
		d             WebhookDelivery
		code          sql.NullInt64
		lastErr       sql.NullString
		next, created int64
		finished      sql.NullInt64
	)
	if err := row.Scan(&d.ID, &d.EventID, &d.EventType, &d.State, &d.Attempts, &code, &lastErr, &next, &created, &finished, &d.eventRow); err != nil {
		return nil, err
	}
	if code.Valid {
		c := int(code.Int64)
		d.LastCode = &c
	}
	d.LastError = lastErr.String
	d.NextAttemptAt = fromMillis(next)
	d.CreatedAt = fromMillis(created)
	d.FinishedAt = fromNullMillis(finished)
	return &d, nil
}

// WebhookDeliveries lists the deliveries of an endpoint of accountID, newest first, one page at a time. The order is the
// event's row order, which is the order the changes were made in: events of one millisecond keep their order, which a
// time order would not. The cursor is the event row of the last row of the page.
func (s *Store) WebhookDeliveries(ctx context.Context, accountID, endpointID string, p Page) ([]*WebhookDelivery, string, error) {
	where := "d.account_id = ? AND d.endpoint_id = ?"
	args := []any{accountID, endpointID}
	if p.Cursor != "" {
		parts, err := decodeCursor(p.Cursor, 1)
		if err != nil {
			return nil, "", err
		}
		row, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, "", ErrBadCursor
		}
		where += " AND d.event_row_id < ?"
		args = append(args, row)
	}
	args = append(args, p.Limit+1)
	rows, err := s.r.QueryContext(ctx, "SELECT "+deliveryCols+" FROM webhook_deliveries d JOIN webhook_events ev ON ev.id = d.event_row_id WHERE "+
		where+" ORDER BY d.event_row_id DESC LIMIT ?", args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []*WebhookDelivery
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > p.Limit {
		out = out[:p.Limit]
		last := out[len(out)-1]
		next = encodeCursor(strconv.FormatInt(last.eventRow, 10))
	}
	return out, next, nil
}

// RecentWebhookDeliveries returns the newest n deliveries of an endpoint of accountID, newest first.
func (s *Store) RecentWebhookDeliveries(ctx context.Context, accountID, endpointID string, n int) ([]*WebhookDelivery, error) {
	out, _, err := s.WebhookDeliveries(ctx, accountID, endpointID, Page{Limit: n})
	return out, err
}

// WebhookDelivery returns one delivery of an endpoint of accountID. Another account's or endpoint's delivery is
// ErrNotFound.
func (s *Store) WebhookDelivery(ctx context.Context, accountID, endpointID, deliveryID string) (*WebhookDelivery, error) {
	return scanDeliveryOf(s.r.QueryRowContext(ctx, deliverySelect, accountID, endpointID, deliveryID))
}

// WebhookDelivery reads one delivery inside the transaction.
func (tx *Tx) WebhookDelivery(ctx context.Context, accountID, endpointID, deliveryID string) (*WebhookDelivery, error) {
	return scanDeliveryOf(tx.q.QueryRowContext(ctx, deliverySelect, accountID, endpointID, deliveryID))
}

const deliverySelect = "SELECT " + deliveryCols + " FROM webhook_deliveries d JOIN webhook_events ev ON ev.id = d.event_row_id " +
	"WHERE d.account_id = ? AND d.endpoint_id = ? AND d.id = ?"

func scanDeliveryOf(row *sql.Row) (*WebhookDelivery, error) {
	d, err := scanDelivery(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

// RequeueWebhookDelivery makes a failed delivery pending again, due now, with a fresh 24-hour window and its attempts
// reset (review finding 4). It joins the endpoint's current generation, so the retry is sent under the endpoint's
// present configuration. It returns ErrNotFound for a delivery that is not failed. The caller checks the endpoint's
// state and room.
func (tx *Tx) RequeueWebhookDelivery(ctx context.Context, accountID, endpointID, deliveryID string, at time.Time) error {
	if err := rowsAffected(tx.q.ExecContext(ctx, `UPDATE webhook_deliveries SET state = 'pending', next_attempt_at = ?,
		finished_at = NULL, lease_until = NULL, first_attempt_at = NULL, attempts = 0,
		generation = (SELECT generation FROM webhook_endpoints WHERE id = ?)
		WHERE id = ? AND account_id = ? AND endpoint_id = ? AND state = 'failed'`,
		millis(at), endpointID, deliveryID, accountID, endpointID)); err != nil {
		return err
	}
	return tx.bumpPending(ctx, []string{endpointID}, 1)
}

// WebhookPending returns the pending deliveries of an endpoint of accountID, from its maintained count.
func (tx *Tx) WebhookPending(ctx context.Context, accountID, endpointID string) (int, error) {
	var n int
	err := tx.q.QueryRowContext(ctx, "SELECT pending_count FROM webhook_endpoints WHERE id = ? AND account_id = ?",
		endpointID, accountID).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return n, err
}

// WebhookStats is what the metrics report about the worker's queue, read at each scrape.
type WebhookStats struct {
	QueueDepth        int64            // pending deliveries of every endpoint
	Deliveries        map[string]int64 // deliveries by state
	DisabledEndpoints map[string]int64 // disabled endpoints by reason
}

// WebhookQueueStats reads WebhookStats.
func (s *Store) WebhookQueueStats(ctx context.Context) (WebhookStats, error) {
	out := WebhookStats{Deliveries: map[string]int64{}, DisabledEndpoints: map[string]int64{}}
	if err := s.r.QueryRowContext(ctx, "SELECT COALESCE(SUM(pending_count), 0) FROM webhook_endpoints").Scan(&out.QueueDepth); err != nil {
		return out, err
	}
	if err := groupCounts(ctx, s.r, out.Deliveries, "SELECT state, COUNT(*) FROM webhook_deliveries GROUP BY state"); err != nil {
		return out, err
	}
	err := groupCounts(ctx, s.r, out.DisabledEndpoints,
		"SELECT COALESCE(disabled_reason, ''), COUNT(*) FROM webhook_endpoints WHERE state = 'disabled' GROUP BY disabled_reason")
	return out, err
}

func groupCounts(ctx context.Context, q querier, into map[string]int64, query string) error {
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int64
		if err := rows.Scan(&k, &n); err != nil {
			return err
		}
		into[k] = n
	}
	return rows.Err()
}

// rowsOf returns the rows a statement changed.
func rowsOf(res sql.Result, err error) (int, error) {
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}
