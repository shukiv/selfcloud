// SPDX-License-Identifier: AGPL-3.0-or-later

package api

// The webhook worker (design section 10, slice j3, decision 28). It runs beside the job runner and the sweep, on the
// same context, and is not a job: a delivery that fails and waits for its retry must not hold up the jobs behind it.
//
// Each delivery is claimed in one write, sent with no transaction open, and its outcome recorded in another write.
// A claim takes the oldest pending delivery of each endpoint only, so an endpoint's deliveries are sent one at a time,
// in event order. Each claimed delivery starts a lane. A lane sends its delivery, records the outcome, and claims the
// endpoint's next delivery itself, for at most webhookLaneBudget; then it gives its token back, so the next claim, ranked
// by account, decides who is served. A slow endpoint holds up only its own lane. At most webhookSendSlots requests are
// in flight at once, and at most webhookBatch deliveries are claimed and not yet finished.

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/version"
	"github.com/shukiv/selfcloud/internal/webhook"
)

const (
	// webhookTick is how often a round looks for deliveries that have come due.
	webhookTick = time.Second
	// webhookBatch is the most deliveries claimed in one write, and so the most lanes at once.
	webhookBatch = 50
	// webhookSendSlots is the most requests in flight at once.
	webhookSendSlots = 8
	// webhookLease is how long a claim holds a delivery. A lane waits for a send slot inside it: 50 lanes at 8 slots
	// with a 10-second attempt finish their waits within about a minute, so no live send is claimed a second time.
	webhookLease = 2 * time.Minute
	// webhookCleanBatch is how many pending deliveries of disabled endpoints one round fails, in one write per endpoint.
	webhookCleanBatch = 2000
	// webhookLaneBudget is how long a lane keeps its token: it sends its endpoint's next deliveries until this much time
	// has passed, then gives the token back, so the claim of the round decides who is served next (review finding 1).
	webhookLaneBudget = 2 * time.Second
)

// webhookWorker holds the state of the worker. Its clock and jitter are functions, so tests run the schedule on a
// fake clock.
type webhookWorker struct {
	s      *Server
	clock  func() time.Time
	jitter func() float64
	// tokens holds one value per deliveries that may be claimed and not yet finished.
	tokens chan struct{}
	// slots limits the requests in flight.
	slots chan struct{}
	wg    sync.WaitGroup
}

// newWebhookWorker returns a worker of s, with its clock and jitter set from the server's.
func (s *Server) newWebhookWorker() *webhookWorker {
	w := &webhookWorker{s: s, clock: s.webhookClock, jitter: s.webhookJitter,
		tokens: make(chan struct{}, webhookBatch), slots: make(chan struct{}, webhookSendSlots)}
	for range webhookBatch {
		w.tokens <- struct{}{}
	}
	return w
}

// runWebhooks runs the worker until ctx ends. Its lanes finish their current send before it returns.
func (s *Server) runWebhooks(ctx context.Context) {
	w := s.newWebhookWorker()
	defer w.wg.Wait()
	w.wg.Add(1)
	go w.cleaner(ctx)
	t := time.NewTicker(webhookTick)
	defer t.Stop()
	for {
		if w.round(ctx) > 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// round claims the deliveries that are due, up to the free lanes, and starts a lane for each. It also fails a batch of
// the pending deliveries of disabled endpoints. It returns how many lanes it started.
func (w *webhookWorker) round(ctx context.Context) int {
	select {
	case <-ctx.Done():
		return 0
	case <-w.tokens:
	}
	want := 1
	for want < webhookBatch && w.takeToken() {
		want++
	}
	now := w.clock()
	jobs, err := w.s.store.ClaimWebhookJobs(ctx, now, webhookLease, want, "")
	for i := len(jobs); i < want; i++ {
		w.tokens <- struct{}{}
	}
	if err != nil {
		if ctx.Err() == nil {
			w.s.log.Warn("cannot claim webhook deliveries", "error", err)
		}
		return 0
	}
	for _, j := range jobs {
		w.wg.Add(1)
		go w.lane(ctx, j)
	}
	return len(jobs)
}

// takeToken takes a token without waiting, and reports whether there was one.
func (w *webhookWorker) takeToken() bool {
	select {
	case <-w.tokens:
		return true
	default:
		return false
	}
}

// cleaner runs clean on its own ticker until ctx ends. It is not behind the token wait of the rounds, so a busy worker
// still fails the stale queues (review finding 1).
func (w *webhookWorker) cleaner(ctx context.Context) {
	defer w.wg.Done()
	t := time.NewTicker(webhookTick)
	defer t.Stop()
	for {
		w.clean(ctx, w.clock())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// clean fails a batch of the stale deliveries: those queued before an endpoint's last disable. A disable leaves its
// queue pending, so the cost of failing it is spread over ticks, not paid in the disabling write.
func (w *webhookWorker) clean(ctx context.Context, now time.Time) {
	if _, err := w.s.store.FailStaleDeliveries(ctx, webhookCleanBatch, now); err != nil && ctx.Err() == nil {
		w.s.log.Warn("cannot fail the stale deliveries of a webhook", "error", err)
	}
}

// lane sends the claimed delivery and then the endpoint's next deliveries, one at a time, until none is due or its
// budget (webhookLaneBudget) has passed. Then it returns its token, so the claims of the rounds decide who is served
// next (review finding 1). It returns its token when it ends.
func (w *webhookWorker) lane(ctx context.Context, j *store.WebhookJob) {
	defer w.wg.Done()
	defer func() { w.tokens <- struct{}{} }()
	started := time.Now()
	for {
		if !w.attempt(ctx, j) || ctx.Err() != nil || time.Since(started) >= webhookLaneBudget {
			return
		}
		next, err := w.s.store.ClaimWebhookJobs(ctx, w.clock(), webhookLease, 1, j.EndpointID)
		if err != nil || len(next) == 0 {
			return
		}
		j = next[0]
	}
}

// attempt sends one claimed delivery and records the outcome. It returns false when the lane must stop: the endpoint
// was disabled, or the server is shutting down.
func (w *webhookWorker) attempt(ctx context.Context, j *store.WebhookJob) bool {
	secrets, err := w.secrets(j, w.clock())
	if err != nil {
		// The secret cannot be read (the key file changed). Nothing is sent to this endpoint until its owner acts.
		w.s.log.Warn("webhook secret cannot be read; the endpoint is disabled", "endpoint", j.EndpointID,
			"origin", webhook.Origin(j.URL))
		w.disable(ctx, j, store.WebhookDisabledSecret)
		return false
	}
	select {
	case w.slots <- struct{}{}:
	case <-ctx.Done():
		return false
	}
	// The signature is made at the time of this attempt, so a retry is signed afresh.
	sentAt := w.clock()
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("Selfcloud-Event-ID", j.EventID)
	header.Set("Selfcloud-Event-Type", j.EventType)
	header.Set("Selfcloud-Signature", webhook.Signature(secrets, sentAt, j.Payload))
	header.Set("User-Agent", "Selfcloud/"+version.Version)
	res := w.s.webhookClient.Post(ctx, j.URL, header, j.Payload)
	<-w.slots
	if res.Err != "" && ctx.Err() != nil {
		// The server is stopping, so the attempt was cut short, not refused. The lease ends, and the delivery is sent again
		// after the restart with the same event ID.
		return false
	}
	return w.record(ctx, j, res, w.clock())
}

// record writes the outcome of one attempt and decides the next step. The write runs even while the server stops, so
// a finished attempt is never lost.
func (w *webhookWorker) record(ctx context.Context, j *store.WebhookJob, res webhook.Result, at time.Time) bool {
	ctx = context.WithoutCancel(ctx)
	o := store.WebhookOutcome{DeliveryID: j.DeliveryID, AccountID: j.AccountID, EndpointID: j.EndpointID,
		URL: j.URL, At: at, Code: res.Code, Err: res.Err, Lease: j.Lease}
	if res.Err != "" {
		first := j.FirstAttemptAt
		o.GiveUp = !at.Before(first.Add(webhook.GiveUpAfter))
		next := at.Add(webhook.Backoff(j.Attempts+1, w.jitter()))
		if limit := first.Add(webhook.GiveUpAfter); next.After(limit) {
			next = limit
		}
		o.NextAt = next
		// The window of "no success for 24 hours" starts at the last success, or at this delivery's first attempt when
		// that is later. Deliveries of an endpoint are sent in order, so nothing else was sent in that window.
		since := first
		if j.LastSuccessAt != nil && j.LastSuccessAt.After(since) {
			since = *j.LastSuccessAt
		}
		switch {
		case res.Code == http.StatusGone:
			o.GiveUp = true
			o.Disable = store.WebhookDisabledFailing
		case at.Sub(since) >= webhook.GiveUpAfter:
			o.Disable = store.WebhookDisabledFailing
		}
		w.s.log.Warn("webhook delivery failed", "origin", webhook.Origin(j.URL), "endpoint", j.EndpointID,
			"event", j.EventID, "code", res.Code, "error", res.Err, "attempts", j.Attempts+1)
	}
	if err := w.s.store.RecordWebhookOutcome(ctx, o); err != nil {
		w.s.log.Error("cannot record a webhook delivery", "endpoint", j.EndpointID, "error", err)
		return false
	}
	return o.Disable == ""
}

// disable disables the endpoint of j with reason.
func (w *webhookWorker) disable(ctx context.Context, j *store.WebhookJob, reason string) {
	if err := w.s.store.DisableWebhookEndpoint(context.WithoutCancel(ctx), j.AccountID, j.EndpointID, reason,
		w.clock(), j.URL); err != nil {
		w.s.log.Error("cannot disable a webhook", "endpoint", j.EndpointID, "error", err)
	}
}

// secrets returns the plain secrets j signs with at now: the current one, then the one a rotation replaced, while its
// grace period lasts. A secret that will not open is an error.
func (w *webhookWorker) secrets(j *store.WebhookJob, now time.Time) ([][]byte, error) {
	cur, err := w.s.webhookSeal.Open(j.SecretEnc, webhookBinding(j.AccountID, j.EndpointID, webhook.FieldSecret))
	if err != nil {
		return nil, err
	}
	out := [][]byte{cur}
	if j.PreviousSecretEnc != nil && j.PreviousSecretUntil != nil && now.Before(*j.PreviousSecretUntil) {
		prev, err := w.s.webhookSeal.Open(j.PreviousSecretEnc, webhookBinding(j.AccountID, j.EndpointID, webhook.FieldPrevious))
		if err != nil {
			return nil, err
		}
		out = append(out, prev)
	}
	return out, nil
}
