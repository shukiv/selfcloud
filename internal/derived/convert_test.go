// SPDX-License-Identifier: AGPL-3.0-or-later

package derived

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/office"
	"github.com/shukiv/selfcloud/internal/store"
)

// convKey and convKind are the converter key and kind of the conversions in these tests.
const (
	convKey  = "test-convert"
	convKind = "export_pdf"
)

// fakeConv is a Converter with a fixed answer. err is returned after the source is read; block waits for the context
// instead and returns its error, as a conversion that runs past the time limit does.
type fakeConv struct {
	err   error
	out   []byte
	block bool
}

func (c *fakeConv) Convert(ctx context.Context, src io.Reader, size int64, ext, format string, dst io.Writer) error {
	if _, err := io.Copy(io.Discard, src); err != nil {
		return err
	}
	if c.block {
		<-ctx.Done()
		return ctx.Err()
	}
	if c.err != nil {
		return c.err
	}
	_, err := dst.Write(c.out)
	return err
}

// convPool returns a conversion pool on the fixture's store and cache, with the base options changed by mod.
func (f *fixture) convPool(conv Converter, mod func(*ConvertOptions)) *ConvertPool {
	f.t.Helper()
	o := ConvertOptions{Store: f.st, Blobs: f.blobs, Cache: f.cache, Conv: conv, Workers: 1, Queue: 8, PerAccount: 8,
		Timeout: time.Minute, MaxSourceBytes: 1 << 20, MaxOutputBytes: 1 << 20,
		Now: func() time.Time { return f.clock }}
	if mod != nil {
		mod(&o)
	}
	return NewConvertPool(o)
}

// convRequest asks for a PDF of a blob of accountID, from a source named docx.
func (f *fixture) convRequest(p *ConvertPool, accountID, blobID string) (Answer, error) {
	f.t.Helper()
	return p.Request(f.ctx, accountID, blobID, convKind, "pdf", "docx", convKey, nil, nil)
}

// runConv runs the next queued conversion on this goroutine.
func (f *fixture) runConv(p *ConvertPool) {
	f.t.Helper()
	select {
	case <-p.q.ready:
		j, ok := p.q.pop()
		if !ok {
			f.t.Fatal("a token with no job")
		}
		p.safeProcess(f.ctx, j)
	default:
		f.t.Fatal("no conversion is queued")
	}
}

// convRow returns the conversion row of a blob of accountID.
func (f *fixture) convRow(accountID, blobID string) *store.Derived {
	f.t.Helper()
	d, err := f.cache.Lookup(f.ctx, accountID, blobID, convKind, convKey)
	if err != nil {
		f.t.Fatal(err)
	}
	return d
}

// addAccount adds a second account to the store and returns its ID.
func (f *fixture) addAccount() string {
	f.t.Helper()
	acc := id.New("acc")
	if err := f.st.Write(f.ctx, func(tx *store.Tx) error {
		return tx.InsertAccount(f.ctx, &store.Account{ID: acc, Name: "Other", Status: store.AccountActive,
			CreatedAt: time.Now()})
	}); err != nil {
		f.t.Fatal(err)
	}
	return acc
}

// TestConvertDefaultAccountShareIsEightOfTheQueue: with no share given, one account may hold 8 places of the queue, not
// all of them (decision 37).
func TestConvertDefaultAccountShareIsEightOfTheQueue(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	p := NewConvertPool(ConvertOptions{Store: f.st, Blobs: f.blobs, Cache: f.cache, Conv: &fakeConv{}, Queue: 32})
	if p.q.perAccount != 8 || p.q.limit != 32 {
		t.Errorf("share %d of %d, want 8 of 32", p.q.perAccount, p.q.limit)
	}
}

// TestConvertAccountShareLeavesRoomForAnotherAccount: one account at its share is refused, and another account is still
// admitted with places left in the queue.
func TestConvertAccountShareLeavesRoomForAnotherAccount(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	other := f.addAccount()
	p := f.convPool(&fakeConv{}, func(o *ConvertOptions) { o.Queue = 4; o.PerAccount = 2 })
	for i := range 2 {
		if _, err := f.convRequest(p, f.acc, f.putBlob(f.acc, []byte{byte(i), 1})); err != nil {
			t.Fatal(err)
		}
	}
	third := f.putBlob(f.acc, []byte{9, 9})
	if _, err := f.convRequest(p, f.acc, third); !errors.Is(err, ErrBusy) {
		t.Fatalf("third request of one account: %v, want ErrBusy", err)
	}
	if n := len(p.q.ready); n != 2 {
		t.Errorf("%d jobs queued after a refused request, want 2", n)
	}
	if _, err := p.Request(f.ctx, other, f.putBlob(other, []byte{7}), convKind, "pdf", "docx", convKey, nil, nil); err != nil {
		t.Fatalf("another account refused with places free: %v", err)
	}
}

// TestOutageIsRetriedAfterAMinuteAndNotCounted: a converter that is down makes a failure with no attempt counted, so
// it is tried again after a minute each time, and never becomes sticky (decision 37).
func TestOutageIsRetriedAfterAMinuteAndNotCounted(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	p := f.convPool(&fakeConv{err: fmt.Errorf("%w: connection refused", office.ErrUnavailable)}, nil)
	bl := f.putBlob(f.acc, []byte("doc"))
	for i := range 4 {
		if _, err := f.convRequest(p, f.acc, bl); err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
		f.runConv(p)
		d := f.convRow(f.acc, bl)
		if d == nil || d.State != store.DerivedFailed || d.Attempts != 0 || d.RetryAt.Sub(f.clock) != time.Minute {
			t.Fatalf("round %d: %+v; want failed, 0 attempts, retry in a minute", i, d)
		}
		f.clock = f.clock.Add(time.Minute)
	}
}

// TestRefusalIsRetriedAfterADay: a refusal of the document is kept for a day, not for ever (decision 37).
func TestRefusalIsRetriedAfterADay(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	p := f.convPool(&fakeConv{err: office.ErrRefused}, nil)
	bl := f.putBlob(f.acc, []byte("doc"))
	if _, err := f.convRequest(p, f.acc, bl); err != nil {
		t.Fatal(err)
	}
	f.runConv(p)
	if d := f.convRow(f.acc, bl); d == nil || d.State != store.DerivedFailed || d.RetryAt.Sub(f.clock) != 24*time.Hour {
		t.Fatalf("after a refusal: %+v; want failed with a retry a day on", d)
	}
	f.clock = f.clock.Add(23 * time.Hour)
	if a, _ := f.convRequest(p, f.acc, bl); a.State != store.DerivedFailed || len(p.q.ready) != 0 {
		t.Errorf("at 23 hours: state %s, queued %d; want still failed and not queued", a.State, len(p.q.ready))
	}
	f.clock = f.clock.Add(2 * time.Hour)
	if a, err := f.convRequest(p, f.acc, bl); err != nil || a.State != store.DerivedPending {
		t.Errorf("at 25 hours: %+v, %v; want pending", a, err)
	}
}

// TestTimeoutCountsThreeAttemptsThenWaitsADay: a time-out is counted. It is retried after a minute, then after ten, and
// the third one becomes a day's wait.
func TestTimeoutCountsThreeAttemptsThenWaitsADay(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	p := f.convPool(&fakeConv{block: true}, func(o *ConvertOptions) { o.Timeout = time.Millisecond })
	bl := f.putBlob(f.acc, []byte("doc"))
	waits := []time.Duration{time.Minute, 10 * time.Minute, 24 * time.Hour}
	for attempt := 1; attempt <= 3; attempt++ {
		if _, err := f.convRequest(p, f.acc, bl); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		at := f.clock
		f.runConv(p)
		d := f.convRow(f.acc, bl)
		if d == nil || d.Attempts != attempt || !strings.HasPrefix(d.Error, msgConvertTimeout) {
			t.Fatalf("attempt %d: %+v; want %d attempts and the time-out message", attempt, d, attempt)
		}
		if got := d.RetryAt.Sub(at); got != waits[attempt-1] {
			t.Fatalf("attempt %d retried after %v, want %v", attempt, got, waits[attempt-1])
		}
		f.clock = f.clock.Add(25 * time.Hour)
	}
}

// TestOversizeOutputFailsStickilyAndEvictsNothing: an output larger than the whole budget fails for its key, and does not
// evict another account's file to make room it can never have (decision 37).
func TestOversizeOutputFailsStickilyAndEvictsNothing(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	f.cache.maxBytes = 1000
	other := f.addAccount()
	small := f.convPool(&fakeConv{out: make([]byte, 100)}, nil)
	ob := f.putBlob(other, []byte("other"))
	if _, err := f.convRequest(small, other, ob); err != nil {
		t.Fatal(err)
	}
	f.runConv(small)
	if d := f.convRow(other, ob); d == nil || d.State != store.DerivedReady {
		t.Fatalf("the other account's file: %+v; want ready", d)
	}
	big := f.convPool(&fakeConv{out: make([]byte, 2000)}, nil)
	bl := f.putBlob(f.acc, []byte("mine"))
	if _, err := f.convRequest(big, f.acc, bl); err != nil {
		t.Fatal(err)
	}
	f.runConv(big)
	d := f.convRow(f.acc, bl)
	if d == nil || d.State != store.DerivedFailed || d.Error != msgConvertOutput || d.RetryAt.Sub(f.clock) != 24*time.Hour {
		t.Errorf("oversize output: %+v; want failed as over the limit, sticky for a day", d)
	}
	if d := f.convRow(other, ob); d == nil || d.State != store.DerivedReady {
		t.Errorf("the other account's file was evicted for an output that cannot fit: %+v", d)
	}
	if n, err := f.st.ReadyDerivedBytes(f.ctx); err != nil || n != 100 {
		t.Errorf("ready bytes %d, %v; want the 100 of the other file", n, err)
	}
}

// TestEvictionRemovesOnlyWhatTheBudgetNeeds: a file that needs 300 bytes of a full 1000-byte budget evicts one 300-byte
// file, not all of them.
func TestEvictionRemovesOnlyWhatTheBudgetNeeds(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	f.cache.maxBytes = 1000
	p := f.convPool(&fakeConv{out: make([]byte, 300)}, nil)
	var blobs []string
	for i := range 4 {
		bl := f.putBlob(f.acc, []byte{byte(i), 'x'})
		blobs = append(blobs, bl)
		if _, err := f.convRequest(p, f.acc, bl); err != nil {
			t.Fatal(err)
		}
		f.runConv(p)
	}
	ready := 0
	for _, bl := range blobs {
		if d := f.convRow(f.acc, bl); d != nil && d.State == store.DerivedReady {
			ready++
		}
	}
	if ready != 3 {
		t.Errorf("%d ready files after the fourth, want 3 (one evicted, the new one kept)", ready)
	}
	if d := f.convRow(f.acc, blobs[3]); d == nil || d.State != store.DerivedReady {
		t.Errorf("the newest file: %+v; want ready", d)
	}
}
