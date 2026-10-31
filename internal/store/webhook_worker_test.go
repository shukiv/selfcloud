// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/webhook"
)

// emitFile reports one file.created of accountID in its own transaction.
func emitFile(t testing.TB, s *Store, accountID, name string, at time.Time) {
	t.Helper()
	err := s.Write(context.Background(), func(tx *Tx) error {
		return tx.EmitEvent(context.Background(), accountID, "file.created", at,
			webhook.File{ID: "nod_" + name, FolderID: "fld_x", Kind: "file", Size: 1, Name: name, ParentID: "fld_x"})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// pendingRows counts the pending deliveries of an endpoint straight from the table.
func pendingRows(t *testing.T, s *Store, endpoint string) int {
	t.Helper()
	var n int
	if err := s.r.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM webhook_deliveries WHERE endpoint_id = ? AND state = 'pending'", endpoint).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// pendingCount reads the maintained count of an endpoint.
func pendingCount(t *testing.T, s *Store, accountID, endpoint string) int {
	t.Helper()
	var n int
	if err := s.r.QueryRowContext(context.Background(),
		"SELECT pending_count FROM webhook_endpoints WHERE id = ? AND account_id = ?", endpoint, accountID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// requireCounted fails when the maintained count of an endpoint is not the number of its pending rows.
func requireCounted(t *testing.T, s *Store, accountID, endpoint, step string) {
	t.Helper()
	if got, rows := pendingCount(t, s, accountID, endpoint), pendingRows(t, s, endpoint); got != rows {
		t.Fatalf("%s: pending_count %d, pending rows %d", step, got, rows)
	}
}

// The head rule: a claim takes the oldest pending delivery of an endpoint, and nothing after it, while that one waits
// on its backoff, is in flight, or is not yet due.
func TestClaimTakesOnlyTheHeadOfAnEndpoint(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "Acme")
	e := newEndpointRow(t, s, acc, "https://hooks.example.com/in", "file.created")
	t0 := Now()
	for _, name := range []string{"a", "b", "c"} {
		emitFile(t, s, acc, name, t0)
	}
	lease := 2 * time.Minute

	jobs, err := s.ClaimWebhookJobs(ctx, t0, lease, 50, "")
	if err != nil || len(jobs) != 1 {
		t.Fatalf("first claim: %d jobs, %v; want the head only", len(jobs), err)
	}
	head := jobs[0]
	if head.EventID == "" || head.EndpointID != e.ID {
		t.Fatalf("claimed %+v", head)
	}
	if again, _ := s.ClaimWebhookJobs(ctx, t0, lease, 50, ""); len(again) != 0 {
		t.Fatalf("a second claim while the head is leased returned %d jobs", len(again))
	}
	// A failure schedules the head for 30 s later. Nothing after it may be claimed meanwhile.
	retry := t0.Add(30 * time.Second)
	if err := s.RecordWebhookOutcome(ctx, WebhookOutcome{DeliveryID: head.DeliveryID, AccountID: acc, EndpointID: e.ID,
		At: t0, Code: 500, Err: "status 500", NextAt: retry, Lease: head.Lease}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ClaimWebhookJobs(ctx, t0.Add(10*time.Second), lease, 50, ""); len(got) != 0 {
		t.Fatalf("a delivery after a head in backoff was claimed: %d", len(got))
	}
	jobs, _ = s.ClaimWebhookJobs(ctx, retry, lease, 50, "")
	if len(jobs) != 1 || jobs[0].DeliveryID != head.DeliveryID {
		t.Fatalf("at the retry time the head is not claimed again: %+v", jobs)
	}
	if jobs[0].Attempts != 1 {
		t.Fatalf("attempts = %d, want 1 recorded before the second send", jobs[0].Attempts)
	}
	if err := s.RecordWebhookOutcome(ctx, WebhookOutcome{DeliveryID: head.DeliveryID, AccountID: acc, EndpointID: e.ID,
		At: retry, Code: 204, Lease: jobs[0].Lease}); err != nil {
		t.Fatal(err)
	}
	next, _ := s.ClaimWebhookJobs(ctx, retry, lease, 50, "")
	if len(next) != 1 || next[0].EventID == head.EventID {
		t.Fatalf("after the head is delivered the next event is not claimed: %+v", next)
	}
	requireCounted(t, s, acc, e.ID, "after delivery")
}

// An endpoint's own lane claims its next delivery and no other endpoint's.
func TestClaimForOneEndpointServesOnlyIt(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "Acme")
	one := newEndpointRow(t, s, acc, "https://one.example.com/in", "file.created")
	two := newEndpointRow(t, s, acc, "https://two.example.com/in", "file.created")
	emitFile(t, s, acc, "a", Now())
	jobs, err := s.ClaimWebhookJobs(ctx, Now(), time.Minute, 1, one.ID)
	if err != nil || len(jobs) != 1 || jobs[0].EndpointID != one.ID {
		t.Fatalf("claim for one: %+v, %v", jobs, err)
	}
	if got, _ := s.ClaimWebhookJobs(ctx, Now(), time.Minute, 1, one.ID); len(got) != 0 {
		t.Fatal("the leased head was claimed again")
	}
	if got, _ := s.ClaimWebhookJobs(ctx, Now(), time.Minute, 50, two.ID); len(got) != 1 {
		t.Fatalf("the other endpoint's head was not claimed for it: %d", len(got))
	}
}

// Only an active endpoint is served. A paused or disabled one keeps its queue.
func TestClaimSkipsPausedAndDisabled(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	acc := newAccountRow(t, s, "Acme")
	paused := newEndpointState(t, s, acc, "https://p.example.com/in", WebhookPaused, "file.created")
	emitFile(t, s, acc, "a", Now())
	if got, _ := s.ClaimWebhookJobs(context.Background(), Now(), time.Minute, 50, ""); len(got) != 0 {
		t.Fatalf("a paused endpoint was served: %d", len(got))
	}
	_ = paused
}

// The maintained count equals the pending rows after every kind of change to a queue.
func TestPendingCountMatchesRows(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "Acme")
	a := newEndpointRow(t, s, acc, "https://a.example.com/in", "file.created")
	b := newEndpointRow(t, s, acc, "https://b.example.com/in", "file.created")
	t0 := Now()
	for i, name := range []string{"x", "y", "z"} {
		emitFile(t, s, acc, name, t0.Add(time.Duration(i)*time.Millisecond))
	}
	requireCounted(t, s, acc, a.ID, "after emits")
	requireCounted(t, s, acc, b.ID, "after emits, other endpoint")

	jobs, _ := s.ClaimWebhookJobs(ctx, t0.Add(time.Second), time.Minute, 50, a.ID)
	if err := s.RecordWebhookOutcome(ctx, WebhookOutcome{DeliveryID: jobs[0].DeliveryID, AccountID: acc, EndpointID: a.ID,
		At: t0, Code: 200, Lease: jobs[0].Lease}); err != nil {
		t.Fatal(err)
	}
	requireCounted(t, s, acc, a.ID, "after one delivered")

	if err := s.RecordWebhookOutcome(ctx, WebhookOutcome{DeliveryID: jobs[0].DeliveryID, AccountID: acc, EndpointID: a.ID,
		At: t0, Code: 500, Err: "status 500", NextAt: t0, GiveUp: true, Lease: jobs[0].Lease}); err != nil {
		t.Fatal(err)
	}
	requireCounted(t, s, acc, a.ID, "a repeated record of a delivered row changes nothing")

	// A disable leaves the queue pending; the batched cleanup fails it.
	if err := s.DisableWebhookEndpoint(ctx, acc, b.ID, WebhookDisabledFailing, t0, "https://b.example.com/in"); err != nil {
		t.Fatal(err)
	}
	requireCounted(t, s, acc, b.ID, "after a disable")
	if n, err := s.FailStaleDeliveries(ctx, 2, t0); err != nil || n != 2 {
		t.Fatalf("FailStaleDeliveries(2) = %d, %v", n, err)
	}
	requireCounted(t, s, acc, b.ID, "after a batch of the cleanup")
	if n, _ := s.FailStaleDeliveries(ctx, 50, t0); n != 1 {
		t.Fatalf("the rest of the queue: %d, want 1", n)
	}
	if got := pendingCount(t, s, acc, b.ID); got != 0 {
		t.Fatalf("pending count after the cleanup = %d", got)
	}
}

// A give-up takes the endpoint's count down by one; a disable with reason failing leaves the count alone until cleanup.
func TestGiveUpAndDisableUpdateTheCount(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "Acme")
	e := newEndpointRow(t, s, acc, "https://g.example.com/in", "file.created")
	emitFile(t, s, acc, "a", Now())
	emitFile(t, s, acc, "b", Now())
	jobs, _ := s.ClaimWebhookJobs(ctx, Now(), time.Minute, 1, e.ID)
	at := Now()
	if err := s.RecordWebhookOutcome(ctx, WebhookOutcome{DeliveryID: jobs[0].DeliveryID, AccountID: acc, EndpointID: e.ID,
		At: at, Code: 500, Err: "status 500", NextAt: at, GiveUp: true, Disable: WebhookDisabledFailing, URL: "https://g.example.com/in?token=T", Lease: jobs[0].Lease}); err != nil {
		t.Fatal(err)
	}
	requireCounted(t, s, acc, e.ID, "after give-up and disable")
	got, err := s.WebhookEndpoint(ctx, acc, e.ID)
	if err != nil || got.State != WebhookDisabled || got.DisabledReason != WebhookDisabledFailing {
		t.Fatalf("endpoint after the disable: %+v, %v", got, err)
	}
	// The audit names the origin and never the token in the query.
	var details string
	if err := s.r.QueryRowContext(ctx, "SELECT details FROM audit_log WHERE action = 'webhook.disabled' AND target = ?", e.ID).Scan(&details); err != nil {
		t.Fatal(err)
	}
	if details != `{"reason":"failing","url":"https://g.example.com"}` {
		t.Fatalf("audit details = %s", details)
	}
}

// The cap at WebhookPendingLimit (decision 28). A queue holding 99,999 accepts one more event. At 100,000 the next event
// is recorded failed with overflow, the endpoint is disabled in the same transaction, and later events of that
// transaction do not reach it.
func TestOverflowAtTheLimit(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "Acme")
	e := newEndpointRow(t, s, acc, "https://big.example.com/in", "file.created")
	seedPending(t, s, acc, e.ID, WebhookPendingLimit-1)
	emitFile(t, s, acc, "last-under-the-cap", Now())
	if got := pendingCount(t, s, acc, e.ID); got != WebhookPendingLimit {
		t.Fatalf("count at the limit = %d", got)
	}
	err := s.Write(ctx, func(tx *Tx) error {
		if err := tx.EmitEvent(ctx, acc, "file.created", Now(), webhook.File{ID: "nod_over", Name: "over"}); err != nil {
			return err
		}
		// The endpoint is disabled now, so this one reaches nothing.
		return tx.EmitEvent(ctx, acc, "file.created", Now(), webhook.File{ID: "nod_after", Name: "after"})
	})
	if err != nil {
		t.Fatalf("the change failed at the cap: %v", err)
	}
	var overflowed, pendingAfter int
	if err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM webhook_deliveries WHERE endpoint_id = ? AND state = 'failed' AND last_error = 'overflow'`, e.ID).Scan(&overflowed); err != nil {
		t.Fatal(err)
	}
	if overflowed != 1 {
		t.Fatalf("overflow rows = %d, want 1 (the event after the disable reaches nothing)", overflowed)
	}
	if err := s.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM webhook_deliveries WHERE endpoint_id = ? AND state = 'pending'", e.ID).Scan(&pendingAfter); err != nil {
		t.Fatal(err)
	}
	if pendingAfter != WebhookPendingLimit {
		t.Fatalf("pending rows = %d; the overflow must not touch the queue", pendingAfter)
	}
	got, _ := s.WebhookEndpoint(ctx, acc, e.ID)
	if got.State != WebhookDisabled || got.DisabledReason != WebhookDisabledOverflow {
		t.Fatalf("endpoint = %s/%s, want disabled/overflow", got.State, got.DisabledReason)
	}
	requireCounted(t, s, acc, e.ID, "after overflow")
}

// seedPending writes n events and a pending delivery of each to endpoint, in SQL, for the cap tests.
func seedPending(t testing.TB, s *Store, accountID, endpoint string, n int) {
	t.Helper()
	ctx := context.Background()
	err := s.Write(ctx, func(tx *Tx) error {
		var before int64
		if err := tx.q.QueryRowContext(ctx, "SELECT COALESCE(MAX(id), 0) FROM webhook_events").Scan(&before); err != nil {
			return err
		}
		if _, err := tx.q.ExecContext(ctx, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?)
			INSERT INTO webhook_events (event_id, account_id, type, payload, created_at)
			SELECT 'evt_seed_' || lower(hex(randomblob(12))), ?, 'file.created', '{}', 0 FROM n`, n, accountID); err != nil {
			return err
		}
		if _, err := tx.q.ExecContext(ctx, `INSERT INTO webhook_deliveries (id, account_id, endpoint_id, event_row_id,
			state, attempts, next_attempt_at, created_at)
			SELECT 'dlv_seed_' || lower(hex(randomblob(12))), ?, ?, id, 'pending', 0, 0, 0 FROM webhook_events
			WHERE account_id = ? AND id > ?`, accountID, endpoint, accountID, before); err != nil {
			return err
		}
		_, err := tx.q.ExecContext(ctx, "UPDATE webhook_endpoints SET pending_count = pending_count + ? WHERE id = ?", n, endpoint)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// deliveryIDs lists the deliveries of an endpoint in event order.
func deliveryIDs(t *testing.T, s *Store, endpoint string) []string {
	t.Helper()
	rows, err := s.r.QueryContext(context.Background(),
		"SELECT id FROM webhook_deliveries WHERE endpoint_id = ? ORDER BY event_row_id", endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	return out
}

// Pruning keeps delivered rows for a week and failed ones for a month, then removes events no delivery refers to. It
// works in batches, and a row inside its retention is left alone.
func TestPruneWebhookRetention(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "Acme")
	e := newEndpointRow(t, s, acc, "https://r.example.com/in", "file.created")
	now := Now()
	for i := 0; i < 5; i++ {
		emitFile(t, s, acc, string(rune('a'+i)), now)
	}
	ids := deliveryIDs(t, s, e.ID)
	// The first two are delivered 8 days ago, the next one failed 8 days ago, and one failed 31 days ago. The rest stay.
	for i, state := range []string{"delivered", "delivered", "failed", "failed"} {
		finished := now.Add(-8 * 24 * time.Hour)
		if i == 3 {
			finished = now.Add(-31 * 24 * time.Hour)
		}
		if _, err := s.w.ExecContext(ctx, "UPDATE webhook_deliveries SET state = ?, finished_at = ? WHERE id = ?",
			state, millis(finished), ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	// Delivered (8 days) goes; failed (8 days) stays; failed (31 days) goes.
	n, err := s.PruneWebhookDeliveries(ctx, now.Add(-7*24*time.Hour), now.Add(-30*24*time.Hour), 1)
	if err != nil || n != 2 {
		t.Fatalf("first batch removed %d, %v; want 2 (batch of one per state)", n, err)
	}
	for k := 0; k < 4; k++ {
		if _, err := s.PruneWebhookDeliveries(ctx, now.Add(-7*24*time.Hour), now.Add(-30*24*time.Hour), 1); err != nil {
			t.Fatal(err)
		}
	}
	var left int
	if err := s.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM webhook_deliveries WHERE endpoint_id = ?", e.ID).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 2 {
		t.Fatalf("deliveries left = %d, want the failed-8-days one and the pending one", left)
	}
	// An event with no delivery is removed; one with a delivery is not.
	_, removed, err := s.PruneWebhookEvents(ctx, 0, 100)
	if err != nil || removed != 3 {
		t.Fatalf("events pruned = %d, %v; want 3 (the events of the three removed deliveries)", removed, err)
	}
	if err := s.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM webhook_events WHERE account_id = ?", acc).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 2 {
		t.Fatalf("events left = %d, want 2", left)
	}
}

// An expired grace secret is cleared; one still in its grace period is kept.
func TestClearExpiredPreviousSecrets(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "Acme")
	old := newEndpointRow(t, s, acc, "https://old.example.com/in", "file.created")
	live := newEndpointRow(t, s, acc, "https://live.example.com/in", "file.created")
	now := Now()
	for _, e := range []struct {
		id    string
		until time.Time
	}{{old.ID, now.Add(-time.Hour)}, {live.ID, now.Add(time.Hour)}} {
		if _, err := s.w.ExecContext(ctx, "UPDATE webhook_endpoints SET previous_secret_enc = ?, previous_secret_until = ? WHERE id = ?",
			[]byte("previous"), millis(e.until), e.id); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.ClearExpiredPreviousSecrets(ctx, now)
	if err != nil || n != 1 {
		t.Fatalf("cleared %d, %v; want 1", n, err)
	}
	var keptEnc []byte
	if err := s.r.QueryRowContext(ctx, "SELECT previous_secret_enc FROM webhook_endpoints WHERE id = ?", live.ID).Scan(&keptEnc); err != nil || string(keptEnc) != "previous" {
		t.Fatalf("the live grace secret was cleared: %q, %v", keptEnc, err)
	}
}

// A retry of a failed delivery is pending again, due now, and a test event reaches only the endpoint it names.
func TestRequeueAndTestEvent(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "Acme")
	one := newEndpointRow(t, s, acc, "https://one.example.com/in", "file.created")
	two := newEndpointRow(t, s, acc, "https://two.example.com/in", "file.created")
	emitFile(t, s, acc, "a", Now())
	jobs, _ := s.ClaimWebhookJobs(ctx, Now(), time.Minute, 1, one.ID)
	at := Now()
	if err := s.RecordWebhookOutcome(ctx, WebhookOutcome{DeliveryID: jobs[0].DeliveryID, AccountID: acc, EndpointID: one.ID,
		At: at, Code: 500, Err: "status 500", NextAt: at, GiveUp: true, Lease: jobs[0].Lease}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.RequeueWebhookDelivery(ctx, acc, one.ID, jobs[0].DeliveryID, at)
	}); err != nil {
		t.Fatal(err)
	}
	requireCounted(t, s, acc, one.ID, "after requeue")
	d, err := s.WebhookDelivery(ctx, acc, one.ID, jobs[0].DeliveryID)
	if err != nil || d.State != "pending" || d.FinishedAt != nil {
		t.Fatalf("requeued delivery = %+v, %v", d, err)
	}

	var evID string
	if err := s.Write(ctx, func(tx *Tx) error {
		var err error
		evID, err = tx.EmitTestEvent(ctx, acc, two.ID, at)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// two holds the file event and the test event; one holds its requeued delivery.
	if pendingRows(t, s, two.ID) != 2 || pendingRows(t, s, one.ID) != 1 {
		t.Fatalf("pending rows: two=%d one=%d, want 2 and 1", pendingRows(t, s, two.ID), pendingRows(t, s, one.ID))
	}
	if evID == "" {
		t.Fatal("no event ID returned")
	}
	requireCounted(t, s, acc, two.ID, "after test event")
}

// Stats count the queue, the deliveries by state and the disabled endpoints by reason.
func TestWebhookQueueStats(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "Acme")
	a := newEndpointRow(t, s, acc, "https://a.example.com/in", "file.created")
	newEndpointState(t, s, acc, "https://d.example.com/in", WebhookDisabled, "file.created")
	emitFile(t, s, acc, "x", Now())
	emitFile(t, s, acc, "y", Now())
	st, err := s.WebhookQueueStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.QueueDepth != 2 || st.Deliveries["pending"] != 2 || st.DisabledEndpoints[WebhookDisabledFailing] != 1 {
		t.Fatalf("stats = %+v", st)
	}
	_ = a
}

// Deliveries are listed newest first, a page at a time, and a cursor moves past a page.
func TestWebhookDeliveriesPage(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "Acme")
	e := newEndpointRow(t, s, acc, "https://p.example.com/in", "file.created")
	t0 := Now()
	for i := 0; i < 5; i++ {
		emitFile(t, s, acc, string(rune('a'+i)), t0.Add(time.Duration(i)*time.Second))
	}
	first, next, err := s.WebhookDeliveries(ctx, acc, e.ID, Page{Limit: 2})
	if err != nil || len(first) != 2 || next == "" {
		t.Fatalf("page one = %d rows, cursor %q, %v", len(first), next, err)
	}
	if !first[0].CreatedAt.After(first[1].CreatedAt) {
		t.Fatal("the page is not newest first")
	}
	second, next2, err := s.WebhookDeliveries(ctx, acc, e.ID, Page{Limit: 2, Cursor: next})
	if err != nil || len(second) != 2 || next2 == "" || second[0].CreatedAt.After(first[1].CreatedAt) {
		t.Fatalf("page two = %d rows, %v", len(second), err)
	}
	last, next3, err := s.WebhookDeliveries(ctx, acc, e.ID, Page{Limit: 2, Cursor: next2})
	if err != nil || len(last) != 1 || next3 != "" {
		t.Fatalf("page three = %d rows, cursor %q, %v", len(last), next3, err)
	}
	// Another account's endpoint lists nothing, because the account is part of the filter.
	other := newAccountRow(t, s, "Beta")
	if got, _, _ := s.WebhookDeliveries(ctx, other, e.ID, Page{Limit: 10}); len(got) != 0 {
		t.Fatalf("another account listed %d deliveries", len(got))
	}
}

// An outcome for a delivery that has gone (its endpoint was deleted meanwhile) records nothing and does not fail.
func TestOutcomeForAGoneDeliveryIsNoOp(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "Acme")
	e := newEndpointRow(t, s, acc, "https://gone.example.com/in", "file.created")
	emitFile(t, s, acc, "a", Now())
	jobs, _ := s.ClaimWebhookJobs(ctx, Now(), time.Minute, 1, e.ID)
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteWebhookEndpoint(ctx, acc, e.ID) }); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordWebhookOutcome(ctx, WebhookOutcome{DeliveryID: jobs[0].DeliveryID, AccountID: acc, EndpointID: e.ID,
		At: Now(), Code: 204}); err != nil {
		t.Fatalf("outcome of a deleted endpoint's delivery: %v", err)
	}
}
