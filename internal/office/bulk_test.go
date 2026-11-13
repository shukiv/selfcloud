// SPDX-License-Identifier: AGPL-3.0-or-later

package office

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The converter's failures are told apart by what happened, and the content search's own conversions never
// take a user's preview slot, nor count a wait for a slot as a conversion's time.

// TestUnreachableConverterIsNotAnAttempt: a connection that cannot be made is ErrUnreachable, not ErrUnavailable, so the
// worker pauses instead of counting a try against the document. Mutation: classifying every failure as ErrUnavailable
// makes this test fail.
func TestUnreachableConverterIsNotAnAttempt(t *testing.T) {
	gone := httptest.NewServer(http.NotFoundHandler())
	base := gone.URL
	gone.Close()
	c, err := New(base, nil, Options{Concurrent: 1, Timeout: time.Second, MaxOutput: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	err = c.ConvertBulk(context.Background(), strings.NewReader("x"), 1, "docx", "txt", io.Discard)
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("got %v, want ErrUnreachable for a closed port", err)
	}
}

// TestBulkUsesItsOwnSlot: a bulk conversion that is in flight holds the bulk slot, not a preview slot, so a preview is
// served at once. Mutation: ConvertBulk taking c.slots makes the preview wait for the stalled bulk conversion.
func TestBulkUsesItsOwnSlot(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	f := newFakeConverter(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			<-release // the bulk conversion, which is the first request, stalls until released
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "ok")
	})
	c := newTestClient(t, f, Options{Timeout: 10 * time.Second, MaxOutput: 1 << 20})
	bulkDone := make(chan error, 1)
	go func() {
		bulkDone <- c.ConvertBulk(context.Background(), strings.NewReader("x"), 1, "docx", "txt", io.Discard)
	}()
	// A failed assertion below must not leave the stalled request open, or the server cannot close and the run hangs. The
	// cleanup runs before the server's (it was registered later), and releases the request only if it is still held.
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	// Wait until the stalled handler is reached, not only until the slot is held: a preview that reached the server first
	// would be the first request, and the one that stalls.
	waitFor(t, func() bool { return calls.Load() == 1 && len(c.bulk) == 1 })
	// The preview has its own deadline: if it shares the bulk work's slot it waits behind the stalled request, and the
	// assertion below fails at two seconds, instead of the run hanging until the test timeout.
	pctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if err := c.Convert(pctx, strings.NewReader("x"), 1, "docx", "txt", io.Discard); err != nil {
		close(release)
		<-bulkDone
		t.Fatalf("the preview did not get a slot while bulk work was stalled (after %v): %v", time.Since(start), err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("the preview waited %v behind bulk work", d)
	}
	close(release)
	if err := <-bulkDone; err != nil {
		t.Fatalf("bulk: %v", err)
	}
}

// TestBulkQueueTimeIsNotCounted: a bulk conversion that waits for the one bulk slot is not timed out by that wait. The
// conversion's own limit starts when its request does, so the second conversion has the full limit once it has the slot.
// Mutation: a deadline set before the slot is taken makes the second conversion time out.
func TestBulkQueueTimeIsNotCounted(t *testing.T) {
	var calls atomic.Int32
	f := newFakeConverter(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			time.Sleep(300 * time.Millisecond)
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "ok")
	})
	c := newTestClient(t, f, Options{Timeout: 250 * time.Millisecond, MaxOutput: 1 << 20})
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			errs <- c.ConvertBulk(context.Background(), strings.NewReader("x"), 1, "docx", "txt", io.Discard)
		}()
	}
	// The first stalls past its limit (a time-out, as it should); the second waits for the slot for about as long and
	// must not count that wait. It has its own limit, and the converter answers at once.
	first, second := <-errs, <-errs
	var timeouts int
	for _, err := range []error{first, second} {
		if errors.Is(err, context.DeadlineExceeded) {
			timeouts++
		}
	}
	if timeouts != 1 {
		t.Fatalf("time-outs: %d (first %v, second %v); want exactly the stalled conversion to time out", timeouts, first, second)
	}
}

// waitFor polls cond until it is true, failing the test after a few seconds.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for range 500 {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}
