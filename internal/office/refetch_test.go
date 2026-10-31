// SPDX-License-Identifier: AGPL-3.0-or-later

package office

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestConvertSendsAFixedLengthBody: the body is sent with a Content-Length that is the bytes it holds, not chunked.
func TestConvertSendsAFixedLengthBody(t *testing.T) {
	t.Parallel()
	f := newFakeConverter(t, okPDF(10))
	c := newTestClient(t, f, Options{Timeout: 5 * time.Second, MaxOutput: 1 << 20})
	if err := c.Convert(context.Background(), strings.NewReader("doc bytes"), 9, "docx", "pdf", io.Discard); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.chunked {
		t.Error("the request was chunked; the length is known and must be sent")
	}
	if f.contentLength <= 0 || f.contentLength != f.received {
		t.Errorf("Content-Length %d, but the body held %d bytes", f.contentLength, f.received)
	}
}

// TestConvertFailsAShortSource: a source that holds fewer bytes than its size fails as an outage; nothing is kept.
func TestConvertFailsAShortSource(t *testing.T) {
	t.Parallel()
	f := newFakeConverter(t, okPDF(10))
	c := newTestClient(t, f, Options{Timeout: 5 * time.Second, MaxOutput: 1 << 20})
	err := c.Convert(context.Background(), strings.NewReader("abc"), 20, "docx", "pdf", io.Discard)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v, want ErrUnavailable for a source shorter than its size", err)
	}
}

// TestStaleCapabilitiesServedWhenRefetchFails: once the copy is due, a failed refetch keeps the stale copy answering, and
// refetches are tried at most once every 30 seconds while Collabora is down.
func TestStaleCapabilitiesServedWhenRefetchFails(t *testing.T) {
	t.Parallel()
	fail := atomic.Bool{}
	f := newFakeConverter(t, func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"convert-to":{"available":true},"productVersion":"24.04.7.2","productVersionHash":"c6c73bdf"}`))
	})
	c := newTestClient(t, f, Options{Timeout: 5 * time.Second, MaxOutput: 1 << 20})
	now := time.Now()
	c.now = func() time.Time { return now }
	if _, err := c.Capabilities(context.Background()); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	now = now.Add(2 * time.Hour)
	fail.Store(true)
	for i, step := range []time.Duration{0, 0, 10 * time.Second, 25 * time.Second} {
		now = now.Add(step)
		caps, err := c.Capabilities(context.Background())
		if err != nil {
			t.Fatalf("call %d while Collabora is down: %v; the stale copy must answer", i, err)
		}
		if caps.VersionHash != "c6c73bdf" {
			t.Fatalf("call %d: caps %+v, want the stale copy", i, caps)
		}
	}
	// Four calls over 35 seconds: the refetch was due at the first, and again once 30 seconds had passed.
	if n := f.hits.Load(); n != 3 {
		t.Errorf("%d capabilities requests in all (one good, then two failures), want 3", n)
	}
}

// TestRefetchDoesNotHoldTheLock: while a refetch is in flight, other callers get the stale copy at once, and are not held
// behind the network call.
func TestRefetchDoesNotHoldTheLock(t *testing.T) {
	t.Parallel()
	stall := atomic.Bool{}
	release := make(chan struct{})
	f := newFakeConverter(t, func(w http.ResponseWriter, r *http.Request) {
		if stall.Load() {
			<-release
		}
		w.Write([]byte(`{"convert-to":{"available":true},"productVersion":"24.04.7.2","productVersionHash":"c6c73bdf"}`))
	})
	t.Cleanup(func() { close(release) })
	c := newTestClient(t, f, Options{Timeout: 5 * time.Second, MaxOutput: 1 << 20})
	now := time.Now()
	c.now = func() time.Time { return now }
	if _, err := c.Capabilities(context.Background()); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	now = now.Add(2 * time.Hour)
	stall.Store(true)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = c.Capabilities(context.Background())
	}()
	deadline := time.Now().Add(5 * time.Second)
	for f.hits.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("the refetch never started")
		}
		time.Sleep(time.Millisecond)
	}
	start := time.Now()
	caps, err := c.Capabilities(context.Background())
	if err != nil || caps.VersionHash != "c6c73bdf" {
		t.Fatalf("a caller during the refetch got %+v, %v; want the stale copy", caps, err)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("a caller during the refetch waited %v; the lock is held across the network", d)
	}
	release <- struct{}{}
	<-done
}
