// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/webhook"
)

// Review finding 1: one account's busy endpoints do not hold back another account's delivery. Account A has 50
// endpoints, each owed six deliveries, so its lanes would hold all the tokens for a long time. Account B's single
// delivery is sent within two lane budgets (plus a second for slots and ticks), not after A's queues end.
func TestWorkerOneAccountDoesNotStarveAnother(t *testing.T) {
	t.Parallel()
	wf := newWorkerFixture(t)
	wf.rc.delay = 300 * time.Millisecond
	for i := 0; i < 50; i++ {
		wf.create(t, map[string]any{"url": wf.url("/busy" + strconv.Itoa(i)), "events": []string{"file.created"}})
	}
	for n := 0; n < 6; n++ {
		wf.emit(t, "busy"+strconv.Itoa(n))
	}
	other, key := newAccount(t, wf.s, adminKey(t, wf.s), "Beta")
	rec := call(t, wf.s, "POST", "/api/v1/accounts/"+other.ID+"/webhooks", key,
		map[string]any{"url": wf.url("/quiet"), "events": []string{"file.created"}})
	wantStatus(t, rec, 201)
	if err := wf.s.store.Write(context.Background(), func(tx *store.Tx) error {
		return tx.EmitEvent(context.Background(), other.ID, "file.created", wf.now(),
			webhook.File{ID: "nod_quiet", FolderID: "fld_x", Kind: "file", Size: 3, Name: "quiet", ParentID: "fld_x"})
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	start := time.Now()
	go func() {
		defer close(done)
		wf.s.runWebhooks(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()
	deadline := start.Add(60 * time.Second)
	var at time.Time
	for {
		wf.rc.mu.Lock()
		var ok bool
		at, ok = wf.rc.first["/quiet"]
		wf.rc.mu.Unlock()
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the other account's delivery was never sent")
		}
		time.Sleep(20 * time.Millisecond)
	}
	waited, limit := at.Sub(start), 2*webhookLaneBudget+time.Second
	t.Logf("the other account's delivery started %v after the worker started (bound %v)", waited.Round(time.Millisecond), limit)
	if waited > limit {
		t.Fatalf("the other account's delivery waited %v to start, want at most %v (one lane budget plus slack)", waited, limit)
	}
}

// Review finding 8: a scrape reads the webhook queue at most once every 30 seconds. The second scrape inside the window
// reports the first one's numbers, and a scrape after the window reports the change.
func TestWebhookMetricsReadTheQueueEvery30Seconds(t *testing.T) {
	t.Parallel()
	fx := newWebhookFixture(t)
	fx.create(t, map[string]any{"url": "https://hooks.example.com/in", "events": []string{"file.created"}})
	clock := time.Now()
	c := newWebhookCollector(fx.s.store, fx.s.log)
	c.now = func() time.Time { return clock }
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(c)
	depth := func() float64 {
		t.Helper()
		mfs, err := reg.Gather()
		if err != nil {
			t.Fatal(err)
		}
		for _, mf := range mfs {
			if mf.GetName() == "selfcloud_webhook_queue_depth" {
				return mf.GetMetric()[0].GetGauge().GetValue()
			}
		}
		t.Fatal("no queue depth metric")
		return -1
	}
	if got := depth(); got != 0 {
		t.Fatalf("empty queue reported %v", got)
	}
	if err := fx.s.store.Write(context.Background(), func(tx *store.Tx) error {
		return tx.EmitEvent(context.Background(), fx.acc.ID, "file.created", clock,
			webhook.File{ID: "nod_m", FolderID: "fld_x", Kind: "file", Size: 3, Name: "m", ParentID: "fld_x"})
	}); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(5 * time.Second)
	if got := depth(); got != 0 {
		t.Fatalf("a scrape 5 s later read the queue again: depth %v, want the cached 0", got)
	}
	clock = clock.Add(26 * time.Second)
	if got := depth(); got != 1 {
		t.Fatalf("a scrape after 31 s did not read the queue: depth %v, want 1", got)
	}
}
