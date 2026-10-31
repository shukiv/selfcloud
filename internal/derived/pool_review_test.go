// SPDX-License-Identifier: AGPL-3.0-or-later

package derived

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// M1: a failure of the image is kept for its key, and a limit that changes is a new key.
func TestImageRefusalIsStickyUntilALimitChanges(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	low := f.newPool(func(o *Options) { o.MaxPixels = 100 })
	bl := f.putBlob(f.acc, samplePNG(t, 64, 64))
	if _, err := low.Request(f.ctx, f.acc, bl, 64, nil); err != nil {
		t.Fatal(err)
	}
	f.runOn(low)
	if d := f.stateOn(low, f.acc, bl, 64); d == nil || d.State != store.DerivedFailed || d.Error != msgTooLarge {
		t.Fatalf("under a 100-pixel cap: %+v", d)
	}
	if d := f.stateOn(low, f.acc, bl, 64); d.RetryAt.IsZero() == false {
		t.Errorf("an image's refusal has a retry time: %v", d.RetryAt)
	}
	// Under the same limit, the refusal is kept: no queue, no decode.
	if a, _ := low.Request(f.ctx, f.acc, bl, 64, nil); a.State != store.DerivedFailed || low.queuedJobs() != 0 {
		t.Errorf("the same limit queued the refused image again: state %s, queued %d", a.State, low.queuedJobs())
	}
	// Raising the cap is a new key: the image is queued once and made.
	high := f.newPool(func(o *Options) {})
	if a, err := high.Request(f.ctx, f.acc, bl, 64, nil); err != nil || a.State != store.DerivedPending || high.queuedJobs() != 1 {
		t.Fatalf("after raising the cap: %+v, %v, queued %d", a, err, high.queuedJobs())
	}
	f.runOn(high)
	if d := f.stateOn(high, f.acc, bl, 64); d == nil || d.State != store.DerivedReady {
		t.Errorf("after raising the cap the image was not made: %+v", d)
	}
}

// M1: a time limit is a host condition. It is retried after a wait, up to maxAttempts, then it is sticky.
func TestTimeoutIsRetriedWithBackoffThenSticky(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	slow := f.newPool(func(o *Options) { o.Timeout = time.Nanosecond })
	bl := f.putBlob(f.acc, samplePNG(t, 64, 64))
	if _, err := slow.Request(f.ctx, f.acc, bl, 64, nil); err != nil {
		t.Fatal(err)
	}
	f.runOn(slow)
	d := f.stateOn(slow, f.acc, bl, 64)
	if d == nil || d.State != store.DerivedFailed || d.Error != msgTimeout || d.Attempts != 1 || d.RetryAt.IsZero() {
		t.Fatalf("after one timeout: %+v; want failed with a retry time", d)
	}
	// Before the retry time, a request is answered and queues nothing.
	if a, _ := slow.Request(f.ctx, f.acc, bl, 64, nil); a.State != store.DerivedFailed || slow.queuedJobs() != 0 {
		t.Fatalf("before the retry time: state %s, queued %d", a.State, slow.queuedJobs())
	}
	for attempt := 2; attempt <= maxAttempts; attempt++ {
		f.clock = f.stateOn(slow, f.acc, bl, 64).RetryAt.Add(time.Second)
		if a, err := slow.Request(f.ctx, f.acc, bl, 64, nil); err != nil || a.State != store.DerivedPending || slow.queuedJobs() != 1 {
			t.Fatalf("attempt %d at its retry time: %+v, %v, queued %d", attempt, a, err, slow.queuedJobs())
		}
		f.runOn(slow)
		d = f.stateOn(slow, f.acc, bl, 64)
		if d.Attempts != attempt {
			t.Fatalf("attempts = %d, want %d", d.Attempts, attempt)
		}
	}
	if !d.RetryAt.IsZero() {
		t.Fatalf("after %d attempts the failure still has a retry time", maxAttempts)
	}
	if d.Error == msgTimeout {
		t.Errorf("the sticky reason does not say it gave up: %q", d.Error)
	}
	// Sticky: far in the future, still no queue.
	f.clock = f.clock.Add(100 * 24 * time.Hour)
	if a, _ := slow.Request(f.ctx, f.acc, bl, 64, nil); a.State != store.DerivedFailed || slow.queuedJobs() != 0 {
		t.Errorf("after the attempts ran out: state %s, queued %d", a.State, slow.queuedJobs())
	}
}

// M1: a shutdown records nothing. The job is dropped, the row is removed, and no failure is written.
func TestShutdownRecordsNothing(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	bl := f.putBlob(f.acc, samplePNG(t, 64, 64))
	f.request(f.acc, bl, 64)
	ctx, cancel := context.WithCancel(f.ctx)
	cancel()
	select {
	case <-f.pool.q.ready:
	default:
		t.Fatal("no job queued")
	}
	j, _ := f.pool.q.pop()
	f.pool.safeProcess(ctx, j)
	if d := f.state(f.acc, bl, 64); d != nil && d.State == store.DerivedFailed {
		t.Errorf("a shutdown recorded a failure: %+v", d)
	}
	// And the next start can queue it again.
	if a := f.request(f.acc, bl, 64); a.State != store.DerivedPending {
		t.Errorf("after a shutdown the request answered %s", a.State)
	}
}

// M1: a retry that has come is queued by the request, and the row keeps its attempt count.
func TestRetryKeepsTheAttemptCount(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	slow := f.newPool(func(o *Options) { o.Timeout = time.Nanosecond })
	bl := f.putBlob(f.acc, samplePNG(t, 64, 64))
	slow.Request(f.ctx, f.acc, bl, 64, nil)
	f.runOn(slow)
	f.clock = f.stateOn(slow, f.acc, bl, 64).RetryAt
	slow.Request(f.ctx, f.acc, bl, 64, nil)
	if d := f.stateOn(slow, f.acc, bl, 64); d.State != store.DerivedPending || d.Attempts != 1 {
		t.Errorf("queued retry: state %s, attempts %d; want pending, 1", d.State, d.Attempts)
	}
}

// M1: a failed row older than the sweep's age is cleared, so its key is queued again.
func TestClearFailedRemovesOldFailures(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	low := f.newPool(func(o *Options) { o.MaxPixels = 100 })
	bl := f.putBlob(f.acc, samplePNG(t, 64, 64))
	low.Request(f.ctx, f.acc, bl, 64, nil)
	f.runOn(low)
	failedAt := f.stateOn(low, f.acc, bl, 64).FailedAt
	if n, err := f.cache.ClearFailed(f.ctx, failedAt); err != nil || n != 0 {
		t.Fatalf("a failure is not older than its own time: removed %d, %v", n, err)
	}
	if n, err := f.cache.ClearFailed(f.ctx, failedAt.Add(time.Second)); err != nil || n != 1 {
		t.Fatalf("old failure: removed %d, %v; want 1", n, err)
	}
	if d := f.stateOn(low, f.acc, bl, 64); d != nil {
		t.Errorf("a cleared failure is still a row: %+v", d)
	}
}

// M3: requests cancelled while the queue is full leave no pending row without a job.
func TestCancelledRequestsLeaveNoPendingRowWithoutAJob(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 1)
	first := f.putBlob(f.acc, samplePNG(t, 16, 16))
	f.request(f.acc, first, 16) // fills the one place
	other := f.putBlob(f.acc, samplePNG(t, 17, 17))
	for i := 0; i < 400; i++ {
		ctx, cancel := context.WithTimeout(f.ctx, time.Duration(i%5)*time.Millisecond)
		_, err := f.pool.Request(ctx, f.acc, other, 16+i%3, nil)
		cancel()
		if err != nil && !errors.Is(err, ErrBusy) && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	// Only the one request that holds the place has a row, and that row has a job.
	pending, err := f.st.DerivedCount(f.ctx, store.DerivedPending)
	if err != nil {
		t.Fatal(err)
	}
	if pending != f.queued() || pending != 1 {
		t.Errorf("pending rows = %d, queued jobs = %d; want 1 and 1", pending, f.queued())
	}
}

// M4: a request over an account's share costs no writer lock. The writer is held here, so a request that
// tried to write would wait for it and never return.
func TestRequestOverTheAccountShareTakesNoWriterLock(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 256)
	// The share is fixed by decision 33 at 32. The test does not read it from the code, so a change to the code's
	// constant is seen here rather than changing the test's own setup.
	const share = 32
	var blobs []string
	for i := 0; i < share+1; i++ {
		blobs = append(blobs, f.putBlob(f.acc, samplePNG(t, 8+i, 8)))
	}
	for _, b := range blobs[:share] {
		f.request(f.acc, b, 16)
	}
	done := make(chan error, 1)
	var got error
	answered := false
	// The request runs while this write holds the writer. Its answer must come without the lock: after the time
	// below, the write is ended, so a request that was waiting finishes and its answer is compared all the same.
	_ = f.st.Write(f.ctx, func(tx *store.Tx) error {
		go func() {
			_, err := f.pool.Request(f.ctx, f.acc, blobs[share], 16, nil)
			done <- err
		}()
		select {
		case got = <-done:
			answered = true
		case <-time.After(5 * time.Second):
			t.Error("a request over the account's share waited for the writer lock")
		}
		return errEndWrite
	})
	if !answered {
		got = <-done
	}
	if !errors.Is(got, ErrBusy) {
		t.Errorf("over the share: %v, want ErrBusy", got)
	}
}

// errEndWrite ends a write that changed nothing, so the write rolls back.
var errEndWrite = errors.New("derived test: end the write without changes")

// M4: one account's backlog does not hold the workers while another waits. Account A fills its share, then B
// queues one job; B's job is the second to run, after the one of A's that is already next.
func TestRoundRobinBetweenAccounts(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 256)
	other := id.New("acc")
	if err := f.st.Write(f.ctx, func(tx *store.Tx) error {
		return tx.InsertAccount(f.ctx, &store.Account{ID: other, Name: "B", Status: store.AccountActive, CreatedAt: time.Now()})
	}); err != nil {
		t.Fatal(err)
	}
	var aBlobs []string
	for i := 0; i < perAccountQueue; i++ {
		aBlobs = append(aBlobs, f.putBlob(f.acc, samplePNG(t, 8+i, 8)))
		f.request(f.acc, aBlobs[i], 16)
	}
	bBlob := f.putBlob(other, samplePNG(t, 40, 40))
	if a := f.request(other, bBlob, 16); a.State != store.DerivedPending {
		t.Fatalf("B's request: %s", a.State)
	}
	var order []string
	for range perAccountQueue + 1 {
		j, ok := f.pool.q.pop()
		if !ok {
			t.Fatal("queue ran out early")
		}
		order = append(order, j.accountID)
	}
	if order[0] != f.acc || order[1] != other {
		t.Errorf("order of accounts = %v..., want A then B", order[:2])
	}
}

// M4: the total is still the constant the queue has always had, whichever accounts fill it.
func TestTotalQueueIsStillBounded(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 2)
	a := f.putBlob(f.acc, samplePNG(t, 8, 8))
	b := f.putBlob(f.acc, samplePNG(t, 9, 8))
	c := f.putBlob(f.acc, samplePNG(t, 10, 8))
	f.request(f.acc, a, 16)
	f.request(f.acc, b, 16)
	if _, err := f.pool.Request(f.ctx, f.acc, c, 16, nil); !errors.Is(err, ErrBusy) {
		t.Fatalf("third request over a total of 2: %v", err)
	}
}

// L5: a refused image is logged below warning, and only once per request that decodes it.
func TestRefusalIsLoggedBelowWarning(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	f := newFixture(t, 8)
	quiet := f.newPool(func(o *Options) { o.MaxPixels = 100; o.Log = log })
	bl := f.putBlob(f.acc, samplePNG(t, 64, 64))
	quiet.Request(f.ctx, f.acc, bl, 64, nil)
	f.runOn(quiet)
	if buf.Len() == 0 {
		t.Fatal("the refusal was not logged at all")
	}
	if bytes.Contains(buf.Bytes(), []byte("level=WARN")) || bytes.Contains(buf.Bytes(), []byte("level=ERROR")) {
		t.Errorf("a refused image logged above info:\n%s", buf.String())
	}
}
