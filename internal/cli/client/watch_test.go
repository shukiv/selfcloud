// SPDX-License-Identifier: AGPL-3.0-or-later

package client

import (
	"bytes"
	"io"
	"testing"
	"time"
)

// runningLimit reads the limit now running, under the watch's own lock.
func runningLimit(w *watch) time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.limit
}

// TestWatchEndOfBodySetsTheTailBeforeTheAnswer: once the last byte of a piece has been read from its source, the longer
// tail limit runs, and a byte moving (kick) returns it to the stall limit while the answer has not yet arrived.
func TestWatchEndOfBodySetsTheTailBeforeTheAnswer(t *testing.T) {
	t.Parallel()
	const stall, tail = 200 * time.Millisecond, 3 * time.Second
	w := newWatch(stall, func() {})
	defer w.stop()
	w.endOfBody(tail)
	if got := runningLimit(w); got != tail {
		t.Errorf("after the body's end the limit = %v, want the tail %v", got, tail)
	}
	w.kick()
	if got := runningLimit(w); got != stall {
		t.Errorf("a byte moving before the answer: limit = %v, want the stall %v", got, stall)
	}
}

// TestWatchedUploadGivesTheTailOnce: the transport reads past the declared length to see whether the body is longer than
// declared, and may do so more than once. The tail is given the first time the declared length is out, and not again on
// those later reads: after a kick has returned the limit to the stall, a later read must not set the tail back.
func TestWatchedUploadGivesTheTailOnce(t *testing.T) {
	t.Parallel()
	const stall, tail = 200 * time.Millisecond, 3 * time.Second
	w := newWatch(stall, func() {})
	defer w.stop()
	data := []byte("abcd")
	u := &watchedUpload{r: bytes.NewReader(data), w: w, left: int64(len(data)), tail: tail}
	buf := make([]byte, 8)
	if _, err := u.Read(buf); err != nil {
		t.Fatalf("the first read: %v", err)
	}
	if got := runningLimit(w); got != tail {
		t.Fatalf("after the declared length is out the limit = %v, want the tail %v", got, tail)
	}
	// A byte moving (the answer's headers) returns the limit to the stall.
	w.kick()
	for i := 2; i <= 3; i++ {
		n, err := u.Read(buf)
		if n != 0 || err != io.EOF {
			t.Fatalf("read %d past the declared length = (%d, %v), want (0, EOF)", i, n, err)
		}
		if got := runningLimit(w); got != stall {
			t.Errorf("read %d past the declared length set the limit to %v, want the tail given once, leaving %v", i, got, stall)
		}
	}
}
