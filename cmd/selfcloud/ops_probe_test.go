// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/api"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/webhook"
)

// The webhook, import and job commands run against the real API with the probe account's key. An import route is a
// signed-in person's, so the real server refuses the key (403, exit 3) and the probe checks that the command reaches its
// route and reports the refusal.

// publicHook is an address the server's network policy accepts without a lookup: a public IP literal. Nothing connects to it
// in the probes that use the server without its delivery loop.
const publicHook = "https://93.184.216.34/hook"

func init() {
	for _, name := range []string{"import sources", "import show", "import items", "import pause"} {
		probeExit[name] = 3
	}
	for name, fn := range opsProbes {
		fileProbes[name] = fn
	}
}

// newHook makes a webhook through the command and returns its id and secret.
func (p *fileProbe) newHook() (hook, secret string) {
	out := p.do("webhook", "add", "--account", p.acc, "--endpoint", publicHook, "--event", "file.created")
	return valueOf(p.t, out, "id="), valueOf(p.t, out, "secret=")
}

// failedDelivery queues a test event for hook and gives its delivery up in the store, so it is failed, and returns its id.
func (p *fileProbe) failedDelivery(hook string) string {
	p.t.Helper()
	p.do("webhook", "test", "--account", p.acc, hook)
	ctx := context.Background()
	jobs, err := p.st.ClaimWebhookJobs(ctx, time.Now().Add(time.Minute), time.Minute, 5, hook)
	if err != nil || len(jobs) != 1 {
		p.t.Fatalf("claim the test delivery: %v, %d jobs", err, len(jobs))
	}
	j := jobs[0]
	if err := p.st.RecordWebhookOutcome(ctx, store.WebhookOutcome{DeliveryID: j.DeliveryID, AccountID: j.AccountID,
		EndpointID: j.EndpointID, URL: j.URL, At: time.Now(), Err: "connect", GiveUp: true, Lease: j.Lease}); err != nil {
		p.t.Fatal(err)
	}
	return j.DeliveryID
}

// newJob makes a job row of the probe account with the status and returns its id.
func (p *fileProbe) newJob(status string) string {
	p.t.Helper()
	now := store.Now()
	j := &store.Job{ID: id.New("job"), AccountID: p.acc, Kind: "zip", Target: id.New("zip"), Status: status, CreatedAt: now, UpdatedAt: now}
	ctx := context.Background()
	if err := p.st.Write(ctx, func(tx *store.Tx) error { return tx.InsertJob(ctx, j) }); err != nil {
		p.t.Fatal(err)
	}
	return j.ID
}

var opsProbes = map[string]func(p *fileProbe) []string{
	"webhook add": func(p *fileProbe) []string {
		return []string{"--account", p.acc, "--endpoint", publicHook, "--event", "file.created,file.deleted", "--description", "probe"}
	},
	"webhook ls": func(p *fileProbe) []string { return []string{"--account", p.acc} },
	"webhook show": func(p *fileProbe) []string {
		h, _ := p.newHook()
		return []string{"--account", p.acc, h}
	},
	"webhook update": func(p *fileProbe) []string {
		h, _ := p.newHook()
		return []string{"--account", p.acc, h, "--state", "paused"}
	},
	"webhook rm": func(p *fileProbe) []string {
		h, _ := p.newHook()
		p.answer = h + "\n"
		return []string{"--account", p.acc, h}
	},
	"webhook rotate-secret": func(p *fileProbe) []string {
		h, _ := p.newHook()
		p.answer = h + "\n"
		return []string{"--account", p.acc, h}
	},
	"webhook test": func(p *fileProbe) []string {
		h, _ := p.newHook()
		return []string{"--account", p.acc, h}
	},
	"webhook deliveries": func(p *fileProbe) []string {
		h, _ := p.newHook()
		p.do("webhook", "test", "--account", p.acc, h)
		return []string{"--account", p.acc, h}
	},
	"webhook retry": func(p *fileProbe) []string {
		h, _ := p.newHook()
		return []string{"--account", p.acc, h, p.failedDelivery(h)}
	},
	"import sources": func(p *fileProbe) []string { return nil },
	"import show":    func(p *fileProbe) []string { return []string{id.New("imp")} },
	"import items":   func(p *fileProbe) []string { return []string{id.New("imp")} },
	"import pause":   func(p *fileProbe) []string { return []string{id.New("imp")} },
	"import archive set": func(p *fileProbe) []string {
		return []string{"--account", p.acc, "off"}
	},
	"job show": func(p *fileProbe) []string { return []string{p.newJob("succeeded")} },
}

// TestWebhookCommandsRoundTripOnTheRealServer: the commands do what they say on the real API. The secret add prints is the
// one the server signs with: no other command shows it, and rotating it prints a new one.
func TestWebhookCommandsRoundTripOnTheRealServer(t *testing.T) {
	isolateRemote(t)
	p := newFileProbe(t)
	hook, secret := p.newHook()
	if !strings.HasPrefix(secret, "whsec_") {
		t.Fatalf("secret %q", secret)
	}
	for _, args := range [][]string{{"webhook", "ls", "--account", p.acc}, {"webhook", "show", "--account", p.acc, hook},
		{"webhook", "ls", "--account", p.acc, "--json"}, {"webhook", "show", "--account", p.acc, hook, "--json"}} {
		if out := p.do(args...); strings.Contains(out, secret) || !strings.Contains(out, hook) {
			t.Fatalf("%v: %q", args, out)
		}
	}
	p.do("webhook", "update", "--account", p.acc, hook, "--description", "", "--event", "file.moved", "--state", "paused")
	if got := valueOf(t, p.do("webhook", "show", "--account", p.acc, hook), "state="); got != "paused" {
		t.Fatalf("state %q", got)
	}
	if got := valueOf(t, p.do("webhook", "show", "--account", p.acc, hook), "events="); got != "file.moved" {
		t.Fatalf("events %q", got)
	}
	// A paused webhook gets no test event: the server's conflict is exit 3.
	var out, errOut bytes.Buffer
	if code := run([]string{"webhook", "test", "--account", p.acc, hook, "--url", p.url}, &out, &errOut); code != 3 {
		t.Fatalf("test of a paused webhook: exit %d, stderr %q", code, errOut.String())
	}
	p.do("webhook", "update", "--account", p.acc, hook, "--state", "active")
	again := p.do("webhook", "rotate-secret", "--account", p.acc, hook, "--yes")
	if next := valueOf(t, again, "secret="); next == secret || valueOf(t, again, "previous_secret_until=") == "" {
		t.Fatalf("rotate: %q", again)
	}
	delivery := p.failedDelivery(hook)
	if got := valueOf(t, p.do("webhook", "retry", "--account", p.acc, hook, delivery), "state="); got != "pending" {
		t.Fatalf("retry state %q", got)
	}
	// A delivery that is pending again is not failed: retry is a conflict (exit 3).
	out.Reset()
	errOut.Reset()
	if code := run([]string{"webhook", "retry", "--account", p.acc, hook, delivery, "--url", p.url}, &out, &errOut); code != 3 {
		t.Fatalf("second retry: exit %d, stderr %q", code, errOut.String())
	}
	p.do("webhook", "rm", "--account", p.acc, hook, "--yes")
	if code := run([]string{"webhook", "show", "--account", p.acc, hook, "--url", p.url}, &out, &errOut); code != 4 {
		t.Fatalf("a removed webhook: exit %d", code)
	}
}

// TestJobShowWaitOnTheRealServer: --wait returns for a finished job, and a failed one exits 1.
func TestJobShowWaitOnTheRealServer(t *testing.T) {
	isolateRemote(t)
	p := newFileProbe(t)
	if got := valueOf(t, p.do("job", "show", p.newJob("succeeded"), "--wait"), "status="); got != "succeeded" {
		t.Fatalf("status %q", got)
	}
	var out, errOut bytes.Buffer
	if code := run([]string{"job", "show", p.newJob("failed"), "--wait", "--url", p.url}, &out, &errOut); code != 1 {
		t.Fatalf("a failed job: exit %d, stderr %q", code, errOut.String())
	}
	out.Reset()
	if code := run([]string{"job", "show", id.New("job"), "--url", p.url}, &out, &errOut); code != 4 {
		t.Fatalf("an unknown job: exit %d", code)
	}
}

// receiver is a webhook receiver on loopback. It records each delivery and answers 200.
type receiver struct {
	mu   sync.Mutex
	got  []delivered
	srv  *httptest.Server
	wake chan struct{}
}

type delivered struct {
	header http.Header
	body   []byte
}

func newReceiver(t *testing.T) *receiver {
	t.Helper()
	r := &receiver{wake: make(chan struct{}, 16)}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var b bytes.Buffer
		_, _ = b.ReadFrom(req.Body)
		r.mu.Lock()
		r.got = append(r.got, delivered{header: req.Header.Clone(), body: b.Bytes()})
		r.mu.Unlock()
		r.wake <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

// next waits for one more delivery than the receiver has handed out.
func (r *receiver) next(t *testing.T, n int) delivered {
	t.Helper()
	deadline := time.After(20 * time.Second)
	for {
		r.mu.Lock()
		if len(r.got) >= n {
			d := r.got[n-1]
			r.mu.Unlock()
			return d
		}
		r.mu.Unlock()
		select {
		case <-r.wake:
		case <-deadline:
			t.Fatalf("no delivery number %d within 20 s", n)
		}
	}
}

// signedWith reports whether the delivery's signature header holds a valid v1 for secret, as a receiver checks it.
func signedWith(t *testing.T, d delivered, shown string) bool {
	t.Helper()
	raw, ok := webhook.ParseSecret(shown)
	if !ok {
		t.Fatalf("%q is not a secret", shown)
	}
	header := d.header.Get("Selfcloud-Signature")
	stamp, _, _ := strings.Cut(strings.TrimPrefix(header, "t="), ",")
	var unix int64
	for _, c := range stamp {
		unix = unix*10 + int64(c-'0')
	}
	want := webhook.Signature([][]byte{raw}, time.Unix(unix, 0), d.body)
	_, v1, _ := strings.Cut(want, ",")
	return strings.Contains(header, v1)
}

// serveWithWorker serves the real API on loopback with its delivery loop running. The test configuration lets webhooks
// reach plain http and loopback; the server's policy in code is unchanged.
func serveWithWorker(t *testing.T) (url, key, acc string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("SELFCLOUD_DATA_DIR", dir)
	code, out, errOut := runCmd(t, "admin", "create-account", "-name", "probe")
	if code != 0 {
		t.Fatalf("create-account: exit %d, stderr %q", code, errOut)
	}
	acc, key = valueOf(t, out, "account_id="), valueOf(t, out, "account_key=")
	cfg := config.Defaults()
	cfg.DataDir = dir
	cfg.Storage.MinFree = 0
	cfg.Store.Path = dir + "/selfcloud.db"
	cfg.Webhooks.AllowHTTP = true
	cfg.Webhooks.AllowNetworks = []string{"127.0.0.0/8"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), store.Options{Path: cfg.Store.Path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s, err := api.New(api.Options{Config: cfg, Store: st})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = s.Serve(ctx, ln, nil) }()
	t.Cleanup(func() { cancel(); <-done })
	t.Setenv("SELFCLOUD_TOKEN", key)
	return "http://" + ln.Addr().String(), key, acc
}

// TestWebhookDeliveryToAReceiverOnLoopback: a webhook added with the command is sent a test event; the receiver checks the
// signature with the secret the command printed. After a rotation the new secret signs, and deliveries lists the log.
func TestWebhookDeliveryToAReceiverOnLoopback(t *testing.T) {
	isolateRemote(t)
	url, _, acc := serveWithWorker(t)
	rcv := newReceiver(t)
	do := func(args ...string) string {
		t.Helper()
		var out, errOut bytes.Buffer
		if code := run(append(args, "--url", url), &out, &errOut); code != 0 {
			t.Fatalf("%v: exit %d, stderr %q", args, code, errOut.String())
		}
		return out.String()
	}
	added := do("webhook", "add", "--account", acc, "--endpoint", rcv.srv.URL+"/in", "--event", "webhook.test")
	hook, secret := valueOf(t, added, "id="), valueOf(t, added, "secret=")

	do("webhook", "test", "--account", acc, hook)
	first := rcv.next(t, 1)
	if first.header.Get("Selfcloud-Event-Type") != "webhook.test" || !signedWith(t, first, secret) {
		t.Fatalf("first delivery: type %q, signed with the printed secret: %v", first.header.Get("Selfcloud-Event-Type"), signedWith(t, first, secret))
	}
	var ev map[string]any
	if err := json.Unmarshal(first.body, &ev); err != nil {
		t.Fatalf("the delivery body is not JSON: %v", err)
	}

	rotated := do("webhook", "rotate-secret", "--account", acc, hook, "--yes")
	next := valueOf(t, rotated, "secret=")
	do("webhook", "test", "--account", acc, hook)
	second := rcv.next(t, 2)
	if !signedWith(t, second, next) {
		t.Fatal("the second delivery is not signed with the rotated secret")
	}

	var log string
	for i := 0; i < 50; i++ {
		log = do("webhook", "deliveries", "--account", acc, hook)
		if strings.Count(log, "delivered") == 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if strings.Count(log, "delivered") != 2 || strings.Contains(log, secret) || strings.Contains(log, next) {
		t.Fatalf("deliveries: %q", log)
	}
	for _, out := range []string{do("webhook", "show", "--account", acc, hook), do("webhook", "ls", "--account", acc, "--json")} {
		if strings.Contains(out, secret) || strings.Contains(out, next) {
			t.Fatalf("a read printed a secret: %q", out)
		}
	}
}
