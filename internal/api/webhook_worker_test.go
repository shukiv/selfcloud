// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/webhook"
)

// receiver is a loopback server that records each delivery, and answers with the status the test sets for it.
type receiver struct {
	srv       *httptest.Server
	mu        sync.Mutex
	got       []delivery
	counts    map[string]int
	status    func(path string, n int) int // n counts the requests to that path, from 1
	delay     time.Duration
	inFlight  int
	maxFlight int
	// first is when each path was first requested.
	first map[string]time.Time
}

// delivery is one request as the receiver saw it.
type delivery struct {
	Path   string
	Header http.Header
	Body   []byte
}

func newReceiver(t *testing.T) *receiver {
	t.Helper()
	rc := &receiver{counts: map[string]int{}, first: map[string]time.Time{}, status: func(string, int) int { return http.StatusOK }}
	rc.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rc.mu.Lock()
		rc.inFlight++
		if rc.inFlight > rc.maxFlight {
			rc.maxFlight = rc.inFlight
		}
		rc.counts[r.URL.Path]++
		if rc.counts[r.URL.Path] == 1 {
			rc.first[r.URL.Path] = time.Now()
		}
		st := rc.status(r.URL.Path, rc.counts[r.URL.Path])
		delay := rc.delay
		rc.got = append(rc.got, delivery{Path: r.URL.Path, Header: r.Header.Clone(), Body: body})
		rc.mu.Unlock()
		if delay > 0 {
			time.Sleep(delay)
		}
		rc.mu.Lock()
		rc.inFlight--
		rc.mu.Unlock()
		// A redirect answer names a private address, to show it is never followed.
		w.Header().Set("Location", "http://10.0.0.1/elsewhere")
		w.WriteHeader(st)
	}))
	t.Cleanup(rc.srv.Close)
	return rc
}

func (rc *receiver) port() string {
	_, p, _ := net.SplitHostPort(rc.srv.Listener.Addr().String())
	return p
}

func (rc *receiver) received() []delivery {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return append([]delivery(nil), rc.got...)
}

// workerFixture is a server whose webhooks reach a loopback receiver, by the name receiver.test, and a worker driven
// on a fake clock: its time is the real time plus the offset, which a test moves forward.
type workerFixture struct {
	*webhookFixture
	rc     *receiver
	w      *webhookWorker
	mu     sync.Mutex
	offset time.Duration
}

func newWorkerFixture(t *testing.T) *workerFixture {
	t.Helper()
	fx := newWebhookFixture(t, func(cfg *config.Config) {
		cfg.Webhooks.AllowHTTP = true
		cfg.Webhooks.AllowNetworks = []string{"127.0.0.1/32"}
	})
	wf := &workerFixture{webhookFixture: fx, rc: newReceiver(t)}
	fx.s.webhookClient = testWebhookClient(fx.s.cfg, map[string][]netip.Addr{"receiver.test": {netip.MustParseAddr("127.0.0.1")}})
	fx.s.webhookClock = wf.now
	fx.s.webhookJitter = func() float64 { return 0 }
	wf.w = fx.s.newWebhookWorker()
	return wf
}

// now is the fake clock.
func (wf *workerFixture) now() time.Time {
	wf.mu.Lock()
	defer wf.mu.Unlock()
	return time.Now().Add(wf.offset)
}

// advance moves the fake clock forward by d.
func (wf *workerFixture) advance(d time.Duration) {
	wf.mu.Lock()
	defer wf.mu.Unlock()
	wf.offset += d
}

// advanceTo moves the fake clock to at, when it is not there yet.
func (wf *workerFixture) advanceTo(at time.Time) {
	if d := at.Sub(wf.now()); d > 0 {
		wf.advance(d + time.Millisecond)
	}
}

// url is an endpoint URL on the receiver, at path.
func (wf *workerFixture) url(path string) string {
	return "http://receiver.test:" + wf.rc.port() + path
}

// drain runs the worker until no delivery is due, waiting for each round's lanes.
func (wf *workerFixture) drain(t *testing.T) {
	t.Helper()
	for i := 0; i < 1000; i++ {
		// The cleaner goroutine runs clean on its own ticker in the server; the fixture has no such goroutine.
		wf.w.clean(context.Background(), wf.now())
		n := wf.w.round(context.Background())
		wf.w.wg.Wait()
		if n == 0 {
			return
		}
	}
	t.Fatal("the worker did not settle")
}

// emit reports one file.created of the account, in its own transaction.
func (wf *workerFixture) emit(t *testing.T, name string) {
	t.Helper()
	err := wf.s.store.Write(context.Background(), func(tx *store.Tx) error {
		return tx.EmitEvent(context.Background(), wf.acc.ID, "file.created", wf.now(),
			webhook.File{ID: "nod_" + name, FolderID: "fld_x", Kind: "file", Size: 3, Name: name, ParentID: "fld_x"})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// deliveriesOf lists an endpoint's deliveries in event order, from the store.
func (wf *workerFixture) deliveriesOf(t *testing.T, hook string) []*store.WebhookDelivery {
	t.Helper()
	list, _, err := wf.s.store.WebhookDeliveries(context.Background(), wf.acc.ID, hook, store.Page{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	// The log is newest first; the worker's order is the reverse.
	out := make([]*store.WebhookDelivery, 0, len(list))
	for i := len(list) - 1; i >= 0; i-- {
		out = append(out, list[i])
	}
	return out
}

// verifyHeader checks a Selfcloud-Signature header for body against secrets, with the rule a receiver uses, and returns
// how many v1 values verified.
func verifyHeader(t *testing.T, header string, body []byte, secrets ...[]byte) int {
	t.Helper()
	var ts string
	var v1s []string
	for _, part := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(part, "=")
		switch k {
		case "t":
			ts = v
		case "v1":
			v1s = append(v1s, v)
		}
	}
	if ts == "" || len(v1s) == 0 {
		t.Fatalf("header %q has no t or no v1", header)
	}
	verified := 0
	for _, secret := range secrets {
		mac := hmac.New(sha256.New, secret)
		mac.Write([]byte(ts + "." + string(body)))
		want := hex.EncodeToString(mac.Sum(nil))
		for _, v := range v1s {
			if hmac.Equal([]byte(v), []byte(want)) {
				verified++
			}
		}
	}
	return verified
}

// The request carries the documented headers, the body is the stored bytes, and the signature verifies with the
// endpoint's secret over "<t>.<body>".
func TestWorkerSendsTheDocumentedRequest(t *testing.T) {
	t.Parallel()
	wf := newWorkerFixture(t)
	_, secret := wf.create(t, map[string]any{"url": wf.url("/in"), "events": []string{"file.created"}})
	wf.emit(t, "a")
	wf.drain(t)
	got := wf.rc.received()
	if len(got) != 1 {
		t.Fatalf("received %d deliveries, want 1", len(got))
	}
	d := got[0]
	if d.Header.Get("Content-Type") != "application/json" || d.Header.Get("Selfcloud-Event-Type") != "file.created" ||
		!strings.HasPrefix(d.Header.Get("Selfcloud-Event-ID"), "evt_") || !strings.HasPrefix(d.Header.Get("User-Agent"), "Selfcloud/") {
		t.Fatalf("headers = %v", d.Header)
	}
	var env struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(d.Body, &env); err != nil || env.ID != d.Header.Get("Selfcloud-Event-ID") || env.Type != "file.created" {
		t.Fatalf("body %s does not match the headers: %v", d.Body, err)
	}
	if verifyHeader(t, d.Header.Get("Selfcloud-Signature"), d.Body, secretBytesOf(t, secret)) != 1 {
		t.Fatalf("the signature does not verify with the endpoint's secret: %s", d.Header.Get("Selfcloud-Signature"))
	}
	stored := outboxOf(t, wf.s, wf.acc.ID)
	if len(stored) != 1 || stored[0].Payload != string(d.Body) {
		t.Fatalf("the body is not the stored bytes: %s vs %v", d.Body, stored)
	}
}

// During a rotation the old secret still signs, so the request carries two v1 values, and each verifies with its own
// secret. After the grace period only the new secret signs.
func TestWorkerSignsWithBothSecretsDuringRotation(t *testing.T) {
	t.Parallel()
	wf := newWorkerFixture(t)
	hook, oldShown := wf.create(t, map[string]any{"url": wf.url("/in"), "events": []string{"file.created"}})
	rec := call(t, wf.s, "POST", wf.base()+"/"+hook+"/secret", wf.key, nil)
	wantStatus(t, rec, 200)
	newShown := decode[webhookShown](t, rec).Secret
	wf.emit(t, "a")
	wf.drain(t)
	d := wf.rc.received()[0]
	if n := strings.Count(d.Header.Get("Selfcloud-Signature"), "v1="); n != 2 {
		t.Fatalf("during rotation the header has %d v1 values, want 2", n)
	}
	if verifyHeader(t, d.Header.Get("Selfcloud-Signature"), d.Body, secretBytesOf(t, newShown), secretBytesOf(t, oldShown)) != 2 {
		t.Fatal("the two v1 values do not verify with the two secrets")
	}
	wf.advance(25 * time.Hour)
	wf.emit(t, "b")
	wf.drain(t)
	later := wf.rc.received()[1]
	if n := strings.Count(later.Header.Get("Selfcloud-Signature"), "v1="); n != 1 {
		t.Fatalf("after the grace period the header has %d v1 values, want 1", n)
	}
	if verifyHeader(t, later.Header.Get("Selfcloud-Signature"), later.Body, secretBytesOf(t, oldShown)) != 0 {
		t.Fatal("the old secret still signs after its grace period")
	}
}

// A retry is signed afresh: its t is the attempt's time.
func TestWorkerRetrySignsAfresh(t *testing.T) {
	t.Parallel()
	wf := newWorkerFixture(t)
	wf.create(t, map[string]any{"url": wf.url("/flaky"), "events": []string{"file.created"}})
	wf.rc.status = func(path string, n int) int {
		if n == 1 {
			return http.StatusInternalServerError
		}
		return http.StatusOK
	}
	wf.emit(t, "a")
	wf.drain(t)
	e := wf.endpointByPath(t, "/flaky")
	wf.advanceTo(wf.deliveriesOf(t, e)[0].NextAttemptAt)
	wf.drain(t)
	got := wf.rc.received()
	if len(got) != 2 {
		t.Fatalf("received %d, want 2", len(got))
	}
	if got[0].Header.Get("Selfcloud-Signature") == got[1].Header.Get("Selfcloud-Signature") {
		t.Fatal("the retry carries the first attempt's signature")
	}
	if got[0].Header.Get("Selfcloud-Event-ID") != got[1].Header.Get("Selfcloud-Event-ID") {
		t.Fatal("the retry changed the event ID")
	}
}

// endpointByPath returns the ID of the one endpoint of the account whose URL ends in path.
func (wf *workerFixture) endpointByPath(t *testing.T, path string) string {
	t.Helper()
	list, _, err := wf.s.store.WebhookEndpoints(context.Background(), wf.acc.ID, store.Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range list {
		if strings.HasSuffix(e.URL, path) {
			return e.ID
		}
	}
	t.Fatalf("no endpoint on %s", path)
	return ""
}

// Deliveries of one endpoint are sent one at a time, in event order. While the first waits on its backoff, nothing after
// it is sent, and the order holds when it is retried.
func TestWorkerKeepsEachEndpointInOrder(t *testing.T) {
	t.Parallel()
	wf := newWorkerFixture(t)
	hook, _ := wf.create(t, map[string]any{"url": wf.url("/ordered"), "events": []string{"file.created"}})
	wf.rc.status = func(path string, n int) int {
		if n == 1 {
			return http.StatusServiceUnavailable
		}
		return http.StatusOK
	}
	for _, name := range []string{"e1", "e2", "e3"} {
		wf.emit(t, name)
	}
	wf.drain(t)
	if n := len(wf.rc.received()); n != 1 {
		t.Fatalf("while the first waits on its backoff, %d requests were sent, want 1", n)
	}
	wf.advanceTo(wf.deliveriesOf(t, hook)[0].NextAttemptAt)
	wf.drain(t)
	var names []string
	for _, d := range wf.rc.received() {
		var env struct {
			Data struct {
				Name string `json:"name"`
			} `json:"data"`
		}
		_ = json.Unmarshal(d.Body, &env)
		names = append(names, env.Data.Name)
	}
	if strings.Join(names, ",") != "e1,e1,e2,e3" {
		t.Fatalf("send order = %v, want e1 (failed), e1, e2, e3", names)
	}
}

// Up to eight endpoints are sent to at once: twelve endpoints, each slow to answer, are never more than eight in flight.
func TestWorkerSendsToEightEndpointsAtOnce(t *testing.T) {
	t.Parallel()
	wf := newWorkerFixture(t)
	wf.rc.delay = 200 * time.Millisecond
	for i := 0; i < 12; i++ {
		wf.create(t, map[string]any{"url": wf.url("/slow" + strconv.Itoa(i)), "events": []string{"file.created"}})
	}
	wf.emit(t, "a")
	wf.drain(t)
	wf.rc.mu.Lock()
	max := wf.rc.maxFlight
	wf.rc.mu.Unlock()
	if len(wf.rc.received()) != 12 {
		t.Fatalf("received %d, want 12", len(wf.rc.received()))
	}
	if max != webhookSendSlots {
		t.Fatalf("the most in flight was %d, want %d", max, webhookSendSlots)
	}
}

// Give-up and disable: an endpoint that never answers is given up 24 hours after the first attempt, and disabled with
// reason failing. Its waiting deliveries fail through the batched cleanup, and the audit records the disable.
func TestWorkerGivesUpAfter24HoursAndDisables(t *testing.T) {
	t.Parallel()
	wf := newWorkerFixture(t)
	wf.rc.status = func(string, int) int { return http.StatusInternalServerError }
	hook, _ := wf.create(t, map[string]any{"url": wf.url("/dead"), "events": []string{"file.created"}})
	wf.emit(t, "a")
	wf.emit(t, "b")
	wf.drain(t)
	for i := 0; i < 500; i++ {
		ds := wf.deliveriesOf(t, hook)
		if ds[0].State != "pending" {
			break
		}
		wf.advanceTo(ds[0].NextAttemptAt)
		wf.drain(t)
	}
	ds := wf.deliveriesOf(t, hook)
	if ds[0].State != "failed" {
		t.Fatalf("the head is %s after the schedule ran out", ds[0].State)
	}
	// The first attempt is a few milliseconds after the event, and the give-up is exactly 24 hours after that.
	if span := ds[0].FinishedAt.Sub(ds[0].CreatedAt); span < webhook.GiveUpAfter || span > webhook.GiveUpAfter+time.Minute {
		t.Fatalf("given up %v after the event, want about 24 h", span)
	}
	e := wf.endpoint(t, hook)
	if e.State != store.WebhookDisabled || e.DisabledReason != store.WebhookDisabledFailing {
		t.Fatalf("endpoint = %s/%s, want disabled/failing", e.State, e.DisabledReason)
	}
	// The cleanup fails the queue that waited behind the endpoint's head.
	wf.advance(2 * time.Second)
	wf.drain(t)
	ds = wf.deliveriesOf(t, hook)
	if ds[1].State != "failed" || ds[1].LastError != store.WebhookDisabledFailing {
		t.Fatalf("the waiting delivery = %s/%q, want failed/failing", ds[1].State, ds[1].LastError)
	}
	if n := countOf(t, wf.s, "SELECT COUNT(*) FROM audit_log WHERE action = 'webhook.disabled' AND target = ?", hook); n != 1 {
		t.Fatalf("webhook.disabled audit entries = %d, want 1", n)
	}
}

// A 410 disables the endpoint at once, and its delivery fails.
func TestWorkerGoneDisablesAtOnce(t *testing.T) {
	t.Parallel()
	wf := newWorkerFixture(t)
	wf.rc.status = func(string, int) int { return http.StatusGone }
	hook, _ := wf.create(t, map[string]any{"url": wf.url("/gone"), "events": []string{"file.created"}})
	wf.emit(t, "a")
	wf.drain(t)
	e := wf.endpoint(t, hook)
	if e.State != store.WebhookDisabled || e.DisabledReason != store.WebhookDisabledFailing {
		t.Fatalf("after a 410 the endpoint is %s/%s", e.State, e.DisabledReason)
	}
	ds := wf.deliveriesOf(t, hook)
	if ds[0].State != "failed" || ds[0].LastCode == nil || *ds[0].LastCode != http.StatusGone {
		t.Fatalf("the delivery after a 410 = %+v", ds[0])
	}
}

// A secret that cannot be read disables its endpoint with secret_unreadable. The other endpoint is sent to as usual.
func TestWorkerUnreadableSecretDisablesOnlyItsEndpoint(t *testing.T) {
	t.Parallel()
	wf := newWorkerFixture(t)
	bad, _ := wf.create(t, map[string]any{"url": wf.url("/bad"), "events": []string{"file.created"}})
	good, _ := wf.create(t, map[string]any{"url": wf.url("/good"), "events": []string{"file.created"}})
	execOn(t, wf.s, "UPDATE webhook_endpoints SET secret_enc = x'00112233445566778899aabbccddeeff00112233445566778899aabb' WHERE id = '"+bad+"'")
	wf.emit(t, "a")
	wf.drain(t)
	if e := wf.endpoint(t, bad); e.State != store.WebhookDisabled || e.DisabledReason != store.WebhookDisabledSecret {
		t.Fatalf("the endpoint with the unreadable secret is %s/%s", e.State, e.DisabledReason)
	}
	if ds := wf.deliveriesOf(t, good); ds[0].State != "delivered" {
		t.Fatalf("the other endpoint's delivery is %s", ds[0].State)
	}
	if n := len(wf.rc.received()); n != 1 {
		t.Fatalf("requests sent = %d; only the good endpoint should be sent to", n)
	}
}

// A redirect answer is a failure, and the target is not followed: the receiver sees one request.
func TestWorkerDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()
	wf := newWorkerFixture(t)
	wf.rc.status = func(string, int) int { return http.StatusFound }
	hook, _ := wf.create(t, map[string]any{"url": wf.url("/moved"), "events": []string{"file.created"}})
	wf.emit(t, "a")
	wf.drain(t)
	if n := len(wf.rc.received()); n != 1 {
		t.Fatalf("requests = %d; the redirect was followed", n)
	}
	if ds := wf.deliveriesOf(t, hook); ds[0].LastCode == nil || *ds[0].LastCode != http.StatusFound || ds[0].LastError != "redirect" {
		t.Fatalf("the redirect delivery = %+v", ds[0])
	}
}

// A DNS change after registration: the name resolved to loopback when the endpoint was made and to a private address
// now. The delivery is refused, and retried, since the change may be undone.
func TestWorkerRefusesAChangedAddressAtDelivery(t *testing.T) {
	t.Parallel()
	wf := newWorkerFixture(t)
	hook, _ := wf.create(t, map[string]any{"url": wf.url("/rebind"), "events": []string{"file.created"}})
	hosts := map[string][]netip.Addr{"receiver.test": {netip.MustParseAddr("10.0.0.5")}}
	wf.s.webhookClient = testWebhookClient(wf.s.cfg, hosts)
	wf.emit(t, "a")
	wf.drain(t)
	if n := len(wf.rc.received()); n != 0 {
		t.Fatalf("the receiver was reached %d times after the address changed", n)
	}
	// A refusal is an attempt that failed: the delivery waits for its retry, with the fixed token.
	ds := wf.deliveriesOf(t, hook)
	if ds[0].State != "pending" || ds[0].Attempts != 1 || ds[0].LastError != "address_refused" {
		t.Fatalf("the refused delivery is %s after %d attempts, last_error %q", ds[0].State, ds[0].Attempts, ds[0].LastError)
	}
}

// An error text can hold the URL and its token. The delivery's last_error and the log hold neither.
func TestWorkerNeverStoresOrLogsTheURL(t *testing.T) {
	t.Parallel()
	wf := newWorkerFixture(t)
	// Port 1 is refused. A port picked by listening and closing can be reused by another test's server, and then the
	// attempt succeeds, which is what made this test flaky under -race.
	const closed = 1
	raw := "http://127.0.0.1:" + strconv.Itoa(closed) + "/services/PATHTOKEN9/B?token=QUERYTOKEN9"
	hook, _ := wf.create(t, map[string]any{"url": raw, "events": []string{"file.created"}})
	wf.emit(t, "a")
	wf.drain(t)
	ds := wf.deliveriesOf(t, hook)
	if ds[0].LastError != "connect" {
		t.Fatalf("last_error = %q, want connect", ds[0].LastError)
	}
	for _, leak := range []string{"PATHTOKEN9", "QUERYTOKEN9", "/services"} {
		if strings.Contains(wf.buf.String(), leak) {
			t.Errorf("the log holds %q: %s", leak, wf.buf.String())
		}
		if strings.Contains(ds[0].LastError, leak) {
			t.Errorf("last_error holds %q", leak)
		}
	}
	if !strings.Contains(wf.buf.String(), "origin=http://127.0.0.1:"+strconv.Itoa(closed)) {
		t.Errorf("the log does not name the origin: %s", wf.buf.String())
	}
}

// A claimed delivery whose worker dies is claimed again once its lease ends, with the same event ID.
func TestWorkerLeaseRecoversACrash(t *testing.T) {
	t.Parallel()
	wf := newWorkerFixture(t)
	wf.create(t, map[string]any{"url": wf.url("/crash"), "events": []string{"file.created"}})
	wf.emit(t, "a")
	first, err := wf.s.store.ClaimWebhookJobs(context.Background(), wf.now(), webhookLease, 50, "")
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim = %d, %v", len(first), err)
	}
	if again, _ := wf.s.store.ClaimWebhookJobs(context.Background(), wf.now(), webhookLease, 50, ""); len(again) != 0 {
		t.Fatal("a leased delivery was claimed by another worker")
	}
	wf.advance(webhookLease + time.Second)
	wf.drain(t)
	got := wf.rc.received()
	if len(got) != 1 || got[0].Header.Get("Selfcloud-Event-ID") != first[0].EventID {
		t.Fatalf("after the lease the delivery was sent %d times with the same ID", len(got))
	}
}

// The schedule runs on the fake clock: the give-up is 24 hours after the first attempt, not after the event.
func TestWorkerRetryScheduleOnTheFakeClock(t *testing.T) {
	t.Parallel()
	wf := newWorkerFixture(t)
	wf.rc.status = func(string, int) int { return http.StatusInternalServerError }
	hook, _ := wf.create(t, map[string]any{"url": wf.url("/clock"), "events": []string{"file.created"}})
	wf.emit(t, "a")
	wf.drain(t)
	first := wf.deliveriesOf(t, hook)[0].NextAttemptAt
	wf.advanceTo(first)
	wf.drain(t)
	second := wf.deliveriesOf(t, hook)[0].NextAttemptAt
	// The first retry is due at attempt one plus 30 s. The second attempt is made at that time plus a millisecond, and its
	// wait is 60 s, so the next retry is due 60 s after the first retry time.
	if gap := second.Sub(first); gap < time.Minute || gap > time.Minute+time.Second {
		t.Fatalf("the gap between the two retry times is %v, want about 1 min", gap)
	}
}

// The delivery log: newest first, a page at a time, for the owner and the account key alike.
func TestDeliveryLogPagesForOwnerAndKey(t *testing.T) {
	t.Parallel()
	wf := newWorkerFixture(t)
	hook, _ := wf.create(t, map[string]any{"url": wf.url("/log"), "events": []string{"file.created"}})
	for _, n := range []string{"a", "b", "c"} {
		wf.emit(t, n)
	}
	wf.drain(t)
	page := decode[struct {
		Items      []deliveryJSON `json:"items"`
		NextCursor string         `json:"next_cursor"`
	}](t, call(t, wf.s, "GET", wf.base()+"/"+hook+"/deliveries?limit=2", wf.key, nil))
	if len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatalf("page one = %d items, cursor %q", len(page.Items), page.NextCursor)
	}
	rest := decode[struct {
		Items      []deliveryJSON `json:"items"`
		NextCursor string         `json:"next_cursor"`
	}](t, call(t, wf.s, "GET", wf.base()+"/"+hook+"/deliveries?limit=2&cursor="+page.NextCursor, wf.key, nil))
	if len(rest.Items) != 1 || rest.NextCursor != "" {
		t.Fatalf("page two = %d items, cursor %q", len(rest.Items), rest.NextCursor)
	}
	wantStatus(t, callSession(t, wf.s, wf.owner, "GET", wf.base()+"/"+hook+"/deliveries", nil), 200)
	if strings.Contains(callSession(t, wf.s, wf.owner, "GET", wf.base()+"/"+hook+"/deliveries", nil).Body.String(), "receiver.test") {
		t.Fatal("an owner's log names the full URL")
	}
}

// The detail shows the newest five deliveries.
func TestDetailShowsFiveRecentDeliveries(t *testing.T) {
	t.Parallel()
	wf := newWorkerFixture(t)
	hook, _ := wf.create(t, map[string]any{"url": wf.url("/recent"), "events": []string{"file.created"}})
	for i := 0; i < 7; i++ {
		wf.emit(t, "r"+strconv.Itoa(i))
	}
	wf.drain(t)
	detail := decode[struct {
		RecentDeliveries []deliveryJSON `json:"recent_deliveries"`
	}](t, call(t, wf.s, "GET", wf.base()+"/"+hook, wf.key, nil))
	if len(detail.RecentDeliveries) != 5 {
		t.Fatalf("recent deliveries = %d, want 5", len(detail.RecentDeliveries))
	}
}

// A retry queues a failed delivery again; the route refuses one that is not failed, and a disabled endpoint.
func TestRetryRouteRequeuesAFailedDelivery(t *testing.T) {
	t.Parallel()
	wf := newWorkerFixture(t)
	wf.rc.status = func(string, int) int { return http.StatusInternalServerError }
	hook, _ := wf.create(t, map[string]any{"url": wf.url("/retry"), "events": []string{"file.created"}})
	wf.emit(t, "a")
	wf.drain(t)
	// Still pending: a retry is refused.
	d := wf.deliveriesOf(t, hook)[0]
	wantError(t, call(t, wf.s, "POST", wf.base()+"/"+hook+"/deliveries/"+d.ID+"/retry", wf.key, nil), 409, apierr.Conflict)
	// Give it up, then retry it. The endpoint is disabled by the give-up, so the first retry is refused until it is paused.
	for i := 0; i < 500 && wf.deliveriesOf(t, hook)[0].State == "pending"; i++ {
		wf.advanceTo(wf.deliveriesOf(t, hook)[0].NextAttemptAt)
		wf.drain(t)
	}
	wantError(t, call(t, wf.s, "POST", wf.base()+"/"+hook+"/deliveries/"+d.ID+"/retry", wf.key, nil), 409, apierr.Conflict)
	wf.rc.status = func(string, int) int { return http.StatusOK }
	wantStatus(t, call(t, wf.s, "PATCH", wf.base()+"/"+hook, wf.key, map[string]any{"url": wf.url("/retry2")}), 200)
	wantStatus(t, call(t, wf.s, "PATCH", wf.base()+"/"+hook, wf.key, map[string]any{"state": "active"}), 200)
	wantStatus(t, call(t, wf.s, "POST", wf.base()+"/"+hook+"/deliveries/"+d.ID+"/retry", wf.key, nil), 202)
	wf.drain(t)
	if got := wf.deliveriesOf(t, hook)[0]; got.State != "delivered" {
		t.Fatalf("after the retry the delivery is %s", got.State)
	}
}

// The test route queues a webhook.test event for an active endpoint, whatever it subscribes to. A paused endpoint is refused.
func TestTestRouteQueuesOneEvent(t *testing.T) {
	t.Parallel()
	wf := newWorkerFixture(t)
	hook, _ := wf.create(t, map[string]any{"url": wf.url("/test"), "events": []string{"user.created"}})
	wantStatus(t, call(t, wf.s, "POST", wf.base()+"/"+hook+"/test", wf.key, nil), 202)
	wf.drain(t)
	got := wf.rc.received()
	if len(got) != 1 || got[0].Header.Get("Selfcloud-Event-Type") != "webhook.test" {
		t.Fatalf("the test event was not sent: %d requests", len(got))
	}
	wantStatus(t, call(t, wf.s, "PATCH", wf.base()+"/"+hook, wf.key, map[string]any{"state": "paused"}), 200)
	wantError(t, call(t, wf.s, "POST", wf.base()+"/"+hook+"/test", wf.key, nil), 409, apierr.Conflict)
}

// The new routes are refused to another account, and to its owner, with not_found; an owner reads but cannot retry or
// test; a token is refused.
func TestNewWebhookRoutesIsolateAccounts(t *testing.T) {
	t.Parallel()
	wf := newWorkerFixture(t)
	hook, _ := wf.create(t, map[string]any{"url": wf.url("/iso"), "events": []string{"file.created"}})
	wf.emit(t, "a")
	wf.drain(t)
	d := wf.deliveriesOf(t, hook)[0]
	other, otherKey := newAccount(t, wf.s, adminKey(t, wf.s), "Beta")
	base := wf.base()
	paths := []struct{ method, path string }{
		{"GET", base + "/" + hook + "/deliveries"},
		{"POST", base + "/" + hook + "/deliveries/" + d.ID + "/retry"},
		{"POST", base + "/" + hook + "/test"},
	}
	for _, p := range paths {
		// Another account's key, addressing this account's endpoint through its own account path: not found.
		wantError(t, call(t, wf.s, p.method, strings.Replace(p.path, wf.acc.ID, other.ID, 1), otherKey, nil), 404, apierr.NotFound)
	}
	// The owner reads the log and is refused the actions.
	if rec := callSession(t, wf.s, wf.owner, "GET", base+"/"+hook+"/deliveries", nil); rec.Code != 200 {
		t.Fatalf("owner log = %d", rec.Code)
	}
	wantError(t, callSession(t, wf.s, wf.owner, "POST", base+"/"+hook+"/test", nil), 403, apierr.Forbidden)
	wantError(t, callSession(t, wf.s, wf.owner, "POST", base+"/"+hook+"/deliveries/"+d.ID+"/retry", nil), 403, apierr.Forbidden)

	// A token reaches none of the new routes.
	f := newFolder(t, wf.s, wf.key, wf.acc.ID, "Mine")
	tok := mintFor(t, wf.s, wf.key, wf.acc.ID, map[string]any{"folders": []string{f.ID}})
	for _, p := range paths {
		if rec := call(t, wf.s, p.method, p.path, tok, nil); rec.Code != 403 && rec.Code != 404 {
			t.Errorf("token reached %s %s: %d", p.method, p.path, rec.Code)
		}
	}
}

// The endpoint's detail names a secret's absence and the delivery log is a separate route; the metrics name no URL.
func TestQueueMetricsNameNoEndpoint(t *testing.T) {
	t.Parallel()
	wf := newWorkerFixture(t)
	hook, _ := wf.create(t, map[string]any{"url": wf.url("/metrics"), "events": []string{"file.created"}})
	wf.emit(t, "a")
	rec := httptest.NewRecorder()
	wf.s.metrics.handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{"selfcloud_webhook_queue_depth 1", `selfcloud_webhook_deliveries{state="pending"} 1`} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics lack %q", want)
		}
	}
	for _, leak := range []string{hook, "receiver.test"} {
		if strings.Contains(body, leak) {
			t.Errorf("metrics name %q", leak)
		}
	}
}

// Registration checks the host by the addresses it resolves to. A private, mixed or unresolvable host gets the same
// refusal, and the answer never says which.
func TestRegistrationRefusesWithoutSayingWhy(t *testing.T) {
	t.Parallel()
	fx := newWebhookFixture(t)
	fx.s.webhookClient = testWebhookClient(fx.s.cfg, map[string][]netip.Addr{
		"internal.example.test": {netip.MustParseAddr("10.0.0.5")},
		"nx.example.test":       {},
		"mixed.example.test":    {netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("192.168.1.9")},
	})
	var messages []string
	for _, host := range []string{"internal.example.test", "nx.example.test", "mixed.example.test", "203.0.113.7"} {
		rec := call(t, fx.s, "POST", fx.base(), fx.key, map[string]any{"url": "https://" + host + "/in", "events": []string{"file.created"}})
		e := wantError(t, rec, 400, apierr.InvalidRequest)
		messages = append(messages, e.Message)
	}
	for _, m := range messages[1:] {
		if m != messages[0] {
			t.Fatalf("the refusals differ: %q and %q", messages[0], m)
		}
	}
	// A change of URL is checked too; a change that keeps the URL is not looked up.
	hook, _ := fx.create(t, nil)
	wantError(t, call(t, fx.s, "PATCH", fx.base()+"/"+hook, fx.key, map[string]any{"url": "https://internal.example.test/in"}), 400, apierr.InvalidRequest)
	wantStatus(t, call(t, fx.s, "PATCH", fx.base()+"/"+hook, fx.key, map[string]any{"description": "same url"}), 200)
}

// The integration path: a file uploaded through the API is reported, delivered to the loopback receiver, and verified
// with the documented algorithm.
func TestFileEventReachesLoopbackReceiver(t *testing.T) {
	t.Parallel()
	wf := newWorkerFixture(t)
	_, secret := wf.create(t, map[string]any{"url": wf.url("/integration"), "events": []string{"file.created"}})
	folder := newFolder(t, wf.s, wf.key, wf.acc.ID, "Inbox")
	putNode(t, wf.s, wf.key, folder.ID, "/report.txt", "hello")
	wf.drain(t)
	got := wf.rc.received()
	if len(got) != 1 {
		t.Fatalf("received %d deliveries, want 1", len(got))
	}
	if verifyHeader(t, got[0].Header.Get("Selfcloud-Signature"), got[0].Body, secretBytesOf(t, secret)) != 1 {
		t.Fatal("the signature does not verify")
	}
	if !bytes.Contains(got[0].Body, []byte(`"name":"report.txt"`)) {
		t.Fatalf("the payload does not name the file: %s", got[0].Body)
	}
}
