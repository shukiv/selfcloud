// SPDX-License-Identifier: AGPL-3.0-or-later

package derived

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/store"
)

// A shutdown that stops a job in progress records nothing. The child is killed by the cancelled context, so it exits
// by a signal: that is the outcome a host condition would give, and it must not be written as a failure.
//
// The child is a script that blocks, not a real decode: a real decode can end on its own before the cancel arrives, by
// finishing or by reaching the child's memory limit, and then the shutdown is not what stopped it.
func TestShutdownDuringADecodeRecordsNothing(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	exe := filepath.Join(dir, "decode.sh")
	script := fmt.Sprintf("#!/bin/sh\n: > %q\nexec /bin/sleep 30\n", started)
	if err := os.WriteFile(exe, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	f.pool = f.newPool(func(o *Options) { o.Exe = exe })
	bl := f.putBlob(f.acc, samplePNG(t, 64, 64))
	f.request(f.acc, bl, 256)
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	// The child writes its marker before it blocks, so the cancel only comes once it is surely running. The poll has a
	// deadline so a child that never starts fails the test instead of hanging it.
	cancelled := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err := os.Stat(started); err == nil {
				cancel()
				cancelled <- nil
				return
			}
			if time.Now().After(deadline) {
				cancel()
				cancelled <- errors.New("the child did not start")
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	<-f.pool.q.ready // the token that goes with the job, as a worker takes it
	j, ok := f.pool.q.pop()
	if !ok {
		t.Fatal("no job queued")
	}
	f.pool.safeProcess(ctx, j)
	if err := <-cancelled; err != nil {
		t.Fatal(err)
	}
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
