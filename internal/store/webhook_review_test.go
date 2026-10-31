// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
	"time"
)

// A re-enabled endpoint is never offered the queue it had when it was disabled (review finding 2 and 3). The disable
// raised its generation, so those deliveries are stale: no claim takes them, and the cleanup fails them whatever the
// endpoint's state is by then. pending_count stays equal to the pending rows throughout.
func TestReEnabledEndpointSendsNoOldQueue(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "Acme")
	e := newEndpointRow(t, s, acc, "https://re.example.com/in", "file.created")
	for _, name := range []string{"a", "b", "c"} {
		emitFile(t, s, acc, name, Now())
	}
	if err := s.DisableWebhookEndpoint(ctx, acc, e.ID, WebhookDisabledFailing, Now(), "https://re.example.com/in"); err != nil {
		t.Fatal(err)
	}
	// The owner changes the URL and activates the endpoint in one request, before any cleanup has run.
	if err := s.Write(ctx, func(tx *Tx) error {
		cur, err := tx.WebhookEndpoint(ctx, acc, e.ID)
		if err != nil {
			return err
		}
		cur.URL = "https://re.example.com/new"
		cur.State = WebhookActive
		cur.DisabledReason = ""
		return tx.UpdateWebhookEndpoint(ctx, cur)
	}); err != nil {
		t.Fatal(err)
	}
	if jobs, _ := s.ClaimWebhookJobs(ctx, Now(), time.Minute, 50, ""); len(jobs) != 0 {
		t.Fatalf("the re-enabled endpoint was offered %d deliveries of the queue it had when disabled", len(jobs))
	}
	// An event after the re-enable is sent, and the stale three are not in front of it.
	emitFile(t, s, acc, "d", Now())
	jobs, _ := s.ClaimWebhookJobs(ctx, Now(), time.Minute, 50, e.ID)
	if len(jobs) != 1 {
		t.Fatalf("after a new event the claim returned %d deliveries, want the new one only", len(jobs))
	}
	requireCounted(t, s, acc, e.ID, "after the re-enable and a new event")
	n, err := s.FailStaleDeliveries(ctx, 100, Now())
	if err != nil || n != 3 {
		t.Fatalf("cleanup failed %d deliveries, %v; want the 3 stale ones", n, err)
	}
	requireCounted(t, s, acc, e.ID, "after the cleanup")
	if got := pendingCount(t, s, acc, e.ID); got != 1 {
		t.Fatalf("pending count after the cleanup = %d, want 1 (the new delivery, which is in flight)", got)
	}
}

// A retry of an old delivery is not sent while the endpoint's head is in flight (review finding 4). The endpoint has one
// request at a time, whatever the retry order.
func TestRetryDoesNotRunBesideAnInFlightHead(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "Acme")
	e := newEndpointRow(t, s, acc, "https://order.example.com/in", "file.created")
	t0 := Now()
	emitFile(t, s, acc, "a", t0)
	emitFile(t, s, acc, "b", t0)
	first, _ := s.ClaimWebhookJobs(ctx, t0, time.Minute, 1, e.ID)
	if err := s.RecordWebhookOutcome(ctx, WebhookOutcome{DeliveryID: first[0].DeliveryID, AccountID: acc, EndpointID: e.ID,
		At: t0, Code: 500, Err: "status 500", NextAt: t0, GiveUp: true, Lease: first[0].Lease}); err != nil {
		t.Fatal(err)
	}
	head, _ := s.ClaimWebhookJobs(ctx, t0, time.Minute, 1, e.ID)
	if len(head) != 1 {
		t.Fatalf("the second event is not claimed: %d", len(head))
	}
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.RequeueWebhookDelivery(ctx, acc, e.ID, first[0].DeliveryID, t0)
	}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ClaimWebhookJobs(ctx, t0, time.Minute, 50, e.ID); len(got) != 0 {
		t.Fatalf("a second request for the endpoint while its head is in flight: %d jobs", len(got))
	}
}

// A retry is a fresh delivery: its attempts and its 24-hour window start again (review finding 4, the cosmetic part).
func TestRetryGivesAFreshWindow(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "Acme")
	e := newEndpointRow(t, s, acc, "https://fresh.example.com/in", "file.created")
	t0 := Now()
	emitFile(t, s, acc, "a", t0)
	jobs, _ := s.ClaimWebhookJobs(ctx, t0, time.Minute, 1, e.ID)
	if err := s.RecordWebhookOutcome(ctx, WebhookOutcome{DeliveryID: jobs[0].DeliveryID, AccountID: acc, EndpointID: e.ID,
		At: t0, Code: 500, Err: "status 500", NextAt: t0, GiveUp: true, Lease: jobs[0].Lease}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.RequeueWebhookDelivery(ctx, acc, e.ID, jobs[0].DeliveryID, t0)
	}); err != nil {
		t.Fatal(err)
	}
	d, err := s.WebhookDelivery(ctx, acc, e.ID, jobs[0].DeliveryID)
	if err != nil || d.Attempts != 0 {
		t.Fatalf("after a retry the delivery has %d attempts, %v; want 0", d.Attempts, err)
	}
	again, _ := s.ClaimWebhookJobs(ctx, t0, time.Minute, 1, e.ID)
	if len(again) != 1 || again[0].FirstAttemptAt.UnixMilli() != t0.UnixMilli() {
		t.Fatalf("the retry's first attempt is not its own: %+v", again)
	}
}

// An outcome applies only to the claim that made it (review finding 5). A lease that lapsed and a second claim: the late
// failure of the first claim changes no counter and does not touch the second claim's lease.
func TestOutcomeOfALapsedLeaseChangesNothing(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "Acme")
	e := newEndpointRow(t, s, acc, "https://lapse.example.com/in", "file.created")
	t0 := Now()
	emitFile(t, s, acc, "a", t0)
	first, _ := s.ClaimWebhookJobs(ctx, t0, time.Minute, 1, e.ID)
	t2 := t0.Add(2 * time.Minute)
	second, _ := s.ClaimWebhookJobs(ctx, t2, time.Minute, 1, e.ID)
	if len(second) != 1 {
		t.Fatalf("the lapsed delivery was not claimed again: %d", len(second))
	}
	if err := s.RecordWebhookOutcome(ctx, WebhookOutcome{DeliveryID: first[0].DeliveryID, AccountID: acc, EndpointID: e.ID,
		At: t2, Code: 500, Err: "status 500", NextAt: t2.Add(time.Minute), Lease: first[0].Lease}); err != nil {
		t.Fatal(err)
	}
	d, _ := s.WebhookDelivery(ctx, acc, e.ID, first[0].DeliveryID)
	if d.Attempts != 0 || d.State != "pending" {
		t.Fatalf("a lapsed claim's outcome moved the delivery: attempts %d, state %s", d.Attempts, d.State)
	}
	ep, _ := s.WebhookEndpoint(ctx, acc, e.ID)
	if ep.ConsecutiveFailures != 0 {
		t.Fatalf("a lapsed claim's outcome moved the endpoint's failures to %d", ep.ConsecutiveFailures)
	}
	if got, _ := s.ClaimWebhookJobs(ctx, t2, time.Minute, 1, e.ID); len(got) != 0 {
		t.Fatal("the lapsed outcome cleared the current claim's lease")
	}
	if err := s.RecordWebhookOutcome(ctx, WebhookOutcome{DeliveryID: second[0].DeliveryID, AccountID: acc, EndpointID: e.ID,
		At: t2, Code: 204, Lease: second[0].Lease}); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.WebhookDelivery(ctx, acc, e.ID, second[0].DeliveryID); d.State != "delivered" {
		t.Fatalf("the current claim's success did not apply: %s", d.State)
	}
	requireCounted(t, s, acc, e.ID, "after the current outcome")
}

// The cleanup fails only deliveries of an earlier generation, and the rows it fails keep pending_count right.
func TestFailStaleDeliveriesTakesOnlyTheOldGeneration(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "Acme")
	e := newEndpointRow(t, s, acc, "https://gen.example.com/in", "file.created")
	emitFile(t, s, acc, "a", Now())
	if err := s.DisableWebhookEndpoint(ctx, acc, e.ID, WebhookDisabledFailing, Now(), "https://gen.example.com/in"); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx *Tx) error {
		cur, err := tx.WebhookEndpoint(ctx, acc, e.ID)
		if err != nil {
			return err
		}
		cur.State = WebhookActive
		cur.DisabledReason = ""
		return tx.UpdateWebhookEndpoint(ctx, cur)
	}); err != nil {
		t.Fatal(err)
	}
	emitFile(t, s, acc, "b", Now())
	n, err := s.FailStaleDeliveries(ctx, 100, Now())
	if err != nil || n != 1 {
		t.Fatalf("cleanup failed %d, %v; want 1 (the earlier generation only)", n, err)
	}
	requireCounted(t, s, acc, e.ID, "after the cleanup")
	if got := pendingRows(t, s, e.ID); got != 1 {
		t.Fatalf("pending rows = %d, want the current generation's 1", got)
	}
}

// Pruning events walks from a cursor and deletes only the events no delivery names, a batch at a time (review finding 8).
// The cursor wraps to 0 when the table has been walked.
func TestPruneWebhookEventsFromACursor(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "Acme")
	gone := newEndpointRow(t, s, acc, "https://gone.example.com/in", "file.created")
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		emitFile(t, s, acc, name, Now())
	}
	// Removing the only endpoint cascades its deliveries and leaves its five events unnamed.
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteWebhookEndpoint(ctx, acc, gone.ID) }); err != nil {
		t.Fatal(err)
	}
	newEndpointRow(t, s, acc, "https://kept.example.com/in", "file.created")
	emitFile(t, s, acc, "f", Now()) // named by the kept endpoint, so it stays
	removed, cursor, calls := 0, int64(0), 0
	for {
		next, n, err := s.PruneWebhookEvents(ctx, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		calls++
		removed += n
		cursor = next
		if cursor == 0 || calls > 100 {
			break
		}
	}
	if removed != 5 {
		t.Fatalf("pruned %d events, want the 5 nobody names", removed)
	}
	if calls < 3 {
		t.Fatalf("batches of 2 walked 6 events in %d calls, want at least 3", calls)
	}
	if got := len(eventsOf(t, s, acc)); got != 1 {
		t.Fatalf("events left = %d, want the 1 the kept endpoint names", got)
	}
}
