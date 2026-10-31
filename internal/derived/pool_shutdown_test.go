// SPDX-License-Identifier: AGPL-3.0-or-later

package derived

import (
	"context"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/store"
)

// M1: a shutdown that stops a decode in progress records nothing. The child is killed by the cancelled context, so it
// exits by a signal: that is the outcome a host condition would give, and it must not be written as a failure.
func TestShutdownDuringADecodeRecordsNothing(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	// A large image, so the decode is still running when the context is cancelled. The cancel waits for the child to
	// start: a cancel before that is a refusal to start, which records nothing for another reason.
	bl := f.putBlob(f.acc, samplePNG(t, 4000, 4000))
	f.request(f.acc, bl, 256)
	ctx, cancel := context.WithCancel(f.ctx)
	time.AfterFunc(400*time.Millisecond, cancel)
	<-f.pool.q.ready // the token that goes with the job, as a worker takes it
	j, ok := f.pool.q.pop()
	if !ok {
		t.Fatal("no job queued")
	}
	f.pool.safeProcess(ctx, j)
	if d := f.stateOn(f.pool, f.acc, bl, 256); d != nil && d.State == store.DerivedFailed {
		t.Fatalf("a shutdown recorded a failure: %+v", d)
	}
	failed, err := f.st.DerivedCount(f.ctx, store.DerivedFailed)
	if err != nil {
		t.Fatal(err)
	}
	if failed != 0 {
		t.Fatalf("%d failed rows after a shutdown", failed)
	}
}
